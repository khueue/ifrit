// Package logsviewer is an interactive, tabbed viewer for the logs of several
// long-running processes.
package logsviewer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	// maxLines is the maximum number of log lines kept per tab.
	maxLines = 10000

	// readBufferSize is the size of the buffer each process is read through.
	// It also bounds how much output is delivered in one batch.
	readBufferSize = 64 * 1024

	// maxLineBytes is the longest log line kept in one piece.
	maxLineBytes = 1024 * 1024
)

// --- Color palette ----------------------------------------------------------

// groupColors is a palette of visually distinct ANSI 256 colors assigned to
// project groups so that tabs belonging to the same project share a color.
var groupColors = []color.Color{
	lipgloss.Color("215"), // orange
	lipgloss.Color("117"), // blue
	lipgloss.Color("156"), // green
	lipgloss.Color("212"), // pink
	lipgloss.Color("223"), // peach
	lipgloss.Color("153"), // light blue
	lipgloss.Color("180"), // tan
	lipgloss.Color("183"), // lavender
	lipgloss.Color("114"), // lime
	lipgloss.Color("210"), // salmon
}

// --- Styles ----------------------------------------------------------------

var (
	tabBarStyle = lipgloss.NewStyle().
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottomForeground(lipgloss.Color("240"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215")).
			Bold(true)

	unreadDotStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)
)

// activeStyle returns the tab style for the active tab with the given group color.
func activeStyle(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(c).
		Border(lipgloss.NormalBorder()).
		BorderBottom(false).
		BorderForeground(c).
		Padding(0, 2)
}

// inactiveStyle returns the tab style for an inactive tab with the given group color.
func inactiveStyle(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Border(lipgloss.NormalBorder()).
		BorderBottom(false).
		BorderForeground(c).
		Padding(0, 2)
}

// unreadStyle returns the tab style for an inactive tab that has unread lines.
func unreadStyle(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Border(lipgloss.NormalBorder()).
		BorderBottom(false).
		BorderForeground(c).
		Padding(0, 2)
}

// --- Messages ---------------------------------------------------------------

// logLinesMsg delivers a batch of log lines from a background process, along
// with the command that waits for the next batch.
type logLinesMsg struct {
	tab   int
	lines []string
	next  tea.Cmd
}

// processExitedMsg signals that a log-tailing process has exited.
type processExitedMsg struct {
	tab int
	err error
}

// --- Model ------------------------------------------------------------------

// tabData holds per-tab state.
type tabData struct {
	name      string
	group     string      // project group for color assignment
	color     color.Color // color derived from group
	lines     []string
	viewport  viewport.Model
	follow    bool // auto-scroll to bottom
	hasUnread bool // new lines arrived while tab was not active
}

// TabInfo describes a single tab to be created in the viewer.
type TabInfo struct {
	Name  string // display label (e.g. "backend/api")
	Group string // grouping key for color (e.g. "backend")
}

// Model is the top-level Bubble Tea model for the interactive logs viewer.
type Model struct {
	tabs     []tabData
	active   int
	width    int
	height   int
	ready    bool
	cmds     []*exec.Cmd
	readers  []*os.File // read-end of each pipe, kept for cleanup
	quitting bool
}

// CmdBuilder is a function that returns an *exec.Cmd for tailing logs of a
// given tab. The viewer calls this once per tab at startup. The argument is
// the tab's Name from TabInfo.
type CmdBuilder func(tabName string) (*exec.Cmd, error)

// New creates a new Model. It does NOT start the background processes yet –
// that happens in Init().
func New(tabInfos []TabInfo, builder CmdBuilder) (*Model, error) {
	// Assign a color to each unique group.
	groupColorMap := make(map[string]color.Color)
	colorIdx := 0
	for _, ti := range tabInfos {
		if _, ok := groupColorMap[ti.Group]; !ok {
			groupColorMap[ti.Group] = groupColors[colorIdx%len(groupColors)]
			colorIdx++
		}
	}

	m := &Model{
		tabs:    make([]tabData, len(tabInfos)),
		cmds:    make([]*exec.Cmd, len(tabInfos)),
		readers: make([]*os.File, len(tabInfos)),
	}

	for i, ti := range tabInfos {
		cmd, err := builder(ti.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to build log command for %s: %w", ti.Name, err)
		}
		m.cmds[i] = cmd

		vp := viewport.New()
		vp.MouseWheelEnabled = false
		vp.FillHeight = true

		m.tabs[i] = tabData{
			name:     ti.Name,
			group:    ti.Group,
			color:    groupColorMap[ti.Group],
			lines:    []string{},
			viewport: vp,
			follow:   true,
		}
	}

	return m, nil
}

// Init starts the log-tailing process of every tab.
func (m *Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, len(m.cmds))
	for i := range m.cmds {
		cmds[i] = m.startLogs(i)
	}
	return tea.Batch(cmds...)
}

// startLogs starts the tab's process and returns a tea.Cmd that streams its
// output into the Bubble Tea runtime via messages.
//
// The process is started right here rather than inside the returned command.
// Commands run on goroutines of their own, while killAll runs on the main
// one: starting up front means it never sees a half-started process, and
// never misses one that is yet to start.
//
// We use os.Pipe so that both stdout and stderr write to the same pipe writer.
// This avoids the io.MultiReader pitfall where stderr is never drained while
// stdout is still streaming, which can deadlock the child process.
func (m *Model) startLogs(tab int) tea.Cmd {
	exited := func(err error) tea.Cmd {
		return func() tea.Msg { return processExitedMsg{tab: tab, err: err} }
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return exited(fmt.Errorf("pipe: %w", err))
	}

	cmd := m.cmds[tab]
	cmd.Stdout = pw
	cmd.Stderr = pw
	err = cmd.Start()

	// Close the write-end in the parent process. The child holds its
	// own copy of the fd. When the child exits, the last writer goes
	// away and reads on pr will see EOF.
	pw.Close()

	if err != nil {
		pr.Close()
		return exited(err)
	}

	// Store the read-end so cleanup can close it if needed.
	m.readers[tab] = pr

	return readLogs(tab, cmd, bufio.NewReaderSize(pr, readBufferSize))
}

// readLogs returns a tea.Cmd that waits for the next batch of lines from the
// process, or for its exit.
func readLogs(tab int, cmd *exec.Cmd, r *bufio.Reader) tea.Cmd {
	return func() tea.Msg {
		lines, err := readBatch(r)
		if len(lines) > 0 {
			// Whatever cut the batch short comes up again on the next read.
			return logLinesMsg{tab: tab, lines: lines, next: readLogs(tab, cmd, r)}
		}

		// Normally the output ends because the process did. Should reading
		// fail for another reason, do not leave the process running with
		// nobody listening: it would block on a full pipe, and Wait with it.
		if !errors.Is(err, io.EOF) {
			_ = cmd.Process.Kill()
		}
		return processExitedMsg{tab: tab, err: cmd.Wait()}
	}
}

// readBatch blocks until a line can be read, then returns it along with all
// further lines that are already buffered. Delivering lines in batches keeps
// a chatty process from costing a full refresh of the log pane per line.
func readBatch(r *bufio.Reader) ([]string, error) {
	var lines []string
	for {
		line, err := readLine(r)
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)

		// Stop where reading the next line could block.
		if buffered, _ := r.Peek(r.Buffered()); !bytes.Contains(buffered, []byte{'\n'}) {
			return lines, nil
		}
	}
}

// readLine reads a single line, without its line ending. A line too long for
// the reader's buffer is pieced together up to maxLineBytes, and whatever
// follows becomes a line of its own. A bufio.Scanner would instead give up on
// such a line for good, leaving the process blocked on a pipe nobody reads.
func readLine(r *bufio.Reader) (string, error) {
	chunk, isPrefix, err := r.ReadLine()
	if err != nil || !isPrefix {
		return string(chunk), err
	}

	// chunk is only valid until the next read.
	line := slices.Clone(chunk)
	for isPrefix && len(line) < maxLineBytes {
		chunk, isPrefix, err = r.ReadLine()
		if err != nil {
			// Deliver what we have; the error comes up again on the next read.
			break
		}
		line = append(line, chunk...)
	}
	return string(line), nil
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			m.killAll()
			return m, tea.Quit
		case "tab", "right", "l":
			m.active = (m.active + 1) % len(m.tabs)
			m.tabs[m.active].hasUnread = false
			m.syncViewport()
		case "shift+tab", "left", "h":
			m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
			m.tabs[m.active].hasUnread = false
			m.syncViewport()
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0]-'0') - 1
			if idx < len(m.tabs) {
				m.active = idx
				m.tabs[m.active].hasUnread = false
				m.syncViewport()
			}
		case "G", "end":
			// Jump to bottom and re-enable follow.
			tab := &m.tabs[m.active]
			tab.follow = true
			tab.viewport.GotoBottom()
		case "g", "home":
			// Jump to top and disable follow.
			tab := &m.tabs[m.active]
			tab.follow = false
			tab.viewport.GotoTop()
		default:
			// Forward to viewport for scrolling (up/down/pgup/pgdn/etc).
			tab := &m.tabs[m.active]
			var cmd tea.Cmd
			tab.viewport, cmd = tab.viewport.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// If the user scrolled away from the bottom, disable follow.
			tab.follow = tab.viewport.AtBottom()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resizeViewports()

	case logLinesMsg:
		m.appendLines(msg.tab, msg.lines...)
		cmds = append(cmds, msg.next)

	case processExitedMsg:
		suffix := " [process exited]"
		if msg.err != nil {
			suffix = fmt.Sprintf(" [process exited: %v]", msg.err)
		}
		m.appendLines(msg.tab, suffix)
	}

	return m, tea.Batch(cmds...)
}

// appendLines adds lines to the tab and refreshes the viewport.
func (m *Model) appendLines(tab int, lines ...string) {
	if tab < 0 || tab >= len(m.tabs) {
		return
	}
	t := &m.tabs[tab]
	t.lines = append(t.lines, lines...)
	if excess := len(t.lines) - maxLines; excess > 0 {
		// Trim oldest lines.
		t.lines = slices.Delete(t.lines, 0, excess)

		// A view that is scrolled back should stay on the lines it shows,
		// not have them slide away underneath it.
		if !t.follow {
			t.viewport.SetYOffset(t.viewport.YOffset() - excess)
		}
	}

	if tab == m.active {
		m.syncViewport()
	} else {
		t.hasUnread = true
	}
}

// syncViewport updates the active tab's viewport content.
func (m *Model) syncViewport() {
	if !m.ready {
		return
	}
	tab := &m.tabs[m.active]
	content := strings.Join(tab.lines, "\n")
	tab.viewport.SetContent(content)
	if tab.follow {
		tab.viewport.GotoBottom()
	}
}

// resizeViewports fits all viewports to the current terminal size, keeping
// their scroll positions.
func (m *Model) resizeViewports() {
	height := m.viewportHeight()
	for i := range m.tabs {
		t := &m.tabs[i]
		t.viewport.SetWidth(m.width)
		t.viewport.SetHeight(height)
		if !t.follow {
			// A taller viewport may now reach past the end of the content.
			t.viewport.SetYOffset(t.viewport.YOffset())
		}
	}
	m.syncViewport()
}

// viewportHeight returns the usable viewport height after subtracting the
// tab bar and help line.
func (m *Model) viewportHeight() int {
	chrome := m.tabBarHeight() + 1 // + help line
	h := max(m.height-chrome, 1)
	return h
}

// truncateLabel shortens a tab label that would not fit the terminal width.
func truncateLabel(label string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(label)
	if len(runes) <= maxWidth {
		return label
	}
	if maxWidth == 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

// renderTab renders a single tab block: a top border row plus a label row.
//
// The unread-dot slot is always reserved, even when there is nothing to show,
// so that a tab's width does not change as lines arrive. Were widths to shift,
// tabs could repack into a different number of rows and the log pane would
// have to be resized mid-stream.
func (m *Model) renderTab(i int) string {
	t := m.tabs[i]

	prefix := ""
	if i < 9 {
		prefix = fmt.Sprintf("%d: ", i+1)
	}

	dot := "  "
	if t.hasUnread && i != m.active {
		dot = unreadDotStyle.Render("●") + " "
	}

	// Room left for the label: terminal width less this tab's borders (2),
	// horizontal padding (4), dot slot (2) and number prefix.
	label := truncateLabel(t.name, m.width-8-len(prefix))
	content := dot + prefix + label

	switch {
	case i == m.active:
		return activeStyle(t.color).Render(content)
	case t.hasUnread:
		return unreadStyle(t.color).Render(content)
	default:
		return inactiveStyle(t.color).Render(content)
	}
}

// tabRows packs tab indices into rows no wider than the terminal, so that
// every tab stays visible. Rendering the tabs as one over-wide row instead
// would let the styling wrap them and shear the border rows apart.
func (m *Model) tabRows() [][]int {
	rows := make([][]int, 0, len(m.tabs))

	var (
		row   []int
		width int
	)
	for i := range m.tabs {
		w := lipgloss.Width(m.renderTab(i))
		if len(row) > 0 && width+w > m.width {
			rows = append(rows, row)
			row, width = nil, 0
		}
		row = append(row, i)
		width += w
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// visibleTabRows returns the packed rows along with the range [start,end) that
// fits the terminal height. Every row is shown unless the terminal is too
// short to hold them all, in which case the rows are scrolled to keep the
// active tab visible.
func (m *Model) visibleTabRows() (rows [][]int, start, end int) {
	rows = m.tabRows()

	// Leave room for the bar's bottom border, at least one log line, and the
	// help line.
	maxRows := max((m.height-3)/2, 1)
	if len(rows) <= maxRows {
		return rows, 0, len(rows)
	}

	activeRow := 0
	for i, row := range rows {
		for _, tab := range row {
			if tab == m.active {
				activeRow = i
			}
		}
	}

	start = min(max(activeRow-maxRows/2, 0), len(rows)-maxRows)

	return rows, start, start + maxRows
}

// tabBarHeight returns the rendered height of the tab bar: two lines per row
// of tabs (top border plus label) and one for the bar's bottom border.
func (m *Model) tabBarHeight() int {
	_, start, end := m.visibleTabRows()
	return 2*(end-start) + 1
}

// renderTabBar renders the tabs, wrapped over as many rows as they need.
func (m *Model) renderTabBar() string {
	rows, start, end := m.visibleTabRows()

	lines := make([]string, 0, end-start)
	for _, row := range rows[start:end] {
		blocks := make([]string, 0, len(row))
		for _, i := range row {
			blocks = append(blocks, m.renderTab(i))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Bottom, blocks...))
	}

	// Truncate before styling: a terminal too narrow for even a single tab
	// would otherwise be wrapped by Width below, shearing the border rows.
	bar := lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(lines, "\n"))

	return tabBarStyle.Width(m.width).Render(bar)
}

// View renders the TUI. Alt screen is declared on the view itself in
// Bubble Tea v2 rather than passed as a program option.
func (m *Model) View() tea.View {
	view := tea.View{AltScreen: true}

	if m.quitting {
		return view
	}
	if !m.ready {
		view.Content = "\n  Initializing…"
		return view
	}

	// --- Tab bar ---
	tabBar := m.renderTabBar()

	// --- Viewport ---
	vp := m.tabs[m.active].viewport.View()

	// --- Help ---
	followIndicator := ""
	if m.tabs[m.active].follow {
		followIndicator = " │ " + titleStyle.Render("FOLLOWING")
	}
	help := helpStyle.Render("tab/←→: switch  ↑↓/pgup/pgdn: scroll  G: follow  g: top  esc/q: quit") + followIndicator

	// MaxHeight guards against a terminal too short for even one row of tabs.
	view.Content = lipgloss.NewStyle().
		MaxHeight(m.height).
		Render(tabBar + "\n" + vp + "\n" + help)

	return view
}

// killAll kills all background log processes and closes pipe readers so that
// any blocked scanner.Scan() calls unblock and return.
func (m *Model) killAll() {
	for i, cmd := range m.cmds {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if m.readers[i] != nil {
			_ = m.readers[i].Close()
		}
	}
}

// Run is a convenience function that creates a Bubble Tea program and runs
// the model. It blocks until the user quits.
func Run(tabInfos []TabInfo, builder CmdBuilder) error {
	if len(tabInfos) == 0 {
		return fmt.Errorf("no tabs to show logs for")
	}

	model, err := New(tabInfos, builder)
	if err != nil {
		return err
	}

	// However the program ends, leave no log processes behind.
	defer model.killAll()

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
