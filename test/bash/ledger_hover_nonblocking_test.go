package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The ledger-hover install runs INSIDE the wrapper's single tmux launch chain,
// before the split-window commands and the final attach. Foreground run-shell
// blocks the rest of the chain until the install script exits — and the
// install serializes on a server-wide root-table lock (up to 15s under a
// many-session server), so a busy machine held the user on a blank tab for
// that long before their panes even existed. It must run with `run-shell -b`:
// tmux expands `#{pane_id}` at the command's position in the chain (still the
// ledger pane — verified against real tmux), then backgrounds the shell so
// splits and attach never wait on the hover plumbing.
func TestWrapper_ledger_hover_install_does_not_block_launch_chain(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	src := string(data)
	re := regexp.MustCompile(`run-shell\s+(\S+)\s+"\$_ledger_hover_setup"`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		if !strings.Contains(src, "_ledger_hover_setup") {
			t.Fatal("wrapper.sh no longer wires _ledger_hover_setup; update this guard")
		}
		t.Fatal("ledger-hover run-shell must carry a flag argument (-b) before \"$_ledger_hover_setup\"")
	}
	if m[1] != "-b" {
		t.Errorf("ledger-hover run-shell uses %q; must be -b so the splits and attach never wait on it", m[1])
	}
}

// Class-level guard: NO foreground run-shell may sit in the launch chain.
// tmux executes the chain's commands in order, and a foreground run-shell
// blocks everything queued after it — splits, attach — for as long as its
// script runs (the ledger-hover install held a server-wide lock for up to
// 15s on a busy machine). bind-key definitions are exempt: their run-shell
// executes at keypress, not at launch.
func TestWrapper_launch_chain_has_no_foreground_run_shell(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	src := string(data)
	start := strings.Index(src, `new-session -d`)
	end := strings.Index(src, `attach-session`)
	if start < 0 || end < 0 || end < start {
		t.Fatal("wrapper.sh launch chain (new-session ... attach-session) not found; update this guard")
	}
	for i, line := range strings.Split(src[start:end], "\n") {
		if !strings.Contains(line, "run-shell") || strings.Contains(line, "bind-key") {
			continue
		}
		if !regexp.MustCompile(`run-shell\s+-b\b`).MatchString(line) {
			t.Errorf("launch-chain line %d uses a foreground run-shell — it blocks the splits "+
				"and attach until its script exits; use `run-shell -b`:\n%s", i, strings.TrimSpace(line))
		}
	}
}
