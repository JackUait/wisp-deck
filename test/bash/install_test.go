package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installSnippet builds a bash snippet that sources tui.sh and install.sh,
// then runs the provided bash code.
func installSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	return fmt.Sprintf("source %q && source %q && %s", tuiPath, installPath, body)
}

// symlinkUsrBinTools creates symlinks in binDir pointing to the named tools
// found in /usr/bin. This lets tests build a restricted PATH that includes
// essential tools (grep, sed, tr, …) without exposing other binaries such as
// jq that may be installed on the host machine.
func symlinkUsrBinTools(t *testing.T, binDir string, names ...string) {
	t.Helper()
	for _, name := range names {
		src := filepath.Join("/usr/bin", name)
		dst := filepath.Join(binDir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue // already exists (e.g. already mocked)
		}
		if err := os.Symlink(src, dst); err != nil {
			t.Fatalf("symlinkUsrBinTools: failed to symlink %s -> %s: %v", src, dst, err)
		}
	}
}

// ============================================================
// detect_arch tests
// ============================================================

func TestDetectArch_returns_arm64_or_x86_64(t *testing.T) {
	snippet := installSnippet(t, `detect_arch`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "arm64" && got != "x86_64" {
		t.Errorf("expected arm64 or x86_64, got %q", got)
	}
}

func TestDetectArch_returns_arm64_for_arm64(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, `detect_arch`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "arm64" {
		t.Errorf("expected arm64, got %q", strings.TrimSpace(out))
	}
}

func TestDetectArch_returns_x86_64_for_x86_64(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "uname", `echo "x86_64"`)
	snippet := installSnippet(t, `detect_arch`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "x86_64" {
		t.Errorf("expected x86_64, got %q", strings.TrimSpace(out))
	}
}

// ============================================================
// install_binary tests
// ============================================================

func TestInstallBinary_downloads_and_makes_executable(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "bin", "mytool")
	binDir := mockCommand(t, dir, "curl", fmt.Sprintf(`
if [ "$1" = "-fsSL" ]; then
  echo "#!/bin/bash" > "$3"
  exit 0
fi
exit 1
`))
	snippet := installSnippet(t, fmt.Sprintf(`install_binary "https://example.com/mytool" %q "mytool"`, dest))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "mytool installed")
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		t.Errorf("expected %s to exist", dest)
	}
}

func TestInstallBinary_warns_on_curl_failure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "bin", "mytool")
	binDir := mockCommand(t, dir, "curl", `exit 1`)
	snippet := installSnippet(t, fmt.Sprintf(`install_binary "https://example.com/mytool" %q "mytool"`, dest))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	_ = code
	assertContains(t, out, "Failed")
}

// ============================================================
// ensure_jq tests
// ============================================================

func TestEnsureJq_skips_when_already_installed(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "jq", `echo "jq-1.7"`)
	snippet := installSnippet(t, `ensure_jq`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "already installed")
}

func TestEnsureJq_downloads_for_arm64(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)

	curlCalls := filepath.Join(dir, "curl_calls")
	binDir := mockCommand(t, dir, "curl", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "-fsSL" ]; then echo "binary" > "$3"; exit 0; fi
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/jqlang/jq/releases/tag/jq-1.7.1\r\n"; exit 0; fi
exit 0
`, curlCalls))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, `ensure_jq`)
	// Symlink needed /usr/bin tools except jq into the mock dir so we can
	// use a restricted PATH that does not expose the real jq binary.
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "mktemp", "tar", "unzip")
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "jq installed")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "macos-arm64")
}

func TestEnsureJq_downloads_for_x86_64(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)

	curlCalls := filepath.Join(dir, "curl_calls")
	binDir := mockCommand(t, dir, "curl", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "-fsSL" ]; then echo "binary" > "$3"; exit 0; fi
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/jqlang/jq/releases/tag/jq-1.7.1\r\n"; exit 0; fi
exit 0
`, curlCalls))
	mockCommand(t, dir, "uname", `echo "x86_64"`)
	snippet := installSnippet(t, `ensure_jq`)
	// Symlink needed /usr/bin tools except jq into the mock dir so we can
	// use a restricted PATH that does not expose the real jq binary.
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "mktemp", "tar", "unzip")
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "jq installed")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "macos-amd64")
}

// ============================================================
// ensure_ghost_tab_tui tests (binary download, not build from source)
// ============================================================

func TestEnsureGhostTabTui_skips_when_binary_version_matches(t *testing.T) {
	dir := t.TempDir()
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.4.0")
	// Mock binary that reports matching version
	binDir := mockCommand(t, dir, "ghost-tab-tui", `
if [ "$1" = "--version" ]; then echo "ghost-tab-tui version 2.4.0"; exit 0; fi
echo "I exist"
`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_ghost_tab_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ghost-tab-tui is up to date")
}

func TestEnsureGhostTabTui_updates_when_version_mismatch(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.5.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	// Mock binary that reports old version
	binDir := mockCommand(t, dir, "ghost-tab-tui", `
if [ "$1" = "--version" ]; then echo "ghost-tab-tui version 2.4.0"; exit 0; fi
echo "I exist"
`)
	mockCommand(t, dir, "curl", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "-fsSL" ]; then echo "binary" > "$3"; exit 0; fi
exit 0
`, curlCalls))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_ghost_tab_tui %q`, shareDir))
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Updating ghost-tab-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "2.5.0")
}

func TestEnsureGhostTabTui_updates_when_no_version_flag(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.5.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	// Mock old binary that doesn't support --version (exits non-zero)
	binDir := mockCommand(t, dir, "ghost-tab-tui", `
if [ "$1" = "--version" ]; then echo "Error: unknown flag: --version" >&2; exit 1; fi
echo "I exist"
`)
	mockCommand(t, dir, "curl", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "-fsSL" ]; then echo "binary" > "$3"; exit 0; fi
exit 0
`, curlCalls))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_ghost_tab_tui %q`, shareDir))
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Updating ghost-tab-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "2.5.0")
}

func TestEnsureGhostTabTui_downloads_binary_for_correct_arch(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.2.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	binDir := mockCommand(t, dir, "curl", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "-fsSL" ]; then echo "binary" > "$3"; exit 0; fi
exit 0
`, curlCalls))
	unameDir := mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_ghost_tab_tui %q`, shareDir))
	// Use explicit PATH so the real ghost-tab-tui (if installed) is not found.
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":"+unameDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ghost-tab-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "ghost-tab-tui-darwin-arm64")
	assertContains(t, string(calls), "2.2.0")
}

func TestEnsureGhostTabTui_fails_when_download_fails(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.2.0")

	binDir := mockCommand(t, dir, "curl", `exit 1`)
	unameDir := mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_ghost_tab_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir, unameDir}, "HOME="+fakeHome, "PATH="+binDir+":"+unameDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Errorf("expected non-zero exit when download fails")
	}
	assertContains(t, out, "Failed")
}

// ============================================================
// ensure_base_requirements tests
// ============================================================

func TestEnsureBaseRequirements_calls_all_installers(t *testing.T) {
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	script := fmt.Sprintf(`
source %q
source %q
called=""
ensure_jq()       { called="$called jq"; }
ensure_tmux()     { called="$called tmux"; }
ensure_lazygit()  { called="$called lazygit"; }
ensure_base_requirements
echo "$called"
`, tuiPath, installPath)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "jq")
	assertContains(t, out, "tmux")
	assertContains(t, out, "lazygit")
}

// ============================================================
// ensure_command tests (kept — still used for AI tools)
// ============================================================

func TestEnsureCommand_reports_already_installed_for_existing_command(t *testing.T) {
	snippet := installSnippet(t, `ensure_command "bash" "echo noop" "" "Bash"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "already installed")
}

func TestEnsureCommand_installs_missing_command(t *testing.T) {
	snippet := installSnippet(t, `ensure_command "nonexistent_cmd_xyz" "true" "" "TestTool"`)
	out, _ := runBashSnippet(t, snippet, nil)
	assertContains(t, out, "installed")
}

func TestEnsureCommand_warns_on_install_failure(t *testing.T) {
	snippet := installSnippet(t, `ensure_command "nonexistent_cmd_xyz" "false" "" "TestTool"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "failed")
}

// ============================================================
// ensure_cask tests
// ============================================================

func TestEnsureCask_skips_when_app_already_installed(t *testing.T) {
	tmpDir := t.TempDir()
	// Create fake /Applications/TestApp.app directory
	appDir := filepath.Join(tmpDir, "Applications", "TestApp.app")
	os.MkdirAll(appDir, 0755)

	snippet := installSnippet(t, fmt.Sprintf(
		`APPLICATIONS_DIR=%q ensure_cask "testapp" "TestApp"`, filepath.Join(tmpDir, "Applications")))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "TestApp found")
}

func TestEnsureCask_installs_via_brew_when_app_missing(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "brew.log")
	binDir := mockCommand(t, tmpDir, "brew", fmt.Sprintf(`echo "$@" >> %q`, logFile))
	env := buildEnv(t, []string{binDir})

	snippet := installSnippet(t, fmt.Sprintf(
		`APPLICATIONS_DIR=%q ensure_cask "wezterm" "WezTerm"`, filepath.Join(tmpDir, "Applications")))
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "WezTerm installed")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read brew log: %v", err)
	}
	assertContains(t, string(data), "install --cask wezterm")
}

func TestEnsureCask_exits_nonzero_when_brew_fails(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := mockCommand(t, tmpDir, "brew", `exit 1`)
	env := buildEnv(t, []string{binDir})

	snippet := installSnippet(t, fmt.Sprintf(
		`APPLICATIONS_DIR=%q ensure_cask "badcask" "BadApp"`, filepath.Join(tmpDir, "Applications")))
	_, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Error("expected non-zero exit when brew install fails")
	}
}

// ============================================================
// ensure_nerd_font tests
// ============================================================

func TestEnsureNerdFont_skips_when_font_already_installed(t *testing.T) {
	tmpDir := t.TempDir()
	fontsDir := filepath.Join(tmpDir, "Fonts")
	os.MkdirAll(fontsDir, 0755)
	// A Hack Nerd Font file is already present.
	writeTempFile(t, fontsDir, "HackNerdFontMono-Regular.ttf", "x")

	// brew must NOT be invoked; if it is, fail loudly.
	binDir := mockCommand(t, tmpDir, "brew", `echo "brew should not run" >&2; exit 3`)
	env := buildEnv(t, []string{binDir})

	snippet := installSnippet(t, fmt.Sprintf(`FONTS_DIR=%q ensure_nerd_font`, fontsDir))
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Nerd Font found")
}

func TestEnsureNerdFont_installs_via_brew_when_missing(t *testing.T) {
	tmpDir := t.TempDir()
	fontsDir := filepath.Join(tmpDir, "Fonts")
	os.MkdirAll(fontsDir, 0755)
	logFile := filepath.Join(tmpDir, "brew.log")
	binDir := mockCommand(t, tmpDir, "brew", fmt.Sprintf(`echo "$@" >> %q`, logFile))
	env := buildEnv(t, []string{binDir})

	snippet := installSnippet(t, fmt.Sprintf(`FONTS_DIR=%q ensure_nerd_font`, fontsDir))
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Nerd Font installed")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read brew log: %v", err)
	}
	assertContains(t, string(data), "install --cask font-hack-nerd-font")
}

func TestEnsureNerdFont_gracefully_warns_when_brew_fails(t *testing.T) {
	tmpDir := t.TempDir()
	fontsDir := filepath.Join(tmpDir, "Fonts")
	os.MkdirAll(fontsDir, 0755)
	binDir := mockCommand(t, tmpDir, "brew", `exit 1`)
	env := buildEnv(t, []string{binDir})

	snippet := installSnippet(t, fmt.Sprintf(`FONTS_DIR=%q ensure_nerd_font`, fontsDir))
	out, code := runBashSnippet(t, snippet, env)
	// Non-fatal: setup must continue even if the font fails to install.
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install Nerd Font")
}

// ============================================================
// ensure_opencode tests
// ============================================================

func TestEnsureOpencode_ready_when_npx_available_and_not_from_brew(t *testing.T) {
	dir := t.TempDir()
	// Mock npx on PATH
	binDir := mockCommand(t, dir, "npx", `echo "npx"`)
	// Mock brew list opencode to fail (not installed via brew)
	mockCommand(t, dir, "brew", `exit 1`)
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenCode ready")
}

func TestEnsureOpencode_warns_when_npx_not_available(t *testing.T) {
	dir := t.TempDir()
	// No npx on PATH — use restricted PATH
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0755)
	// Mock brew list to fail (not from brew)
	mockCommand(t, dir, "brew", `exit 1`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Node.js")
}

func TestEnsureOpencode_removes_brew_opencode(t *testing.T) {
	dir := t.TempDir()
	brewLog := filepath.Join(dir, "brew_calls")
	// Mock brew: "list opencode" succeeds (installed via brew), log uninstall
	binDir := mockCommand(t, dir, "brew", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "list" ] && [ "$2" = "opencode" ]; then exit 0; fi
if [ "$1" = "uninstall" ]; then exit 0; fi
exit 1
`, brewLog))
	// Mock npx available
	mockCommand(t, dir, "npx", `echo "npx"`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	brewCalls, _ := os.ReadFile(brewLog)
	assertContains(t, string(brewCalls), "uninstall opencode")
	assertContains(t, out, "Removing brew-installed OpenCode")
	assertContains(t, out, "OpenCode ready")
}
