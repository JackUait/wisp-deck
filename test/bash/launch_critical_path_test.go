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

// The launch critical path: everything wrapper.sh does between the Ghostty
// window opening and the project picker painting. The user stares at the
// loading splash for exactly this long, so it is the one stretch of code where
// a blocking subprocess is a user-visible defect rather than a slow function.
//
// It has regressed before. `resolve_opencode_cmd` shelled out to
// `npx --no-install opencode-ai --version` — spawning node purely to decide
// WHICH npx string could launch OpenCode — and it ran eagerly, on every launch,
// for every tool. It cost 3s warm and 6-13s under load. Nobody noticed for
// releases, because the splash screen made the wait look like work.
//
// A guard naming that one function would not have caught it before it shipped,
// and will not catch the next one: the next offender will be some other command
// that seems cheap until it is on the wrong side of the picker. So this guard
// tests the PROPERTY, not the instance.
//
// How it works: every command that is expensive in real life (npx, npm, node,
// curl, ...) is mocked to be expensive HERE — each sleeps far longer than the
// budget. wisp-deck-tui is mocked to decline, which makes wrapper.sh run its
// whole pre-picker path and exit. If any of those commands is invoked
// synchronously before the picker, the wrapper cannot finish inside the budget
// and this test fails, naming the command.
//
// Backgrounded work (the once-a-day `npm view` update check is disowned by
// design) does not block the wrapper's exit and correctly does NOT fail this
// test — blocking is the defect, spawning is not.

// Commands that reach the network or boot a language runtime. None of these has
// any business running before the picker paints.
//
// The wall-clock budget below catches a slow pre-picker path whatever the cause
// — even a slow loop in bash. These mocks exist to make commands that are slow
// in REAL life reliably slow in the sandbox too, where they might otherwise
// return instantly (no network, nothing installed) and let a genuine regression
// pass. So the list is deliberately broad: err toward listing a runtime nobody
// has used yet, because the whole point is catching the offender nobody
// predicted. Adding a name here is free — if it is not on the path, nothing
// invokes it.
var expensiveCommands = []string{
	// node ecosystem (the one that already bit us)
	"npx", "npm", "node", "pnpm", "yarn", "bun", "deno",
	// other language runtimes / package managers
	"python", "python3", "pip", "pip3", "ruby", "gem", "cargo", "go",
	// network + heavyweight CLIs
	"curl", "wget", "brew", "gh", "docker", "ssh", "aws", "kubectl",
}

const (
	// Far longer than any plausible honest pause, so a synchronous call cannot
	// hide inside the budget. Real npx was 3-13s; 20s is unmistakable.
	expensiveCommandDelay = 20 * time.Second
	// The real path measures ~0.13s. 5s is ~38x headroom, so this stays stable
	// under a loaded parallel suite instead of becoming another wall-clock flake.
	criticalPathBudget = 5 * time.Second
)

// TestLaunchCriticalPath_paints_the_picker_without_blocking_on_any_subprocess is
// the regression guard for the class of bug, not the instance.
func TestLaunchCriticalPath_paints_the_picker_without_blocking_on_any_subprocess(t *testing.T) {
	root := projectRoot(t)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}

	// wrapper.sh sources its libs through the config dir's lib symlink, exactly
	// as a real install does — so the code under test is the real code, not a
	// stripped-down copy of it.
	if err := os.Symlink(filepath.Join(root, "lib"), filepath.Join(cfg, "lib")); err != nil {
		t.Fatalf("symlink lib: %v", err)
	}
	writeTempFile(t, cfg, "ai-tool", "claude\n")
	writeTempFile(t, cfg, "projects", "demo:"+home+"\n")
	// Steady state: the update check ran recently, so its (deliberately
	// backgrounded) npm call is throttled off. Without this the test would be
	// asserting against a once-per-day code path rather than the everyday one.
	writeTempFile(t, cfg, "last-update-check", strconv.FormatInt(time.Now().Unix(), 10)+"\n")

	// The spies MUST live in $HOME/.local/bin, and nowhere else. wrapper.sh's
	// first act is
	//
	//	export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
	//
	// so a spy dir merely prepended to the test's PATH is shadowed by Homebrew,
	// and every offender installed there (python3, gh, docker, go, wget, ...)
	// would run the REAL, fast binary and sail past this guard. That hole was
	// live until a python3 regression passed a test that should have caught it.
	// $HOME/.local/bin is the one entry wrapper.sh puts ahead of those — and it
	// is where wisp-deck-tui genuinely installs, so this is also the honest layout.
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir spybin: %v", err)
	}
	calls := filepath.Join(home, "calls.log")

	// Each expensive command records itself and then takes as long as the real
	// thing would. Anything calling it synchronously blows the budget.
	for _, name := range expensiveCommands {
		writeExecutable(t, filepath.Join(bin, name), "#!/bin/bash\n"+
			"echo \""+name+" $*\" >> "+strconv.Quote(calls)+"\n"+
			"sleep "+strconv.Itoa(int(expensiveCommandDelay.Seconds()))+"\n"+
			"exit 0\n")
	}

	// The picker itself: record the invocation, then decline (exit 1), which
	// wrapper.sh treats as the user pressing ESC and exits 0. Reaching this is
	// proof the whole pre-picker path ran.
	writeExecutable(t, filepath.Join(bin, "wisp-deck-tui"), "#!/bin/bash\n"+
		"echo \"wisp-deck-tui $*\" >> "+strconv.Quote(calls)+"\n"+
		"exit 1\n")

	env := buildEnv(t, []string{bin},
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)

	// A guard that cannot see the thing it guards is worse than no guard: it
	// reports safety. Before trusting the result, prove the spies actually win
	// PATH resolution once wrapper.sh has applied its own PATH prologue — so if
	// that prologue ever changes and re-shadows them, THIS fails loudly instead
	// of the budget check quietly passing everything.
	assertSpiesAreReachable(t, env, bin)

	// Output goes to a file, never a pipe: a pipe would stay open until the
	// disowned background children exit too, and we would end up timing THEM
	// instead of the wrapper. We want the wrapper's own blocking time.
	out := filepath.Join(home, "wrapper.out")
	elapsed, exit := runScriptTimed(t, filepath.Join(root, "wrapper.sh"), env, out)

	logged, _ := os.ReadFile(calls)
	if !strings.Contains(string(logged), "main-menu") {
		body, _ := os.ReadFile(out)
		t.Fatalf("wrapper.sh never reached the project picker (exit %d) — this guard "+
			"proves nothing unless the pre-picker path actually ran.\ncalls:\n%s\noutput:\n%s",
			exit, logged, truncate(string(body), 2000))
	}

	if elapsed > criticalPathBudget {
		t.Errorf("wrapper.sh took %s to reach the project picker (budget %s).\n\n"+
			"Something on the launch critical path is now BLOCKING on a subprocess. "+
			"These expensive commands were invoked before the picker:\n%s\n"+
			"Move the call after the picker, or make it answerable without the "+
			"subprocess — see opencode_available() in lib/ai-tools.sh for the pattern.",
			elapsed.Round(time.Millisecond), criticalPathBudget,
			indent(blockingCalls(string(logged))))
	}
}

// blockingCalls lists the expensive commands that fired, so a failure names the
// culprit instead of just reporting a slow number.
func blockingCalls(log string) string {
	var hits []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		for _, name := range expensiveCommands {
			if strings.HasPrefix(line, name+" ") {
				hits = append(hits, line)
			}
		}
	}
	if len(hits) == 0 {
		return "(none recorded — the delay is inside wrapper.sh itself, not a subprocess)"
	}
	return strings.Join(hits, "\n")
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

// writeExecutable drops an executable script at path. (mockCommand always writes
// into dir/bin; this guard needs full control of its PATH directory.)
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runScriptTimed runs a script to completion and reports how long the script
// ITSELF blocked. Output is redirected to a file rather than collected through a
// pipe on purpose: a pipe is not closed until every process holding it exits,
// including disowned background children, so piping would time the background
// work too and make a correctly-backgrounded call look like a blocking one.
func runScriptTimed(t *testing.T, script string, env []string, outFile string) (time.Duration, int) {
	t.Helper()
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatalf("create %s: %v", outFile, err)
	}
	defer f.Close()

	cmd := exec.Command("bash", script)
	cmd.Env = env
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Stdin = nil

	start := time.Now()
	err = cmd.Run()
	elapsed := time.Since(start)

	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s: %v", script, err)
		}
		code = exitErr.ExitCode()
	}
	return elapsed, code
}

// assertSpiesAreReachable replays wrapper.sh's own PATH prologue and checks that
// every expensive command still resolves to our mock rather than a real binary.
// This is the guard's guard: it is the check that would have caught the spy dir
// being shadowed by /opt/homebrew/bin.
func assertSpiesAreReachable(t *testing.T, env []string, bin string) {
	t.Helper()
	// Byte-for-byte the PATH line at the top of wrapper.sh.
	script := `export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
for c in ` + strings.Join(expensiveCommands, " ") + `; do
  printf '%s\t%s\n' "$c" "$(command -v "$c" 2>/dev/null || echo MISSING)"
done`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe PATH: %v", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, resolved, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if !strings.HasPrefix(resolved, bin+"/") {
			t.Fatalf("spy for %q is shadowed: after wrapper.sh's PATH prologue it resolves to %q, "+
				"not the mock in %s.\n\nThis guard would silently pass a blocking %s call. "+
				"Put the mocks somewhere wrapper.sh's PATH cannot outrank.",
				name, resolved, bin, name)
		}
	}
}
