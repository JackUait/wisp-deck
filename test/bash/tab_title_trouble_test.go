package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tab tells the user at a glance that its session is in trouble, and how bad
// it is. ❌ is a confirmed failure — the turn died. ⚠️ is a possible one — the
// session has stopped reporting after having reported fine, which is where a
// stalled adapter shows up but also where a merely slow agent does, so the
// uncertain case deliberately gets the softer cue.

func TestTui_set_tab_title_error_prepends_a_cross(t *testing.T) {
	modulePath := filepath.Join(projectRoot(t), "lib", "tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title_error "wisp-deck" "claude"`, modulePath)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if want := "\033]0;❌ wisp-deck · claude\007"; out != want {
		t.Errorf("set_tab_title_error output = %q, want %q", out, want)
	}
}

func TestTui_set_tab_title_warning_prepends_a_warning_sign(t *testing.T) {
	modulePath := filepath.Join(projectRoot(t), "lib", "tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title_warning "wisp-deck" "claude"`, modulePath)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if want := "\033]0;⚠️ wisp-deck · claude\007"; out != want {
		t.Errorf("set_tab_title_warning output = %q, want %q", out, want)
	}
}

func TestTabTitleWatcher_apply_tab_title_trouble_states(t *testing.T) {
	tests := []struct{ name, state, mode, want, unwanted string }{
		{"error full", "error", "full", "❌ myproj · claude", "🔔"},
		{"error project", "error", "project", "❌ myproj", "claude"},
		{"warning full", "warning", "full", "⚠️ myproj · claude", "🔔"},
		{"warning project", "warning", "project", "⚠️ myproj", "claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := runBashSnippet(t, tabTitleSnippet(t,
				fmt.Sprintf(`apply_tab_title %q %q myproj claude`, tt.state, tt.mode)), nil)
			assertExitCode(t, code, 0)
			assertContains(t, out, tt.want)
			assertNotContains(t, out, tt.unwanted)
		})
	}
}

// The reason field already travels the protocol; until now the tick rendered
// the phase alone, so a turn that died was indistinguishable from one that
// finished. A failed turn must also stay ❌ after the user looks at the tab:
// the seen swap means "you know about this", which is not the same as fixed.
func TestTabTitleWatcher_a_failed_turn_shows_a_cross_focused_or_not(t *testing.T) {
	tests := []struct{ name, flags, reason, want string }{
		{"error unfocused", "attached,UTF-8", "error", "title:error"},
		{"error focused", "attached,focused,UTF-8", "error", "title:error"},
		{"done unfocused still rings", "attached,UTF-8", "done", "title:waiting"},
		{"question focused is still seen", "attached,focused,UTF-8", "question", "title:seen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			generation := "generation.trouble1"
			state := writeAttentionState(t, root, generation, "1", "attention", tt.reason)
			descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
			logFile := filepath.Join(root, "titles")
			binDir := seenTmuxMock(t, root, tt.flags)
			script := tabTitleSnippet(t, fmt.Sprintf(`
apply_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
keep_awake_tick() { :; }
attention_watcher_reset
attention_watcher_tick sess project full %q %q %q
`, logFile, filepath.Join(binDir, "tmux"), descriptor, root))

			_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
			assertExitCode(t, code, 0)
			data, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			assertContains(t, string(data), tt.want)
		})
	}
}

// apply_tab_title returns early in model mode — the agent named the tab itself —
// so the per-tick model re-emit is a SECOND rendering site, and it carries the
// cue. Miss it and every model-title user sees no trouble cue at all.
func TestTabTitleWatcher_model_title_mode_carries_the_trouble_cue(t *testing.T) {
	root := t.TempDir()
	generation := "generation.trouble2"
	state := writeAttentionState(t, root, generation, "1", "attention", "error")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	writeTempFile(t, root, "settings", "theme=auto\ntab_title=model\n")
	binDir := seenTmuxMock(t, root, "attached,UTF-8")
	script := tabTitleSnippet(t, fmt.Sprintf(`
keep_awake_tick() { :; }
attention_watcher_reset
attention_watcher_tick sess project model %q %q %q
`, filepath.Join(binDir, "tmux"), descriptor, root))

	out, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	assertContains(t, out, "❌ Fixing tests")
	assertNotContains(t, out, "🔔")
}

// A session that has reported fine and then goes quiet is the stalled-adapter
// signal. It must be SUSTAINED: a single missed read is routine, and warning on
// it would flag every healthy session constantly.
func TestTabTitleWatcher_a_session_that_stops_reporting_warns_after_sustained_silence(t *testing.T) {
	root := t.TempDir()
	generation := "generation.trouble3"
	state := writeAttentionState(t, root, generation, "1", "working", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "titles")
	binDir := seenTmuxMock(t, root, "attached,UTF-8")
	script := tabTitleSnippet(t, fmt.Sprintf(`
apply_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
keep_awake_tick() { :; }
attention_watcher_reset
attention_watcher_tick sess project full %q %q %q
rm -f %q
attention_watcher_tick sess project full %q %q %q
printf 'after-one-gap\n' >> %q
attention_watcher_tick sess project full %q %q %q
attention_watcher_tick sess project full %q %q %q
`, logFile,
		filepath.Join(binDir, "tmux"), descriptor, root,
		state,
		filepath.Join(binDir, "tmux"), descriptor, root,
		logFile,
		filepath.Join(binDir, "tmux"), descriptor, root,
		filepath.Join(binDir, "tmux"), descriptor, root))

	env := buildEnv(t, []string{binDir}, "WISP_DECK_WATCH_QUIET_TICKS=3")
	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	before, after := splitAtMarker(t, string(data), "after-one-gap")
	assertNotContains(t, before, "title:warning")
	assertContains(t, after, "title:warning")
}

// A session that has NEVER reported is a launch in progress, not a fault. The
// agent pane can take minutes to publish its first state under load, and
// warning through every cold start would make the cue mean nothing.
func TestTabTitleWatcher_never_warns_before_the_session_has_ever_reported(t *testing.T) {
	root := t.TempDir()
	descriptor := filepath.Join(root, "missing-descriptor")
	logFile := filepath.Join(root, "titles")
	binDir := seenTmuxMock(t, root, "attached,UTF-8")
	body := ""
	for i := 0; i < 5; i++ {
		body += fmt.Sprintf("attention_watcher_tick sess project full %q %q %q\n",
			filepath.Join(binDir, "tmux"), descriptor, root)
	}
	script := tabTitleSnippet(t, fmt.Sprintf(`
apply_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
keep_awake_tick() { :; }
attention_watcher_reset
%s`, logFile, body))

	env := buildEnv(t, []string{binDir}, "WISP_DECK_WATCH_QUIET_TICKS=2")
	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, string(data), "title:warning")
}

// The warning is a live reading, not a latch: a session that starts reporting
// again is well, and the tab must say so without a relaunch.
func TestTabTitleWatcher_a_session_that_reports_again_clears_the_warning(t *testing.T) {
	root := t.TempDir()
	generation := "generation.trouble4"
	state := writeAttentionState(t, root, generation, "1", "working", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "titles")
	binDir := seenTmuxMock(t, root, "attached,UTF-8")
	tmuxPath := filepath.Join(binDir, "tmux")
	tick := fmt.Sprintf("attention_watcher_tick sess project full %q %q %q\n", tmuxPath, descriptor, root)
	script := tabTitleSnippet(t, fmt.Sprintf(`
apply_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
keep_awake_tick() { :; }
attention_watcher_reset
%[2]smv %[3]q %[3]q.away
%[2]s%[2]sprintf 'recovered\n' >> %[1]q
mv %[3]q.away %[3]q
%[2]s`, logFile, tick, state))

	env := buildEnv(t, []string{binDir}, "WISP_DECK_WATCH_QUIET_TICKS=2")
	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	before, after := splitAtMarker(t, string(data), "recovered")
	assertContains(t, before, "title:warning")
	assertNotContains(t, after, "title:warning")
}

// Split a tick log around a marker line, so a test can assert what the tab said
// before an event and after it.
func splitAtMarker(t *testing.T, log, marker string) (string, string) {
	t.Helper()
	before, after, found := strings.Cut(log, marker)
	if !found {
		t.Fatalf("marker %q not found in log:\n%s", marker, log)
	}
	return before, after
}
