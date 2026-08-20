package bash_test

// A restore chain that dies mid-drain used to lock the user out of the
// terminal. Observed: the boot's first tab popped entry 1 of 11, called
// restore_advance, and the Cmd+T it fired never produced a tab (osascript
// still exits 0 when the keystroke lands on whatever app is frontmost during
// a boot-time restore). The queue kept its 10 entries, so for the next five
// minutes restore_queue_active still reported a drain "in progress" and every
// terminal the user opened was closed as surplus — Ghostty renders any exit
// inside abnormal-command-exit-runtime (250ms) as "failed to launch the
// requested command".
//
// These tests pin both halves of the fix: a stalled chain must be detected so
// the spawner can revive it, and a launch that merely saw a DEAD queue must
// fall open to the picker instead of closing.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stampFile writes an epoch marker aged by the given duration.
func stampFile(t *testing.T, dir, name string, age time.Duration) {
	t.Helper()
	writeTempFile(t, dir, name,
		strconv.FormatInt(time.Now().Add(-age).Unix(), 10)+"\n")
}

// ageFile backdates a file's mtime, the signal restore_chain_alive reads to
// tell a draining queue from an abandoned one.
func ageFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

// --- restore_chain_alive ---

func TestRestoreChainAlive_true_while_queue_is_being_popped(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "boot-1|/p/app|claude\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_chain_alive",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreChainAlive_true_while_a_fresh_ticket_is_outstanding(t *testing.T) {
	// The spawned tab has not claimed its ticket yet — the chain is in flight.
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue", "boot-1|/p/app|claude\n")
	ageFile(t, q, 4*time.Minute)
	stampFile(t, dir, "restore-chain-ticket", 5*time.Second)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_chain_alive",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreChainAlive_true_just_after_the_drain_finished(t *testing.T) {
	dir := t.TempDir()
	stampFile(t, dir, "restore-drained-at", 3*time.Second)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_chain_alive",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
}

func TestRestoreChainAlive_false_when_the_chain_stalled(t *testing.T) {
	// The exact production state: entries left over, last pop minutes ago,
	// ticket swept, drain marker from a previous boot.
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue",
		"boot-1|/p/app|claude\nboot-1|/p/web|claude\n")
	ageFile(t, q, 3*time.Minute)
	stampFile(t, dir, "restore-drained-at", 400*time.Hour)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_chain_alive",
		[]string{dir}, nil)
	if code == 0 {
		t.Error("a queue nobody has popped for minutes is a dead chain")
	}
}

func TestRestoreChainAlive_false_when_ticket_is_past_its_claim_window(t *testing.T) {
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue", "boot-1|/p/app|claude\n")
	ageFile(t, q, 4*time.Minute)
	stampFile(t, dir, "restore-chain-ticket", 5*time.Minute)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_chain_alive",
		[]string{dir}, nil)
	if code == 0 {
		t.Error("a ticket older than its 60s claim window proves the chain broke")
	}
}

func TestRestoreChainAlive_false_on_empty_config_dir(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_chain_alive",
		[]string{dir}, nil)
	if code == 0 {
		t.Error("no queue, no ticket and no drain marker is not a live chain")
	}
}

// --- restore_surplus_launch gated on chain liveness ---

func TestRestoreSurplusLaunch_participant_of_a_dead_chain_is_not_surplus(t *testing.T) {
	// THE LOCKOUT. participant=1 only records that a queue existed when this
	// launch started. When the chain behind it is dead, closing the tab denies
	// the user a terminal; the picker is the safe fallback.
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue",
		"boot-1|/p/app|claude\nboot-1|/p/web|claude\n")
	ageFile(t, q, 3*time.Minute)
	stampFile(t, dir, "restore-queue-built-at", 3*time.Minute)
	stampFile(t, dir, "restore-drained-at", 400*time.Hour)
	launch := strconv.FormatInt(time.Now().Unix(), 10)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "1", "0", launch}, nil)
	if code == 0 {
		t.Error("a launch that saw a DEAD queue must fall back to the picker, not close")
	}
}

func TestRestoreSurplusLaunch_participant_of_a_live_drain_is_surplus(t *testing.T) {
	// The crash-storm case this gate exists for must keep working: the queue
	// is still being popped, so this extra launch is genuinely surplus.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "boot-1|/p/app|claude\n")
	launch := strconv.FormatInt(time.Now().Unix(), 10)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_surplus_launch",
		[]string{dir, "1", "0", launch}, nil)
	assertExitCode(t, code, 0)
}

// --- restore_advance verifies the tab it spawned actually started ---

// runRestoreAdvanceWaiting runs restore_advance with stubbed spawn hooks and a
// short chain-tab wait. claimTicket makes the stubbed Cmd+T behave like a real
// tab: it claims (removes) the ticket restore_advance issued.
func runRestoreAdvanceWaiting(t *testing.T, configDir, trigFile, winFile string, trigExit int, claimTicket bool) (string, int) {
	t.Helper()
	mod := filepath.Join(projectRoot(t), "lib", "session-restore.sh")
	claim := ""
	if claimTicket {
		claim = "rm -f " + quote(filepath.Join(configDir, "restore-chain-ticket")) + "; "
	}
	script := `
source ` + quote(mod) + `
restore_trigger_tab() { echo triggered >> ` + quote(trigFile) + `; ` + claim + `return ` + strconv.Itoa(trigExit) + `; }
terminal_launch_window() { echo window >> ` + quote(winFile) + `; }
restore_advance ` + quote(configDir) + `
`
	env := buildEnv(t, nil, "WISP_DECK_CHAIN_TAB_WAIT=1")
	return runBashSnippet(t, script, env)
}

func TestRestoreAdvance_falls_back_to_window_when_the_tab_never_starts(t *testing.T) {
	// osascript exits 0 even when the keystroke goes to another app, so a
	// zero exit is not proof of a tab. The unclaimed ticket is: nothing
	// started, so the chain must be revived with a plain window.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvanceWaiting(t, dir, trig, win, 0, false)
	assertExitCode(t, code, 0)
	data := waitForFile(t, win, "a lost Cmd+T must fall back to a window")
	if got := strings.Count(data, "window"); got != 1 {
		t.Errorf("spawned %d windows, want exactly 1", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err != nil {
		t.Error("queue must survive for the fallback window to pop")
	}
	// The reviving window earns its pop by claiming a ticket, so a fresh one
	// must be outstanding — the original is near its 60s claim window.
	if _, err := os.Stat(filepath.Join(dir, "restore-chain-ticket")); err != nil {
		t.Error("the fallback window has no claimable ticket, so it cannot pop")
	}
}

// --- the surplus close must outlive Ghostty's abnormal-exit window ---

func TestWrapperSurplus_close_outlives_ghostty_abnormal_exit_window(t *testing.T) {
	// Ghostty's abnormal-command-exit-runtime (250ms) is checked against the
	// process runtime for ANY exit code, so a surplus close that returns
	// sooner paints "Ghostty failed to launch the requested command" over the
	// tab. Pinned at the source: the runtime cost is real but invisible to a
	// timing assertion, which the wrapper's own startup would satisfy anyway.
	src := repositorySource(t, "wrapper.sh")
	const marker = "surplus restore launch closed"
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("surplus close branch not found in wrapper.sh")
	}
	branch := src[start:]
	if end := strings.Index(branch, "exit 0"); end >= 0 {
		branch = branch[:end]
	} else {
		t.Fatal("surplus branch does not exit")
	}
	m := regexp.MustCompile(`sleep\s+([0-9.]+)`).FindStringSubmatch(branch)
	if m == nil {
		t.Fatalf("surplus close must delay past the 250ms abnormal-exit window, branch:\n%s", branch)
	}
	secs, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("unparsable sleep %q: %v", m[1], err)
	}
	if secs < 0.25 {
		t.Errorf("surplus close sleeps %.3fs, must exceed Ghostty's 0.25s threshold", secs)
	}
}

func TestRestoreAdvance_no_window_when_the_spawned_tab_claims_its_ticket(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvanceWaiting(t, dir, trig, win, 0, true)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(win); err == nil {
		t.Error("a tab that claimed its ticket started; no fallback window may be spawned")
	}
}
