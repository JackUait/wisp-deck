package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedRestoreQueue plants a current-boot restore-queue entry (matching the
// mocked sysctl boot id 12345) so wrapper.sh, launched with no arguments,
// restores projDir/tool directly instead of opening the interactive picker.
func seedRestoreQueue(t *testing.T, home, projDir, tool string) {
	t.Helper()
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "restore-queue"),
		[]byte("12345|"+projDir+"|"+tool+"\n"), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

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
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
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

	seedRestoreQueue(t, home, projDir, "claude")
	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
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

// recordWrapperNewSession runs wrapper.sh with a tmux mock that records the
// whole "new-session ... \; ..." chain (one invocation, captured via $*) and
// returns that recorded argument string.
func recordWrapperNewSession(t *testing.T) string {
	t.Helper()
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
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
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
	seedRestoreQueue(t, home, projDir, "claude")
	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("new-session was never invoked (no record): %v", err)
	}
	return string(data)
}

// recordWrapperNewSessionForTool is like recordWrapperNewSession but launches
// the wrapper restoring the given AI tool (with a matching mock command), so the
// recorded chain reflects that tool's theming (e.g. the pane-border accent).
func recordWrapperNewSessionForTool(t *testing.T, tool string) string {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	recPath := filepath.Join(home, "rec")
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"new-session\" ]; then printf '%s\\n' \"$*\" > \"$GT_REC\"; exit 0; fi\nexit 0\n",
		"lazygit":       "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
		tool:            "#!/bin/bash\nexit 0\n",
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
	seedRestoreQueue(t, home, projDir, tool)
	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("new-session was never invoked (no record): %v", err)
	}
	return string(data)
}

// TestWrapper_active_pane_border_uses_tool_accent verifies the focused-pane
// border follows the session's tool: purple (colour141) for OpenCode, orange
// (colour209) for claude.
func TestWrapper_active_pane_border_uses_tool_accent(t *testing.T) {
	opencode := recordWrapperNewSessionForTool(t, "opencode")
	if !strings.Contains(opencode, "pane-active-border-style fg=colour141") {
		t.Errorf("opencode pane border should be purple (colour141); got:\n%s", opencode)
	}
	claude := recordWrapperNewSessionForTool(t, "claude")
	if !strings.Contains(claude, "pane-active-border-style fg=colour209") {
		t.Errorf("claude pane border should be orange (colour209); got:\n%s", claude)
	}
}

// TestWrapper_default_panel_is_compact verifies that, with no saved panel_mode
// setting, the left pane runs the compact changeset ledger (the application
// default) rather than lazygit. recordWrapperNewSession uses a fresh HOME with
// no settings file, so the wrapper falls back to its built-in default.
func TestWrapper_default_panel_is_compact(t *testing.T) {
	got := recordWrapperNewSession(t)
	if !strings.Contains(got, "compact_view") {
		t.Errorf("default left pane should run compact_view; got new-session chain:\n%s", got)
	}
}

// TestWrapper_selects_ai_pane_geometrically verifies the wrapper focuses panes
// by direction (-L / -R) instead of fixed indices. tmux routes external
// drag-drops (e.g. a screenshot) to the ACTIVE pane, so the AI pane must end up
// active for a dropped screenshot to land in the AI tool. Fixed indices
// (select-pane -t 0 / -t 2) silently target the wrong pane under a non-zero
// pane-base-index; directional selection is robust to any base-index.
func TestWrapper_selects_ai_pane_geometrically(t *testing.T) {
	got := recordWrapperNewSession(t)
	if !strings.Contains(got, "select-pane -L") {
		t.Errorf("expected directional 'select-pane -L' to focus the left column; got:\n%s", got)
	}
	if !strings.Contains(got, "select-pane -R") {
		t.Errorf("expected directional 'select-pane -R' to leave the AI (right) pane active; got:\n%s", got)
	}
	if strings.Contains(got, "select-pane -t 0") || strings.Contains(got, "select-pane -t 2") {
		t.Errorf("fixed-index select-pane breaks under non-zero pane-base-index; use directional selection. got:\n%s", got)
	}
}

// TestWrapper_spare_pane_runs_tabbed_tmux verifies the spare bottom-left pane
// launches a nested tmux (the tab bar) instead of a bare shell, and that the
// tab keybindings (add/close) are wired on the outer session.
func TestWrapper_spare_pane_runs_tabbed_tmux(t *testing.T) {
	got := recordWrapperNewSession(t)
	if !strings.Contains(got, "split-window -v -p 45") {
		t.Fatalf("expected the spare pane split; got:\n%s", got)
	}
	for _, want := range []string{
		"env -u TMUX -u TMUX_PANE tmux -L gtspare_", // nested server, $TMUX shed
		"new-session",              // the inner session that hosts the tabs
		"|| exec bash",             // graceful fallback if tmux is unavailable
		"bind-key t ",              // keyboard: add a tab
		"bind-key w ",              // keyboard: close current tab
		"spare_tabs_close_current", // close routes through the guarded helper
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected new-session chain to contain %q; got:\n%s", want, got)
		}
	}
}

// TestWrapper_marks_ai_pane locks in the @gt_ai marker on the AI pane, which
// lib/screenshot.sh uses to resolve the AI pane for prefix+i injection.
func TestWrapper_marks_ai_pane(t *testing.T) {
	got := recordWrapperNewSession(t)
	if !strings.Contains(got, "set-option -p @gt_ai 1") {
		t.Errorf("expected the AI pane to be marked with '@gt_ai 1'; got:\n%s", got)
	}
}

// TestWrapper_active_pane_border_is_visible verifies the active pane has a
// distinct border. Without this, the active and inactive borders look
// identical, so a user can't tell which pane is focused -- and a screenshot
// dropped onto a non-AI active pane silently fails to reach the AI tool.
func TestWrapper_active_pane_border_is_visible(t *testing.T) {
	got := recordWrapperNewSession(t)
	if !strings.Contains(got, "pane-active-border-style") {
		t.Errorf("expected new-session to set a distinct pane-active-border-style; got:\n%s", got)
	}
}
