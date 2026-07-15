package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// End-to-end regression guard for the mid-session account "switch back" bug,
// driven through a REAL tmux server (private socket): the pane runs the
// "personal" login while the GLOBAL pointer already says Default (flipped by
// another session or the launcher). Picking Default in the switcher must still
// relaunch the AI pane — the old pointer-diff decision saw "Default == Default"
// and silently skipped it, stranding the pane on the old login forever.
//
// The interactive Go popup is replaced by a fake wisp-deck-tui that immediately
// "picks Default" (writes the --result-file and removes the pointer), so the
// test exercises every real seam the bug lived in: the tmux session-env stamp,
// display-popup (which needs the pty-attached client), the result-file
// contract, the relaunch decision, respawn-pane, and the re-stamp.
func TestAccountSwitch_endToEnd_realTmux_relaunches_when_pointer_matches_choice(t *testing.T) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	dir := t.TempDir()
	sock := fmt.Sprintf("wdswitch-e2e-%d", os.Getpid())
	tmux := func(args ...string) ([]byte, error) {
		return exec.Command(tmuxBin, append([]string{"-L", sock}, args...)...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = tmux("kill-server") })

	// Config tree: one managed login ("personal") + the implicit Default; the
	// pointer file is ABSENT — the global pointer already says Default.
	cfg := filepath.Join(dir, "cfg")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, filepath.Join(cfg, "claude-accounts", "personal"), ".keep", "")
	pointer := filepath.Join(cfg, "claude-account")

	// Fake claude: logs the login it was launched under, then stays alive.
	logFile := filepath.Join(dir, "claude.log")
	binDir := filepath.Join(dir, "bin")
	writeTempFile(t, binDir, "claude", fmt.Sprintf(
		"#!/bin/bash\necho \"CLAUDE_CONFIG_DIR=[${CLAUDE_CONFIG_DIR:-UNSET}]\" >> %q\nsleep 300\n", logFile))
	// Fake popup binary: advertises the new flags in --help (the capability
	// probe reads it) and, when run as the popup, "picks Default".
	writeTempFile(t, binDir, "wisp-deck-tui", fmt.Sprintf(`#!/bin/bash
if [ "$2" = "--help" ] || [ "$1" = "--help" ]; then
  printf -- 'Flags:\n      --active string\n      --result-file string\n'
  exit 0
fi
rf=""; ptr=""
while [ $# -gt 0 ]; do
  case "$1" in
    --result-file) rf="$2"; shift 2 ;;
    --pointer) ptr="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "$rf" ] && printf '\n' > "$rf"
[ -n "$ptr" ] && rm -f "$ptr"
exit 0
`))
	for _, f := range []string{"claude", "wisp-deck-tui"} {
		if err := os.Chmod(filepath.Join(binDir, f), 0755); err != nil {
			t.Fatal(err)
		}
	}

	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=" + filepath.Join(binDir, "claude"),
		"settings=", "filter=",
		"project_dir=" + dir,
		"accounts_dir=" + filepath.Join(cfg, "claude-accounts"),
		"pointer=" + pointer,
		"list=" + filepath.Join(cfg, "claude-accounts.list"),
		"colors=" + filepath.Join(cfg, "claude-account-colors"),
		"default_label=" + filepath.Join(cfg, "claude-account-default-label"),
		"",
	}, "\n"))

	// Real server: ledger pane 0 + AI pane 1 running "claude" under personal,
	// with the session stamped as running personal (what wrapper.sh does).
	if out, err := tmux("new-session", "-d", "-s", "e2e", "-x", "200", "-y", "50"); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	if out, err := tmux("split-window", "-t", "e2e",
		fmt.Sprintf("CLAUDE_CONFIG_DIR=%q %s; exec bash", filepath.Join(cfg, "claude-accounts", "personal"), filepath.Join(binDir, "claude"))); err != nil {
		t.Fatalf("split-window: %v: %s", err, out)
	}
	if out, err := tmux("set-option", "-p", "-t", "e2e:0.1", "@gt_ai", "1"); err != nil {
		t.Fatalf("set-option: %v: %s", err, out)
	}
	if out, err := tmux("set-environment", "-t", "e2e", "WISP_DECK_CLAUDE_ACCOUNT", "personal"); err != nil {
		t.Fatalf("set-environment: %v: %s", err, out)
	}

	// display-popup needs an attached client; a pty client provides one.
	attachPtyClient(t, tmuxBin, sock, "e2e")

	// A tmux shim so the lib's literal `tmux` hits the private socket.
	writeTempFile(t, binDir, "tmux", fmt.Sprintf("#!/bin/bash\nexec %q -L %q \"$@\"\n", tmuxBin, sock))
	if err := os.Chmod(filepath.Join(binDir, "tmux"), 0755); err != nil {
		t.Fatal(err)
	}
	// The popup command resolves wisp-deck-tui through the tmux GLOBAL env's
	// PATH (not the pane shell's), so the fake binary must be stamped there.
	if out, err := tmux("set-environment", "-g", "PATH", binDir+":"+os.Getenv("PATH")); err != nil {
		t.Fatalf("set-environment -g PATH: %v: %s", err, out)
	}

	// From the ledger pane: source the real libs and open the switcher.
	script := fmt.Sprintf(
		"unset WISP_DECK_ATTENTION_FILE WISP_DECK_ATTENTION_GENERATION WISP_DECK_ATTENTION_ROOT WISP_DECK_ATTENTION_DESCRIPTOR; export PATH=%q:$PATH; source %q; source %q; source %q; source %q; source %q; open_account_switcher tmux %q; echo E2E-RC=$?",
		binDir,
		filepath.Join(lib, "statusline.sh"), filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"), filepath.Join(lib, "claude-shared-settings.sh"),
		filepath.Join(lib, "account-switch.sh"), relaunch)
	if out, err := tmux("send-keys", "-t", "e2e:0.0", script, "Enter"); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}

	// The regression: the AI pane must be RELAUNCHED under Default even though
	// the pointer never changed (it already said Default). Poll the fake
	// claude's log for the post-switch launch.
	deadline := time.Now().Add(15 * time.Second)
	for {
		data, _ := os.ReadFile(logFile)
		if strings.Contains(string(data), "CLAUDE_CONFIG_DIR=[UNSET]") {
			break
		}
		if time.Now().After(deadline) {
			pane, _ := tmux("capture-pane", "-p", "-t", "e2e:0.0")
			t.Fatalf("AI pane was not relaunched under Default (the switch-back bug): log=%q pane:\n%s", data, pane)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// And the session stamp must now say Default (empty value, set).
	envOut, _ := tmux("show-environment", "-t", "e2e", "WISP_DECK_CLAUDE_ACCOUNT")
	if strings.TrimSpace(string(envOut)) != "WISP_DECK_CLAUDE_ACCOUNT=" {
		t.Fatalf("session stamp not updated to Default: %q", envOut)
	}
}
