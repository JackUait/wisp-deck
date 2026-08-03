package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The bash -l PATH blindness that shipped as "Claude Code is not installed"
// is not claude-specific: codex installed by nvm's npm, or opencode living in
// ~/.bun/bin, are exactly as invisible to the Ghostty-launched wrapper. The
// claude-only resolver generalizes to resolve_agent_cmd/activate_agent_cmd/
// cache_agent_cmd, so every agent CLI wisp-deck can launch is found wherever
// it is installed — still with filesystem probes only, never a language
// runtime (launch critical path).

func agentToolsCSV(t *testing.T, log []byte) []string {
	t.Helper()
	m := regexp.MustCompile(`--ai-tools (\S+)`).FindSubmatch(log)
	if m == nil {
		t.Fatalf("picker was never given --ai-tools.\ncalls:\n%s", log)
	}
	return strings.Split(string(m[1]), ",")
}

func TestResolveAgentCmd_prefers_PATH(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "codex", `exit 0`)
	home := t.TempDir()

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_agent_cmd",
		[]string{"codex"}, claudeEnv(t, home, binDir))
	assertExitCode(t, code, 0)
	if got, want := strings.TrimSpace(out), filepath.Join(binDir, "codex"); got != want {
		t.Errorf("resolve_agent_cmd codex = %q, want the PATH hit %q", got, want)
	}
}

func TestResolveAgentCmd_uses_setup_cache_when_PATH_misses(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "custom-prefix", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write codex: %v", err)
	}
	cache := writeTempFile(t, home, "codex-cmd", real+"\n")

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_agent_cmd",
		[]string{"codex", cache}, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != real {
		t.Errorf("resolve_agent_cmd codex = %q, want the cached %q", got, real)
	}
}

func TestResolveAgentCmd_ignores_a_stale_cache(t *testing.T) {
	home := t.TempDir()
	cache := writeTempFile(t, home, "codex-cmd",
		filepath.Join(home, "gone", "codex")+"\n")

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_agent_cmd",
		[]string{"codex", cache}, claudeEnv(t, home))
	if code == 0 {
		t.Errorf("resolve_agent_cmd succeeded on a dangling cache, output %q", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("resolve_agent_cmd printed %q for a dangling cache, want nothing", out)
	}
}

// The home list is generic, not claude-specific: a volta-managed opencode
// must be found by the same resolver with no per-tool code.
func TestResolveAgentCmd_finds_a_version_manager_install(t *testing.T) {
	home := t.TempDir()
	volta := filepath.Join(home, ".volta", "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(volta), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(volta, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write opencode: %v", err)
	}

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_agent_cmd",
		[]string{"opencode"}, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != volta {
		t.Errorf("resolve_agent_cmd opencode = %q, want the volta install %q", got, volta)
	}
}

// Version order, not lexical order: v9 sorts AFTER v22 as strings.
func TestResolveAgentCmd_finds_the_newest_nvm_install(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".nvm", "versions", "node", "v9.1.0", "bin", "codex")
	newest := filepath.Join(home, ".nvm", "versions", "node", "v22.0.0", "bin", "codex")
	for _, p := range []string{old, newest} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatalf("write codex: %v", err)
		}
	}

	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_agent_cmd",
		[]string{"codex"}, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != newest {
		t.Errorf("resolve_agent_cmd codex = %q, want the newest nvm install %q", got, newest)
	}
}

func TestResolveAgentCmd_reports_nothing_found(t *testing.T) {
	home := t.TempDir()
	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_agent_cmd",
		[]string{"codex"}, claudeEnv(t, home))
	if code == 0 {
		t.Errorf("resolve_agent_cmd succeeded with no codex anywhere, output %q", out)
	}
}

// activate_agent_cmd is the wrapper's entry point: it must set the named
// variable AND put the find's bin dir on PATH, so the tool resolves for
// everything downstream — the Go menu's exec.LookPath, the tmux panes that
// exec it, and (for npm installs) the sibling `node` the shebang needs.
func TestActivateAgentCmd_puts_the_found_bin_dir_on_PATH(t *testing.T) {
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	codex := filepath.Join(nvmBin, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write codex: %v", err)
	}

	root := projectRoot(t)
	out, code := runBashSnippet(t, `
source `+strconv.Quote(filepath.Join(root, "lib/ai-tools.sh"))+`
activate_agent_cmd CODEX_CMD codex ""
echo "CODEX_CMD=$CODEX_CMD"
command -v codex >/dev/null 2>&1 && echo "ON-PATH" || echo "STILL-MISSING"
`, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	assertContains(t, out, "CODEX_CMD="+codex)
	assertContains(t, out, "ON-PATH")
}

// OpenCode's availability check is a PATH lookup (opencode_available); an
// opencode living only in ~/.bun/bin used to fail it under bash -l. After
// activation the same check must pass.
func TestActivateAgentCmd_makes_an_off_PATH_opencode_available(t *testing.T) {
	home := t.TempDir()
	bunBin := filepath.Join(home, ".bun", "bin")
	if err := os.MkdirAll(bunBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bunBin, "opencode"),
		[]byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write opencode: %v", err)
	}

	root := projectRoot(t)
	out, code := runBashSnippet(t, `
source `+strconv.Quote(filepath.Join(root, "lib/ai-tools.sh"))+`
opencode_available && echo "BEFORE-AVAILABLE" || echo "BEFORE-MISSING"
activate_agent_cmd _WD_OPENCODE_BIN opencode ""
opencode_available && echo "AFTER-AVAILABLE" || echo "AFTER-MISSING"
`, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	assertContains(t, out, "BEFORE-MISSING")
	assertContains(t, out, "AFTER-AVAILABLE")
}

func TestCacheAgentCmd_records_the_users_shell_resolution(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "codex", `exit 0`)
	home := t.TempDir()
	cache := filepath.Join(home, "cfg", "codex-cmd")

	_, code := runBashFunc(t, "lib/ai-tools.sh", "cache_agent_cmd",
		[]string{"codex", cache}, claudeEnv(t, home, binDir))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatalf("cache file was not written: %v", err)
	}
	if got, want := strings.TrimSpace(string(data)), filepath.Join(binDir, "codex"); got != want {
		t.Errorf("cache holds %q, want %q", got, want)
	}
}

func TestCacheAgentCmd_writes_nothing_when_the_tool_is_missing(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cfg", "opencode-cmd")

	_, code := runBashFunc(t, "lib/ai-tools.sh", "cache_agent_cmd",
		[]string{"opencode", cache}, claudeEnv(t, home))
	assertExitCode(t, code, 0)
	if _, err := os.Stat(cache); err == nil {
		t.Error("cache file written with no opencode on PATH — a wrong cache is worse than none")
	}
}

// Setup runs in the user's real shell — the only wisp-deck process that sees
// an nvm/volta PATH — and must record where EVERY agent CLI lives there, not
// just claude: codex installs through the same npm channels, and opencode's
// direct binary hides the same way.
func TestSetup_caches_every_agent_command_for_the_wrapper(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "wisp-deck"))
	if err != nil {
		t.Fatalf("read bin/wisp-deck: %v", err)
	}
	for _, tool := range []string{"claude", "codex", "opencode"} {
		if !strings.Contains(string(data), tool) || !strings.Contains(string(data), "cache_agent_cmd") {
			t.Errorf("bin/wisp-deck must cache the %s location via cache_agent_cmd — "+
				"setup is the only process that ever sees the user's real PATH", tool)
		}
	}
}

// End-to-end mirror of the shipped claude bug, for codex: installed ONLY where
// nvm's npm puts it, invisible to the wrapper's bash -l PATH — the picker must
// still be told codex is available.
func TestWrapper_detects_codex_through_the_nvm_home(t *testing.T) {
	root := projectRoot(t)
	home, bin, calls, env := wrapperSandbox(t)

	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir nvm bin: %v", err)
	}
	writeExecutable(t, filepath.Join(nvmBin, "codex"), "#!/bin/bash\nexit 0\n")

	// Guard of the guard: if a codex is reachable after wrapper.sh's own PATH
	// prologue, the assertion below passes without exercising the resolver.
	probe := exec.Command("bash", "-c",
		`export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"; command -v codex`)
	probe.Env = env
	if leak, err := probe.Output(); err == nil {
		t.Fatalf("codex leaked into the sandbox PATH at %q — this test would prove nothing",
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
	for _, tool := range agentToolsCSV(t, logged) {
		if tool == "codex" {
			return
		}
	}
	t.Errorf("the picker was not told codex is available — an nvm-installed "+
		"codex is invisible again.\npicker args:\n%s", logged)
}

// Same proof for opencode's direct binary in a bun home. Skipped when the host
// leaks an npx through the wrapper's PATH prologue (CI runners ship node), as
// opencode_available would then pass without the resolver.
func TestWrapper_detects_opencode_through_the_bun_home(t *testing.T) {
	root := projectRoot(t)
	home, bin, calls, env := wrapperSandbox(t)

	leakProbe := exec.Command("bash", "-c",
		`export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"; command -v npx || command -v opencode`)
	leakProbe.Env = env
	if leak, err := leakProbe.Output(); err == nil {
		t.Skipf("host npx/opencode reachable at %q — opencode would be available "+
			"without the resolver, proving nothing", strings.TrimSpace(string(leak)))
	}

	bunBin := filepath.Join(home, ".bun", "bin")
	if err := os.MkdirAll(bunBin, 0o755); err != nil {
		t.Fatalf("mkdir bun bin: %v", err)
	}
	writeExecutable(t, filepath.Join(bunBin, "opencode"), "#!/bin/bash\nexit 0\n")

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
	for _, tool := range agentToolsCSV(t, logged) {
		if tool == "opencode" {
			return
		}
	}
	t.Errorf("the picker was not told opencode is available — a bun-installed "+
		"opencode is invisible again.\npicker args:\n%s", logged)
}
