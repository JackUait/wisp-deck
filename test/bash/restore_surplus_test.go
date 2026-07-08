package bash_test

// After a macOS crash, TWO restore mechanisms run at once: macOS resume
// relaunches Ghostty with its saved windows (each re-running the wrapper),
// AND wisp-deck's own restore chain spawns one tab per queue entry. Every
// resumed window pops an entry and also advances the chain, so total
// interactive launches exceed the queue length — the surplus launches popped
// an empty queue and fell through to the project picker, littering the
// window with "main page" tabs. These tests pin the fix: a launch that took
// part in a restore drain but got no entry must close itself instead of
// showing the picker; only the queue builder (the user's own window) may
// fall back to the picker.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- restore_queue_pop drain marker ---

func TestRestoreQueuePop_writes_drain_marker_on_last_entry(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "222|/p/app|claude\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(filepath.Join(dir, "restore-drained-at"))
	if err != nil {
		t.Fatalf("drain marker not written: %v", err)
	}
	stamp := strings.TrimSpace(string(data))
	if stamp == "" || strings.ContainsFunc(stamp, func(r rune) bool { return r < '0' || r > '9' }) {
		t.Errorf("drain marker must be a numeric epoch, got %q", stamp)
	}
}

func TestRestoreQueuePop_no_drain_marker_while_entries_remain(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-drained-at")); err == nil {
		t.Error("drain marker must only be written when the queue is exhausted")
	}
}

func TestRestoreQueuePop_no_drain_marker_on_boot_mismatch_discard(t *testing.T) {
	// Discarding a previous boot's queue is not a drain — a fresh marker here
	// would make the next launch close itself instead of showing the picker.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "boot-old|/p/app|claude\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "boot-new"}, nil)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-drained-at")); err == nil {
		t.Error("drain marker must not be written when discarding a stale-boot queue")
	}
}

// --- restore_queue_active ---

func TestRestoreQueueActive_true_for_fresh_current_boot_queue(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "boot-1|/p/app|claude\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_active",
		[]string{dir, "boot-1"}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreQueueActive_false_when_no_queue(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_active",
		[]string{dir, "boot-1"}, nil)
	if code == 0 {
		t.Error("restore_queue_active must fail when no queue exists")
	}
}

func TestRestoreQueueActive_false_for_other_boot_queue(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "boot-old|/p/app|claude\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_active",
		[]string{dir, "boot-new"}, nil)
	if code == 0 {
		t.Error("a previous boot's queue must not mark this launch a participant")
	}
}

func TestRestoreQueueActive_false_for_stale_queue(t *testing.T) {
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue", "boot-1|/p/app|claude\n")
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(q, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_active",
		[]string{dir, "boot-1"}, nil)
	if code == 0 {
		t.Error("a stale queue (broken chain) must not mark this launch a participant")
	}
}

// --- restore_surplus_launch ---

func writeDrainMarker(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	epoch := strconv.FormatInt(time.Now().Add(-age).Unix(), 10)
	writeTempFile(t, dir, "restore-drained-at", epoch+"\n")
}

func TestRestoreSurplusLaunch_participant_with_empty_pop_is_surplus(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "1", "0"}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreSurplusLaunch_fresh_drain_marker_is_surplus(t *testing.T) {
	dir := t.TempDir()
	writeDrainMarker(t, dir, 3*time.Second)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "0", "0"}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreSurplusLaunch_builder_is_never_surplus(t *testing.T) {
	// The queue builder is the user's own window — it must keep its picker
	// fallback even when every queued entry was skipped (deleted projects).
	dir := t.TempDir()
	writeDrainMarker(t, dir, 3*time.Second)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "1", "1"}, nil)
	if code == 0 {
		t.Error("the queue builder must never be treated as surplus")
	}
}

func TestRestoreSurplusLaunch_no_signals_is_not_surplus(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "0", "0"}, nil)
	if code == 0 {
		t.Error("a plain launch (no queue seen, no recent drain) must show the picker")
	}
}

func TestRestoreSurplusLaunch_old_drain_marker_is_not_surplus(t *testing.T) {
	dir := t.TempDir()
	writeDrainMarker(t, dir, 2*time.Minute)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "0", "0"}, nil)
	if code == 0 {
		t.Error("a long-past drain must not close a user's fresh launch")
	}
}

func TestRestoreSurplusLaunch_garbage_marker_is_not_surplus(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-drained-at", "not-a-number\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "0", "0"}, nil)
	if code == 0 {
		t.Error("a corrupt drain marker must not close the launch")
	}
}

// --- maybe_restore_session builder flag ---

func TestMaybeRestore_sets_builder_flag_when_queue_built(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "boot-old|app|/p/app|opencode|ghostty|||\n")
	root := projectRoot(t)
	script := `
source ` + quote(filepath.Join(root, "lib", "session-restore.sh")) + `
maybe_restore_session ` + quote(dir) + ` boot-new
echo "builder=${WISP_DECK_RESTORE_BUILDER:-0}"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "builder=1")
}

func TestMaybeRestore_no_builder_flag_when_gate_closed(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "boot-old|app|/p/app|opencode|ghostty|||\n")
	writeTempFile(t, dir, "last-restore-boot", "boot-new\n")
	root := projectRoot(t)
	script := `
source ` + quote(filepath.Join(root, "lib", "session-restore.sh")) + `
maybe_restore_session ` + quote(dir) + ` boot-new
echo "builder=${WISP_DECK_RESTORE_BUILDER:-0}"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "builder=0")
}

// --- wrapper-level behavior ---

// wrapperHomeWithMocks builds a temp HOME with the wrapper's required mocks
// in ~/.local/bin (wrapper.sh line 2 puts $HOME/.local/bin first in PATH) and
// an empty wisp-deck config dir. The wisp-deck-tui mock records every
// invocation so tests can assert whether the picker was reached.
func wrapperHomeWithMocks(t *testing.T) (home, confDir, tuiRec string) {
	t.Helper()
	home = t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	tuiRec = filepath.Join(home, "tui-rec")
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$GT_TUI_REC\"\nexit 1\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	confDir = filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	return home, confDir, tuiRec
}

func TestWrapperSurplus_launch_closes_after_recent_drain(t *testing.T) {
	// Crash storm: this launch pops an empty queue moments after the restore
	// chain drained it. It must exit quietly — no picker, no tmux session.
	home, confDir, tuiRec := wrapperHomeWithMocks(t)
	// Restore gate already ran this boot; queue fully drained seconds ago.
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "restore-drained-at"),
		[]byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0644); err != nil {
		t.Fatalf("write drain marker: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_TUI_REC="+tuiRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	if data, err := os.ReadFile(tuiRec); err == nil && strings.Contains(string(data), "main-menu") {
		t.Errorf("surplus launch must not show the picker; tui calls:\n%s", data)
	}
}

func TestWrapperInteractive_shows_picker_when_no_restore_signals(t *testing.T) {
	// A normal interactive launch (no queue, no recent drain) keeps today's
	// behavior: the project picker opens.
	home, confDir, tuiRec := wrapperHomeWithMocks(t)
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_TUI_REC="+tuiRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(tuiRec)
	if err != nil || !strings.Contains(string(data), "main-menu") {
		t.Errorf("normal launch must reach the picker; tui calls: %q, err=%v", string(data), err)
	}
}

func TestWrapperBuilder_falls_back_to_picker_when_all_entries_skipped(t *testing.T) {
	// The launch that BUILDS the queue is the user's own window. When every
	// queued entry is unwanted (project dirs deleted), it drains the queue
	// itself and must still fall back to the picker, not close.
	home, confDir, tuiRec := wrapperHomeWithMocks(t)
	// Prior-boot snapshot pointing at projects that no longer exist. Boot id
	// "99999" differs from the mocked current boottime (12345) by more than
	// the drift window, so the entries are queued.
	snap := "99999|gone|" + filepath.Join(home, "gone-a") + "|opencode|ghostty|||\n" +
		"99999|gone2|" + filepath.Join(home, "gone-b") + "|opencode|ghostty|||\n"
	if err := os.WriteFile(filepath.Join(confDir, "last-session"), []byte(snap), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_TUI_REC="+tuiRec)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(tuiRec)
	if err != nil || !strings.Contains(string(data), "main-menu") {
		t.Errorf("queue builder must keep its picker fallback; tui calls: %q, err=%v", string(data), err)
	}
}

// --- account-switch fresh launch must ignore stale restore env ---

func TestBuildSwitchLaunchCmd_fresh_launch_ignores_inherited_resume_env(t *testing.T) {
	// Every pane of a restored tab inherits the wrapper's WISP_DECK_RESUME /
	// WISP_DECK_RESUME_SESSION exports. A mid-session account switch with no
	// stamped session must launch a FRESH claude — resuming the stale
	// launch-time sid would reopen a conversation that was never this pane's.
	env := buildEnv(t, nil,
		"WISP_DECK_RESUME=1",
		"WISP_DECK_RESUME_SESSION=dead-beef-sid")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude opencode "/cfg/settings.json" "" "/proj" "/cfg/claude-accounts/work"`), env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "--resume")
	assertNotContains(t, out, "claude -c")
}

func TestBuildAILaunchCmd_default_login_sheds_inherited_config_dir(t *testing.T) {
	// "Default" must MEAN the Keychain login. If the shell that started the
	// tmux server had CLAUDE_CONFIG_DIR exported (e.g. a wrapper launched from
	// inside another claude session), a Default launch built without an
	// explicit override would silently inherit that managed login. The built
	// command must actively shed the variable.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "claude.log")
	binDir := mockCommand(t, dir, "claude",
		`echo "CFG=[${CLAUDE_CONFIG_DIR:-UNSET}]" >> "$GT_CLAUDE_LOG"`)
	env := buildEnv(t, []string{binDir},
		"GT_CLAUDE_LOG="+logFile,
		"CLAUDE_CONFIG_DIR=/leaked/personal",
		"WISP_DECK_CLAUDE_ACCOUNT_DIR=")
	root := projectRoot(t)
	script := `
source ` + quote(filepath.Join(root, "lib", "tmux-session.sh")) + `
cmd="$(build_ai_launch_cmd claude claude opencode "")"
eval "$cmd"
`
	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("mock claude never ran: %v", err)
	}
	assertContains(t, string(data), "CFG=[UNSET]")
}

func TestRestoreSurplusLaunch_started_before_drain_is_surplus_even_when_slow(t *testing.T) {
	// A storm tab whose init stalls (e.g. a slow update check) can pop empty
	// long after the 15s post-drain window. Its launch time is the precise
	// signal: it STARTED while the restore was still draining, so it belongs
	// to the storm and must close.
	dir := t.TempDir()
	writeDrainMarker(t, dir, time.Minute)
	launchEpoch := strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "0", "0", launchEpoch}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreSurplusLaunch_started_after_drain_is_not_surplus(t *testing.T) {
	// A window the user opens after the restore finished must keep the picker.
	dir := t.TempDir()
	writeDrainMarker(t, dir, time.Minute)
	launchEpoch := strconv.FormatInt(time.Now().Add(-30*time.Second).Unix(), 10)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "0", "0", launchEpoch}, nil)
	if code == 0 {
		t.Error("a launch begun after the drain must not be treated as surplus")
	}
}
