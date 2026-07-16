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
