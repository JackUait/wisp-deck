package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover live propagation of Settings-menu changes into ALL
// already-running sessions. Each session's tab-title-watcher re-reads the
// settings file every poll tick and re-applies the theme accent + tab-title
// mode, so a toggle in the menu reaches every open window without a relaunch.

// --- read_settings_value: parse a key=value line from the settings file ---

func TestLiveSettings_read_settings_value_returns_value(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "settings", "animation=on\ntab_title=model\ntheme=purple\n")
	settings := filepath.Join(dir, "settings")

	out, code := runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "theme"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "purple" {
		t.Errorf("expected 'purple', got %q", strings.TrimSpace(out))
	}
}

func TestLiveSettings_read_settings_value_empty_when_key_absent(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "settings", "animation=on\n")
	settings := filepath.Join(dir, "settings")

	out, code := runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "theme"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty for absent key, got %q", strings.TrimSpace(out))
	}
}

func TestLiveSettings_read_settings_value_empty_when_file_missing(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "no-such-settings")

	out, code := runBashFunc(t, "lib/tab-title-watcher.sh", "read_settings_value",
		[]string{settings, "theme"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty when file missing, got %q", strings.TrimSpace(out))
	}
}

// --- apply_session_theme: re-paint a running session's chrome ---

// recordingTmux records the full arg string of every invocation to $GT_REC.
const recordingTmux = `#!/bin/bash
printf '%s\n' "$*" >> "$GT_REC"
exit 0
`

func TestLiveSettings_apply_session_theme_sets_pane_border(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", recordingTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	tmuxPath := filepath.Join(binDir, "tmux")

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	snippet := fmt.Sprintf("source %q && source %q && apply_session_theme %q dev-test-1 141",
		tuiPath, watcherPath, tmuxPath)

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "pane-active-border-style")
	assertContains(t, got, "fg=colour141")
	assertContains(t, got, "dev-test-1")
}

// When lib/spare-tabs.sh is also loaded, apply_session_theme must also repaint
// the nested spare-pane tab bar so the whole window stays one colour.
func TestLiveSettings_apply_session_theme_repaints_spare_chip(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", recordingTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	tmuxPath := filepath.Join(binDir, "tmux")

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	sparePath := filepath.Join(root, "lib", "spare-tabs.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	snippet := fmt.Sprintf("source %q && source %q && source %q && apply_session_theme %q dev-test-1 141",
		tuiPath, sparePath, watcherPath, tmuxPath)

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	// Inner spare tmux addressed by its -L socket, status-left repainted purple.
	assertContains(t, got, "gtspare_dev-test-1")
	assertContains(t, got, "status-left")
	assertContains(t, got, "colour141")
}

// --- apply_theme_to_all_sessions: repaint EVERY active session ---

// A theme change must reach every running ghost-tab session, not only those
// whose watcher loop was started with the live-theme code. apply_theme_to_all_sessions
// addresses each session externally: it enumerates tmux sessions, skips non
// ghost-tab ones, and resolves each session's accent from its own AI tool (the
// GHOST_TAB_TOOL env captured at launch) so an "auto"/unset theme still picks the
// right hue per session.
const allSessionsTmux = `#!/bin/bash
printf '%s\n' "$*" >> "$GT_REC"
case "$1" in
  list-sessions) printf '%s\n' "dev-alpha-1" "dev-beta-2" "plain-3" ;;
  show-environment)
    sess="$3"; var="$4"
    [ "$sess" = "plain-3" ] && exit 1   # not a ghost-tab session
    case "$var" in
      GHOST_TAB) exit 0 ;;
      GHOST_TAB_TOOL)
        case "$sess" in
          dev-alpha-1) echo "GHOST_TAB_TOOL=claude" ;;
          dev-beta-2)  echo "GHOST_TAB_TOOL=opencode" ;;
        esac ;;
    esac ;;
esac
exit 0
`

func TestLiveSettings_apply_theme_to_all_sessions_per_tool(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	// theme unset -> resolve per tool: claude->orange(209), opencode->purple(141).
	writeTempFile(t, dir, "settings", "animation=on\ntab_title=full\n")
	settings := filepath.Join(dir, "settings")
	binDir := mockCommand(t, dir, "tmux", allSessionsTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	tmuxPath := filepath.Join(binDir, "tmux")

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	themePath := filepath.Join(root, "lib", "theme.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	snippet := fmt.Sprintf("source %q && source %q && source %q && apply_theme_to_all_sessions %q %q",
		tuiPath, themePath, watcherPath, tmuxPath, settings)

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "set-option -t dev-alpha-1 pane-active-border-style fg=colour209")
	assertContains(t, got, "set-option -t dev-beta-2 pane-active-border-style fg=colour141")
	// The non-ghost-tab session must never be touched.
	assertNotContains(t, got, "set-option -t plain-3")
}

func TestLiveSettings_apply_theme_to_all_sessions_named_preset(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	// A named preset wins for every session regardless of its tool.
	writeTempFile(t, dir, "settings", "theme=purple\n")
	settings := filepath.Join(dir, "settings")
	binDir := mockCommand(t, dir, "tmux", allSessionsTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	tmuxPath := filepath.Join(binDir, "tmux")

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	themePath := filepath.Join(root, "lib", "theme.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	snippet := fmt.Sprintf("source %q && source %q && source %q && apply_theme_to_all_sessions %q %q",
		tuiPath, themePath, watcherPath, tmuxPath, settings)

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "set-option -t dev-alpha-1 pane-active-border-style fg=colour141")
	assertContains(t, got, "set-option -t dev-beta-2 pane-active-border-style fg=colour141")
	assertNotContains(t, got, "set-option -t plain-3")
}

// --- apply_session_panel_mode: live compact <-> full switch ---

// Mock tmux for panel-mode tests: reports the session's current mode as
// "compact", a 3-pane layout (ledger top-left=0, spare bottom-left=1, AI
// right=2), and a 100-col window so resize math is exact (75/50 cells).
const panelModeTmux = `#!/bin/bash
printf '%s\n' "$*" >> "$GT_REC"
case "$1" in
  show-options)   echo "compact" ;;           # current @gt_panel_mode
  list-panes)     printf '%s\n' "0 0 0" "1 0 20" "2 47 0" ;;
  display-message) echo "100" ;;               # window_width
esac
exit 0
`

func runPanelMode(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", panelModeTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	tmuxPath := filepath.Join(binDir, "tmux")

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	snippet := fmt.Sprintf(
		"source %q && source %q && apply_session_panel_mode %q dev-x %q /proj /libdir /usr/bin/lazygit",
		tuiPath, watcherPath, tmuxPath, mode)

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(rec)
	return string(data)
}

func TestLiveSettings_panel_mode_switch_to_full_respawns_and_resizes(t *testing.T) {
	got := runPanelMode(t, "full")
	// Ledger pane (top-left, index 0) respawned with lazygit.
	assertContains(t, got, "respawn-pane")
	assertContains(t, got, "dev-x:0.0")
	assertContains(t, got, "lazygit")
	// AI pane (rightmost, index 2) resized to 50% of 100 cols.
	assertContains(t, got, "resize-pane -t dev-x:0.2 -x 50")
	// Session re-tagged so a later sync sees the new mode.
	assertContains(t, got, "@gt_panel_mode full")
}

func TestLiveSettings_panel_mode_switch_to_compact_uses_compact_view(t *testing.T) {
	// Current mode is "compact" (mock), so requesting compact must be a no-op:
	// it may read @gt_panel_mode to detect drift, but must not respawn or re-tag.
	got := runPanelMode(t, "compact")
	assertNotContains(t, got, "respawn-pane")
	assertNotContains(t, got, "set-option -t dev-x @gt_panel_mode")
}

// --- apply_settings_to_all_sessions: theme + panel_mode across every session ---

const allSettingsTmux = `#!/bin/bash
printf '%s\n' "$*" >> "$GT_REC"
case "$1" in
  list-sessions) printf '%s\n' "dev-alpha-1" "plain-2" ;;
  show-environment)
    sess="$3"; var="$4"
    [ "$sess" = "plain-2" ] && exit 1
    case "$var" in
      GHOST_TAB)      exit 0 ;;
      GHOST_TAB_TOOL) echo "GHOST_TAB_TOOL=claude" ;;
      GHOST_TAB_PATH) echo "GHOST_TAB_PATH=/proj/alpha" ;;
    esac ;;
  show-options)    echo "compact" ;;
  list-panes)      printf '%s\n' "0 0 0" "1 0 20" "2 47 0" ;;
  display-message) echo "100" ;;
esac
exit 0
`

func TestLiveSettings_apply_settings_to_all_sessions_theme_and_panel(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	writeTempFile(t, dir, "settings", "theme=purple\npanel_mode=full\n")
	settings := filepath.Join(dir, "settings")
	binDir := mockCommand(t, dir, "tmux", allSettingsTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)
	tmuxPath := filepath.Join(binDir, "tmux")

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	themePath := filepath.Join(root, "lib", "theme.sh")
	watcherPath := filepath.Join(root, "lib", "tab-title-watcher.sh")
	snippet := fmt.Sprintf("source %q && source %q && source %q && apply_settings_to_all_sessions %q %q /libdir /usr/bin/lazygit",
		tuiPath, themePath, watcherPath, tmuxPath, settings)

	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	// Theme accent applied to the ghost-tab session.
	assertContains(t, got, "set-option -t dev-alpha-1 pane-active-border-style fg=colour141")
	// panel_mode=full respawned its ledger pane to lazygit.
	assertContains(t, got, "respawn-pane")
	assertContains(t, got, "lazygit")
	// The non-ghost-tab session is probed but never acted on.
	assertNotContains(t, got, "set-option -t plain-2")
	assertNotContains(t, got, "respawn-pane -k -t plain-2")
}

// --- spare_tabs_status_left: the reusable status-left builder ---

func TestSpareTabs_status_left_uses_accent(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_status_left",
		[]string{"141"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "colour141")
	assertContains(t, out, "range=user|new") // + button still present
	assertContains(t, out, "#{window_index}")
}

func TestSpareTabs_status_left_defaults_to_orange(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_status_left",
		[]string{}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "colour209")
}

// spare_tabs_set_accent repaints a running inner tmux's tab bar with a new accent.
func TestSpareTabs_set_accent_repaints_inner_bar(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", recordingTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)

	_, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_set_accent",
		[]string{"gtspare_x", "78"}, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "gtspare_x")
	assertContains(t, got, "status-left")
	assertContains(t, got, "colour78")
}

// --- watcher loop wiring: live re-read each tick ---

func TestLiveSettings_watcher_loop_rereads_settings_live(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "lib", "tab-title-watcher.sh"))
	if err != nil {
		t.Fatalf("failed to read tab-title-watcher.sh: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "read_settings_value") {
		t.Error("watcher loop should re-read settings live via read_settings_value")
	}
	if !strings.Contains(content, "apply_session_theme") {
		t.Error("watcher loop should re-apply the theme live via apply_session_theme")
	}
	// The tab-title mode must be read live (not only the frozen launch arg), so
	// a mid-session change reaches the running watcher.
	if !strings.Contains(content, "cur_tab_title") {
		t.Error("watcher loop should track a live tab-title value (cur_tab_title), not only the frozen launch arg")
	}
}

// spare_tabs_config must build its status-left through spare_tabs_status_left so
// the launch-time bar and the live-repaint path share one definition.
func TestSpareTabs_config_uses_status_left_helper(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "lib", "spare-tabs.sh"))
	if err != nil {
		t.Fatalf("failed to read spare-tabs.sh: %v", err)
	}
	if !strings.Contains(string(data), "spare_tabs_status_left") {
		t.Error("spare_tabs_config should build status-left via spare_tabs_status_left (shared with live repaint)")
	}
}
