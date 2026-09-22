package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileClaudeLogins_passes_the_deck_files_and_prints_nothing(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	record := filepath.Join(dir, "args")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "wisp-deck-tui"),
		"#!/bin/bash\nprintf '%s\\n' \"$@\" > "+record+"\necho noise; echo err >&2; exit 3\n")
	root := filepath.Join(dir, "cfg")

	out, code := runBashFunc(t, "lib/claude-accounts.sh", "reconcile_claude_logins",
		[]string{root + "/claude-accounts", root + "/claude-accounts.list"}, buildEnv(t, []string{bin}))
	assertExitCode(t, code, 0)
	if out != "" {
		t.Fatalf("the launch pane must stay clean, got %q", out)
	}
	args, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"claude-account", "reconcile",
		"--list", root + "/claude-accounts.list",
		"--accounts-dir", root + "/claude-accounts",
		"--emails", root + "/claude-account-emails",
		"--usage-dir", root + "/account-usage",
	}, "\n") + "\n"
	if string(args) != want {
		t.Fatalf("args =\n%s\nwant\n%s", args, want)
	}
}

// Claude reads the Keychain once at start, so the repair must land before the
// launch in both places that start claude under a login.
func TestReconcileClaudeLogins_runs_before_every_claude_launch(t *testing.T) {
	root := projectRoot(t)
	for file, anchor := range map[string]string{
		"wrapper.sh":            `sync_claude_shared_settings "$HOME/.claude" "$WISP_DECK_CLAUDE_ACCOUNT_DIR"`,
		"lib/account-switch.sh": `pane="$(find_ai_pane "$tmux_cmd")"`,
	} {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		call := strings.Index(src, "reconcile_claude_logins ")
		at := strings.Index(src, anchor)
		if call < 0 || at < 0 || call > at {
			t.Fatalf("%s: reconcile_claude_logins must run before %q (call %d, anchor %d)", file, anchor, call, at)
		}
	}
}
