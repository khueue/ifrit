package logsviewer

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/khueue/ifrit/internal/check"
)

// trueModel returns a model with a tab per name, none of them sized yet.
func trueModel(t *testing.T, names ...string) *Model {
	t.Helper()
	tabs := make([]TabInfo, len(names))
	for i, n := range names {
		group, _, _ := strings.Cut(n, "/")
		tabs[i] = TabInfo{Name: n, Group: group}
	}
	m, err := New(tabs, func(string) (*exec.Cmd, error) { return exec.Command("true"), nil })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newTestModel(t *testing.T) *Model {
	t.Helper()
	return trueModel(t, "backend/api", "frontend/web")
}

// readyModel returns a two-tab model sized 100x20, with a line in each tab.
func readyModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(logLinesMsg{tab: 0, lines: []string{"backend line one"}})
	m.Update(logLinesMsg{tab: 1, lines: []string{"frontend line one"}})
	return m
}

func TestViewBeforeFirstResize(t *testing.T) {
	m := newTestModel(t)
	check.Contains(t, "pre-ready view", m.View().Content, "Initializing")
	check.Equal(t, "AltScreen", m.View().AltScreen, true)
}

func TestViewShowsActiveTab(t *testing.T) {
	m := readyModel(t)

	check.Contains(t, "view", m.View().Content,
		"backend line one",                  // content of the active tab
		"1: backend/api", "2: frontend/web", // tab bar
		"●",         // unread dot on the inactive tab with new lines
		"FOLLOWING", // follow indicator
	)

	// Viewport height must account for chrome (3 tab-bar rows + 1 help row).
	check.Equal(t, "viewport height", m.tabs[0].viewport.Height(), 16)
}

func TestSwitchingTabs(t *testing.T) {
	m := readyModel(t)

	m.Update(keyTab)
	check.Equal(t, "active after tab", m.active, 1)

	v := m.View().Content
	check.Contains(t, "view", v, "frontend line one")
	check.Equal(t, "stale content from previous tab", strings.Contains(v, "backend line one"), false)

	m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	check.Equal(t, "active after number key", m.active, 0)
}

func TestFollowKeys(t *testing.T) {
	m := readyModel(t)

	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	check.Equal(t, "follow after g", m.tabs[0].follow, false)

	m.Update(tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift})
	check.Equal(t, "follow after G", m.tabs[0].follow, true)
}

func TestQuit(t *testing.T) {
	m := readyModel(t)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	check.Equal(t, "q returns a quit cmd", cmd != nil, true)
	check.Equal(t, "quitting view", m.View().Content, "")
}

func nineTabModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m := trueModel(t,
		"api/dev", "api-playwright/playwright-worker", "app/dev", "docs/dev",
		"infra/dev", "jobs/dev", "mail/dev", "memory/dev", "shared-firebase/dev",
	)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// checkTabBar verifies that the bar is as tall as tabBarHeight says, and that
// none of its lines is wider than the terminal.
func checkTabBar(t *testing.T, m *Model) {
	t.Helper()
	lines := strings.Split(m.renderTabBar(), "\n")
	check.Equal(t, "tab bar lines", len(lines), m.tabBarHeight())
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("line %d is %d cols wide, terminal is %d", i, w, m.width)
		}
	}
}

// The tab bar wraps onto as many rows as it needs, and no row may exceed the
// terminal width -- otherwise the styling wraps it and shears the borders.
func TestTabBarWrapsIntoRows(t *testing.T) {
	for _, width := range []int{20, 40, 80, 100, 120, 200, 400} {
		for active := range 9 {
			t.Run(fmt.Sprintf("width=%d active=%d", width, active), func(t *testing.T) {
				m := nineTabModel(t, width, 40)
				m.active = active
				checkTabBar(t, m)
			})
		}
	}
}

// Every tab must be reachable on screen, not scrolled out of sight.
func TestAllTabsVisible(t *testing.T) {
	labels := make([]string, 9)
	for i := range labels {
		labels[i] = fmt.Sprintf("%d:", i+1)
	}
	for _, width := range []int{40, 80, 130, 400} {
		m := nineTabModel(t, width, 40)
		check.Contains(t, fmt.Sprintf("tab bar at width %d", width), m.renderTabBar(), labels...)
	}
}

// A tab's width must not change as log lines arrive, or the tabs would repack
// into a different number of rows and resize the log pane mid-stream.
func TestTabWidthStableAcrossUnread(t *testing.T) {
	m := nineTabModel(t, 130, 40)
	width := lipgloss.Width(m.renderTab(8))
	rows := len(m.tabRows())

	m.Update(logLinesMsg{tab: 8, lines: []string{"firebase ready"}})

	check.Equal(t, "unread flag", m.tabs[8].hasUnread, true)
	check.Equal(t, "tab width with unread dot", lipgloss.Width(m.renderTab(8)), width)
	check.Equal(t, "row count with unread dot", len(m.tabRows()), rows)
}

// The whole view must fill the terminal exactly, so the log pane is neither
// squashed nor pushed off screen by a multi-row tab bar.
func TestViewFitsHeight(t *testing.T) {
	for _, width := range []int{20, 40, 80, 130} {
		for _, height := range []int{4, 6, 12, 20, 40} {
			m := nineTabModel(t, width, height)
			m.Update(logLinesMsg{tab: 0, lines: []string{"dev-1  | Ready!"}})

			rows := strings.Split(m.View().Content, "\n")
			check.Equal(t, fmt.Sprintf("view rows at %dx%d", width, height), len(rows), height)
		}
	}
}
