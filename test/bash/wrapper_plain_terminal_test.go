package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The "plain terminal" menu action opens a bare Ghostty shell (wrapper.sh execs
// $SHELL) instead of a project session. That exec happens before wrapper.sh's
// normal account resolution, so without help `claude` in a plain terminal would
// always use the Keychain default rather than the login the user currently has
// selected. apply_plain_terminal_claude_account bridges that gap: it exports
// CLAUDE_CONFIG_DIR for the active account so the plain shell loads the current
// Claude Code user. Default (no active account) leaves it unset (Keychain login).

func TestApplyPlainTerminalAccount_exports_active_account_dir(t *testing.T) {
	root := projectRoot(t)
	libPath := filepath.Join(root, "lib/claude-accounts.sh")
	dir := t.TempDir()
	acctRoot := filepath.Join(dir, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(acctRoot, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	ptr := filepath.Join(dir, "claude-account")
	writeTempFile(t, dir, "claude-account", "work")

	script := `
unset CLAUDE_CONFIG_DIR
source ` + libPath + `
apply_plain_terminal_claude_account "` + acctRoot + `" "` + ptr + `"
echo "RESULT=[${CLAUDE_CONFIG_DIR:-}]"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	want := "RESULT=[" + filepath.Join(acctRoot, "work") + "]"
	if !strings.Contains(out, want) {
		t.Fatalf("got %q, want it to contain %q", out, want)
	}
}

func TestApplyPlainTerminalAccount_default_leaves_config_dir_unset(t *testing.T) {
	root := projectRoot(t)
	libPath := filepath.Join(root, "lib/claude-accounts.sh")
	dir := t.TempDir()
	acctRoot := filepath.Join(dir, "claude-accounts")
	ptr := filepath.Join(dir, "claude-account") // absent → Default

	script := `
unset CLAUDE_CONFIG_DIR
source ` + libPath + `
apply_plain_terminal_claude_account "` + acctRoot + `" "` + ptr + `"
echo "RESULT=[${CLAUDE_CONFIG_DIR:-}]"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "RESULT=[]")
}

func TestApplyPlainTerminalAccount_missing_dir_leaves_config_dir_unset(t *testing.T) {
	root := projectRoot(t)
	libPath := filepath.Join(root, "lib/claude-accounts.sh")
	dir := t.TempDir()
	acctRoot := filepath.Join(dir, "claude-accounts")
	ptr := filepath.Join(dir, "claude-account")
	// Pointer names an account whose dir does not exist → fall back to Keychain.
	writeTempFile(t, dir, "claude-account", "missing")

	script := `
unset CLAUDE_CONFIG_DIR
source ` + libPath + `
apply_plain_terminal_claude_account "` + acctRoot + `" "` + ptr + `"
echo "RESULT=[${CLAUDE_CONFIG_DIR:-}]"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "RESULT=[]")
}
