package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ghosttyAdapterSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "ghostty.sh")
	return fmt.Sprintf("source %q && source %q && source %q && %s",
		tuiPath, installPath, adapterPath, body)
}

func TestGhosttyAdapter_get_config_path(t *testing.T) {
	snippet := ghosttyAdapterSnippet(t, `terminal_get_config_path`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	home := os.Getenv("HOME")
	expected := home + "/.config/ghostty/config"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestGhosttyAdapter_get_wrapper_path(t *testing.T) {
	snippet := ghosttyAdapterSnippet(t, `terminal_get_wrapper_path`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	home := os.Getenv("HOME")
	expected := home + "/.config/wisp-deck/wrapper.sh"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestGhosttyAdapter_setup_config_creates_new(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Appended")

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertContains(t, string(data), "command = direct:/bin/bash -l "+wrapperPath)
}

func TestGhosttyAdapter_setup_config_replaces_existing(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "config", "command = /old/path\n")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Replaced")

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "command = direct:/bin/bash -l "+wrapperPath)
	assertNotContains(t, content, "/old/path")
}

func TestGhosttyAdapter_setup_config_preserves_other_settings(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "config", "font-size = 14\ntheme = dark\n")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "font-size = 14")
	assertContains(t, content, "theme = dark")
	assertContains(t, content, "command = direct:/bin/bash -l "+wrapperPath)
}

func TestGhosttyAdapter_setup_config_uses_direct_prefix(t *testing.T) {
	// Ghostty 1.2.x has two broken code paths for launching scripts:
	// 1. Bare path: wraps with "bash --noprofile --norc -c exec -l <path>" — exec is
	//    a no-op, script never runs, Ghostty exits in 248ms.
	// 2. Path with args (e.g. "/bin/bash -l <path>"): uses "/bin/sh -c" expansion.
	//    On macOS /bin/sh is bash in POSIX mode, which adds "--posix" to the launch
	//    command. In POSIX mode, process substitution <(...) is a syntax error that
	//    prevents loading.sh from being sourced.
	// Fix: use "direct:" prefix to bypass the /bin/sh -c mechanism entirely, so
	// Ghostty exec's "/bin/bash -l <path>" directly without adding --posix.
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config")
	wrapperPath := filepath.Join(tmpDir, "wrapper.sh")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_setup_config %q %q`, configFile, wrapperPath))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	// Must use direct: to bypass /bin/sh -c (which causes --posix mode).
	assertContains(t, content, "command = direct:/bin/bash -l")
	// Must NOT be a bare script path (broken exec -l) or without direct: (adds --posix).
	assertNotContains(t, content, "command = "+wrapperPath)
	assertNotContains(t, content, "command = /bin/bash -l")
}

func TestGhosttyAdapter_cleanup_config_removes_command_line(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "config", "font-size = 14\ncommand = direct:/bin/bash -l /Users/u/.config/wisp-deck/wrapper.sh\ntheme = dark\n")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, configFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)
	assertContains(t, content, "font-size = 14")
	assertContains(t, content, "theme = dark")
	assertNotContains(t, content, "command =")
}

func TestGhosttyAdapter_cleanup_config_removes_pre_direct_format(t *testing.T) {
	// Versions before the direct: prefix wrote "command = /bin/bash -l <wrapper>".
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "config", "command = /bin/bash -l /Users/u/.config/wisp-deck/wrapper.sh\n")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, configFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertNotContains(t, string(data), "command =")
}

func TestGhosttyAdapter_cleanup_config_preserves_user_command_line(t *testing.T) {
	// A command line the user wrote themselves (Skip path during setup)
	// must survive cleanup — only wisp-deck's own line may be removed.
	tmpDir := t.TempDir()
	configFile := writeTempFile(t, tmpDir, "config", "command = /opt/homebrew/bin/fish\n")

	snippet := ghosttyAdapterSnippet(t,
		fmt.Sprintf(`terminal_cleanup_config %q`, configFile))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	assertContains(t, string(data), "command = /opt/homebrew/bin/fish")
}

func TestGhosttyAdapter_terminal_install_skips_when_app_exists(t *testing.T) {
	dir := t.TempDir()
	fakeApps := filepath.Join(dir, "Applications")
	os.MkdirAll(filepath.Join(fakeApps, "Ghostty.app"), 0755)

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "ghostty.sh")
	script := fmt.Sprintf(`
source %q && source %q && source %q
GHOSTTY_APP_PATH=%q
terminal_install
`, tuiPath, installPath, adapterPath, filepath.Join(fakeApps, "Ghostty.app"))

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Ghostty found")
}

func TestGhosttyAdapter_terminal_install_returns_1_not_exits_when_missing(t *testing.T) {
	// Ghostty adapter should return 1 (not exit 1) when app is still
	// missing after the download page prompt, so callers can handle
	// the failure gracefully.
	dir := t.TempDir()
	fakeApps := filepath.Join(dir, "Applications")

	binDir := mockCommand(t, dir, "open", `true`)

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "ghostty.sh")
	// Call terminal_install inside a function to distinguish return vs exit.
	// If terminal_install uses exit 1, the entire subshell dies and "AFTER"
	// is never printed. If it uses return 1, the wrapper function catches it
	// and prints "AFTER".
	script := fmt.Sprintf(`
source %q && source %q && source %q
GHOSTTY_APP_PATH=%q
wrapper() { terminal_install </dev/null; }
wrapper || true
echo "AFTER"
`, tuiPath, installPath, adapterPath, filepath.Join(fakeApps, "Ghostty.app"))

	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "AFTER")
}

func TestGhosttyAdapter_terminal_install_message_omits_removed_flag(t *testing.T) {
	// Terminal selection was removed; wisp-deck takes no flags. The
	// not-found message must not tell users to run a flag that now errors.
	dir := t.TempDir()
	fakeApps := filepath.Join(dir, "Applications")

	binDir := mockCommand(t, dir, "open", `true`)

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "ghostty.sh")
	script := fmt.Sprintf(`
source %q && source %q && source %q
GHOSTTY_APP_PATH=%q
terminal_install </dev/null || true
`, tuiPath, installPath, adapterPath, filepath.Join(fakeApps, "Ghostty.app"))

	env := buildEnv(t, []string{binDir})
	out, _ := runBashSnippet(t, script, env)
	assertNotContains(t, out, "--terminal")
	assertContains(t, out, "wisp-deck")
}

func TestGhosttyAdapter_ensure_hushlogin_creates_when_absent(t *testing.T) {
	// Ghostty launches the wrapper through `login -flp`, which prints the macOS
	// "Last login: ... on ttysNNN" banner before bash runs. That banner lingers
	// through the login-shell profile load until the wrapper's splash clears it,
	// so a new tab flashes a bare shell first. `login` skips the banner when
	// ~/.hushlogin exists, so the window goes straight to the splash.
	home := t.TempDir()
	snippet := ghosttyAdapterSnippet(t, fmt.Sprintf(`ensure_hushlogin %q`, home))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(filepath.Join(home, ".hushlogin")); err != nil {
		t.Errorf("expected ~/.hushlogin to be created, stat failed: %v", err)
	}
}

func TestGhosttyAdapter_ensure_hushlogin_preserves_existing(t *testing.T) {
	// Never clobber a hushlogin the user already maintains.
	home := t.TempDir()
	hushfile := writeTempFile(t, home, ".hushlogin", "user content\n")

	snippet := ghosttyAdapterSnippet(t, fmt.Sprintf(`ensure_hushlogin %q`, home))
	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(hushfile)
	if err != nil {
		t.Fatalf("failed to read hushlogin: %v", err)
	}
	if string(data) != "user content\n" {
		t.Errorf("existing hushlogin clobbered, got %q", string(data))
	}
}

func TestGhosttyAdapter_launch_restore(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "open", `echo "$@" > `+fmt.Sprintf("%q", rec))
	env := buildEnv(t, []string{binDir})
	snippet := ghosttyAdapterSnippet(t,
		`terminal_launch_restore "/w/wrapper.sh" "/p/app" "claude"`)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("open not invoked: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "-na Ghostty --args -e /bin/bash -l /w/wrapper.sh --restore /p/app claude"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGhosttyAdapter_terminal_install_opens_download_page_when_missing(t *testing.T) {
	dir := t.TempDir()
	fakeApps := filepath.Join(dir, "Applications")
	// Ghostty.app does NOT exist

	openCalls := filepath.Join(dir, "open_calls")
	binDir := mockCommand(t, dir, "open", fmt.Sprintf(`echo "$@" >> %q`, openCalls))

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "ghostty.sh")
	// Pipe empty Enter to satisfy the read, and || true to suppress exit 1 when app still not found
	script := fmt.Sprintf(`
source %q && source %q && source %q
GHOSTTY_APP_PATH=%q
terminal_install </dev/null || true
`, tuiPath, installPath, adapterPath, filepath.Join(fakeApps, "Ghostty.app"))

	env := buildEnv(t, []string{binDir})
	_, _ = runBashSnippet(t, script, env)

	calls, _ := os.ReadFile(openCalls)
	assertContains(t, string(calls), "ghostty.org")
}
