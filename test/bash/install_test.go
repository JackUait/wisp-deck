package bash_test

import (
	"fmt"
	"os"
	"os/exec"
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

func opencodePluginDestination(configHome string) string {
	return filepath.Join(configHome, "opencode", "plugins", "wisp-deck.ts")
}

func assertKnownOpenCodePluginsAbsent(t *testing.T, configHome string) {
	t.Helper()
	for _, name := range []string{"wisp-deck.ts", "ghost-tab.ts"} {
		path := filepath.Join(configHome, "opencode", "plugins", name)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("known OpenCode sound plugin %s was not retired: %v", path, err)
		}
	}
}

func TestRetireKnownOpenCodeSoundPlugins_removes_exact_known_files(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	pluginDir := filepath.Join(configHome, "opencode", "plugins")
	writeTempFile(t, pluginDir, "wisp-deck.ts", "legacy adapter fixture\n")
	writeTempFile(t, pluginDir, "ghost-tab.ts", "legacy sound fixture\n")
	binDir := mockCommand(t, dir, "shasum", `
case "$*" in
  *wisp-deck.ts*) printf '93acddeb65141aaee763c3dd891a7006a1716137a2fdeda6a05cf7fec1fe01f4  %s\n' "$3" ;;
  *ghost-tab.ts*) printf 'a7ed3712ba0bb00f77c351c236073fc2d71cf80b644c6acaca19f1bced6fb218  %s\n' "$3" ;;
  *) exit 1 ;;
esac
`)
	env := buildEnv(t, []string{binDir}, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)
	out, code := runBashSnippet(t, installSnippet(t, `retire_known_opencode_sound_plugins`), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("retirement contaminated stdout: %q", out)
	}
	assertKnownOpenCodePluginsAbsent(t, configHome)
}

func TestRetireKnownOpenCodeSoundPlugins_preserves_unknown_edits_and_symlinks(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	pluginDir := filepath.Join(configHome, "opencode", "plugins")
	unknown := writeTempFile(t, pluginDir, "wisp-deck.ts", "locally edited plugin\n")
	target := writeTempFile(t, dir, "target.ts", "legacy sound fixture\n")
	symlink := filepath.Join(pluginDir, "ghost-tab.ts")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)
	_, code := runBashSnippet(t, installSnippet(t, `retire_known_opencode_sound_plugins`), env)
	assertExitCode(t, code, 0)
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "locally edited plugin\n" {
		t.Fatalf("unknown plugin was changed: %q, %v", data, err)
	}
	if info, err := os.Lstat(symlink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("plugin symlink was changed: %v, %v", info, err)
	}
}

func TestRetireKnownOpenCodeSoundPlugins_does_not_create_config_tree(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "missing-config")
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)
	_, code := runBashSnippet(t, installSnippet(t, `retire_known_opencode_sound_plugins`), env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(configHome); !os.IsNotExist(err) {
		t.Fatalf("retirement created a config tree: %v", err)
	}
}

func TestRetireKnownOpenCodeSoundPlugins_works_when_sourced_from_zsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	plugin := writeTempFile(t, filepath.Join(configHome, "opencode", "plugins"), "wisp-deck.ts", "legacy fixture\n")
	binDir := mockCommand(t, dir, "shasum", `printf '93acddeb65141aaee763c3dd891a7006a1716137a2fdeda6a05cf7fec1fe01f4  %s\n' "$3"`)
	command := exec.Command(zsh, "-c", `source "$INSTALL_LIB" && retire_known_opencode_sound_plugins`)
	command.Env = buildEnv(t, []string{binDir},
		"HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome,
		"INSTALL_LIB="+filepath.Join(projectRoot(t), "lib", "install.sh"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("zsh retirement failed: %v: %s", err, output)
	}
	if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
		t.Fatalf("zsh retirement preserved exact known plugin: %v", err)
	}
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
	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `echo "#!/bin/bash" > "$dest"; exit 0`))
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
	binDir := mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/jqlang/jq/releases/tag/jq-1.7.1\r\n"; exit 0; fi`, curlCalls),
		`printf '#!/bin/bash\necho "jq-1.7.1"\n' > "$dest"; exit 0`))
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
	binDir := mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/jqlang/jq/releases/tag/jq-1.7.1\r\n"; exit 0; fi`, curlCalls),
		`printf '#!/bin/bash\necho "jq-1.7.1"\n' > "$dest"; exit 0`))
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
// ensure_wisp_deck_tui tests (binary download, not build from source)
// ============================================================

func TestEnsureWispDeckTui_skips_when_binary_version_matches(t *testing.T) {
	dir := t.TempDir()
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.4.0")
	curlCalls := filepath.Join(dir, "curl-calls")
	// Mock binary reports the exact version and proves the production boundary.
	binDir := mockCommand(t, dir, "wisp-deck-tui", `
case "$1" in
  --version) echo "wisp-deck-tui version 2.4.0" ;;
  capabilities)
    [ "$2" = "--require-production" ] || exit 1
    echo '`+validTuiCapabilities+`'
    ;;
  *) exit 1 ;;
esac
`)
	mockCommand(t, dir, "curl", fmt.Sprintf(`printf 'called\n' >> %q; exit 1`, curlCalls))
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "wisp-deck-tui is up to date")
	if _, err := os.Stat(curlCalls); err == nil {
		t.Error("valid existing artifact should be kept without calling curl")
	}
}

func TestEnsureWispDeckTui_updates_when_version_mismatch(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.5.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	// Mock binary that reports old version
	binDir := mockCommand(t, dir, "wisp-deck-tui", `
if [ "$1" = "--version" ]; then echo "wisp-deck-tui version 2.4.0"; exit 0; fi
echo "I exist"
`)
	download := filepath.Join(dir, "downloaded-wisp-deck-tui")
	writeTuiArtifact(t, download, "2.5.0", validTuiCapabilities, 0)
	mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q`, curlCalls),
		fmt.Sprintf(`cp %q "$dest"; exit 0`, download)))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir}, "HOME="+fakeHome)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Updating wisp-deck-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "2.5.0")
}

func TestEnsureWispDeckTui_updates_when_no_version_flag(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.5.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	// Mock old binary that doesn't support --version (exits non-zero)
	binDir := mockCommand(t, dir, "wisp-deck-tui", `
if [ "$1" = "--version" ]; then echo "Error: unknown flag: --version" >&2; exit 1; fi
echo "I exist"
`)
	download := filepath.Join(dir, "downloaded-wisp-deck-tui")
	writeTuiArtifact(t, download, "2.5.0", validTuiCapabilities, 0)
	mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q`, curlCalls),
		fmt.Sprintf(`cp %q "$dest"; exit 0`, download)))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir}, "HOME="+fakeHome)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Updating wisp-deck-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "2.5.0")
}

func TestEnsureWispDeckTui_x86_64_downloads_amd64_tui_asset(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.2.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	download := filepath.Join(dir, "downloaded-wisp-deck-tui")
	writeTuiArtifact(t, download, "2.2.0", validTuiCapabilities, 0)
	binDir := mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q`, curlCalls),
		fmt.Sprintf(`cp %q "$dest"; exit 0`, download)))
	unameDir := mockCommand(t, dir, "uname", `echo "x86_64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	// Use explicit PATH so the real wisp-deck-tui (if installed) is not found.
	env := buildEnv(t, []string{binDir, unameDir}, "HOME="+fakeHome)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "wisp-deck-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "wisp-deck-tui-darwin-amd64")
	assertContains(t, string(calls), "2.2.0")
}

func TestEnsureWispDeckTui_fails_when_download_fails(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.2.0")

	binDir := mockCommand(t, dir, "curl", `exit 1`)
	unameDir := mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
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
ensure_base_requirements
echo "$called"
`, tuiPath, installPath)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "jq")
	assertContains(t, out, "tmux")
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

// A directly-installed `opencode` binary launches ~3x faster than
// `npx opencode-ai@latest` (no per-launch registry check or reinstall), so the
// installer installs it globally via npm when possible and only falls back to
// npx when that is unavailable.

func TestEnsureOpencode_installs_globally_via_npm_when_absent(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	npmLog := filepath.Join(dir, "npm_calls")
	// opencode is NOT mocked → not yet installed. npm is available.
	binDir := mockCommand(t, dir, "npm", fmt.Sprintf(`echo "$@" >> %q; exit 0`, npmLog))
	mockCommand(t, dir, "brew", `exit 1`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mktemp", "cmp")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenCode installed")
	npmCalls, _ := os.ReadFile(npmLog)
	assertContains(t, string(npmCalls), "install -g opencode-ai")
	assertKnownOpenCodePluginsAbsent(t, configHome)
}

func TestEnsureOpencode_skips_install_when_already_present(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	npmLog := filepath.Join(dir, "npm_calls")
	binDir := mockCommand(t, dir, "opencode", `echo opencode`)
	mockCommand(t, dir, "npm", fmt.Sprintf(`echo "$@" >> %q; exit 0`, npmLog))
	mockCommand(t, dir, "brew", `exit 1`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mktemp", "cmp")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenCode already installed")
	if _, err := os.Stat(npmLog); err == nil {
		t.Errorf("npm must not run when opencode is already installed")
	}
	assertKnownOpenCodePluginsAbsent(t, configHome)
}

func TestEnsureOpencode_falls_back_to_npx_when_npm_install_fails(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	// npm present but its install fails; npx is available as the fallback.
	binDir := mockCommand(t, dir, "npm", `exit 1`)
	mockCommand(t, dir, "npx", `echo npx`)
	mockCommand(t, dir, "brew", `exit 1`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mktemp", "cmp")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenCode ready")
	assertKnownOpenCodePluginsAbsent(t, configHome)
}

func TestEnsureOpencode_uses_npx_when_npm_is_unavailable(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	binDir := mockCommand(t, dir, "npx", `echo npx`)
	mockCommand(t, dir, "brew", `exit 1`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mktemp", "cmp")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "PATH="+binDir+":/bin")

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenCode ready")
	assertKnownOpenCodePluginsAbsent(t, configHome)
}

func TestEnsureOpencode_warns_when_no_node(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	// Neither npm nor npx on PATH — use restricted PATH.
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0755)
	mockCommand(t, dir, "brew", `exit 1`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mktemp", "cmp")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 1)
	assertContains(t, out, "Node.js")
	if _, err := os.Stat(opencodePluginDestination(configHome)); !os.IsNotExist(err) {
		t.Fatalf("plugin must not be installed when OpenCode cannot run: %v", err)
	}
}

func TestEnsureOpencode_removes_brew_opencode(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	brewLog := filepath.Join(dir, "brew_calls")
	npmLog := filepath.Join(dir, "npm_calls")
	// Mock brew: "list opencode" succeeds (installed via brew), log uninstall
	binDir := mockCommand(t, dir, "brew", fmt.Sprintf(`
echo "$@" >> %q
if [ "$1" = "list" ] && [ "$2" = "opencode" ]; then exit 0; fi
if [ "$1" = "uninstall" ]; then exit 0; fi
exit 1
`, brewLog))
	// npm available so the (post-removal) global install path runs.
	mockCommand(t, dir, "npm", fmt.Sprintf(`echo "$@" >> %q; exit 0`, npmLog))
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "dirname", "mktemp", "cmp")
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	brewCalls, _ := os.ReadFile(brewLog)
	assertContains(t, string(brewCalls), "uninstall opencode")
	assertContains(t, out, "Removing brew-installed OpenCode")
	assertContains(t, out, "OpenCode installed")
	assertKnownOpenCodePluginsAbsent(t, configHome)
}

func TestEnsureOpencode_fails_when_known_plugin_retirement_fails(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	writeTempFile(t, filepath.Join(configHome, "opencode", "plugins"), "wisp-deck.ts", "legacy fixture\n")
	binDir := mockCommand(t, dir, "opencode", `exit 0`)
	mockCommand(t, dir, "brew", `exit 1`)
	mockCommand(t, dir, "shasum", `printf '93acddeb65141aaee763c3dd891a7006a1716137a2fdeda6a05cf7fec1fe01f4  %s\n' "$3"`)
	mockCommand(t, dir, "rm", `exit 73`)
	snippet := installSnippet(t, `ensure_opencode`)
	env := buildEnv(t, []string{binDir}, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)

	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Fatal("ensure_opencode reported success after known-plugin retirement failed")
	}
	assertContains(t, out, "known OpenCode sound plugin")
}

func TestWispDeckSetup_retires_known_opencode_sound_plugins_unconditionally(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "wisp-deck"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	start := strings.Index(content, "# OpenCode sound-plugin retirement")
	end := strings.Index(content, "# ---------- Summary ----------")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("cannot locate OpenCode retirement block")
	}
	block := content[start:end]
	if !strings.Contains(block, "retire_known_opencode_sound_plugins") {
		t.Fatal("setup must invoke known OpenCode sound-plugin retirement")
	}
	if strings.Contains(block, `SELECTED_AI_TOOL" = "opencode`) ||
		strings.Contains(block, `SELECTED_AI_TOOL" = 'opencode`) {
		t.Fatalf("OpenCode sound-plugin retirement is conditional on the selected tool:\n%s", block)
	}
}
