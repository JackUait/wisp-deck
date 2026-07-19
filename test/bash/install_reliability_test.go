package bash_test

// Install reliability: downloads must be atomic (a failed or corrupt download
// never clobbers a working binary), verified (a truncated or wrong-version
// artifact is rejected before it replaces anything), and resilient to
// transient network failures (curl retries).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockCurlWriting returns a mock-curl body that parses the -o flag properly
// (instead of assuming a positional arg) and writes the given shell command's
// output to the download destination. extra runs before the write (for logs).
func mockCurlWriting(extra, writeCmd string) string {
	return extra + `
dest=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && dest="$a"
  prev="$a"
done
[ -n "$dest" ] || exit 1
` + writeCmd + `
`
}

const validTuiCapabilities = `{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":1,"host_effects_allowed":false}`

func writeTuiArtifact(t *testing.T, path, version, capabilities string, capabilityExit int) {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/bash
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
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWispDeckTuiArtifact_requires_exact_typed_production_capabilities(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		capabilities   string
		capabilityExit int
		wantValid      bool
	}{
		{
			name:         "valid with runtime host effects denied",
			version:      "2.23.1",
			capabilities: validTuiCapabilities,
			wantValid:    true,
		},
		{
			name:         "superstring version",
			version:      "12.23.1",
			capabilities: validTuiCapabilities,
		},
		{
			name:           "production capability exits nonzero",
			version:        "2.23.1",
			capabilities:   validTuiCapabilities,
			capabilityExit: 1,
		},
		{name: "empty JSON", version: "2.23.1"},
		{name: "malformed JSON", version: "2.23.1", capabilities: `{broken`},
		{
			name:         "multiple JSON values",
			version:      "2.23.1",
			capabilities: validTuiCapabilities + "\n" + validTuiCapabilities,
		},
		{
			name:         "host effects compiled false",
			version:      "2.23.1",
			capabilities: `{"host_effects_compiled":false,"sound_preview_compiled":true,"host_effects_boundary":1}`,
		},
		{
			name:         "sound preview compiled false",
			version:      "2.23.1",
			capabilities: `{"host_effects_compiled":true,"sound_preview_compiled":false,"host_effects_boundary":1}`,
		},
		{
			name:         "fractional boundary",
			version:      "2.23.1",
			capabilities: `{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":1.5}`,
		},
		{
			name:         "wrong boundary",
			version:      "2.23.1",
			capabilities: `{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":2}`,
		},
		{
			name:         "wrong field types",
			version:      "2.23.1",
			capabilities: `{"host_effects_compiled":"true","sound_preview_compiled":1,"host_effects_boundary":"1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := filepath.Join(t.TempDir(), "wisp-deck-tui")
			writeTuiArtifact(t, artifact, tt.version, tt.capabilities, tt.capabilityExit)

			snippet := installSnippet(t,
				fmt.Sprintf(`verify_wisp_deck_tui_artifact %q %q`, "2.23.1", artifact))
			out, code := runBashSnippet(t, snippet, nil)
			if tt.wantValid && code != 0 {
				t.Fatalf("valid artifact was rejected with exit %d: %s", code, out)
			}
			if !tt.wantValid && code == 0 {
				t.Fatalf("invalid artifact was accepted: version=%q capabilities=%q", tt.version, tt.capabilities)
			}
		})
	}
}

func TestInstallBinary_preserves_existing_binary_when_download_fails(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(destDir, "mytool")
	if err := os.WriteFile(dest, []byte("old-working-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// curl writes a partial file to -o, then fails (interrupted transfer).
	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `echo "partial" > "$dest"; exit 1`))
	snippet := installSnippet(t, fmt.Sprintf(`install_binary "https://example.com/mytool" %q "mytool"`, dest))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)

	if code == 0 {
		t.Error("expected non-zero exit when download fails")
	}
	assertContains(t, out, "Failed")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("existing binary was removed by a failed download: %v", err)
	}
	if string(data) != "old-working-binary" {
		t.Errorf("existing binary was clobbered by a failed download, content now %q", data)
	}
	assertNoLeftoverDownloads(t, destDir)
}

func TestInstallBinary_verifier_rejects_bad_download(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(destDir, "mytool")
	if err := os.WriteFile(dest, []byte("old-working-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// curl "succeeds" but delivers garbage; the verifier must reject it.
	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `echo "garbage" > "$dest"; exit 0`))
	mockCommand(t, dir, "verify-tool", `exit 1`)
	snippet := installSnippet(t,
		fmt.Sprintf(`install_binary "https://example.com/mytool" %q "mytool" verify-tool`, dest))
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)

	if code == 0 {
		t.Error("expected non-zero exit when downloaded binary fails verification")
	}
	assertContains(t, out, "verif")
	data, _ := os.ReadFile(dest)
	if string(data) != "old-working-binary" {
		t.Errorf("unverified download replaced the working binary, content now %q", data)
	}
	assertNoLeftoverDownloads(t, destDir)
}

func TestInstallBinary_retries_transient_failures(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "bin", "mytool")
	curlCalls := filepath.Join(dir, "curl_calls")
	binDir := mockCommand(t, dir, "curl",
		mockCurlWriting(fmt.Sprintf(`echo "$@" >> %q`, curlCalls), `echo "binary" > "$dest"; exit 0`))
	snippet := installSnippet(t, fmt.Sprintf(`install_binary "https://example.com/mytool" %q "mytool"`, dest))
	env := buildEnv(t, []string{binDir})
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	calls, _ := os.ReadFile(curlCalls)
	if !strings.Contains(string(calls), "--retry") {
		t.Errorf("curl download should retry transient failures (--retry), got args: %s", calls)
	}
	if !strings.Contains(string(calls), "--connect-timeout") {
		t.Errorf("curl download should bound connection hangs (--connect-timeout), got args: %s", calls)
	}
}

func TestEnsureWispDeckTui_rejects_download_reporting_wrong_version(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.5.0")

	// The download "succeeds" but the delivered binary reports another version
	// (stale CDN, wrong asset). It must not be installed as if it were 2.5.0.
	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `cat > "$dest" <<'PAYLOAD'
#!/bin/bash
[ "$1" = "--version" ] && echo "wisp-deck-tui version 0.0.0"
PAYLOAD
exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/usr/bin:/bin")
	out, code := runBashSnippet(t, snippet, env)

	if code == 0 {
		t.Error("expected non-zero exit when downloaded binary reports the wrong version")
	}
	assertContains(t, out, "verif")
	if _, err := os.Stat(filepath.Join(fakeHome, ".local", "bin", "wisp-deck-tui")); err == nil {
		t.Error("wrong-version download was installed anyway")
	}
}

func TestEnsureWispDeckTui_installs_download_reporting_expected_version(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.5.0")

	download := filepath.Join(dir, "downloaded-wisp-deck-tui")
	writeTuiArtifact(t, download, "2.5.0", validTuiCapabilities, 0)
	binDir := mockCommand(t, dir, "curl",
		mockCurlWriting("", fmt.Sprintf(`cp %q "$dest"; exit 0`, download)))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{binDir}, "HOME="+fakeHome)
	out, code := runBashSnippet(t, snippet, env)

	assertExitCode(t, code, 0)
	assertContains(t, out, "installed")
	bin := filepath.Join(fakeHome, ".local", "bin", "wisp-deck-tui")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("verified download was not installed: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}
	assertNoLeftoverDownloads(t, filepath.Join(fakeHome, ".local", "bin"))
}

func TestEnsureWispDeckTui_replaces_exact_version_without_production_capability(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	tuiDir := filepath.Join(fakeHome, ".local", "bin")
	if err := os.MkdirAll(tuiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.23.1")

	existing := filepath.Join(tuiDir, "wisp-deck-tui")
	writeTuiArtifact(t, existing, "2.23.1", validTuiCapabilities, 1)
	download := filepath.Join(dir, "downloaded-wisp-deck-tui")
	writeTuiArtifact(t, download, "2.23.1", validTuiCapabilities, 0)
	curlCalls := filepath.Join(dir, "curl-calls")
	binDir := mockCommand(t, dir, "curl", mockCurlWriting(
		fmt.Sprintf(`printf 'called\n' >> %q`, curlCalls),
		fmt.Sprintf(`cp %q "$dest"; exit 0`, download)))
	mockCommand(t, dir, "uname", `echo "arm64"`)

	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{tuiDir, binDir}, "HOME="+fakeHome)
	out, code := runBashSnippet(t, snippet, env)
	if code != 0 {
		t.Fatalf("expected replacement to succeed, exit %d: %s", code, out)
	}
	if _, err := os.Stat(curlCalls); err != nil {
		t.Fatal("existing exact-version artifact without a production capability was incorrectly kept")
	}
	_, verified := runBashSnippet(t, installSnippet(t,
		fmt.Sprintf(`verify_wisp_deck_tui_artifact %q %q`, "2.23.1", existing)), env)
	assertExitCode(t, verified, 0)
}

func TestEnsureWispDeckTui_rejects_capability_invalid_download_atomically(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	tuiDir := filepath.Join(fakeHome, ".local", "bin")
	if err := os.MkdirAll(tuiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "2.23.1")

	existing := filepath.Join(tuiDir, "wisp-deck-tui")
	old := "#!/bin/bash\nprintf 'wisp-deck-tui version 2.22.0\\n'\n"
	if err := os.WriteFile(existing, []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	download := filepath.Join(dir, "downloaded-wisp-deck-tui")
	writeTuiArtifact(t, download, "2.23.1", `{malformed`, 0)
	binDir := mockCommand(t, dir, "curl",
		mockCurlWriting("", fmt.Sprintf(`cp %q "$dest"; exit 0`, download)))
	mockCommand(t, dir, "uname", `echo "arm64"`)

	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, []string{tuiDir, binDir}, "HOME="+fakeHome)
	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Fatal("capability-invalid download was accepted")
	}
	assertContains(t, out, "verif")
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("existing TUI was removed: %v", err)
	}
	if string(data) != old {
		t.Fatalf("invalid download replaced the existing TUI: %q", data)
	}
	assertNoLeftoverDownloads(t, tuiDir)
}

func TestEnsureWispDeckTui_skip_requires_exact_repository_test_mode(t *testing.T) {
	tests := []struct {
		name        string
		testing     string
		skip        string
		wantSkipped bool
	}{
		{name: "exact marker and skip", testing: "1", skip: "1", wantSkipped: true},
		{name: "nonexact skip", testing: "1", skip: "true"},
		{name: "outside repository test mode", testing: "0", skip: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := buildEnv(t, nil)
			filtered := make([]string, 0, len(env)+2)
			for _, entry := range env {
				if strings.HasPrefix(entry, "WISP_DECK_TESTING=") ||
					strings.HasPrefix(entry, "WISP_DECK_SKIP_TUI_DOWNLOAD=") {
					continue
				}
				filtered = append(filtered, entry)
			}
			filtered = append(filtered,
				"WISP_DECK_TESTING="+tt.testing,
				"WISP_DECK_SKIP_TUI_DOWNLOAD="+tt.skip,
			)
			missingShare := filepath.Join(t.TempDir(), "missing-share")
			out, code := runBashSnippet(t,
				installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, missingShare)),
				filtered)
			if tt.wantSkipped && code != 0 {
				t.Fatalf("exact repository test skip failed with exit %d: %s", code, out)
			}
			if !tt.wantSkipped && code == 0 {
				t.Fatalf("nonexact skip combination testing=%q skip=%q bypassed verification", tt.testing, tt.skip)
			}
		})
	}
}

func TestEnsureJq_rejects_download_that_cannot_execute(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A truncated download: not a runnable binary at all.
	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `echo "garbage-not-a-binary" > "$dest"; exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, `ensure_jq`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "mktemp", "tar", "unzip")
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)

	if code == 0 {
		t.Error("expected non-zero exit when the downloaded jq cannot execute")
	}
	assertContains(t, out, "verif")
	if _, err := os.Stat(filepath.Join(fakeHome, ".local", "bin", "jq")); err == nil {
		t.Error("broken jq download was installed anyway")
	}
}

func TestEnsureTmux_fails_on_corrupt_archive(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	// curl succeeds (both the HEAD tag lookup and the tarball) but the archive
	// is corrupt — the extraction failure must not be reported as success.
	binDir := mockCommand(t, dir, "curl", mockCurlWriting(
		`if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/tmux/tmux-builds/releases/tag/v3.5\r\n"; exit 0; fi`,
		`echo "not-a-tarball" > "$dest"; exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, `ensure_tmux`)
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "mktemp", "tar")
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/bin")
	out, code := runBashSnippet(t, snippet, env)

	if code == 0 {
		t.Error("expected non-zero exit when the tmux archive is corrupt")
	}
	assertNotContains(t, out, "tmux installed")
}

// assertNoLeftoverDownloads fails if any in-flight download temp files remain.
func assertNoLeftoverDownloads(t *testing.T, dir string) {
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
