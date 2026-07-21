package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/muesli/termenv"
)

// --- Disable toggle (x) ---

func TestAIToolsPanel_x_toggles_disabled_and_persists(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")

	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !m.aiToolRows[0].Disabled {
		t.Error("x must mark the focused tool disabled")
	}
	if !models.LoadDisabledTools(m.disabledToolsFile)["codex"] {
		t.Error("disabled state must persist to the file")
	}

	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.aiToolRows[0].Disabled {
		t.Error("x again must re-enable the tool")
	}
	if models.LoadDisabledTools(m.disabledToolsFile)["codex"] {
		t.Error("re-enabling must persist too")
	}
}

func TestAIToolsPanel_disabling_the_default_falls_back_to_first_enabled(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true},
	)
	m.aiTools = []string{"claude", "codex"}
	m.selectedAI = 1 // codex is the default
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")
	m.aiToolFile = filepath.Join(t.TempDir(), "ai-tool")

	m.aiToolsCursor = 1
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if got := m.CurrentAITool(); got != "claude" {
		t.Errorf("default after disabling it = %q, want claude", got)
	}
}

func TestAIToolsPanel_d_is_a_noop_for_disabled_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true, Disabled: true},
	)
	m.aiTools = []string{"claude", "codex"}
	m.aiToolsCursor = 1
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := m.CurrentAITool(); got != "claude" {
		t.Errorf("default = %q, want claude (a disabled tool cannot become default)", got)
	}
}

func TestAIToolsPanel_render_shows_disabled_tag(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true, Disabled: true})
	out := m.renderAIToolsPanel()
	if !strings.Contains(out, "disabled") {
		t.Errorf("panel must tag a disabled row, got:\n%s", out)
	}
}

func TestAIToolsPanel_x_refuses_to_disable_the_last_enabled_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true, Disabled: true},
	)
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")
	if err := os.WriteFile(m.disabledToolsFile, []byte("codex\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m.aiToolsCursor = 0 // claude: the last enabled installed tool
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.aiToolRows[0].Disabled {
		t.Error("the last enabled tool must not become disabled")
	}
	if models.LoadDisabledTools(m.disabledToolsFile)["claude"] {
		t.Error("the sidecar file must stay untouched")
	}
	if m.aiToolsErr == nil || !strings.Contains(m.aiToolsErr.Error(), "At least one AI tool must stay enabled") {
		t.Errorf("aiToolsErr = %v, want the last-enabled message", m.aiToolsErr)
	}
}

func TestAIToolsPanel_x_still_disables_with_an_enabled_peer(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true},
	)
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")

	m.aiToolsCursor = 1
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if !m.aiToolRows[1].Disabled {
		t.Error("disabling with another enabled tool present must work")
	}
	if m.aiToolsErr != nil {
		t.Errorf("unexpected error: %v", m.aiToolsErr)
	}
}

func TestAIToolsPanel_x_always_reenables(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true, Disabled: true})
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")
	if err := os.WriteFile(m.disabledToolsFile, []byte("codex\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.aiToolRows[0].Disabled {
		t.Error("re-enabling must never be blocked")
	}
}

func TestAIToolsPanel_disabled_tool_bullet_is_not_green(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	greenBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render("●")
	grayBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("●")

	m := panelMenu(t, models.AITool{Name: "codex", Installed: true, Disabled: true})
	out := m.renderAIToolsPanel()
	if strings.Contains(out, greenBullet) {
		t.Error("a disabled tool must not show the green installed bullet")
	}
	if !strings.Contains(out, grayBullet) {
		t.Error("a disabled tool must show a gray bullet")
	}

	m = panelMenu(t, models.AITool{Name: "codex", Installed: true})
	if out := m.renderAIToolsPanel(); !strings.Contains(out, greenBullet) {
		t.Error("an enabled installed tool must keep the green bullet")
	}
}

func TestAIToolsPanel_help_shows_enable_when_focused_tool_disabled(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true, Disabled: true})
	out := stripAnsi(m.renderAIToolsPanel())
	if !strings.Contains(out, "x enable") {
		t.Errorf("help must offer 'x enable' on a disabled focused tool, got:\n%s", out)
	}
	if strings.Contains(out, "x disable") {
		t.Errorf("help must not still say 'x disable', got:\n%s", out)
	}
}

func TestAIToolsPanel_help_shows_disable_when_focused_tool_enabled(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	out := stripAnsi(m.renderAIToolsPanel())
	if !strings.Contains(out, "x disable") {
		t.Errorf("help must offer 'x disable' on an enabled focused tool, got:\n%s", out)
	}
}

func TestAIToolsPanel_detect_overlays_disabled_state(t *testing.T) {
	m := panelMenu(t)
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")
	if _, err := models.ToggleDisabledTool(m.disabledToolsFile, "codex"); err != nil {
		t.Fatal(err)
	}
	m.detectAITools = func() []models.AITool {
		return []models.AITool{{Name: "claude", Installed: true}, {Name: "codex", Installed: true}}
	}
	m.openAIToolsPanel()
	var codex *models.AITool
	for i := range m.aiToolRows {
		if m.aiToolRows[i].Name == "codex" {
			codex = &m.aiToolRows[i]
		}
	}
	if codex == nil || !codex.Disabled {
		t.Errorf("rows %v must mark codex disabled from the file", m.aiToolRows)
	}
}

func TestSetDisabledToolsFile(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetDisabledToolsFile("/cfg/disabled-tools")
	if m.disabledToolsFile != "/cfg/disabled-tools" {
		t.Errorf("disabledToolsFile = %q", m.disabledToolsFile)
	}
}

// --- removeCmdFor ---

func TestRemoveCmdFor_shells_into_the_bash_removers(t *testing.T) {
	for _, tc := range []struct{ tool, fn string }{
		{"opencode", "remove_opencode"},
		{"codex", "remove_codex"},
	} {
		cmd, err := removeCmdFor(tc.tool, "/libs")
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.tool, err)
		}
		joined := strings.Join(cmd.Args, " ")
		if !strings.Contains(joined, tc.fn) {
			t.Errorf("%s: command %q must call %s", tc.tool, joined, tc.fn)
		}
		var found bool
		for _, e := range cmd.Env {
			if e == "WISP_DECK_LIB_DIR=/libs" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: WISP_DECK_LIB_DIR=/libs must be in the command env", tc.tool)
		}
	}
}

func TestRemoveCmdFor_refuses_claude_and_unknown_tools(t *testing.T) {
	for _, tool := range []string{"claude", "bogus", ""} {
		if _, err := removeCmdFor(tool, "/libs"); err == nil {
			t.Errorf("removeCmdFor(%q) must return an error", tool)
		}
	}
}

// --- Remove modal (r) ---

func TestAIToolsPanel_r_opens_warning_modal_only_for_removable_installed_tools(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true},
		models.AITool{Name: "opencode"}, // not installed
	)
	m.libDir = "/libs"

	m.aiToolsCursor = 0 // claude: never removable
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.aiToolRemovePending != "" {
		t.Error("r on claude must not open the modal")
	}

	m.aiToolsCursor = 2 // not installed
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.aiToolRemovePending != "" {
		t.Error("r on a missing tool must not open the modal")
	}

	m.aiToolsCursor = 1 // installed codex
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.aiToolRemovePending != "codex" {
		t.Errorf("aiToolRemovePending = %q, want codex", m.aiToolRemovePending)
	}
}

func TestAIToolsPanel_modal_renders_warning(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiToolRemovePending = "codex"
	out := m.renderAIToolsPanel()
	if !strings.Contains(out, "Remove Codex?") {
		t.Errorf("modal must warn about removing Codex, got:\n%s", out)
	}
	if !strings.Contains(out, "npm uninstall -g @openai/codex") {
		t.Errorf("modal must show the exact command, got:\n%s", out)
	}
}

func TestAIToolsPanel_modal_for_opencode_mentions_npx(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "opencode", Installed: true})
	m.aiToolRemovePending = "opencode"
	out := m.renderAIToolsPanel()
	if !strings.Contains(out, "npx") {
		t.Errorf("opencode modal must mention the npx fallback, got:\n%s", out)
	}
}

func TestAIToolsPanel_modal_esc_cancels(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiToolRemovePending = "codex"
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEsc})
	if m.aiToolRemovePending != "" {
		t.Error("esc must cancel the remove modal")
	}
	if m.aiToolsPanelOpen != true {
		t.Error("cancelling the modal must keep the panel open")
	}
	if m.aiToolRemoving != "" {
		t.Error("cancelling must not start a removal")
	}
}

func TestAIToolsPanel_modal_enter_starts_background_removal(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.libDir = "/libs"
	m.aiToolRemovePending = "codex"
	_, cmd := m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirming must issue a removal command")
	}
	if m.aiToolRemovePending != "" {
		t.Error("confirming must close the modal")
	}
	if m.aiToolRemoving != "codex" {
		t.Errorf("aiToolRemoving = %q, want codex", m.aiToolRemoving)
	}
}

func TestAIToolsPanel_removing_row_shows_progress_bar(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiToolRemoving = "codex"
	m.aiToolInstallPct = 0.5
	out := m.renderAIToolsPanel()
	if !strings.Contains(out, "%") {
		t.Errorf("a removing row must show the progress bar, got:\n%s", out)
	}
	if !m.applyInstallTick() {
		t.Error("the progress ticker must keep running during a removal")
	}
}

func TestAIToolRemoveDone_success_refreshes_and_drops_from_available(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiTools = []string{"claude", "codex"}
	m.selectedAI = 1
	m.aiToolRemoving = "codex"
	m.detectAITools = func() []models.AITool {
		return []models.AITool{{Name: "claude", Installed: true}, {Name: "codex"}}
	}

	m.applyAIToolRemoveDone(aiToolRemoveDoneMsg{tool: "codex"})

	if m.aiToolRemoving != "" {
		t.Error("removing marker must clear")
	}
	for _, tool := range m.aiTools {
		if tool == "codex" {
			t.Errorf("aiTools = %v, codex must not stay selectable after removal", m.aiTools)
		}
	}
	if got := m.CurrentAITool(); got != "claude" {
		t.Errorf("default after removal = %q, want claude", got)
	}
	if m.feedbackStyle != "success" || !strings.Contains(m.feedbackMsg, "Codex") {
		t.Errorf("feedback = %q/%q, want a success mentioning Codex", m.feedbackStyle, m.feedbackMsg)
	}
}

func TestAIToolRemoveDone_failure_reports_error(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiTools = []string{"claude", "codex"}
	m.aiToolRemoving = "codex"
	m.detectAITools = func() []models.AITool {
		return []models.AITool{{Name: "codex", Installed: true}}
	}

	m.applyAIToolRemoveDone(aiToolRemoveDoneMsg{tool: "codex", err: errors.New("npm exploded")})

	if m.aiToolRemoving != "" {
		t.Error("removing marker must clear even on failure")
	}
	if m.feedbackStyle != "error" || !strings.Contains(m.feedbackMsg, "Codex") {
		t.Errorf("feedback = %q/%q, want an error mentioning Codex", m.feedbackStyle, m.feedbackMsg)
	}
	var stillListed bool
	for _, tool := range m.aiTools {
		if tool == "codex" {
			stillListed = true
		}
	}
	if !stillListed {
		t.Error("a failed removal must keep codex selectable")
	}
}

func TestAIToolsPanel_r_is_a_noop_while_a_removal_or_install_runs(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "codex", Installed: true},
		models.AITool{Name: "opencode", Installed: true},
	)
	m.libDir = "/libs"
	m.aiToolRemoving = "opencode"
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.aiToolRemovePending != "" {
		t.Error("r must be ignored while another removal runs")
	}

	m.aiToolRemoving = ""
	m.aiToolInstalling = "opencode"
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.aiToolRemovePending != "" {
		t.Error("r must be ignored while an install runs")
	}
}
