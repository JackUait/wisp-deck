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

func TestKittyAdapter_setup_config_adds_nerd_font_symbol_map(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "kitty.conf")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := kittyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	// A symbol_map line maps the Nerd Font glyph ranges to the installed font.
	assertContains(t, content, "symbol_map ")
	assertContains(t, content, "Symbols Nerd Font Mono")
	// Must cover the three statusline icons: brain U+F09D1 and memory U+F035B
	// (Material Design Icons range U+F0001-U+F1AF0) and cpu gauge U+F0E4
	// (FontAwesome range U+F000-U+F2FF).
	assertContains(t, content, "U+F0001-U+F1AF0")
	assertContains(t, content, "U+F000-U+F2FF")
}

func TestKittyAdapter_setup_config_symbol_map_is_idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "kitty.conf")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	body := fmt.Sprintf(`terminal_setup_config %q %q && terminal_setup_config %q %q`,
		configFile, wrapperPath, configFile, wrapperPath)
	snippet := kittyAdapterSnippet(t, body)
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if n := strings.Count(string(data), "symbol_map "); n != 1 {
		t.Errorf("expected exactly one symbol_map line after two setups, got %d:\n%s", n, string(data))
	}
}

func TestKittyAdapter_cleanup_config_removes_symbol_map(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "kitty.conf",
		"font_size 14\nshell /Users/u/.config/ghost-tab/wrapper.sh\nsymbol_map U+F0001-U+F1AF0,U+F400-U+F532 Symbols Nerd Font Mono\n")

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
	assertNotContains(t, content, "symbol_map ")
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
	configFile := writeTempFile(t, tmpDir, "kitty.conf", "font_size 14\nshell /Users/u/.config/ghost-tab/wrapper.sh\ntheme dark\n")

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

func TestKittyAdapter_cleanup_config_preserves_user_shell_line(t *testing.T) {
	// A shell line the user wrote themselves (Skip path during setup)
	// must survive cleanup — only ghost-tab's own line may be removed.
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "kitty.conf", "shell /opt/homebrew/bin/fish\n")

	snippet := kittyAdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, configFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertContains(t, string(data), "shell /opt/homebrew/bin/fish")
}
