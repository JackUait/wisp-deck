package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The POST-pick launch path: everything wrapper.sh does between the project
// being decided and the tmux session chain being issued. The user stares at a
// dead terminal for exactly this long — the splash is already stopped and the
// panes do not exist yet.
//
// This stretch has collected blocking spawns of its own: python3 ran up to
// three times per Claude launch (two of them one-time migrations), and an
// uncached `wisp-deck-tui ledger --help` Go exec ran per pane. Like the
// pre-picker guard in launch_critical_path_test.go, this tests the PROPERTY:
// expensive commands are mocked to be slow, and any synchronous call blows a
// generous wall-clock budget and names itself. python3 is the one exception —
// the per-generation Claude settings overlay legitimately needs one spawn —
// so it is mocked fast and COUNTED instead: more than one spawn per launch
// means a migration crept back onto the hot path.
func TestLaunchPostPickPath_reaches_tmux_without_blocking_on_any_subprocess(t *testing.T) {
	root := projectRoot(t)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "lib"), filepath.Join(cfg, "lib")); err != nil {
		t.Fatalf("symlink lib: %v", err)
	}
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	writeTempFile(t, cfg, "ai-tool", "claude\n")
	// A real, migration-free Claude settings file: the pre-fix code spawned
	// python3 for the hook migration whenever this file merely EXISTED, so
	// without it the python3 count below could not catch that regression.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	writeTempFile(t, claudeDir, "settings.json", `{"model":"opus"}`+"\n")
	writeTempFile(t, cfg, "last-update-check", strconv.FormatInt(time.Now().Unix(), 10)+"\n")
	// Skip the interactive picker: a seeded chain-ticketed restore entry
	// drives the wrapper straight through the post-pick path to tmux.
	writeTempFile(t, cfg, "restore-queue", "12345|"+proj+"|claude|||\n")
	writeTempFile(t, cfg, "last-restore-boot", "12345\n")
	seedChainTicket(t, cfg)

	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir spybin: %v", err)
	}
	calls := filepath.Join(home, "calls.log")

	for _, name := range expensiveCommands {
		if name == "python3" {
			continue
		}
		writeExecutable(t, filepath.Join(bin, name), "#!/bin/bash\n"+
			"echo \""+name+" $*\" >> "+strconv.Quote(calls)+"\n"+
			"sleep "+strconv.Itoa(int(expensiveCommandDelay.Seconds()))+"\n"+
			"exit 0\n")
	}
	// python3: fast but counted — exactly one spawn (the launch-settings
	// overlay) is legitimate per Claude launch.
	writeExecutable(t, filepath.Join(bin, "python3"), "#!/bin/bash\n"+
		"echo \"python3 $*\" >> "+strconv.Quote(calls)+"\n"+
		"exit 0\n")
	writeExecutable(t, filepath.Join(bin, "sysctl"), "#!/bin/bash\n"+
		"echo \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n")
	writeExecutable(t, filepath.Join(bin, "tmux"), "#!/bin/bash\n"+
		"if [ \"$1\" = \"new-session\" ]; then echo \"tmux-new-session\" >> "+strconv.Quote(calls)+"; fi\n"+
		"exit 0\n")
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/bash\nexit 0\n")
	writeExecutable(t, filepath.Join(bin, "wisp-deck-tui"), "#!/bin/bash\n"+
		"echo \"wisp-deck-tui $*\" >> "+strconv.Quote(calls)+"\n"+
		"exit 0\n")

	env := buildEnv(t, []string{bin},
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
	assertSpiesAreReachable(t, env, bin)

	out := filepath.Join(home, "wrapper.out")
	elapsed, exit := runScriptTimed(t, filepath.Join(root, "wrapper.sh"), env, out)

	logged, _ := os.ReadFile(calls)
	if !strings.Contains(string(logged), "tmux-new-session") {
		body, _ := os.ReadFile(out)
		t.Fatalf("wrapper.sh never issued tmux new-session (exit %d) — this guard "+
			"proves nothing unless the post-pick path actually ran.\ncalls:\n%s\noutput:\n%s",
			exit, logged, truncate(string(body), 2000))
	}

	if elapsed > criticalPathBudget {
		t.Errorf("wrapper.sh took %s from launch to tmux new-session (budget %s).\n\n"+
			"Something on the post-pick launch path is BLOCKING on a subprocess. "+
			"These expensive commands were invoked:\n%s",
			elapsed.Round(time.Millisecond), criticalPathBudget,
			indent(blockingCalls(string(logged))))
	}

	pythonSpawns := strings.Count(string(logged), "python3 ")
	if pythonSpawns > 1 {
		t.Errorf("the launch spawned python3 %d times; only the Claude launch-settings "+
			"overlay may spawn it (once). A one-time migration is back on the hot path — "+
			"gate it behind a cheap bash check (see remove_waiting_indicator_hooks).",
			pythonSpawns)
	}
}
