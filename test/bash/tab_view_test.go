package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockTabViewTmux is a tmux spy for tab-view tests: records every invocation
// to $GT_REC, answers show-environment lookups from MOCK_* vars, and prints
// fake pane ids for the -P creation calls (%10 window/ledger pane, %11 AI
// pane) so pane targeting is observable in the record.
const mockTabViewTmux = `#!/bin/bash
printf '%s\n' "$*" >> "$GT_REC"
case "$1" in
  show-environment)
    for last; do :; done
    case "$last" in
      WISP_DECK_RELAUNCH_FILE)
        [ -n "$MOCK_RELAUNCH" ] && { echo "WISP_DECK_RELAUNCH_FILE=$MOCK_RELAUNCH"; exit 0; }
        echo "-WISP_DECK_RELAUNCH_FILE" >&2; exit 1 ;;
      WISP_DECK_CLAUDE_ACCOUNT)
        [ -n "$MOCK_ACCOUNT" ] && { echo "WISP_DECK_CLAUDE_ACCOUNT=$MOCK_ACCOUNT"; exit 0; }
        echo "-WISP_DECK_CLAUDE_ACCOUNT" >&2; exit 1 ;;
      *) echo "-$last" >&2; exit 1 ;;
    esac ;;
  new-window) echo '%10' ;;
  split-window) case "$*" in *" -P "*) echo '%11' ;; esac ;;
esac
exit 0
`

// writeRelaunchFixture writes a minimal relaunch-context file of the exact
// key=value shape wrapper.sh persists via write_relaunch_context.
func writeRelaunchFixture(t *testing.T, cfgDir, projDir, tool, toolCmd string) string {
	t.Helper()
	content := "tool=" + tool + "\n" +
		"tool_cmd=" + toolCmd + "\n" +
		"settings=\n" +
		"settings_source=\n" +
		"filter=\n" +
		"project_dir=" + projDir + "\n" +
		"accounts_dir=" + filepath.Join(cfgDir, "claude-accounts") + "\n" +
		"pointer=" + filepath.Join(cfgDir, "claude-account") + "\n" +
		"tools=claude\n" +
		"claude_cmd=" + toolCmd + "\n" +
		"opencode_cmd=\n" +
		"codex_cmd=\n"
	return writeTempFile(t, cfgDir, "relaunch-dev-proj-1", content)
}

func TestTabViewStatusLeft_contains_label_ranges_and_accent(t *testing.T) {
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_status_left",
		[]string{"myproj", "141"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "⬡ myproj")
	// One chip per window, iterated live by tmux; each chip is a click range
	// carrying its window id, and the + button has its own named range.
	assertContains(t, out, "#{W:")
	assertContains(t, out, "range=user|wdtab:#{window_id}")
	assertContains(t, out, "range=user|wdnew")
	assertContains(t, out, "colour141")
	// Chips are numbered 1-based (outer windows are 0-indexed).
	assertContains(t, out, "#{e|+:#{window_index},1}")
}

func TestTabViewDispatch_select_routes_to_select_window(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `printf '%s\n' "$*" >> "$GT_REC"`)
	rec := filepath.Join(dir, "rec")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	_, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_dispatch",
		[]string{filepath.Join(binDir, "tmux"), filepath.Join(root, "lib"), "dev-proj-1", "wdtab:@5"}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux never invoked: %v", err)
	}
	assertContains(t, string(data), "select-window -t @5")
}

func TestTabViewDispatch_unknown_range_is_ignored(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `printf '%s\n' "$*" >> "$GT_REC"`)
	rec := filepath.Join(dir, "rec")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	_, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_dispatch",
		[]string{filepath.Join(binDir, "tmux"), filepath.Join(root, "lib"), "dev-proj-1", ""}, env)
	assertExitCode(t, code, 0)
	if _, err := os.ReadFile(rec); err == nil {
		t.Fatalf("expected no tmux calls for an empty range")
	}
}

// newWindowRecord runs tab_view_new_window against the spy tmux and returns
// the recorded tmux invocations.
func newWindowRecord(t *testing.T, account string) string {
	t.Helper()
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", "")
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(mockTabViewTmux), 0755); err != nil {
		t.Fatalf("write tmux mock: %v", err)
	}
	rec := filepath.Join(dir, "rec")
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	relaunch := writeRelaunchFixture(t, cfgDir, projDir, "claude", "/usr/bin/claude")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"GT_REC="+rec, "MOCK_RELAUNCH="+relaunch, "MOCK_ACCOUNT="+account)
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_new_window",
		[]string{tmuxPath, filepath.Join(root, "lib"), "dev-proj-1"}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux never invoked: %v (output: %s)", err, out)
	}
	return string(data)
}

func TestTabViewNewWindow_builds_three_pane_layout(t *testing.T) {
	got := newWindowRecord(t, "")

	// Ledger pane: the window itself, opened in the project dir on the
	// compact view.
	if !strings.Contains(got, "new-window") {
		t.Fatalf("no new-window call:\n%s", got)
	}
	assertContains(t, got, "compact_view")
	assertContains(t, got, "/proj")

	// AI pane: 75% horizontal split, running the context's tool fresh.
	assertContains(t, got, "split-window -h -p 75")
	assertContains(t, got, "/usr/bin/claude")
	assertNotContains(t, got, "--resume")
	// One attention generation has one publisher (window 0's); extra windows
	// launch unsupervised.
	assertNotContains(t, got, "claude-attention")

	// The AI pane is marked for gt_ai_pane consumers and left focused.
	assertContains(t, got, "set-option -p -t %11 @gt_ai 1")
	assertContains(t, got, "select-pane -t %11")

	// Spare pane: 45% vertical split joining the session's existing inner
	// spare server (per-session socket + prewritten conf).
	assertContains(t, got, "split-window -v -p 45")
	assertContains(t, got, "-L gtspare_dev-proj-1")
	assertContains(t, got, "spare-dev-proj-1.conf")
}

func TestTabViewNewWindow_fresh_launch_honours_session_account(t *testing.T) {
	got := newWindowRecord(t, "work")
	assertContains(t, got, `CLAUDE_CONFIG_DIR=`)
	assertContains(t, got, filepath.Join("claude-accounts", "work"))
}

func TestTabViewNewWindow_default_account_sheds_config_dir(t *testing.T) {
	got := newWindowRecord(t, "")
	assertContains(t, got, "env -u CLAUDE_CONFIG_DIR")
}

func TestTabViewDispatch_wdnew_creates_window(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", "")
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(mockTabViewTmux), 0755); err != nil {
		t.Fatalf("write tmux mock: %v", err)
	}
	rec := filepath.Join(dir, "rec")
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	projDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	relaunch := writeRelaunchFixture(t, cfgDir, projDir, "claude", "/usr/bin/claude")
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"GT_REC="+rec, "MOCK_RELAUNCH="+relaunch, "MOCK_ACCOUNT=")
	_, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_dispatch",
		[]string{tmuxPath, filepath.Join(root, "lib"), "dev-proj-1", "wdnew"}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux never invoked: %v", err)
	}
	assertContains(t, string(data), "new-window")
	assertContains(t, string(data), "split-window -h -p 75")
}

// TestWrapper_tab_view_bar_and_binds verifies the launch chain wires the tab
// view: first batch turns the session status bar on at the top with the
// chip/+ status-left; second batch binds prefix+c and the status mouse keys
// BEFORE the ledger-hover install grabs its key-table clone.
func TestWrapper_tab_view_bar_and_binds(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	recPath := filepath.Join(home, "rec")
	mocks := map[string]string{
		"tmux":          recordingTmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	seedRestoreQueue(t, home, projDir, "claude")
	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("tmux never invoked: %v", err)
	}
	got := string(data)

	// First batch: visible top bar with the tab view.
	assertContains(t, got, "set-option status on")
	assertContains(t, got, "set-option status-position top")
	assertContains(t, got, "range=user|wdnew")
	assertContains(t, got, "range=user|wdtab:#{window_id}")

	// Second batch: new-window keybind and clickable status line.
	assertContains(t, got, "bind-key c run-shell")
	assertContains(t, got, "MouseDown1Status")
	assertContains(t, got, "MouseDown1StatusLeft")
	assertContains(t, got, "MouseDown1StatusRight")
	assertContains(t, got, "tab_view_dispatch")

	// The hover install clones the session's effective key table; the status
	// mouse binds must exist before that clone or clicks are lost.
	hoverAt := strings.Index(got, "ledger_hover_install")
	bindAt := strings.Index(got, "MouseDown1Status")
	if hoverAt == -1 {
		t.Fatalf("ledger hover install missing from chain:\n%s", got)
	}
	if bindAt == -1 || bindAt > hoverAt {
		t.Fatalf("status mouse binds must precede the hover install (bind=%d hover=%d)", bindAt, hoverAt)
	}
}
