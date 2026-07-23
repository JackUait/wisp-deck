package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ledger pane resolves the account/profile pill from the relaunch context
// named by WISP_DECK_RELAUNCH_FILE, which the pane's env carries from the very
// tmux new-session that creates it — while wrapper.sh writes the file itself
// afterwards, in the launch tail (it MUST stay there: the post-pick path guard
// keeps every millisecond of tail work behind new-session, so the agent's boot
// overlaps it). The pane therefore always races its own context, and the ledger
// re-reads it each refresh until it appears
// (TestLedgerAccountPillRecoversWhenSessionContextArrivesLate).
//
// This guards the other half: whatever a racing reader observes must be a whole
// context. A truncated prefix parses cleanly into a context with no accounts —
// indistinguishable from "nothing to switch to" — so a mid-write reader would
// silently drop the pill (and every switch row with it) rather than fail and be
// retried. The mid-session switch path rewrites this same file while the pane
// is live, so the window is not confined to launch.

// A reader that opens the destination path mid-write must see either nothing or
// a complete context — never a truncated prefix. That means publishing by
// rename, so the destination is never the file being appended to.
func TestWriteRelaunchContextPublishesAtomically(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "relaunch-session")

	// A pre-existing complete context must never be observable as truncated, so
	// the write must not target the destination in place. Prove it by making the
	// destination unwritable: an in-place `> "$out"` truncates it (or fails after
	// destroying nothing), while a rename replaces it wholesale.
	if err := os.WriteFile(out, []byte("tool=claude\nlist=/old/list\n"), 0o400); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	_, code := runBashFunc(t, "lib/account-switch.sh", "write_relaunch_context",
		[]string{out, "claude", "/usr/bin/claude", "/settings.json", "", "/repo",
			"/cfg", "claude codex", "/usr/bin/claude", "", "/usr/bin/codex"}, nil)
	assertExitCode(t, code, 0)

	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	for _, key := range []string{"tool=claude", "list=/cfg/claude-accounts.list",
		"configs_list=/cfg/claude-configs.list", "attention_descriptor="} {
		if !strings.Contains(string(content), key) {
			t.Fatalf("published context missing %q:\n%s", key, content)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(out) {
			t.Fatalf("write left a stray file behind: %s", entry.Name())
		}
	}
}
