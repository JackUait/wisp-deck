package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// switchSnippet sources tui + adapter loader and runs body with HOME pointed at home.
func switchSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	registryPath := filepath.Join(root, "lib", "terminals", "registry.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "adapter.sh")
	return fmt.Sprintf("source %q && source %q && source %q && source %q && %s",
		tuiPath, installPath, registryPath, adapterPath, body)
}

// writeGhosttyConfig creates a fake HOME with a ghostty config containing the
// ghost-tab command line plus user settings. Returns (home, configPath).
func writeGhosttyConfig(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "ghostty")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config")
	content := "font-size = 14\ncommand = direct:/bin/bash -l /x/.config/ghost-tab/wrapper.sh\ntheme = dark\n"
	if err := os.WriteFile(cfg, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return home, cfg
}

func TestCleanupPreviousTerminal_removes_old_terminal_config_on_switch(t *testing.T) {
	home, cfg := writeGhosttyConfig(t)
	prefFile := filepath.Join(home, "pref")
	if err := os.WriteFile(prefFile, []byte("ghostty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := switchSnippet(t,
		fmt.Sprintf(`cleanup_previous_terminal %q iterm2`, prefFile))
	env := buildEnv(t, nil, "HOME="+home)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	_ = out

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	assertNotContains(t, content, "command =")
	assertContains(t, content, "font-size = 14")
	assertContains(t, content, "theme = dark")
}

func TestCleanupPreviousTerminal_noop_when_terminal_unchanged(t *testing.T) {
	home, cfg := writeGhosttyConfig(t)
	prefFile := filepath.Join(home, "pref")
	if err := os.WriteFile(prefFile, []byte("ghostty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := switchSnippet(t,
		fmt.Sprintf(`cleanup_previous_terminal %q ghostty`, prefFile))
	env := buildEnv(t, nil, "HOME="+home)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), "command = direct:/bin/bash -l /x/.config/ghost-tab/wrapper.sh")
}

func TestCleanupPreviousTerminal_noop_when_no_previous_preference(t *testing.T) {
	home, cfg := writeGhosttyConfig(t)
	prefFile := filepath.Join(home, "missing-pref")

	snippet := switchSnippet(t,
		fmt.Sprintf(`cleanup_previous_terminal %q iterm2`, prefFile))
	env := buildEnv(t, nil, "HOME="+home)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), "command = direct:/bin/bash -l /x/.config/ghost-tab/wrapper.sh")
}

func TestCleanupPreviousTerminal_noop_for_unknown_previous_terminal(t *testing.T) {
	home, _ := writeGhosttyConfig(t)
	prefFile := filepath.Join(home, "pref")
	if err := os.WriteFile(prefFile, []byte("nonsense\n"), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := switchSnippet(t,
		fmt.Sprintf(`cleanup_previous_terminal %q iterm2`, prefFile))
	env := buildEnv(t, nil, "HOME="+home)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
}

func TestCleanupPreviousTerminal_does_not_clobber_loaded_adapter(t *testing.T) {
	// Cleaning the old terminal must not overwrite terminal_* functions of the
	// adapter already loaded for the newly selected terminal.
	home, cfg := writeGhosttyConfig(t)
	prefFile := filepath.Join(home, "pref")
	if err := os.WriteFile(prefFile, []byte("ghostty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := switchSnippet(t, fmt.Sprintf(`
load_terminal_adapter iterm2
cleanup_previous_terminal %q iterm2
terminal_get_config_path
`, prefFile))
	env := buildEnv(t, nil, "HOME="+home)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "DynamicProfiles/ghost-tab.json") {
		t.Errorf("loaded adapter clobbered, got: %s", out)
	}

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, string(data), "command =")
}
