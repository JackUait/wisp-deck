package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Companion to stderr_mute_test.go. Muting stderr closed one of the two pipes
// into the AI tool's UI; this closes the other.
//
// Every job wrapper.sh backgrounds keeps running for the whole session with its
// stdout still pointed at the session terminal — the surface tmux hands to the
// AI tool for its full-screen UI. A single stray `echo` from any lib any of
// those loops calls (they call a lot: keep-awake, theme, notifications, tmux
// helpers) prints into the middle of that UI, exactly as the keep-awake
// reaper's failing read did on stderr.
//
// The tab-title watcher is the one job with a legitimate reason to reach the
// terminal — the OSC title escape — and it now does so explicitly via /dev/tty
// (set_tab_title), so its stdout can be dropped like everyone else's.
func TestWrapperBackgroundJobsDoNotWriteToTheTerminal(t *testing.T) {
	root := projectRoot(t)

	// A backgrounded command whose stdout still goes to the terminal.
	// `>/dev/null`, `>>"$log"`, `&>/dev/null` and `>&3` all count as redirected.
	backgrounded := regexp.MustCompile(`&\s*$`)
	stdoutRedirected := regexp.MustCompile(`(^|[^0-9<>&])>[>&]?\s*\S|&>`)

	for _, rel := range []string{"wrapper.sh", "lib/tab-title-watcher.sh", "lib/session-restore.sh"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if idx := strings.Index(code, "#"); idx >= 0 {
				code = code[:idx]
			}
			code = strings.TrimSpace(code)
			// `&&`, `2>&1` and a bare `&` disown are not job backgrounding.
			if !backgrounded.MatchString(code) || strings.HasSuffix(code, "&&") {
				continue
			}
			if stdoutRedirected.MatchString(code) {
				continue
			}
			t.Errorf("%s:%d: this job is backgrounded for the whole session with its "+
				"stdout still on the session terminal — anything it prints lands on top "+
				"of the AI tool's UI.\n    %s\n    Add >/dev/null (the tab title goes out "+
				"via /dev/tty in set_tab_title, not stdout).", rel, i+1, strings.TrimSpace(line))
		}
	}
}

// The tab-title watcher calls into a dozen libs on every 0.5s tick. If one of
// them ever echoes, it must not reach the terminal — while the tab title, the
// loop's one legitimate output, must still get there.
func TestTabTitleWatcherSwallowsStrayOutputFromTheLibsItCalls(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()

	// A tmux stub that reports one pane, so the watcher gets past pane discovery
	// and into the loop body, where our leaky lib is called.
	binDir := mockCommand(t, dir, "tmux", `
case "$1" in
  list-panes)  echo "0 0" ;;
  capture-pane) echo "> " ;;
  *) : ;;
esac
`)
	env := buildEnv(t, []string{binDir})

	// check_ai_tool_state is called every tick from inside the loop. Redefining
	// it AFTER the module is sourced stands in for any lib on that path echoing.
	out, code := runBashSnippet(t, `
		source "`+filepath.Join(root, "lib", "tui.sh")+`"
		source "`+filepath.Join(root, "lib", "tab-title-watcher.sh")+`"

		check_ai_tool_state() {
			echo "STRAY LINE FROM A LIB"
			echo "STRAY ERROR FROM A LIB" >&2
			echo "waiting"
		}
		play_notification_sound() { :; }

		start_tab_title_watcher "sess" "claude" "proj" "project" "tmux" "`+dir+`/marker" ""
		sleep 2
		stop_tab_title_watcher
	`, env)

	assertExitCode(t, code, 0)
	assertNotContains(t, out, "STRAY LINE FROM A LIB")
	assertNotContains(t, out, "STRAY ERROR FROM A LIB")
}

// set_tab_title must reach the terminal directly rather than through stdout, so
// that the watcher's stdout can be dropped without losing the title. Outside a
// terminal (CI, `go test`) there is no /dev/tty to write to, and it must fall
// back to stdout rather than failing or printing an error.
func TestSetTabTitleFallsBackToStdoutWithoutATty(t *testing.T) {
	out, code := runBashFunc(t, "lib/tui.sh", "set_tab_title", []string{"proj", "claude"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "proj · claude")
}
