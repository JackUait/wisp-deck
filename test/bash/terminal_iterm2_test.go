package bash_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func iterm2AdapterSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "iterm2.sh")
	return fmt.Sprintf("source %q && source %q && source %q && %s",
		tuiPath, installPath, adapterPath, body)
}

// mockDefaultsCommand creates a mock 'defaults' command that logs all calls
// and returns readOutput for 'read' operations.
func mockDefaultsCommand(t *testing.T, dir string, readOutput string) (string, string) {
	t.Helper()
	logFile := filepath.Join(dir, "defaults-calls.log")
	body := fmt.Sprintf(`echo "$@" >> %q
if [ "$1" = "read" ]; then
    echo %q
fi`, logFile, readOutput)
	binDir := mockCommand(t, dir, "defaults", body)
	return binDir, logFile
}

func TestIterm2Adapter_get_config_path_returns_dynamic_profile(t *testing.T) {
	snippet := iterm2AdapterSnippet(t, `terminal_get_config_path`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	home := os.Getenv("HOME")
	expected := home + "/Library/Application Support/iTerm2/DynamicProfiles/ghost-tab.json"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestIterm2Adapter_get_wrapper_path(t *testing.T) {
	snippet := iterm2AdapterSnippet(t, `terminal_get_wrapper_path`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	home := os.Getenv("HOME")
	expected := home + "/.config/ghost-tab/wrapper.sh"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestIterm2Adapter_install_calls_ensure_cask(t *testing.T) {
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "Applications", "iTerm.app")
	os.MkdirAll(appDir, 0755)

	snippet := iterm2AdapterSnippet(t, fmt.Sprintf(
		`APPLICATIONS_DIR=%q terminal_install`, filepath.Join(tmpDir, "Applications")))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "iTerm found")
}

func TestIterm2Adapter_setup_config_creates_json_file(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "DynamicProfiles", "ghost-tab.json")
	wrapperPath := "/path/to/wrapper.sh"

	binDir, _ := mockDefaultsCommand(t, tmpDir, "mock-guid")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, profilePath, wrapperPath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		t.Fatal("expected dynamic profile JSON to be created")
	}
}

func TestIterm2Adapter_setup_config_json_has_correct_profile(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "DynamicProfiles", "ghost-tab.json")
	wrapperPath := "/test/wrapper.sh"

	binDir, _ := mockDefaultsCommand(t, tmpDir, "mock-guid")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, profilePath, wrapperPath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("failed to read profile JSON: %v", err)
	}

	var profile map[string]interface{}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent: %s", err, string(data))
	}

	profiles, ok := profile["Profiles"].([]interface{})
	if !ok || len(profiles) == 0 {
		t.Fatal("expected Profiles array with at least one entry")
	}

	p := profiles[0].(map[string]interface{})
	if p["Name"] != "Ghost Tab" {
		t.Errorf("Name = %q, want %q", p["Name"], "Ghost Tab")
	}
	if p["Guid"] != "ghost-tab-profile" {
		t.Errorf("Guid = %q, want %q", p["Guid"], "ghost-tab-profile")
	}
	if p["Custom Command"] != "Yes" {
		t.Errorf("Custom Command = %q, want %q", p["Custom Command"], "Yes")
	}
	if p["Command"] != wrapperPath {
		t.Errorf("Command = %q, want %q", p["Command"], wrapperPath)
	}
}

func TestIterm2Adapter_setup_config_overwrites_existing(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "DynamicProfiles", "ghost-tab.json")
	os.MkdirAll(filepath.Dir(profilePath), 0755)
	os.WriteFile(profilePath, []byte(`{"Profiles":[{"Name":"Old"}]}`), 0644)

	wrapperPath := "/new/wrapper.sh"

	binDir, _ := mockDefaultsCommand(t, tmpDir, "mock-guid")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, profilePath, wrapperPath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("failed to read profile: %v", err)
	}

	var profile map[string]interface{}
	json.Unmarshal(data, &profile)
	profiles := profile["Profiles"].([]interface{})
	p := profiles[0].(map[string]interface{})
	if p["Command"] != wrapperPath {
		t.Errorf("Command = %q, want %q", p["Command"], wrapperPath)
	}
}

func TestIterm2Adapter_setup_config_saves_previous_default_guid(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "DynamicProfiles", "ghost-tab.json")
	wrapperPath := "/path/to/wrapper.sh"

	binDir, _ := mockDefaultsCommand(t, tmpDir, "previous-guid-ABC")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, profilePath, wrapperPath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	// Saved GUID must be in config dir, NOT in DynamicProfiles dir
	savedGuidPath := filepath.Join(tmpDir, ".config", "ghost-tab", "iterm2-previous-guid")
	data, err := os.ReadFile(savedGuidPath)
	if err != nil {
		t.Fatalf("expected saved GUID file at %s: %v", savedGuidPath, err)
	}
	if strings.TrimSpace(string(data)) != "previous-guid-ABC" {
		t.Errorf("saved GUID = %q, want %q", strings.TrimSpace(string(data)), "previous-guid-ABC")
	}
}

func TestIterm2Adapter_setup_config_sets_ghost_tab_as_default(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "DynamicProfiles", "ghost-tab.json")
	wrapperPath := "/path/to/wrapper.sh"

	binDir, logFile := mockDefaultsCommand(t, tmpDir, "some-guid")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, profilePath, wrapperPath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	calls, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read defaults log: %v", err)
	}
	assertContains(t, string(calls), "write com.googlecode.iterm2 Default Bookmark Guid -string ghost-tab-profile")
}

// Regression: iTerm2 scans ALL files in DynamicProfiles/ as JSON.
// The saved GUID file must NOT be in that directory.
func TestIterm2Adapter_setup_config_no_extra_files_in_profiles_dir(t *testing.T) {
	tmpDir := t.TempDir()
	profileDir := filepath.Join(tmpDir, "DynamicProfiles")
	profilePath := filepath.Join(profileDir, "ghost-tab.json")
	wrapperPath := "/path/to/wrapper.sh"

	binDir, _ := mockDefaultsCommand(t, tmpDir, "some-guid")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, profilePath, wrapperPath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	entries, err := os.ReadDir(profileDir)
	if err != nil {
		t.Fatalf("failed to read profiles dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "ghost-tab.json" {
			t.Errorf("unexpected file in DynamicProfiles dir: %s (iTerm2 will try to parse it as JSON)", entry.Name())
		}
	}
}

func TestIterm2Adapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "osascript", `printf '%s\n' "$*" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := iterm2AdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("osascript not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := `-e tell application "iTerm" to create window with default profile command "/bin/bash -l '/w/wrapper.sh' --restore '/p/app' 'claude'"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIterm2Adapter_launch_restore_path_with_spaces(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "osascript", `printf '%s\n' "$*" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := iterm2AdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/my app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("osascript not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	// Path with space must arrive as single token — single-quoted inside the AppleScript command string.
	want := `-e tell application "iTerm" to create window with default profile command "/bin/bash -l '/w/wrapper.sh' --restore '/p/my app' 'claude'"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIterm2Adapter_cleanup_config_removes_json(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "ghost-tab.json")
	os.WriteFile(profilePath, []byte(`{"Profiles":[]}`), 0644)

	binDir, _ := mockDefaultsCommand(t, tmpDir, "")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, profilePath))
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Removed Ghost Tab profile")

	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Error("expected dynamic profile JSON to be removed")
	}
}

func TestIterm2Adapter_cleanup_config_noop_if_missing(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "nonexistent.json")

	binDir, _ := mockDefaultsCommand(t, tmpDir, "")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, profilePath))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
}

func TestIterm2Adapter_cleanup_config_restores_previous_default(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "ghost-tab.json")
	os.WriteFile(profilePath, []byte(`{"Profiles":[]}`), 0644)

	// Create saved GUID file in config dir (as terminal_setup_config would)
	configDir := filepath.Join(tmpDir, ".config", "ghost-tab")
	os.MkdirAll(configDir, 0755)
	savedGuidPath := filepath.Join(configDir, "iterm2-previous-guid")
	os.WriteFile(savedGuidPath, []byte("original-guid-XYZ"), 0644)

	binDir, logFile := mockDefaultsCommand(t, tmpDir, "")
	env := buildEnv(t, []string{binDir}, "HOME="+tmpDir)

	snippet := iterm2AdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, profilePath))
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Removed Ghost Tab profile")

	// Verify defaults write was called to restore GUID
	calls, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read defaults log: %v", err)
	}
	assertContains(t, string(calls), "write com.googlecode.iterm2 Default Bookmark Guid -string original-guid-XYZ")

	// Verify saved GUID file was removed
	if _, err := os.Stat(savedGuidPath); !os.IsNotExist(err) {
		t.Error("expected saved GUID file to be removed")
	}
}
