package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/usage"
)

func TestHumanizeTokens(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{12345, "12.3K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{2000000000, "2.0B"},
	}
	for _, tt := range tests {
		if got := humanizeTokens(tt.in); got != tt.want {
			t.Errorf("humanizeTokens(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStatsView_rendersMonthRowsHumanizedAndBars(t *testing.T) {
	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 2_000_000, Output: 0, CacheWrite: 0, CacheRead: 0}, // total 2M (max)
		{Month: "2026-05", Input: 1_000_000, Output: 0, CacheWrite: 0, CacheRead: 0}, // total 1M (half)
	}
	m := NewStatsModelWithData(months)
	view := m.View()

	if !strings.Contains(view, "2026-06") || !strings.Contains(view, "2026-05") {
		t.Errorf("view missing month labels:\n%s", view)
	}
	if !strings.Contains(view, "2.0M") || !strings.Contains(view, "1.0M") {
		t.Errorf("view missing humanized totals:\n%s", view)
	}
	bar6 := countBarRunes(view, "2026-06")
	bar5 := countBarRunes(view, "2026-05")
	if bar6 <= bar5 || bar5 == 0 {
		t.Errorf("bar widths not proportional: jun=%d may=%d", bar6, bar5)
	}
}

func TestStatsView_emptyShowsFriendlyMessage(t *testing.T) {
	m := NewStatsModelWithData([]usage.MonthlyUsage{})
	if !strings.Contains(m.View(), "No usage data") {
		t.Errorf("empty view should show 'No usage data', got:\n%s", m.View())
	}
}

func TestStatsView_errorShown(t *testing.T) {
	m := NewStatsModelWithData(nil)
	m.err = errTestStats
	if !strings.Contains(m.View(), "Failed") {
		t.Errorf("error view should mention failure, got:\n%s", m.View())
	}
}

// countBarRunes returns how many '█' runes appear on the line containing label.
func countBarRunes(view, label string) int {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, label) {
			return strings.Count(line, "█")
		}
	}
	return 0
}

var errTestStats = stubErr("boom")

type stubErr string

func (e stubErr) Error() string { return string(e) }

func threeMonths() []usage.MonthlyUsage {
	return []usage.MonthlyUsage{
		{Month: "2026-06", Input: 3}, {Month: "2026-05", Input: 2}, {Month: "2026-04", Input: 1},
	}
}

func TestStatsUpdate_scrollClampsAtBothEnds(t *testing.T) {
	// With 3 months and a window of 8, every row fits — there is no scrolling.
	m := NewStatsModelWithData(threeMonths())
	// Up at top stays at 0.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.(StatsModel).offset != 0 {
		t.Errorf("offset after up-at-top = %d, want 0", updated.(StatsModel).offset)
	}
	// Down does not scroll because everything already fits.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.(StatsModel).offset != 0 {
		t.Errorf("offset after down (all rows fit) = %d, want 0", updated.(StatsModel).offset)
	}
	// Down repeatedly still stays at 0.
	m2 := updated.(StatsModel)
	for i := 0; i < 10; i++ {
		u, _ := m2.Update(tea.KeyMsg{Type: tea.KeyDown})
		m2 = u.(StatsModel)
	}
	if m2.offset != 0 {
		t.Errorf("offset after repeated down = %d, want 0", m2.offset)
	}
}

func TestStatsUpdate_scrollStopsAtFullWindow(t *testing.T) {
	months := make([]usage.MonthlyUsage, 12)
	for i := range months {
		months[i] = usage.MonthlyUsage{Month: "2026-01", Input: int64(i + 1)}
	}
	m := NewStatsModelWithData(months)
	for i := 0; i < 50; i++ {
		u, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = u.(StatsModel)
	}
	// Max offset must leave a full window visible: len-statsWindow = 12-8 = 4.
	if m.offset != 4 {
		t.Errorf("max offset = %d, want 4 (len-statsWindow)", m.offset)
	}
}

func TestStatsUpdate_escEmitsPopScreen(t *testing.T) {
	m := NewStatsModelWithData(threeMonths())
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc returned nil cmd, want PopScreenMsg cmd")
	}
	if _, ok := cmd().(PopScreenMsg); !ok {
		t.Errorf("esc cmd = %T, want PopScreenMsg", cmd())
	}
}

func TestStatsUpdate_loadedMsgPopulatesAndStopsLoading(t *testing.T) {
	m := StatsModel{loading: true}
	updated, _ := m.Update(statsLoadedMsg{months: threeMonths()})
	sm := updated.(StatsModel)
	if sm.loading || len(sm.months) != 3 {
		t.Errorf("after load: loading=%v months=%d, want false/3", sm.loading, len(sm.months))
	}
}

func TestStatsUpdate_errMsgSetsError(t *testing.T) {
	m := StatsModel{loading: true}
	updated, _ := m.Update(statsErrMsg{err: errTestStats})
	sm := updated.(StatsModel)
	if sm.err == nil || sm.loading {
		t.Errorf("after err: err=%v loading=%v, want set/false", sm.err, sm.loading)
	}
}
