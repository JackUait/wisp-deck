package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settingsJsonSnippet sources the UI helpers used for status messages and the
// settings JSON library, then runs body.
func settingsJsonSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	return fmt.Sprintf("source %q && source %q && %s",
		filepath.Join(root, "lib", "tui.sh"),
		filepath.Join(root, "lib", "settings-json.sh"), body)
}

func TestSettingsJson_merge_claude_settings_creates_file_when_missing(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "settings.json")
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_claude_settings %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Created Claude settings with status line")
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), `"statusLine"`)
	assertContains(t, string(data), "statusline-wrapper.sh")
}

func TestSettingsJson_merge_claude_settings_adds_to_existing_file(t *testing.T) {
	dir := t.TempDir()
	settingsFile := writeTempFile(t, dir, "settings.json", "{\n  \"hooks\": {}\n}\n")
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_claude_settings %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Added status line")
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), `"hooks"`)
	assertContains(t, string(data), `"statusLine"`)
}

func TestSettingsJson_merge_claude_settings_skips_existing_status_line(t *testing.T) {
	dir := t.TempDir()
	settingsFile := writeTempFile(t, dir, "settings.json", `{
  "statusLine": {"type": "command", "command": "custom"}
}
`)
	before, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_claude_settings %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "already configured")
	after, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("existing status line settings were rewritten")
	}
}

func TestSettingsJson_merge_subagent_statusline_creates_file(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "settings.json")
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`merge_subagent_statusline %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Created Claude settings with subagent status line")
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), `"subagentStatusLine"`)
	assertContains(t, string(data), "subagent-statusline.sh")
}

func TestSettingsJson_remove_waiting_indicator_hooks_removes_all_historicalMarkers(t *testing.T) {
	dir := t.TempDir()
	settingsFile := writeTempFile(t, dir, "settings.json", `{
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "touch $WISP_DECK_MARKER_FILE"}]}],
    "Notification": [{"hooks": [{"type": "command", "command": "touch $GHOST_TAB_MARKER_FILE"}]}],
    "PreToolUse": [{"hooks": [{"type": "command", "command": "rm -f ${WISP_DECK_MARKER_FILE}-ask"}]}],
    "PostToolUse": [{"hooks": [{"type": "command", "command": "touch ${WISP_DECK_MARKER_FILE}-cooldown"}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "rm -f $WISP_DECK_MARKER_FILE"}]}]
  }
}
`)
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "removed" {
		t.Fatalf("output = %q, want removed", strings.TrimSpace(out))
	}
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, string(data), "WISP_DECK_MARKER_FILE")
	assertNotContains(t, string(data), "GHOST_TAB_MARKER_FILE")
	assertNotContains(t, string(data), `"hooks"`)
}

func TestSettingsJson_remove_waiting_indicator_hooks_preserves_unrelatedEntries(t *testing.T) {
	dir := t.TempDir()
	settingsFile := writeTempFile(t, dir, "settings.json", `{
  "model": "opus",
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "touch $WISP_DECK_MARKER_FILE"}]},
      {"matcher": "user", "hooks": [{"type": "command", "command": "echo user-hook"}]}
    ]
  }
}
`)
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	assertNotContains(t, content, "WISP_DECK_MARKER_FILE")
	assertContains(t, content, "echo user-hook")
	assertContains(t, content, `"matcher": "user"`)
	assertContains(t, content, `"model": "opus"`)
}

func TestSettingsJson_remove_waiting_indicator_hooks_returns_not_found_withoutMarkers(t *testing.T) {
	dir := t.TempDir()
	settingsFile := writeTempFile(t, dir, "settings.json", `{"model":"opus"}`)
	before, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "not_found" {
		t.Fatalf("output = %q, want not_found", strings.TrimSpace(out))
	}
	after, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("settings without migration hooks were rewritten")
	}
}

func TestSettingsJson_remove_waiting_indicator_hooks_does_not_clobber_invalidJSON(t *testing.T) {
	dir := t.TempDir()
	settingsFile := writeTempFile(t, dir, "settings.json", "{invalid\n")
	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settingsFile))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "not_found" {
		t.Fatalf("output = %q, want not_found", strings.TrimSpace(out))
	}
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{invalid\n" {
		t.Fatalf("invalid settings were clobbered: %q", data)
	}
}
