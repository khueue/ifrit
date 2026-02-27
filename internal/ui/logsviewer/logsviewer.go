package logsviewer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// maxLines is the maximum number of log lines kept per tab.
const maxLines = 10000

// --- Styles ----------------------------------------------------------------

var (
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("215")).
			Border(lipgloss.NormalBorder()).
			BorderBottom(false).
			BorderForeground(lipgloss.Color("215")).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Border(lipgloss.NormalBorder()).
				BorderBottom(false).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 2)

	unreadTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Border(lipgloss.NormalBorder()).
			BorderBottom(false).
			BorderForeground(lipgloss.Color("230")).
			Padding(0, 2)

	unreadDotStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)

	tabBarStyle = lipgloss.NewStyle().
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottomForeground(lipgloss.Color("240"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215")).
			Bold(true)
)

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
	lines     []string
	viewport  viewport.Model
	follow    bool // auto-scroll to bottom
	hasUnread bool // new lines arrived while tab was not active
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
// given project. The viewer calls this once per tab at startup.
type CmdBuilder func(projectName string) (*exec.Cmd, error)

// New creates a new Model. It does NOT start the background processes yet –
// that happens in Init().
func New(projectNames []string, builder CmdBuilder) (*Model, error) {
	m := &Model{
		tabs:    make([]tabData, len(projectNames)),
		cmds:    make([]*exec.Cmd, len(projectNames)),
		readers: make([]*os.File, len(projectNames)),
	}

	for i, name := range projectNames {
		cmd, err := builder(name)
		if err != nil {
			return nil, fmt.Errorf("failed to build log command for %s: %w", name, err)
		}
		m.cmds[i] = cmd
		m.tabs[i] = tabData{
			name:   name,
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
				logLineMsg: logLineMsg{tab: tab, line: line},
				next:       continueScanning(tab, scanner, cmd, pr),
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
				logLineMsg: logLineMsg{tab: tab, line: line},
				next:       continueScanning(tab, scanner, cmd, pr),
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
	case tea.KeyMsg:
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
		m.tabs[i].viewport = viewport.New(vpWidth, vpHeight)
		m.tabs[i].viewport.MouseWheelEnabled = false
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
	// 3 lines for tab bar (border top + content + border bottom) + 1 help line.
	chrome := 4
	h := max(m.height-chrome, 1)
	return h
}

// View renders the TUI.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "\n  Initializing…"
	}

	// --- Tab bar ---
	var tabs []string
	for i, t := range m.tabs {
		label := t.name
		if i < 9 {
			label = fmt.Sprintf("%d:%s", i+1, label)
		}
		if i == m.active {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else if t.hasUnread {
			label = unreadDotStyle.Render("●") + " " + label
			tabs = append(tabs, unreadTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}
	tabBar := tabBarStyle.Width(m.width).Render(lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...))

	// --- Viewport ---
	vp := m.tabs[m.active].viewport.View()

	// --- Help ---
	followIndicator := ""
	if m.tabs[m.active].follow {
		followIndicator = " │ " + titleStyle.Render("FOLLOWING")
	}
	help := helpStyle.Render("tab/←→: switch  ↑↓/pgup/pgdn: scroll  G: follow  g: top  esc/q: quit") + followIndicator

	return tabBar + "\n" + vp + "\n" + help
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
func Run(projectNames []string, builder CmdBuilder) error {
	if len(projectNames) == 0 {
		return fmt.Errorf("no projects to show logs for")
	}

	model, err := New(projectNames, builder)
	if err != nil {
		return err
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
