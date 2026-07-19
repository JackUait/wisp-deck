package npx_test

// Launcher reliability: the TUI binary download must be atomic and verified —
// a failed, truncated, or wrong-version download must never leave a broken
// binary at ~/.local/bin/wisp-deck-tui — and upgrades must not leave stale
// files from previous versions in the install dir.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

const validLauncherTuiCapabilities = `{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":1,"host_effects_allowed":false}`

func launcherTuiArtifact(version, capabilities string, capabilityExit int) string {
	return fmt.Sprintf(`#!/bin/bash
case "$1" in
  --version)
    printf '%%s\n' 'wisp-deck-tui version %s'
    ;;
  capabilities)
    [ "$2" = "--require-production" ] || exit 64
    cat <<'CAPABILITIES'
%s
CAPABILITIES
    exit %d
    ;;
  *)
    exit 64
    ;;
esac
`, version, capabilities, capabilityExit)
}

func (s *launcherSandbox) mockTuiDownload(t *testing.T, version, capabilities string, capabilityExit int) {
	t.Helper()
	artifact := filepath.Join(t.TempDir(), "wisp-deck-tui")
	if err := os.WriteFile(artifact, []byte(launcherTuiArtifact(version, capabilities, capabilityExit)), 0o755); err != nil {
		t.Fatal(err)
	}
	s.mockCurl(t, fmt.Sprintf(`cp %q "$dest"; exit 0`, artifact))
}

func writeLauncherTuiArtifact(t *testing.T, path, version, capabilities string, capabilityExit int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(launcherTuiArtifact(version, capabilities, capabilityExit)), 0o755); err != nil {
		t.Fatal(err)
	}
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
	sb.mockTuiDownload(t, version, validLauncherTuiCapabilities, 0)

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

func TestLauncher_downloaded_tui_capability_matrix(t *testing.T) {
	version := repoVersion(t)
	tests := []struct {
		name           string
		reported       string
		capabilities   string
		capabilityExit int
		wantValid      bool
	}{
		{
			name:         "valid with runtime host effects denied",
			reported:     version,
			capabilities: validLauncherTuiCapabilities,
			wantValid:    true,
		},
		{
			name:         "superstring version",
			reported:     "1" + version,
			capabilities: validLauncherTuiCapabilities,
		},
		{
			name:           "production capability exits nonzero",
			reported:       version,
			capabilities:   validLauncherTuiCapabilities,
			capabilityExit: 1,
		},
		{name: "empty JSON", reported: version},
		{name: "malformed JSON", reported: version, capabilities: `{broken`},
		{
			name:         "multiple JSON objects",
			reported:     version,
			capabilities: validLauncherTuiCapabilities + "\n" + validLauncherTuiCapabilities,
		},
		{
			name:         "host effects compiled false",
			reported:     version,
			capabilities: `{"host_effects_compiled":false,"sound_preview_compiled":true,"host_effects_boundary":1}`,
		},
		{
			name:         "sound preview compiled false",
			reported:     version,
			capabilities: `{"host_effects_compiled":true,"sound_preview_compiled":false,"host_effects_boundary":1}`,
		},
		{
			name:         "fractional boundary",
			reported:     version,
			capabilities: `{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":1.5}`,
		},
		{
			name:         "wrong boundary",
			reported:     version,
			capabilities: `{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":2}`,
		},
		{
			name:         "wrong field types",
			reported:     version,
			capabilities: `{"host_effects_compiled":"true","sound_preview_compiled":1,"host_effects_boundary":"1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newLauncherSandbox(t)
			sb.mockTuiDownload(t, tt.reported, tt.capabilities, tt.capabilityExit)

			_, stderr, code := runLauncher(t, sb.env)
			if tt.wantValid && code != 0 {
				t.Fatalf("valid artifact was rejected with exit %d: %s", code, stderr)
			}
			if !tt.wantValid && code == 0 {
				t.Fatalf("invalid artifact was accepted: version=%q capabilities=%q", tt.reported, tt.capabilities)
			}
			if !tt.wantValid {
				if _, err := os.Stat(sb.tuiPath()); err == nil {
					t.Error("invalid downloaded TUI was installed")
				}
				assertNoPartialDownloads(t, filepath.Dir(sb.tuiPath()))
			}
		})
	}
}

func TestLauncher_keeps_up_to_date_tui_with_production_capability_without_curl(t *testing.T) {
	sb := newLauncherSandbox(t)
	version := repoVersion(t)
	writeLauncherTuiArtifact(t, sb.tuiPath(), version, validLauncherTuiCapabilities, 0)
	curlCalls := filepath.Join(t.TempDir(), "curl-calls")
	sb.mockCurl(t, fmt.Sprintf(`printf 'called\n' >> %q; exit 1`, curlCalls))

	stdout, stderr, code := runLauncher(t, sb.env)
	if code != 0 {
		t.Fatalf("valid existing artifact was rejected with exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "already up to date") {
		t.Fatalf("expected up-to-date result, got %q", stdout)
	}
	if _, err := os.Stat(curlCalls); err == nil {
		t.Error("valid existing artifact should not call curl")
	}
}

func TestLauncher_replaces_up_to_date_version_with_failing_capability(t *testing.T) {
	sb := newLauncherSandbox(t)
	version := repoVersion(t)
	writeLauncherTuiArtifact(t, sb.tuiPath(), version, validLauncherTuiCapabilities, 1)
	sb.mockTuiDownload(t, version, validLauncherTuiCapabilities, 0)

	stdout, stderr, code := runLauncher(t, sb.env)
	if code != 0 {
		t.Fatalf("replacement failed with exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "installed") {
		t.Fatalf("expected invalid existing artifact to be replaced, got %q", stdout)
	}
}

func TestLauncher_rejects_capability_invalid_download_atomically(t *testing.T) {
	sb := newLauncherSandbox(t)
	if err := os.MkdirAll(filepath.Dir(sb.tuiPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "#!/bin/bash\nprintf 'wisp-deck-tui version 0.0.1\\n'\n"
	if err := os.WriteFile(sb.tuiPath(), []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	sb.mockTuiDownload(t, repoVersion(t), `{malformed`, 0)

	_, _, code := runLauncher(t, sb.env)
	if code == 0 {
		t.Fatal("capability-invalid download was accepted")
	}
	data, err := os.ReadFile(sb.tuiPath())
	if err != nil {
		t.Fatalf("invalid download removed existing TUI: %v", err)
	}
	if string(data) != existing {
		t.Fatalf("invalid download replaced existing TUI: %q", data)
	}
	assertNoPartialDownloads(t, filepath.Dir(sb.tuiPath()))
}

func TestLauncher_uses_supported_tui_asset_architecture(t *testing.T) {
	tests := []struct {
		nodeArch  string
		assetArch string
	}{
		{nodeArch: "x64", assetArch: "amd64"},
		{nodeArch: "arm64", assetArch: "arm64"},
	}
	for _, tt := range tests {
		t.Run(tt.nodeArch, func(t *testing.T) {
			sb := newLauncherSandbox(t)
			version := repoVersion(t)
			curlCalls := filepath.Join(t.TempDir(), "curl-calls")
			artifact := filepath.Join(t.TempDir(), "wisp-deck-tui")
			writeLauncherTuiArtifact(t, artifact, version, validLauncherTuiCapabilities, 0)
			sb.mockCurl(t, fmt.Sprintf(`printf '%%s\n' "$*" >> %q; cp %q "$dest"; exit 0`, curlCalls, artifact))
			env := append(sb.env, "WISP_DECK_MOCK_ARCH="+tt.nodeArch)

			_, stderr, code := runLauncher(t, env)
			if code != 0 {
				t.Fatalf("launcher failed for %s: %s", tt.nodeArch, stderr)
			}
			calls, err := os.ReadFile(curlCalls)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(calls), "wisp-deck-tui-darwin-"+tt.assetArch) {
				t.Fatalf("curl args %q do not contain mapped architecture %q", calls, tt.assetArch)
			}
		})
	}
}

func TestLauncher_rejects_unsupported_tui_asset_architecture(t *testing.T) {
	sb := newLauncherSandbox(t)
	curlCalls := filepath.Join(t.TempDir(), "curl-calls")
	sb.mockCurl(t, fmt.Sprintf(`printf 'called\n' >> %q; exit 1`, curlCalls))
	env := append(sb.env, "WISP_DECK_MOCK_ARCH=ppc64")

	_, stderr, code := runLauncher(t, env)
	if code == 0 {
		t.Fatal("unsupported architecture was accepted")
	}
	if !strings.Contains(stderr, "Unsupported architecture") {
		t.Fatalf("missing unsupported architecture error: %s", stderr)
	}
	if _, err := os.Stat(curlCalls); err == nil {
		t.Error("unsupported architecture reached curl")
	}
}

func TestLauncher_skip_tui_download_requires_exact_repository_test_mode(t *testing.T) {
	t.Run("exact test marker skips", func(t *testing.T) {
		sb := newLauncherSandbox(t)
		curlCalls := filepath.Join(t.TempDir(), "curl-calls")
		sb.mockCurl(t, fmt.Sprintf(`printf 'called\n' >> %q; exit 1`, curlCalls))
		env := append(sb.env, "WISP_DECK_SKIP_TUI_DOWNLOAD=1")

		_, stderr, code := runLauncher(t, env)
		if code != 0 {
			t.Fatalf("exact repository test skip failed: %s", stderr)
		}
		if _, err := os.Stat(curlCalls); err == nil {
			t.Error("exact repository test skip called curl")
		}
	})

	t.Run("pure policy requires both exact values", func(t *testing.T) {
		for _, marker := range []string{"", "0", "stale", "1"} {
			for _, skip := range []string{"", "0", "stale", "1"} {
				want := marker == "1" && skip == "1"
				if got := launcherSkipTuiDownload(t, marker, skip); got != want {
					t.Fatalf(
						"marker=%q skip=%q result=%t, want %t",
						marker,
						skip,
						got,
						want,
					)
				}
			}
		}
	})

	source, err := os.ReadFile(filepath.Join(
		projectRoot(t),
		"bin",
		"npx-wisp-deck.js",
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{
		"const skipTuiDownload = shouldSkipTuiDownload(process.env);",
		"if (!skipTuiDownload) {\n    ensureTuiBinary(version);\n  }",
		"if (require.main === module) {\n  main();\n}",
	} {
		if got := strings.Count(string(source), exact); got != 1 {
			t.Fatalf(
				"launcher main contains %d exact %q call-site shapes, want 1",
				got,
				exact,
			)
		}
	}
}

func launcherSkipTuiDownload(t *testing.T, marker, skip string) bool {
	t.Helper()
	launcher := filepath.Join(projectRoot(t), "bin", "npx-wisp-deck.js")
	script := `
const launcher = require(process.argv[1]);
const environment = JSON.parse(process.argv[2]);
process.stdout.write(launcher.shouldSkipTuiDownload(environment) ? "true" : "false");
`
	environment := map[string]string{
		"WISP_DECK_SKIP_TUI_DOWNLOAD": skip,
	}
	if marker != "" {
		environment["WISP_DECK_TESTING"] = marker
	}
	encoded, err := json.Marshal(environment)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "-e", script, launcher, string(encoded))
	cmd.Env = repositoryTestEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evaluate launcher download-skip policy: %v\n%s", err, output)
	}
	switch string(output) {
	case "true":
		return true
	case "false":
		return false
	default:
		t.Fatalf("unexpected launcher download-skip result %q", output)
		return false
	}
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
