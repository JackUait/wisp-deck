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
