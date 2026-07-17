package npx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// extractTarball unpacks the single .tgz in dir and returns the package root.
func extractTarball(t *testing.T, dir string) string {
	t.Helper()
	tarballs, err := filepath.Glob(filepath.Join(dir, "*.tgz"))
	if err != nil || len(tarballs) != 1 {
		t.Fatalf("expected exactly one tarball in %s, got %v (err %v)", dir, tarballs, err)
	}
	if out, err := exec.Command("tar", "xzf", tarballs[0], "-C", dir).CombinedOutput(); err != nil {
		t.Fatalf("tar extract failed: %v\n%s", err, out)
	}
	return filepath.Join(dir, "package")
}

// packLocal builds the npm tarball from the working tree and unpacks it, so the
// install below sees exactly the file set npm would publish — not whatever the
// repo happens to have lying around.
func packLocal(t *testing.T) string {
	t.Helper()
	root := projectRoot(t)
	dest := t.TempDir()
	cmd := exec.Command("npm", "pack", root, "--pack-destination", dest)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm pack failed: %v\n%s", err, out)
	}
	return extractTarball(t, dest)
}

// packPublished downloads the tarball real users get from the npm registry.
func packPublished(t *testing.T, spec string) string {
	t.Helper()
	dest := t.TempDir()
	cmd := exec.Command("npm", "pack", spec, "--pack-destination", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm pack %s failed: %v\n%s", spec, err, out)
	}
	return extractTarball(t, dest)
}

func mustLookPath(t *testing.T, cmd string) string {
	t.Helper()
	p, err := exec.LookPath(cmd)
	if err != nil {
		t.Skipf("%s not on PATH; cannot run install e2e", cmd)
	}
	return filepath.Dir(p)
}

func writeMock(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// installSandbox is an empty HOME plus stand-ins for everything the installer
// reaches for outside itself, so a run is hermetic and offline.
type installSandbox struct {
	home    string
	mockBin string
	env     []string
}

func newInstallSandbox(t *testing.T) *installSandbox {
	t.Helper()
	home := t.TempDir()
	fake := t.TempDir()

	// Pretend Ghostty and a Nerd Font are already present, so the installer takes
	// its "already installed" branches instead of reaching for Homebrew.
	appsDir := filepath.Join(fake, "Applications")
	ghosttyApp := filepath.Join(appsDir, "Ghostty.app")
	fontsDir := filepath.Join(fake, "Fonts")
	for _, d := range []string{ghosttyApp, fontsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fontsDir, "HackNerdFont-Regular.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mockBin := filepath.Join(fake, "bin")
	if err := os.MkdirAll(mockBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMock(t, mockBin, "claude", `echo "claude mock"`)
	writeMock(t, mockBin, "tmux", `echo "tmux mock"`)
	// Reaching Homebrew or the npm registry means the offline path is broken.
	writeMock(t, mockBin, "brew", `echo "brew called with: $*" >&2; exit 1`)
	writeMock(t, mockBin, "npm", `
case "$1" in
  list) echo "/usr/local/lib └── ccstatusline@99.0.0" ;;
  *) exit 0 ;;
esac`)

	path := strings.Join([]string{
		mockBin,
		mustLookPath(t, "node"),
		mustLookPath(t, "jq"),
		"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}, ":")

	return &installSandbox{
		home:    home,
		mockBin: mockBin,
		env: []string{
			"HOME=" + home,
			"PATH=" + path,
			"TERM=xterm-256color",
			"APPLICATIONS_DIR=" + appsDir,
			"GHOSTTY_APP_PATH=" + ghosttyApp,
			"FONTS_DIR=" + fontsDir,
		},
	}
}

// mockTUI installs a stand-in for wisp-deck-tui at the given path, answering the
// version check and the AI-tool picker (which is otherwise interactive).
func (s *installSandbox) mockTUI(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMock(t, dir, "wisp-deck-tui", `
case "$1" in
  --version) echo "wisp-deck-tui version `+version+`" ;;
  multi-select-ai-tool) echo '{"confirmed":true,"tools":["claude"]}' ;;
  *) exit 0 ;;
esac`)
}

func (s *installSandbox) run(t *testing.T, pkg string, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command("node", filepath.Join(pkg, "bin", "npx-wisp-deck.js"))
	cmd.Env = append(append([]string{}, s.env...), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, out)
	}
	return string(out)
}

func pkgVersion(t *testing.T, pkg string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(pkg, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// assertInstalled checks the install produced a workspace a user can actually
// use. os.Stat follows symlinks, so a link to a file npm never published fails
// here — which is exactly how a broken install used to report success.
func assertInstalled(t *testing.T, home, out string) {
	t.Helper()
	if !strings.Contains(out, "Setup complete") {
		t.Errorf("installer did not report completion:\n%s", out)
	}

	for _, rel := range []string{
		".local/bin/wisp-deck",                  // the config CLI the installer tells users to run
		".config/wisp-deck/wrapper.sh",          // what Ghostty launches
		".config/wisp-deck/lib",                 // libs the wrapper sources
		".config/wisp-deck/ai-tool",             // the selected tool
		".config/wisp-deck/claude-configs.list", // seeded from defaults/
		".config/wisp-deck/claude-configs/openai-gpt.json",
		".config/ghostty/config",
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("post-install ~/%s is missing or unresolvable: %v", rel, err)
		}
	}

	configList, err := os.ReadFile(filepath.Join(home, ".config", "wisp-deck", "claude-configs.list"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configList), "OpenAI GPT:openai-gpt.json") {
		t.Errorf("installed subscription list is missing OpenAI GPT:\n%s", configList)
	}

	ghosttyCfg, err := os.ReadFile(filepath.Join(home, ".config", "ghostty", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ghosttyCfg), filepath.Join(home, ".config", "wisp-deck", "wrapper.sh")) {
		t.Errorf("ghostty config does not launch the wrapper:\n%s", ghosttyCfg)
	}

	// The general guard: an install must never leave a symlink pointing at
	// something that was not shipped.
	assertNoDanglingSymlinks(t, home)
}

func assertNoDanglingSymlinks(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if _, statErr := os.Stat(path); statErr != nil {
			target, _ := os.Readlink(path)
			rel, _ := filepath.Rel(root, path)
			t.Errorf("dangling symlink after install: ~/%s -> %s", rel, target)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestInstall_e2e_from_packed_tarball runs the complete install the way an
// `npx wisp-deck` user does — from the tarball npm would publish into an empty
// HOME — and asserts the result is usable. This is what catches an installer
// that references a file the package does not ship.
func TestInstall_e2e_from_packed_tarball(t *testing.T) {
	pkg := packLocal(t)
	sb := newInstallSandbox(t)
	sb.mockTUI(t, sb.mockBin, pkgVersion(t, pkg))

	out := sb.run(t, pkg, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	assertInstalled(t, sb.home, out)
}

// TestInstall_e2e_from_npm_registry installs the version real users get from npm,
// including the real wisp-deck-tui download from GitHub Releases. It proves the
// published artifacts are actually installable — the failure mode that shipped in
// v2.22.0 and that no source-tree test could see.
//
// Network-dependent, so it is opt-in: set WISP_DECK_VERIFY_PUBLISHED=1 (CI runs it
// after a release and on a schedule). Override the target with
// WISP_DECK_VERIFY_SPEC (default: wisp-deck@latest).
func TestInstall_e2e_from_npm_registry(t *testing.T) {
	if os.Getenv("WISP_DECK_VERIFY_PUBLISHED") == "" {
		t.Skip("set WISP_DECK_VERIFY_PUBLISHED=1 to verify the published package (requires network)")
	}
	spec := os.Getenv("WISP_DECK_VERIFY_SPEC")
	if spec == "" {
		spec = "wisp-deck@latest"
	}

	pkg := packPublished(t, spec)
	version := pkgVersion(t, pkg)
	sb := newInstallSandbox(t)

	// Phase 1: let the launcher do the real thing — copy the published files and
	// download the released TUI binary. Stop before the bash installer, which
	// would block on the interactive AI-tool picker.
	sb.run(t, pkg, "WISP_DECK_SKIP_EXEC=1")

	tui := filepath.Join(sb.home, ".local", "bin", "wisp-deck-tui")
	verOut, err := exec.Command(tui, "--version").Output()
	if err != nil {
		t.Fatalf("released wisp-deck-tui did not run: %v", err)
	}
	if !strings.Contains(string(verOut), version) {
		t.Errorf("released wisp-deck-tui reports %q, want version %s", strings.TrimSpace(string(verOut)), version)
	}

	// Phase 2: swap in a scriptable TUI (same path, same version) so the picker
	// answers itself, then run the installer through to completion.
	sb.mockTUI(t, filepath.Dir(tui), version)
	out := sb.run(t, pkg, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")
	assertInstalled(t, sb.home, out)
}
