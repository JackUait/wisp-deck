package bash_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settingsJsonSnippet builds a bash snippet that sources tui.sh and settings-json.sh,
// then runs the provided bash code.
func settingsJsonSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	settingsJsonPath := filepath.Join(root, "lib", "settings-json.sh")
	return fmt.Sprintf("source %q && source %q && %s", tuiPath, settingsJsonPath, body)
}

func TestSettingsJson_merge_claude_settings_creates_file_when_missing(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_claude_settings %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Created Claude settings with status line")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json should have been created: %v", err)
	}
	content := string(data)
	assertContains(t, content, `"statusLine"`)
	assertContains(t, content, "statusline-wrapper.sh")
}

func TestSettingsJson_merge_claude_settings_adds_status_line_to_existing(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {}
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_claude_settings %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Added status line to Claude settings")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	assertContains(t, content, `"statusLine"`)
}

// --- add_waiting_indicator_hooks ---

func TestSettingsJson_add_waiting_indicator_hooks_creates_file_with_Stop_PreToolUse_PostToolUse_and_UserPromptSubmit(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "added")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json should have been created: %v", err)
	}
	content := string(data)
	assertContains(t, content, `"Stop"`)
	assertContains(t, content, `"PreToolUse"`)
	assertContains(t, content, `"PostToolUse"`)
	assertContains(t, content, `"UserPromptSubmit"`)
	assertContains(t, content, "GHOST_TAB_MARKER_FILE")
	assertContains(t, content, `"AskUserQuestion"`)
	// PostToolUse hook should touch cooldown file
	assertContains(t, content, "cooldown")
	// Must NOT have Notification hook (replaced by Stop)
	assertNotContains(t, content, `"Notification"`)
}

func TestSettingsJson_add_waiting_indicator_hooks_adds_to_existing_settings(t *testing.T) {
	tmpDir := t.TempDir()
	// Existing settings has a Notification hook from another plugin (afplay sound)
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Notification": [
      {
        "matcher": "idle_prompt",
        "hooks": [{"type": "command", "command": "afplay /System/Library/Sounds/Bottle.aiff &"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "added")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	// The other plugin's Notification hook should remain untouched
	assertContains(t, content, "afplay")
	assertContains(t, content, `"Notification"`)
	// Our hooks should use Stop, not Notification
	assertContains(t, content, `"Stop"`)
	assertContains(t, content, "GHOST_TAB_MARKER_FILE")
	assertContains(t, content, `"PreToolUse"`)
}

func TestSettingsJson_add_waiting_indicator_hooks_reports_exists_when_duplicate(t *testing.T) {
	tmpDir := t.TempDir()
	// Current format hooks already installed (including PostToolUse cooldown + catch-all matcher + -ask sidecar)
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "AskUserQuestion",
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\" \"${GHOST_TAB_MARKER_FILE}-ask\"; fi"}]
      },
      {
        "matcher": "^(?!AskUserQuestion$)",
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\" \"${GHOST_TAB_MARKER_FILE}-ask\"; fi"}]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"${GHOST_TAB_MARKER_FILE}-cooldown\"; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\" \"${GHOST_TAB_MARKER_FILE}-ask\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, strings.TrimSpace(out), "exists")
}

func TestSettingsJson_add_waiting_indicator_hooks_upgrades_format_without_PostToolUse(t *testing.T) {
	tmpDir := t.TempDir()
	// Old format: has Stop + PreToolUse with AskUserQuestion but NO PostToolUse cooldown hook
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "AskUserQuestion",
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      },
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "upgraded")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	// Should now include PostToolUse hook with cooldown
	assertContains(t, content, `"PostToolUse"`)
	assertContains(t, content, "cooldown")
	// Should still have all other hooks
	assertContains(t, content, `"Stop"`)
	assertContains(t, content, `"PreToolUse"`)
	assertContains(t, content, `"AskUserQuestion"`)
	assertContains(t, content, `"UserPromptSubmit"`)
}

func TestSettingsJson_add_waiting_indicator_hooks_upgrades_notification_format_to_stop(t *testing.T) {
	tmpDir := t.TempDir()
	// Old Notification format (the format being replaced)
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Notification": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "_gt_in=$(cat); if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then if [[ \"$_gt_in\" == *AskUserQuestion* ]]; then touch \"$GHOST_TAB_MARKER_FILE\"; else rm -f \"$GHOST_TAB_MARKER_FILE\"; fi; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "upgraded")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	// Should now use Stop instead of Notification
	assertContains(t, content, `"Stop"`)
	assertNotContains(t, content, `"Notification"`)
	// PreToolUse should use matcher instead of stdin reading
	assertContains(t, content, `"AskUserQuestion"`)
	assertNotContains(t, content, "$(cat)")
	// Should include PostToolUse cooldown hook
	assertContains(t, content, `"PostToolUse"`)
	assertContains(t, content, "cooldown")
}

func TestSettingsJson_add_waiting_indicator_hooks_upgrades_v3_stop_ask_format_to_current(t *testing.T) {
	tmpDir := t.TempDir()
	// v3 format: Stop hook with .ask marker (old format)
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "_gt_in=$(cat); if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then if [[ \"$_gt_in\" == *AskUserQuestion* ]]; then touch \"$GHOST_TAB_MARKER_FILE\" \"$GHOST_TAB_MARKER_FILE.ask\"; else rm -f \"$GHOST_TAB_MARKER_FILE\" \"$GHOST_TAB_MARKER_FILE.ask\"; fi; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\" \"$GHOST_TAB_MARKER_FILE.ask\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "upgraded")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	// Should still use Stop (current format)
	assertContains(t, content, `"Stop"`)
	// Should no longer have .ask references
	assertNotContains(t, content, ".ask")
	// Should use matcher instead of stdin reading
	assertContains(t, content, `"AskUserQuestion"`)
	assertNotContains(t, content, "$(cat)")
	// Should include PostToolUse cooldown hook
	assertContains(t, content, `"PostToolUse"`)
	assertContains(t, content, "cooldown")
}

func TestSettingsJson_add_waiting_indicator_hooks_catchall_PreToolUse_has_negative_lookahead_matcher(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json should have been created: %v", err)
	}
	content := string(data)
	// Catch-all PreToolUse must have a matcher that excludes AskUserQuestion
	assertContains(t, content, `"matcher": "^(?!AskUserQuestion$)"`)
}

func TestSettingsJson_add_waiting_indicator_hooks_upgrades_catchall_without_matcher(t *testing.T) {
	tmpDir := t.TempDir()
	// Old format: has Stop + PreToolUse with AskUserQuestion + catch-all WITHOUT matcher + PostToolUse cooldown
	// This is the pre-fix format that needs upgrading to add the negative lookahead
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "AskUserQuestion",
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      },
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"${GHOST_TAB_MARKER_FILE}-cooldown\"; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "upgraded")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	// After upgrade, catch-all should now have the negative lookahead matcher
	assertContains(t, content, `"matcher": "^(?!AskUserQuestion$)"`)
	// Should still have all hooks
	assertContains(t, content, `"Stop"`)
	assertContains(t, content, `"PreToolUse"`)
	assertContains(t, content, `"PostToolUse"`)
	assertContains(t, content, `"UserPromptSubmit"`)
}

// --- add_waiting_indicator_hooks: safe exit code format ---

func TestSettingsJson_add_waiting_indicator_hooks_uses_if_then_fi_not_and_operator(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json should have been created: %v", err)
	}
	content := string(data)

	// Must use safe if-then-fi format
	assertContains(t, content, `if [ -n`)
	assertContains(t, content, `; then`)
	assertContains(t, content, `; fi`)

	// Must NOT use old && format that returns exit 1 when var is empty
	assertNotContains(t, content, `] && touch`)
	assertNotContains(t, content, `] && rm`)
}

func TestSettingsJson_hook_commands_exit_zero_when_marker_env_var_empty(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks into settings file
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse the generated JSON to extract hook commands
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Hooks []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	// Collect all hook commands
	var commands []string
	for _, groups := range settings.Hooks {
		for _, group := range groups {
			for _, h := range group.Hooks {
				if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
					commands = append(commands, h.Command)
				}
			}
		}
	}

	if len(commands) == 0 {
		t.Fatal("no GHOST_TAB_MARKER_FILE hook commands found in generated settings")
	}

	// Run each command with GHOST_TAB_MARKER_FILE="" — must exit 0
	for _, cmd := range commands {
		bashScript := fmt.Sprintf(`GHOST_TAB_MARKER_FILE="" ; %s`, cmd)
		_, exitCode := runBashSnippet(t, bashScript, nil)
		if exitCode != 0 {
			t.Errorf("command should exit 0 when GHOST_TAB_MARKER_FILE is empty, got %d for: %s", exitCode, cmd)
		}
	}
}

func TestSettingsJson_hook_commands_exit_zero_when_marker_env_var_set(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks into settings file
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse the generated JSON to extract hook commands
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Hooks []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	// Collect all hook commands
	var commands []string
	for _, groups := range settings.Hooks {
		for _, group := range groups {
			for _, h := range group.Hooks {
				if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
					commands = append(commands, h.Command)
				}
			}
		}
	}

	if len(commands) == 0 {
		t.Fatal("no GHOST_TAB_MARKER_FILE hook commands found in generated settings")
	}

	markerFile := filepath.Join(tmpDir, "test-marker")

	// Test with marker file NOT existing yet — touch should create it, rm -f should succeed
	for _, cmd := range commands {
		// Remove marker between each command so each starts fresh
		os.Remove(markerFile)
		bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q ; %s`, markerFile, cmd)
		_, exitCode := runBashSnippet(t, bashScript, nil)
		if exitCode != 0 {
			t.Errorf("command should exit 0 when marker file does not exist, got %d for: %s", exitCode, cmd)
		}
	}

	// Test with marker file already existing — touch and rm -f should both succeed
	if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create marker file: %v", err)
	}
	for _, cmd := range commands {
		// Re-create marker before each command so it always exists
		if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
			t.Fatalf("failed to re-create marker file: %v", err)
		}
		bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q ; %s`, markerFile, cmd)
		_, exitCode := runBashSnippet(t, bashScript, nil)
		if exitCode != 0 {
			t.Errorf("command should exit 0 when marker file exists, got %d for: %s", exitCode, cmd)
		}
	}
}

// --- remove_waiting_indicator_hooks ---

func TestSettingsJson_remove_waiting_indicator_hooks_removes_old_stop_hooks(t *testing.T) {
	tmpDir := t.TempDir()
	// Full format with all hooks including PostToolUse
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"${GHOST_TAB_MARKER_FILE}-cooldown\"; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "removed")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	assertNotContains(t, string(data), "GHOST_TAB_MARKER_FILE")
}

func TestSettingsJson_remove_waiting_indicator_hooks_removes_notification_hooks(t *testing.T) {
	tmpDir := t.TempDir()
	// New Notification format hooks
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Notification": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "_gt_in=$(cat); if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then if [[ \"$_gt_in\" == *AskUserQuestion* ]]; then touch \"$GHOST_TAB_MARKER_FILE\"; else rm -f \"$GHOST_TAB_MARKER_FILE\"; fi; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "removed")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	assertNotContains(t, string(data), "GHOST_TAB_MARKER_FILE")
}

func TestSettingsJson_remove_waiting_indicator_hooks_preserves_other_hooks(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "afplay /System/Library/Sounds/Bottle.aiff &"}]
      },
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && touch \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && rm -f \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "removed")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	assertContains(t, content, "afplay")
	assertNotContains(t, content, "GHOST_TAB_MARKER_FILE")
}

func TestSettingsJson_remove_waiting_indicator_hooks_returns_not_found_when_absent(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{}`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, strings.TrimSpace(out), "not_found")
}

// --- Stop hook behavior ---

func TestSettingsJson_Stop_hook_creates_marker_file(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse to find the Stop hook command
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Hooks []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	var stopCmd string
	for _, group := range settings.Hooks["Stop"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
				stopCmd = h.Command
			}
		}
	}
	if stopCmd == "" {
		t.Fatal("no GHOST_TAB_MARKER_FILE Stop hook found")
	}

	// The Stop hook should touch (create) the marker file
	markerFile := filepath.Join(tmpDir, "test-marker")
	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, stopCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("marker file should exist after Stop hook, but it does not")
	}
}

// --- PreToolUse hook behavior ---

func TestSettingsJson_PreToolUse_hook_creates_marker_for_AskUserQuestion(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse to verify structure
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	// Find the AskUserQuestion PreToolUse hook (has matcher)
	var askCmd string
	for _, group := range settings.Hooks["PreToolUse"] {
		if group.Matcher == "AskUserQuestion" {
			for _, h := range group.Hooks {
				if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
					askCmd = h.Command
				}
			}
		}
	}
	if askCmd == "" {
		t.Fatal("no AskUserQuestion PreToolUse hook with matcher found")
	}

	// The AskUserQuestion hook should touch (create) the marker
	markerFile := filepath.Join(tmpDir, "test-marker")
	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, askCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("marker file should exist after AskUserQuestion PreToolUse, but it does not")
	}
}

func TestSettingsJson_PreToolUse_hook_clears_marker_for_other_tools(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse to find the catch-all PreToolUse hook (no matcher)
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	// Find the catch-all PreToolUse hook (negative lookahead matcher, has rm -f)
	var clearCmd string
	for _, group := range settings.Hooks["PreToolUse"] {
		if group.Matcher != "" && group.Matcher != "AskUserQuestion" {
			for _, h := range group.Hooks {
				if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
					clearCmd = h.Command
				}
			}
		}
	}
	if clearCmd == "" {
		t.Fatal("no catch-all PreToolUse hook found (with negative lookahead matcher)")
	}

	// Create marker, then run the catch-all hook — marker should be REMOVED
	markerFile := filepath.Join(tmpDir, "test-marker")
	if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}
	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, clearCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("marker file should have been removed by catch-all PreToolUse, but it still exists")
	}
}

func TestSettingsJson_PostToolUse_hook_creates_cooldown_file(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse to find the PostToolUse hook
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Hooks []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	var postToolCmd string
	for _, group := range settings.Hooks["PostToolUse"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
				postToolCmd = h.Command
			}
		}
	}
	if postToolCmd == "" {
		t.Fatal("no GHOST_TAB_MARKER_FILE PostToolUse hook found")
	}

	// The PostToolUse hook should create a cooldown file (marker-cooldown)
	markerFile := filepath.Join(tmpDir, "test-marker")
	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, postToolCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	cooldownFile := markerFile + "-cooldown"
	if _, err := os.Stat(cooldownFile); os.IsNotExist(err) {
		t.Error("cooldown file should exist after PostToolUse hook, but it does not")
	}
}

func TestSettingsJson_UserPromptSubmit_hook_clears_marker(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	// Generate hooks
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Parse UserPromptSubmit hook command
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Hooks []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	var userPromptCmd string
	for _, group := range settings.Hooks["UserPromptSubmit"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
				userPromptCmd = h.Command
			}
		}
	}
	if userPromptCmd == "" {
		t.Fatal("no GHOST_TAB_MARKER_FILE UserPromptSubmit hook found")
	}

	markerFile := filepath.Join(tmpDir, "test-marker")

	// Create marker file
	if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	// Run the UserPromptSubmit command — marker should be REMOVED
	bashScript := fmt.Sprintf(
		`export GHOST_TAB_MARKER_FILE=%q; %s`,
		markerFile, userPromptCmd,
	)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("marker file should have been removed after UserPromptSubmit, but it still exists")
	}
}

func TestSettingsJson_generated_hooks_contain_no_ask_references_or_stdin_reading(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings.json should have been created: %v", err)
	}
	content := string(data)
	// No .ask references
	assertNotContains(t, content, ".ask")
	// No stdin reading (cat) — uses matcher instead
	assertNotContains(t, content, "$(cat)")
	assertNotContains(t, content, "_gt_in")
}

func TestSettingsJson_merge_claude_settings_skips_when_already_configured(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/statusline-wrapper.sh"
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_claude_settings %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "already configured")
}

// --- cleanup_waiting_indicator_hooks (wrapper.sh cleanup logic) ---

// cleanupHooksSnippet builds a bash snippet that sources the required libraries
// and runs the cleanup-time hook removal logic extracted from wrapper.sh.
// It simulates the conditional: if claude + no other markers, remove hooks.
// markerDir controls where the snippet looks for marker files (for test isolation).
func cleanupHooksSnippet(t *testing.T, aiTool, settingsFile, markerDir string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	settingsJsonPath := filepath.Join(root, "lib", "settings-json.sh")
	return fmt.Sprintf(`source %q && source %q
SELECTED_AI_TOOL=%q
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  if ! ls %s/ghost-tab-waiting-* &>/dev/null; then
    remove_waiting_indicator_hooks %q
  fi
fi
`, tuiPath, settingsJsonPath, aiTool, markerDir, settingsFile)
}

func TestCleanupHooksRemoval_removes_hooks_when_claude_and_no_markers(t *testing.T) {
	tmpDir := t.TempDir()
	markerDir := filepath.Join(tmpDir, "markers")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatalf("failed to create marker dir: %v", err)
	}
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && touch \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && rm -f \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ]
  }
}
`)

	// No marker files exist in markerDir — hooks should be removed
	snippet := cleanupHooksSnippet(t, "claude", settingsFile, markerDir)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "removed")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	assertNotContains(t, string(data), "GHOST_TAB_MARKER_FILE")
}

func TestCleanupHooksRemoval_skips_when_other_markers_exist(t *testing.T) {
	tmpDir := t.TempDir()
	markerDir := filepath.Join(tmpDir, "markers")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatalf("failed to create marker dir: %v", err)
	}
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && touch \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && rm -f \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ]
  }
}
`)

	// Create a marker file in the isolated marker dir to simulate another session
	markerFile := filepath.Join(markerDir, "ghost-tab-waiting-99999")
	if err := os.WriteFile(markerFile, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create marker file: %v", err)
	}

	snippet := cleanupHooksSnippet(t, "claude", settingsFile, markerDir)
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Hooks should still be present because another marker exists
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	assertContains(t, string(data), "GHOST_TAB_MARKER_FILE")
}

func TestCleanupHooksRemoval_skips_when_not_claude(t *testing.T) {
	tmpDir := t.TempDir()
	markerDir := filepath.Join(tmpDir, "markers")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatalf("failed to create marker dir: %v", err)
	}
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && touch \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "[ -n \"$GHOST_TAB_MARKER_FILE\" ] && rm -f \"$GHOST_TAB_MARKER_FILE\""}]
      }
    ]
  }
}
`)

	// No marker files, but AI tool is opencode — hooks should NOT be removed
	snippet := cleanupHooksSnippet(t, "opencode", settingsFile, markerDir)
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	assertContains(t, string(data), "GHOST_TAB_MARKER_FILE")
}

func TestCleanupHooksRemoval_cleans_orphaned_markers_from_dead_pids(t *testing.T) {
	tmpDir := t.TempDir()
	markerDir := filepath.Join(tmpDir, "markers")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		t.Fatalf("failed to create marker dir: %v", err)
	}
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	// Create a marker with a PID that definitely doesn't exist
	orphanedMarker := filepath.Join(markerDir, "ghost-tab-waiting-99999999")
	if err := os.WriteFile(orphanedMarker, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create orphaned marker: %v", err)
	}

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	settingsJsonPath := filepath.Join(root, "lib", "settings-json.sh")
	snippet := fmt.Sprintf(`source %q && source %q
SELECTED_AI_TOOL="claude"
# Clean up orphaned markers from dead sessions
for marker in %s/ghost-tab-waiting-*; do
  [ -f "$marker" ] || continue
  pid="${marker##*-}"
  if ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$marker"
  fi
done
if ! ls %s/ghost-tab-waiting-* &>/dev/null; then
  remove_waiting_indicator_hooks %q
fi
`, tuiPath, settingsJsonPath, markerDir, markerDir, settingsFile)

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "removed")

	// Orphaned marker should have been cleaned up
	if _, err := os.Stat(orphanedMarker); !os.IsNotExist(err) {
		t.Error("orphaned marker should have been cleaned up")
	}
	// Hooks should be removed (no live markers remain)
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	assertNotContains(t, string(data), "GHOST_TAB_MARKER_FILE")
}

// --- ask sidecar file tests ---

func TestSettingsJson_AskUserQuestion_hook_creates_ask_sidecar_file(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Matcher string      `json:"matcher"`
		Hooks   []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	// Find the AskUserQuestion PreToolUse hook
	var askCmd string
	for _, group := range settings.Hooks["PreToolUse"] {
		if group.Matcher == "AskUserQuestion" {
			for _, h := range group.Hooks {
				if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
					askCmd = h.Command
				}
			}
		}
	}
	if askCmd == "" {
		t.Fatal("no AskUserQuestion PreToolUse hook found")
	}

	// The AskUserQuestion hook should create both the marker AND the -ask sidecar
	markerFile := filepath.Join(tmpDir, "test-marker")
	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, askCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("marker file should exist after AskUserQuestion hook")
	}
	askFile := markerFile + "-ask"
	if _, err := os.Stat(askFile); os.IsNotExist(err) {
		t.Error("-ask sidecar file should exist after AskUserQuestion hook")
	}
}

func TestSettingsJson_catchall_PreToolUse_clears_ask_sidecar(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Matcher string      `json:"matcher"`
		Hooks   []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	// Find the catch-all PreToolUse hook (negative lookahead matcher)
	var clearCmd string
	for _, group := range settings.Hooks["PreToolUse"] {
		if group.Matcher != "" && group.Matcher != "AskUserQuestion" {
			for _, h := range group.Hooks {
				if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
					clearCmd = h.Command
				}
			}
		}
	}
	if clearCmd == "" {
		t.Fatal("no catch-all PreToolUse hook found")
	}

	// Create marker and -ask sidecar, then run catch-all — both should be removed
	markerFile := filepath.Join(tmpDir, "test-marker")
	askFile := markerFile + "-ask"
	os.WriteFile(markerFile, []byte(""), 0644)
	os.WriteFile(askFile, []byte(""), 0644)

	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, clearCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("marker file should be removed by catch-all hook")
	}
	if _, err := os.Stat(askFile); !os.IsNotExist(err) {
		t.Error("-ask sidecar file should be removed by catch-all hook")
	}
}

func TestSettingsJson_UserPromptSubmit_clears_ask_sidecar(t *testing.T) {
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Hooks []hookEntry `json:"hooks"`
	}
	var settings struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	var submitCmd string
	for _, group := range settings.Hooks["UserPromptSubmit"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "GHOST_TAB_MARKER_FILE") {
				submitCmd = h.Command
			}
		}
	}
	if submitCmd == "" {
		t.Fatal("no UserPromptSubmit hook found")
	}

	// Create marker and -ask sidecar, run UserPromptSubmit — both should be removed
	markerFile := filepath.Join(tmpDir, "test-marker")
	askFile := markerFile + "-ask"
	os.WriteFile(markerFile, []byte(""), 0644)
	os.WriteFile(askFile, []byte(""), 0644)

	bashScript := fmt.Sprintf(`export GHOST_TAB_MARKER_FILE=%q; %s`, markerFile, submitCmd)
	_, exitCode := runBashSnippet(t, bashScript, nil)
	assertExitCode(t, exitCode, 0)

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("marker file should be removed by UserPromptSubmit")
	}
	if _, err := os.Stat(askFile); !os.IsNotExist(err) {
		t.Error("-ask sidecar should be removed by UserPromptSubmit")
	}
}

func TestSettingsJson_upgrades_AskUserQuestion_hook_without_ask_sidecar(t *testing.T) {
	tmpDir := t.TempDir()
	// Old format: has all current hooks BUT the AskUserQuestion command only touches marker (no -ask sidecar)
	settingsFile := writeTempFile(t, tmpDir, "settings.json", `{
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "AskUserQuestion",
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      },
      {
        "matcher": "^(?!AskUserQuestion$)",
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then touch \"${GHOST_TAB_MARKER_FILE}-cooldown\"; fi"}]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{"type": "command", "command": "if [ -n \"$GHOST_TAB_MARKER_FILE\" ]; then rm -f \"$GHOST_TAB_MARKER_FILE\"; fi"}]
      }
    ]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`add_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "upgraded")

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	content := string(data)
	// After upgrade, AskUserQuestion hook should create -ask sidecar
	assertContains(t, content, "-ask")
	// Clear commands should also remove -ask sidecar
	assertContains(t, content, `"Stop"`)
	assertContains(t, content, `"PreToolUse"`)
	assertContains(t, content, `"PostToolUse"`)
	assertContains(t, content, `"UserPromptSubmit"`)
}
