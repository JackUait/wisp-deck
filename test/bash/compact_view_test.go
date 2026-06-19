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
