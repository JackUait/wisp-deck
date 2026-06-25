package bash_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// resolve_opencode_cmd picks the command used to launch OpenCode. The motivation
// is launch speed: `npx opencode-ai@latest` revalidates against the npm registry
// on every launch (~6s warm) and reinstalls the whole package on every version
// bump (~46s). A directly-installed `opencode` binary launches in ~2s, so it is
// preferred whenever present. When only npx exists, the fallback adds
// --prefer-offline so the npx cache is reused instead of hitting the registry.

// ocEnv builds a hermetic env whose PATH is exactly binDir, so command -v sees
// only the commands the test mocked (no leakage from the real machine PATH).
func ocEnv(t *testing.T, binDir string) []string {
	t.Helper()
	return buildEnv(t, nil, "PATH="+binDir)
}

func TestResolveOpencodeCmd_prefers_direct_binary(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "opencode", `echo opencode "$@"`)
	// npx also present — the binary must still win.
	mockCommand(t, dir, "npx", `echo npx "$@"`)

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "opencode" {
		t.Errorf("got %q, want %q", got, "opencode")
	}
}

func TestResolveOpencodeCmd_falls_back_to_prefer_offline_npx(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", `echo npx "$@"`)

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx --prefer-offline opencode-ai@latest" {
		t.Errorf("got %q, want %q", got, "npx --prefer-offline opencode-ai@latest")
	}
}

func TestResolveOpencodeCmd_empty_when_neither_present(t *testing.T) {
	dir := t.TempDir()
	// An empty bin dir: neither opencode nor npx on PATH.
	emptyBin := filepath.Join(dir, "empty")
	mockCommand(t, dir, "placeholder", `:`) // creates dir/bin; we point PATH elsewhere
	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, ocEnv(t, emptyBin))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
