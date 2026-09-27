package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Restore reopens every tab-view window on its own conversation, at its own
// index. These cover tab_view_new_window's optional resume_sid/window_index.

func newWindowRun(t *testing.T, extra ...string) (rec, out string) {
	t.Helper()
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", "")
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(mockTabViewTmux), 0755); err != nil {
		t.Fatalf("write tmux mock: %v", err)
	}
	recPath := filepath.Join(dir, "rec")
	cfgDir := filepath.Join(dir, "cfg")
	projDir := filepath.Join(dir, "proj")
	for _, d := range []string{cfgDir, projDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	relaunch := writeRelaunchFixture(t, cfgDir, projDir, "claude", "/usr/bin/claude")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir}, "GT_REC="+recPath, "MOCK_RELAUNCH="+relaunch, "MOCK_ACCOUNT=")
	args := append([]string{tmuxPath, filepath.Join(root, "lib"), "dev-proj-1"}, extra...)
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_new_window", args, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("tmux never invoked: %v (output: %s)", err, out)
	}
	return string(data), out
}

func TestTabViewNewWindowRestore_resume_sid_builds_resume_launch(t *testing.T) {
	rec, _ := newWindowRun(t, "11111111-2222-3333-4444-555555555555", "3")
	assertContains(t, rec, "--resume 11111111-2222-3333-4444-555555555555")
}

func TestTabViewNewWindowRestore_lands_at_requested_index(t *testing.T) {
	rec, _ := newWindowRun(t, "11111111-2222-3333-4444-555555555555", "3")
	assertContains(t, rec, "new-window -t =dev-proj-1:3 ")
}

func TestTabViewNewWindowRestore_stamps_window_with_resume_sid(t *testing.T) {
	rec, _ := newWindowRun(t, "11111111-2222-3333-4444-555555555555", "3")
	assertContains(t, rec, "set-option -w -t @7 @wd_claude_session 11111111-2222-3333-4444-555555555555")
}

func TestTabViewNewWindowRestore_prints_new_window_id(t *testing.T) {
	_, out := newWindowRun(t, "11111111-2222-3333-4444-555555555555", "3")
	if strings.TrimSpace(out) != "@7" {
		t.Fatalf("want the new window id on stdout, got %q", out)
	}
}

func TestTabViewNewWindowRestore_empty_sid_is_fresh_and_unstamped(t *testing.T) {
	rec, out := newWindowRun(t, "", "2")
	assertContains(t, rec, "new-window -t =dev-proj-1:2 ")
	assertNotContains(t, rec, "--resume")
	assertNotContains(t, rec, "@wd_claude_session")
	if strings.TrimSpace(out) != "@7" {
		t.Fatalf("want the new window id on stdout, got %q", out)
	}
}

// prefix+c and the [+] click run this through a foreground run-shell, which
// shows any stdout in view mode over the pane. The 3-arg call must print
// nothing and keep appending at the next free index.
func TestTabViewNewWindowRestore_three_arg_call_is_unchanged(t *testing.T) {
	rec, out := newWindowRun(t)
	if out != "" {
		t.Fatalf("3-arg call must print nothing, got %q", out)
	}
	assertContains(t, rec, "new-window -t =dev-proj-1: ")
	assertNotContains(t, rec, "--resume")
	assertNotContains(t, rec, "@wd_claude_session")
}
