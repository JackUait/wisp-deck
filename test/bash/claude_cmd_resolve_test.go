package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Ghostty launches wrapper.sh through `bash -l`, which never reads a zsh
// user's ~/.zshrc — so a claude that lives on a PATH built there (an npm
// install under nvm/volta/asdf, or the legacy ~/.claude/local alias install)
// runs fine in the user's own terminal while `command -v claude` in the
// wrapper finds nothing. The shipped symptom: setup reports Claude Code
// installed, the launched menu says "not installed" with no way out.
//
// resolve_claude_cmd answers "where is claude?" with PATH first, then the
// path setup cached from the user's real shell, then the well-known install
// homes. Every probe is a filesystem stat or a tiny utility spawn — never a
// language runtime — so the launch critical path stays fast.

func claudeEnv(t *testing.T, home string, pathDirs ...string) []string {
	t.Helper()
	path := strings.Join(append(pathDirs, "/usr/bin", "/bin", "/usr/sbin", "/sbin"), ":")
	return buildEnv(t, nil, "HOME="+home, "PATH="+path)
}

func TestResolveClaudeCmd_prefers_PATH(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "claude", `exit 0`)
	home := t.TempDir()

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_claude_cmd", nil,
		claudeEnv(t, home, binDir))
	assertExitCode(t, code, 0)
	if got, want := strings.TrimSpace(out), filepath.Join(binDir, "claude"); got != want {
		t.Errorf("resolve_claude_cmd = %q, want the PATH hit %q", got, want)
	}
}

func TestResolveClaudeCmd_uses_setup_cache_when_PATH_misses(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "custom-prefix", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	cache := writeTempFile(t, home, "claude-cmd", real+"\n")

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_claude_cmd",
		[]string{cache}, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != real {
		t.Errorf("resolve_claude_cmd = %q, want the cached %q", got, real)
	}
}

// A cached path stops being true when the node version it lived under is
// removed. A stale cache must be skipped, not returned.
func TestResolveClaudeCmd_ignores_a_stale_cache(t *testing.T) {
	home := t.TempDir()
	cache := writeTempFile(t, home, "claude-cmd",
		filepath.Join(home, "gone", "claude")+"\n")

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_claude_cmd",
		[]string{cache}, claudeEnv(t, home))
	if code == 0 {
		t.Errorf("resolve_claude_cmd succeeded on a dangling cache, output %q", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("resolve_claude_cmd printed %q for a dangling cache, want nothing", out)
	}
}

func TestResolveClaudeCmd_finds_the_legacy_local_install(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".claude", "local", "claude")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(local, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_claude_cmd", nil,
		claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != local {
		t.Errorf("resolve_claude_cmd = %q, want the ~/.claude/local install %q", got, local)
	}
}

// Version order, not lexical order: v9 sorts AFTER v22 as strings, so a plain
// sort would hand back the oldest node's claude.
func TestResolveClaudeCmd_finds_the_newest_nvm_install(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".nvm", "versions", "node", "v9.1.0", "bin", "claude")
	newest := filepath.Join(home, ".nvm", "versions", "node", "v22.0.0", "bin", "claude")
	for _, p := range []string{old, newest} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatalf("write claude: %v", err)
		}
	}

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_claude_cmd", nil,
		claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != newest {
		t.Errorf("resolve_claude_cmd = %q, want the newest nvm install %q", got, newest)
	}
}

func TestResolveClaudeCmd_reports_nothing_found(t *testing.T) {
	home := t.TempDir()
	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_claude_cmd", nil,
		claudeEnv(t, home))
	if code == 0 {
		t.Errorf("resolve_claude_cmd succeeded with no claude anywhere, output %q", out)
	}
}

// activate_claude_cmd is the wrapper's entry point: it must set CLAUDE_CMD and
// put the find's bin dir on PATH, so that `claude` resolves for everything
// downstream — the Go menu's own exec.LookPath, the tmux panes that exec
// claude, and (for npm installs) the sibling `node` the shebang needs.
func TestActivateClaudeCmd_puts_the_found_bin_dir_on_PATH(t *testing.T) {
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	claude := filepath.Join(nvmBin, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write claude: %v", err)
	}

	root := projectRoot(t)
	out, code := runBashSnippet(t, `
source `+strconv.Quote(filepath.Join(root, "lib/ai-tools.sh"))+`
activate_claude_cmd ""
echo "CLAUDE_CMD=$CLAUDE_CMD"
command -v claude >/dev/null 2>&1 && echo "ON-PATH" || echo "STILL-MISSING"
`, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	assertContains(t, out, "CLAUDE_CMD="+claude)
	assertContains(t, out, "ON-PATH")
}

// cache_claude_cmd runs from bin/wisp-deck — the one process that lives in the
// user's real shell, where an nvm/volta PATH is actually loaded. It records
// what `command -v claude` said there for the wrapper to read back.
func TestCacheClaudeCmd_records_the_users_shell_resolution(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "claude", `exit 0`)
	home := t.TempDir()
	cache := filepath.Join(home, "cfg", "claude-cmd")

	_, code := runBashFunc(t, "lib/ai-tools.sh", "cache_claude_cmd",
		[]string{cache}, claudeEnv(t, home, binDir))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatalf("cache file was not written: %v", err)
	}
	if got, want := strings.TrimSpace(string(data)), filepath.Join(binDir, "claude"); got != want {
		t.Errorf("cache holds %q, want %q", got, want)
	}
}

func TestCacheClaudeCmd_writes_nothing_when_claude_is_missing(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cfg", "claude-cmd")

	_, code := runBashFunc(t, "lib/ai-tools.sh", "cache_claude_cmd",
		[]string{cache}, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if _, err := os.Stat(cache); err == nil {
		t.Error("cache file written with no claude on PATH — a wrong cache is worse than none")
	}
}

// Setup must actually write the cache: it is the only process that ever sees
// the user's real PATH, so skipping it there loses the answer for good.
func TestSetup_caches_the_claude_command_for_the_wrapper(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "wisp-deck"))
	if err != nil {
		t.Fatalf("read bin/wisp-deck: %v", err)
	}
	if !strings.Contains(string(data), "cache_claude_cmd") {
		t.Error("bin/wisp-deck must call cache_claude_cmd — setup runs in the user's " +
			"real shell, the only place an nvm/volta claude is on PATH at all")
	}
}

// wrapperSandbox builds the minimal installed layout wrapper.sh needs to reach
// the project picker: an isolated $HOME, the config dir with its lib symlink,
// and a PATH stripped to the system dirs plus the sandbox's own ~/.local/bin —
// so nothing from the host machine (like a real claude) can leak in.
func wrapperSandbox(t *testing.T) (home, bin, callsLog string, env []string) {
	t.Helper()
	root := projectRoot(t)
	home = t.TempDir()
	cfg := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "lib"), filepath.Join(cfg, "lib")); err != nil {
		t.Fatalf("symlink lib: %v", err)
	}
	writeTempFile(t, cfg, "ai-tool", "claude\n")
	writeTempFile(t, cfg, "projects", "demo:"+home+"\n")
	writeTempFile(t, cfg, "last-update-check", strconv.FormatInt(time.Now().Unix(), 10)+"\n")
	bin = filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	callsLog = filepath.Join(home, "calls.log")
	env = buildEnv(t, nil,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PATH="+bin+":/usr/bin:/bin:/usr/sbin:/sbin",
	)
	return home, bin, callsLog, env
}

// The end-to-end proof for the shipped bug: claude installed ONLY where nvm's
// npm puts it, invisible to the wrapper's bash -l PATH — the picker must still
// be told claude is available.
func TestWrapper_detects_claude_through_the_nvm_home(t *testing.T) {
	root := projectRoot(t)
	home, bin, calls, env := wrapperSandbox(t)

	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir nvm bin: %v", err)
	}
	writeExecutable(t, filepath.Join(nvmBin, "claude"), "#!/bin/bash\nexit 0\n")

	// Guard of the guard: if a claude is reachable after wrapper.sh's own PATH
	// prologue, the assertion below passes without exercising the probe.
	probe := exec.Command("bash", "-c",
		`export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"; command -v claude`)
	probe.Env = env
	if leak, err := probe.Output(); err == nil {
		t.Fatalf("claude leaked into the sandbox PATH at %q — this test would prove nothing",
			strings.TrimSpace(string(leak)))
	}

	writeExecutable(t, filepath.Join(bin, "wisp-deck-tui"), "#!/bin/bash\n"+
		"echo \"wisp-deck-tui $*\" >> "+strconv.Quote(calls)+"\n"+
		"exit 1\n")

	outFile := filepath.Join(home, "wrapper.out")
	runScriptTimed(t, filepath.Join(root, "wrapper.sh"), env, outFile)

	logged, _ := os.ReadFile(calls)
	if !strings.Contains(string(logged), "main-menu") {
		body, _ := os.ReadFile(outFile)
		t.Fatalf("wrapper.sh never reached the picker.\ncalls:\n%s\noutput:\n%s",
			logged, truncate(string(body), 2000))
	}
	if !strings.Contains(string(logged), "--ai-tools claude") {
		t.Errorf("the picker was not told claude is available — an nvm-installed "+
			"claude is invisible again.\npicker args:\n%s", logged)
	}
}
