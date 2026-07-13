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

	// Every lib, not just the ones the wrapper backgrounds itself: a job spawned
	// from a lib inherits the terminal just the same, and update.sh's disowned
	// npm check outlives the launch by design — the exact shape of this bug.
	files := []string{"wrapper.sh"}
	libs, err := filepath.Glob(filepath.Join(root, "lib", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range libs {
		rel, _ := filepath.Rel(root, l)
		// The one job that legitimately owns the terminal's stdout: the launch
		// splash. Its frames ARE its output, it runs before the picker, and
		// stop_loading_screen kills it at the handoff — it never coexists with
		// the AI tool's UI.
		if rel == "lib/loading.sh" {
			continue
		}
		files = append(files, rel)
	}

	for _, rel := range files {
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
	generation := "generation.stdout1"
	stateFile := writeAttentionState(t, dir, generation, "0", "working", "-")
	descriptor := writeAttentionDescriptor(t, dir, generation, "claude", stateFile)
	workingTick := filepath.Join(dir, "working-tick")
	semanticTick := filepath.Join(dir, "semantic-tick")
	errorLog := filepath.Join(dir, "watcher.log")

	// A current-format tmux pane record also witnesses that the watcher consumed
	// the initial working snapshot before the test publishes attention.
	binDir := mockCommand(t, dir, "tmux", `
case "$1" in
  list-panes) touch "$WORKING_TICK"; printf '%%1\t1\n' ;;
  *) : ;;
esac
`)
	env := buildEnv(t, []string{binDir},
		"WORKING_TICK="+workingTick,
		"SEMANTIC_TICK="+semanticTick,
		"WISP_DECK_ERROR_LOG="+errorLog,
		"WISP_DECK_WATCH_INTERVAL=0.05")

	// A semantic attention transition calls the notification lib. Redefining it
	// after sourcing stands in for any library on that path writing by mistake.
	// The marker is written last, so observing it proves both stray writes ran.
	out, code := runBashSnippet(t, `
		source "`+filepath.Join(root, "lib", "tui.sh")+`"
		source "`+filepath.Join(root, "lib", "tab-title-watcher.sh")+`"

		play_notification_sound() {
			echo "STRAY LINE FROM A LIB"
			echo "STRAY ERROR FROM A LIB" >&2
			touch "$SEMANTIC_TICK"
		}

		start_tab_title_watcher "sess" "proj" "project" "tmux" "`+descriptor+`" "`+dir+`"
		for _i in {1..100}; do [ -f "$WORKING_TICK" ] && break; sleep 0.02; done
		if [ ! -f "$WORKING_TICK" ]; then
			stop_tab_title_watcher
			exit 70
		fi
		printf '1\t`+generation+`\t1\tattention\tdone\n' > "`+stateFile+`.tmp"
		mv "`+stateFile+`.tmp" "`+stateFile+`"
		for _i in {1..100}; do [ -f "$SEMANTIC_TICK" ] && break; sleep 0.02; done
		stop_tab_title_watcher
		[ -f "$SEMANTIC_TICK" ] || exit 71
	`, env)

	assertExitCode(t, code, 0)
	if _, err := os.Stat(workingTick); err != nil {
		t.Fatalf("watcher never consumed the working semantic state: %v", err)
	}
	if _, err := os.Stat(semanticTick); err != nil {
		t.Fatalf("watcher never reached the injected semantic attention tick: %v", err)
	}
	assertNotContains(t, out, "STRAY LINE FROM A LIB")
	assertNotContains(t, out, "STRAY ERROR FROM A LIB")
	logged, err := os.ReadFile(errorLog)
	if err != nil {
		t.Fatalf("watcher error log missing: %v", err)
	}
	assertContains(t, string(logged), "STRAY ERROR FROM A LIB")
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
