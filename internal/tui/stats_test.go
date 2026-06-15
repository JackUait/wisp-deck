package tui

import (
	"strings"
	"testing"

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
