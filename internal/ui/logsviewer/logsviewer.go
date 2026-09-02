package logsviewer

import (
	"bufio"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// maxLines is the maximum number of log lines kept per tab.
const maxLines = 10000

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

// logLineMsg delivers a single log line from a background process.
type logLineMsg struct {
	tab  int
	line string
}

// processExitedMsg signals that a log-tailing process has exited.
type processExitedMsg struct {
	tab int
	err error
}

// logLineAndContinue wraps a log line plus a Cmd to read the next one.
type logLineAndContinue struct {
	logLineMsg
	next tea.Cmd
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
		m.tabs[i] = tabData{
			name:   ti.Name,
			group:  ti.Group,
			color:  groupColorMap[ti.Group],
			lines:  []string{},
			follow: true,
		}
	}

	return m, nil
}

// Init starts background log-tailing goroutines for every tab.
func (m *Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.cmds))
	for i, cmd := range m.cmds {
		cmds = append(cmds, m.tailLogs(i, cmd))
	}
	return tea.Batch(cmds...)
}

// tailLogs starts the process and returns a tea.Cmd that streams lines into
// the Bubble Tea runtime via messages.
//
// We use os.Pipe so that both stdout and stderr write to the same pipe writer.
// This avoids the io.MultiReader pitfall where stderr is never drained while
// stdout is still streaming, which can deadlock the child process.
func (m *Model) tailLogs(tab int, cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		pr, pw, err := os.Pipe()
		if err != nil {
			return processExitedMsg{tab: tab, err: fmt.Errorf("pipe: %w", err)}
		}

		cmd.Stdout = pw
		cmd.Stderr = pw

		if err := cmd.Start(); err != nil {
			pw.Close()
			pr.Close()
			return processExitedMsg{tab: tab, err: err}
		}

		// Close the write-end in the parent process. The child holds its
		// own copy of the fd. When the child exits, the last writer goes
		// away and reads on pr will see EOF.
		pw.Close()

		// Store the read-end so cleanup can close it if needed.
		m.readers[tab] = pr

		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		if scanner.Scan() {
			line := scanner.Text()
			return logLineAndContinue{
				tab: tab, line: line,
				next: continueScanning(tab, scanner, cmd, pr),
			}
		}

		// Nothing read at all — process probably exited immediately.
		waitErr := cmd.Wait()
		pr.Close()
		return processExitedMsg{tab: tab, err: waitErr}
	}
}

// continueScanning returns a tea.Cmd that reads the next line from the scanner.
func continueScanning(tab int, scanner *bufio.Scanner, cmd *exec.Cmd, pr *os.File) tea.Cmd {
	return func() tea.Msg {
		if scanner.Scan() {
			line := scanner.Text()
			return logLineAndContinue{
				tab: tab, line: line,
				next: continueScanning(tab, scanner, cmd, pr),
			}
		}
		waitErr := cmd.Wait()
		pr.Close()
		return processExitedMsg{tab: tab, err: waitErr}
	}
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
		m.initViewports()

	case logLineAndContinue:
		m.appendLine(msg.tab, msg.line)
		cmds = append(cmds, msg.next)

	case logLineMsg:
		m.appendLine(msg.tab, msg.line)

	case processExitedMsg:
		suffix := " [process exited]"
		if msg.err != nil {
			suffix = fmt.Sprintf(" [process exited: %v]", msg.err)
		}
		m.appendLine(msg.tab, suffix)
	}

	return m, tea.Batch(cmds...)
}

// appendLine adds a line to the tab and refreshes the viewport.
func (m *Model) appendLine(tab int, line string) {
	if tab < 0 || tab >= len(m.tabs) {
		return
	}
	t := &m.tabs[tab]
	t.lines = append(t.lines, line)
	if len(t.lines) > maxLines {
		// Trim oldest lines.
		t.lines = t.lines[len(t.lines)-maxLines:]
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

// initViewports (re-)initializes all viewports to the current terminal size.
func (m *Model) initViewports() {
	vpHeight := m.viewportHeight()
	vpWidth := m.width
	for i := range m.tabs {
		m.tabs[i].viewport = viewport.New(
			viewport.WithWidth(vpWidth),
			viewport.WithHeight(vpHeight),
		)
		m.tabs[i].viewport.MouseWheelEnabled = false
		m.tabs[i].viewport.FillHeight = true
		content := strings.Join(m.tabs[i].lines, "\n")
		m.tabs[i].viewport.SetContent(content)
		if m.tabs[i].follow {
			m.tabs[i].viewport.GotoBottom()
		}
	}
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

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
