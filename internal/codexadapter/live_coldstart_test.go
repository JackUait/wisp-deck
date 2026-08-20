package codexadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveColdAppServerIsObserved runs the real startup path — the real
// app-server process and the real observer handshake — against a real Codex
// binary, which the fake-observer unit tests cannot cover. Point
// resolves the vendored Mach-O behind the npm launcher and copies it to a
// fresh inode, so the exec pays a genuine cold start rather than reusing a
// warm one. WISP_DECK_LIVE_CODEX_BIN overrides which Codex is used.
func TestLiveColdAppServerIsObserved(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_CODEX_COLD_E2E") != "1" {
		t.Skip("set WISP_DECK_LIVE_CODEX_COLD_E2E=1 to run against a real Codex")
	}
	codexPath := liveCodexBinary(t)

	options := supervisorOptions(t, "", "")
	options.CodexPath = codexPath
	options.ProjectCWD = t.TempDir()

	var launch []string
	supervisor := CodexSupervisor{
		TempBase: shortCodexTempBase(t),
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			launch = append([]string(nil), argv...)
			return CodexExitResult{Elapsed: options.FallbackWindow}, nil
		},
	}

	started := time.Now()
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	elapsed := time.Since(started)
	// IdentityFile is unset, so a missed setup degrades to an unobserved
	// launch instead of an error. --remote is the only proof it was observed.
	if !containsArg(launch, "--remote") {
		t.Fatalf("real app-server was not observed after %s: %#v", elapsed, launch)
	}
	t.Logf("real cold app-server observed in %s (budget %s)", elapsed, defaultCodexStartupTimeout)
}

func liveCodexBinary(t *testing.T) string {
	t.Helper()
	source := os.Getenv("WISP_DECK_LIVE_CODEX_BIN")
	if source == "" {
		resolved, err := exec.LookPath("codex")
		if err != nil {
			t.Skipf("no codex on PATH: %v", err)
		}
		source = resolved
	}
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		source = resolved
	}
	if isLauncherScript(t, source) {
		vendored := vendoredCodexBinary(source)
		if vendored == "" {
			t.Logf("%s is a launcher script and its payload was not found; "+
				"running it in place, so this start may be warm", source)
			return source
		}
		source = vendored
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh inode has neither cached pages nor a cached signature
	// assessment, which is the closest a test gets to a first-ever exec.
	cold := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(cold, data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Logf("cold copy of %s (%d bytes)", source, len(data))
	return cold
}

func isLauncherScript(t *testing.T, path string) bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header := make([]byte, 2)
	n, _ := file.Read(header)
	return n == 2 && string(header) == "#!"
}

// vendoredCodexBinary finds the Mach-O the npm launcher spawns. The launcher
// resolves its payload relative to its own directory, so only the payload can
// be copied somewhere cold and still run.
func vendoredCodexBinary(launcher string) string {
	dir := filepath.Dir(launcher)
	patterns := []string{
		filepath.Join(dir, "..", "lib", "node_modules", "@openai", "codex",
			"node_modules", "@openai", "codex-*", "vendor", "*", "bin", "codex"),
		filepath.Join(dir, "..", "node_modules", "@openai", "codex-*", "vendor", "*", "bin", "codex"),
		filepath.Join(dir, "..", "vendor", "*", "bin", "codex"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return match
			}
		}
	}
	return ""
}
