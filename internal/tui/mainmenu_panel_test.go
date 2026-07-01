package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/models"
)

func TestCyclePanelMode_compactToLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.PanelMode() != "compact" {
		t.Fatalf("default panel mode should be compact, got %q", m.PanelMode())
	}
	m.CyclePanelMode()
	if m.PanelMode() != "lazygit" {
		t.Errorf("expected lazygit after cycling from compact, got %q", m.PanelMode())
	}
}

func TestCyclePanelMode_lazygitToCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CyclePanelMode() // -> lazygit
	m.CyclePanelMode() // -> compact
	if m.PanelMode() != "compact" {
		t.Errorf("expected compact after cycling from lazygit, got %q", m.PanelMode())
	}
}

func TestCyclePanelModeReverse_compactToLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CyclePanelModeReverse()
	if m.PanelMode() != "lazygit" {
		t.Errorf("expected lazygit (reverse from compact), got %q", m.PanelMode())
	}
}

func TestCyclePanelModeReverse_lazygitToCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CyclePanelMode()        // -> lazygit
	m.CyclePanelModeReverse() // -> compact
	if m.PanelMode() != "compact" {
		t.Errorf("expected compact (reverse from lazygit), got %q", m.PanelMode())
	}
}

func TestPanelMode_defaultIsCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.PanelMode() != "compact" {
		t.Errorf("default panel mode = %q, want compact", m.PanelMode())
	}
}

func TestRenderSettingsBox_hasPanelRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "Side panel") {
		t.Errorf("settings box missing Panel row:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowShowsCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "[Compact]") {
		t.Errorf("settings box should show [Compact] by default:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowShowsLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.CyclePanelMode() // -> lazygit
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "[lazygit]") {
		t.Errorf("settings box should show [lazygit] after cycling:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowColor_greenForCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// compact = default = green (114)
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	expected := greenStyle.Render("Compact")
	if !strings.Contains(out, expected) {
		t.Errorf("panel row should use green for compact:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowColor_yellowForLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.CyclePanelMode() // -> lazygit
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// lazygit = non-default = yellow (220)
	yellowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	expected := yellowStyle.Render("lazygit")
	if !strings.Contains(out, expected) {
		t.Errorf("panel row should use yellow for lazygit:\n%s", out)
	}
}

func TestSettingsItemCount_includesPanelRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"opencode"}, "opencode", "animated")
	// The Plan + Login + Auto-switch rows are shared across agents, so
	// opencode also has 9 items (ghost, tab title, sound, panel, projects dir,
	// plan, login, auto-switch accounts).
	if m.settingsItemCount() != 9 {
		t.Errorf("settingsItemCount = %d, want 9", m.settingsItemCount())
	}
}

func TestSettingsItemCount_withClaudeConfig_includesPanelRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "Pro", File: "pro.json"}})
	m.SetActiveClaudeConfig("pro.json")
	// With Claude config: 7 items (incl. Plan + Login)
	if m.settingsItemCount() != 9 {
		t.Errorf("settingsItemCount = %d, want 9", m.settingsItemCount())
	}
}

func TestSettingsValueRight_panelCycles(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = 3 // Panel row
	m.settingsValueRight()
	if m.PanelMode() != "lazygit" {
		t.Errorf("expected lazygit after right on panel row, got %q", m.PanelMode())
	}
}

func TestSettingsValueLeft_panelCycles(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = 3 // Panel row
	m.settingsValueLeft()
	if m.PanelMode() != "lazygit" {
		t.Errorf("expected lazygit (reverse) after left on panel row, got %q", m.PanelMode())
	}
}

func TestMainMenuResult_includesPanelMode_whenChanged(t *testing.T) {
	projects := []models.Project{{Name: "p", Path: "/tmp/p"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.CyclePanelMode() // -> lazygit (changed from default)
	m.selectCurrent()
	r := m.Result()
	if r == nil {
		t.Fatal("result should not be nil")
	}
	if r.PanelMode != "lazygit" {
		t.Errorf("result.PanelMode = %q, want lazygit", r.PanelMode)
	}
}

func TestMainMenuResult_omitsPanelMode_whenUnchanged(t *testing.T) {
	projects := []models.Project{{Name: "p", Path: "/tmp/p"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.selectCurrent()
	r := m.Result()
	if r == nil {
		t.Fatal("result should not be nil")
	}
	if r.PanelMode != "" {
		t.Errorf("result.PanelMode should be empty when unchanged, got %q", r.PanelMode)
	}
}
