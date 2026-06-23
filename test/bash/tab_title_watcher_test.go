package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tabTitleSnippet sources tui.sh and tab-title-watcher.sh, then runs the provided bash code.
func tabTitleSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	return fmt.Sprintf("source %q && source %q && %s", tuiPath, watcherPath, body)
}

// --- check_ai_tool_state: Claude with marker file ---

func TestTabTitleWatcher_check_ai_tool_state_claude_returns_waiting_when_marker_exists_and_prompt_visible(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	os.WriteFile(markerFile, []byte(""), 0644)
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "capture-pane" ]; then
  printf 'Some output\n> \n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "claude" "dev-test-123" %q %q`, tmuxPath, markerFile))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting', got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_claude_waiting_when_marker_and_no_spinner(t *testing.T) {
	// Detection requires the marker AND no working spinner in the pane. Pane
	// text without a recognized spinner indicator (no token counter, no
	// gerund timer, no "esc to interrupt") counts as idle → "waiting".
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	os.WriteFile(markerFile, []byte(""), 0644)
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "capture-pane" ]; then
  printf 'Processing request...\nGenerating code\n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "claude" "dev-test-123" %q %q`, tmuxPath, markerFile))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting' (marker exists = Claude idle), got %q", strings.TrimSpace(out))
	}
}

// --- Claude pane-aware suppression (working-spinner detection) ---

// claudePaneMock returns a mock-tmux body whose capture-pane prints the given
// pane content. Used to simulate Claude's TUI in working vs idle states.
func claudePaneMock(content string) string {
	return fmt.Sprintf(`
if [ "$1" = "capture-pane" ]; then
  cat <<'PANE_EOF'
%s
PANE_EOF
  exit 0
fi
exit 0
`, content)
}

func TestTabTitleWatcher_check_ai_tool_state_claude_suppresses_when_pane_shows_working_spinner(t *testing.T) {
	// Marker exists (Stop fired) BUT the pane shows Claude actively working
	// (spinner line with live token counter). This is a mid-turn "thinking"
	// gap, not a genuine idle — must report "active" so no sound fires.
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	os.WriteFile(markerFile, []byte(""), 0644)

	working := "" +
		"⏺ Let me check the files.\n" +
		"✢ Clauding… (7m 56s · ↓ 28.1k tokens · thought for 3s)\n" +
		"                                              ◎ /goal active (8m)\n" +
		"────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────\n" +
		"  ghost-tab | Opus 4.8 (1M context) [high]\n"

	binDir := mockCommand(t, tmpDir, "tmux", claudePaneMock(working))
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "claude" "dev-test-123" %q %q`, tmuxPath, markerFile))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("expected 'active' (pane shows working spinner = not genuinely idle), got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_claude_waiting_when_marker_and_pane_idle(t *testing.T) {
	// Marker exists AND the pane shows no working spinner (just the finished
	// response and an idle input box) — Claude is genuinely waiting for the
	// user. Must report "waiting" so the sound fires.
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	os.WriteFile(markerFile, []byte(""), 0644)

	idle := "" +
		"⏺ All done — the fix is in place and tests pass.\n" +
		"────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────\n" +
		"  ghost-tab | Opus 4.8 (1M context) [high]\n"

	binDir := mockCommand(t, tmpDir, "tmux", claudePaneMock(idle))
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "claude" "dev-test-123" %q %q`, tmuxPath, markerFile))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting' (marker + idle pane = genuinely waiting), got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_claude_pane_working_detects_indicators(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string // "0" = working (true), "1" = not working
	}{
		{"token counter", "· Deliberating… (8m 34s · ↓ 34.1k tokens)", "0"},
		{"gerund timer", "✶ Cascading… (5m 19s · thinking more)", "0"},
		{"gerund timer seconds only", "✽ Ebbing… (0s · ↓ 12 tokens)", "0"},
		{"upload counter (no down arrow)", "✶ Hatching… (4m 20s · ↑ 7.6k tokens)", "0"},
		{"esc to interrupt", "Working (esc to interrupt)", "0"},
		{"idle response prose", "⏺ Here are the results, all done.", "1"},
		{"empty prompt", "❯ ", "1"},
		{"statusline", "  ghost-tab | 10.0% | Opus 4.8 (1M context) [high]", "1"},
		// Regression: idle prose must not match. The ellipsis-then-paren and
		// bare down-arrow patterns appear naturally in idle summaries; matching
		// them would silence the sound forever (worst-case false negative).
		{"idle ellipsis paren count", "⏺ Updated files… (12 insertions, 4 deletions). All tests pass.", "1"},
		{"idle down-arrow non-token", "Jump to bottom (ctrl+End) ↓ 5 results below", "1"},
		{"idle ellipsis paren viable", "⏺ Options considered… (2 viable).", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// claude_pane_working returns 0 (true) when the content shows a
			// working indicator, non-zero otherwise.
			snippet := tabTitleSnippet(t,
				fmt.Sprintf(`if claude_pane_working %q; then echo 0; else echo 1; fi`, tt.line))
			out, code := runBashSnippet(t, snippet, nil)
			assertExitCode(t, code, 0)
			if strings.TrimSpace(out) != tt.want {
				t.Errorf("claude_pane_working(%q) = %q, want %q", tt.line, strings.TrimSpace(out), tt.want)
			}
		})
	}
}

func TestTabTitleWatcher_check_ai_tool_state_claude_returns_active_when_marker_absent_after_user_submits(t *testing.T) {
	tmpDir := t.TempDir()
	// No marker file — simulates UserPromptSubmit hook having removed it
	markerFile := filepath.Join(tmpDir, "marker")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "claude" "" "" %q`, markerFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("expected 'active' (marker removed by UserPromptSubmit), got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_claude_returns_active_when_marker_absent(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "claude" "" "" %q`, markerFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("expected 'active', got %q", strings.TrimSpace(out))
	}
}

// --- check_ai_tool_state: non-Claude with mock tmux ---

func TestTabTitleWatcher_check_ai_tool_state_opencode_returns_waiting_when_prompt_detected(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "capture-pane" ]; then
  printf 'some output\n❯ \n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "opencode" "dev-test-123" %q ""`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting', got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_opencode_returns_active_when_no_prompt(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "capture-pane" ]; then
  printf 'Processing request...\nGenerating code\n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "opencode" "dev-test-123" %q ""`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("expected 'active', got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_detects_dollar_prompt(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "capture-pane" ]; then
  printf 'Welcome to opencode\n$ \n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "opencode" "dev-test-123" %q ""`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting', got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_detects_gt_prompt(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "capture-pane" ]; then
  printf 'Ready\n> \n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "opencode" "dev-test-123" %q ""`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting', got %q", strings.TrimSpace(out))
	}
}

// --- check_ai_tool_state: pane targeting ---

func TestTabTitleWatcher_check_ai_tool_state_targets_correct_pane(t *testing.T) {
	tmpDir := t.TempDir()
	// Mock tmux that only returns a prompt for pane 0.3
	binDir := mockCommand(t, tmpDir, "tmux", `
for arg in "$@"; do
  if [ "$arg" = "dev-test-123:0.3" ]; then
    printf 'Some output\n❯ \n'
    exit 0
  fi
done
printf 'no prompt here\n'
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "opencode" "dev-test-123" %q "" "3"`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting' (pane 0.3 targeted), got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_check_ai_tool_state_targets_pane_with_base_index_1(t *testing.T) {
	tmpDir := t.TempDir()
	// Mock tmux that only returns a prompt for pane 0.1 (pane-base-index 1 scenario)
	binDir := mockCommand(t, tmpDir, "tmux", `
for arg in "$@"; do
  if [ "$arg" = "dev-test-123:0.1" ]; then
    printf 'Some output\n❯ \n'
    exit 0
  fi
done
printf 'no prompt here\n'
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	// Pass pane_index "1" — simulating pane-base-index 1
	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`check_ai_tool_state "opencode" "dev-test-123" %q "" "1"`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("expected 'waiting' (pane 0.1 targeted), got %q", strings.TrimSpace(out))
	}
}

// --- discover_ai_pane: dynamic pane discovery ---

func TestTabTitleWatcher_discover_ai_pane_finds_rightmost_pane(t *testing.T) {
	tmpDir := t.TempDir()
	// Mock tmux list-panes returning 4 panes with different pane_left values
	// Pane 3 has the largest pane_left (rightmost), should be selected
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "list-panes" ]; then
  printf '0 0\n1 0\n2 80\n3 80\n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`discover_ai_pane "dev-session" %q`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "3" {
		t.Errorf("expected rightmost pane '3', got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_discover_ai_pane_with_base_index_1(t *testing.T) {
	tmpDir := t.TempDir()
	// Mock tmux list-panes with pane-base-index 1:
	// Panes are numbered 1-4 instead of 0-3
	// Pane 4 has the largest pane_left (rightmost)
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "list-panes" ]; then
  printf '1 0\n2 0\n3 80\n4 80\n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`discover_ai_pane "dev-session" %q`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "4" {
		t.Errorf("expected rightmost pane '4' (base-index 1), got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_discover_ai_pane_picks_highest_index_among_tied_pane_left(t *testing.T) {
	tmpDir := t.TempDir()
	// When multiple panes share the same pane_left (rightmost column),
	// we want the one with the highest pane_left. The AI pane is the
	// top-right pane (created by split-window -h), which has the highest
	// pane_left. sort -k2 -rn then head -1 picks the first among ties,
	// but sort is stable so input order is preserved for ties.
	// In real tmux, the AI pane (split-window -h first) will appear first
	// among panes sharing the same pane_left.
	binDir := mockCommand(t, tmpDir, "tmux", `
if [ "$1" = "list-panes" ]; then
  printf '0 0\n1 0\n2 80\n3 80\n'
  exit 0
fi
exit 0
`)
	env := buildEnv(t, []string{binDir})
	tmuxPath := filepath.Join(binDir, "tmux")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`discover_ai_pane "dev-session" %q`, tmuxPath))

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	result := strings.TrimSpace(out)
	// Should return one of the rightmost panes (pane_left=80)
	if result != "2" && result != "3" {
		t.Errorf("expected a rightmost pane (2 or 3), got %q", result)
	}
}

func TestTabTitleWatcher_start_tab_title_watcher_takes_seven_params(t *testing.T) {
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	// start_tab_title_watcher should accept config_dir as 7th parameter
	if !strings.Contains(content, `config_dir="${7`) {
		t.Error("start_tab_title_watcher should accept a config_dir parameter (7th argument) for sound notifications")
	}

	// It should call play_notification_sound on waiting transition
	if !strings.Contains(content, "play_notification_sound") {
		t.Error("start_tab_title_watcher should call play_notification_sound when state transitions to waiting")
	}

	// It should use discover_ai_pane for dynamic discovery
	if !strings.Contains(content, "discover_ai_pane") {
		t.Error("start_tab_title_watcher should use discover_ai_pane for dynamic pane discovery")
	}
}

func TestTabTitleWatcher_watcher_uses_stop_hook_with_marker_age_debounce(t *testing.T) {
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	// Should use marker_age for debounce (Stop hook needs filtering)
	if !strings.Contains(content, "marker_age") {
		t.Error("watcher should contain marker_age function for debounce filtering")
	}

	// Should NOT reference .ask marker (removed)
	if strings.Contains(content, ".ask") {
		t.Error("watcher should NOT reference .ask marker")
	}

	// Should still poll at 0.5s
	if !strings.Contains(content, "sleep 0.5") {
		t.Error("watcher loop should poll every 0.5 seconds")
	}

	// Should still use play_notification_sound
	if !strings.Contains(content, "play_notification_sound") {
		t.Error("watcher should call play_notification_sound when state transitions to waiting")
	}

	// Should still use discover_ai_pane
	if !strings.Contains(content, "discover_ai_pane") {
		t.Error("watcher should use discover_ai_pane for dynamic pane discovery")
	}
}

func TestTabTitleWatcher_watcher_suppresses_active_work_via_pane_check(t *testing.T) {
	// The watcher no longer relies on a cooldown / 15s extended debounce to
	// filter mid-turn "thinking" noise — that delayed genuine notifications.
	// Instead check_ai_tool_state confirms idleness against the live pane via
	// claude_pane_working, and the loop uses a single short debounce.
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	// Should suppress active work via the pane working-spinner check.
	if !strings.Contains(content, "claude_pane_working") {
		t.Error("watcher should use claude_pane_working to confirm idleness against the pane")
	}

	// Should NOT use a 15s extended debounce threshold anymore.
	if strings.Contains(content, "debounce_threshold=15") {
		t.Error("watcher should no longer use a 15s extended debounce (caused late notifications)")
	}
}

func TestTabTitleWatcher_check_ai_tool_state_claude_comment_references_stop_hook(t *testing.T) {
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "Stop hook") {
		t.Error("check_ai_tool_state comment should reference Stop hook")
	}
}

// --- stop_tab_title_watcher: cleanup ---

func TestTabTitleWatcher_stop_tab_title_watcher_removes_marker_file(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	os.WriteFile(markerFile, []byte(""), 0644)

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`_TAB_TITLE_WATCHER_PID=""; stop_tab_title_watcher %q`, markerFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Errorf("expected marker file to be removed")
	}
}

func TestTabTitleWatcher_stop_tab_title_watcher_removes_cooldown_file(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	cooldownFile := markerFile + "-cooldown"
	os.WriteFile(markerFile, []byte(""), 0644)
	os.WriteFile(cooldownFile, []byte(""), 0644)

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`_TAB_TITLE_WATCHER_PID=""; stop_tab_title_watcher %q`, markerFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(cooldownFile); !os.IsNotExist(err) {
		t.Errorf("expected cooldown file to be removed")
	}
}

func TestTabTitleWatcher_stop_tab_title_watcher_succeeds_when_marker_absent(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "no-such-marker")

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`_TAB_TITLE_WATCHER_PID=""; stop_tab_title_watcher %q`, markerFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
}

// --- wrapper.sh: tmux set-titles off ---

func TestTabTitleWatcher_wrapper_disables_tmux_set_titles(t *testing.T) {
	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "wrapper.sh")
	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)

	// The tmux new-session command must keep set-titles off so tmux never
	// overwrites the tab title: Ghost Tab's watcher owns it in every mode,
	// including model mode where the watcher mirrors the AI pane's own title.
	if !strings.Contains(content, "set-option set-titles off") {
		t.Error("wrapper.sh must contain 'set-option set-titles off' so tmux does not overwrite Ghost Tab's tab title")
	}
}

// --- wrapper.sh: GHOST_TAB_MARKER_FILE passed to tmux via -e ---

func TestTabTitleWatcher_wrapper_passes_marker_file_to_tmux(t *testing.T) {
	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "wrapper.sh")
	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)

	// The tmux new-session command must pass GHOST_TAB_MARKER_FILE via -e flag
	// so that hooks inside tmux panes can access the marker file path
	if !strings.Contains(content, `-e "GHOST_TAB_MARKER_FILE=$GHOST_TAB_MARKER_FILE"`) {
		t.Error("wrapper.sh must pass GHOST_TAB_MARKER_FILE to tmux new-session via -e flag")
	}

	// Verify the variable is defined BEFORE the tmux new-session command
	lines := strings.Split(content, "\n")
	definitionLine := -1
	tmuxNewSessionLine := -1
	for i, line := range lines {
		if strings.Contains(line, `GHOST_TAB_MARKER_FILE="/tmp/ghost-tab-waiting-`) && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			definitionLine = i
		}
		if strings.Contains(line, "new-session") && strings.Contains(line, "GHOST_TAB_MARKER_FILE") {
			tmuxNewSessionLine = i
		}
	}
	if definitionLine == -1 {
		t.Fatal("wrapper.sh must define GHOST_TAB_MARKER_FILE variable")
	}
	if tmuxNewSessionLine == -1 {
		t.Fatal("wrapper.sh must use GHOST_TAB_MARKER_FILE in tmux new-session command")
	}
	if definitionLine >= tmuxNewSessionLine {
		t.Errorf("GHOST_TAB_MARKER_FILE must be defined (line %d) before tmux new-session (line %d)", definitionLine+1, tmuxNewSessionLine+1)
	}

	// Cleanup should also remove cooldown file
	if !strings.Contains(content, "cooldown") {
		t.Error("wrapper.sh cleanup should handle cooldown file removal")
	}
}

// --- play_notification_sound ---

// soundWatcherSnippet sources tui.sh, settings-json.sh, notification-setup.sh,
// and tab-title-watcher.sh, then runs the provided bash code.
func soundWatcherSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	settingsJsonPath := filepath.Join(root, "lib", "settings-json.sh")
	notifPath := filepath.Join(root, "lib", "notification-setup.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	return fmt.Sprintf("source %q && source %q && source %q && source %q && %s",
		tuiPath, settingsJsonPath, notifPath, watcherPath, body)
}

func TestTabTitleWatcher_play_notification_sound_calls_afplay_when_enabled(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "claude-features.json", `{"sound": true, "sound_name": "Glass"}`)

	logFile := filepath.Join(tmpDir, "afplay.log")
	binDir := mockCommand(t, tmpDir, "afplay", fmt.Sprintf(`echo "$1" >> %q`, logFile))
	env := buildEnv(t, []string{binDir})

	snippet := soundWatcherSnippet(t,
		fmt.Sprintf(`play_notification_sound "claude" %q`, configDir))

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	// Give background process a moment to write
	time.Sleep(200 * time.Millisecond)

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected afplay to be called, log file missing: %v", err)
	}
	assertContains(t, string(data), "Glass.aiff")
}

func TestTabTitleWatcher_play_notification_sound_skips_when_sound_disabled(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "claude-features.json", `{"sound": false}`)

	logFile := filepath.Join(tmpDir, "afplay.log")
	binDir := mockCommand(t, tmpDir, "afplay", fmt.Sprintf(`echo "$1" >> %q`, logFile))
	env := buildEnv(t, []string{binDir})

	snippet := soundWatcherSnippet(t,
		fmt.Sprintf(`play_notification_sound "claude" %q`, configDir))

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	time.Sleep(200 * time.Millisecond)

	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Errorf("expected afplay NOT to be called when sound is disabled")
	}
}

func TestTabTitleWatcher_play_notification_sound_uses_default_when_features_file_missing(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "nonexistent")

	logFile := filepath.Join(tmpDir, "afplay.log")
	binDir := mockCommand(t, tmpDir, "afplay", fmt.Sprintf(`echo "$1" >> %q`, logFile))
	env := buildEnv(t, []string{binDir})

	snippet := soundWatcherSnippet(t,
		fmt.Sprintf(`play_notification_sound "claude" %q`, configDir))

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	time.Sleep(200 * time.Millisecond)

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected afplay to be called with default sound: %v", err)
	}
	assertContains(t, string(data), "Bottle.aiff")
}

func TestTabTitleWatcher_wrapper_passes_config_dir_to_watcher(t *testing.T) {
	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "wrapper.sh")
	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	content := string(data)

	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "start_tab_title_watcher") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			if !strings.Contains(line, "ghost-tab") {
				t.Errorf("start_tab_title_watcher call should pass ghost-tab config dir, got: %s", line)
			}
			return
		}
	}
	t.Error("start_tab_title_watcher call not found in wrapper.sh")
}

// --- Full marker lifecycle ---

func TestTabTitleWatcher_check_ai_tool_state_claude_full_marker_lifecycle(t *testing.T) {
	// Tests the complete lifecycle:
	// 1. Initially no marker → "active"
	// 2. Notification hook touches marker → "waiting"
	// 3. UserPromptSubmit hook removes marker → "active"
	// 4. Notification hook touches marker again → "waiting"
	// 5. PreToolUse hook removes marker → "active"

	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")

	snippet := tabTitleSnippet(t, fmt.Sprintf(`check_ai_tool_state "claude" "" "" %q`, markerFile))

	// Step 1: No marker → active
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("step 1: expected 'active' (no marker initially), got %q", strings.TrimSpace(out))
	}

	// Step 2: Simulate Notification hook (touch marker) → waiting
	if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
		t.Fatalf("step 2: failed to create marker file: %v", err)
	}
	out, code = runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("step 2: expected 'waiting' (Notification hook created marker), got %q", strings.TrimSpace(out))
	}

	// Step 3: Simulate UserPromptSubmit hook (rm -f marker) → active
	if err := os.Remove(markerFile); err != nil {
		t.Fatalf("step 3: failed to remove marker file: %v", err)
	}
	out, code = runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("step 3: expected 'active' (UserPromptSubmit removed marker), got %q", strings.TrimSpace(out))
	}

	// Step 4: Simulate Notification hook again (touch marker) → waiting
	if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
		t.Fatalf("step 4: failed to create marker file: %v", err)
	}
	out, code = runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "waiting" {
		t.Errorf("step 4: expected 'waiting' (Notification hook created marker again), got %q", strings.TrimSpace(out))
	}

	// Step 5: Simulate PreToolUse hook (rm -f marker) → active
	if err := os.Remove(markerFile); err != nil {
		t.Fatalf("step 5: failed to remove marker file: %v", err)
	}
	out, code = runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "active" {
		t.Errorf("step 5: expected 'active' (PreToolUse removed marker), got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_hook_commands_manage_marker_file_correctly(t *testing.T) {
	// Verifies the actual hook commands (from settings-json.sh) work correctly:
	// 1. Notification command creates marker file
	// 2. UserPromptSubmit command removes it
	// 3. PreToolUse command removes it
	// 4. All commands exit 0 even when file doesn't exist

	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")

	stopCmd := fmt.Sprintf(`GHOST_TAB_MARKER_FILE=%q; if [ -n "$GHOST_TAB_MARKER_FILE" ]; then touch "$GHOST_TAB_MARKER_FILE"; fi`, markerFile)
	clearCmd := fmt.Sprintf(`GHOST_TAB_MARKER_FILE=%q; if [ -n "$GHOST_TAB_MARKER_FILE" ]; then rm -f "$GHOST_TAB_MARKER_FILE"; fi`, markerFile)

	// Step 1: Notification command creates marker file
	_, code := runBashSnippet(t, stopCmd, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("step 1: Notification hook should create marker file")
	}

	// Step 2: UserPromptSubmit command removes marker file
	_, code = runBashSnippet(t, clearCmd, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("step 2: UserPromptSubmit hook should remove marker file")
	}

	// Step 3: Notification command creates marker again
	_, code = runBashSnippet(t, stopCmd, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("step 3: Notification hook should create marker file again")
	}

	// Step 4: PreToolUse command removes marker (same clear_cmd)
	_, code = runBashSnippet(t, clearCmd, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("step 4: PreToolUse hook should remove marker file")
	}

	// Step 5: Clear command succeeds even when file already gone
	_, code = runBashSnippet(t, clearCmd, nil)
	assertExitCode(t, code, 0)

	// Step 6: Clear command is a noop when GHOST_TAB_MARKER_FILE is empty
	noopCmd := `GHOST_TAB_MARKER_FILE=""; if [ -n "$GHOST_TAB_MARKER_FILE" ]; then rm -f "$GHOST_TAB_MARKER_FILE"; fi`
	_, code = runBashSnippet(t, noopCmd, nil)
	assertExitCode(t, code, 0)
}

// --- ask sidecar file tests ---

func TestTabTitleWatcher_watcher_source_contains_ask_sidecar_bypass(t *testing.T) {
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	// The watcher should check for an -ask sidecar file
	if !strings.Contains(content, "-ask") {
		t.Error("watcher should reference -ask sidecar file suffix")
	}
}

func TestTabTitleWatcher_stop_tab_title_watcher_removes_ask_sidecar(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "marker")
	askFile := markerFile + "-ask"
	os.WriteFile(markerFile, []byte(""), 0644)
	os.WriteFile(askFile, []byte(""), 0644)

	snippet := tabTitleSnippet(t,
		fmt.Sprintf(`_TAB_TITLE_WATCHER_PID=""; stop_tab_title_watcher %q`, markerFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(askFile); !os.IsNotExist(err) {
		t.Errorf("expected -ask sidecar file to be removed")
	}
}

// --- apply_tab_title: per-mode title writing ---
//
// apply_tab_title <state> <mode> <project> <tool> writes the terminal title for
// the active/waiting state, EXCEPT in "model" mode where it leaves the title
// untouched so the AI tool's own title (the one the model set) shows through.

func TestTabTitleWatcher_apply_tab_title_full_active_includes_tool(t *testing.T) {
	snippet := tabTitleSnippet(t, `apply_tab_title "active" "full" "myproj" "claude"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "myproj · claude")
	assertNotContains(t, out, "●") // no waiting dot when active
}

func TestTabTitleWatcher_apply_tab_title_full_waiting_prepends_dot(t *testing.T) {
	snippet := tabTitleSnippet(t, `apply_tab_title "waiting" "full" "myproj" "claude"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "● myproj · claude")
}

func TestTabTitleWatcher_apply_tab_title_project_active_omits_tool(t *testing.T) {
	snippet := tabTitleSnippet(t, `apply_tab_title "active" "project" "myproj" "claude"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "myproj")
	assertNotContains(t, out, "claude")
}

func TestTabTitleWatcher_apply_tab_title_model_active_writes_nothing(t *testing.T) {
	snippet := tabTitleSnippet(t, `apply_tab_title "active" "model" "myproj" "claude"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("model mode must leave the title untouched, got %q", out)
	}
}

func TestTabTitleWatcher_apply_tab_title_model_waiting_writes_nothing(t *testing.T) {
	snippet := tabTitleSnippet(t, `apply_tab_title "waiting" "model" "myproj" "claude"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("model mode must leave the title untouched even when waiting, got %q", out)
	}
}

// The watcher loop must route title writes through apply_tab_title and must
// still play the notification sound regardless of mode (so "model" mode still
// signals idle audibly even though it never writes the title).
func TestTabTitleWatcher_loop_uses_apply_tab_title(t *testing.T) {
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "apply_tab_title") {
		t.Error("watcher loop should route title writes through apply_tab_title")
	}
	if !strings.Contains(content, "play_notification_sound") {
		t.Error("watcher should still call play_notification_sound on the waiting transition")
	}
}

// --- model_tab_title: mirror the AI pane's own title in model mode ---
//
// In "model" mode the AI tool sets its tmux pane's title (via an OSC escape) to
// describe its task. The watcher reads that pane title and mirrors it to the
// terminal tab. When the pane has no meaningful title yet, tmux reports the
// hostname — so fall back to the project name instead of showing the host.

func TestTabTitleWatcher_model_tab_title_uses_pane_title(t *testing.T) {
	snippet := tabTitleSnippet(t,
		`model_tab_title "Refactoring auth module" "myhost.local" "blok"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Refactoring auth module" {
		t.Errorf("model_tab_title should echo the AI pane title, got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_model_tab_title_falls_back_to_project_on_hostname(t *testing.T) {
	snippet := tabTitleSnippet(t,
		`model_tab_title "myhost.local" "myhost.local" "blok"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "blok" {
		t.Errorf("model_tab_title should fall back to project when the pane title is just the hostname, got %q", strings.TrimSpace(out))
	}
}

func TestTabTitleWatcher_model_tab_title_falls_back_to_project_on_empty(t *testing.T) {
	snippet := tabTitleSnippet(t,
		`model_tab_title "" "myhost.local" "blok"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "blok" {
		t.Errorf("model_tab_title should fall back to project when the pane title is empty, got %q", strings.TrimSpace(out))
	}
}

// The watcher loop, in model mode, must read the AI pane's title and mirror it
// to the tab (so the model's own title shows). It reads #{pane_title} and routes
// it through model_tab_title with a hostname/project fallback.
func TestTabTitleWatcher_loop_mirrors_ai_pane_title_in_model_mode(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "lib", "tab-title-watcher.sh"))
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "model_tab_title") {
		t.Error("watcher loop should mirror the AI pane title via model_tab_title in model mode")
	}
	if !strings.Contains(content, "pane_title") {
		t.Error("watcher loop should read #{pane_title} of the AI pane in model mode")
	}
}
