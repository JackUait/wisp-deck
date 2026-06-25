package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin the launch-time fast paths that avoid spawning python3 when
// the relevant settings are already in their final form. A python3 cold start
// is ~40-90ms; wrapper.sh runs add_waiting_indicator_hooks AND
// set_claude_notif_channel synchronously on EVERY Claude launch, before the AI
// tool can start. When settings.json is already current (the common case after
// the first launch), no rewrite is needed, so python3 must not be invoked at
// all — shaving that latency off the time-to-agent-ready.

// currentFormatWaitingHooks is a settings.json whose waiting-indicator hooks are
// already in the current format (Stop + AskUserQuestion -ask sidecar + catch-all
// negative-lookahead matcher + PostToolUse cooldown). Mirrors the fixture in
// TestSettingsJson_add_waiting_indicator_hooks_reports_exists_when_duplicate.
const currentFormatWaitingHooks = `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$WISP_DECK_MARKER_FILE\" ]; then touch \"$WISP_DECK_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "AskUserQuestion",
        "hooks": [{"type": "command", "command": "if [ -n \"$WISP_DECK_MARKER_FILE\" ]; then touch \"$WISP_DECK_MARKER_FILE\" \"${WISP_DECK_MARKER_FILE}-ask\"; fi"}]
      },
      {
        "matcher": "^(?!AskUserQuestion$)",
        "hooks": [{"type": "command", "command": "if [ -n \"$WISP_DECK_MARKER_FILE\" ]; then rm -f \"$WISP_DECK_MARKER_FILE\" \"${WISP_DECK_MARKER_FILE}-ask\"; fi"}]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$WISP_DECK_MARKER_FILE\" ]; then touch \"${WISP_DECK_MARKER_FILE}-cooldown\"; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$WISP_DECK_MARKER_FILE\" ]; then rm -f \"$WISP_DECK_MARKER_FILE\" \"${WISP_DECK_MARKER_FILE}-ask\"; fi"}]
      }
    ]
  }
}
`

// pythonProbeEnv returns (env, sentinelPath) where env shadows python3/python
// with a mock that records its invocation by creating sentinelPath. If the
// sentinel exists after the call, python3 was spawned.
func pythonProbeEnv(t *testing.T, tmpDir string) ([]string, string) {
	t.Helper()
	sentinel := filepath.Join(tmpDir, "python3-was-called")
	body := fmt.Sprintf("touch %q\nexit 0", sentinel)
	binDir := mockCommand(t, tmpDir, "python3", body)
	// Some code paths may call bare `python`; shadow it too for safety.
	mockCommand(t, tmpDir, "python", body)
	return buildEnv(t, []string{binDir}), sentinel
}

func TestFastPath_add_waiting_indicator_hooks_skips_python_when_current(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := writeTempFile(t, tmpDir, "settings.json", currentFormatWaitingHooks)
	before, _ := os.ReadFile(settingsFile)

	env, sentinel := pythonProbeEnv(t, tmpDir)
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, strings.TrimSpace(out), "exists")

	if _, err := os.Stat(sentinel); err == nil {
		t.Error("python3 was spawned even though hooks are already current — fast path should skip it")
	}
	after, _ := os.ReadFile(settingsFile)
	if string(before) != string(after) {
		t.Error("settings.json was rewritten even though hooks are already current")
	}
}

// screenshotLibSnippet sources screenshot.sh then runs body.
func screenshotLibSnippet(t *testing.T, body string) string {
	t.Helper()
	lib := filepath.Join(projectRoot(t), "lib", "screenshot.sh")
	return fmt.Sprintf("source %q && %s", lib, body)
}

func TestFastPath_claude_filter_prefix_probes_once_then_caches(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	counter := filepath.Join(tmpDir, "probe-count")
	// Mock wisp-deck-tui: records each invocation and succeeds (supports filter).
	binDir := mockCommand(t, tmpDir, "wisp-deck-tui",
		fmt.Sprintf("echo x >> %q\nexit 0", counter))
	env := buildEnv(t, []string{binDir})

	snippet := screenshotLibSnippet(t, fmt.Sprintf(`gt_claude_filter_prefix %q`, cacheDir))

	out1, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out1, "wisp-deck-tui screenshot-filter -- ")

	out2, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out2, "wisp-deck-tui screenshot-filter -- ")

	data, _ := os.ReadFile(counter)
	n := strings.Count(string(data), "x")
	if n != 1 {
		t.Errorf("probe ran %d times, want 1 (result should be cached after the first launch)", n)
	}
}

func TestFastPath_claude_filter_prefix_reprobes_when_binary_changes(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	counter := filepath.Join(tmpDir, "probe-count")
	binDir := mockCommand(t, tmpDir, "wisp-deck-tui",
		fmt.Sprintf("echo x >> %q\nexit 0", counter))
	env := buildEnv(t, []string{binDir})
	snippet := screenshotLibSnippet(t, fmt.Sprintf(`gt_claude_filter_prefix %q`, cacheDir))

	runBashSnippet(t, snippet, env)
	// Simulate an install/update: change the binary's mtime so its signature differs.
	binPath := filepath.Join(binDir, "wisp-deck-tui")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(binPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	runBashSnippet(t, snippet, env)

	data, _ := os.ReadFile(counter)
	if n := strings.Count(string(data), "x"); n != 2 {
		t.Errorf("probe ran %d times, want 2 (a changed binary must be re-probed)", n)
	}
}

func TestFastPath_claude_filter_prefix_caches_negative_result(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	counter := filepath.Join(tmpDir, "probe-count")
	// Mock that does NOT support the subcommand (exits non-zero).
	binDir := mockCommand(t, tmpDir, "wisp-deck-tui",
		fmt.Sprintf("echo x >> %q\nexit 1", counter))
	env := buildEnv(t, []string{binDir})
	snippet := screenshotLibSnippet(t, fmt.Sprintf(`gt_claude_filter_prefix %q`, cacheDir))

	out1, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0) // helper must not fail the launch
	if strings.TrimSpace(out1) != "" {
		t.Errorf("unsupported binary should yield no prefix, got %q", out1)
	}
	out2, _ := runBashSnippet(t, snippet, env)
	if strings.TrimSpace(out2) != "" {
		t.Errorf("cached negative result should still yield no prefix, got %q", out2)
	}
	data, _ := os.ReadFile(counter)
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Errorf("probe ran %d times, want 1 (negative result should be cached)", n)
	}
}

func TestFastPath_set_claude_notif_channel_skips_python_when_already_silenced(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0755)
	claudeDir := filepath.Join(tmpDir, "claude")
	os.MkdirAll(claudeDir, 0755)
	settingsFile := writeTempFile(t, claudeDir, "settings.json",
		`{"preferredNotifChannel": "terminal_bell", "model": "opus"}`)
	before, _ := os.ReadFile(settingsFile)

	env, sentinel := pythonProbeEnv(t, tmpDir)
	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_claude_notif_channel %q %q`, configDir, settingsFile))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(sentinel); err == nil {
		t.Error("python3 was spawned even though channel is already terminal_bell — fast path should skip it")
	}
	after, _ := os.ReadFile(settingsFile)
	if string(before) != string(after) {
		t.Error("settings.json was rewritten even though channel is already terminal_bell")
	}
	// A second concurrent session must not clobber a previously-saved prev value;
	// the fast path writes nothing, so no prev file is created here.
	if _, err := os.Stat(filepath.Join(configDir, "prev-notif-channel")); err == nil {
		t.Error("prev-notif-channel must not be written when channel is already silenced")
	}
}
