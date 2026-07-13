package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keepAwakeEnv builds an environment with a fake pmset (stateful, backed by a
// file) and a fake sudo (logs its argv, then execs the rest). WISP_DECK_PMSET
// points at the fake so the real kernel flag is never touched by tests.
//
// The fake pmset understands exactly the two invocations the module makes:
//
//	pmset -g                    -> prints " SleepDisabled  1" only when disabled
//	pmset -a disablesleep 0|1   -> records the new value
func keepAwakeEnv(t *testing.T, dir string) (env []string, stateFile, logFile string) {
	t.Helper()
	stateFile = filepath.Join(dir, "sleepdisabled")
	logFile = filepath.Join(dir, "sudo.log")

	binDir := mockCommand(t, dir, "pmset", `
state="$KA_STATE"
if [ "$1" = "-g" ]; then
  cur=0
  [ -f "$state" ] && cur="$(cat "$state")"
  echo " sleep                1"
  [ "$cur" = "1" ] && echo " SleepDisabled        1"
  exit 0
fi
if [ "$1" = "-a" ] && [ "$2" = "disablesleep" ]; then
  echo "$3" > "$state"
  exit 0
fi
exit 64
`)
	// Emulates real sudo with ONLY the wisp-deck sudoers rule installed:
	// the two disablesleep invocations run passwordless, `sudo -l <cmd>`
	// answers whether a command is granted, everything else is denied.
	// A permissive exec-anything mock is exactly what hid the bug where
	// the can_sudo probe used a command the rule does not grant.
	mockCommand(t, dir, "sudo", `
echo "sudo $*" >> "$KA_LOG"
[ "$1" = "-n" ] && shift
list=0
if [ "$1" = "-l" ]; then list=1; shift; fi
case "$2 $3 $4" in
  "-a disablesleep 0" | "-a disablesleep 1")
    [ "$list" = "1" ] && exit 0
    exec "$@"
    ;;
esac
echo "sudo: a password is required" >&2
exit 1
`)

	env = buildEnv(t, []string{binDir},
		"KA_STATE="+stateFile,
		"KA_LOG="+logFile,
		"WISP_DECK_PMSET="+filepath.Join(binDir, "pmset"),
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
	)
	return env, stateFile, logFile
}

func readFileTrim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func TestKeepAwakeEnabled_reads_setting(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantCode int
	}{
		{"on", "theme=auto\nkeep_awake=on\n", 0},
		{"off", "keep_awake=off\n", 1},
		{"absent", "theme=auto\n", 1},
		{"no file", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			sf := filepath.Join(dir, "settings")
			if tt.content != "" {
				writeTempFile(t, dir, "settings", tt.content)
			}
			_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_enabled", []string{sf}, nil)
			assertExitCode(t, code, tt.wantCode)
		})
	}
}

func TestKeepAwakeHold_sets_kernel_flag(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, logFile := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold",
		[]string{cfg, "sess-a", "1"}, env)
	assertExitCode(t, code, 0)

	if got := readFileTrim(t, stateFile); got != "1" {
		t.Errorf("SleepDisabled = %q, want \"1\"", got)
	}
	assertContains(t, readFileTrim(t, logFile), "disablesleep 1")

	// The holder is recorded so other sessions can see it.
	holder := filepath.Join(cfg, "keep-awake.d", "sess-a")
	if _, err := os.Stat(holder); err != nil {
		t.Errorf("expected holder file %s: %v", holder, err)
	}
}

func TestKeepAwakeDrop_clears_flag_when_last_holder_leaves(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, _ := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")

	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold", []string{cfg, "sess-a", "1"}, env)
	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_drop", []string{cfg, "sess-a"}, env)
	assertExitCode(t, code, 0)

	if got := readFileTrim(t, stateFile); got != "0" {
		t.Errorf("SleepDisabled = %q, want \"0\" after last holder dropped", got)
	}
}

func TestKeepAwakeDrop_keeps_flag_while_another_session_holds(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, _ := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")

	// Two live sessions, both busy. Use PID 1 (launchd) — always alive.
	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold", []string{cfg, "sess-a", "1"}, env)
	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold", []string{cfg, "sess-b", "1"}, env)

	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_drop", []string{cfg, "sess-a"}, env)

	if got := readFileTrim(t, stateFile); got != "1" {
		t.Errorf("SleepDisabled = %q, want \"1\" while sess-b still holds", got)
	}
}

// A session that crashed leaves its holder file behind. Nothing else may ever
// clear the flag, so the machine would never sleep again — the worst failure
// mode of this feature. Reaping must drop holders whose PID is gone.
func TestKeepAwakeSync_reaps_dead_holders(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, _ := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")

	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold", []string{cfg, "sess-a", "1"}, env)

	// Forge a holder owned by a PID that cannot exist.
	holders := filepath.Join(cfg, "keep-awake.d")
	writeTempFile(t, holders, "sess-dead", "999999\n")
	// And kill the only live holder.
	os.Remove(filepath.Join(holders, "sess-a"))

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_sync", []string{cfg}, env)
	assertExitCode(t, code, 0)

	if got := readFileTrim(t, stateFile); got != "0" {
		t.Errorf("SleepDisabled = %q, want \"0\" after dead holder reaped", got)
	}
	if _, err := os.Stat(filepath.Join(holders, "sess-dead")); !os.IsNotExist(err) {
		t.Error("expected dead holder file to be removed")
	}
}

// Sync runs on every 0.5s watcher tick. It must not shell out to sudo when the
// flag already matches the desired state, or every busy session sprays sudo
// calls twice a second.
func TestKeepAwakeSync_is_idempotent(t *testing.T) {
	dir := t.TempDir()
	env, _, logFile := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")

	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold", []string{cfg, "sess-a", "1"}, env)
	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_sync", []string{cfg}, env)
	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_sync", []string{cfg}, env)

	writes := strings.Count(readFileTrim(t, logFile), "disablesleep")
	if writes != 1 {
		t.Errorf("sudo pmset invoked %d times, want 1 (idempotent syncs must not re-set)", writes)
	}
}

// Without the sudoers rule, `sudo -n` fails. The feature must degrade quietly
// rather than block a session behind a password prompt.
func TestKeepAwakeHold_degrades_when_sudo_unavailable(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "pmset", `exit 0`)
	mockCommand(t, dir, "sudo", `echo "sudo: a password is required" >&2; exit 1`)
	env := buildEnv(t, []string{binDir},
		"WISP_DECK_PMSET="+filepath.Join(binDir, "pmset"),
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
	)
	cfg := filepath.Join(dir, "config")

	out, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_hold",
		[]string{cfg, "sess-a", "1"}, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "password")
}

func TestKeepAwakeCanSudo_reflects_sudoers_rule(t *testing.T) {
	t.Run("granted", func(t *testing.T) {
		dir := t.TempDir()
		env, _, _ := keepAwakeEnv(t, dir)
		_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_can_sudo", nil, env)
		assertExitCode(t, code, 0)
	})
	t.Run("denied", func(t *testing.T) {
		dir := t.TempDir()
		binDir := mockCommand(t, dir, "pmset", `exit 0`)
		mockCommand(t, dir, "sudo", `exit 1`)
		env := buildEnv(t, []string{binDir},
			"WISP_DECK_PMSET="+filepath.Join(binDir, "pmset"),
			"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
		)
		_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_can_sudo", nil, env)
		assertExitCode(t, code, 1)
	})
}

// Regression: the sudoers rule grants exactly two pmset invocations. The
// can_sudo probe must succeed with ONLY those granted — probing with any
// other command (e.g. `sudo -n pmset -g`) fails against the real rule, so
// every launch re-concluded "rule missing" and asked for the password again.
func TestKeepAwakeCanSudo_probe_uses_only_granted_commands(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, _ := keepAwakeEnv(t, dir)

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_can_sudo", nil, env)
	assertExitCode(t, code, 0)

	// The probe must not flip the kernel flag as a side effect.
	if got := readFileTrim(t, stateFile); got != "" {
		t.Errorf("can_sudo probe mutated SleepDisabled to %q; probing must be read-only", got)
	}
}

// keep_awake_tick is what the watcher calls every 0.5s. It maps the agent's
// state onto the holder file, and — critically — releases when the user turns
// the setting off mid-session, so a running session is never stuck holding it.
func TestKeepAwakeTick(t *testing.T) {
	tests := []struct {
		name     string
		setting  string
		state    string
		wantFlag string
	}{
		{"agent working, enabled", "keep_awake=on\n", "active", "1"},
		{"agent idle, enabled", "keep_awake=on\n", "waiting", "0"},
		{"agent working, disabled", "keep_awake=off\n", "active", "0"},
		{"agent working, unset", "theme=auto\n", "active", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			env, stateFile, _ := keepAwakeEnv(t, dir)
			cfg := filepath.Join(dir, "config")
			writeTempFile(t, cfg, "settings", tt.setting)

			_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_tick",
				[]string{cfg, "sess-a", "1", tt.state}, env)
			assertExitCode(t, code, 0)

			got := readFileTrim(t, stateFile)
			if got == "" {
				got = "0" // never written == flag never set
			}
			if got != tt.wantFlag {
				t.Errorf("SleepDisabled = %q, want %q", got, tt.wantFlag)
			}
		})
	}
}

// The watcher ticks twice a second in every session. When the feature is off
// and this session holds nothing, the tick must cost zero subprocesses — no
// pmset probe, no sudo.
func TestKeepAwakeTick_disabled_spawns_no_subprocesses(t *testing.T) {
	dir := t.TempDir()
	probeLog := filepath.Join(dir, "pmset.log")
	binDir := mockCommand(t, dir, "pmset", `echo "pmset $*" >> "$KA_PROBE_LOG"; exit 0`)
	mockCommand(t, dir, "sudo", `echo "sudo $*" >> "$KA_PROBE_LOG"; exit 1`)
	env := buildEnv(t, []string{binDir},
		"KA_PROBE_LOG="+probeLog,
		"WISP_DECK_PMSET="+filepath.Join(binDir, "pmset"),
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
	)
	cfg := filepath.Join(dir, "config")
	writeTempFile(t, cfg, "settings", "theme=auto\n")

	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_tick", []string{cfg, "sess-a", "1", "active"}, env)

	if got := readFileTrim(t, probeLog); got != "" {
		t.Errorf("disabled tick shelled out, want silence, got:\n%s", got)
	}
}

// Turning the setting off while the agent is mid-turn must release immediately,
// not wait for the agent to go idle.
func TestKeepAwakeTick_releases_when_setting_turned_off_mid_turn(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, _ := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")

	writeTempFile(t, cfg, "settings", "keep_awake=on\n")
	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_tick", []string{cfg, "sess-a", "1", "active"}, env)
	if got := readFileTrim(t, stateFile); got != "1" {
		t.Fatalf("precondition: SleepDisabled = %q, want \"1\"", got)
	}

	writeTempFile(t, cfg, "settings", "keep_awake=off\n")
	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_tick", []string{cfg, "sess-a", "1", "active"}, env)

	if got := readFileTrim(t, stateFile); got != "0" {
		t.Errorf("SleepDisabled = %q, want \"0\" after setting turned off mid-turn", got)
	}
}

// End-to-end through the semantic watcher tick: working and unknown keep the
// machine awake, while ready and attention release it. Pane text is irrelevant.
func TestTabTitleWatcher_holds_flag_while_agent_works(t *testing.T) {
	dir := t.TempDir()
	env, stateFile, _ := keepAwakeEnv(t, dir)
	cfg := filepath.Join(dir, "config")
	writeTempFile(t, cfg, "settings", "keep_awake=on\n")

	attentionRoot := filepath.Join(dir, "attention")
	if err := os.MkdirAll(attentionRoot, 0700); err != nil {
		t.Fatal(err)
	}
	generation := "generation.keep1"
	attentionState := writeAttentionState(t, attentionRoot, generation, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, attentionRoot, generation, "opencode", attentionState)
	binDir := mockCommand(t, dir, "faketmux", `
case "$1" in
  list-panes) printf '%%3\t1\n' ;;
	  *) exit 0 ;;
esac
`)
	env = append(env, "PATH="+binDir+":"+os.Getenv("PATH"))

	root := projectRoot(t)
	script := fmt.Sprintf(`
set -e
source %q
source %q
set_tab_title() { :; }
set_tab_title_waiting() { :; }
play_notification_sound() { :; }
publish_state() {
  printf '1\t%s\t%%s\t%%s\t%%s\n' "$1" "$2" "$3" > %q.tmp
  mv %q.tmp %q
}
attention_watcher_reset
publish_state 1 working -
attention_watcher_tick sess-x proj project %q %q %q
echo "working=$(cat "$KA_STATE" 2>/dev/null || echo none)"
publish_state 2 unknown -
attention_watcher_tick sess-x proj project %q %q %q
echo "unknown=$(cat "$KA_STATE" 2>/dev/null || echo none)"
publish_state 3 ready -
attention_watcher_tick sess-x proj project %q %q %q
echo "ready=$(cat "$KA_STATE" 2>/dev/null || echo none)"
publish_state 4 attention question
attention_watcher_tick sess-x proj project %q %q %q
echo "attention=$(cat "$KA_STATE" 2>/dev/null || echo none)"
`, filepath.Join(root, "lib", "keep-awake.sh"),
		filepath.Join(root, "lib", "tab-title-watcher.sh"),
		generation, attentionState, attentionState, attentionState,
		filepath.Join(binDir, "faketmux"), descriptor, cfg,
		filepath.Join(binDir, "faketmux"), descriptor, cfg,
		filepath.Join(binDir, "faketmux"), descriptor, cfg,
		filepath.Join(binDir, "faketmux"), descriptor, cfg)
	out, _ := runBashSnippet(t, script, env)

	assertContains(t, out, "working=1")
	assertContains(t, out, "unknown=1")
	assertContains(t, out, "ready=0")
	assertContains(t, out, "attention=0")
	_ = stateFile
}

// wrapper.sh must source the module and release on every exit path, and must
// sync at startup so a previous session that was SIGKILLed (no trap runs) has
// its stale holder reaped rather than pinning the machine awake forever.
func TestWrapper_releases_keep_awake_on_exit(t *testing.T) {
	root := projectRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if !strings.Contains(src, "keep-awake") {
		t.Error("wrapper.sh does not source lib/keep-awake.sh")
	}
	if !strings.Contains(src, "keep_awake_drop") {
		t.Error("wrapper.sh cleanup() must call keep_awake_drop, or a closed window leaves the flag set")
	}
	if !strings.Contains(src, "keep_awake_sync") {
		t.Error("wrapper.sh must call keep_awake_sync at startup to reap holders from SIGKILLed sessions")
	}
}

// sudoersInstallEnv mocks sudo (exec the rest) and visudo (accept or reject).
func sudoersInstallEnv(t *testing.T, dir string, visudoOK bool) (env []string, target string) {
	t.Helper()
	target = filepath.Join(dir, "sudoers.d", "wisp-deck")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	visudoBody := `exit 0`
	if !visudoOK {
		visudoBody = `echo ">>> syntax error near line 3" >&2; exit 1`
	}
	binDir := mockCommand(t, dir, "visudo", visudoBody)
	mockCommand(t, dir, "sudo", `[ "$1" = "-n" ] && shift
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`)
	env = buildEnv(t, []string{binDir},
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
		"WISP_DECK_VISUDO="+filepath.Join(binDir, "visudo"),
		"WISP_DECK_SUDOERS="+target,
	)
	return env, target
}

func TestKeepAwakeInstallSudoers_writes_validated_rule(t *testing.T) {
	dir := t.TempDir()
	env, target := sudoersInstallEnv(t, dir, true)

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_install_sudoers", []string{"alice"}, env)
	assertExitCode(t, code, 0)

	got := readFileTrim(t, target)
	assertContains(t, got, "alice ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 1")

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	// sudo refuses to read a sudoers.d file that is group- or world-writable.
	if perm := info.Mode().Perm(); perm != 0440 {
		t.Errorf("sudoers file mode = %o, want 0440", perm)
	}
}

// A sudoers file that fails validation must never reach /etc/sudoers.d — it
// would break sudo for every command on the machine.
func TestKeepAwakeInstallSudoers_refuses_invalid_rule(t *testing.T) {
	dir := t.TempDir()
	env, target := sudoersInstallEnv(t, dir, false)

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_install_sudoers", []string{"alice"}, env)
	if code == 0 {
		t.Error("expected non-zero exit when visudo rejects the rule")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("invalid sudoers rule was installed — this can lock the user out of sudo")
	}
}

// keep_awake_ensure_sudoers runs right after the Settings menu closes. It must
// prompt (install) only when the feature is on AND the rule is missing.
func TestKeepAwakeEnsureSudoers(t *testing.T) {
	tests := []struct {
		name        string
		setting     string
		alreadyOK   bool
		wantInstall bool
	}{
		{"enabled, rule missing", "keep_awake=on\n", false, true},
		{"enabled, rule present", "keep_awake=on\n", true, false},
		{"disabled", "keep_awake=off\n", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "sudoers.d", "wisp-deck")
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				t.Fatal(err)
			}
			// `sudo -n` succeeds only when the rule is already granted.
			denied := `[ "$1" = "-n" ] && exit 1
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`
			granted := `[ "$1" = "-n" ] && exit 0
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`
			body := denied
			if tt.alreadyOK {
				body = granted
			}
			binDir := mockCommand(t, dir, "visudo", `exit 0`)
			mockCommand(t, dir, "sudo", body)
			env := buildEnv(t, []string{binDir},
				"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
				"WISP_DECK_VISUDO="+filepath.Join(binDir, "visudo"),
				"WISP_DECK_SUDOERS="+target,
			)
			cfg := filepath.Join(dir, "config")
			writeTempFile(t, cfg, "settings", tt.setting)

			runBashFunc(t, "lib/keep-awake.sh", "keep_awake_ensure_sudoers", []string{cfg}, env)

			_, err := os.Stat(target)
			installed := err == nil
			if installed != tt.wantInstall {
				t.Errorf("installed = %v, want %v", installed, tt.wantInstall)
			}
		})
	}
}

// The password request is the first thing a user sees after enabling
// keep-awake. It must render as a proper bordered window on a cleared screen —
// explaining WHY root is needed, exactly WHAT the rule grants, and HOW to
// revoke it — not as raw text spilled over the splash.
func TestKeepAwakeEnsureSudoers_renders_explanation_window(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sudoers.d", "wisp-deck")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, dir, "visudo", `exit 0`)
	// `sudo -n` fails (rule missing); the interactive install call may carry a
	// custom `-p <prompt>` — skip it before exec'ing the real command.
	mockCommand(t, dir, "sudo", `[ "$1" = "-n" ] && exit 1
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`)
	env := buildEnv(t, []string{binDir},
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
		"WISP_DECK_VISUDO="+filepath.Join(binDir, "visudo"),
		"WISP_DECK_SUDOERS="+target,
	)
	cfg := filepath.Join(dir, "config")
	writeTempFile(t, cfg, "settings", "keep_awake=on\n")

	out, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_ensure_sudoers", []string{cfg}, env)
	assertExitCode(t, code, 0)

	// The splash is still on screen when this runs — it must clear first.
	assertContains(t, out, "\x1b[2J")
	// A real window: rounded box borders, not bare printf lines.
	assertContains(t, out, "╭")
	assertContains(t, out, "╰")
	// Why root is needed: lid-close sleep would stall a working agent.
	assertContains(t, out, "lid")
	assertContains(t, out, "sleep")
	// Exactly what is granted.
	assertContains(t, out, "pmset -a disablesleep")
	// How to revoke it.
	assertContains(t, out, "sudo rm /etc/sudoers.d/wisp-deck")
	// How to bail out without entering a password.
	assertContains(t, out, "Ctrl-C")
}

// assertBoxFlush fails when the framed window lines are not all the same
// width: a body line longer than the inner width pushes the right border out
// of column and shreds the frame.
func assertBoxFlush(t *testing.T, out string) {
	t.Helper()
	stripANSI := func(s string) string {
		var b strings.Builder
		inEsc := false
		for _, r := range s {
			switch {
			case inEsc:
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					inEsc = false
				}
			case r == '\x1b':
				inEsc = true
			default:
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	var widths []int
	for _, line := range strings.Split(stripANSI(out), "\n") {
		line = strings.TrimRight(line, " ")
		if strings.ContainsAny(line, "│╭╰") {
			widths = append(widths, len([]rune(strings.TrimLeft(line, " "))))
		}
	}
	if len(widths) < 5 {
		t.Fatalf("expected a multi-line box, got %d framed lines\noutput:\n%s", len(widths), out)
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("box line %d is %d columns wide, want %d — right border out of column", i, w, widths[0])
		}
	}
}

// Every line of the window body must fit the box: a line longer than the inner
// width would push the right border out of column and shred the frame.
func TestKeepAwakeEnsureSudoers_window_lines_are_flush(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sudoers.d", "wisp-deck")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, dir, "visudo", `exit 0`)
	mockCommand(t, dir, "sudo", `[ "$1" = "-n" ] && exit 1
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`)
	env := buildEnv(t, []string{binDir},
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
		"WISP_DECK_VISUDO="+filepath.Join(binDir, "visudo"),
		"WISP_DECK_SUDOERS="+target,
	)
	cfg := filepath.Join(dir, "config")
	writeTempFile(t, cfg, "settings", "keep_awake=on\n")

	out, _ := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_ensure_sudoers", []string{cfg}, env)
	assertBoxFlush(t, out)
}

// revokeEnv builds an environment where the sudoers rule IS installed: a real
// file at the target path, a stateful fake pmset, and a fake sudo that accepts
// -n / -p and execs the rest — so `sudo rm <target>` really removes the file.
func revokeEnv(t *testing.T, dir string) (env []string, target, stateFile string) {
	t.Helper()
	stateFile = filepath.Join(dir, "sleepdisabled")
	target = filepath.Join(dir, "sudoers.d", "wisp-deck")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("rule\n"), 0644); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, dir, "pmset", `
state="$KA_STATE"
if [ "$1" = "-g" ]; then
  cur=0
  [ -f "$state" ] && cur="$(cat "$state")"
  [ "$cur" = "1" ] && echo " SleepDisabled        1"
  exit 0
fi
if [ "$1" = "-a" ] && [ "$2" = "disablesleep" ]; then
  echo "$3" > "$state"
  exit 0
fi
exit 64
`)
	mockCommand(t, dir, "sudo", `[ "$1" = "-n" ] && shift
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`)
	env = buildEnv(t, []string{binDir},
		"WISP_DECK_PMSET="+filepath.Join(binDir, "pmset"),
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
		"WISP_DECK_SUDOERS="+target,
		"KA_STATE="+stateFile,
	)
	return env, target, stateFile
}

// Turning the toggle off is the in-app path to revoke the standing sudo rule:
// on an on→off flip with the rule still granted, a window must explain what is
// still installed and, on "y", clear the kernel flag and remove the rule.
func TestKeepAwakeOfferRevoke_removes_rule_on_yes(t *testing.T) {
	dir := t.TempDir()
	env, target, stateFile := revokeEnv(t, dir)
	if err := os.WriteFile(stateFile, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config")
	writeTempFile(t, cfg, "settings", "keep_awake=off\n")

	out, code := runBashFuncWithStdin(t, "lib/keep-awake.sh", "keep_awake_offer_revoke",
		[]string{cfg, "1"}, env, "y\n")
	assertExitCode(t, code, 0)

	// A real window that explains the decision.
	assertContains(t, out, "╭")
	assertContains(t, out, "pmset -a disablesleep")
	assertContains(t, out, "password")
	assertContains(t, out, "y/N")
	assertBoxFlush(t, out)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("sudoers rule still installed after user confirmed revoke")
	}
	// The kernel flag must be released too — revoking with SleepDisabled stuck
	// at 1 would leave a laptop that can never sleep and no rule to fix it.
	if got := readFileTrim(t, stateFile); got != "0" {
		t.Errorf("SleepDisabled = %q after revoke, want 0", got)
	}
}

// Anything except an explicit yes keeps the rule: this is standing root access
// the user consciously granted, so the default answer must be "keep it".
func TestKeepAwakeOfferRevoke_keeps_rule_unless_yes(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", ""} {
		t.Run(fmt.Sprintf("answer %q", answer), func(t *testing.T) {
			dir := t.TempDir()
			env, target, _ := revokeEnv(t, dir)
			cfg := filepath.Join(dir, "config")
			writeTempFile(t, cfg, "settings", "keep_awake=off\n")

			_, code := runBashFuncWithStdin(t, "lib/keep-awake.sh", "keep_awake_offer_revoke",
				[]string{cfg, "1"}, env, answer)
			assertExitCode(t, code, 0)

			if _, err := os.Stat(target); err != nil {
				t.Error("sudoers rule removed without an explicit yes")
			}
		})
	}
}

// The offer fires ONLY on the on→off flip with a rule to remove. Every launch
// passes through this call site, so any other combination must stay silent.
func TestKeepAwakeOfferRevoke_silent_when_not_flipped_off(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		wasOn   string
		granted bool
	}{
		{"was already off", "keep_awake=off\n", "0", true},
		{"still on", "keep_awake=on\n", "1", true},
		{"no rule installed", "keep_awake=off\n", "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			env, target, _ := revokeEnv(t, dir)
			if !tt.granted {
				// `sudo -n` denied — the rule is not actually installed.
				binDir := mockCommand(t, dir, "sudo-denied", `[ "$1" = "-n" ] && exit 1; exec "$@"`)
				env = append(env, "WISP_DECK_SUDO="+filepath.Join(binDir, "sudo-denied"))
			}
			cfg := filepath.Join(dir, "config")
			writeTempFile(t, cfg, "settings", tt.setting)

			out, code := runBashFuncWithStdin(t, "lib/keep-awake.sh", "keep_awake_offer_revoke",
				[]string{cfg, tt.wasOn}, env, "y\n")
			assertExitCode(t, code, 0)
			assertNotContains(t, out, "╭")
			assertNotContains(t, out, "\x1b[2J")
			if _, err := os.Stat(target); err != nil {
				t.Error("sudoers rule removed on a silent path")
			}
		})
	}
}

// With theme.sh loaded (as in the wrapper) but no ai-tool file yet, the window
// must not leak a "No such file or directory" from the tool lookup.
func TestKeepAwakePromptWindow_tolerates_missing_ai_tool_file(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(cfg, 0755); err != nil {
		t.Fatal(err)
	}
	root := projectRoot(t)
	out, code := runBashSnippet(t,
		"source "+root+"/lib/theme.sh && source "+root+"/lib/keep-awake.sh && keep_awake_prompt_window "+cfg, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "No such file")
	assertContains(t, out, "╭")
}

// When nothing needs installing there must be no window and no screen clear —
// this path runs on every single launch.
func TestKeepAwakeEnsureSudoers_no_window_when_silent(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		sudo    string
	}{
		{"feature off", "keep_awake=off\n", `[ "$1" = "-n" ] && exit 1; exec "$@"`},
		{"rule already granted", "keep_awake=on\n", `[ "$1" = "-n" ] && exit 0; exec "$@"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			binDir := mockCommand(t, dir, "sudo", tt.sudo)
			env := buildEnv(t, []string{binDir},
				"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
			)
			cfg := filepath.Join(dir, "config")
			writeTempFile(t, cfg, "settings", tt.setting)

			out, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_ensure_sudoers", []string{cfg}, env)
			assertExitCode(t, code, 0)
			assertNotContains(t, out, "╭")
			assertNotContains(t, out, "\x1b[2J")
		})
	}
}

// The interactive sudo call must carry a custom prompt so the password line
// under the window reads as part of it, not as sudo's bare default.
func TestKeepAwakeInstallSudoers_styles_the_password_prompt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sudoers.d", "wisp-deck")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(dir, "sudo-args")
	binDir := mockCommand(t, dir, "visudo", `exit 0`)
	mockCommand(t, dir, "sudo", `printf '%s\n' "$@" >> "`+argsLog+`"
while [ "$1" = "-p" ]; do shift 2; done
exec "$@"`)
	env := buildEnv(t, []string{binDir},
		"WISP_DECK_SUDO="+filepath.Join(binDir, "sudo"),
		"WISP_DECK_VISUDO="+filepath.Join(binDir, "visudo"),
		"WISP_DECK_SUDOERS="+target,
	)

	_, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_install_sudoers", []string{"alice"}, env)
	assertExitCode(t, code, 0)

	got := readFileTrim(t, argsLog)
	assertContains(t, got, "-p")
	assertContains(t, got, "Password")
}

// The sudoers content is what grants standing root. It must be exactly two
// fully-qualified pmset invocations — never a bare `pmset` (argument-free
// wildcard) and never NOPASSWD: ALL.
func TestKeepAwakeSudoersContent_is_narrowly_scoped(t *testing.T) {
	out, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_sudoers_content",
		[]string{"alice"}, nil)
	assertExitCode(t, code, 0)

	assertContains(t, out, "alice ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 0")
	assertContains(t, out, "/usr/bin/pmset -a disablesleep 1")
	assertNotContains(t, out, "NOPASSWD: ALL")

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if !strings.Contains(line, "/usr/bin/pmset -a disablesleep") {
			t.Errorf("unexpected sudoers directive grants more than pmset: %q", line)
		}
	}
}

// --- reap must never write to the terminal ---
//
// Every live session's tab-title watcher ticks keep_awake_reap twice a second,
// and reap walks the WHOLE holders directory — not just its own entry. So N
// sessions means N reapers racing over one shared directory: reaper A globs a
// holder and passes the -e check, reaper B rm's it (dead PID, or the owning
// session dropping it on exit), and A's read then opens a path that is gone.
//
// That is a normal, expected outcome of the race — reap's whole job is to
// remove those files, and someone else already did it. It must be a silent
// no-op. It was not: the failing open printed
//
//	keep-awake.sh: line 79: .../keep-awake.d/<session>: No such file or directory
//
// to the watcher's inherited stderr, which is the session TTY that the AI
// tool's full-screen UI is drawing on, so the text landed in Claude's input box.
// (The trailing 2>/dev/null did not catch it: bash applies redirections
// left-to-right, so the input redirect fails before stderr is rerouted.)
func TestKeepAwakeReap_is_silent_when_a_holder_vanishes_mid_reap(t *testing.T) {
	dir := t.TempDir()
	env, _, _ := keepAwakeEnv(t, dir)
	holders := filepath.Join(dir, "keep-awake.d")
	if err := os.MkdirAll(holders, 0755); err != nil {
		t.Fatal(err)
	}

	// Many holders, all owned by a PID that cannot exist, so every reaper below
	// wants to delete every one of them — maximizing the window in which one
	// reaper's rm lands between another's glob and its read. 80 is comfortably
	// above the count that reproduced the leak on every trial (60).
	for i := 0; i < 80; i++ {
		writeTempFile(t, holders, fmt.Sprintf("proj-%d", i), "2147483646\n")
	}

	root := projectRoot(t)
	// Two concurrent reapers == two live wisp-deck sessions ticking at once.
	snippet := fmt.Sprintf(
		`source %q
reap() { keep_awake_reap %q; }
reap & reap & wait`,
		filepath.Join(root, "lib", "keep-awake.sh"), dir)

	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	// Any output at all is a leak: reap is a background housekeeping call that
	// shares a terminal with a TUI, so it must say nothing, ever.
	if strings.TrimSpace(out) != "" {
		t.Errorf("reap leaked output to the terminal during a concurrent reap:\n%s", out)
	}

	// And it still did the job: every dead holder is gone.
	left, err := os.ReadDir(holders)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("expected all dead holders reaped, %d left", len(left))
	}
}

// The same unsuppressed-open bug, pinned deterministically rather than by race:
// a holder that exists (so -e passes) but cannot be opened must be reaped in
// silence, not announced on the terminal.
func TestKeepAwakeReap_is_silent_when_a_holder_cannot_be_read(t *testing.T) {
	dir := t.TempDir()
	env, _, _ := keepAwakeEnv(t, dir)
	holders := filepath.Join(dir, "keep-awake.d")
	if err := os.MkdirAll(holders, 0755); err != nil {
		t.Fatal(err)
	}
	unreadable := writeTempFile(t, holders, "dev-locked-1234", "4242\n")
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0644) })

	out, code := runBashFunc(t, "lib/keep-awake.sh", "keep_awake_reap", []string{dir}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("reap leaked output for an unreadable holder:\n%s", out)
	}
}
