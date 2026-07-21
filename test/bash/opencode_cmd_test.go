package bash_test

import (
	"os"
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

// A cached npx install must win over @latest: the registry's advertised
// latest can be uninstallable (observed live: opencode-ai@latest resolved to
// 1.17.18 which 404s — ETARGET), and then every @latest launch dies at npm
// and dumps the pane to a bare shell while a working cached copy sits unused.
func TestResolveOpencodeCmd_prefers_cached_npx_install(t *testing.T) {
	dir := t.TempDir()
	// npx succeeds for the --no-install probe: a cached copy exists.
	binDir := mockCommand(t, dir, "npx", `[ "$1" = "--no-install" ] && exit 0; echo npx "$@"`)

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx --no-install opencode-ai" {
		t.Errorf("got %q, want %q", got, "npx --no-install opencode-ai")
	}
}

func TestResolveOpencodeCmd_falls_back_to_prefer_offline_npx(t *testing.T) {
	dir := t.TempDir()
	// npx fails the --no-install probe: nothing cached, install path needed.
	binDir := mockCommand(t, dir, "npx", `[ "$1" = "--no-install" ] && exit 1; echo npx "$@"`)

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, ocEnv(t, binDir))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx --prefer-offline opencode-ai@latest" {
		t.Errorf("got %q, want %q", got, "npx --prefer-offline opencode-ai@latest")
	}
}

// The --no-install probe spawns node (6-13s measured, warm cache) and used to
// run on EVERY OpenCode launch. Its verdict is a property of the npx cache
// contents, so a successful probe is recorded together with the package.json
// of the cached opencode-ai copy that satisfied it. While that file still
// exists, later launches trust the record and skip the probe; when it is gone
// (npm cache cleared, package updated away) the probe runs again.
func TestResolveOpencodeCmd_probe_verdict_is_cached(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	pkgJSON := filepath.Join(home, ".npm", "_npx", "abc123", "node_modules", "opencode-ai", "package.json")
	writeTempFile(t, filepath.Dir(pkgJSON), "package.json", `{"version":"1.0.0"}`)

	npxLog := filepath.Join(dir, "npx.log")
	binDir := mockCommand(t, dir, "npx", `
echo "npx $*" >> "$NPX_LOG"
[ "$1" = "--no-install" ] && exit 0
exit 0
`)
	// PATH keeps the system dirs (mkdir/rm for the verdict write) but the mock
	// bin dir comes first so its npx shadows any real one; neither opencode
	// nor npx lives in /usr/bin:/bin.
	env := buildEnv(t, nil, "PATH="+binDir+":/usr/bin:/bin", "HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "NPX_LOG="+npxLog)

	// First resolve pays the probe and records the verdict.
	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx --no-install opencode-ai" {
		t.Fatalf("first resolve: got %q, want no-install", got)
	}
	if readFileTrim(t, npxLog) == "" {
		t.Fatal("first resolve must actually probe npx")
	}

	// Second resolve: same answer, WITHOUT spawning npx.
	if err := os.Remove(npxLog); err != nil {
		t.Fatal(err)
	}
	out, code = runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx --no-install opencode-ai" {
		t.Fatalf("cached resolve: got %q, want no-install", got)
	}
	if got := readFileTrim(t, npxLog); got != "" {
		t.Errorf("cached resolve spawned npx (6-13s of node on the launch path):\n%s", got)
	}

	// The recorded copy disappears (npm cache cleared): the verdict is stale
	// and the probe must run again rather than launch a dead command.
	if err := os.RemoveAll(filepath.Join(home, ".npm")); err != nil {
		t.Fatal(err)
	}
	out, code = runBashFunc(t, "lib/ai-tools.sh", "resolve_opencode_cmd", nil, env)
	assertExitCode(t, code, 0)
	if readFileTrim(t, npxLog) == "" {
		t.Error("resolve trusted a stale verdict after the cached opencode-ai copy vanished")
	}
	if got := strings.TrimSpace(out); got != "npx --no-install opencode-ai" {
		t.Errorf("re-probe: got %q, want no-install (mock npx still succeeds)", got)
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
