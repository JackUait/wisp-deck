package npx_test

// Launcher reliability: the TUI binary download must be atomic and verified —
// a failed, truncated, or wrong-version download must never leave a broken
// binary at ~/.local/bin/wisp-deck-tui — and upgrades must not leave stale
// files from previous versions in the install dir.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launcherSandbox sets up an isolated HOME plus a mock-bin dir wired into PATH
// (ahead of the system, behind nothing) so a scripted `curl` intercepts the
// TUI download while node and coreutils still resolve.
type launcherSandbox struct {
	home       string
	installDir string
	mockBin    string
	env        []string
}

func newLauncherSandbox(t *testing.T) *launcherSandbox {
	t.Helper()
	home := t.TempDir()
	mockBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(mockBin, 0o755); err != nil {
		t.Fatal(err)
	}
	installDir := filepath.Join(home, ".local", "share", "wisp-deck")
	return &launcherSandbox{
		home:       home,
		installDir: installDir,
		mockBin:    mockBin,
		env: []string{
			"HOME=" + home,
			"WISP_DECK_INSTALL_DIR=" + installDir,
			"WISP_DECK_SKIP_EXEC=1",
			"PATH=" + mockBin + ":" + os.Getenv("PATH"),
		},
	}
}

func (s *launcherSandbox) tuiPath() string {
	return filepath.Join(s.home, ".local", "bin", "wisp-deck-tui")
}

// mockCurl installs a curl stand-in that parses -o and runs writeCmd with
// $dest set to the download destination.
func (s *launcherSandbox) mockCurl(t *testing.T, writeCmd string) {
	t.Helper()
	writeMock(t, s.mockBin, "curl", `
dest=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && dest="$a"
  prev="$a"
done
`+writeCmd)
}

func repoVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(projectRoot(t), "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestLauncher_rejects_tui_download_with_wrong_version(t *testing.T) {
	sb := newLauncherSandbox(t)
	// Download "succeeds" but delivers a binary of another version.
	sb.mockCurl(t, `cat > "$dest" <<'PAYLOAD'
#!/bin/bash
[ "$1" = "--version" ] && echo "wisp-deck-tui version 0.0.0"
PAYLOAD
exit 0`)

	_, stderr, code := runLauncher(t, sb.env)
	if code == 0 {
		t.Error("expected non-zero exit when the downloaded TUI reports the wrong version")
	}
	if !strings.Contains(stderr, "verif") {
		t.Errorf("expected a verification error on stderr, got: %s", stderr)
	}
	if _, err := os.Stat(sb.tuiPath()); err == nil {
		t.Error("wrong-version TUI download was installed anyway")
	}
	assertNoPartialDownloads(t, filepath.Dir(sb.tuiPath()))
}

func TestLauncher_rejects_corrupt_tui_download(t *testing.T) {
	sb := newLauncherSandbox(t)
	// Truncated download: not runnable at all.
	sb.mockCurl(t, `echo "garbage-not-a-binary" > "$dest"; exit 0`)

	_, stderr, code := runLauncher(t, sb.env)
	if code == 0 {
		t.Error("expected non-zero exit when the downloaded TUI cannot execute")
	}
	if !strings.Contains(stderr, "verif") {
		t.Errorf("expected a verification error on stderr, got: %s", stderr)
	}
	if _, err := os.Stat(sb.tuiPath()); err == nil {
		t.Error("corrupt TUI download was installed anyway")
	}
	assertNoPartialDownloads(t, filepath.Dir(sb.tuiPath()))
}

func TestLauncher_installs_tui_after_verifying_version(t *testing.T) {
	sb := newLauncherSandbox(t)
	version := repoVersion(t)
	sb.mockCurl(t, `cat > "$dest" <<PAYLOAD
#!/bin/bash
[ "\$1" = "--version" ] && echo "wisp-deck-tui version `+version+`"
PAYLOAD
exit 0`)

	stdout, stderr, code := runLauncher(t, sb.env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "installed") {
		t.Errorf("expected install confirmation, got: %s", stdout)
	}
	info, err := os.Stat(sb.tuiPath())
	if err != nil {
		t.Fatalf("verified TUI download was not installed: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("installed TUI binary is not executable")
	}
	assertNoPartialDownloads(t, filepath.Dir(sb.tuiPath()))
}

func TestLauncher_preserves_existing_tui_when_download_fails(t *testing.T) {
	sb := newLauncherSandbox(t)
	// An older-but-working binary is already installed.
	if err := os.MkdirAll(filepath.Dir(sb.tuiPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "#!/bin/bash\n[ \"$1\" = \"--version\" ] && echo \"wisp-deck-tui version 0.0.1\"\n"
	if err := os.WriteFile(sb.tuiPath(), []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	sb.mockCurl(t, `exit 1`)

	_, _, code := runLauncher(t, sb.env)
	if code == 0 {
		t.Error("expected non-zero exit when the TUI download fails")
	}
	data, err := os.ReadFile(sb.tuiPath())
	if err != nil {
		t.Fatalf("failed download removed the existing working binary: %v", err)
	}
	if string(data) != existing {
		t.Errorf("failed download clobbered the existing working binary, content now %q", data)
	}
	assertNoPartialDownloads(t, filepath.Dir(sb.tuiPath()))
}

func TestLauncher_removes_stale_files_from_previous_install(t *testing.T) {
	sb := newLauncherSandbox(t)
	// Simulate an old install with a lib file the new version no longer ships.
	stale := filepath.Join(sb.installDir, "lib", "removed-in-new-version.sh")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("echo stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sb.installDir, ".version"), []byte("0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := append(sb.env, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	_, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr: %s", code, stderr)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale lib file from the previous version survived the upgrade")
	}
	if _, err := os.Stat(filepath.Join(sb.installDir, "lib", "tui.sh")); err != nil {
		t.Errorf("upgrade did not install the new lib files: %v", err)
	}
}

func assertNoPartialDownloads(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".download") {
			t.Errorf("leftover partial download: %s", filepath.Join(dir, e.Name()))
		}
	}
}
