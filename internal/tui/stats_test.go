package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func twoMonths() []usage.MonthlyUsage {
	return []usage.MonthlyUsage{
		{Month: "2026-06", Input: 2_000_000, Output: 0, CacheWrite: 0, CacheRead: 0}, // total 2M
		{Month: "2026-05", Input: 1_000_000, Output: 0, CacheWrite: 0, CacheRead: 0}, // total 1M
	}
}

func TestStatsView_rendersMonthRowsHumanizedAndBars(t *testing.T) {
	view := NewStatsModelWithData(twoMonths()).View()

	if !strings.Contains(view, "Jun 2026") || !strings.Contains(view, "May 2026") {
		t.Errorf("view missing month labels:\n%s", view)
	}
	if !strings.Contains(view, "2.0M") || !strings.Contains(view, "1.0M") {
		t.Errorf("view missing humanized totals:\n%s", view)
	}
	// Each month's bar sits on the line directly below its data row and is scaled
	// to that month's share of all tokens, so June (2M of 3M) > May (1M of 3M).
	bar6 := barBlocksAfter(view, "Jun 2026")
	bar5 := barBlocksAfter(view, "May 2026")
	if bar6 <= bar5 || bar5 == 0 {
		t.Errorf("bar widths not proportional: jun=%d may=%d\n%s", bar6, bar5, view)
	}
}

func TestStatsView_monthRendersAsName(t *testing.T) {
	view := NewStatsModelWithData(twoMonths()).View()
	for _, want := range []string{"Jun 2026", "May 2026"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing human month label %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "2026-06") || strings.Contains(view, "2026-05") {
		t.Errorf("raw YYYY-MM month should be replaced by a name:\n%s", view)
	}
}

func TestStatsView_biggerGapBeforeTotal(t *testing.T) {
	// The Total column is set apart from the calculation columns by widening the
	// gap after Cache R from 1 to 3 spaces (7 visible spaces between the headers).
	view := stripANSI(NewStatsModelWithData(twoMonths()).View())
	gap := "Cache R" + strings.Repeat(" ", 9) + "Total"
	if !strings.Contains(view, gap) {
		t.Errorf("expected a wider gap %q before Total:\n%s", gap, view)
	}
}

func TestStatsView_isBoxedWithGhostBorder(t *testing.T) {
	view := NewStatsModelWithData(twoMonths()).View()
	for _, glyph := range []string{"╭", "╮", "╰", "╯", "│"} {
		if !strings.Contains(view, glyph) {
			t.Errorf("view missing box glyph %q (ghost-tab look):\n%s", glyph, view)
		}
	}
}

func TestStatsView_hasLabeledColumnHeader(t *testing.T) {
	view := NewStatsModelWithData(twoMonths()).View()
	for _, label := range []string{"Month", "Input", "Output", "Cache W", "Cache R", "Total"} {
		if !strings.Contains(view, label) {
			t.Errorf("view missing column header %q:\n%s", label, view)
		}
	}
}

func TestStatsView_showsGrandTotalRow(t *testing.T) {
	view := NewStatsModelWithData(twoMonths()).View()
	// Footer row labelled "Total" summing every month: 2M + 1M = 3.0M.
	found := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Total") && strings.Contains(line, "3.0M") {
			found = true
		}
	}
	if !found {
		t.Errorf("view missing grand-total row with 3.0M:\n%s", view)
	}
}

func TestStatsView_showsShareOfAllPercent(t *testing.T) {
	view := NewStatsModelWithData(twoMonths()).View()
	// June is 2M of 3M total ≈ 67%.
	if !strings.Contains(view, "67%") {
		t.Errorf("view missing share-of-all percentage (67%%):\n%s", view)
	}
}

func TestStatsView_gaugeHasTrack(t *testing.T) {
	// Months below 100% of all tokens render an empty track so the gauge reads as
	// a meter (filled portion + remaining track) rather than a loose bar.
	view := NewStatsModelWithData(twoMonths()).View()
	if !strings.Contains(view, "░") {
		t.Errorf("gauge should render an empty track glyph for sub-100%% months:\n%s", view)
	}
}

func TestStatsView_modelRowsUseTreeConnector(t *testing.T) {
	months := []usage.MonthlyUsage{{
		Month: "2026-06", Input: 2_000_000,
		Models: []usage.ModelUsage{
			{Model: "claude-opus-4-7", Input: 1_000_000},
			{Model: "claude-sonnet-4-6", Input: 1_000_000},
		},
	}}
	view := NewStatsModelWithData(months).View()
	if !strings.Contains(view, "├") {
		t.Errorf("intermediate model rows should use a ├ connector:\n%s", view)
	}
	if !strings.Contains(view, "└") {
		t.Errorf("the last model row should use a └ connector:\n%s", view)
	}
}

func TestStatsView_blankLineSeparatesMonths(t *testing.T) {
	// Each month block is set off by a blank spacer line so blocks don't cluster.
	view := NewStatsModelWithData(twoMonths()).View()
	lines := strings.Split(view, "\n")
	idx := -1
	for i, l := range lines {
		if strings.Contains(l, "May 2026") {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("could not find May 2026 row:\n%s", view)
	}
	inner := strings.ReplaceAll(lines[idx-1], "│", "")
	if strings.TrimSpace(inner) != "" {
		t.Errorf("expected a blank spacer line before the second month, got %q", lines[idx-1])
	}
}

func TestDollarFmt(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.42, "$0.42"},
		{312, "$312"},
		{1234, "$1,234"},
		{12345, "$12.3K"},
		{4_600_000, "$4.6M"},
	}
	for _, c := range cases {
		if got := dollarFmt(c.in); got != c.want {
			t.Errorf("dollarFmt(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatsView_showsModelRowsAndCost(t *testing.T) {
	months := []usage.MonthlyUsage{{
		Month: "2026-06", Input: 2_000_000, Output: 1_000_000,
		Models: []usage.ModelUsage{
			{Model: "claude-opus-4-7", Input: 2_000_000, Output: 1_000_000}, // $10 + $25 = $35
		},
	}}
	view := NewStatsModelWithData(months).View()
	if !strings.Contains(view, "opus-4-7") {
		t.Errorf("missing per-model row:\n%s", view)
	}
	if !strings.Contains(view, "$35") {
		t.Errorf("missing $35 cost (model/bar/total):\n%s", view)
	}
}

func TestStatsView_modelRowFitsBoxWidth(t *testing.T) {
	// A pathologically long model id must be truncated, not overflow the box and
	// shove the right border / money column out of alignment.
	months := []usage.MonthlyUsage{{
		Month: "2026-06", Input: 1000,
		Models: []usage.ModelUsage{
			{Model: "claude-some-absurdly-long-experimental-model-id-2026-xyz", Input: 1000},
		},
	}}
	view := NewStatsModelWithData(months).View()
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "│") {
			continue
		}
		if w := lipgloss.Width(line); w != statsInner+2 {
			t.Errorf("box line width = %d, want %d (inner+2 borders): %q", w, statsInner+2, line)
		}
	}
}

func TestStatsView_unpricedModelShowsDash(t *testing.T) {
	months := []usage.MonthlyUsage{{
		Month: "2026-06", Input: 100, Models: []usage.ModelUsage{
			{Model: "mystery-model", Input: 100},
		},
	}}
	view := NewStatsModelWithData(months).View()
	if !strings.Contains(view, "—") {
		t.Errorf("unpriced model should render — for cost:\n%s", view)
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

// barBlocksAfter counts '█' runes on the line immediately following the line that
// contains label — i.e. a month's bar row, which sits below its data row.
func barBlocksAfter(view, label string) int {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, label) && i+1 < len(lines) {
			return strings.Count(lines[i+1], "█")
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

func TestStatsUpdate_storesWindowSize(t *testing.T) {
	m := NewStatsModelWithData(twoMonths())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	sm := updated.(StatsModel)
	if sm.width != 120 || sm.height != 50 {
		t.Errorf("size = %dx%d, want 120x50", sm.width, sm.height)
	}
}

func TestStatsView_centeredWithinWindow(t *testing.T) {
	m := NewStatsModelWithData(twoMonths())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	lines := strings.Split(updated.(StatsModel).View(), "\n")

	// Vertical centering leaves blank padding rows above the box.
	if strings.TrimSpace(lines[0]) != "" {
		t.Errorf("expected blank top padding (vertical centering), got %q", lines[0])
	}
	// Horizontal centering indents the box; its top border is not at column 0.
	var top string
	for _, l := range lines {
		if strings.Contains(l, "╭") {
			top = l
			break
		}
	}
	if !strings.HasPrefix(top, " ") {
		t.Errorf("expected left padding before box (horizontal centering), got %q", top)
	}
}

func TestStatsView_notCenteredWithoutSize(t *testing.T) {
	// width/height unset (0): render the box at the origin so non-sized callers
	// and tests still see raw content.
	view := NewStatsModelWithData(twoMonths()).View()
	if !strings.HasPrefix(view, "╭") {
		t.Errorf("unsized view should start at the box border, got %q", strings.SplitN(view, "\n", 2)[0])
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
