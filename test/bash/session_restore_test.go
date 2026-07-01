package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func quote(s string) string { return "\"" + s + "\"" }

func TestCurrentBootId_parses_sec_value(t *testing.T) {
	dir := t.TempDir()
	// macOS sysctl prints: { sec = 1700000000, usec = 123456 } Thu ...
	binDir := mockCommand(t, dir, "sysctl", `echo "{ sec = 1700000000, usec = 123456 } Thu Jan  1 00:00:00 2024"`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "1700000000" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "1700000000")
	}
}

func TestCurrentBootId_empty_when_sysctl_fails(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "sysctl", `exit 1`)
	env := buildEnv(t, []string{binDir})
	out, _ := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

// referenced by later tasks; keep import of filepath/os used
var _ = filepath.Join
var _ = os.Environ

// Helper: run maybe_restore_session with a stub launch_restore_window that
// records every call (one line per spawn) to recFile. The new queue-based
// restore must never spawn windows from maybe_restore_session.
func runMaybeRestore(t *testing.T, configDir, curBoot, recFile string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
launch_restore_window() { echo "$1|$2|$3|$4" >> ` + quote(recFile) + `; }
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + ` "/w/wrapper.sh"
`
	return runBashSnippet(t, script, nil)
}

func TestMaybeRestore_writes_ordered_queue_and_marker(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty\n111|web|/p/web|opencode|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)

	// No windows spawned — restore now goes through the tab queue.
	if _, err := os.Stat(rec); err == nil {
		t.Error("maybe_restore_session must not spawn windows anymore")
	}
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude\n222|/p/web|opencode"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
	marker, _ := os.ReadFile(filepath.Join(dir, "last-restore-boot"))
	if strings.TrimSpace(string(marker)) != "222" {
		t.Errorf("marker = %q, want 222", string(marker))
	}
}

func TestMaybeRestore_noop_when_already_restored_this_boot(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	writeTempFile(t, dir, "last-restore-boot", "222\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when boot already restored")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("expected no queue when boot already restored")
	}
}

func TestMaybeRestore_noop_when_no_snapshot(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when snapshot missing")
	}
}

func TestMaybeRestore_skips_current_boot_lines(t *testing.T) {
	dir := t.TempDir()
	// All lines are from the current boot -> nothing to restore.
	writeTempFile(t, dir, "last-session", "222|app|/p/app|claude|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("expected no queue when all lines are current-boot")
	}
	// marker must NOT be written when nothing was restored
	if _, err := os.Stat(filepath.Join(dir, "last-restore-boot")); err == nil {
		t.Error("marker should not be written when nothing restored")
	}
}

func TestMaybeRestore_noop_when_claim_already_taken(t *testing.T) {
	// Two wrappers may start simultaneously at login. The claim file is the
	// atomic gate: if it already exists for this boot, a second caller must
	// not rebuild the queue (which would resurrect already-popped entries).
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	writeTempFile(t, dir, "last-restore-boot.222", "")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("expected no queue when claim for this boot already exists")
	}
}

func TestWriteSessionSnapshot_writes_ghost_sessions_only(t *testing.T) {
	dir := t.TempDir()
	// Mock tmux: list two sessions; only dev-app-1 carries WISP_DECK=1.
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1"; echo "200 other-sess" ;;
  show-environment)
    if [ "$3" = "dev-app-1" ]; then
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    else
      printf 'SOMEVAR=1\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_preserves_file_when_tmux_dead(t *testing.T) {
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	// tmux server is dead: list-sessions exits 1.
	tmuxBody := `
case "$1" in
  list-sessions) exit 1 ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot disappeared: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty"
	if got != want {
		t.Errorf("snapshot was wiped when tmux dead: got %q, want %q", got, want)
	}
}

func TestRestoreQueuePop_pops_first_line_and_keeps_rest(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude" {
		t.Errorf("pop = %q, want %q", strings.TrimSpace(out), "/p/app|claude")
	}
	rest, _ := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if strings.TrimSpace(string(rest)) != "222|/p/web|opencode" {
		t.Errorf("remaining queue = %q, want %q", strings.TrimSpace(string(rest)), "222|/p/web|opencode")
	}
}

func TestRestoreQueuePop_removes_file_after_last_entry(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "222|/p/app|claude\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude" {
		t.Errorf("pop = %q, want %q", strings.TrimSpace(out), "/p/app|claude")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("queue file should be removed after last entry is popped")
	}
}

func TestRestoreQueuePop_empty_when_no_queue(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

func TestRestoreQueuePop_discards_queue_on_boot_mismatch(t *testing.T) {
	// A queue left over from a previous boot must never be consumed.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "111|/p/app|claude\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty on boot mismatch, got %q", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("stale-boot queue should be deleted")
	}
}

func TestRestoreQueuePop_discards_stale_queue(t *testing.T) {
	// A broken chain must not hijack a tab the user opens much later.
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue", "222|/p/app|claude\n")
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(q, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty for stale queue, got %q", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("stale queue should be deleted")
	}
}

// Helper: run restore_advance with stubbed restore_trigger_tab and
// terminal_launch_window hooks recording to trigFile/winFile.
func runRestoreAdvance(t *testing.T, configDir, trigFile, winFile string, trigExit int) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
restore_trigger_tab() { echo triggered >> ` + quote(trigFile) + `; return ` + strconv.Itoa(trigExit) + `; }
terminal_launch_window() { echo window >> ` + quote(winFile) + `; }
restore_advance ` + quote(configDir) + `
`
	return runBashSnippet(t, script, nil)
}

func TestRestoreAdvance_triggers_one_tab_when_queue_nonempty(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 0)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(trig)
	if err != nil {
		t.Fatalf("restore_trigger_tab not called: %v", err)
	}
	if got := strings.Count(string(data), "triggered"); got != 1 {
		t.Errorf("trigger called %d times, want 1", got)
	}
	if _, err := os.Stat(win); err == nil {
		t.Error("no windows must be spawned when the tab trigger succeeds")
	}
	// Queue must stay intact for the next tab to pop.
	q, _ := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if !strings.Contains(string(q), "/p/app") || !strings.Contains(string(q), "/p/web") {
		t.Errorf("queue must be untouched by advance, got %q", string(q))
	}
}

func TestRestoreAdvance_noop_when_queue_missing(t *testing.T) {
	dir := t.TempDir()
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 0)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(trig); err == nil {
		t.Error("must not trigger a tab when queue is missing")
	}
	if _, err := os.Stat(win); err == nil {
		t.Error("must not spawn windows when queue is missing")
	}
}

func TestRestoreAdvance_falls_back_to_plain_windows_when_trigger_fails(t *testing.T) {
	// osascript needs the Accessibility permission; when it fails, restore
	// degrades to one plain window per remaining entry. The windows run the
	// wrapper via Ghostty's configured command and pop the queue themselves,
	// so the queue must survive.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 1)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(win)
	if err != nil {
		t.Fatalf("fallback windows not spawned: %v", err)
	}
	if got := strings.Count(string(data), "window"); got != 2 {
		t.Errorf("spawned %d windows, want 2 (one per queue entry)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err != nil {
		t.Error("queue must survive for fallback windows to pop")
	}
}

func TestRestoreTriggerTab_invokes_osascript_and_propagates_failure(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "osascript", `echo "$@" >> `+quote(rec)+`; exit 0`)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_trigger_tab", nil, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("osascript not invoked: %v", err)
	}
	if !strings.Contains(string(data), "keystroke") {
		t.Errorf("expected a keystroke script, got %q", string(data))
	}

	failDir := t.TempDir()
	failBin := mockCommand(t, failDir, "osascript", `exit 1`)
	failEnv := buildEnv(t, []string{failBin})
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_trigger_tab", nil, failEnv)
	if code == 0 {
		t.Error("restore_trigger_tab must propagate osascript failure")
	}
}

func TestWriteSessionSnapshot_orders_by_creation_time(t *testing.T) {
	// tmux list-sessions returns sessions alphabetically; the snapshot must
	// be ordered by creation time so restore reproduces the tab order.
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "200 dev-a-1"; echo "100 dev-b-2" ;;
  show-environment)
    if [ "$3" = "dev-a-1" ]; then
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=a\nWISP_DECK_PATH=/p/a\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    else
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=b\nWISP_DECK_PATH=/p/b\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|b|/p/b|claude|ghostty\n111|a|/p/a|claude|ghostty"
	if got != want {
		t.Errorf("snapshot order:\n got %q\nwant %q", got, want)
	}
}

func TestWriteSessionSnapshot_handles_session_name_with_spaces(t *testing.T) {
	dir := t.TempDir()
	// Session name contains a space: "dev-My Project-1".
	// The word-splitting bug in the old for loop would split this into two tokens.
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-My Project-1" ;;
  show-environment)
    if [ "$3" = "dev-My Project-1" ]; then
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=My Project\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    else
      printf 'SOMEVAR=1\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|My Project|/p/app|claude|ghostty"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_empty_when_no_ghost_sessions(t *testing.T) {
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 other-sess" ;;
  show-environment) printf 'SOMEVAR=1\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("expected empty snapshot, got %q", string(data))
	}
}
