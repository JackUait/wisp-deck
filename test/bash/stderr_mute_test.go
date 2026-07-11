package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The session terminal is the surface the AI tool paints its full-screen UI
// onto, and it is also wrapper.sh's stderr. So ANY line any lib writes to
// stderr after tmux takes over lands in the middle of Claude's UI — most
// visibly inside its input box.
//
// Until now the invariant "background jobs must not write to the terminal" was
// re-stated by hand at every call site (`2>/dev/null` on each backgrounded
// function, and inside the libs those jobs call). One miss is enough: the
// keep-awake reaper's failing read printed
//
//	keep-awake.sh: line 79: .../keep-awake.d/<session>: No such file or directory
//
// straight into the input box. Patching that one read (see redirect_leak_test.go)
// fixes that one line; it does not stop the next one.
//
// gt_mute_terminal_stderr closes the door once, at the wrapper: fd 2 is pointed
// at a per-session log before any background job starts, so a lib that forgets
// its redirect can no longer reach the terminal at all — and the error is kept
// on disk instead of being thrown away.
func TestMuteTerminalStderr_keeps_later_stderr_off_the_terminal(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "logs", "dev-proj-123.log")
	tui := filepath.Join(projectRoot(t), "lib", "tui.sh")

	// The runner captures fd1+fd2 of this bash — i.e. exactly what the terminal
	// would have shown. Nothing after the mute may appear there.
	out, code := runBashSnippet(t, `
		source "`+tui+`"
		gt_mute_terminal_stderr "`+log+`"
		echo "a stray lib error" >&2
		bash -c 'echo "an error from a child process" >&2'
		cat /definitely/not/a/file
		echo "stdout still works"
	`, nil)

	assertExitCode(t, code, 0)
	assertContains(t, out, "stdout still works")
	assertNotContains(t, out, "a stray lib error")
	assertNotContains(t, out, "an error from a child process")
	assertNotContains(t, out, "No such file or directory")

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("session error log was not created: %v", err)
	}
	logged := string(data)
	for _, want := range []string{
		"a stray lib error",
		"an error from a child process",
		"No such file or directory", // the shell's own redirect failures, too
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("stderr was silenced but not logged: %q missing from %s\n%s", want, log, logged)
		}
	}
}

// A session must launch even when its log cannot be written (read-only or
// missing config dir). Muting is the point; logging is the bonus. It must never
// take the session down with it — and it must still not fall back to printing
// on the terminal.
func TestMuteTerminalStderr_survives_an_unwritable_log(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(blocked, "logs", "dev-proj-123.log") // parent is a file
	tui := filepath.Join(projectRoot(t), "lib", "tui.sh")

	out, code := runBashSnippet(t, `
		set -e
		source "`+tui+`"
		gt_mute_terminal_stderr "`+log+`"
		echo "a stray lib error" >&2
		echo "session still launched"
	`, nil)

	assertExitCode(t, code, 0)
	assertContains(t, out, "session still launched")
	assertNotContains(t, out, "a stray lib error")
}

// The mute is only worth anything if it happens BEFORE the jobs that outlive
// the interactive phase. Each of these backgrounds a loop that runs for the
// whole session; the keep-awake reaper that started this bug is called from the
// first of them. A future job added below the mute is covered automatically —
// one added above it is not, so pin the order.
func TestWrapperMutesTerminalStderrBeforeStartingBackgroundJobs(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")

	// First non-comment occurrence of each, by line number.
	find := func(re *regexp.Regexp) int {
		for i, line := range lines {
			code := line
			if idx := strings.Index(code, "#"); idx >= 0 {
				code = code[:idx]
			}
			if re.MatchString(code) {
				return i + 1
			}
		}
		return -1
	}

	mute := find(regexp.MustCompile(`gt_mute_terminal_stderr`))
	if mute < 0 {
		t.Fatal("wrapper.sh never mutes the session terminal's stderr; a lib that " +
			"forgets a redirect will print into the AI tool's UI")
	}

	for _, job := range []struct{ name, pattern string }{
		{"the AI-pane focus watcher", `gt_focus_ai_pane_when_ready`},
		{"the tab-title watcher (calls keep_awake_tick)", `start_tab_title_watcher`},
		{"the snapshot heartbeat", `run_snapshot_heartbeat`},
		{"the tmux session", `new-session`},
	} {
		at := find(regexp.MustCompile(job.pattern))
		if at < 0 {
			t.Errorf("%s (%s) not found in wrapper.sh", job.name, job.pattern)
			continue
		}
		if at < mute {
			t.Errorf("%s starts at line %d, before stderr is muted at line %d — "+
				"anything it writes to stderr lands on the session terminal, on top "+
				"of the AI tool's UI", job.name, at, mute)
		}
	}
}
