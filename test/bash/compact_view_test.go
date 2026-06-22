package bash_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// The branch heading must show the active subscription/plan (GHOST_TAB_PLAN)
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
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm", "GHOST_TAB_PLAN=Work Max")
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
	pane0Cmd := "source " + module + " && GHOST_TAB_PLAN='Standard Claude' compact_view " + dir
	if out, err := tmux("new-session", "-d", "-s", session, "-x", "160", "-y", "24",
		pane0Cmd); err != nil {
		t.Fatalf("new-session: %v\n%s", err, out)
	}
	// Split off a WIDE pane on the right and focus it, mirroring ghost-tab's
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
