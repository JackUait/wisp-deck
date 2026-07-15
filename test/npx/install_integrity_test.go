package npx_test

// Install integrity: an install must survive being re-run, being resumed after
// an interrupted copy, and living in a HOME whose path has a space. Each of
// these is a state a real user reaches without doing anything unusual.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLauncher_repairs_install_when_files_missing_despite_version_marker
// covers the interrupted/partially-deleted install. The launcher decides
// whether to copy the distribution purely from the .version marker, so an
// install dir that has the right marker but is missing files is declared
// "already up to date" — and then the bash installer it execs is not there.
// Re-running `npx wisp-deck`, the one thing a stuck user will try, must fix it.
func TestLauncher_repairs_install_when_files_missing_despite_version_marker(t *testing.T) {
	sb := newLauncherSandbox(t)
	version := repoVersion(t)

	// A prior install that got truncated: correct marker, but the tree is gone.
	if err := os.MkdirAll(sb.installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sb.installDir, ".version"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := append(sb.env, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	_, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr: %s", code, stderr)
	}

	for _, rel := range []string{"bin/wisp-deck", "lib/tui.sh", "wrapper.sh", "VERSION"} {
		if _, err := os.Stat(filepath.Join(sb.installDir, rel)); err != nil {
			t.Errorf("re-running the launcher did not restore %s: %v", rel, err)
		}
	}
}

// A complete, healthy install must still short-circuit — the repair check must
// not turn every launch into a full re-copy.
func TestLauncher_skips_copy_when_install_is_complete(t *testing.T) {
	sb := newLauncherSandbox(t)
	env := append(sb.env, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")

	if _, stderr, code := runLauncher(t, env); code != 0 {
		t.Fatalf("first run failed: %d %s", code, stderr)
	}
	stdout, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("second run failed: %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("a complete install should short-circuit, got: %s", stdout)
	}
}

func TestLauncher_repairs_missing_wrapper_despite_version_marker(t *testing.T) {
	sb := newLauncherSandbox(t)
	env := append(sb.env, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")

	if _, stderr, code := runLauncher(t, env); code != 0 {
		t.Fatalf("first run failed: %d %s", code, stderr)
	}
	wrapper := filepath.Join(sb.installDir, "wrapper.sh")
	if err := os.Remove(wrapper); err != nil {
		t.Fatalf("remove installed wrapper: %v", err)
	}

	stdout, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("repair run failed: %d %s", code, stderr)
	}
	if strings.Contains(stdout, "already up to date") {
		t.Fatalf("missing wrapper was treated as intact: %s", stdout)
	}
	if _, err := os.Stat(wrapper); err != nil {
		t.Fatalf("repair run did not restore wrapper: %v", err)
	}
}

// homeWithSpace returns a temp HOME whose path contains a space — the classic
// shell-quoting minefield, and a real macOS setup ("/Users/Jane Smith").
func homeWithSpace(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "Jane Smith")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// A full install into a HOME with a space in it must work end to end: an
// unquoted path anywhere in the installer would corrupt the symlinks or the
// Ghostty command line and only fail for the users unlucky enough to have one.
func TestInstall_e2e_into_home_with_space(t *testing.T) {
	pkg := packLocal(t)
	sb := newInstallSandbox(t)
	sb.home = homeWithSpace(t)
	sb.env = append(sb.env, "HOME="+sb.home) // later HOME wins
	sb.mockTUI(t, sb.mockBin, pkgVersion(t, pkg))

	out := sb.run(t, pkg, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	assertInstalled(t, sb.home, out)

	// The Ghostty command line must name the wrapper inside the spaced HOME.
	cfg, err := os.ReadFile(filepath.Join(sb.home, ".config", "ghostty", "config"))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(sb.home, ".config", "wisp-deck", "wrapper.sh")
	if !strings.Contains(string(cfg), wrapper) {
		t.Errorf("ghostty config lost the spaced wrapper path:\n%s", cfg)
	}
}

// Re-running the installer is the standard upgrade path and the standard
// "did it work?" reflex. It must be idempotent: still a working install, and
// no duplicated command lines accumulating in the Ghostty config.
func TestInstall_e2e_is_idempotent(t *testing.T) {
	pkg := packLocal(t)
	sb := newInstallSandbox(t)
	sb.mockTUI(t, sb.mockBin, pkgVersion(t, pkg))

	first := sb.run(t, pkg, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	assertInstalled(t, sb.home, first)

	second := sb.run(t, pkg, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	assertInstalled(t, sb.home, second)

	cfgPath := filepath.Join(sb.home, ".config", "ghostty", "config")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := countPrefixLines(string(cfg), "command"); n != 1 {
		t.Errorf("ghostty config has %d command lines after two installs, want exactly 1:\n%s", n, cfg)
	}
	if n := countPrefixLines(string(cfg), "window-save-state"); n != 1 {
		t.Errorf("ghostty config has %d window-save-state lines after two installs, want exactly 1:\n%s", n, cfg)
	}
}

// countPrefixLines counts lines whose first token is the given key.
func countPrefixLines(cfg, key string) int {
	n := 0
	for _, line := range strings.Split(cfg, "\n") {
		f := strings.TrimSpace(line)
		if strings.HasPrefix(f, key) {
			rest := strings.TrimSpace(strings.TrimPrefix(f, key))
			if strings.HasPrefix(rest, "=") {
				n++
			}
		}
	}
	return n
}
