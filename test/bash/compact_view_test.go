package bash_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// format_file now shows the file BASENAME only (no parent path), truncating
// with an ellipsis when it exceeds the available width.

func TestFormatFile_strips_path_to_basename(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"design/changeset.html", "30"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "changeset.html" {
		t.Errorf("got %q, want %q", got, "changeset.html")
	}
}

func TestFormatFile_strips_deep_path_to_basename(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"internal/tui/compact-view.go", "30"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "compact-view.go" {
		t.Errorf("got %q, want %q", got, "compact-view.go")
	}
}

func TestFormatFile_short_name_unchanged(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"x.go", "10"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "x.go" {
		t.Errorf("got %q, want %q", got, "x.go")
	}
}

func TestFormatFile_truncates_long_basename_with_ellipsis(t *testing.T) {
	// max=8 -> keep 7 chars + ellipsis
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_file",
		[]string{"a/b/verylongfilename.go", "8"}, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "verylon…" {
		t.Errorf("got %q, want %q", got, "verylon…")
	}
}

// The change ledger lines up each file's +/- counts directly UNDER the section
// name: a group header (" <glyph> <label>  (n)") puts its label at column 3, and
// a file row must start its "+added" count at that same column 3 — no extra
// indent — so the figures read as a column beneath "modified"/"added"/etc.

func ledgerRow(t *testing.T, added, deleted, file string) string {
	t.Helper()
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_ledger_row",
		[]string{added, deleted, file}, nil)
	assertExitCode(t, code, 0)
	return ansiRE.ReplaceAllString(out, "")
}

func TestFormatLedgerRow_plus_starts_at_section_label_column(t *testing.T) {
	plain := ledgerRow(t, "59", "35", "file.go")
	// 3 leading spaces then the "+count" — column 3, where a section label sits.
	if !strings.HasPrefix(plain, "   +59") {
		t.Errorf("row should start +count at column 3, got %q", plain)
	}
	// Must NOT be pushed one column further right (the old right-aligned indent).
	if strings.HasPrefix(plain, "    +") {
		t.Errorf("row must not indent the +count past column 3, got %q", plain)
	}
}

// The section header's label and a file row's "+count" must land in the SAME
// column, so the counts sit directly under the section name.
func TestFormatLedgerRow_aligns_with_group_header_label(t *testing.T) {
	hOut, code := runBashFunc(t, "lib/compact-view.sh", "format_group_header",
		[]string{"", "○", "modified", "1"}, nil)
	assertExitCode(t, code, 0)
	header := []rune(ansiRE.ReplaceAllString(hOut, ""))
	labelCol := -1
	for i := 0; i+8 <= len(header); i++ {
		if string(header[i:i+8]) == "modified" {
			labelCol = i
			break
		}
	}
	if labelCol < 0 {
		t.Fatalf("header should contain the label, got %q", string(header))
	}
	row := []rune(ledgerRow(t, "59", "35", "file.go"))
	plusCol := -1
	for i, r := range row {
		if r == '+' {
			plusCol = i
			break
		}
	}
	if plusCol != labelCol {
		t.Errorf("+count column %d must equal section label column %d\nheader=%q\nrow=%q",
			plusCol, labelCol, string(header), string(row))
	}
}

// Counts stay aligned across digit widths: the "−deleted" column must land in
// the same place whether the added count is 1 or 3 digits wide.
func TestFormatLedgerRow_minus_column_stable_across_widths(t *testing.T) {
	minusCol := func(s string) int {
		for i, r := range []rune(s) {
			if r == '−' {
				return i
			}
		}
		return -1
	}
	a := minusCol(ledgerRow(t, "5", "5", "f.go"))
	b := minusCol(ledgerRow(t, "123", "99", "f.go"))
	if a < 0 || b < 0 {
		t.Fatalf("both rows should contain a − column (a=%d b=%d)", a, b)
	}
	if a != b {
		t.Errorf("− column drifted with digit width: 1-digit=%d, 3-digit=%d", a, b)
	}
}

// sum_numstat totals the added/deleted columns of `git --numstat` output,
// treating binary markers ("-") as zero. Echoes "<added> <deleted>".

func TestSumNumstat_totals_columns(t *testing.T) {
	in := "142\t38\tinternal/tui/compact-view.go\n54\t29\tlib/tmux-session.sh\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "sum_numstat",
		nil, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "196 67" {
		t.Errorf("got %q, want %q", got, "196 67")
	}
}

func TestSumNumstat_treats_binary_as_zero(t *testing.T) {
	in := "-\t-\tassets/logo.png\n5\t3\tlib/x.sh\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "sum_numstat",
		nil, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "5 3" {
		t.Errorf("got %q, want %q", got, "5 3")
	}
}

func TestSumNumstat_empty_is_zero(t *testing.T) {
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "sum_numstat",
		nil, nil, "")
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "0 0" {
		t.Errorf("got %q, want %q", got, "0 0")
	}
}

// clamp_scroll keeps the scroll offset within [0, total-avail].

func TestClampScroll_within_range(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "clamp_scroll",
		[]string{"5", "30", "10"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "5" {
		t.Errorf("got %q, want %q", got, "5")
	}
}

func TestClampScroll_negative_floors_to_zero(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "clamp_scroll",
		[]string{"-4", "30", "10"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "0" {
		t.Errorf("got %q, want %q", got, "0")
	}
}

func TestClampScroll_beyond_max_caps_at_max(t *testing.T) {
	// total 30, avail 10 -> max scroll 20
	out, code := runBashFunc(t, "lib/compact-view.sh", "clamp_scroll",
		[]string{"99", "30", "10"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "20" {
		t.Errorf("got %q, want %q", got, "20")
	}
}

func TestClampScroll_content_fits_is_zero(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "clamp_scroll",
		[]string{"3", "8", "10"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "0" {
		t.Errorf("got %q, want %q", got, "0")
	}
}

// viewport_slice prints <count> lines of stdin starting after <scroll> lines.

func TestViewportSlice_middle_window(t *testing.T) {
	in := "a\nb\nc\nd\ne\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "viewport_slice",
		[]string{"1", "2"}, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "b\nc" {
		t.Errorf("got %q, want %q", got, "b\nc")
	}
}

func TestViewportSlice_from_top(t *testing.T) {
	in := "a\nb\nc\nd\ne\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "viewport_slice",
		[]string{"0", "3"}, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "a\nb\nc" {
		t.Errorf("got %q, want %q", got, "a\nb\nc")
	}
}

func TestViewportSlice_past_end_clips(t *testing.T) {
	in := "a\nb\nc\nd\ne\n"
	out, code := runBashFuncWithStdin(t, "lib/compact-view.sh", "viewport_slice",
		[]string{"3", "10"}, nil, in)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "d\ne" {
		t.Errorf("got %q, want %q", got, "d\ne")
	}
}

// scroll_status renders the position indicator "first-last/total" with up/down
// arrows reflecting whether more content sits above/below the viewport.

func TestScrollStatus_at_top_shows_down_only(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "scroll_status",
		[]string{"0", "10", "22"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "1-10/22")
	assertContains(t, out, "↓")
	if strings.Contains(out, "↑") {
		t.Errorf("no up arrow expected at top:\n%q", out)
	}
}

func TestScrollStatus_middle_shows_both(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "scroll_status",
		[]string{"5", "10", "22"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "6-15/22")
	assertContains(t, out, "↑")
	assertContains(t, out, "↓")
}

func TestScrollStatus_at_bottom_shows_up_only(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "scroll_status",
		[]string{"12", "10", "22"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "13-22/22")
	assertContains(t, out, "↑")
	if strings.Contains(out, "↓") {
		t.Errorf("no down arrow expected at bottom:\n%q", out)
	}
}

// enter_ui_mode/exit_ui_mode wrap the live pane's terminal setup. The list must
// not be scrollable "infinitely": the refresh loop redraws on the screen with
// \033[2J, and on the MAIN buffer every old frame piles into the terminal's
// scrollback, which the mouse wheel can then scroll through without bound. The
// fix is the ALTERNATE screen buffer (\033[?1049h), which has no scrollback, so
// the wheel can only move the app's own clamped viewport. Setup is emitted only
// for an interactive tty (the Go harness pipes stdin -> $1 != 1 -> nothing).

func TestEnterUiMode_interactive_uses_alt_screen(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "enter_ui_mode",
		[]string{"1"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[?1049h") // alternate screen: kills scrollback
	assertContains(t, out, "\x1b[?25l")   // hide cursor
	assertContains(t, out, "\x1b[?1000h") // SGR mouse reporting
	assertContains(t, out, "\x1b[?1006h")
}

func TestEnterUiMode_noninteractive_emits_nothing(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "enter_ui_mode",
		[]string{"0"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "" {
		t.Errorf("non-interactive enter_ui_mode should emit nothing, got %q", got)
	}
}

func TestExitUiMode_interactive_leaves_alt_screen(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "exit_ui_mode",
		[]string{"1"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[?1049l") // leave alternate screen
	assertContains(t, out, "\x1b[?1000l") // disable mouse reporting
	assertContains(t, out, "\x1b[?25h")   // show cursor
}

func TestExitUiMode_noninteractive_emits_nothing(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "exit_ui_mode",
		[]string{"0"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "" {
		t.Errorf("non-interactive exit_ui_mode should emit nothing, got %q", got)
	}
}

// numstat_path extracts the actual working-tree path from a `git --numstat`
// third field, which for renames is encoded as "old => new" (or with a common
// prefix/suffix brace form "pre{old => new}suf"). Clicking a renamed file must
// open the diff for its CURRENT path, so the map needs the post-rename path.

func TestNumstatPath_plain_path_unchanged(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "numstat_path",
		[]string{"lib/compact-view.sh"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "lib/compact-view.sh" {
		t.Errorf("got %q, want %q", got, "lib/compact-view.sh")
	}
}

func TestNumstatPath_simple_rename_takes_new(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "numstat_path",
		[]string{"old/name.txt => new/name.txt"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "new/name.txt" {
		t.Errorf("got %q, want %q", got, "new/name.txt")
	}
}

func TestNumstatPath_brace_rename_rebuilds_new(t *testing.T) {
	// git collapses a partial rename to a common prefix/suffix with a brace.
	out, code := runBashFunc(t, "lib/compact-view.sh", "numstat_path",
		[]string{"src/{old => new}/file.go"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "src/new/file.go" {
		t.Errorf("got %q, want %q", got, "src/new/file.go")
	}
}

// body_path_map emits ONE entry per rendered body line — the file's path on a
// file row, and an EMPTY line on a group-header or trailing-blank row — so a
// click's body-line index can be looked up to a path. It MUST mirror
// render_group's line structure exactly (1 header line + N file rows + 1 blank
// per non-empty group, staged then modified). Clicking a non-file line yields
// no path and opens nothing.

func bodyPathMap(t *testing.T, staged, unstaged string) []string {
	t.Helper()
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := "source " + module + " && body_path_map \"$1\" \"$2\""
	cmd := exec.Command("bash", "-c", script, "bash", staged, unstaged)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("body_path_map: %v\n%s", err, out)
	}
	// Trim only the single trailing newline printf adds, then split.
	s := strings.TrimSuffix(string(out), "\n")
	return strings.Split(s, "\n")
}

func TestBodyPathMap_file_rows_carry_path_headers_blank(t *testing.T) {
	staged := "1\t2\tlib/a.sh\n3\t4\tlib/b.sh"
	unstaged := "5\t6\tcmd/c.go"
	lines := bodyPathMap(t, staged, unstaged)
	// [0]="" (staged header), [1]=lib/a.sh, [2]=lib/b.sh, [3]="" (blank),
	// [4]="" (modified header), [5]=cmd/c.go, [6]="" (blank)
	want := []string{"", "lib/a.sh", "lib/b.sh", "", "", "cmd/c.go", ""}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(lines), lines, len(want), want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d: got %q, want %q (all=%q)", i, lines[i], want[i], lines)
		}
	}
}

func TestBodyPathMap_rename_row_uses_new_path(t *testing.T) {
	lines := bodyPathMap(t, "1\t2\told.txt => new.txt", "")
	// header, file row, trailing blank
	if len(lines) < 2 || lines[1] != "new.txt" {
		t.Errorf("file row should carry the post-rename path %q, got %q", "new.txt", lines)
	}
}

func TestBodyPathMap_empty_state_has_no_paths(t *testing.T) {
	lines := bodyPathMap(t, "", "")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			t.Errorf("empty changeset must yield no file paths, line %d = %q (all=%q)", i, l, lines)
		}
	}
}

// body_line_for_click maps a clicked SCREEN row to a 1-based body-line index, or
// 0 when the click landed on the pinned header (rows 1-2), the bottom scroll
// status row, or past the end of the content. Args: <row> <scroll> <avail> <total>.

func TestBodyLineForClick_header_rows_yield_zero(t *testing.T) {
	for _, r := range []string{"1", "2"} {
		out, code := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
			[]string{r, "0", "10", "5"}, nil)
		assertExitCode(t, code, 0)
		if got := strings.TrimSpace(out); got != "0" {
			t.Errorf("header row %s should yield 0, got %q", r, got)
		}
	}
}

func TestBodyLineForClick_body_no_overflow(t *testing.T) {
	// row 3 is the first body line; scroll 0.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"3", "0", "10", "5"}, nil)
	if got := strings.TrimSpace(out); got != "1" {
		t.Errorf("row 3 should map to body line 1, got %q", got)
	}
	out, _ = runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"4", "0", "10", "5"}, nil)
	if got := strings.TrimSpace(out); got != "2" {
		t.Errorf("row 4 should map to body line 2, got %q", got)
	}
}

func TestBodyLineForClick_past_content_yields_zero(t *testing.T) {
	// total=5, so row 10 (body line 8) is beyond the content.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"10", "0", "10", "5"}, nil)
	if got := strings.TrimSpace(out); got != "0" {
		t.Errorf("click past content should yield 0, got %q", got)
	}
}

func TestBodyLineForClick_applies_scroll_offset(t *testing.T) {
	// overflowing: scroll 4, row 3 -> body line 5.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"3", "4", "9", "20"}, nil)
	if got := strings.TrimSpace(out); got != "5" {
		t.Errorf("row 3 at scroll 4 should map to body line 5, got %q", got)
	}
}

func TestBodyLineForClick_status_row_yields_zero(t *testing.T) {
	// avail=9 -> body view rows are 1..9 (screen rows 3..11); the status row at
	// screen row 12 (view row 10) is past avail and must not open a file.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"12", "4", "9", "20"}, nil)
	if got := strings.TrimSpace(out); got != "0" {
		t.Errorf("status row should yield 0, got %q", got)
	}
}

// When the branch+plan heading is wider than the pane it WRAPS to extra screen
// rows, pushing the body down. body_line_for_click takes the actual header
// height (5th arg) so the click→row mapping stays correct; omitting it keeps the
// historical 2-row assumption for existing callers/tests.
func TestBodyLineForClick_wrapped_header_offsets_body(t *testing.T) {
	// header_rows=3 (heading wrapped onto 2 rows + separator): screen rows 1-3
	// are the header, so row 3 is still header (0) and row 4 is body line 1.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"3", "0", "10", "5", "3"}, nil)
	if got := strings.TrimSpace(out); got != "0" {
		t.Errorf("row 3 with a 3-row header should yield 0, got %q", got)
	}
	out, _ = runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"4", "0", "10", "5", "3"}, nil)
	if got := strings.TrimSpace(out); got != "1" {
		t.Errorf("row 4 with a 3-row header should map to body line 1, got %q", got)
	}
}

func TestBodyLineForClick_two_wrapped_header_rows(t *testing.T) {
	// header_rows=4 (heading wrapped onto 3 rows + separator): row 4 is still
	// header (0), row 5 is body line 1.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"4", "0", "10", "5", "4"}, nil)
	if got := strings.TrimSpace(out); got != "0" {
		t.Errorf("row 4 with a 4-row header should yield 0, got %q", got)
	}
	out, _ = runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"5", "0", "10", "5", "4"}, nil)
	if got := strings.TrimSpace(out); got != "1" {
		t.Errorf("row 5 with a 4-row header should map to body line 1, got %q", got)
	}
}

func TestBodyLineForClick_omitted_header_rows_defaults_to_two(t *testing.T) {
	// Backward compatibility: with no 5th arg the body starts at screen row 3.
	out, _ := runBashFunc(t, "lib/compact-view.sh", "body_line_for_click",
		[]string{"3", "0", "10", "5"}, nil)
	if got := strings.TrimSpace(out); got != "1" {
		t.Errorf("row 3 with no header arg should map to body line 1, got %q", got)
	}
}

// body_line_for_click also stores its result in the global BODY_LINE so the hover
// hot path can read it without a $() subshell fork (that fork cost ~8ms/event
// under load and made the selection bar crawl). Assert the global matches the
// printed value for both a hit and a miss.
func TestBodyLineForClick_sets_body_line_global(t *testing.T) {
	mod := filepath.Join(projectRoot(t), "lib", "compact-view.sh")
	hit := "source " + mod + "; body_line_for_click 4 0 10 5 2 >/dev/null; echo \"BL=$BODY_LINE\""
	out, code := runBashSnippet(t, hit, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "BL=2")

	miss := "source " + mod + "; body_line_for_click 1 0 10 5 2 >/dev/null; echo \"BL=$BODY_LINE\""
	out, _ = runBashSnippet(t, miss, nil)
	assertContains(t, out, "BL=0")
}

// header_rows_for converts the heading's visible column width and the pane width
// into the number of SCREEN rows the pinned header occupies: ceil(vis/w) wrapped
// heading rows plus the one-row separator. It is the single source of truth for
// the body's vertical offset once the heading can wrap.
func TestHeaderRowsFor_fits_one_row(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "header_rows_for",
		[]string{"40", "80"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "2" {
		t.Errorf("vis 40 in width 80 should be 2 rows, got %q", got)
	}
}

func TestHeaderRowsFor_exact_width_stays_one_row(t *testing.T) {
	// A heading exactly as wide as the pane does NOT wrap (pending-wrap margin).
	out, _ := runBashFunc(t, "lib/compact-view.sh", "header_rows_for",
		[]string{"80", "80"}, nil)
	if got := strings.TrimSpace(out); got != "2" {
		t.Errorf("vis 80 in width 80 should be 2 rows, got %q", got)
	}
}

func TestHeaderRowsFor_overflow_adds_a_row(t *testing.T) {
	out, _ := runBashFunc(t, "lib/compact-view.sh", "header_rows_for",
		[]string{"81", "80"}, nil)
	if got := strings.TrimSpace(out); got != "3" {
		t.Errorf("vis 81 in width 80 should be 3 rows, got %q", got)
	}
}

func TestHeaderRowsFor_double_overflow_adds_two_rows(t *testing.T) {
	out, _ := runBashFunc(t, "lib/compact-view.sh", "header_rows_for",
		[]string{"161", "80"}, nil)
	if got := strings.TrimSpace(out); got != "4" {
		t.Errorf("vis 161 in width 80 should be 4 rows, got %q", got)
	}
}

func TestHeaderRowsFor_empty_heading_is_two_rows(t *testing.T) {
	out, _ := runBashFunc(t, "lib/compact-view.sh", "header_rows_for",
		[]string{"0", "80"}, nil)
	if got := strings.TrimSpace(out); got != "2" {
		t.Errorf("vis 0 in width 80 should be 2 rows, got %q", got)
	}
}

// open_diff_popup floats a whole-window tmux popup running the full-file diff
// for the clicked path, piped through less. It builds the popup command; the
// actual rendering is tmux's job (mocked here).

func TestOpenDiffPopup_builds_whole_file_diff_popup(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	env := buildEnv(t, []string{binDir})
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := "source " + module + " && open_diff_popup /proj lib/x.sh"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup: %v\n%s", err, out)
	}
	got := string(out)
	assertContains(t, got, "display-popup")
	assertContains(t, got, "diff HEAD -U999999")
	assertContains(t, got, "lib/x.sh")
	assertContains(t, got, "color=never")
}

func TestOpenDiffPopup_quotes_path_with_spaces(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	env := buildEnv(t, []string{binDir})
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	// Pass a path containing a space; open_diff_popup must shell-quote it so the
	// popup command treats it as a single argument.
	script := "source " + module + " && open_diff_popup /proj 'a dir/my file.sh'"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup: %v\n%s", err, out)
	}
	got := string(out)
	// The space must be escaped (a\ dir / my\ file.sh) rather than passed raw.
	if !strings.Contains(got, `my\ file.sh`) && !strings.Contains(got, `'my file.sh'`) {
		t.Errorf("path with spaces should be shell-quoted in the popup command:\n%q", got)
	}
}

// The diff pager themes itself from --ai-tool. open_diff_popup must forward the
// active tool (exported as WISP_DECK_TOOL on the session) so OpenCode sessions
// render the purple chrome instead of falling back to Claude's orange.
func TestOpenDiffPopup_forwards_opencode_tool(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	env := buildEnv(t, []string{binDir}, "WISP_DECK_TOOL=opencode")
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := "source " + module + " && open_diff_popup /proj lib/x.sh"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup: %v\n%s", err, out)
	}
	assertContains(t, string(out), "--ai-tool opencode")
}

func TestOpenDiffPopup_defaults_tool_to_claude(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	// No WISP_DECK_TOOL in the environment — the pager should default to claude.
	env := buildEnv(t, []string{binDir})
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := "source " + module + " && open_diff_popup /proj lib/x.sh"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup: %v\n%s", err, out)
	}
	assertContains(t, string(out), "--ai-tool claude")
}

// The diff popup runs FULL-SCREEN and BORDERLESS: the rounded orange box and its
// click-to-close margin are drawn by the wisp-deck-tui diff-view pager (tmux
// swallows clicks outside a smaller popup, so the pager must own the whole
// window). The redundant header block (diff --git / index / --- / +++ / the hunk
// @@ line) is still stripped so content starts at the top; the filename is
// passed as the viewer's --title.
func TestOpenDiffPopup_uses_diff_viewer(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	env := buildEnv(t, []string{binDir})
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := "source " + module + " && open_diff_popup /proj lib/x.sh"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup: %v\n%s", err, out)
	}
	got := string(out)

	// Full-screen, borderless popup — the pager draws its own (orange) frame and
	// owns the margin so a click outside the box can close it.
	assertContains(t, got, "-B")
	assertContains(t, got, "100%")

	// Strip the redundant diff header: drop everything through the first @@.
	assertContains(t, got, "awk")
	assertContains(t, got, "/@@/")

	// Rendered by the Go pager (closes on Esc), with the file as its title.
	assertContains(t, got, "wisp-deck-tui diff-view")
	assertContains(t, got, "--title")

	// A screen snapshot is captured and handed to the pager so it can show what's
	// behind the full-screen popup dimmed in the margin.
	assertContains(t, got, "--backdrop-file")

	// The pager header now shows the path + added/deleted counts itself, so the
	// popup carries no redundant "git diff:" border-title label.
	assertNotContains(t, got, "git diff:")
}

// The header filter must drop the diff --git / index / --- / +++ metadata AND
// the @@ hunk header (matching even when the @@ line is wrapped in ANSI color),
// leaving only the file content (context + added/removed lines).
func TestOpenDiffPopup_strips_diff_header_lines(t *testing.T) {
	dir := t.TempDir()
	sample := "diff --git a/x b/x\n" +
		"index 111aaa..222bbb 100644\n" +
		"--- a/x\n" +
		"+++ b/x\n" +
		"\x1b[36m@@ -1,2 +1,2 @@\x1b[m\n" +
		" context line\n" +
		"-removed\n" +
		"+added\n"
	path := writeTempFile(t, dir, "diff.txt", sample)
	out, code := runBashSnippet(t, "awk 'f;/@@/{f=1}' "+path, nil)
	assertExitCode(t, code, 0)
	// metadata + hunk header gone
	assertNotContains(t, out, "diff --git")
	assertNotContains(t, out, "index 111aaa")
	assertNotContains(t, out, "--- a/x")
	assertNotContains(t, out, "+++ b/x")
	assertNotContains(t, out, "@@ -1,2")
	// content preserved
	assertContains(t, out, "context line")
	assertContains(t, out, "-removed")
	assertContains(t, out, "+added")
}

// enter_ui_mode must also enable any-motion mouse tracking (\033[?1003h) so the
// ledger receives hover (no-button) motion reports and can highlight the file
// row under the cursor; exit_ui_mode must turn it back off (\033[?1003l).

func TestEnterUiMode_enables_motion_tracking_for_hover(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "enter_ui_mode",
		[]string{"1"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[?1003h") // any-motion tracking -> hover events
}

func TestExitUiMode_disables_motion_tracking(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "exit_ui_mode",
		[]string{"1"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[?1003l")
}

// highlight_body_line wraps the Nth (1-based) line of a body in a hover style,
// re-asserting that style after every internal ANSI reset so it spans the whole
// row (the file rows carry their own \033[0m resets between the +/- columns and
// the name). Other lines pass through untouched; an out-of-range line index
// leaves the body unchanged.

func highlightBodyLine(t *testing.T, body, line, style string) string {
	t.Helper()
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := `source "$0" && highlight_body_line "$1" "$2" "$3"`
	cmd := exec.Command("bash", "-c", script, module, body, line, style)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("highlight_body_line: %v\n%s", err, out)
	}
	return string(out)
}

func TestHighlightBodyLine_styles_target_line(t *testing.T) {
	body := "plain\n\x1b[32m+2\x1b[0m hi\nlast"
	out := highlightBodyLine(t, body, "2", "7")
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[1], "\x1b[7m") {
		t.Errorf("target line should start with the hover style \\x1b[7m, got %q", lines[1])
	}
	if !strings.HasSuffix(lines[1], "\x1b[0m") {
		t.Errorf("target line should end with a reset, got %q", lines[1])
	}
}

func TestHighlightBodyLine_reasserts_internal_resets(t *testing.T) {
	// The internal \x1b[0m must be followed by a re-assertion of the style so the
	// highlight keeps spanning the rest of the row.
	body := "\x1b[32m+2\x1b[0m hi"
	out := highlightBodyLine(t, body, "1", "7")
	if !strings.Contains(out, "\x1b[0m\x1b[7m") {
		t.Errorf("internal reset should be followed by re-asserted style, got %q", out)
	}
}

// Regression: the re-assertion must emit REAL escape bytes, not the literal
// text "$'\033'". zsh (the pane's shell) does NOT interpret a $'...' literal
// inside the replacement half of ${var//pat/repl}; bash does. So the function
// must build the replacement from a variable holding real ESC bytes, or the row
// shows raw "$'\033'[0m" garbage when hovered. Run under BOTH shells.
func TestHighlightBodyLine_emits_real_escapes_not_literal_under_both_shells(t *testing.T) {
	body := "\x1b[32m+2\x1b[0m hi"
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Logf("%s not available, skipping", sh)
			continue
		}
		root := projectRoot(t)
		module := filepath.Join(root, "lib", "compact-view.sh")
		script := `source "$0" && highlight_body_line "$1" "$2" "$3"`
		cmd := exec.Command(sh, "-c", script, module, body, "1", "7")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[%s] highlight_body_line: %v\n%s", sh, err, out)
		}
		if strings.Contains(string(out), `$'`) || strings.Contains(string(out), `\033`) {
			t.Errorf("[%s] highlight leaked a literal escape token, got %q", sh, string(out))
		}
		if !strings.Contains(string(out), "\x1b[0m\x1b[7m") {
			t.Errorf("[%s] expected real re-asserted escapes, got %q", sh, string(out))
		}
	}
}

func TestHighlightBodyLine_leaves_other_lines_untouched(t *testing.T) {
	body := "alpha\nbravo\ncharlie"
	out := highlightBodyLine(t, body, "2", "7")
	lines := strings.Split(out, "\n")
	if lines[0] != "alpha" || lines[2] != "charlie" {
		t.Errorf("non-target lines must be unchanged, got %q", lines)
	}
}

// nth_line extracts the Nth (1-based) line of a newline-delimited string into the
// global NTH_LINE WITHOUT forking — the hover hot path calls it per mouse-motion
// event, and the old `printf | sed -n` forked a subprocess every time, which made
// the highlight crawl behind the cursor. Must behave identically under zsh (the
// pane's shell) and bash, preserving empty lines and returning empty out-of-range.
func runNthLine(t *testing.T, sh, text, n string) string {
	t.Helper()
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := `source "$0" && nth_line "$1" "$2" && printf '[%s]' "$NTH_LINE"`
	cmd := exec.Command(sh, "-c", script, module, text, n)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("[%s] nth_line: %v\n%s", sh, err, out)
	}
	return string(out)
}

func TestNthLine_extracts_line_under_both_shells(t *testing.T) {
	cases := []struct {
		name, text, n, want string
	}{
		{"second", "a\nb\nc", "2", "[b]"},
		{"first", "a\nb\nc", "1", "[a]"},
		{"last", "a\nb\nc", "3", "[c]"},
		{"empty middle line preserved", "a\n\nc", "2", "[]"},
		{"out of range", "a\nb", "5", "[]"},
		{"zero index", "a\nb", "0", "[]"},
		{"path with spaces", "app x.sh\nlib/y.sh", "1", "[app x.sh]"},
	}
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Logf("%s not available, skipping", sh)
			continue
		}
		for _, c := range cases {
			t.Run(sh+"/"+c.name, func(t *testing.T) {
				if got := runNthLine(t, sh, c.text, c.n); got != c.want {
					t.Errorf("[%s] nth_line(%q,%s) = %q, want %q", sh, c.text, c.n, got, c.want)
				}
			})
		}
	}
}

// The hovered row's full-width padding (which strips ANSI to measure visible
// width) must work under zsh too, since the strip is now pure shell (no sed).
func TestHighlightBodyLine_pads_to_width_under_both_shells(t *testing.T) {
	body := "\x1b[32m+2\x1b[0m hi" // visible "+2 hi" = 5 cols
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Logf("%s not available, skipping", sh)
			continue
		}
		root := projectRoot(t)
		module := filepath.Join(root, "lib", "compact-view.sh")
		script := `source "$0" && highlight_body_line "$1" "$2" "$3" "$4"`
		cmd := exec.Command(sh, "-c", script, module, body, "1", "48;5;238", "12")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[%s] highlight_body_line: %v\n%s", sh, err, out)
		}
		visible := ansiRe.ReplaceAllString(string(out), "")
		if got := len([]rune(visible)); got != 12 {
			t.Errorf("[%s] padded visible width = %d, want 12: %q", sh, got, visible)
		}
	}
}

// split_content splits the rendered ledger into the 2-line pinned HEADER and the
// scrollable BODY and counts the body lines into BODY_TOTAL — all via globals and
// WITHOUT forking (no `sed`/`wc`). The refresh loop used to derive these with two
// `sed` calls plus `wc | tr` on EVERY loop iteration — including every mouse-motion
// event under any-motion tracking — which (with the per-event tmux size query) made
// the hover highlight crawl ~60ms behind the cursor. Moving it behind a build tick
// AND making it fork-free is the fix; it must behave identically under both shells.
func runSplitContent(t *testing.T, sh, content string) string {
	t.Helper()
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := `source "$0" && split_content "$1" && printf 'R<%s>H<%s>B<%s>T<%s>' "$HEADER_ROWS" "$HEADER" "$BODY" "$BODY_TOTAL"`
	cmd := exec.Command(sh, "-c", script, module, content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("[%s] split_content: %v\n%s", sh, err, out)
	}
	return string(out)
}

func TestSplitContent_splits_and_counts_under_both_shells(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		// Line 1 is the header-row count metadata; lines 2-3 are the pinned
		// heading + separator; the rest is the scrollable body.
		{"basic", "2\nbranch\n────\nA\nB\nC", "R<2>H<branch\n────>B<A\nB\nC>T<3>"},
		{"internal blank preserved", "2\nh1\nh2\nA\n\nB", "R<2>H<h1\nh2>B<A\n\nB>T<3>"},
		{"single body line", "2\nh1\nh2\nonly", "R<2>H<h1\nh2>B<only>T<1>"},
		{"path with spaces in body", "2\nh1\nh2\napp x.sh", "R<2>H<h1\nh2>B<app x.sh>T<1>"},
		{"wrapped heading reports three rows", "3\nh1\nh2\nA\nB", "R<3>H<h1\nh2>B<A\nB>T<2>"},
	}
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Logf("%s not available, skipping", sh)
			continue
		}
		for _, c := range cases {
			t.Run(sh+"/"+c.name, func(t *testing.T) {
				if got := runSplitContent(t, sh, c.content); got != c.want {
					t.Errorf("[%s] split_content(%q) = %q, want %q", sh, c.content, got, c.want)
				}
			})
		}
	}
}

// viewport_slice is on the hover redraw path (one slice per repaint while the list
// overflows), so it must be fork-free (no `sed`) and behave identically under zsh,
// the pane's shell — not just bash.
func TestViewportSlice_under_both_shells(t *testing.T) {
	in := "a\nb\nc\nd\ne\n"
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Logf("%s not available, skipping", sh)
			continue
		}
		root := projectRoot(t)
		module := filepath.Join(root, "lib", "compact-view.sh")
		script := `source "$0" && viewport_slice "$1" "$2"`
		cmd := exec.Command(sh, "-c", script, module, "1", "2")
		cmd.Stdin = strings.NewReader(in)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[%s] viewport_slice: %v\n%s", sh, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "b\nc" {
			t.Errorf("[%s] viewport_slice 1 2 = %q, want %q", sh, got, "b\nc")
		}
	}
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func highlightBodyLineW(t *testing.T, body, line, style, width string) string {
	t.Helper()
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")
	script := `source "$0" && highlight_body_line "$1" "$2" "$3" "$4"`
	cmd := exec.Command("bash", "-c", script, module, body, line, style, width)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("highlight_body_line: %v\n%s", err, out)
	}
	return string(out)
}

// With a width argument, the hovered row must be padded (inside the highlight)
// so the background bar spans the full pane width — from the very left to the
// very right — instead of stopping at the end of the filename.
func TestHighlightBodyLine_pads_highlighted_line_to_full_width(t *testing.T) {
	body := "ab" // visible width 2
	out := highlightBodyLineW(t, body, "1", "48;5;238", "10")
	visible := ansiRe.ReplaceAllString(out, "")
	if got := len([]rune(visible)); got != 10 {
		t.Errorf("padded visible width = %d, want 10: %q", got, visible)
	}
	// The padding spaces must sit under the background (before the final reset).
	if !strings.HasSuffix(out, "        \x1b[0m") { // 8 trailing spaces + reset
		t.Errorf("expected 8 trailing padding spaces before reset, got %q", out)
	}
}

func TestHighlightBodyLine_no_width_does_not_pad(t *testing.T) {
	body := "ab"
	out := highlightBodyLineW(t, body, "1", "48;5;238", "0")
	visible := ansiRe.ReplaceAllString(out, "")
	if visible != "ab" {
		t.Errorf("width 0 should not pad, got visible %q", visible)
	}
}

func TestHighlightBodyLine_out_of_range_unchanged(t *testing.T) {
	body := "alpha\nbravo"
	for _, ln := range []string{"0", "99"} {
		out := highlightBodyLine(t, body, ln, "7")
		if strings.Contains(out, "\x1b[7m") {
			t.Errorf("line %s out of range should not style anything, got %q", ln, out)
		}
	}
}

// The pinned header must state the number of changed files (not just the net
// +/- line stamp), so the user always sees the changeset size at a glance.
func TestCompactView_header_shows_changed_file_count(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "a.txt", "one\n")
	writeTempFile(t, dir, "b.txt", "one\n")
	git("add", "a.txt", "b.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "one\ntwo\n")  // modified
	writeTempFile(t, dir, "b.txt", "one\nthree\n") // modified -> 2 changed files

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")
	out, _ := cmd.CombinedOutput()

	if !strings.Contains(string(out), "2 files") {
		t.Errorf("header should state the changed-file count (\"2 files\"):\n%q", string(out))
	}
}

// The branch heading must show the active subscription/plan (WISP_DECK_PLAN)
// inline next to the branch name, so the ledger always states which plan the
// session is running on. Shown even with no working-tree changes.
func TestCompactView_header_shows_active_plan(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "a.txt", "one\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init") // clean tree -> "no changes"

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm", "WISP_DECK_PLAN=Work Max")
	out, _ := cmd.CombinedOutput()

	if !strings.Contains(string(out), "Work Max") {
		t.Errorf("header should show the active plan (\"Work Max\"):\n%q", string(out))
	}
}

// Regression: the refresh loop must not leak the `w` (pane width) variable to
// stdout. The pane runs the script under zsh, where `local NAME` with no
// assignment on an already-set variable acts as a *display* command and prints
// "NAME=value". With `local w` re-declared each loop iteration that flashed
// "w=141" on screen until the next refresh.
func TestCompactView_does_not_leak_width_variable_under_zsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	// A git repo with a modified tracked file so the view has content to render.
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "app.txt", "one\n")
	git("add", "app.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "app.txt", "one\ntwo\nthree\n")

	// Run the real loop under zsh with a fast refresh so several iterations
	// (and thus the second+ `local w`) happen quickly, then kill it.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c",
		"source "+module+" && compact_view "+dir)
	// Drop TMUX so width comes from `tput cols`; keeps the test deterministic
	// and independent of any attached tmux client.
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")
	out, _ := cmd.CombinedOutput()

	if strings.Contains(string(out), "w=") {
		t.Errorf("width variable leaked to output (saw \"w=\"):\n%q", string(out))
	}
}

// The "new" (untracked) section is excess for a line-change ledger: untracked
// files carry no +/- counts. compact_view must omit it entirely — no "new"
// header, no untracked filenames, and (since the old block declared `local
// display` inside its loop) no leaked "display=..." line under zsh.
func TestCompactView_omits_untracked_new_section(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "app.txt", "one\n")
	git("add", "app.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "app.txt", "one\ntwo\n") // modified (tracked)
	writeTempFile(t, dir, "untrackedonly.txt", "x\n") // untracked

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c",
		"source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")
	out, _ := cmd.CombinedOutput()
	got := string(out)

	if strings.Contains(got, "untrackedonly.txt") {
		t.Errorf("untracked filename should not appear:\n%q", got)
	}
	if strings.Contains(got, "new") {
		t.Errorf("'new' section header should not appear:\n%q", got)
	}
	if strings.Contains(got, "display=") {
		t.Errorf("leaked 'display=' from untracked loop:\n%q", got)
	}
	// Sanity: the modified file IS still rendered.
	if !strings.Contains(got, "app.txt") {
		t.Errorf("modified file app.txt should still appear:\n%q", got)
	}
}

// The header separator line must span the full inner width (a horizontal rule),
// not collapse to a single "─". Regression for `printf '%.*s' "$iw" '─'`, where
// the precision merely truncates the one-char string instead of repeating it —
// rendering a lone dash under the branch heading instead of a side-to-side line.
func TestCompactView_separator_spans_full_width(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "a.txt", "one\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm", "COLUMNS=80")
	out, _ := cmd.CombinedOutput()
	got := string(out)

	// A real horizontal rule repeats the box-drawing dash many times. The buggy
	// single-dash separator yields exactly one. Require a long run.
	if n := strings.Count(got, "─"); n < 20 {
		t.Errorf("separator should span the pane as a full-width rule, got %d '─' chars:\n%q", n, got)
	}
}

// Regression: the ahead/behind marker must render as a REAL ANSI escape, not the
// literal text "\033[36m↑1\033[0m". The color vars are stored as the literal
// string "\033[36m" (backslash-0-3-3), which printf only interprets when it sits
// in the FORMAT string. `ahead_behind` was printed via `printf "%s" "$ahead_behind"`
// — a %s ARGUMENT — where printf does NOT process backslash escapes, so the raw
// "\033[36m↑1\033[0m" leaked onto the branch line. The fix prints it with %b (or
// embeds it in the format) so the escapes are interpreted.
func TestCompactView_ahead_marker_renders_real_escape_not_literal(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// A bare "remote" the local branch tracks, with HEAD one commit ahead of it.
	remote := t.TempDir()
	bare := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := bare.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git("init", "-q")
	git("checkout", "-q", "-b", "master")
	writeTempFile(t, dir, "a.txt", "one\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	git("remote", "add", "origin", remote)
	git("push", "-q", "-u", "origin", "master")
	writeTempFile(t, dir, "a.txt", "one\ntwo\n")
	git("commit", "-q", "-am", "ahead by one") // HEAD now 1 ahead of @{u}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")
	out, _ := cmd.CombinedOutput()
	got := string(out)

	// The bug printed the four literal chars backslash-0-3-3. After the fix the
	// output carries only real ESC (0x1b) bytes.
	if strings.Contains(got, `\033`) {
		t.Errorf("ahead marker leaked literal escape text %q:\n%q", `\033`, got)
	}
	// And the arrow must be there, preceded by a real cyan escape.
	if !strings.Contains(got, "\x1b[36m↑") {
		t.Errorf("expected a real cyan escape before the up-arrow (\\x1b[36m↑):\n%q", got)
	}
}

// Regression: the panel must size itself to ITS OWN pane, not the active pane.
// `tmux display-message -p '#{pane_width}'` with no -t target returns the
// *active* pane's width. In the real layout the AI pane is active and far wider
// than the (left, inactive) compact-view pane, so the panel built a heading and
// separator sized for the wide pane that then WRAPPED across several rows in the
// narrow pane — a wrapped heading and a doubled separator. The fix targets
// "$TMUX_PANE". Asserts the separator fits on a single row of the narrow pane.
func TestCompactView_sizes_to_own_pane_not_active_pane(t *testing.T) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "compact-view.sh")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTempFile(t, dir, "a.txt", "one\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init") // clean tree -> "no changes"

	session := "gtcv_test"
	tmux := func(args ...string) (string, error) {
		t.Helper()
		c := exec.Command(tmuxBin, args...)
		// A standalone server, isolated from any developer tmux.
		c.Env = append(os.Environ(), "TMUX=")
		out, err := c.CombinedOutput()
		return string(out), err
	}
	_, _ = tmux("kill-session", "-t", session)
	t.Cleanup(func() { _, _ = tmux("kill-session", "-t", session) })

	// Pane 0 (left) runs the panel; it will be the NARROW, INACTIVE pane.
	pane0Cmd := "source " + module + " && WISP_DECK_PLAN='Standard Claude' compact_view " + dir
	if out, err := tmux("new-session", "-d", "-s", session, "-x", "160", "-y", "24",
		pane0Cmd); err != nil {
		t.Fatalf("new-session: %v\n%s", err, out)
	}
	// Split off a WIDE pane on the right and focus it, mirroring wisp-deck's
	// layout where the AI pane is active and wider than the compact view.
	widePane, err := tmux("split-window", "-h", "-t", session, "-l", "120",
		"-P", "-F", "#{pane_id}", "sleep", "30")
	if err != nil {
		t.Fatalf("split-window: %v\n%s", err, widePane)
	}
	if out, err := tmux("select-pane", "-t", strings.TrimSpace(widePane)); err != nil {
		t.Fatalf("select-pane: %v\n%s", err, out)
	}

	// Let the panel render at least one refresh tick.
	time.Sleep(1500 * time.Millisecond)

	cap, err := tmux("capture-pane", "-t", session+".0", "-p")
	if err != nil {
		t.Fatalf("capture-pane: %v\n%s", err, cap)
	}

	// Confirm pane 0 really is the narrow one (sanity for the test setup).
	wOut, _ := tmux("display-message", "-p", "-t", session+".0", "#{pane_width}")
	paneW := strings.TrimSpace(wOut)

	// Count rows that are a horizontal rule (a long run of box-drawing dashes).
	// One pinned separator => exactly one such row. The bug sized the rule for
	// the wide active pane, so it wrapped into several full-dash rows here.
	ruleRows := 0
	for _, line := range strings.Split(cap, "\n") {
		if strings.Count(line, "─") >= 10 {
			ruleRows++
		}
	}
	if ruleRows != 1 {
		t.Errorf("expected exactly 1 separator row in the narrow (pane_width=%s) pane, got %d:\n%s",
			paneW, ruleRows, cap)
	}
}

// ── Discard a file from the preview ─────────────────────────────────────────

// should_discard reports (via exit code) whether the diff-view pager left the
// "discard" marker, telling compact_view to revert the file after the popup.
func TestShouldDiscard_true_for_discard_marker(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "decision", "discard")
	_, code := runBashFunc(t, "lib/compact-view.sh", "should_discard",
		[]string{filepath.Join(dir, "decision")}, nil)
	if code != 0 {
		t.Errorf("should_discard exit = %d, want 0 for the 'discard' marker", code)
	}
}

func TestShouldDiscard_false_for_empty_decision(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "decision", "")
	_, code := runBashFunc(t, "lib/compact-view.sh", "should_discard",
		[]string{filepath.Join(dir, "decision")}, nil)
	if code == 0 {
		t.Error("should_discard should be non-zero for an empty decision file")
	}
}

func TestShouldDiscard_false_for_missing_file(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/compact-view.sh", "should_discard",
		[]string{filepath.Join(dir, "absent")}, nil)
	if code == 0 {
		t.Error("should_discard should be non-zero when the decision file is absent")
	}
}

// discardGitRepo sets up a one-commit repo and returns a git runner.
func discardGitRepo(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return git
}

// discard_worktree_file reverts a modified tracked file back to the index
// (git restore -- <file>): the working-tree edit is gone.
func TestDiscardWorktreeFile_reverts_modified_tracked_file(t *testing.T) {
	dir := t.TempDir()
	git := discardGitRepo(t, dir)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "committed\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "committed\nDIRTY\n") // working-tree edit

	_, code := runBashFunc(t, "lib/compact-view.sh", "discard_worktree_file",
		[]string{dir, "a.txt"}, nil)
	if code != 0 {
		t.Fatalf("discard_worktree_file exit = %d, want 0", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(got) != "committed\n" {
		t.Errorf("file not reverted: got %q, want %q", string(got), "committed\n")
	}
}

// A staged change is left in the index; only the further working-tree edit on
// top of it is discarded (git restore restores the worktree FROM the index).
func TestDiscardWorktreeFile_keeps_staged_change(t *testing.T) {
	dir := t.TempDir()
	git := discardGitRepo(t, dir)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "base\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "staged\n")
	git("add", "a.txt") // index now holds "staged\n"
	writeTempFile(t, dir, "a.txt", "staged-and-dirty\n")

	_, code := runBashFunc(t, "lib/compact-view.sh", "discard_worktree_file",
		[]string{dir, "a.txt"}, nil)
	if code != 0 {
		t.Fatalf("discard_worktree_file exit = %d, want 0", code)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "staged\n" {
		t.Errorf("worktree should restore from the index: got %q, want %q", string(got), "staged\n")
	}
	// The staged change is still present in the index.
	c := exec.Command("git", "-C", dir, "show", ":a.txt")
	staged, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git show :a.txt: %v\n%s", err, staged)
	}
	if string(staged) != "staged\n" {
		t.Errorf("staged copy should be intact: got %q, want %q", string(staged), "staged\n")
	}
}

// extractBashFunc returns the source of a bash function `name` (from its
// `name() {` to the matching closing brace) by brace-matching. Group-command
// braces inside the body balance out, so a simple depth counter is enough for
// these functions. Returns "" if the definition isn't found.
func extractBashFunc(src, name string) string {
	start := strings.Index(src, name+"() {")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	return ""
}

// stripBashComments removes whole-line and trailing `#` comments so a fork
// regression check sees only EXECUTED code — a comment may legitimately mention
// `$(` or `sed` while explaining why the code avoids them.
func stripBashComments(body string) string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// A $() command substitution: a `$(` NOT part of `$((` arithmetic expansion
// (which is fork-free). Arithmetic `$((x + y))` must not trip the tripwire.
var cmdSubRE = regexp.MustCompile(`\$\([^(]`)

// The hover/scroll hot path runs ONCE PER buffered mouse report, and under
// any-motion tracking (?1003h) a single fast cursor sweep floods dozens of
// reports at once. The original lag bug was that each event forked a $()
// subshell (~8ms under load) to map the cursor row and to slice the Nth body
// line, so the selection bar crawled a long backlog behind the cursor. These
// three functions were rewritten fork-free — results return via the BODY_LINE /
// NTH_LINE globals, read through a >/dev/null redirect, with no subshell. This
// tripwire fails the instant any of them regains a $() or backtick command
// substitution, which is the precise change that brings the crawl back. It is a
// deterministic guard (no PTY, no timing) so it can never be flaky-disabled.
func TestCompactView_hover_hotpath_stays_fork_free(t *testing.T) {
	srcBytes, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "compact-view.sh"))
	if err != nil {
		t.Fatalf("read compact-view.sh: %v", err)
	}
	src := string(srcBytes)

	for _, fn := range []string{"body_line_for_click", "nth_line", "set_hover_from_row"} {
		body := extractBashFunc(src, fn)
		if body == "" {
			t.Fatalf("could not locate %q in compact-view.sh; if the per-event hover "+
				"function was renamed, point this fork-free tripwire at the new name", fn)
		}
		code := stripBashComments(body)
		if cmdSubRE.MatchString(code) {
			t.Errorf("%s contains a $() command substitution: that forks a subshell on "+
				"EVERY mouse-motion event (~8ms under load) and is exactly what made the "+
				"changeset file list crawl behind the cursor. Keep the hot path fork-free "+
				"— return via a global and read it with a >/dev/null redirect.", fn)
		}
		if strings.Contains(code, "`") {
			t.Errorf("%s contains a backtick command substitution: that forks a subshell "+
				"per mouse event and reintroduces the hover lag. Keep the hot path "+
				"fork-free.", fn)
		}
	}

	// The fork-free contract is more than "no $()": set_hover_from_row must read
	// body_line_for_click's result from the BODY_LINE global via a >/dev/null
	// redirect. If those markers disappear the function is being rewired in a way
	// that almost certainly forks again.
	hover := extractBashFunc(src, "set_hover_from_row")
	if !strings.Contains(hover, "BODY_LINE") || !strings.Contains(hover, ">/dev/null") {
		t.Errorf("set_hover_from_row no longer maps the row via $BODY_LINE + a >/dev/null "+
			"redirect; that fork-free pattern is what keeps the per-event hover cheap.")
	}
}

// ── Multi-select & batch discard ────────────────────────────────────────────
//
// Selection is a newline-delimited set of file PATHS (stable across the 2s
// rebuild and scrolling, unlike body-line indices). These pure helpers manage
// the set and render it; the loop wires them to the x/d/y/n keys.

// cvFuncArgv sources compact-view.sh and calls <fn> with the given args passed
// as REAL positional parameters ("$@"), so newline-delimited selection sets
// survive verbatim — runBashFunc's %q quoting would turn a newline into a
// literal "\n" inside bash double-quotes (see body_path_map's test helper).
func cvFuncArgv(t *testing.T, fn string, args ...string) (string, int) {
	t.Helper()
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")
	script := "source " + module + " && " + fn + ` "$@"`
	cmd := exec.Command("bash", append([]string{"-c", script, "bash"}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %s: %v", fn, err)
		}
	}
	return string(out), code
}

// toggle_selection adds a path when absent and removes it when present, echoing
// the new newline-delimited set.
func TestToggleSelection_adds_when_absent(t *testing.T) {
	out, code := cvFuncArgv(t, "toggle_selection", "", "a.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "a.txt" {
		t.Errorf("adding to empty set: got %q, want %q", out, "a.txt")
	}
}

func TestToggleSelection_appends_second(t *testing.T) {
	out, code := cvFuncArgv(t, "toggle_selection", "a.txt", "b.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "a.txt\nb.txt" {
		t.Errorf("appending: got %q, want %q", out, "a.txt\nb.txt")
	}
}

func TestToggleSelection_removes_when_present(t *testing.T) {
	out, code := cvFuncArgv(t, "toggle_selection", "a.txt\nb.txt\nc.txt", "b.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "a.txt\nc.txt" {
		t.Errorf("removing middle: got %q, want %q", out, "a.txt\nc.txt")
	}
}

func TestToggleSelection_removing_only_member_empties(t *testing.T) {
	out, code := cvFuncArgv(t, "toggle_selection", "a.txt", "a.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("removing sole member: got %q, want empty", out)
	}
}

// A path is matched WHOLE-line: "a.txt" must not match "src/a.txt".
func TestToggleSelection_matches_whole_path_only(t *testing.T) {
	out, code := cvFuncArgv(t, "toggle_selection", "src/a.txt", "a.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "src/a.txt\na.txt" {
		t.Errorf("substring must not match: got %q, want %q", out, "src/a.txt\na.txt")
	}
}

// selection_contains exits 0 when the path is a member, non-zero otherwise.
func TestSelectionContains_true_for_member(t *testing.T) {
	_, code := cvFuncArgv(t, "selection_contains", "a.txt\nb.txt", "b.txt")
	if code != 0 {
		t.Errorf("selection_contains exit = %d, want 0 for a member", code)
	}
}

func TestSelectionContains_false_for_nonmember(t *testing.T) {
	_, code := cvFuncArgv(t, "selection_contains", "a.txt\nb.txt", "c.txt")
	if code == 0 {
		t.Error("selection_contains should be non-zero for a non-member")
	}
}

func TestSelectionContains_false_for_substring(t *testing.T) {
	_, code := cvFuncArgv(t, "selection_contains", "src/a.txt", "a.txt")
	if code == 0 {
		t.Error("selection_contains must match whole paths, not substrings")
	}
}

// selection_count echoes the number of non-empty members.
func TestSelectionCount_counts_members(t *testing.T) {
	out, code := cvFuncArgv(t, "selection_count", "a.txt\nb.txt\nc.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "3" {
		t.Errorf("count: got %q, want 3", out)
	}
}

func TestSelectionCount_empty_is_zero(t *testing.T) {
	out, code := cvFuncArgv(t, "selection_count", "")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "0" {
		t.Errorf("empty count: got %q, want 0", out)
	}
}

// prune_selection drops members no longer present in the valid path set (a file
// that left the changeset), keeping the rest in order.
func TestPruneSelection_drops_missing_keeps_present(t *testing.T) {
	out, code := cvFuncArgv(t, "prune_selection", "a.txt\nb.txt\nc.txt", "a.txt\nc.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "a.txt\nc.txt" {
		t.Errorf("prune: got %q, want %q", out, "a.txt\nc.txt")
	}
}

func TestPruneSelection_all_missing_empties(t *testing.T) {
	out, code := cvFuncArgv(t, "prune_selection", "a.txt\nb.txt", "x.txt\ny.txt")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("prune all-missing: got %q, want empty", out)
	}
}

// discard_prompt is the armed-confirm footer text; "file" is singular at 1.
func TestDiscardPrompt_plural(t *testing.T) {
	out, code := cvFuncArgv(t, "discard_prompt", "3")
	assertExitCode(t, code, 0)
	clean := ansiRE.ReplaceAllString(out, "")
	if !strings.Contains(clean, "Discard 3 files?") || !strings.Contains(clean, "[y/n]") {
		t.Errorf("plural prompt: got %q", clean)
	}
}

func TestDiscardPrompt_singular(t *testing.T) {
	out, code := cvFuncArgv(t, "discard_prompt", "1")
	assertExitCode(t, code, 0)
	clean := ansiRE.ReplaceAllString(out, "")
	if !strings.Contains(clean, "Discard 1 file?") {
		t.Errorf("singular prompt: got %q, want to contain %q", clean, "Discard 1 file?")
	}
	if strings.Contains(clean, "files?") {
		t.Errorf("singular prompt should not say 'files': got %q", clean)
	}
}

// apply_selection_markers marks each selected FILE row (per body_map) with a ✓
// and leaves unselected rows and non-file rows (empty map path) untouched.
func TestApplySelectionMarkers_marks_selected_file_rows(t *testing.T) {
	// A body with a header row, two file rows, a blank row. The map carries a path
	// on the file rows and an empty line on the header/blank rows.
	body := "HEADER\n   +1 −0  a.txt\n   +2 −0  b.txt\n"
	bodyMap := "\na.txt\nb.txt\n"
	out, code := cvFuncArgv(t, "apply_selection_markers", body, bodyMap, "a.txt")
	assertExitCode(t, code, 0)
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected >=3 lines, got %q", out)
	}
	if !strings.Contains(lines[1], "✓") {
		t.Errorf("selected row a.txt should carry a ✓ marker: got %q", lines[1])
	}
	if strings.Contains(lines[2], "✓") {
		t.Errorf("unselected row b.txt must not carry a marker: got %q", lines[2])
	}
	if strings.Contains(lines[0], "✓") {
		t.Errorf("header row must not carry a marker: got %q", lines[0])
	}
}

// The marker must preserve the row's VISIBLE width (it replaces the 3-space
// indent with a 3-column " ✓ "), so column alignment and the hover-highlight
// padding math are unaffected. Compare the stripped-ANSI visible length.
func TestApplySelectionMarkers_preserves_visible_width(t *testing.T) {
	body := "   +1 −0  a.txt"
	bodyMap := "a.txt"
	out, code := cvFuncArgv(t, "apply_selection_markers", body, bodyMap, "a.txt")
	assertExitCode(t, code, 0)
	got := len([]rune(ansiRE.ReplaceAllString(strings.TrimRight(out, "\n"), "")))
	want := len([]rune(body))
	if got != want {
		t.Errorf("visible width changed by marker: got %d, want %d (row %q)",
			got, want, ansiRE.ReplaceAllString(out, ""))
	}
}

// ledger_hint is the dim key-hint shown while the cursor is over a file row, so
// the select/discard keys are discoverable without reading docs.
func TestLedgerHint_none_marked_shows_keys(t *testing.T) {
	out, code := cvFuncArgv(t, "ledger_hint", "0")
	assertExitCode(t, code, 0)
	clean := ansiRE.ReplaceAllString(out, "")
	if !strings.Contains(clean, "x mark") || !strings.Contains(clean, "d discard") {
		t.Errorf("hint should advertise the mark/discard keys: got %q", clean)
	}
}

func TestLedgerHint_marked_shows_count(t *testing.T) {
	out, code := cvFuncArgv(t, "ledger_hint", "2")
	assertExitCode(t, code, 0)
	clean := ansiRE.ReplaceAllString(out, "")
	if !strings.Contains(clean, "2") || !strings.Contains(clean, "d discard") {
		t.Errorf("hint with marked files should show the count and discard key: got %q", clean)
	}
}

// ledger_footer combines the scroll position indicator with the mark/discard
// hint on ONE reserved row, so hovering an overflowing list keeps the scroll
// data visible AND advertises the keys — the hint no longer REPLACES the scroll
// position (see the reserved-footer render in compact_view).
func TestLedgerFooter_shows_scroll_and_hint(t *testing.T) {
	// scroll=10, avail=24, total=34 -> "11-34/34"; 0 files marked.
	out, code := cvFuncArgv(t, "ledger_footer", "10", "24", "34", "0")
	assertExitCode(t, code, 0)
	clean := ansiRE.ReplaceAllString(out, "")
	if !strings.Contains(clean, "11-34/34") {
		t.Errorf("footer must keep the scroll position data: got %q", clean)
	}
	if !strings.Contains(clean, "x mark") || !strings.Contains(clean, "d discard") {
		t.Errorf("footer must also advertise the mark/discard keys: got %q", clean)
	}
}

// With files marked, the footer still shows the scroll data and now reports the
// marked count alongside the keys.
func TestLedgerFooter_shows_scroll_and_marked_count(t *testing.T) {
	out, code := cvFuncArgv(t, "ledger_footer", "10", "24", "34", "2")
	assertExitCode(t, code, 0)
	clean := ansiRE.ReplaceAllString(out, "")
	if !strings.Contains(clean, "11-34/34") {
		t.Errorf("footer must keep the scroll position data: got %q", clean)
	}
	if !strings.Contains(clean, "2") || !strings.Contains(clean, "d discard") {
		t.Errorf("footer must report the marked count and discard key: got %q", clean)
	}
}

// discard_worktree_files reverts every selected path back to HEAD, leaving an
// unselected modified file untouched.
func TestDiscardWorktreeFiles_reverts_all_selected(t *testing.T) {
	dir := t.TempDir()
	git := discardGitRepo(t, dir)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		writeTempFile(t, dir, f, "base\n")
		git("add", f)
	}
	git("commit", "-q", "-m", "init")
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n")
	writeTempFile(t, dir, "b.txt", "base\nDIRTY\n")
	writeTempFile(t, dir, "c.txt", "base\nDIRTY\n") // NOT selected — must stay dirty

	_, code := cvFuncArgv(t, "discard_worktree_files", dir, "a.txt\nb.txt")
	if code != 0 {
		t.Fatalf("discard_worktree_files exit = %d, want 0", code)
	}
	for _, f := range []string{"a.txt", "b.txt"} {
		got, _ := os.ReadFile(filepath.Join(dir, f))
		if string(got) != "base\n" {
			t.Errorf("%s not reverted: got %q, want %q", f, string(got), "base\n")
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "c.txt"))
	if string(got) != "base\nDIRTY\n" {
		t.Errorf("unselected c.txt should be untouched: got %q", string(got))
	}
}
