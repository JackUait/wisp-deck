package bash_test

// The first exec of a freshly built/installed wisp-deck-tui pays a macOS
// Gatekeeper/XProtect assessment (~1s idle, worse under load). Both the
// file-list diff modal and the account switcher exec that binary, so the FIRST
// modal open after a rebuild or reboot stalled visibly. warm_tui_binary runs
// the binary once in the background at session launch so the assessment is
// already paid by the time the user clicks.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// warm_tui_binary must exec `wisp-deck-tui --version` (in the background) so
// the OS pays the first-run assessment before any modal needs the binary.
func TestWarmTuiBinary_execs_binary_with_version(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	binDir := mockCommand(t, dir, "wisp-deck-tui", `echo "$@" >> `+logFile)
	env := buildEnv(t, []string{binDir})

	out, code := runBashFunc(t, "lib/tui.sh", "warm_tui_binary", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("warm_tui_binary should be silent, got %q", out)
	}

	// The warm-up runs in the background — poll for the mock's log.
	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(logFile); err == nil && len(b) > 0 {
			data = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if data == nil {
		t.Fatal("warm_tui_binary never exec'd wisp-deck-tui")
	}
	if !strings.Contains(string(data), "--version") {
		t.Errorf("warm_tui_binary should call --version (cheap, no TUI), got %q", string(data))
	}
}

// A missing binary must be a silent no-op — the wrapper already errors
// separately when the binary is absent; the warm-up must never add noise.
func TestWarmTuiBinary_silent_noop_when_binary_missing(t *testing.T) {
	dir := t.TempDir()
	// PATH with only an empty dir: no wisp-deck-tui anywhere.
	emptyBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(emptyBin, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + emptyBin, "HOME=" + dir}

	out, code := runBashFunc(t, "lib/tui.sh", "warm_tui_binary", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("warm_tui_binary should be silent when binary missing, got %q", out)
	}
}

// warm_tui_binary must not block the caller: even if the binary hangs, the
// function must return promptly (the warm-up is fire-and-forget).
func TestWarmTuiBinary_does_not_block_on_slow_binary(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "wisp-deck-tui", `sleep 10`)
	env := buildEnv(t, []string{binDir})

	start := time.Now()
	_, code := runBashFunc(t, "lib/tui.sh", "warm_tui_binary", nil, env)
	assertExitCode(t, code, 0)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("warm_tui_binary blocked for %v; must return immediately", elapsed)
	}
}

// wrapper.sh must warm the binary at session launch — after the libs are
// sourced (the function lives in lib/tui.sh) and before the tmux session is
// built, so the assessment overlaps session setup instead of the first click.
func TestWrapper_warms_tui_binary_at_launch(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("failed to read wrapper.sh: %v", err)
	}
	if !strings.Contains(string(data), "warm_tui_binary") {
		t.Error("wrapper.sh should call warm_tui_binary at launch so the first modal open doesn't pay the binary's first-run Gatekeeper assessment")
	}
}

// ensure_wisp_deck_tui downloads a brand-new binary on install/update — it
// must exec it once right after so the first-run assessment happens at
// install time, not on the user's first modal click.
func TestEnsureWispDeckTui_warms_freshly_installed_binary(t *testing.T) {
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	shareDir := t.TempDir()
	writeTempFile(t, shareDir, "VERSION", "9.9.9")

	// The "downloaded" binary logs every invocation, so we can see the warm-up.
	warmLog := filepath.Join(dir, "warm.log")
	binDir := mockCommand(t, dir, "curl", mockCurlWriting("", `cat > "$dest" <<PAYLOAD
#!/bin/bash
echo "\$@" >> `+warmLog+`
case "\$1" in
  --version) echo "wisp-deck-tui version 9.9.9" ;;
  capabilities)
    [ "\$2" = "--require-production" ] || exit 1
    echo '`+validTuiCapabilities+`'
    ;;
  *) exit 1 ;;
esac
PAYLOAD
exit 0`))
	mockCommand(t, dir, "uname", `echo "arm64"`)

	snippet := installSnippet(t, `ensure_wisp_deck_tui "`+shareDir+`"`)
	env := buildEnv(t, []string{binDir}, "HOME="+fakeHome)
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(warmLog)
	if err != nil {
		t.Fatal("ensure_wisp_deck_tui never exec'd the freshly downloaded binary; it must run it once (--version) so the first-run Gatekeeper assessment happens at install time")
	}
	if !strings.Contains(string(data), "--version") {
		t.Errorf("the install-time warm-up should use --version, got %q", string(data))
	}
}

// The Makefile install target copies + re-signs the binary into ~/.local/bin,
// which resets the Gatekeeper assessment — it must exec it once after.
func TestMakefile_install_warms_binary(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("failed to read Makefile: %v", err)
	}
	s := string(data)
	signIdx := strings.Index(s, "codesign --sign - --force $(HOME)/.local/bin/wisp-deck-tui")
	warmIdx := strings.Index(s, "$(HOME)/.local/bin/wisp-deck-tui --version")
	if warmIdx == -1 {
		t.Fatal("Makefile install target should run the installed binary once (--version) after codesign, so the first-run Gatekeeper assessment doesn't land on the next modal open")
	}
	if signIdx != -1 && warmIdx < signIdx {
		t.Error("the warm-up exec must come AFTER codesign (signing resets the assessment)")
	}
}

// The release script re-signs the freshly built local binary, which resets the
// Gatekeeper assessment — it must exec the binary once right after so the
// developer's next modal open is warm.
func TestReleaseScript_warms_local_binary_after_rebuild(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read scripts/release.sh: %v", err)
	}
	s := string(data)
	signIdx := strings.Index(s, "codesign --sign")
	warmIdx := strings.Index(s, `"$local_bin" --version`)
	if warmIdx == -1 {
		t.Fatal("scripts/release.sh should run \"$local_bin\" --version after rebuilding + signing the local binary, so the first-run Gatekeeper assessment happens at release time, not on the next modal open")
	}
	if signIdx != -1 && warmIdx < signIdx {
		t.Error("the warm-up exec must come AFTER codesign (signing resets the assessment)")
	}
}
