package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func newClaudeMenu(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "codex"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "Work", File: "work.json"}, {Name: "Personal", File: "personal.json"}})
	m.SetActiveClaudeConfig("")
	return m, ptr
}

func TestClaudeConfig_starts_standard(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if m.CurrentClaudeConfigName() != "Standard Claude" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
	if m.CurrentClaudeConfigFile() != "" {
		t.Fatalf("standard should have empty file")
	}
}

func TestClaudeConfig_cycle_wraps_and_persists(t *testing.T) {
	m, ptr := newClaudeMenu(t)
	m.CycleClaudeConfig("next") // Work
	if m.CurrentClaudeConfigName() != "Work" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
	data, _ := os.ReadFile(ptr)
	if strings.TrimSpace(string(data)) != "work.json" {
		t.Fatalf("pointer = %q", string(data))
	}
	m.CycleClaudeConfig("next") // Personal
	m.CycleClaudeConfig("next") // wrap to Standard
	if m.CurrentClaudeConfigName() != "Standard Claude" {
		t.Fatalf("expected wrap to Standard, got %q", m.CurrentClaudeConfigName())
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("standard should clear pointer")
	}
}

func TestClaudeConfig_prev_from_standard_to_last(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.CycleClaudeConfig("prev")
	if m.CurrentClaudeConfigName() != "Personal" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
}

func TestClaudeConfig_active_preselected(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.SetActiveClaudeConfig("personal.json")
	if m.CurrentClaudeConfigName() != "Personal" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
}

func TestClaudeConfig_visibility_follows_tool(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if !m.ClaudeConfigVisible() {
		t.Fatal("should be visible for claude")
	}
	m.CycleAITool("next") // -> codex
	if m.ClaudeConfigVisible() {
		t.Fatal("should hide for non-claude")
	}
}

func TestSettings_shows_config_row_for_claude(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.OpenSettings()
	view := m.renderSettingsForTest()
	if !strings.Contains(view, "Config") {
		t.Fatalf("settings should show Config row:\n%s", view)
	}
	if !strings.Contains(view, "Standard Claude") {
		t.Fatalf("should show current config name")
	}
}

func TestSettings_hides_config_row_for_non_claude(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.CycleAITool("next") // codex
	m.OpenSettings()
	view := m.renderSettingsForTest()
	if strings.Contains(view, "Config") {
		t.Fatalf("config row must be hidden for non-claude:\n%s", view)
	}
}

func TestSettings_nav_count_includes_config_when_visible(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if got := m.settingsItemCount(); got != 5 {
		t.Fatalf("claude should have 5 settings items, got %d", got)
	}
	m.CycleAITool("next")
	if got := m.settingsItemCount(); got != 4 {
		t.Fatalf("non-claude should have 4 settings items, got %d", got)
	}
}

// renderSettingsForTest is a test-only accessor for the settings box render.
func (m *MainMenuModel) renderSettingsForTest() string { return m.renderSettingsBox() }

// OpenSettings is a test/utility entry point that enters settings mode.
func (m *MainMenuModel) OpenSettings() { m.settingsMode = true; m.settingsSelected = 0 }
