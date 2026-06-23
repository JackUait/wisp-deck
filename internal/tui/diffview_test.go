package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sampleDiff(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString("+added line ")
		b.WriteByte(byte('0' + (i % 10)))
		b.WriteByte('\n')
	}
	return b.String()
}

func sizeDiff(m DiffViewModel, w, h int) DiffViewModel {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(DiffViewModel)
}

func keyDiff(m DiffViewModel, t tea.KeyType) (DiffViewModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: t})
	return updated.(DiffViewModel), cmd
}

func runeDiff(m DiffViewModel, r rune) (DiffViewModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(DiffViewModel), cmd
}

func clickDiff(m DiffViewModel, x, y int) (DiffViewModel, tea.Cmd) {
	updated, cmd := m.Update(tea.MouseMsg{
		X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	return updated.(DiffViewModel), cmd
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestNewDiffView_stores_title_and_content(t *testing.T) {
	m := NewDiffView("lib/x.sh", "+hello\n")
	if m.title != "lib/x.sh" {
		t.Errorf("title = %q, want lib/x.sh", m.title)
	}
	if m.content != "+hello\n" {
		t.Errorf("content = %q, want +hello", m.content)
	}
}

func TestDiffView_Escape_quits(t *testing.T) {
	m := sizeDiff(NewDiffView("f", sampleDiff(5)), 80, 24)
	m2, cmd := keyDiff(m, tea.KeyEscape)
	if !m2.quitting {
		t.Error("Escape should set quitting")
	}
	if !quits(cmd) {
		t.Error("Escape should emit tea.Quit")
	}
}

func TestDiffView_q_quits(t *testing.T) {
	m := sizeDiff(NewDiffView("f", sampleDiff(5)), 80, 24)
	m2, cmd := runeDiff(m, 'q')
	if !m2.quitting {
		t.Error("q should set quitting")
	}
	if !quits(cmd) {
		t.Error("q should emit tea.Quit")
	}
}

func TestDiffView_CtrlC_quits(t *testing.T) {
	m := sizeDiff(NewDiffView("f", sampleDiff(5)), 80, 24)
	_, cmd := keyDiff(m, tea.KeyCtrlC)
	if !quits(cmd) {
		t.Error("ctrl+c should emit tea.Quit")
	}
}

func TestDiffView_View_shows_title_controls_and_content(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", "+added unique-marker\n"), 80, 24)
	out := m.View()
	if !strings.Contains(out, "lib/x.sh") {
		t.Error("view should show the title (filename)")
	}
	if !strings.Contains(out, "unique-marker") {
		t.Error("view should show the diff content")
	}
	// A control bar advertising how to scroll and close.
	if !strings.Contains(strings.ToLower(out), "scroll") {
		t.Error("view should show a scroll hint")
	}
	if !strings.Contains(strings.ToLower(out), "esc") {
		t.Error("view should advertise Esc to close")
	}
}

// countDiffLines tallies added (+) and deleted (-) lines of the (possibly
// ANSI-colored) diff body. The +++/--- file markers are already stripped
// upstream, so a plain leading +/- after stripping color is authoritative.
func TestCountDiffLines_counts_added_and_deleted(t *testing.T) {
	content := " context line\n" +
		"+added one\n" +
		"+added two\n" +
		"-removed one\n" +
		"\x1b[32m+added colored\x1b[m\n" +
		"\x1b[31m-removed colored\x1b[m\n" +
		"\n" // trailing blank
	added, deleted := countDiffLines(content)
	if added != 3 {
		t.Errorf("added = %d, want 3", added)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
}

// The header must show ONLY the file path and the added/deleted line counts —
// nothing else (no "git diff:" label).
func TestDiffView_header_shows_path_and_line_counts(t *testing.T) {
	content := " ctx\n+a\n+b\n+c\n-x\n-y\n"
	m := sizeDiff(NewDiffView("lib/x.sh", content), 80, 24)
	out := m.View()
	if !strings.Contains(out, "lib/x.sh") {
		t.Errorf("header should show the file path, got:\n%s", out)
	}
	if !strings.Contains(out, "+3") {
		t.Errorf("header should show +3 added lines, got:\n%s", out)
	}
	if !strings.Contains(out, "−2") { // U+2212 minus, matching the ledger
		t.Errorf("header should show −2 deleted lines, got:\n%s", out)
	}
	if strings.Contains(out, "git diff:") {
		t.Errorf("header should NOT carry a 'git diff:' label, got:\n%s", out)
	}
}

// The popup is a centered, rounded-bordered box floating on a full-screen
// surface. A left-click in the surrounding margin (outside the box) closes it;
// a click inside the box does not. This is the only way to honor "click outside
// to close": tmux 3.6 swallows clicks outside a sub-full-screen popup, so the
// popup runs full-screen and the pager owns the margin itself.
func TestDiffView_click_in_margin_quits(t *testing.T) {
	for _, pt := range []struct{ x, y int }{{0, 0}, {79, 23}} {
		m := sizeDiff(NewDiffView("f", sampleDiff(5)), 80, 24)
		m2, cmd := clickDiff(m, pt.x, pt.y)
		if !m2.quitting {
			t.Errorf("left-click in margin at (%d,%d) should set quitting", pt.x, pt.y)
		}
		if !quits(cmd) {
			t.Errorf("left-click in margin at (%d,%d) should emit tea.Quit", pt.x, pt.y)
		}
	}
}

func TestDiffView_click_inside_box_does_not_quit(t *testing.T) {
	m := sizeDiff(NewDiffView("f", sampleDiff(5)), 80, 24)
	m2, cmd := clickDiff(m, 40, 12)
	if m2.quitting {
		t.Error("left-click inside the box should not set quitting")
	}
	if quits(cmd) {
		t.Error("left-click inside the box should not emit tea.Quit")
	}
}

func TestDiffView_View_is_rounded_border_box(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", "+a\n"), 80, 24)
	out := m.View()
	if !strings.Contains(out, "╭") { // rounded top-left corner
		t.Errorf("view should draw a rounded border box, got:\n%s", out)
	}
}

// numberLines prefixes each diff line with a right-aligned NEW-file line number
// gutter. Context and added (+) lines advance and show their number; removed (-)
// lines are not in the current file, so their gutter is blank. The diff's own
// ANSI color after the gutter is preserved.
func TestNumberLines_numbers_nonremoved_blank_for_removed(t *testing.T) {
	content := " ctx\n+add\n-del\n ctx2\n"
	out := diffAnsiSeq.ReplaceAllString(numberLines(content), "")
	lines := strings.Split(out, "\n")
	want := []string{
		"1 │  ctx",
		"2 │ +add",
		"  │ -del", // removed line: blank gutter
		"3 │  ctx2",
		"", // trailing element from the final newline: no gutter
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(lines), lines, len(want), want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestNumberLines_pads_gutter_to_widest_number(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 12; i++ { // 12 lines -> 2-digit gutter
		b.WriteString(" x\n")
	}
	out := diffAnsiSeq.ReplaceAllString(numberLines(b.String()), "")
	first := strings.SplitN(out, "\n", 2)[0]
	if first != " 1 │  x" { // right-aligned in a 2-wide gutter
		t.Errorf("first line gutter should be 2-wide right-aligned, got %q", first)
	}
}

func TestDiffView_View_shows_line_numbers(t *testing.T) {
	content := " line one\n line two\n line three\n"
	m := sizeDiff(NewDiffView("lib/x.sh", content), 80, 24)
	out := diffAnsiSeq.ReplaceAllString(m.View(), "")
	for _, g := range []string{"1 │", "2 │", "3 │"} {
		if !strings.Contains(out, g) {
			t.Errorf("view should show line-number gutter %q, got:\n%s", g, out)
		}
	}
}

func stripA(s string) string { return diffAnsiSeq.ReplaceAllString(s, "") }

// dropMarker strips the leading +/-/space diff marker while preserving the
// line's ANSI color.
func TestDropMarker_drops_prefix_keeps_color(t *testing.T) {
	if got := dropMarker(" ctx"); got != "ctx" {
		t.Errorf("context: got %q, want %q", got, "ctx")
	}
	if got := dropMarker("+add"); got != "add" {
		t.Errorf("added: got %q, want %q", got, "add")
	}
	if got := dropMarker("\x1b[32m+code\x1b[m"); got != "\x1b[32mcode\x1b[m" {
		t.Errorf("colored: got %q, want %q", got, "\x1b[32mcode\x1b[m")
	}
}

// fitColumn truncates/pads to a fixed visible width, counting only printable
// columns (ANSI escapes don't count) so colored text aligns.
func TestFitColumn_truncates_and_pads(t *testing.T) {
	if got := stripA(fitColumn("ab", 5)); got != "ab   " {
		t.Errorf("pad: got %q, want %q", got, "ab   ")
	}
	if got := stripA(fitColumn("abcdef", 3)); got != "abc" {
		t.Errorf("truncate: got %q, want %q", got, "abc")
	}
	if got := stripA(fitColumn("\x1b[32mabcdef\x1b[m", 3)); got != "abc" {
		t.Errorf("ansi truncate: got %q, want %q", got, "abc")
	}
}

// Narrow popup -> inline numbered view (markers kept, single gutter).
func TestRenderDiffBody_inline_when_narrow(t *testing.T) {
	out := stripA(renderDiffBody(" ctx\n-del\n+add\n", 50))
	if !strings.Contains(out, "+add") || !strings.Contains(out, "-del") {
		t.Errorf("inline should keep the +/- markers, got:\n%s", out)
	}
}

// Wide popup -> side-by-side: markers dropped, deletions on the left of the
// divider and additions on the right, context shown on both sides.
func TestRenderDiffBody_side_by_side_when_wide(t *testing.T) {
	out := stripA(renderDiffBody(" ctx\n-del\n+add\n ctx2\n", 120))
	if strings.Contains(out, "+add") || strings.Contains(out, "-del") {
		t.Errorf("side-by-side should drop the +/- markers, got:\n%s", out)
	}
	var changeRow, ctxRow string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "del") && strings.Contains(ln, "add") {
			changeRow = ln
		}
		if strings.Count(ln, "ctx2") == 2 {
			ctxRow = ln
		}
	}
	if changeRow == "" {
		t.Fatalf("expected a row pairing del|add, got:\n%s", out)
	}
	bar := strings.Index(changeRow, "│")
	if bar < 0 || strings.Index(changeRow, "del") > bar || strings.Index(changeRow, "add") < bar {
		t.Errorf("del should be left of the divider, add right: %q", changeRow)
	}
	if ctxRow == "" {
		t.Errorf("a context line should appear on both sides of a row, got:\n%s", out)
	}
}

func TestDiffView_side_by_side_when_wide(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", " ctx\n-del\n+add\n ctx2\n"), 200, 40)
	out := stripA(m.View())
	found := false
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "del") && strings.Contains(ln, "add") {
			found = true
		}
	}
	if !found {
		t.Errorf("wide view should render side-by-side (del|add on one row), got:\n%s", out)
	}
}

func TestDiffView_inline_when_narrow(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", " ctx\n-del\n+add\n"), 60, 24)
	out := stripA(m.View())
	if !strings.Contains(out, "+add") {
		t.Errorf("narrow view should be inline (markers kept), got:\n%s", out)
	}
}

func isSideBySide(out string) bool {
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "del") && strings.Contains(ln, "add") {
			return true
		}
	}
	return false
}

func TestDiffView_View_shows_view_buttons(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", " ctx\n+add\n"), 200, 40)
	out := stripA(m.View())
	if !strings.Contains(out, "Inline") {
		t.Errorf("view should show an Inline button, got:\n%s", out)
	}
	if !strings.Contains(out, "Side-by-side") {
		t.Errorf("view should show a Side-by-side button, got:\n%s", out)
	}
}

// At 200x40 the layout auto-picks side-by-side; clicking the Inline button must
// force inline, and clicking Side-by-side must force it back. Button hit boxes
// (computed in the model): tabs row is screen y=4, content starts at screen
// x=11, Inline spans content cols [1,11), Side-by-side [12,28).
func TestDiffView_click_buttons_switch_mode(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", " ctx\n-del\n+add\n ctx2\n"), 200, 40)
	if !isSideBySide(stripA(m.View())) {
		t.Fatal("expected auto side-by-side at 200 wide")
	}
	// Click the Inline button (screen x=15, y=4).
	m, cmd := clickDiff(m, 15, 4)
	if quits(cmd) || m.quitting {
		t.Fatal("clicking a button must not close the popup")
	}
	if isSideBySide(stripA(m.View())) || !strings.Contains(stripA(m.View()), "+add") {
		t.Errorf("clicking Inline should switch to inline, got:\n%s", stripA(m.View()))
	}
	// Click the Side-by-side button (screen x=30, y=4).
	m, _ = clickDiff(m, 30, 4)
	if !isSideBySide(stripA(m.View())) {
		t.Errorf("clicking Side-by-side should switch back, got:\n%s", stripA(m.View()))
	}
}

func TestDiffView_hover_highlights_tab_without_switching(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", " ctx\n-del\n+add\n ctx2\n"), 200, 40)
	if !isSideBySide(stripA(m.View())) {
		t.Fatal("expected auto side-by-side at 200 wide")
	}
	// Hover the Inline button (x=15, y=4) while side-by-side is active.
	updated, cmd := m.Update(tea.MouseMsg{X: 15, Y: 4, Action: tea.MouseActionMotion})
	m = updated.(DiffViewModel)
	if cmd != nil {
		t.Error("hover should not emit a command")
	}
	if m.hoverMode != diffModeInline {
		t.Errorf("hovering Inline should set hoverMode=%d, got %d", diffModeInline, m.hoverMode)
	}
	// Hover must not switch the active view.
	if !isSideBySide(stripA(m.View())) {
		t.Error("hover must not switch the view mode")
	}
}

func TestDiffView_tab_key_toggles_view(t *testing.T) {
	m := sizeDiff(NewDiffView("lib/x.sh", " ctx\n-del\n+add\n ctx2\n"), 200, 40)
	if !isSideBySide(stripA(m.View())) {
		t.Fatal("expected auto side-by-side at 200 wide")
	}
	m, _ = keyDiff(m, tea.KeyTab)
	if isSideBySide(stripA(m.View())) {
		t.Errorf("Tab should toggle to inline, got:\n%s", stripA(m.View()))
	}
	m, _ = keyDiff(m, tea.KeyTab)
	if !isSideBySide(stripA(m.View())) {
		t.Errorf("Tab again should toggle back to side-by-side, got:\n%s", stripA(m.View()))
	}
}

// isSingleSided reports whether a diff is a whole-file addition or deletion:
// every body line is the same kind (+ or -) with no context. Such files (git
// status A or D) have nothing to compare across two columns.
func TestIsSingleSided_pure_add_and_pure_delete(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"pure add (new file)", "+a\n+b\n+c\n", true},
		{"pure delete (deleted file)", "-a\n-b\n-c\n", true},
		{"colored pure add", "\x1b[32m+a\x1b[m\n\x1b[32m+b\x1b[m\n", true},
		{"add with context (appended)", " ctx\n+a\n", false},
		{"delete with context", " ctx\n-a\n", false},
		{"modified (add and delete)", "-old\n+new\n", false},
		{"all lines changed, no context", "-a\n-b\n+x\n+y\n", false},
		{"empty diff", "\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSingleSided(tc.content); got != tc.want {
				t.Errorf("isSingleSided(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

// diffStatus classifies a diff as a whole-file addition, a deletion, or a
// modification, from the same content signal isSingleSided uses.
func TestDiffStatus_classifies_add_delete_modify(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"pure add", "+a\n+b\n", "added"},
		{"pure delete", "-a\n-b\n", "deleted"},
		{"modified (add+delete)", "-old\n+new\n", "modified"},
		{"modified (appended)", " ctx\n+a\n", "modified"},
		{"all lines changed", "-a\n+x\n", "modified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffStatus(tc.content); got != tc.want {
				t.Errorf("diffStatus(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// The header advertises the file's git status: added, deleted, or modified.
func TestDiffView_header_shows_status(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"added", "+a\n+b\n", "ADDED"},
		{"deleted", "-a\n-b\n", "DELETED"},
		{"modified", " ctx\n-old\n+new\n", "MODIFIED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizeDiff(NewDiffView("f.go", tc.content), 120, 30)
			out := stripA(m.View())
			if !strings.Contains(out, tc.want) {
				t.Errorf("header should show %q status, got:\n%s", tc.want, out)
			}
		})
	}
}

// A modified file's header must not mislabel it as added or deleted.
func TestDiffView_modified_header_not_added_or_deleted(t *testing.T) {
	m := sizeDiff(NewDiffView("f.go", " ctx\n-old\n+new\n"), 120, 30)
	out := stripA(m.View())
	if strings.Contains(out, "ADDED") || strings.Contains(out, "DELETED") {
		t.Errorf("modified file must not show ADDED/DELETED, got:\n%s", out)
	}
}

// A whole-file addition or deletion has nothing to show side-by-side, so the
// pager locks to the single (inline) view and hides the view switcher — even at
// a width that would otherwise auto-pick side-by-side.
func TestDiffView_added_file_has_no_view_switcher(t *testing.T) {
	m := sizeDiff(NewDiffView("new.go", "+a\n+b\n+c\n"), 200, 40)
	out := stripA(m.View())
	if strings.Contains(out, "Inline") || strings.Contains(out, "Side-by-side") {
		t.Errorf("added file should hide the view switcher, got:\n%s", out)
	}
	if isSideBySide(out) {
		t.Errorf("added file should render single (inline) view, got:\n%s", out)
	}
}

func TestDiffView_deleted_file_has_no_view_switcher(t *testing.T) {
	m := sizeDiff(NewDiffView("gone.go", "-a\n-b\n-c\n"), 200, 40)
	out := stripA(m.View())
	if strings.Contains(out, "Inline") || strings.Contains(out, "Side-by-side") {
		t.Errorf("deleted file should hide the view switcher, got:\n%s", out)
	}
}

// The Tab key must not switch views for a single-sided file (there is no other
// view to switch to).
func TestDiffView_added_file_tab_key_is_noop(t *testing.T) {
	m := sizeDiff(NewDiffView("new.go", "+a\n+b\n+c\n"), 200, 40)
	m, _ = keyDiff(m, tea.KeyTab)
	if isSideBySide(stripA(m.View())) {
		t.Errorf("Tab must not switch a single-sided file to side-by-side, got:\n%s", stripA(m.View()))
	}
}

// Clicking where the (now absent) switcher buttons would be must not switch
// views nor close the popup for a single-sided file.
func TestDiffView_added_file_click_on_tab_row_does_not_switch(t *testing.T) {
	m := sizeDiff(NewDiffView("new.go", "+a\n+b\n+c\n"), 200, 40)
	m, cmd := clickDiff(m, 15, 4)
	if quits(cmd) || m.quitting {
		t.Fatal("clicking the header of a single-sided file must not close the popup")
	}
	if isSideBySide(stripA(m.View())) {
		t.Errorf("clicking the header must not switch a single-sided file, got:\n%s", stripA(m.View()))
	}
}

// A file where every line changed (no context, but both adds and deletes) is a
// modification, not an add/delete — it keeps the view switcher.
func TestDiffView_all_lines_changed_keeps_view_switcher(t *testing.T) {
	m := sizeDiff(NewDiffView("mod.go", "-a\n-b\n+x\n+y\n"), 200, 40)
	out := stripA(m.View())
	if !strings.Contains(out, "Inline") || !strings.Contains(out, "Side-by-side") {
		t.Errorf("a fully-changed (but modified) file should keep the view switcher, got:\n%s", out)
	}
}

// ParseBackdrop composites the serialized screen capture (a "W H" header, then
// "PANE left top" blocks of captured lines ending in "ENDPANE") into W×H rows,
// placing each pane's lines at its window position. This is what the pager
// renders dimmed behind the floating box.
func TestParseBackdrop_places_panes_by_geometry(t *testing.T) {
	data := "10 3\n" +
		"PANE 0 0\nAAAAA\nBBBBB\nCCCCC\nENDPANE\n" +
		"PANE 5 0\nVVVVV\nWWWWW\nXXXXX\nENDPANE\n"
	rows := ParseBackdrop(data)
	want := []string{"AAAAAVVVVV", "BBBBBWWWWW", "CCCCCXXXXX"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows %q, want %d %q", len(rows), rows, len(want), want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: got %q, want %q", i, rows[i], want[i])
		}
	}
}

func TestParseBackdrop_offset_pane_placed_at_top(t *testing.T) {
	// A pane starting at row 1 leaves row 0 blank.
	data := "6 2\nPANE 0 1\nHELLO\nENDPANE\n"
	rows := ParseBackdrop(data)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %q", len(rows), rows)
	}
	if strings.TrimSpace(rows[0]) != "" {
		t.Errorf("row 0 should be blank, got %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], "HELLO") {
		t.Errorf("row 1 should start with HELLO, got %q", rows[1])
	}
}

// With a backdrop set, the margin around the floating box shows the (dimmed)
// captured screen — not blank — while the box itself stays on top.
func TestDiffView_shows_backdrop_in_margin(t *testing.T) {
	rows := make([]string, 24)
	for i := range rows {
		rows[i] = strings.Repeat("·", 80)
	}
	rows[0] = "BEHIND-TOP" + strings.Repeat("·", 70) // top row sits in the margin
	m := NewDiffView("lib/x.sh", "+a\n").WithBackdrop(rows)
	m = sizeDiff(m, 80, 24)
	out := m.View()
	if !strings.Contains(out, "BEHIND-TOP") {
		t.Errorf("margin should show the backdrop text, got:\n%s", out)
	}
	if !strings.Contains(out, "lib/x.sh") {
		t.Errorf("box (with header) should still render on top, got:\n%s", out)
	}
	if !strings.Contains(out, "╭") {
		t.Errorf("box border should still render, got:\n%s", out)
	}
}

func TestDiffView_preserves_ansi_color_in_content(t *testing.T) {
	colored := "\x1b[32m+added\x1b[m\n\x1b[31m-removed\x1b[m\n"
	m := sizeDiff(NewDiffView("f", colored), 80, 24)
	out := m.View()
	if !strings.Contains(out, "\x1b[32m") || !strings.Contains(out, "\x1b[31m") {
		t.Error("view should preserve the diff's ANSI color escapes")
	}
}

func TestDiffView_scrolls_with_keys(t *testing.T) {
	// Content much taller than the viewport so there's room to scroll.
	m := sizeDiff(NewDiffView("f", sampleDiff(100)), 80, 10)
	if !m.viewport.AtTop() {
		t.Fatal("should start at top")
	}
	// Page down moves off the top.
	m, _ = keyDiff(m, tea.KeySpace)
	if m.viewport.AtTop() {
		t.Error("space (page down) should scroll off the top")
	}
	// G jumps to the bottom.
	m, _ = runeDiff(m, 'G')
	if !m.viewport.AtBottom() {
		t.Error("G should jump to the bottom")
	}
	// g jumps back to the top.
	m, _ = runeDiff(m, 'g')
	if !m.viewport.AtTop() {
		t.Error("g should jump back to the top")
	}
}
