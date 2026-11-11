package logsviewer

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/khueue/ifrit/internal/check"
)

var (
	keyPgUp     = tea.KeyPressMsg{Code: tea.KeyPgUp}
	keyTab      = tea.KeyPressMsg{Code: tea.KeyTab}
	keyShiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
)

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

// press sends the key to the model the given number of times.
func press(m *Model, key tea.KeyPressMsg, times int) {
	for range times {
		m.Update(key)
	}
}

// scrolledBack returns a model whose first tab holds n lines and has been
// paged up, so it no longer follows.
func scrolledBack(t *testing.T, n int) *Model {
	t.Helper()
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(logLinesMsg{tab: 0, lines: numbered(n)})
	press(m, keyPgUp, 3)

	if m.tabs[0].follow || m.tabs[0].viewport.YOffset() == 0 {
		t.Fatal("expected a scroll position in the middle of the log, not following")
	}
	return m
}

// A whole batch must cost one refresh, however many lines it holds. Feeding
// lines one by one used to be quadratic: 10k lines took minutes.
func TestLargeBatchesAreFast(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	start := time.Now()
	for range 50 {
		m.Update(logLinesMsg{tab: 0, lines: numbered(500)})
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("25000 lines in 50 batches took %v", elapsed)
	}

	check.Equal(t, "lines kept", len(m.tabs[0].lines), maxLines)
	check.Equal(t, "newest line", m.tabs[0].lines[maxLines-1], "line 499")
	check.Contains(t, "following view", m.View().Content, "line 499")
}

func TestResizeKeepsScrollPosition(t *testing.T) {
	m := scrolledBack(t, 200)
	vp := &m.tabs[0].viewport
	before := vp.YOffset()

	m.Update(tea.WindowSizeMsg{Width: 90, Height: 20})
	check.Equal(t, "offset after resize", vp.YOffset(), before)
	check.Equal(t, "viewport width", vp.Width(), 90)

	// Growing far taller than the log leaves nothing to scroll.
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 500})
	check.Equal(t, "offset after growing past the content", vp.YOffset(), 0)
}

func TestResizeKeepsFollowing(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(logLinesMsg{tab: 0, lines: numbered(200)})

	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	check.Equal(t, "at bottom", m.tabs[0].viewport.AtBottom(), true)
	check.Contains(t, "view", m.View().Content, "line 199")
}

// Once the buffer is full, old lines are dropped as new ones arrive. A view
// that is scrolled back must keep showing the same lines regardless.
func TestTrimKeepsScrolledBackViewInPlace(t *testing.T) {
	for _, inBackground := range []bool{false, true} {
		t.Run(fmt.Sprintf("in background: %v", inBackground), func(t *testing.T) {
			m := scrolledBack(t, maxLines)
			tab := &m.tabs[0]
			want := tab.lines[tab.viewport.YOffset()]

			if inBackground {
				m.Update(keyTab)
				defer m.Update(keyShiftTab)
			}
			m.Update(logLinesMsg{tab: 0, lines: []string{"new 1", "new 2", "new 3"}})
			m.Update(logLinesMsg{tab: 0, lines: []string{"new 4"}})

			check.Equal(t, "lines kept", len(tab.lines), maxLines)
			check.Equal(t, "line at top of view", tab.lines[tab.viewport.YOffset()], want)
		})
	}
}

func TestKeysBeforeFirstResize(t *testing.T) {
	m := newTestModel(t)
	for _, k := range []tea.KeyPressMsg{
		{Code: 'G', Text: "G"},
		{Code: 'g', Text: "g"},
		{Code: tea.KeyDown},
		keyPgUp,
		keyTab,
		{Code: '2', Text: "2"},
		{Code: '9', Text: "9"}, // no such tab
	} {
		m.Update(k)
	}
	m.Update(logLinesMsg{tab: 0, lines: []string{"early"}})
	m.Update(logLinesMsg{tab: 7, lines: []string{"no such tab"}})

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	check.Contains(t, "view", m.View().Content, "early")
}

func TestTabNavigationWraps(t *testing.T) {
	m := nineTabModel(t, 100, 30)

	m.Update(keyShiftTab)
	check.Equal(t, "active after shift+tab on the first tab", m.active, 8)
	m.Update(keyTab)
	check.Equal(t, "active after tab on the last tab", m.active, 0)

	for _, k := range []tea.KeyPressMsg{{Code: 'l', Text: "l"}, {Code: tea.KeyRight}, {Code: 'h', Text: "h"}, {Code: tea.KeyLeft}} {
		m.Update(k)
	}
	check.Equal(t, "active after l, right, h, left", m.active, 0)
}

func TestTruncateLabel(t *testing.T) {
	tests := []struct {
		label string
		max   int
		want  string
	}{
		{"backend/api", 20, "backend/api"},
		{"backend/api", 11, "backend/api"},
		{"backend/api", 8, "backend…"},
		{"backend/api", 1, "…"},
		{"backend/api", 0, ""},
		{"backend/api", -3, ""},
		{"bäckend/åpi", 8, "bäckend…"},
	}
	for _, tt := range tests {
		check.Equal(t, fmt.Sprintf("truncateLabel(%q, %d)", tt.label, tt.max), truncateLabel(tt.label, tt.max), tt.want)
	}
}
