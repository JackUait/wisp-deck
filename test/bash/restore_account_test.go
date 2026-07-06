package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests guard the root-cause fix for "the account changed by itself":
// a session's Claude login is per-session state (stamped into the tmux env as
// WISP_DECK_CLAUDE_ACCOUNT), but the automatic relaunch paths used to resolve
// the GLOBAL claude-account pointer instead — so session-restore after a
// reboot (and a relaunch racing another session's pointer write) silently
// flipped a session onto whatever login the pointer happened to name.
//
// The fix threads the per-session account through every relaunch:
//   snapshot (8th field) -> restore queue (6th field) -> wrapper launch,
// and makes the mid-session switch respawn under the popup's CHOICE rather
// than re-reading the mutable pointer.

// --- snapshot records the session's account -------------------------------

// snapshotTmuxMock builds a tmux mock whose show-environment output includes
// the given extra env lines (after the standard WISP_DECK fields).
func snapshotTmuxMock(t *testing.T, dir, extraEnv, layout string) string {
	t.Helper()
	return mockCommand(t, dir, "tmux", `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    printf '`+extraEnv+`' ;;
  display-message) echo "`+layout+`" ;;
esac
`)
}

func runSnapshot(t *testing.T, dir, binDir string) string {
	t.Helper()
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestWriteSessionSnapshot_records_session_account(t *testing.T) {
	// A session running a managed login (WISP_DECK_CLAUDE_ACCOUNT=personal)
	// must record that login as the snapshot's 8th field, so restore can bring
	// the session back under ITS login rather than the global pointer's.
	dir := t.TempDir()
	bin := snapshotTmuxMock(t, dir, `WISP_DECK_CLAUDE_ACCOUNT=personal\n`, sampleLayout)
	got := runSnapshot(t, dir, bin)
	want := "111|app|/p/app|claude|ghostty||" + sampleLayout + "|personal"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_stamped_default_records_default(t *testing.T) {
	// A session stamped as running the Default (Keychain) login sets the env
	// var to the EMPTY string. That is a positive statement ("this session
	// runs Default"), distinct from "unknown" — encode it as the literal
	// "default" so restore forces Default even when the pointer names a
	// managed login.
	dir := t.TempDir()
	bin := snapshotTmuxMock(t, dir, `WISP_DECK_CLAUDE_ACCOUNT=\n`, sampleLayout)
	got := runSnapshot(t, dir, bin)
	want := "111|app|/p/app|claude|ghostty||" + sampleLayout + "|default"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_unstamped_account_left_empty(t *testing.T) {
	// A pre-stamping session has no WISP_DECK_CLAUDE_ACCOUNT in its env: the
	// account is unknown, recorded as an empty field so restore falls back to
	// the pointer (the pre-fix behavior).
	dir := t.TempDir()
	bin := snapshotTmuxMock(t, dir, ``, sampleLayout)
	got := runSnapshot(t, dir, bin)
	want := "111|app|/p/app|claude|ghostty||" + sampleLayout + "|"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- the queue carries the account -----------------------------------------

func TestMaybeRestore_carries_account_into_queue(t *testing.T) {
	// The snapshot's account must ride through queue construction into the
	// queue entry as a trailing field, so the restoring wrapper can launch the
	// tab under the login the session actually ran.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-a", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-a|"+sampleLayout+"|personal\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-a|" + sampleLayout + "|personal"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_old_snapshot_without_account_still_queues(t *testing.T) {
	// A snapshot written before the account field existed must keep restoring:
	// its queue entry carries an empty account (pointer fallback at launch).
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-a", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-a|"+sampleLayout+"\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-a|" + sampleLayout + "|"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

// --- resolving a restored entry's account dir ------------------------------

func TestResolveRestoreClaudeAccountDir_named_account(t *testing.T) {
	dir := t.TempDir()
	acct := filepath.Join(dir, "claude-accounts", "personal")
	writeTempFile(t, acct, ".keep", "")
	pointer := filepath.Join(dir, "claude-account") // absent — must not matter
	out, code := runBashFunc(t, "lib/claude-accounts.sh", "resolve_restore_claude_account_dir",
		[]string{filepath.Join(dir, "claude-accounts"), pointer, "personal"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != acct {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), acct)
	}
}

func TestResolveRestoreClaudeAccountDir_default_beats_pointer(t *testing.T) {
	// The session ran the Default login. Even if the global pointer now names
	// a managed login, the restored tab must come back as Default.
	dir := t.TempDir()
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	pointer := writeTempFile(t, dir, "claude-account", "personal\n")
	out, code := runBashFunc(t, "lib/claude-accounts.sh", "resolve_restore_claude_account_dir",
		[]string{filepath.Join(dir, "claude-accounts"), pointer, "default"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("got %q, want empty (Default login)", strings.TrimSpace(out))
	}
}

func TestResolveRestoreClaudeAccountDir_unknown_falls_back_to_pointer(t *testing.T) {
	// Old snapshot, no account recorded: keep the pre-fix behavior (the
	// pointer decides).
	dir := t.TempDir()
	acct := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acct, ".keep", "")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	out, code := runBashFunc(t, "lib/claude-accounts.sh", "resolve_restore_claude_account_dir",
		[]string{filepath.Join(dir, "claude-accounts"), pointer, ""}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != acct {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), acct)
	}
}

func TestResolveRestoreClaudeAccountDir_removed_account_degrades_to_default(t *testing.T) {
	// The recorded login was deleted since the snapshot: its dir is gone, so
	// the tab launches as Default rather than under an unrelated login.
	dir := t.TempDir()
	pointer := writeTempFile(t, dir, "claude-account", "personal\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	out, code := runBashFunc(t, "lib/claude-accounts.sh", "resolve_restore_claude_account_dir",
		[]string{filepath.Join(dir, "claude-accounts"), pointer, "gone"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("got %q, want empty (account removed -> Default)", strings.TrimSpace(out))
	}
}

// --- wrapper wiring ---------------------------------------------------------

func TestWrapper_restore_resolves_account_from_queue_entry(t *testing.T) {
	// The restore branch of wrapper.sh must consume the queue entry's account
	// field via resolve_restore_claude_account_dir instead of resolving the
	// global pointer. A static wiring guard (same class as the
	// source-integrity tests): losing either piece reintroduces the
	// account-flip-on-restore bug.
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "_q_acct") {
		t.Error("wrapper.sh does not parse the queue entry's account field (_q_acct)")
	}
	if !strings.Contains(src, "resolve_restore_claude_account_dir") {
		t.Error("wrapper.sh does not resolve the restored account via resolve_restore_claude_account_dir")
	}
}

// --- the mid-session switch must respawn under the CHOICE ------------------

// relaunchCtx writes a relaunch-context file whose account files live in dir.
func relaunchCtx(t *testing.T, dir string) string {
	t.Helper()
	return writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "claude_cmd=claude", "opencode_cmd=opencode",
		"settings=", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
}

func TestRelaunchAIPane_explicit_default_choice_overrides_pointer(t *testing.T) {
	// The user picked Default in the popup, but the global pointer (mutable,
	// shared by every session and the launcher) names "work" at respawn time.
	// The relaunch must honor the explicit choice: no CLAUDE_CONFIG_DIR.
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-account", "work\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "work"), ".keep", "")
	relaunch := relaunchCtx(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q \"\"", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertNotContains(t, logOut, "CLAUDE_CONFIG_DIR")
}

func TestRelaunchAIPane_explicit_named_choice_overrides_pointer(t *testing.T) {
	// Inverse case: the user picked "work" but the pointer says Default (or
	// was flipped by another session). The respawn must run work's config dir.
	dir := t.TempDir()
	acct := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acct, ".keep", "")
	relaunch := relaunchCtx(t, dir) // pointer file absent = Default
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q work", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+acct+`"`)
}

func TestOpenAccountSwitcher_respawns_under_choice_not_pointer(t *testing.T) {
	// Race guard: the popup reported "personal" through the result file, but
	// by respawn time the global pointer holds "other" (another session's
	// switch landed in between). The pane must come back under the login the
	// USER PICKED HERE — not whatever the pointer says at that instant.
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\nOther:other\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "personal"), ".keep", "")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "other"), ".keep", "")
	pointer := filepath.Join(dir, "claude-account")
	rec := filepath.Join(dir, "tmux.log")
	// display-popup simulates: user picks "personal" (result file), then a
	// concurrent session's switch rewrites the pointer to "other".
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then
  printf 'WISP_DECK_CLAUDE_ACCOUNT=\n'; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "display-popup" ]; then
  rf=$(printf '%%s' "$*" | sed -n 's/.*--result-file \([^ ]*\).*/\1/p')
  [ -n "$rf" ] && printf 'personal\n' > "$rf"
  printf 'other\n' > %q
  exit 0
fi
printf '%%s\n' "$*" >> %q`, pointer, rec))
	relaunch := relaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+filepath.Join(dir, "claude-accounts", "personal")+`"`)
	assertNotContains(t, logOut, filepath.Join(dir, "claude-accounts", "other"))
}
