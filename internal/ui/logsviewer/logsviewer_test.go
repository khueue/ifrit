package logsviewer

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	m, err := New([]TabInfo{
		{Name: "backend/api", Group: "backend"},
		{Name: "frontend/web", Group: "frontend"},
	}, func(name string) (*exec.Cmd, error) { return exec.Command("true"), nil })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestModelFlow(t *testing.T) {
	m := newTestModel(t)

	// Before any window size: init screen.
	if got := m.View().Content; !strings.Contains(got, "Initializing") {
		t.Errorf("pre-ready view = %q", got)
	}
	if !m.View().AltScreen {
		t.Error("AltScreen not set on view")
	}

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})

	m.Update(logLineMsg{tab: 0, line: "backend line one"})
	m.Update(logLineMsg{tab: 1, line: "frontend line one"})

	v := m.View().Content
	if !strings.Contains(v, "backend line one") {
		t.Errorf("active tab content missing; view:\n%s", v)
	}
	if !strings.Contains(v, "1: backend/api") || !strings.Contains(v, "2: frontend/web") {
		t.Errorf("tab bar wrong; view:\n%s", v)
	}
	if !strings.Contains(v, "●") {
		t.Error("expected unread dot on inactive tab with new lines")
	}
	if !strings.Contains(v, "FOLLOWING") {
		t.Error("expected follow indicator")
	}

	// Switch to tab 2 and confirm its content is now shown.
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.active != 1 {
		t.Fatalf("tab switch failed, active = %d", m.active)
	}
	v = m.View().Content
	if !strings.Contains(v, "frontend line one") {
		t.Errorf("switched-tab content missing; view:\n%s", v)
	}
	if strings.Contains(v, "backend line one") {
		t.Errorf("stale content from previous tab; view:\n%s", v)
	}

	// Number keys, top/bottom, and quit.
	m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	if m.active != 0 {
		t.Errorf("number-key switch failed, active = %d", m.active)
	}
	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if m.tabs[0].follow {
		t.Error("g should disable follow")
	}
	m.Update(tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift})
	if !m.tabs[0].follow {
		t.Error("G should re-enable follow")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil {
		t.Error("q should return a quit cmd")
	}
	if got := m.View().Content; got != "" {
		t.Errorf("quitting view should be empty, got %q", got)
	}

	// Viewport height must account for chrome (3 tab-bar rows + 1 help row).
	if h := m.tabs[0].viewport.Height(); h != 16 {
		t.Errorf("viewport height = %d, want 16", h)
	}
}

func nineTabModel(t *testing.T, width, height int) *Model {
	t.Helper()
	names := []string{
		"api/dev", "api-playwright/playwright-worker", "app/dev", "docs/dev",
		"infra/dev", "jobs/dev", "mail/dev", "memory/dev", "shared-firebase/dev",
	}
	tabs := make([]TabInfo, len(names))
	for i, n := range names {
		group, _, _ := strings.Cut(n, "/")
		tabs[i] = TabInfo{Name: n, Group: group}
	}
	m, err := New(tabs, func(string) (*exec.Cmd, error) { return exec.Command("true"), nil })
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// The tab bar wraps onto as many rows as it needs, and no row may exceed the
// terminal width -- otherwise the styling wraps it and shears the borders.
func TestTabBarWrapsIntoRows(t *testing.T) {
	for _, width := range []int{20, 40, 80, 100, 120, 200, 400} {
		for active := range 9 {
			m := nineTabModel(t, width, 40)
			m.active = active

			lines := strings.Split(m.renderTabBar(), "\n")
			if got, want := len(lines), m.tabBarHeight(); got != want {
				t.Errorf("width=%d active=%d: bar is %d lines, tabBarHeight says %d",
					width, active, got, want)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("width=%d active=%d: line %d is %d cols wide", width, active, i, w)
				}
			}
		}
	}
}

// Every tab must be reachable on screen, not scrolled out of sight.
func TestAllTabsVisible(t *testing.T) {
	for _, width := range []int{40, 80, 130, 400} {
		m := nineTabModel(t, width, 40)
		bar := m.renderTabBar()
		for i := range m.tabs {
			if label := fmt.Sprintf("%d:", i+1); !strings.Contains(bar, label) {
				t.Errorf("width=%d: tab %q missing from bar:\n%s", width, label, bar)
			}
		}
	}
}

// A tab's width must not change as log lines arrive, or the tabs would repack
// into a different number of rows and resize the log pane mid-stream.
func TestTabWidthStableAcrossUnread(t *testing.T) {
	m := nineTabModel(t, 130, 40)
	before := lipgloss.Width(m.renderTab(8))
	rowsBefore := len(m.tabRows())

	m.Update(logLineMsg{tab: 8, line: "firebase ready"})
	if !m.tabs[8].hasUnread {
		t.Fatal("expected unread flag")
	}
	if after := lipgloss.Width(m.renderTab(8)); after != before {
		t.Errorf("tab width changed with unread dot: %d -> %d", before, after)
	}
	if rows := len(m.tabRows()); rows != rowsBefore {
		t.Errorf("row count changed with unread dot: %d -> %d", rowsBefore, rows)
	}
}

// The whole view must fill the terminal exactly, so the log pane is neither
// squashed nor pushed off screen by a multi-row tab bar.
func TestViewFitsHeight(t *testing.T) {
	for _, width := range []int{20, 40, 80, 130} {
		for _, height := range []int{4, 6, 12, 20, 40} {
			m := nineTabModel(t, width, height)
			m.Update(logLineMsg{tab: 0, line: "dev-1  | Ready!"})
			if rows := strings.Split(m.View().Content, "\n"); len(rows) != height {
				t.Errorf("width=%d height=%d: view is %d rows, want %d",
					width, height, len(rows), height)
			}
		}
	}
}
