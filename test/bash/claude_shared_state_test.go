package bash_test

import (
	"os"
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
