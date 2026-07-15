package bash_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return got
}

func TestSettingsJsonClaudeLaunchSettingsMergesActiveConfigAndDisablesNativeNotifications(t *testing.T) {
	dir := t.TempDir()
	generationDir := filepath.Join(dir, "runtime", "generation.Abc123")
	if err := os.MkdirAll(generationDir, 0o700); err != nil {
		t.Fatal(err)
	}
	active := writeTempFile(t, dir, "active.json", `{
  "model": "opus",
  "preferredNotifChannel": "iterm2",
  "disableAllHooks": false,
  "permissions": {"allow": ["Read"]},
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo user-stop"}]}]},
  "enabledPlugins": {"user-plugin@example": true}
}
`)
	before, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`write_claude_launch_settings %q %q`, generationDir, active))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	target := filepath.Join(generationDir, "claude-settings.json")
	if strings.TrimSpace(out) != target {
		t.Fatalf("output path = %q, want %q", strings.TrimSpace(out), target)
	}
	got := readJSONMap(t, target)
	if got["preferredNotifChannel"] != "notifications_disabled" {
		t.Fatalf("preferredNotifChannel = %#v, want notifications_disabled", got["preferredNotifChannel"])
	}
	if got["disableAllHooks"] != true {
		t.Fatalf("disableAllHooks = %#v, want true", got["disableAllHooks"])
	}
	if got["model"] != "opus" {
		t.Fatalf("model = %#v, want opus", got["model"])
	}
	permissions, ok := got["permissions"].(map[string]any)
	if !ok || len(permissions) != 1 {
		t.Fatalf("permissions not preserved: %#v", got["permissions"])
	}
	hooks, ok := got["hooks"].(map[string]any)
	if !ok || len(hooks) != 1 {
		t.Fatalf("hooks not preserved under disableAllHooks: %#v", got["hooks"])
	}
	plugins, ok := got["enabledPlugins"].(map[string]any)
	if !ok || plugins["user-plugin@example"] != true {
		t.Fatalf("enabledPlugins not preserved under disableAllHooks: %#v", got["enabledPlugins"])
	}
	after, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("active Wisp settings file was mutated")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("launch settings mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSettingsJsonClaudeLaunchSettingsWithoutActiveConfigOnlyDisablesNativeNotifications(t *testing.T) {
	generationDir := filepath.Join(t.TempDir(), "generation.Empty1")
	if err := os.Mkdir(generationDir, 0o700); err != nil {
		t.Fatal(err)
	}

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`write_claude_launch_settings %q ""`, generationDir))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	got := readJSONMap(t, filepath.Join(generationDir, "claude-settings.json"))
	if len(got) != 2 || got["preferredNotifChannel"] != "notifications_disabled" || got["disableAllHooks"] != true {
		t.Fatalf("launch settings = %#v, want only strict notification and hook overrides", got)
	}
}

func TestSettingsJsonClaudeLaunchSettingsFailureKeepsPreviousAtomicFile(t *testing.T) {
	dir := t.TempDir()
	generationDir := filepath.Join(dir, "generation.Atomic1")
	if err := os.Mkdir(generationDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := writeTempFile(t, generationDir, "claude-settings.json", "{\"sentinel\":true}\n")
	invalid := writeTempFile(t, dir, "invalid.json", "{invalid\n")

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`write_claude_launch_settings %q %q`, generationDir, invalid))
	_, code := runBashSnippet(t, snippet, nil)
	if code == 0 {
		t.Fatal("invalid active settings unexpectedly succeeded")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"sentinel\":true}\n" {
		t.Fatalf("atomic target changed after failure: %q", data)
	}
	matches, err := filepath.Glob(filepath.Join(generationDir, ".claude-settings.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary settings files leaked: %v", matches)
	}
}

func TestSettingsJsonRemoveWaitingHooksPreservesUserHooksInMixedEntries(t *testing.T) {
	dir := t.TempDir()
	settings := writeTempFile(t, dir, "settings.json", `{
  "model": "opus",
  "preferredNotifChannel": "iterm2",
  "hooks": {
    "Stop": [{
      "matcher": "keep-me",
      "hooks": [
        {"type": "command", "command": "touch $WISP_DECK_MARKER_FILE"},
        {"type": "command", "command": "echo user-stop"}
      ]
    }],
    "Notification": [{
      "hooks": [
        {"type": "command", "command": "rm -f $GHOST_TAB_MARKER_FILE"},
        {"type": "command", "command": "echo user-notification"}
      ]
    }],
    "SessionStart": [{
      "hooks": [{"type": "command", "command": "echo user-start"}]
    }]
  }
}
`)

	snippet := settingsJsonSnippet(t,
		fmt.Sprintf(`remove_waiting_indicator_hooks %q`, settings))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "removed" {
		t.Fatalf("output = %q, want removed", strings.TrimSpace(out))
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, removed := range []string{"WISP_DECK_MARKER_FILE", "GHOST_TAB_MARKER_FILE"} {
		assertNotContains(t, content, removed)
	}
	for _, preserved := range []string{"echo user-stop", "echo user-notification", "echo user-start", `"matcher": "keep-me"`, `"model": "opus"`, `"preferredNotifChannel": "iterm2"`} {
		assertContains(t, content, preserved)
	}
}

func TestSettingsJsonMigratesLegacyNotificationLeaseWithoutClobberingUserSettings(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "wisp")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := writeTempFile(t, dir, "settings.json", `{
  "model": "opus",
  "preferredNotifChannel": "terminal_bell"
}
`)
	legacy := writeTempFile(t, configDir, "prev-notif-channel", "iterm2")

	snippet := settingsJsonSnippet(t, fmt.Sprintf(
		`migrate_legacy_claude_notif_channel %q %q`, settings, configDir))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	got := readJSONMap(t, settings)
	if got["preferredNotifChannel"] != "iterm2" || got["model"] != "opus" {
		t.Fatalf("migrated settings = %#v", got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy notification lease remains: %v", err)
	}

	// A different live value is a user change made after the legacy lease was
	// written. Consume the stale lease without rolling that choice back.
	if err := os.WriteFile(settings, []byte(`{"preferredNotifChannel":"notifications_disabled"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy = writeTempFile(t, configDir, "prev-notif-channel", "terminal_bell")
	_, code = runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got = readJSONMap(t, settings)
	if got["preferredNotifChannel"] != "notifications_disabled" {
		t.Fatalf("user notification choice was clobbered: %#v", got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("stale notification lease remains: %v", err)
	}
}

func TestSettingsJsonMigratesLegacyUnsetThroughSymlinkAtomically(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "wisp")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := writeTempFile(t, dir, "shared-settings.json", `{
  "model": "sonnet",
  "preferredNotifChannel": "terminal_bell"
}
`)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, settings); err != nil {
		t.Fatal(err)
	}
	legacy := writeTempFile(t, configDir, "prev-notif-channel", "__UNSET__")

	snippet := settingsJsonSnippet(t, fmt.Sprintf(
		`migrate_legacy_claude_notif_channel %q %q`, settings, configDir))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	link, err := os.Readlink(settings)
	if err != nil || link != target {
		t.Fatalf("settings symlink = %q, %v; want %q", link, err, target)
	}
	got := readJSONMap(t, target)
	if _, exists := got["preferredNotifChannel"]; exists || got["model"] != "sonnet" {
		t.Fatalf("migrated unset settings = %#v", got)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("target mode after migration = %v, %v", info, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy notification lease remains: %v", err)
	}
}

func TestSettingsJsonLegacyNotificationMigrationRetriesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "wisp")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := writeTempFile(t, dir, "settings.json", "{invalid\n")
	legacy := writeTempFile(t, configDir, "prev-notif-channel", "iterm2")

	snippet := settingsJsonSnippet(t, fmt.Sprintf(
		`migrate_legacy_claude_notif_channel %q %q`, settings, configDir))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(settings)
	if err != nil || string(data) != "{invalid\n" {
		t.Fatalf("invalid settings changed = %q, %v", data, err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("retry lease was consumed after invalid JSON: %v", err)
	}
}

func TestSettingsJsonConcurrentUpgradeKeepsHookAndNotificationMigrations(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "wisp")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := writeTempFile(t, dir, "settings.json", `{
  "preferredNotifChannel": "terminal_bell",
  "hooks": {
    "Stop": [{"hooks": [
      {"type":"command","command":"touch $WISP_DECK_MARKER_FILE"},
      {"type":"command","command":"echo keep-user-hook"}
	    ]}]
	  }
	}
	`)
	legacy := writeTempFile(t, configDir, "prev-notif-channel", "iterm2")

	root := projectRoot(t)
	script := fmt.Sprintf(`source %q
for i in 1 2 3 4 5 6 7 8; do
  (
    remove_waiting_indicator_hooks %q %q >/dev/null
    migrate_legacy_claude_notif_channel %q %q
  ) &
done
wait
`, filepath.Join(root, "lib", "settings-json.sh"), settings, configDir, settings, configDir)
	_, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)

	got := readJSONMap(t, settings)
	if got["preferredNotifChannel"] != "iterm2" {
		t.Fatalf("concurrent migration lost restored notification value: %#v", got)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, string(data), "WISP_DECK_MARKER_FILE")
	assertContains(t, string(data), "keep-user-hook")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy notification lease remains: %v", err)
	}
}

func TestSettingsJsonHookMigrationDoesNotOverwriteExternalAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "wisp")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := writeTempFile(t, dir, "settings.json", fmt.Sprintf(`{
  "padding": %q,
  "hooks": {"Stop": [{"hooks": [
    {"type":"command","command":"touch $WISP_DECK_MARKER_FILE"}
  ]}]}
}

`, strings.Repeat("x", 16*1024*1024)))
	replacement := []byte(`{"userReplacement":true,"preferredNotifChannel":"iterm2"}` + "\n")

	script := fmt.Sprintf(`source %q
remove_waiting_indicator_hooks %q %q >/dev/null
`, filepath.Join(projectRoot(t), "lib", "settings-json.sh"), settings, configDir)
	cmd := exec.Command("bash", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(dir, ".wisp-hooks.*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("hook migration never reached atomic temporary write")
		}
		time.Sleep(time.Millisecond)
	}
	replacementPath := filepath.Join(dir, ".external-settings")
	if err := os.WriteFile(replacementPath, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, settings); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("hook migration after external replacement: %v", err)
	}
	got, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(replacement) {
		prefix := got
		if len(prefix) > 120 {
			prefix = prefix[:120]
		}
		t.Fatalf("external settings replacement was overwritten: %d bytes, prefix %q", len(got), prefix)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".wisp-hooks.*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("hook migration temporaries remain: %v, %v", matches, err)
	}
}

func TestSettingsJsonLegacyNotificationMigrationDoesNotOverwriteExternalAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "wisp")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := writeTempFile(t, dir, "settings.json", fmt.Sprintf(`{
  "padding": %q,
  "preferredNotifChannel": "terminal_bell"
}
`, strings.Repeat("x", 8*1024*1024)))
	legacy := writeTempFile(t, configDir, "prev-notif-channel", "iterm2")
	replacement := []byte(`{"userReplacement":true,"preferredNotifChannel":"notifications_disabled"}` + "\n")

	script := fmt.Sprintf(`source %q
migrate_legacy_claude_notif_channel %q %q
`, filepath.Join(projectRoot(t), "lib", "settings-json.sh"), settings, configDir)
	cmd := exec.Command("bash", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(dir, ".wisp-settings-migration.*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("notification migration never reached atomic temporary write")
		}
		time.Sleep(time.Millisecond)
	}
	replacementPath := filepath.Join(dir, ".external-settings")
	if err := os.WriteFile(replacementPath, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, settings); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("notification migration after external replacement: %v", err)
	}
	got, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(replacement) {
		prefix := got
		if len(prefix) > 120 {
			prefix = prefix[:120]
		}
		t.Fatalf("external settings replacement was overwritten: %d bytes, prefix %q", len(got), prefix)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy lease should remain for retry after raced write: %v", err)
	}
	_, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("stale lease was not consumed on clean retry: %v", err)
	}
	got, err = os.ReadFile(settings)
	if err != nil || string(got) != string(replacement) {
		t.Fatalf("clean retry changed user replacement: %q, %v", got, err)
	}
}

func TestClaudeSettingsMigrationRemovesLegacyRuntimeLifecycle(t *testing.T) {
	root := projectRoot(t)
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(data)
	}

	settingsJSON := read("lib/settings-json.sh")
	notifications := read("lib/notification-setup.sh")
	wrapper := read("wrapper.sh")

	for _, obsolete := range []string{"add_waiting_indicator_hooks"} {
		assertNotContains(t, settingsJSON, obsolete)
	}
	for _, obsolete := range []string{
		"setup_sound_notification", "remove_sound_notification",
		"set_claude_notif_channel", "restore_claude_notif_channel",
		"prev-notif-channel",
	} {
		assertNotContains(t, notifications, obsolete)
		assertNotContains(t, wrapper, obsolete)
	}
	for _, obsolete := range []string{
		"add_waiting_indicator_hooks", "WISP_DECK_MARKER_FILE",
		"GHOST_TAB_MARKER_FILE", "wisp-deck-waiting-",
	} {
		assertNotContains(t, wrapper, obsolete)
	}

	if strings.Count(wrapper, "remove_waiting_indicator_hooks") != 1 {
		t.Fatalf("wrapper migration calls = %d, want exactly one", strings.Count(wrapper, "remove_waiting_indicator_hooks"))
	}
	if strings.Count(wrapper, "migrate_legacy_claude_notif_channel") != 1 {
		t.Fatalf("wrapper notification migration calls = %d, want exactly one", strings.Count(wrapper, "migrate_legacy_claude_notif_channel"))
	}
	begin := strings.Index(wrapper, "attention_begin_generation")
	legacyRestore := strings.Index(wrapper, "migrate_legacy_claude_notif_channel")
	generate := strings.Index(wrapper, "write_claude_launch_settings")
	build := strings.Index(wrapper, "AI_LAUNCH_CMD=\"$(build_ai_launch_cmd")
	if begin < 0 || legacyRestore < 0 || generate < 0 || build < 0 || !(begin < legacyRestore && legacyRestore < generate && generate < build) {
		t.Fatalf("launch ordering invalid: begin=%d legacy=%d generate=%d build=%d", begin, legacyRestore, generate, build)
	}
	assertContains(t, wrapper, `${WISP_DECK_ATTENTION_FILE%/state}`)
	sourceResolve := strings.Index(wrapper, `WISP_DECK_CLAUDE_SETTINGS_SOURCE="$(resolve_claude_config_path`)
	if sourceResolve < 0 {
		t.Fatal("active Claude settings source is not resolved")
	}
	claudeGenerate := strings.Index(wrapper[sourceResolve:], `if [ "$SELECTED_AI_TOOL" = "claude" ]`)
	if claudeGenerate < 0 {
		t.Fatal("active Claude settings source must be resolved even when another tool launches first")
	}
	contextStart := strings.Index(wrapper, `write_relaunch_context "$WISP_DECK_RELAUNCH_FILE"`)
	if contextStart < 0 || !strings.Contains(wrapper[contextStart:], `"$WISP_DECK_CLAUDE_SETTINGS_SOURCE"`) {
		t.Fatal("relaunch context must preserve the active Claude settings source for later tool switches")
	}
}
