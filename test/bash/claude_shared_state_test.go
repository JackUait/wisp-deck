package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Conversation state (transcripts, prompt history, todos, …) must be ONE store
// shared by every Claude login. When each account keeps its own isolated
// projects/ under CLAUDE_CONFIG_DIR, switching the active login silently hides
// all history recorded under the other login, and session-restore resumes the
// wrong conversation after a reboot (the incident: `claude --resume <sid>`
// launched under the personal account could not see the transcript written to
// ~/.claude, and the `-c` fallback opened a stale conversation).
// sync_claude_shared_state merges any account-local state into the standard
// login's store, then symlinks the account's entry to it.

func runStateSync(t *testing.T, source, account string) (string, int) {
	t.Helper()
	return runBashFunc(t, "lib/claude-shared-settings.sh", "sync_claude_shared_state",
		[]string{source, account}, nil)
}

func TestSyncSharedState_merges_account_transcripts_into_source_and_links(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	// A transcript only the account store has (the "lost day" of history) …
	writeSharedFile(t, filepath.Join(account, "projects", "-p", "sid-acc.jsonl"), `{"type":"assistant"}`)
	// … and one only the standard store has.
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid-std.jsonl"), `{"type":"assistant"}`)

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	// Both transcripts now live in the standard store.
	for _, f := range []string{"sid-acc.jsonl", "sid-std.jsonl"} {
		if _, err := os.Stat(filepath.Join(source, "projects", "-p", f)); err != nil {
			t.Fatalf("standard store must hold %s after merge: %v", f, err)
		}
	}
	// The account's projects entry is a symlink to the shared store, so every
	// login reads and writes the same conversation history from now on.
	target, err := os.Readlink(filepath.Join(account, "projects"))
	if err != nil {
		t.Fatalf("account projects should be a symlink: %v", err)
	}
	if target != filepath.Join(source, "projects") {
		t.Fatalf("projects links to %q, want %q", target, filepath.Join(source, "projects"))
	}
	// The transcript is reachable through the account path (what a restored
	// `claude --resume` launched under this account resolves).
	if _, err := os.Stat(filepath.Join(account, "projects", "-p", "sid-std.jsonl")); err != nil {
		t.Fatalf("standard transcript must be visible via account path: %v", err)
	}
}

func TestSyncSharedState_source_wins_on_transcript_conflict(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid.jsonl"), "standard-copy")
	writeSharedFile(t, filepath.Join(account, "projects", "-p", "sid.jsonl"), "account-copy")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	got, _ := os.ReadFile(filepath.Join(source, "projects", "-p", "sid.jsonl"))
	if string(got) != "standard-copy" {
		t.Fatalf("standard store's copy must win on conflict, got %q", got)
	}
	// The losing account copy is preserved (as a *.conflict-* sibling), never
	// silently deleted — a severed link can leave the account copy with turns
	// the store copy lacks.
	if findFileContaining(t, filepath.Join(source, "projects"), "account-copy") == "" {
		t.Fatalf("conflicting account copy must be preserved somewhere under the store")
	}
}

// findFileContaining walks root and returns the path of the first regular file
// whose content contains needle, or "".
func findFileContaining(t *testing.T, root, needle string) string {
	t.Helper()
	var found string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.Mode().IsRegular() || found != "" {
			return nil
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), needle) {
			found = p
		}
		return nil
	})
	return found
}

func TestSyncSharedState_identical_duplicate_is_dropped_without_conflict_copy(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid.jsonl"), "same")
	writeSharedFile(t, filepath.Join(account, "projects", "-p", "sid.jsonl"), "same")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	entries, _ := os.ReadDir(filepath.Join(source, "projects", "-p"))
	if len(entries) != 1 {
		t.Fatalf("identical account copy must be dropped, not kept as a conflict file: %v", entries)
	}
}

func TestSyncSharedState_steady_state_is_a_true_noop(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid.jsonl"), "x")
	writeSharedFile(t, filepath.Join(source, "history.jsonl"), "h")
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-link every item, then make the account dir read-only: a true no-op
	// needs no writes at all. Re-linking on every launch (rm + ln) would open a
	// window where a live claude append lands in a fresh real file that the
	// next launch's migration races against.
	for _, item := range []string{"projects", "history.jsonl"} {
		if err := os.Symlink(filepath.Join(source, item), filepath.Join(account, item)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(account, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(account, 0o755) })

	out, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("steady-state sync must be silent (no rm/ln attempts), got %q", out)
	}
	if target, err := os.Readlink(filepath.Join(account, "projects")); err != nil || target != filepath.Join(source, "projects") {
		t.Fatalf("existing correct link must be left untouched: %q %v", target, err)
	}
}

// The steady-state check must also be FORK-FREE. sync_all runs it for every
// account on the launch critical path (before the picker), and a readlink
// subprocess per item per account added ~0.2s per launch (multiples under
// load). `[ dest -ef src ]` answers "already an alias of the store" as a test
// builtin; readlink is only acceptable as a fallback for a dangling link.
func TestSyncSharedState_steady_state_spawns_no_readlink(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid.jsonl"), "x")
	writeSharedFile(t, filepath.Join(source, "history.jsonl"), "h")
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{"projects", "history.jsonl"} {
		if err := os.Symlink(filepath.Join(source, item), filepath.Join(account, item)); err != nil {
			t.Fatal(err)
		}
	}

	spyLog := filepath.Join(dir, "readlink.log")
	binDir := mockCommand(t, dir, "readlink", `
echo "readlink $*" >> "$READLINK_LOG"
exec /usr/bin/readlink "$@"
`)
	env := buildEnv(t, []string{binDir}, "READLINK_LOG="+spyLog)

	_, code := runBashFunc(t, "lib/claude-shared-settings.sh", "sync_claude_shared_state",
		[]string{source, account}, env)
	assertExitCode(t, code, 0)

	if logged, _ := os.ReadFile(spyLog); len(logged) != 0 {
		t.Errorf("steady-state sync spawned readlink; the linked-already check "+
			"must use the -ef test builtin (no fork):\n%s", logged)
	}
	if target, err := os.Readlink(filepath.Join(account, "projects")); err != nil || target != filepath.Join(source, "projects") {
		t.Fatalf("existing correct link must be left untouched: %q %v", target, err)
	}
}

func TestSyncSharedState_unmergeable_files_survive_and_heal_later(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	// The store's project subdir is unwritable, so the mv into it must fail —
	// the account transcript must survive somewhere on disk (never be deleted)
	// and reach the store once the store is writable again.
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "existing.jsonl"), "x")
	writeSharedFile(t, filepath.Join(account, "projects", "-p", "fresh.jsonl"), "precious")
	if err := os.Chmod(filepath.Join(source, "projects", "-p"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(source, "projects", "-p"), 0o755) })

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)
	if findFileContaining(t, dir, "precious") == "" {
		t.Fatalf("unmergeable transcript was destroyed — it must survive a failed merge")
	}

	if err := os.Chmod(filepath.Join(source, "projects", "-p"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, code = runStateSync(t, source, account)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(source, "projects", "-p", "fresh.jsonl")); err != nil {
		t.Fatalf("second sync must complete the merge into the store: %v", err)
	}
}

func TestSyncSharedState_drains_leftover_aside_dir(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "std.jsonl"), "x")
	// An interrupted earlier migration left an aside dir behind; its owning
	// process (pid 99999 — above macOS's max pid) is gone.
	writeSharedFile(t, filepath.Join(account, "projects.migrating.99999", "-p", "orphan.jsonl"), "orphan")
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(filepath.Join(source, "projects", "-p", "orphan.jsonl")); err != nil {
		t.Fatalf("aside leftovers must be merged into the store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(account, "projects.migrating.99999")); err == nil {
		t.Fatalf("drained aside dir must be removed")
	}
}

func TestSyncSharedState_leaves_live_migrations_aside_alone(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "history.jsonl"), "{\"h\":\"a\"}\n")
	// An aside owned by a LIVE process (this test) — a concurrent launch is
	// mid-drain. Touching it here would double-append its history entries.
	aside := filepath.Join(account, fmt.Sprintf("history.jsonl.migrating.%d", os.Getpid()))
	writeSharedFile(t, aside, "{\"h\":\"b\"}\n")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(aside); err != nil {
		t.Fatalf("a live migration's aside must not be touched: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(source, "history.jsonl"))
	if strings.Contains(string(got), `"b"`) {
		t.Fatalf("concurrent aside must not be drained by another launch, got %q", got)
	}
}

func TestSyncSharedState_history_append_is_deduplicated(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	// Severed-link shape: the account copy is a full copy of the shared history
	// plus new entries. A blind append would duplicate the whole history.
	writeSharedFile(t, filepath.Join(source, "history.jsonl"), "{\"h\":\"a\"}\n{\"h\":\"b\"}\n")
	writeSharedFile(t, filepath.Join(account, "history.jsonl"), "{\"h\":\"a\"}\n{\"h\":\"b\"}\n{\"h\":\"c\"}\n")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	got, _ := os.ReadFile(filepath.Join(source, "history.jsonl"))
	want := "{\"h\":\"a\"}\n{\"h\":\"b\"}\n{\"h\":\"c\"}\n"
	if string(got) != want {
		t.Fatalf("history must gain only the missing entries, got %q", got)
	}
}

func TestSyncSharedState_appends_account_history_to_source(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "history.jsonl"), "{\"display\":\"std\"}\n")
	writeSharedFile(t, filepath.Join(account, "history.jsonl"), "{\"display\":\"acc\"}\n")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	got, _ := os.ReadFile(filepath.Join(source, "history.jsonl"))
	if !strings.Contains(string(got), `"std"`) || !strings.Contains(string(got), `"acc"`) {
		t.Fatalf("prompt history from both logins must survive the merge, got %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(account, "history.jsonl")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("account history.jsonl must become a symlink to the shared file")
	}
}

func TestSyncSharedState_moves_account_item_when_source_missing(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "history.jsonl"), "x")
	writeSharedFile(t, filepath.Join(account, "todos", "sid.json"), "[]")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(filepath.Join(source, "todos", "sid.json")); err != nil {
		t.Fatalf("account-only todos must move into the standard store: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(account, "todos")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("account todos must become a symlink after the move")
	}
}

func TestSyncSharedState_is_idempotent(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid.jsonl"), "keep")
	writeSharedFile(t, filepath.Join(account, "history.jsonl"), "h")

	for i := 0; i < 2; i++ {
		if _, code := runStateSync(t, source, account); code != 0 {
			t.Fatalf("run %d failed", i)
		}
	}
	got, _ := os.ReadFile(filepath.Join(source, "projects", "-p", "sid.jsonl"))
	if string(got) != "keep" {
		t.Fatalf("shared store content must survive repeated syncs, got %q", got)
	}
	// history must not be duplicated by a second run (dest is a link by then).
	h, _ := os.ReadFile(filepath.Join(source, "history.jsonl"))
	if string(h) != "h" {
		t.Fatalf("second sync must not re-append history, got %q", h)
	}
}

func TestSyncSharedState_preserves_account_credentials(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "s.jsonl"), "x")
	writeSharedFile(t, filepath.Join(account, ".credentials.json"), "secret")
	writeSharedFile(t, filepath.Join(account, ".claude.json"), "identity")

	_, code := runStateSync(t, source, account)
	assertExitCode(t, code, 0)

	for name, want := range map[string]string{".credentials.json": "secret", ".claude.json": "identity"} {
		got, _ := os.ReadFile(filepath.Join(account, name))
		if string(got) != want {
			t.Fatalf("%s must be untouched, got %q", name, got)
		}
		if fi, err := os.Lstat(filepath.Join(account, name)); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s must remain a real file", name)
		}
	}
}

func TestSyncSharedState_noop_when_source_equals_account_or_dirs_missing(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "s.jsonl"), "x")

	if _, code := runStateSync(t, source, source); code != 0 {
		t.Fatalf("self-sync must be a no-op")
	}
	if fi, err := os.Lstat(filepath.Join(source, "projects")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("self-sync must never replace the store with a link to itself")
	}
	if _, code := runStateSync(t, filepath.Join(dir, "nope"), source); code != 0 {
		t.Fatalf("missing source dir must be a clean no-op")
	}
	if _, code := runStateSync(t, source, filepath.Join(dir, "nope")); code != 0 {
		t.Fatalf("missing account dir must be a clean no-op")
	}
}

func TestSyncAllAccountsState_merges_every_registered_account(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	accounts := filepath.Join(dir, "claude-accounts")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "std.jsonl"), "x")
	writeSharedFile(t, filepath.Join(accounts, "personal", "projects", "-p", "a.jsonl"), "x")
	writeSharedFile(t, filepath.Join(accounts, "work", "projects", "-p", "b.jsonl"), "x")

	_, code := runBashFunc(t, "lib/claude-shared-settings.sh", "sync_all_claude_accounts_state",
		[]string{source, accounts}, nil)
	assertExitCode(t, code, 0)

	for _, f := range []string{"a.jsonl", "b.jsonl"} {
		if _, err := os.Stat(filepath.Join(source, "projects", "-p", f)); err != nil {
			t.Fatalf("transcript %s from an account store must reach the shared store: %v", f, err)
		}
	}
	for _, acc := range []string{"personal", "work"} {
		if fi, err := os.Lstat(filepath.Join(accounts, acc, "projects")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("account %s projects must be linked to the shared store", acc)
		}
	}
}

// runStateSyncUnderZsh sources claude-shared-settings.sh and calls
// sync_claude_shared_state under ZSH — the shell the compact-view pane runs
// under (the user's $SHELL). It prints a survival marker AFTER the call so the
// test can tell "completed cleanly" from "the shell aborted mid-function".
func runStateSyncUnderZsh(t *testing.T, source, account string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	module := filepath.Join(root, "lib", "claude-shared-settings.sh")
	script := fmt.Sprintf(
		"source %q && sync_claude_shared_state %q %q && print DONE-SYNC-SURVIVED",
		module, source, account)
	cmd := exec.Command("zsh", "-c", script)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run zsh: %v", err)
		}
	}
	return string(out), code
}

// Regression: the compact-view ledger pane runs under ZSH (the user's $SHELL),
// where an unmatched glob is a FATAL error by default (the `nomatch` option) —
// not a literal pass-through as in bash. The mid-session account switch runs
// sync_claude_shared_state FROM that pane's zsh (via relaunch_ai_pane); its
// `for aside in "$dest".migrating.*` loop hit no `.migrating.*` files in the
// normal case and aborted the whole pane, so the file-list view vanished
// instead of restoring. Every other sync test runs under bash, which hid this.
// The function must complete cleanly under zsh even when NO migration files
// exist (the common case): the account item is migrated and linked, and the
// shell survives to print the marker.
func TestSyncSharedState_survives_under_zsh_pane_shell(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	// A real (unlinked) account item so the migration path runs, and NO
	// `.migrating.*` files anywhere — exactly the state at a first switch.
	writeSharedFile(t, filepath.Join(account, "projects", "-p", "sid-acc.jsonl"), `{"type":"assistant"}`)
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid-std.jsonl"), `{"type":"assistant"}`)

	out, code := runStateSyncUnderZsh(t, source, account)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "DONE-SYNC-SURVIVED") {
		t.Fatalf("sync aborted the zsh pane (no survival marker); output:\n%s", out)
	}
	// And it actually did its job: the account entry is linked to the store.
	if target, err := os.Readlink(filepath.Join(account, "projects")); err != nil ||
		target != filepath.Join(source, "projects") {
		t.Fatalf("account projects must link to the shared store under zsh (got %q, err %v)",
			target, err)
	}
}

// Regression: the steady state (account item ALREADY linked to the store) must
// also survive under zsh. This is the most common mid-session switch — nothing
// to migrate — yet the fatal `.migrating.*` glob is evaluated BEFORE the
// already-linked skip, so a second switch aborted the pane just like the first.
func TestSyncSharedState_steady_state_survives_under_zsh(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	account := filepath.Join(dir, "account")
	writeSharedFile(t, filepath.Join(source, "projects", "-p", "sid-std.jsonl"), `{"type":"assistant"}`)
	// Pre-link the account entry to the store: the steady state a repeat switch
	// lands in (no migration, no `.migrating.*` files).
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "projects"), filepath.Join(account, "projects")); err != nil {
		t.Fatal(err)
	}

	out, code := runStateSyncUnderZsh(t, source, account)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "DONE-SYNC-SURVIVED") {
		t.Fatalf("steady-state sync aborted the zsh pane; output:\n%s", out)
	}
}

// Defense-in-depth: sync_all_claude_accounts_state has the same fatal-glob
// hazard (`for acc in "$accounts_dir"/*/`). It runs at boot under bash today,
// but must be zsh-safe too so a future zsh caller can never reintroduce the
// pane-killing abort. With an accounts dir that has no registered accounts yet,
// the glob matches nothing — under unguarded zsh that aborts the shell.
func TestSyncAllAccountsState_survives_under_zsh_when_empty(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "standard")
	accounts := filepath.Join(dir, "claude-accounts")
	if err := os.MkdirAll(accounts, 0o755); err != nil { // exists but empty
		t.Fatal(err)
	}
	module := filepath.Join(projectRoot(t), "lib", "claude-shared-settings.sh")
	script := fmt.Sprintf(
		"source %q && sync_all_claude_accounts_state %q %q && print DONE-ALL-SURVIVED",
		module, source, accounts)
	out, err := exec.Command("zsh", "-c", script).CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	assertExitCode(t, code, 0)
	if !strings.Contains(string(out), "DONE-ALL-SURVIVED") {
		t.Fatalf("sync_all aborted the zsh shell on an empty accounts dir; output:\n%s", out)
	}
}
