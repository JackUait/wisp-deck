package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tab that is waiting on the user rings (🔔). Once the user has actually
// looked at the tab without answering, the bell has done its job and the title
// switches to the quieter "seen" cue (👀) so a glance at the tab strip
// separates "you have not looked yet" from "you looked and left it hanging".

func TestTui_set_tab_title_seen_prepends_eyes(t *testing.T) {
	modulePath := filepath.Join(projectRoot(t), "lib", "tui.sh")
	script := fmt.Sprintf(`source %q && set_tab_title_seen "wisp-deck" "claude"`, modulePath)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if want := "\033]0;👀 wisp-deck · claude\007"; out != want {
		t.Errorf("set_tab_title_seen output = %q, want %q", out, want)
	}
}

func TestTabTitleWatcher_apply_tab_title_seen_state(t *testing.T) {
	tests := []struct{ name, mode, want, unwanted string }{
		{"full", "full", "👀 myproj · claude", "🔔"},
		{"project", "project", "👀 myproj", "claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := runBashSnippet(t, tabTitleSnippet(t,
				fmt.Sprintf(`apply_tab_title seen %q myproj claude`, tt.mode)), nil)
			assertExitCode(t, code, 0)
			assertContains(t, out, tt.want)
			assertNotContains(t, out, tt.unwanted)
		})
	}
}

// tmux only reports a client as unfocused when focus reporting is on, so the
// watcher's read is `list-clients -t <session>`: a Ghostty tab is exactly one
// client of the session.
func seenTmuxMock(t *testing.T, root, flags string) string {
	t.Helper()
	return mockCommand(t, root, "tmux", fmt.Sprintf(`
case "$1" in
  list-panes) printf '%%%%7\t1\n' ;;
  list-clients) printf '%s\n' ;;
  display-message) printf 'Fixing tests\n' ;;
esac
`, flags))
}

func TestTabTitleWatcher_attention_rings_until_the_tab_is_focused(t *testing.T) {
	tests := []struct{ name, flags, want string }{
		{"unfocused tab rings", "attached,UTF-8", "title:waiting"},
		{"focused tab is seen", "attached,focused,UTF-8", "title:seen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			generation := "generation.seen1"
			state := writeAttentionState(t, root, generation, "1", "attention", "question")
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

// Looking at the tab and walking away again is still "seen" — the cue tracks
// the whole waiting turn, not where the user's focus happens to be this tick.
// A new waiting turn starts over at the bell.
func TestTabTitleWatcher_seen_is_sticky_for_the_turn_and_resets_with_the_phase(t *testing.T) {
	root := t.TempDir()
	generation := "generation.seen2"
	state := writeAttentionState(t, root, generation, "1", "attention", "question")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "titles")
	flagsFile := filepath.Join(root, "flags")
	if err := os.WriteFile(flagsFile, []byte("attached,focused,UTF-8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, root, "tmux", fmt.Sprintf(`
case "$1" in
  list-panes) printf '%%%%7\t1\n' ;;
  list-clients) cat %q ;;
esac
`, flagsFile))
	tmuxPath := filepath.Join(binDir, "tmux")
	script := tabTitleSnippet(t, fmt.Sprintf(`
WATCH_LOG=%q
apply_tab_title() { printf 'title:%%s\n' "$1" >> "$WATCH_LOG"; }
keep_awake_tick() { :; }
publish_state() {
  printf '1\t%s\t%%s\t%%s\t%%s\n' "$1" "$2" "$3" > %q.tmp
  mv %q.tmp %q
}
tick() { attention_watcher_tick sess project full %q %q %q; }
attention_watcher_reset
tick
printf 'attached,UTF-8\n' > %q
tick
publish_state 2 working -
tick
publish_state 3 attention question
tick
`, logFile, generation, state, state, state,
		tmuxPath, descriptor, root, flagsFile))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(strings.TrimSpace(string(data)))
	want := []string{"title:seen", "title:active", "title:waiting"}
	if len(got) != len(want) {
		t.Fatalf("title states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("title states = %v, want %v", got, want)
		}
	}
}

// The bell → eyes swap happens with no change in phase, tool or title mode, so
// the re-emit guard must key on the rendered state too or the tab keeps ringing.
func TestTabTitleWatcher_reemits_the_title_when_only_the_seen_state_changes(t *testing.T) {
	root := t.TempDir()
	generation := "generation.seen3"
	state := writeAttentionState(t, root, generation, "1", "attention", "question")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "titles")
	flagsFile := filepath.Join(root, "flags")
	if err := os.WriteFile(flagsFile, []byte("attached,UTF-8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, root, "tmux", fmt.Sprintf(`
case "$1" in
  list-panes) printf '%%%%7\t1\n' ;;
  list-clients) cat %q ;;
esac
`, flagsFile))
	tmuxPath := filepath.Join(binDir, "tmux")
	script := tabTitleSnippet(t, fmt.Sprintf(`
apply_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
keep_awake_tick() { :; }
tick() { attention_watcher_tick sess project full %q %q %q; }
attention_watcher_reset
tick
printf 'attached,focused,UTF-8\n' > %q
tick
tick
`, logFile, tmuxPath, descriptor, root, flagsFile))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(strings.TrimSpace(string(data)))
	want := []string{"title:waiting", "title:seen"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("title states = %v, want %v (one emit per change, no repeats)", got, want)
	}
}

// In model mode the tab carries the AI tool's own title, and the per-tick
// re-emit is the only thing that can carry the cue.
func TestTabTitleWatcher_model_mode_tick_prepends_eyes_when_seen(t *testing.T) {
	root := t.TempDir()
	generation := "generation.seen4"
	state := writeAttentionState(t, root, generation, "1", "attention", "question")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "titles")
	binDir := seenTmuxMock(t, root, "attached,focused,UTF-8")
	script := tabTitleSnippet(t, fmt.Sprintf(`
set_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
attention_watcher_reset
attention_watcher_tick s p model %q %q %q
`, logFile, filepath.Join(binDir, "tmux"), descriptor, root))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), "title:👀 Fixing tests\n")
	assertNotContains(t, string(data), "🔔")
}

// tmux reports every client as focused unless focus reporting is enabled, so
// without this option the seen cue can never fire. The client attaches later in
// the same batch, which is what makes the option reach its terminal.
func TestWrapper_enables_tmux_focus_events_before_attaching(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	focus := strings.Index(body, "focus-events on")
	if focus < 0 {
		t.Fatal("wrapper.sh never turns tmux focus-events on")
	}
	attach := strings.Index(body, "attach-session -t \"$SESSION_NAME\"")
	if attach < 0 {
		t.Fatal("wrapper.sh no longer attaches the session by name")
	}
	if focus > attach {
		t.Fatal("focus-events is enabled after the attach; the client never requests focus reporting")
	}
}
