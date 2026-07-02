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
	home := t.TempDir()
	// First line carries a Claude conversation id (6th field, backed by a
	// resumable transcript); second is an old-format 5-field line — its queue
	// entry gets an empty id.
	writeTranscript(t, home, "/p/app", "sid-a", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-a\n111|web|/p/web|opencode|ghostty\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-a\n222|/p/web|opencode|"
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
	want := "111|app|/p/app|claude|ghostty|"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_includes_claude_session_id(t *testing.T) {
	// The statusline stamps WISP_DECK_CLAUDE_SESSION into the tmux session
	// env; the snapshot must carry it (6th field) so restore can reopen each
	// tab's own conversation instead of `claude -c` (which resumes the same,
	// most recent one for every tab of a project).
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CLAUDE_SESSION=sid-42\n' ;;
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
	want := "111|app|/p/app|claude|ghostty|sid-42"
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

// runMaybeRestoreHome is runMaybeRestore with a HOME override so the
// transcript lookup under ~/.claude/projects/ can be faked.
func runMaybeRestoreHome(t *testing.T, configDir, curBoot, home string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + `
`
	return runBashSnippet(t, script, buildEnv(t, nil, "HOME="+home))
}

// writeTranscript creates a fake Claude conversation transcript for a project
// path (munged as Claude does: every non-alphanumeric byte becomes '-') with
// the given mtime age. The content includes a model turn ("type":"assistant")
// — that is what makes a real transcript resumable.
func writeTranscript(t *testing.T, home, projPath, sid string, age time.Duration) {
	t.Helper()
	writeTranscriptRaw(t, home, projPath, sid,
		"{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n", age)
}

// writeTranscriptRaw is writeTranscript with explicit file content, for
// faking transcripts that are NOT resumable (no model turn).
func writeTranscriptRaw(t *testing.T, home, projPath, sid, content string, age time.Duration) {
	t.Helper()
	munged := ""
	for _, r := range projPath {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			munged += string(r)
		} else {
			munged += "-"
		}
	}
	dir := filepath.Join(home, ".claude", "projects", munged)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	f := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	ts := time.Now().Add(-age)
	if err := os.Chtimes(f, ts, ts); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestMaybeRestore_assigns_distinct_transcripts_to_unstamped_duplicates(t *testing.T) {
	// Two tabs of the same project whose conversation ids were never stamped
	// (e.g. claude launched before the stamping update and sat idle). Plain
	// `-c` would open the SAME most-recent conversation in both. The queue
	// builder must pin each to a distinct recent transcript instead.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-old", 2*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-new\n222|/p/app|claude|sid-old"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_duplicate_fill_skips_stamped_sids(t *testing.T) {
	// One tab of the pair did stamp its id (the most recent transcript);
	// the unstamped one must get the NEXT transcript, not the same one.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-old", 2*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-new\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-new\n222|/p/app|claude|sid-old"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_single_unstamped_entry_keeps_c_fallback(t *testing.T) {
	// A lone tab of a project needs no pinning — `claude -c` already reopens
	// its most recent conversation, and guessing a transcript adds risk.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_duplicate_fill_survives_missing_transcript_dir(t *testing.T) {
	// No transcript store for the project (e.g. brand-new install): the
	// duplicates keep the empty id and both fall back to `claude -c`.
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|\n222|/p/app|claude|"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_blanks_stamped_sid_without_transcript(t *testing.T) {
	// The statusline stamps whatever session_id claude currently shows — for a
	// brand-new or just-/clear'd session no transcript exists yet, and
	// `claude --resume <id>` fails hard ("No conversation found") and exits to
	// a bare shell. A stamped id with no transcript on disk must be blanked so
	// the tab falls back to the safe `claude -c`.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-real", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-dead\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_blanks_stamped_sid_without_model_turn(t *testing.T) {
	// A transcript can exist yet be unresumable: claude marks sessions with no
	// model turn (no assistant reply yet) as non-resumable, and --resume fails
	// on them exactly like on a missing file. Such an id must be blanked too.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscriptRaw(t, home, "/p/app", "sid-empty",
		"{\"type\":\"user\"}\n", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-empty\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_keeps_stamped_sid_with_resumable_transcript(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-good", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-good\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-good"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_dead_stamped_duplicate_gets_pinned_distinct(t *testing.T) {
	// Two tabs of one project: one stamped with a dead id, one unstamped.
	// After blanking the dead id both are unstamped duplicates, so the pinning
	// logic must give each a distinct resumable transcript.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-old", 2*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-dead\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-new\n222|/p/app|claude|sid-old"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_backs_up_prior_snapshot(t *testing.T) {
	// The heartbeat rewrites last-session from currently-alive sessions soon
	// after restore starts, destroying the only pointers to the pre-reboot
	// tabs. Keep a copy so a broken restore chain stays recoverable.
	dir := t.TempDir()
	snapContent := "111|app|/p/app|claude|ghostty|sid-a\n"
	writeTempFile(t, dir, "last-session", snapContent)
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	prev, err := os.ReadFile(filepath.Join(dir, "last-session.prev"))
	if err != nil {
		t.Fatalf("last-session.prev not written: %v", err)
	}
	if string(prev) != snapContent {
		t.Errorf("backup = %q, want %q", string(prev), snapContent)
	}
}

func TestClaudePickTranscript_skips_transcript_without_model_turn(t *testing.T) {
	// The mtime-based pick must never pin a tab to an unresumable transcript —
	// `claude --resume` would fail and dump the tab to a bare shell.
	home := t.TempDir()
	writeTranscriptRaw(t, home, "/p/app", "sid-unresumable",
		"{\"type\":\"user\"}\n", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-resumable", 2*time.Hour)
	out, code := runBashFunc(t, "lib/session-restore.sh", "claude_pick_transcript",
		[]string{"/p/app", ""}, buildEnv(t, nil, "HOME="+home))
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "sid-resumable" {
		t.Errorf("picked %q, want %q", strings.TrimSpace(out), "sid-resumable")
	}
}

func TestWriteSessionSnapshot_noop_while_restore_queue_fresh(t *testing.T) {
	// While a restore chain is draining (fresh restore-queue), the heartbeat
	// must not rewrite the snapshot: the alive sessions are only the
	// restored-so-far subset, and overwriting would lose the rest.
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty|sid-a\n")
	writeTempFile(t, dir, "restore-queue", "222|/p/web|claude|sid-b\n")
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=222\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty|sid-a"
	if got != want {
		t.Errorf("snapshot rewritten during restore: got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_writes_after_restore_queue_stale(t *testing.T) {
	// A stale queue (>5 min — the chain broke) must not freeze the snapshot
	// forever; normal heartbeat snapshotting resumes.
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty|sid-a\n")
	queue := writeTempFile(t, dir, "restore-queue", "222|/p/web|claude|sid-b\n")
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(queue, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=222\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	got := strings.TrimSpace(string(data))
	want := "222|app|/p/app|claude|ghostty|"
	if got != want {
		t.Errorf("snapshot not rewritten after queue went stale: got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_removes_stale_tmp_files(t *testing.T) {
	// A heartbeat SIGKILL'd mid-write (e.g. at shutdown) leaves its
	// last-session.tmp.<pid> behind forever. The next snapshot write must
	// sweep such debris — but only stale files, never a fresh tmp that a
	// concurrent writer is about to mv into place.
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty|\n")
	staleTmp := writeTempFile(t, dir, "last-session.tmp.12345", "")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(staleTmp, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	freshTmp := writeTempFile(t, dir, "last-session.tmp.67890", "")
	// tmux server dead: the function returns before writing, but the sweep
	// must still have happened.
	binDir := mockCommand(t, dir, "tmux", `exit 1`)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(staleTmp); err == nil {
		t.Error("stale tmp file not removed")
	}
	if _, err := os.Stat(freshTmp); err != nil {
		t.Error("fresh tmp file must be kept (concurrent writer)")
	}
}

func TestRestoreQueuePop_pops_first_line_and_keeps_rest(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude|sid-a\n222|/p/web|opencode\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude|sid-a" {
		t.Errorf("pop = %q, want %q", strings.TrimSpace(out), "/p/app|claude|sid-a")
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
	want := "111|b|/p/b|claude|ghostty|\n111|a|/p/a|claude|ghostty|"
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
	want := "111|My Project|/p/app|claude|ghostty|"
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
