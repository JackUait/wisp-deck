package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A backgrounded job inherits the stderr of the shell that spawned it. For
// everything wrapper.sh spawns, that stderr is the session terminal — the one
// tmux hands to the AI tool, which paints a full-screen UI on it. These jobs
// then outlive their launch and keep ticking for the whole session, so a single
// stray line from any of them lands in the middle of that UI, at some
// unpredictable moment, long after the code that emitted it ran.
//
// That is exactly how the keep-awake reaper's failing read ended up printing
//
//	keep-awake.sh: line 79: .../keep-awake.d/<session>: No such file or directory
//
// into Claude's input box. Silencing that one read fixed that one trigger; it
// left the channel wide open. The snapshot heartbeat re-sources the lib in a
// fresh bash on every tick, so a future typo in ANY sourced file becomes a
// syntax error painted onto the user's session — no new bug required.
//
// So the invariant is the channel, not the trigger: nothing that runs in the
// background may write to the session terminal's stderr. Nothing out there is
// reading it; the only thing it can do is corrupt the display. Redirect it at
// the point the job is spawned (`) 2>/dev/null &`), where it holds for every
// line the job will ever run, rather than trusting each of them to stay quiet.
//
// Stdout is the other half of the same channel, and is covered by its companion,
// TestWrapperBackgroundJobsDoNotWriteToTheTerminal in stdout_leak_test.go: the
// tab-title escape — the one thing a background job legitimately needs the
// terminal for — is addressed to /dev/tty in set_tab_title, so no background
// job needs stdout either.
//
// The redirect here is belt to the wrapper's braces: wrapper.sh points its own
// fd 2 at a session log before spawning anything (gt_mute_terminal_stderr), so
// these jobs are already safe. Keeping it at the spawn point too means a job
// stays safe even if it is ever moved, copied, or spawned from a process that
// has not muted itself — and "$WISP_DECK_ERROR_LOG" keeps the error readable
// instead of dumping it in /dev/null.
func TestBackgroundJobsDoNotInheritTerminalStderr(t *testing.T) {
	root := projectRoot(t)

	files := []string{filepath.Join(root, "wrapper.sh")}
	libs, err := filepath.Glob(filepath.Join(root, "lib", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, libs...)

	// A line that ends in a bare `&` spawns a background job. (`&&` is not a
	// background op; a trailing `\` continuation is not the end of the command.)
	spawns := regexp.MustCompile(`[^&>|\\]&[[:space:]]*$`)
	// Any form that takes stderr off the terminal: 2>/dev/null, 2>&1, 2>"$log".
	muted := regexp.MustCompile(`2>`)

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if idx := strings.Index(code, "#"); idx >= 0 {
				code = code[:idx]
			}
			code = strings.TrimRight(code, " \t")
			if !spawns.MatchString(code) || muted.MatchString(code) {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s:%d: background job inherits the session terminal's stderr, so "+
				"anything it ever prints lands on top of the AI tool's UI.\n"+
				"    %s\n"+
				"    Redirect at the spawn point:  <job> 2>/dev/null &",
				rel, i+1, strings.TrimSpace(line))
		}
	}
}
