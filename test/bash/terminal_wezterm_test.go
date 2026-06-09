package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func weztermAdapterSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "wezterm.sh")
	return fmt.Sprintf("source %q && source %q && source %q && %s",
		tuiPath, installPath, adapterPath, body)
}

func TestWeztermAdapter_get_config_path(t *testing.T) {
	snippet := weztermAdapterSnippet(t, `terminal_get_config_path`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	home := os.Getenv("HOME")
	expected := home + "/.wezterm.lua"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestWeztermAdapter_install_calls_ensure_cask(t *testing.T) {
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "Applications", "WezTerm.app")
	os.MkdirAll(appDir, 0755)

	snippet := weztermAdapterSnippet(t, fmt.Sprintf(
		`APPLICATIONS_DIR=%q terminal_install`, filepath.Join(tmpDir, "Applications")))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "WezTerm found")
}

func TestWeztermAdapter_setup_config_creates_new(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, ".wezterm.lua")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := weztermAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "default_prog")
	assertContains(t, content, wrapperPath)
}

func TestWeztermAdapter_setup_config_replaces_existing(t *testing.T) {
	tmpDir := t.TempDir()
	existing := "local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nconfig.default_prog = { '/old/path' }\nreturn config\n"
	configFile := writeTempFile(t, tmpDir, ".wezterm.lua", existing)
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := weztermAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, wrapperPath)
	assertNotContains(t, content, "/old/path")
	assertContains(t, content, "wezterm.config_builder()")
}

func TestWeztermAdapter_setup_config_inserts_before_return(t *testing.T) {
	tmpDir := t.TempDir()
	existing := "local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nconfig.font_size = 14\nreturn config\n"
	configFile := writeTempFile(t, tmpDir, ".wezterm.lua", existing)
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := weztermAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "default_prog")
	assertContains(t, content, wrapperPath)
	assertContains(t, content, "font_size = 14")
	assertContains(t, content, "return config")
}

func TestWeztermAdapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "open", `echo "$@" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := weztermAdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("open not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "-na WezTerm --args start -- /bin/bash -l /w/wrapper.sh --restore /p/app claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWeztermAdapter_cleanup_config_removes_default_prog(t *testing.T) {
	tmpDir := t.TempDir()
	existing := "local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nconfig.default_prog = { '/some/path' }\nconfig.font_size = 14\nreturn config\n"
	configFile := writeTempFile(t, tmpDir, ".wezterm.lua", existing)

	snippet := weztermAdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, configFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertNotContains(t, content, "default_prog")
	assertContains(t, content, "font_size = 14")
}
