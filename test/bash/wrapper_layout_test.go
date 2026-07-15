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
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
		"tput": `#!/bin/bash
case "$1" in
  cols) echo 173 ;;
  lines) echo 47 ;;
  colors) echo 256 ;;
esac
`,
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

// OpenCode launches must cross the strict Go adapter boundary once a semantic
// attention generation exists.
func TestWrapperOpenCode_routes_new_session_through_strict_adapter(t *testing.T) {
	launch := recordWrapperNewSessionForTool(t, "opencode")
	for _, fragment := range []string{"opencode-adapter", "--state-file", "--generation", "-- /"} {
		if !strings.Contains(launch, fragment) {
			t.Fatalf("OpenCode tmux launch is missing %q:\n%s", fragment, launch)
		}
	}
}

// Exact legacy sound-plugin retirement happens before attention generation
// creation; a retirement error therefore cannot rotate or publish runtime state.
func TestWrapperOpenCode_retires_known_sound_plugins_before_attention_generation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	retire := strings.Index(content, "retire_known_opencode_sound_plugins")
	generation := strings.Index(content, "attention_session_create")
	if retire < 0 || generation < 0 || retire >= generation {
		t.Fatalf("OpenCode retirement must precede attention generation: retire=%d generation=%d", retire, generation)
	}
}

func TestWrapperPlainTerminal_has_no_plugin_install_boundary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	start := strings.Index(content, "plain-terminal)")
	end := strings.Index(content[start:], "add-worktree)")
	if start < 0 || end < 0 {
		t.Fatal("cannot locate plain-terminal action")
	}
	block := content[start : start+end]
	if strings.Contains(block, "opencode") || strings.Contains(block, "plugin") {
		t.Fatalf("plain terminal unexpectedly mutates OpenCode configuration:\n%s", block)
	}
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

// TestWrapper_default_panel_is_compact verifies the left pane runs the compact
// changeset ledger — the only panel the wrapper builds.
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

// TestWrapperBuildsDetachedSessionBeforeAttach locks down the startup frame:
// tmux must finish the complete workspace off-screen, focus the AI pane, and
// only then attach the client. exit-unattached is deliberately enabled after
// attachment because setting it on a clientless detached session kills it.
func TestWrapperBuildsDetachedSessionBeforeAttach(t *testing.T) {
	got := recordWrapperNewSession(t)

	for _, want := range []string{"new-session -d", "-x 173", "-y 47"} {
		if !strings.Contains(got, want) {
			t.Errorf("detached launch is missing %q:\n%s", want, got)
		}
	}

	horizontal := strings.Index(got, "split-window -h -p 75")
	vertical := strings.Index(got, "split-window -v -p 45")
	focusAI := strings.LastIndex(got, "select-pane -R")
	attach := strings.Index(got, "attach-session")
	exitUnattached := strings.Index(got, "set-option exit-unattached on")
	if horizontal < 0 || vertical < 0 || focusAI < 0 || attach < 0 || exitUnattached < 0 {
		t.Fatalf("launch queue is missing a required lifecycle command:\n%s", got)
	}
	if !(horizontal < vertical && vertical < focusAI && focusAI < attach && attach < exitUnattached) {
		t.Fatalf("workspace must be built and focused before attach, then arm teardown:\n%s", got)
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
