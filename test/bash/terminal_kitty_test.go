package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func kittyAdapterSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "kitty.sh")
	return fmt.Sprintf("source %q && source %q && source %q && %s",
		tuiPath, installPath, adapterPath, body)
}

func TestKittyAdapter_get_config_path(t *testing.T) {
	snippet := kittyAdapterSnippet(t, `terminal_get_config_path`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	home := os.Getenv("HOME")
	expected := home + "/.config/kitty/kitty.conf"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestKittyAdapter_setup_config_creates_new(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "kitty.conf")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := kittyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Appended")

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertContains(t, string(data), "shell "+wrapperPath)
}

func TestKittyAdapter_setup_config_replaces_existing_shell(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "kitty.conf", "shell /old/path\nfont_size 14\n")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := kittyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Replaced")

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "shell "+wrapperPath)
	assertContains(t, content, "font_size 14")
	assertNotContains(t, content, "/old/path")
}

func TestKittyAdapter_install_calls_ensure_cask(t *testing.T) {
	tmpDir := t.TempDir()
	// Create fake app so ensure_cask finds it
	appDir := filepath.Join(tmpDir, "Applications", "kitty.app")
	os.MkdirAll(appDir, 0755)

	snippet := kittyAdapterSnippet(t, fmt.Sprintf(
		`APPLICATIONS_DIR=%q terminal_install`, filepath.Join(tmpDir, "Applications")))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "kitty found")
}

func TestKittyAdapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "open", `echo "$@" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := kittyAdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("open not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "-na kitty --args /bin/bash -l /w/wrapper.sh --restore /p/app claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestKittyAdapter_cleanup_config_removes_shell_line(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "kitty.conf", "font_size 14\nshell /some/path\ntheme dark\n")

	snippet := kittyAdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, configFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "font_size 14")
	assertNotContains(t, content, "shell ")
}
