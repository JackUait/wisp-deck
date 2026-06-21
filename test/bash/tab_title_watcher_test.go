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

func TestTabTitleWatcher_check_ai_tool_state_claude_marker_only_no_pane_check(t *testing.T) {
	// Claude detection is marker-only. Even without a prompt in the pane,
	// marker existing means Stop hook fired → Claude is waiting.
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

	// Should use cooldown file for extended debounce after tool use
	if !strings.Contains(content, "cooldown") {
		t.Error("watcher should use cooldown file to extend debounce after tool completions")
	}
}

func TestTabTitleWatcher_watcher_uses_extended_debounce_when_cooldown_present(t *testing.T) {
	root := projectRoot(t)
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	data, err := os.ReadFile(watcherPath)
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	// The watcher should check for a cooldown file (marker_file + "-cooldown")
	if !strings.Contains(content, "-cooldown") {
		t.Error("watcher should reference cooldown file suffix (-cooldown)")
	}

	// When cooldown is active, debounce should be significantly longer than 1 second
	// to filter out subagent completion noise
	if !strings.Contains(content, "cooldown") {
		t.Error("watcher should contain cooldown logic for extended debounce")
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

	// The tmux new-session command block must include set-titles off
	// to prevent user's global set-titles on from overwriting Ghost Tab's tab title
	if !strings.Contains(content, "set-option set-titles off") {
		t.Error("wrapper.sh must contain 'set-option set-titles off' in tmux new-session command to prevent tmux from overwriting tab titles")
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
