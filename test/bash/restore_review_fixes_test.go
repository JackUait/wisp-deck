package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Window 0 without its own stamp must not borrow the session env's sid when
// that sid belongs to another window: every tab-view window writes the same
// env var, so the borrowed id would open one conversation in two windows.
func TestWriteSessionSnapshot_first_window_does_not_borrow_an_extra_windows_sid(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "10", "WISP_DECK_CLAUDE_SESSION=sid-one\n")},
	}, []string{"dev-a-1|0|0||L0", "dev-a-1|1|1|sid-one|L1"})
	writeSnapshot(t, bin, snap)
	lines := readLines(t, snap)
	if len(lines) != 1 {
		t.Fatalf("want one line, got %q", lines)
	}
	f := strings.Split(lines[0], "|")
	if f[5] != "" {
		t.Errorf("first window borrowed sid %q from window 1", f[5])
	}
	assertContains(t, f[12], "1"+us+"sid-one"+us+"L1")
}

// The env stamp still names a single-window tab's conversation (sessions from
// before the per-window stamp existed).
func TestWriteSessionSnapshot_single_window_keeps_the_env_sid(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "10", "WISP_DECK_CLAUDE_SESSION=sid-env\n")},
	}, []string{"dev-a-1|0|1||L0"})
	writeSnapshot(t, bin, snap)
	f := strings.Split(readLines(t, snap)[0], "|")
	if f[5] != "sid-env" {
		t.Errorf("want env sid for a single-window tab, got %q", f[5])
	}
}

// Lines a restore already consumed describe tabs that were reopened under new
// session names. Keeping them for the grace period meant a window closed just
// after a restore froze old and new lines together and restored a tab twice.
func TestWriteSessionSnapshot_drops_restored_lines_without_grace(t *testing.T) {
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session",
		snapLine("B1", "p", "/p/p", "claude", "ghostty", "", "L", "", "", "dev-p-111", "", "10", "", "0", "0")+"\n")
	writeTempFile(t, dir, "restored-sessions", "dev-p-111\n")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-p-222", created: 1, attached: 1, env: wispEnv("B1", "p", "/p/p", "20", "")},
	}, []string{"dev-p-222|0|1||L"})
	writeSnapshot(t, bin, snap)
	for _, l := range readLines(t, snap) {
		if strings.Contains(l, "dev-p-111") {
			t.Fatalf("restored line kept: %q", l)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "restored-sessions")); err != nil {
		t.Fatalf("list dropped while an old line still named it: %v", err)
	}
	// Next tick: nothing names the list any more, so a reused PID cannot hit it.
	writeSnapshot(t, bin, snap)
	if _, err := os.Stat(filepath.Join(dir, "restored-sessions")); !os.IsNotExist(err) {
		t.Fatalf("spent restored-sessions list kept: %v", err)
	}
}

func TestMaybeRestore_records_the_session_names_it_restores(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, dir, "last-session",
		snapLine("B0", "p", proj, "claude", "ghostty", "", "L", "", "", "dev-p-1", "", "10", "", "0", "0")+"\n"+
			snapLine("B0", "q", proj, "claude", "ghostty", "", "L", "", "", "dev-q-2", "", "20", "", "0", "0")+"\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "maybe_restore_session",
		[]string{dir, "B1", "false"}, buildEnv(t, nil, "HOME="+dir))
	assertExitCode(t, code, 0)
	got := strings.Join(readLines(t, filepath.Join(dir, "restored-sessions")), ",")
	if got != "dev-p-1,dev-q-2" {
		t.Errorf("restored-sessions = %q", got)
	}
}

// A restored Codex window has no per-window id, and a plain Codex launch would
// replace the lost conversation with an empty one (lib/CLAUDE.md). It must go
// through the resume selector instead.
func TestTabViewNewWindowRestore_codex_window_without_sid_resumes(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", "")
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(mockTabViewTmux), 0o755); err != nil {
		t.Fatal(err)
	}
	recPath := filepath.Join(dir, "rec")
	cfgDir := filepath.Join(dir, "cfg")
	projDir := filepath.Join(dir, "proj")
	for _, d := range []string{cfgDir, projDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	relaunch := writeRelaunchFixture(t, cfgDir, projDir, "codex", "/usr/bin/codex")
	env := buildEnv(t, []string{binDir}, "GT_REC="+recPath, "MOCK_RELAUNCH="+relaunch, "MOCK_ACCOUNT=")
	_, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_new_window",
		[]string{tmuxPath, filepath.Join(projectRoot(t), "lib"), "dev-proj-1", "", "2"}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), "codex resume")
}

// The render path runs many times a second, and set-option redraws every
// attached client even when the value did not move. The write is guarded.
func TestStatuslineStamp_window_option_write_is_guarded(t *testing.T) {
	tmux, sock, shimDir, pane0, _ := stampTmux(t, "guard")
	_ = sock
	payload := stampPayload(t, "sid-g", true)
	env := buildEnv(t, []string{shimDir}, "TMUX=/tmp/x,1,0", "TMUX_PANE="+pane0)
	for i := 0; i < 2; i++ {
		_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session", []string{payload}, env)
		assertExitCode(t, code, 0)
	}
	if got := windowOpt(t, tmux, sock, pane0); got != "sid-g" {
		t.Fatalf("window option = %q", got)
	}
}

// The focused tab is read at wrapper start, before the picker: a tab the user
// looks at while choosing a project must not become the new tab's predecessor.
func TestWrapper_reads_the_focused_tab_before_the_picker(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	read := strings.Index(s, "@wd_focus_seq > ")
	picker := strings.Index(s, "Use TUI for project selection")
	if read < 0 || picker < 0 || read > picker {
		t.Fatalf("focus seq must be captured before the picker (read at %d, picker at %d)", read, picker)
	}
}
