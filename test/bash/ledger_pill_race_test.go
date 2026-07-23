package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// End-to-end guard for the missing account/profile pill.
//
// The ledger pane is created by the same tmux new-session that stamps
// WISP_DECK_RELAUNCH_FILE into its environment, while wrapper.sh writes that
// file afterwards in the launch tail — where the post-pick path guard requires
// it to stay, so the agent's boot overlaps it. The pane therefore ALWAYS races
// its own context, and whether it wins depends on how warm the TUI binary is.
// A ledger that read the context once, at startup, showed no pill at all for
// the pane's entire life whenever it lost that race.
//
// This drives the REAL binary over a pty, exactly as the pane does, and covers
// what the model-level tests cannot: the flag plumbing in cmd/wisp-deck-tui,
// the session source wiring, the refresh loop, and the footer renderer.
func TestNativeLedgerPillAppearsWhenRelaunchContextIsWrittenLate(t *testing.T) {
	project := initNativeLedgerRepo(t)
	snapshot := writeNativeLedgerSnapshot(t, 3, "main")

	config := t.TempDir()
	accounts := filepath.Join(config, "claude-accounts.list")
	if err := os.WriteFile(accounts, []byte("Personal:personal\nWork:work\n"), 0o644); err != nil {
		t.Fatalf("write accounts list: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config, "claude-account"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write account pointer: %v", err)
	}
	// Deliberately NOT created yet: this is the launch race.
	relaunch := filepath.Join(config, "relaunch-testsession")

	session := startNativeLedgerPTY(t, project, snapshot,
		&pty.Winsize{Rows: 24, Cols: 100}, nil,
		"--relaunch-file", relaunch, "--refresh-interval", "200ms")

	if _, raw, ok := session.waitFor("modified", 20*time.Second); !ok {
		t.Fatalf("ledger never painted:\n%s", raw)
	}
	if plain := ansiSeq.ReplaceAllString(session.capture.snapshot(), ""); strings.Contains(plain, "󰀄") {
		t.Fatalf("pill rendered before any context existed:\n%s", plain)
	}
	offset := session.capture.length()

	// The launch tail finally publishes the context, as wrapper.sh does.
	context := strings.Join([]string{
		"tool=claude",
		"tool_cmd=/usr/bin/claude",
		"project_dir=" + project,
		"accounts_dir=" + filepath.Join(config, "claude-accounts"),
		"pointer=" + filepath.Join(config, "claude-account"),
		"list=" + accounts,
		"colors=" + filepath.Join(config, "claude-account-colors"),
		"default_label=" + filepath.Join(config, "claude-account-default-label"),
		"tools=claude",
		"claude_cmd=/usr/bin/claude",
		"",
	}, "\n")
	if err := os.WriteFile(relaunch, []byte(context), 0o644); err != nil {
		t.Fatalf("publish relaunch context: %v", err)
	}

	if _, raw, ok := session.waitAfter(offset, "󰀄", 20*time.Second); !ok {
		t.Fatalf("the ledger never picked up the relaunch context — the account "+
			"pill stayed hidden for the pane's whole life:\n%s",
			ansiSeq.ReplaceAllString(raw, ""))
	}
}
