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

	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `cat > "$dest" <<'PAYLOAD'
#!/bin/bash
[ "$1" = "--version" ] && echo "wisp-deck-tui version 2.5.0"
PAYLOAD
exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)
	snippet := installSnippet(t, fmt.Sprintf(`ensure_wisp_deck_tui %q`, shareDir))
	env := buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/usr/bin:/bin")
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
