package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func opencodePluginTemplate(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "templates", "opencode-plugin.ts"))
	if err != nil {
		t.Fatalf("read OpenCode plugin template: %v", err)
	}
	return data
}

func opencodePluginDestination(configHome string) string {
	return filepath.Join(configHome, "opencode", "plugins", "wisp-deck.ts")
}

func assertOpencodePluginInstalled(t *testing.T, configHome string) {
	t.Helper()
	dest := opencodePluginDestination(configHome)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("OpenCode plugin was not installed at %s: %v", dest, err)
	}
	want := opencodePluginTemplate(t)
	if string(got) != string(want) {
		t.Fatalf("OpenCode plugin content differs from template\ngot:  %q\nwant: %q", got, want)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat OpenCode plugin: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("OpenCode plugin mode = %04o, want 0600", gotMode)
	}
}

func TestInstallOpencodePlugin_uses_xdg_destination(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	configHome := filepath.Join(dir, "xdg")
	env := buildEnv(t, nil, "HOME="+home, "XDG_CONFIG_HOME="+configHome)

	out, code := runBashSnippet(t, installSnippet(t, `umask 000; install_opencode_plugin`), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("successful plugin sync must not contaminate stdout, got %q", out)
	}
	assertOpencodePluginInstalled(t, configHome)
	if _, err := os.Stat(opencodePluginDestination(filepath.Join(home, ".config"))); !os.IsNotExist(err) {
		t.Fatalf("plugin must not be written below HOME when XDG_CONFIG_HOME is set: %v", err)
	}
}

func TestInstallOpencodePlugin_replaces_stale_file_with_sibling_rename(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	dest := writeTempFile(t, filepath.Dir(opencodePluginDestination(configHome)), "wisp-deck.ts", "stale plugin\n")
	if err := os.Chmod(dest, 0o644); err != nil {
		t.Fatal(err)
	}
	moveLog := filepath.Join(dir, "move.log")
	binDir := mockCommand(t, dir, "mv", `printf '%s\n' "$@" > "$MOVE_LOG"; exec /bin/mv "$@"`)
	env := buildEnv(t, []string{binDir}, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "MOVE_LOG="+moveLog)

	_, code := runBashSnippet(t, installSnippet(t, `install_opencode_plugin`), env)
	assertExitCode(t, code, 0)
	assertOpencodePluginInstalled(t, configHome)
	moveData, err := os.ReadFile(moveLog)
	if err != nil {
		t.Fatalf("atomic rename was not invoked: %v", err)
	}
	args := strings.Fields(string(moveData))
	if len(args) < 2 {
		t.Fatalf("mv args malformed: %q", moveData)
	}
	tmp, target := args[len(args)-2], args[len(args)-1]
	if target != dest {
		t.Fatalf("rename target = %q, want %q", target, dest)
	}
	if filepath.Dir(tmp) != filepath.Dir(dest) || tmp == dest {
		t.Fatalf("rename source %q must be a temporary sibling of %q", tmp, dest)
	}
	if leftovers, _ := filepath.Glob(dest + ".tmp.*"); len(leftovers) != 0 {
		t.Fatalf("temporary plugin files leaked: %v", leftovers)
	}
}

func TestInstallOpencodePlugin_identical_content_preserves_mtime_and_repairs_mode(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	dest := opencodePluginDestination(configHome)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, opencodePluginTemplate(t), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o666); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-4 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dest, old, old); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)

	_, code := runBashSnippet(t, installSnippet(t, `install_opencode_plugin`), env)
	assertExitCode(t, code, 0)
	after, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("identical sync changed mtime: before=%s after=%s", before.ModTime(), after.ModTime())
	}
	if got := after.Mode().Perm(); got != 0o600 {
		t.Fatalf("identical plugin mode = %04o, want 0600", got)
	}
}

func TestInstallOpencodePlugin_resolves_physical_distribution_root_through_symlinked_lib(t *testing.T) {
	dir := t.TempDir()
	physical := filepath.Join(dir, "share", "wisp-deck")
	physicalLib := filepath.Join(physical, "lib")
	if err := os.MkdirAll(physicalLib, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.sh", "tui.sh"} {
		data, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(physicalLib, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const physicalTemplate = "plugin from physical distribution root\n"
	writeTempFile(t, filepath.Join(physical, "templates"), "opencode-plugin.ts", physicalTemplate)
	configHome := filepath.Join(dir, "config")
	logicalLib := filepath.Join(configHome, "wisp-deck", "lib")
	if err := os.MkdirAll(filepath.Dir(logicalLib), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physicalLib, logicalLib); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`source %q && source %q && install_opencode_plugin`,
		filepath.Join(logicalLib, "tui.sh"), filepath.Join(logicalLib, "install.sh"))
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)

	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	got, err := os.ReadFile(opencodePluginDestination(configHome))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != physicalTemplate {
		t.Fatalf("installed %q, want physical-root template %q", got, physicalTemplate)
	}
}

func TestInstallOpencodePlugin_uses_exported_lib_dir_when_invoked_from_zsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	libDir := filepath.Join(projectRoot(t), "lib")
	cmd := exec.Command(zsh, "-c", `
cd "$TEST_PROJECT_CWD" || exit 70
source "$WISP_DECK_LIB_DIR/install.sh" || exit 71
install_opencode_plugin
`)
	cmd.Env = buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "WISP_DECK_LIB_DIR="+libDir,
		"TEST_PROJECT_CWD="+filepath.Join(dir, "unrelated-project"))
	if err := os.MkdirAll(filepath.Join(dir, "unrelated-project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zsh plugin sync failed: %v: %s", err, out)
	}
	assertOpencodePluginInstalled(t, configHome)
}

func TestInstallOpencodePlugin_failed_rename_preserves_previous_plugin(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	dest := writeTempFile(t, filepath.Dir(opencodePluginDestination(configHome)), "wisp-deck.ts", "known-good old plugin\n")
	moveLog := filepath.Join(dir, "move-attempted")
	binDir := mockCommand(t, dir, "mv", `printf attempted > "$MOVE_LOG"; exit 73`)
	env := buildEnv(t, []string{binDir}, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "MOVE_LOG="+moveLog)

	_, code := runBashSnippet(t, installSnippet(t, `install_opencode_plugin`), env)
	if code == 0 {
		t.Fatal("plugin sync succeeded despite failed atomic rename")
	}
	if _, err := os.Stat(moveLog); err != nil {
		t.Fatalf("test did not reach the failing rename: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "known-good old plugin\n" {
		t.Fatalf("failed sync changed previous plugin to %q", got)
	}
	if leftovers, _ := filepath.Glob(dest + ".tmp.*"); len(leftovers) != 0 {
		t.Fatalf("failed sync leaked temporary files: %v", leftovers)
	}
}

func TestInstallOpencodePlugin_rejects_destination_directory_without_nested_temp(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	dest := opencodePluginDestination(configHome)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, nil, "HOME="+filepath.Join(dir, "home"), "XDG_CONFIG_HOME="+configHome)

	_, code := runBashSnippet(t, installSnippet(t, `install_opencode_plugin`), env)
	if code == 0 {
		t.Fatal("plugin sync reported success with a directory at the destination path")
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("destination type changed to %s, want original directory", info.Mode())
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("plugin temp was moved inside destination directory: %v", entries)
	}
	if leftovers, _ := filepath.Glob(dest + ".tmp.*"); len(leftovers) != 0 {
		t.Fatalf("plugin temp leaked beside destination directory: %v", leftovers)
	}
}

func TestInstallOpencodePlugin_rejects_symlink_to_directory_before_creating_temp(t *testing.T) {
	dir := t.TempDir()
	configHome := filepath.Join(dir, "config")
	dest := opencodePluginDestination(configHome)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(dir, "directory-target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, dest); err != nil {
		t.Fatal(err)
	}
	mktempLog := filepath.Join(dir, "mktemp-called")
	binDir := mockCommand(t, dir, "mktemp", `printf called > "$MKTEMP_LOG"; exec /usr/bin/mktemp "$@"`)
	env := buildEnv(t, []string{binDir}, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+configHome, "MKTEMP_LOG="+mktempLog)

	_, code := runBashSnippet(t, installSnippet(t, `install_opencode_plugin`), env)
	if code == 0 {
		t.Fatal("plugin sync reported success with a symlink-to-directory destination")
	}
	if _, err := os.Stat(mktempLog); !os.IsNotExist(err) {
		t.Fatalf("plugin sync created a temp before rejecting symlink-to-directory: %v", err)
	}
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink was replaced: mode=%s", info.Mode())
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("plugin temp leaked through destination symlink: %v", entries)
	}
	if leftovers, _ := filepath.Glob(dest + ".tmp.*"); len(leftovers) != 0 {
		t.Fatalf("plugin temp leaked beside destination symlink: %v", leftovers)
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
	// Mock binary that reports matching version
	binDir := mockCommand(t, dir, "wisp-deck-tui", `
if [ "$1" = "--version" ]; then echo "wisp-deck-tui version 2.4.0"; exit 0; fi
echo "I exist"
`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "wisp-deck-tui is up to date")
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
	mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q`, curlCalls),
		`printf '#!/bin/bash\n[ "$1" = "--version" ] && echo "wisp-deck-tui version 2.5.0"\n' > "$dest"; exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/usr/bin:/bin")
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
	mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q`, curlCalls),
		`printf '#!/bin/bash\n[ "$1" = "--version" ] && echo "wisp-deck-tui version 2.5.0"\n' > "$dest"; exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Updating wisp-deck-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "2.5.0")
}

func TestEnsureWispDeckTui_downloads_binary_for_correct_arch(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0755)
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.2.0")

	curlCalls := filepath.Join(dir, "curl_calls")
	binDir := mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`echo "$@" >> %q`, curlCalls),
		`printf '#!/bin/bash\n[ "$1" = "--version" ] && echo "wisp-deck-tui version 2.2.0"\n' > "$dest"; exit 0`))
	unameDir := mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	// Use explicit PATH so the real wisp-deck-tui (if installed) is not found.
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":"+unameDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "wisp-deck-tui")
	calls, _ := os.ReadFile(curlCalls)
	assertContains(t, string(calls), "wisp-deck-tui-darwin-arm64")
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
	assertOpencodePluginInstalled(t, configHome)
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
	assertOpencodePluginInstalled(t, configHome)
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
	assertOpencodePluginInstalled(t, configHome)
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
	assertOpencodePluginInstalled(t, configHome)
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
	assertOpencodePluginInstalled(t, configHome)
}

func TestEnsureOpencode_fails_when_plugin_sync_fails(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "opencode", `exit 0`)
	mockCommand(t, dir, "brew", `exit 1`)
	snippet := installSnippet(t, `install_opencode_plugin() { return 73; }; ensure_opencode`)
	env := buildEnv(t, []string{binDir}, "HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))

	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Fatal("ensure_opencode reported success after plugin sync failed")
	}
	assertContains(t, out, "OpenCode plugin")
}

func TestWispDeckSetup_installs_opencode_plugin_regardless_of_selected_tool(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "wisp-deck"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	start := strings.Index(content, "# OpenCode plugin")
	end := strings.Index(content, "# ---------- Summary ----------")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("cannot locate OpenCode setup block")
	}
	block := content[start:end]
	if !strings.Contains(block, "install_opencode_plugin") {
		t.Fatal("setup must invoke the shared OpenCode plugin installer")
	}
	if strings.Contains(block, `SELECTED_AI_TOOL" = "opencode`) ||
		strings.Contains(block, `SELECTED_AI_TOOL" = 'opencode`) {
		t.Fatalf("OpenCode plugin setup is still conditional on the selected tool:\n%s", block)
	}
}
