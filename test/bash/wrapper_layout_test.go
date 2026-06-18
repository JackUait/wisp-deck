package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWrapper_terminal_pane_is_45_percent verifies the left column's vertical
// split gives the bottom terminal pane 45% of the height. The whole
// "new-session ... \; split-window ..." chain is one tmux invocation, so the
// mock records all of it via $* and we can assert the split percentage.
func TestWrapper_terminal_pane_is_45_percent(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	recPath := filepath.Join(home, "rec")
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"new-session\" ]; then printf '%s\\n' \"$*\" > \"$GT_REC\"; exit 0; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"lazygit":       "#!/bin/bash\nexit 0\n",
		"ghost-tab-tui": "#!/bin/bash\nexit 0\n",
	}
	for name, body := range mocks {
		p := filepath.Join(binDir, name)
		if err := os.WriteFile(p, []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}

	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", []string{"--restore", projDir, "claude"}, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("new-session was never invoked (no record): %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "split-window -v -p 45") {
		t.Errorf("terminal pane should be split at 45%%; got tmux args:\n%s", got)
	}
}
