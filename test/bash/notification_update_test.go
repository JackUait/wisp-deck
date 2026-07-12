package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// notificationSnippet builds a bash snippet that sources tui.sh, settings-json.sh,
// and notification-setup.sh, then runs the provided bash code.
func notificationSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	settingsJsonPath := filepath.Join(root, "lib", "settings-json.sh")
	notifPath := filepath.Join(root, "lib", "notification-setup.sh")
	return fmt.Sprintf("source %q && source %q && source %q && %s",
		tuiPath, settingsJsonPath, notifPath, body)
}

// updateSnippet builds a bash snippet that sources update.sh then runs the provided bash code.
func updateSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	updatePath := filepath.Join(root, "lib", "update.sh")
	return fmt.Sprintf("source %q && %s", updatePath, body)
}

// ==================== notification-setup.sh tests ====================

// --- setup_sound_notification ---

// --- is_sound_enabled ---

func TestNotification_is_sound_enabled_returns_true_when_features_file_missing(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistentDir := filepath.Join(tmpDir, "nonexistent")

	snippet := notificationSnippet(t,
		fmt.Sprintf(`is_sound_enabled "claude" %q`, nonexistentDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "true" {
		t.Errorf("expected 'true', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_is_sound_enabled_returns_true_when_sound_key_missing(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`is_sound_enabled "claude" %q`, tmpDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "true" {
		t.Errorf("expected 'true', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_is_sound_enabled_returns_true_when_sound_is_true(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": true}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`is_sound_enabled "claude" %q`, tmpDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "true" {
		t.Errorf("expected 'true', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_is_sound_enabled_returns_false_when_sound_is_false(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": false}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`is_sound_enabled "claude" %q`, tmpDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "false" {
		t.Errorf("expected 'false', got %q", strings.TrimSpace(out))
	}
}

// --- remove_sound_notification ---

// --- set_sound_feature_flag ---

func TestNotification_set_sound_feature_flag_creates_file_with_sound_true(t *testing.T) {
	tmpDir := t.TempDir()

	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_sound_feature_flag "claude" %q true`, tmpDir))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	featuresFile := filepath.Join(tmpDir, "claude-features.json")
	if _, err := os.Stat(featuresFile); os.IsNotExist(err) {
		t.Fatalf("expected features file to be created at %s", featuresFile)
	}

	// Verify sound is true using python3
	verifySnippet := fmt.Sprintf(
		`python3 -c "import json; print(json.load(open('%s'))['sound'])"`, featuresFile)
	out, code := runBashSnippet(t, verifySnippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("expected 'True', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_set_sound_feature_flag_sets_sound_false_in_existing_file(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": true}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_sound_feature_flag "claude" %q false`, tmpDir))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	featuresFile := filepath.Join(tmpDir, "claude-features.json")
	verifySnippet := fmt.Sprintf(
		`python3 -c "import json; print(json.load(open('%s'))['sound'])"`, featuresFile)
	out, code := runBashSnippet(t, verifySnippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "False" {
		t.Errorf("expected 'False', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_set_sound_feature_flag_preserves_other_keys(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": false, "other": 42}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_sound_feature_flag "claude" %q true`, tmpDir))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	featuresFile := filepath.Join(tmpDir, "claude-features.json")
	verifySnippet := fmt.Sprintf(
		`python3 -c "import json; d=json.load(open('%s')); print(d['sound'], d['other'])"`, featuresFile)
	out, code := runBashSnippet(t, verifySnippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "True 42" {
		t.Errorf("expected 'True 42', got %q", strings.TrimSpace(out))
	}
}

// --- get_sound_name ---

func TestNotification_get_sound_name_returns_Bottle_when_features_file_missing(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistentDir := filepath.Join(tmpDir, "nonexistent")

	snippet := notificationSnippet(t,
		fmt.Sprintf(`get_sound_name "claude" %q`, nonexistentDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Bottle" {
		t.Errorf("expected 'Bottle', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_get_sound_name_returns_Bottle_when_sound_name_key_missing(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": true}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`get_sound_name "claude" %q`, tmpDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Bottle" {
		t.Errorf("expected 'Bottle', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_get_sound_name_returns_stored_name(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": true, "sound_name": "Glass"}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`get_sound_name "claude" %q`, tmpDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Glass" {
		t.Errorf("expected 'Glass', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_get_sound_name_returns_empty_when_sound_disabled(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": false}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`get_sound_name "claude" %q`, tmpDir))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty string, got %q", strings.TrimSpace(out))
	}
}

// --- set_sound_name ---

func TestNotification_set_sound_name_writes_name_to_features_file(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": true}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_sound_name "claude" %q "Glass"`, tmpDir))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	featuresFile := filepath.Join(tmpDir, "claude-features.json")
	verifySnippet := fmt.Sprintf(
		`python3 -c "import json; print(json.load(open('%s'))['sound_name'])"`, featuresFile)
	out, code := runBashSnippet(t, verifySnippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Glass" {
		t.Errorf("expected 'Glass', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_set_sound_name_creates_file_when_missing(t *testing.T) {
	tmpDir := t.TempDir()

	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_sound_name "claude" %q "Ping"`, tmpDir))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	featuresFile := filepath.Join(tmpDir, "claude-features.json")
	verifySnippet := fmt.Sprintf(
		`python3 -c "import json; d=json.load(open('%s')); print(d['sound_name'])"`, featuresFile)
	out, code := runBashSnippet(t, verifySnippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Ping" {
		t.Errorf("expected 'Ping', got %q", strings.TrimSpace(out))
	}
}

func TestNotification_set_sound_name_preserves_other_keys(t *testing.T) {
	tmpDir := t.TempDir()
	writeTempFile(t, tmpDir, "claude-features.json", `{"sound": true, "other": 42}`)

	snippet := notificationSnippet(t,
		fmt.Sprintf(`set_sound_name "claude" %q "Hero"`, tmpDir))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	featuresFile := filepath.Join(tmpDir, "claude-features.json")
	verifySnippet := fmt.Sprintf(
		`python3 -c "import json; d=json.load(open('%s')); print(d['sound_name'], d['other'])"`, featuresFile)
	out, code := runBashSnippet(t, verifySnippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Hero 42" {
		t.Errorf("expected 'Hero 42', got %q", strings.TrimSpace(out))
	}
}

// ==================== lib/update.sh tests (npm-based) ====================

// --- notify_if_update_available ---

func TestUpdate_notify_if_update_available_does_nothing_when_no_flag(t *testing.T) {
	dir := t.TempDir()

	snippet := updateSnippet(t, fmt.Sprintf(`
XDG_CONFIG_HOME=%q
notify_if_update_available
`, filepath.Join(dir, "config")))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output when no flag file, got %q", out)
	}
}

func TestUpdate_notify_if_update_available_shows_version_and_keeps_flag(t *testing.T) {
	// The flag must survive the notify so the main menu (which reads it via
	// get_update_version) can keep showing the update notice until the update
	// actually runs.
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "update-available", "2.7.0")
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	snippet := updateSnippet(t, fmt.Sprintf(`
XDG_CONFIG_HOME=%q
notify_if_update_available %q
`, filepath.Join(dir, "config"), installDir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "2.7.0")
	assertContains(t, out, "npx wisp-deck")
	flagFile := filepath.Join(configDir, "update-available")
	if _, err := os.Stat(flagFile); err != nil {
		t.Errorf("expected flag file to survive notify_if_update_available, got %v", err)
	}
}

func TestUpdate_notify_if_update_available_silent_when_flag_matches_installed(t *testing.T) {
	// A stale flag equal to the installed version (left over from before an
	// update, inside the 24h re-check throttle) must not nag.
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "update-available", "2.7.0")
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.7.0")

	snippet := updateSnippet(t, fmt.Sprintf(`
XDG_CONFIG_HOME=%q
notify_if_update_available %q
`, filepath.Join(dir, "config"), installDir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output when flag matches installed version, got %q", out)
	}
}

// --- get_update_version ---

func TestUpdate_get_update_version_prints_version_when_newer(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "update-available", "2.7.0")
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	snippet := updateSnippet(t, fmt.Sprintf(`
XDG_CONFIG_HOME=%q
get_update_version %q
`, filepath.Join(dir, "config"), installDir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "2.7.0" {
		t.Errorf("expected '2.7.0', got %q", strings.TrimSpace(out))
	}
}

func TestUpdate_get_update_version_empty_when_flag_matches_installed(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "update-available", "2.7.0")
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.7.0")

	snippet := updateSnippet(t, fmt.Sprintf(`
XDG_CONFIG_HOME=%q
get_update_version %q
`, filepath.Join(dir, "config"), installDir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output when up to date, got %q", out)
	}
}

func TestUpdate_get_update_version_empty_when_no_flag(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	snippet := updateSnippet(t, fmt.Sprintf(`
XDG_CONFIG_HOME=%q
get_update_version %q
`, filepath.Join(dir, "config"), installDir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output when no flag file, got %q", out)
	}
}

// --- run_wisp_deck_update ---

func TestUpdate_run_wisp_deck_update_runs_npx_latest(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "npx-args")
	binDir := mockCommand(t, dir, "npx", fmt.Sprintf(`echo "$@" > %q`, argsFile))
	env := buildEnv(t, []string{binDir})

	snippet := updateSnippet(t, `run_wisp_deck_update`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("expected npx to be invoked: %v", err)
	}
	assertContains(t, string(data), "wisp-deck@latest")
}

// --- check_for_update ---

func TestUpdate_check_for_update_does_nothing_when_npm_not_available(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// Use a PATH without npm but with basic utilities
	env := buildEnv(t, nil,
		"PATH=/bin:/usr/bin",
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	// No sleep needed — function returns immediately when npm not found
	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	flagFile := filepath.Join(dir, "config", "wisp-deck", "update-available")
	if _, err := os.Stat(flagFile); !os.IsNotExist(err) {
		t.Errorf("expected no flag file when npm not available")
	}
}

func TestUpdate_check_for_update_does_nothing_when_version_file_missing(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	// No .version file

	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
sleep 0.3
`, installDir))
	env := buildEnv(t, nil, "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	flagFile := filepath.Join(dir, "config", "wisp-deck", "update-available")
	if _, err := os.Stat(flagFile); !os.IsNotExist(err) {
		t.Errorf("expected no flag file when .version missing")
	}
}

func TestUpdate_check_for_update_writes_flag_when_newer_version_exists(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// Mock npm to return a newer version
	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.7.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	snippet := updateSnippet(t, fmt.Sprintf(`check_for_update %q`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	// Polled, not slept on: the update check is disowned and both its streams go
	// to /dev/null (it outlives the launch, so anything it printed would land on
	// the AI tool's UI). It therefore holds nothing open that the harness waits
	// for, and the flag lands whenever npm finishes. The flag is the contract —
	// wait for it.
	flagFile := filepath.Join(dir, "config", "wisp-deck", "update-available")
	data := waitForFile(t, flagFile, "expected flag file to be written")
	if strings.TrimSpace(data) != "2.7.0" {
		t.Errorf("expected flag content '2.7.0', got %q", strings.TrimSpace(data))
	}
}

func TestUpdate_check_for_update_does_nothing_when_up_to_date(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// Mock npm to return same version
	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.6.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
sleep 0.3
`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	flagFile := filepath.Join(dir, "config", "wisp-deck", "update-available")
	if _, err := os.Stat(flagFile); !os.IsNotExist(err) {
		t.Errorf("expected no flag file when already up to date")
	}
}

func TestUpdate_check_for_update_clears_stale_flag_when_up_to_date(t *testing.T) {
	// After an update installs, a flag written before it becomes stale; the next
	// check must remove it so the menu stops offering the update.
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.7.0")
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	writeTempFile(t, configDir, "update-available", "2.7.0")

	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.7.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	snippet := updateSnippet(t, fmt.Sprintf(`check_for_update %q`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	// Same as above: the disowned check holds nothing the harness waits on, so
	// poll for the clearing rather than sleeping a guess.
	flagFile := filepath.Join(configDir, "update-available")
	waitForFileGone(t, flagFile, "expected stale flag to be cleared when up to date")
}

func TestUpdate_check_for_update_skips_when_checked_recently(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// Write last-update-check with timestamp 1 hour ago (recent — should be skipped)
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	recentTS := strconv.FormatInt(time.Now().Unix()-3600, 10)
	writeTempFile(t, configDir, "last-update-check", recentTS)

	// Mock npm to return a newer version — if throttle works, npm must NOT be called
	// and no update-available flag should be written.
	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.7.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
sleep 0.3
`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	// If throttle is NOT implemented, npm will run and write "2.7.0" to the flag.
	// The test fails (correctly) because update-available exists when it shouldn't.
	flagFile := filepath.Join(configDir, "update-available")
	if _, err := os.Stat(flagFile); err == nil {
		t.Errorf("expected no update-available flag when check was skipped due to recent timestamp")
	}
}

func TestUpdate_check_for_update_runs_when_last_check_is_stale(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// Write last-update-check with timestamp 25 hours ago (stale — check must run)
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	staleTS := strconv.FormatInt(time.Now().Unix()-90000, 10)
	writeTempFile(t, configDir, "last-update-check", staleTS)

	// Mock npm to return a newer version
	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.7.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	before := time.Now().Unix()
	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
sleep 0.3
`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	after := time.Now().Unix()
	assertExitCode(t, code, 0)

	// Assert update-available flag was written with correct version
	flagFile := filepath.Join(configDir, "update-available")
	data, err := os.ReadFile(flagFile)
	if err != nil {
		t.Fatalf("expected update-available flag file to be written, got error: %v", err)
	}
	if strings.TrimSpace(string(data)) != "2.7.0" {
		t.Errorf("expected flag content '2.7.0', got %q", strings.TrimSpace(string(data)))
	}

	// Assert last-update-check was refreshed (only passes once throttle feature exists)
	tsFile := filepath.Join(configDir, "last-update-check")
	tsData, err := os.ReadFile(tsFile)
	if err != nil {
		t.Fatalf("expected last-update-check to be refreshed after stale run, got error: %v", err)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(tsData)), 10, 64)
	if err != nil {
		t.Fatalf("expected numeric timestamp in last-update-check, got %q", strings.TrimSpace(string(tsData)))
	}
	if ts < before || ts > after+1 {
		t.Errorf("refreshed timestamp %d out of expected range [%d, %d]", ts, before, after+1)
	}
}

func TestUpdate_check_for_update_runs_when_timestamp_is_in_future(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// Write a last-update-check timestamp that is 1 hour IN THE FUTURE
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)
	futureTs := fmt.Sprintf("%d", time.Now().Unix()+3600) // 1 hour from now
	writeTempFile(t, configDir, "last-update-check", futureTs)

	// Mock npm to return a newer version — it MUST be called (future ts should not throttle)
	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.7.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
sleep 0.3
`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	// Flag MUST be written — future timestamp should not suppress the check
	flagFile := filepath.Join(dir, "config", "wisp-deck", "update-available")
	data, err := os.ReadFile(flagFile)
	if err != nil {
		t.Fatalf("expected flag file when future timestamp was present: %v", err)
	}
	if strings.TrimSpace(string(data)) != "2.7.0" {
		t.Errorf("expected flag content '2.7.0', got %q", strings.TrimSpace(string(data)))
	}
}

func TestUpdate_check_for_update_writes_timestamp_after_check(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	os.MkdirAll(installDir, 0755)
	writeTempFile(t, installDir, ".version", "2.6.0")

	// No last-update-check file (first run)
	configDir := filepath.Join(dir, "config", "wisp-deck")
	os.MkdirAll(configDir, 0755)

	// Mock npm to return same version (no update)
	npmBody := `
if [ "$1" = "view" ] && [ "$2" = "wisp-deck" ] && [ "$3" = "version" ]; then
  echo "2.6.0"
  exit 0
fi
exit 1
`
	binDir := mockCommand(t, dir, "npm", npmBody)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	before := time.Now().Unix()
	snippet := updateSnippet(t, fmt.Sprintf(`
check_for_update %q
sleep 0.3
`, installDir))
	_, code := runBashSnippet(t, snippet, env)
	after := time.Now().Unix()
	assertExitCode(t, code, 0)

	tsFile := filepath.Join(configDir, "last-update-check")
	data, err := os.ReadFile(tsFile)
	if err != nil {
		t.Fatalf("expected last-update-check file to be written, got error: %v", err)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatalf("expected numeric timestamp in last-update-check, got %q", strings.TrimSpace(string(data)))
	}
	if ts < before || ts > after+1 {
		t.Errorf("timestamp %d out of expected range [%d, %d]", ts, before, after+1)
	}
}
