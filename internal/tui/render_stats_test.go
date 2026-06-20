package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/usage"
)

func TestRenderStatsBox_hasTabBar(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()
	if !strings.Contains(out, "Stats") {
		t.Errorf("stats box missing tab bar: %q", out)
	}
}

func TestRenderStatsBox_statsTabAccented(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()
	// Active tab renders bold + underlined (no ▌Stats▐ glyph artifact).
	want := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true).Underline(true).Render(" Stats ")
	if !strings.Contains(out, want) {
		t.Errorf("active Stats tab should be bold+underlined, got:\n%s", out)
	}
}

func TestRenderStatsBox_hasChromeStructure(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()
	for _, glyph := range []string{"╭", "╮", "╰", "╯", "│"} {
		if !strings.Contains(out, glyph) {
			t.Errorf("stats box missing border glyph %q:\n%s", glyph, out)
		}
	}
}

// TestRenderStatsBox_SetSizeHonored is the carried-forward size test from Task 2.
// It verifies that SetSize is honored — the output is non-empty, contains "Stats"
// (from the tab bar), and renders within menuContentWidth.
func TestRenderStatsBox_SetSizeHonored(t *testing.T) {
	const w, h = 120, 40
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSize(w, h)
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()

	if out == "" {
		t.Fatal("renderStatsBox returned empty string after SetSize")
	}
	if !strings.Contains(out, "Stats") {
		t.Errorf("output missing 'Stats' (tab bar not rendered): %q", out)
	}

	// Each box line must not exceed the box width (menuInnerWidth + 2 borders).
	maxAllowed := menuInnerWidth + 2
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Only check lines that are box rows (contain border glyphs).
		if !strings.Contains(line, "│") {
			continue
		}
		if w := visibleWidth(line); w > maxAllowed {
			t.Errorf("box line exceeds menuInnerWidth+2 (%d): width=%d %q", maxAllowed, w, line)
		}
	}
}

// TestRenderStatsBox_dataRowsFitBoxWidth renders the stats table WITH data (the
// full Month/Input/Output/Cache W/Cache R/Total header + data rows) and verifies
// no box line spills past the right border. The empty-state width test above
// never exercises the wide table, so the overflow only shows up with real data.
func TestRenderStatsBox_dataRowsFitBoxWidth(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)

	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 34_400_000, Output: 30_800_000, CacheWrite: 252_300_000, CacheRead: 6_000_000_000,
			Models: []usage.ModelUsage{
				{Model: "claude-opus-4-8", Input: 30_000_000, Output: 28_000_000, CacheWrite: 200_000_000, CacheRead: 5_000_000_000},
				{Model: "claude-haiku-4-5", Input: 4_400_000, Output: 2_800_000, CacheWrite: 52_300_000, CacheRead: 1_000_000_000},
			}},
		{Month: "2026-05", Input: 7_000_000, Output: 12_600_000, CacheWrite: 90_300_000, CacheRead: 2_000_000_000,
			Models: []usage.ModelUsage{
				{Model: "claude-sonnet-4-6", Input: 7_000_000, Output: 12_600_000, CacheWrite: 90_300_000, CacheRead: 2_000_000_000},
			}},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	out := updated.(*MainMenuModel).renderStatsBox()

	maxAllowed := menuInnerWidth + 2 // left border + content + right pad/border
	for _, line := range strings.Split(out, "\n") {
		if line == "" || !strings.Contains(line, "│") {
			continue
		}
		if w := visibleWidth(line); w > maxAllowed {
			t.Errorf("stats box line exceeds box width (%d): width=%d %q", maxAllowed, w, line)
		}
	}
	// Sanity: the wide header actually rendered (otherwise the test proves nothing).
	if !strings.Contains(out, "Cache R") {
		t.Fatalf("expected the Cache R column header to render:\n%s", out)
	}
}

// TestRenderStatsBox_showsPerModelBreakdown verifies the per-model rows that the
// old stats screen printed under each month survived the move into the tabbed UI:
// each month lists its models (claude- prefix stripped) so you can see which model
// drove the spend, not just the monthly aggregate.
func TestRenderStatsBox_showsPerModelBreakdown(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)

	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 1_000_000, Output: 500_000,
			Models: []usage.ModelUsage{
				{Model: "claude-opus-4-8", Input: 800_000, Output: 400_000},
				{Model: "claude-haiku-4-5", Input: 200_000, Output: 100_000},
			}},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	out := stripANSI(updated.(*MainMenuModel).renderStatsBox())

	for _, want := range []string{"opus-4-8", "haiku-4-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats box missing per-model row %q:\n%s", want, out)
		}
	}
	// The raw claude- prefix should be stripped for compactness.
	if strings.Contains(out, "claude-opus-4-8") {
		t.Errorf("per-model row should strip the claude- prefix:\n%s", out)
	}
}

// TestRenderStatsBox_blankRowBetweenMonths verifies that consecutive months are
// separated by a blank spacer row so the per-model breakdown of one month doesn't
// visually run straight into the next month's header row.
func TestRenderStatsBox_blankRowBetweenMonths(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)

	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 1_000_000, Models: []usage.ModelUsage{{Model: "claude-opus-4-8", Input: 1_000_000}}},
		{Month: "2026-05", Input: 500_000, Models: []usage.ModelUsage{{Model: "claude-opus-4-8", Input: 500_000}}},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	lines := strings.Split(updated.(*MainMenuModel).renderStatsBox(), "\n")

	idx := -1
	for i, l := range lines {
		if strings.Contains(stripANSI(l), "May 2026") {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("could not find the May 2026 month row:\n%s", strings.Join(lines, "\n"))
	}
	// The row directly above the second month must be a blank box row (only the
	// border glyphs with spaces between them).
	above := stripANSI(lines[idx-1])
	inner := strings.TrimSuffix(strings.TrimPrefix(above, "│"), "│")
	if strings.TrimSpace(inner) != "" {
		t.Errorf("expected a blank spacer row above the second month, got: %q", above)
	}
}

// TestRenderStatsBox_costColumnRightAligned verifies every dollar figure (per-model
// cost, the month bar-row cost, and the grand-total cost) shares the same right edge
// as the month's Total-tokens number, so the money reads as one clean column under
// the "Total" header instead of sitting one character to its left.
func TestRenderStatsBox_costColumnRightAligned(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)

	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 1_000_000, Output: 500_000,
			Models: []usage.ModelUsage{{Model: "claude-opus-4-8", Input: 1_000_000, Output: 500_000}}},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	lines := strings.Split(updated.(*MainMenuModel).renderStatsBox(), "\n")

	// rightEdge is the column (count of chars up to and including the last
	// non-space) of a box row's content, with borders/padding stripped.
	rightEdge := func(s string) int {
		s = stripANSI(s)
		s = strings.TrimPrefix(s, "│")
		s = strings.TrimSuffix(s, "│")
		return len([]rune(strings.TrimRight(s, " ")))
	}

	var totalRow, modelRow, barRow, estRow string
	for _, l := range lines {
		p := stripANSI(l)
		switch {
		case strings.Contains(p, "Jun 2026"):
			totalRow = l
		case strings.Contains(p, "opus-4-8"):
			modelRow = l
		case strings.Contains(p, "%"): // bar row carries the percent + month cost
			barRow = l
		case strings.Contains(p, "Est. cost"):
			estRow = l
		}
	}
	if totalRow == "" || modelRow == "" || barRow == "" || estRow == "" {
		t.Fatalf("missing rows:\n%s", strings.Join(lines, "\n"))
	}
	want := rightEdge(totalRow)
	for name, row := range map[string]string{"per-model": modelRow, "bar cost": barRow, "est. cost": estRow} {
		if got := rightEdge(row); got != want {
			t.Errorf("%s right edge = %d, want %d (Total-tokens column)\ntotal: %q\nrow:   %q",
				name, got, want, stripANSI(totalRow), stripANSI(row))
		}
	}
}

// TestRenderStatsBox_perModelTreeBranches verifies the per-model rows hang off the
// month as a tree: every model but the last gets a ├─ connector and the last gets a
// └─, so the breakdown visually reads as children of the month.
func TestRenderStatsBox_perModelTreeBranches(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)

	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 1_000_000, Models: []usage.ModelUsage{
			{Model: "claude-opus-4-8", Input: 800_000},
			{Model: "claude-fable-5", Input: 150_000},
			{Model: "claude-haiku-4-5", Input: 50_000},
		}},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	box := updated.(*MainMenuModel).renderStatsBox()

	for _, mid := range []string{"opus-4-8", "fable-5"} {
		line := stripANSI(findLineContaining(box, mid))
		if !strings.Contains(line, "├─") {
			t.Errorf("non-last model row %q should carry a ├─ branch: %q", mid, line)
		}
	}
	last := stripANSI(findLineContaining(box, "haiku-4-5"))
	if !strings.Contains(last, "└─") {
		t.Errorf("last model row should close the tree with a └─ branch: %q", last)
	}
	if strings.Contains(last, "├─") {
		t.Errorf("last model row must use └─, not ├─: %q", last)
	}
}

func TestRenderStatsBox_monthRendersAsName(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 2_000_000},
		{Month: "2026-05", Input: 1_000_000},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	out := stripANSI(updated.(*MainMenuModel).renderStatsBox())
	for _, want := range []string{"Jun 2026", "May 2026"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats box missing human month label %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "2026-06") || strings.Contains(out, "2026-05") {
		t.Errorf("raw YYYY-MM month should be replaced by a name:\n%s", out)
	}
}

func TestRenderStatsBox_biggerGapBeforeTotal(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	months := []usage.MonthlyUsage{{Month: "2026-06", Input: 2_000_000}}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	out := stripANSI(updated.(*MainMenuModel).renderStatsBox())
	gap := "Cache R" + strings.Repeat(" ", 11) + "Total"
	if !strings.Contains(out, gap) {
		t.Errorf("expected a wider gap %q before Total:\n%s", gap, out)
	}
}

func TestRenderStatsBox_containsStatsRows(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()
	// The stats rows section must be present (loading or data row).
	// At minimum "Token" or "usage" or loading text appears.
	if !strings.Contains(out, "Token") && !strings.Contains(out, "usage") && !strings.Contains(out, "Loading") && !strings.Contains(out, "No usage") && !strings.Contains(out, "Crunching") {
		t.Errorf("stats box missing stats content:\n%s", out)
	}
}

// TestRenderStatsBox_LoadingState verifies a fresh model shows the loading text
// when statsLoading is set but no data is cached yet.
func TestRenderStatsBox_LoadingState(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.statsLoading = true
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()
	if !strings.Contains(out, "Crunching") {
		t.Errorf("loading state should render 'Crunching', got:\n%s", out)
	}
}

// TestRenderStatsBox_DataState verifies that after statsLoadedMsg is applied the
// stats box renders humanized token figures and the "Total est. cost" footer.
func TestRenderStatsBox_DataState(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)

	// Simulate a successful load by sending the msg through Update.
	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 2_000_000, Output: 500_000},
	}
	updated, _ := m.Update(statsLoadedMsg{months: months})
	mm := updated.(*MainMenuModel)

	out := mm.renderStatsBox()
	if !strings.Contains(out, "2.0M") && !strings.Contains(out, "Jun 2026") {
		t.Errorf("data state should render humanized tokens or month label:\n%s", out)
	}
	if !strings.Contains(out, "Est. cost") && !strings.Contains(out, "Total") {
		t.Errorf("data state should render 'Total' or 'Est. cost' footer:\n%s", out)
	}
}

// TestHandleRune_TReturnsNonNilCmdAndSetsLoading verifies that pressing 't'
// kicks off the async load (returns a non-nil cmd) and sets statsLoading true.
func TestHandleRune_TReturnsNonNilCmdAndSetsLoading(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	_, cmd := m.handleRune('t')
	if m.ActiveTab() != TabStats {
		t.Errorf("after 't' activeTab = %v, want TabStats", m.ActiveTab())
	}
	if !m.statsLoading {
		t.Error("after 't' statsLoading should be true")
	}
	if cmd == nil {
		t.Error("after 't' cmd should be non-nil (async load triggered)")
	}
}

// visibleWidth returns the printable (non-ANSI) width of s using lipgloss.
func visibleWidth(s string) int {
	// Import lipgloss at the top of the file — reuse existing import in package.
	// We just call strings.Count as a rough proxy; lipgloss.Width handles ANSI.
	// Since this is an internal test in package tui, we can call lipgloss directly.
	return len([]rune(stripANSI(s)))
}

// stripANSI removes ANSI escape sequences for width measurement.
func stripANSI(s string) string {
	var out []rune
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
