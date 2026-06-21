package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/models"
)

func TestCyclePanelMode_lazygitToCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.PanelMode() != "lazygit" {
		t.Fatalf("default panel mode should be lazygit, got %q", m.PanelMode())
	}
	m.CyclePanelMode()
	if m.PanelMode() != "compact" {
		t.Errorf("expected compact after cycling from lazygit, got %q", m.PanelMode())
	}
}

func TestCyclePanelMode_compactToLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CyclePanelMode() // -> compact
	m.CyclePanelMode() // -> lazygit
	if m.PanelMode() != "lazygit" {
		t.Errorf("expected lazygit after cycling from compact, got %q", m.PanelMode())
	}
}

func TestCyclePanelModeReverse_lazygitToCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CyclePanelModeReverse()
	if m.PanelMode() != "compact" {
		t.Errorf("expected compact (reverse from lazygit), got %q", m.PanelMode())
	}
}

func TestCyclePanelModeReverse_compactToLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CyclePanelMode()        // -> compact
	m.CyclePanelModeReverse() // -> lazygit
	if m.PanelMode() != "lazygit" {
		t.Errorf("expected lazygit (reverse from compact), got %q", m.PanelMode())
	}
}

func TestPanelMode_defaultIsLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.PanelMode() != "lazygit" {
		t.Errorf("default panel mode = %q, want lazygit", m.PanelMode())
	}
}

func TestRenderSettingsBox_hasPanelRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "Panel") {
		t.Errorf("settings box missing Panel row:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowShowsLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "[lazygit]") {
		t.Errorf("settings box should show [lazygit] by default:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowShowsCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.CyclePanelMode() // -> compact
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "[Compact]") {
		t.Errorf("settings box should show [Compact] after cycling:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowColor_greenForLazygit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// lazygit = default = green (114)
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	expected := greenStyle.Render("lazygit")
	if !strings.Contains(out, expected) {
		t.Errorf("panel row should use green for lazygit:\n%s", out)
	}
}

func TestRenderSettingsBox_panelRowColor_yellowForCompact(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.CyclePanelMode() // -> compact
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// compact = non-default = yellow (220)
	yellowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	expected := yellowStyle.Render("Compact")
	if !strings.Contains(out, expected) {
		t.Errorf("panel row should use yellow for compact:\n%s", out)
	}
}

func TestSettingsItemCount_includesPanelRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"opencode"}, "opencode", "animated")
	// The subscription row is shared across agents, so opencode also has 6 items
	// (ghost, tab title, sound, panel, projects dir, subscription).
	if m.settingsItemCount() != 6 {
		t.Errorf("settingsItemCount = %d, want 6", m.settingsItemCount())
	}
}

func TestSettingsItemCount_withClaudeConfig_includesPanelRow(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "Pro", File: "pro.json"}})
	m.SetActiveClaudeConfig("pro.json")
	// With Claude config: 6 items
	if m.settingsItemCount() != 6 {
		t.Errorf("settingsItemCount = %d, want 6", m.settingsItemCount())
	}
}

func TestSettingsValueRight_panelCycles(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = 3 // Panel row
	m.settingsValueRight()
	if m.PanelMode() != "compact" {
		t.Errorf("expected compact after right on panel row, got %q", m.PanelMode())
	}
}

func TestSettingsValueLeft_panelCycles(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = 3 // Panel row
	m.settingsValueLeft()
	if m.PanelMode() != "compact" {
		t.Errorf("expected compact (reverse) after left on panel row, got %q", m.PanelMode())
	}
}

func TestMainMenuResult_includesPanelMode_whenChanged(t *testing.T) {
	projects := []models.Project{{Name: "p", Path: "/tmp/p"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.CyclePanelMode() // -> compact (changed from default)
	m.selectCurrent()
	r := m.Result()
	if r == nil {
		t.Fatal("result should not be nil")
	}
	if r.PanelMode != "compact" {
		t.Errorf("result.PanelMode = %q, want compact", r.PanelMode)
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
