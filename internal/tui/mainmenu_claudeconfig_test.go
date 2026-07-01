package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

func newClaudeMenu(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "opencode"}, "claude", "none")
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

// Subscriptions are shared across every agent (the active plan drives Claude's
// --settings file AND OpenCode's default model), so the control is shown for all
// agents, not just Claude.
func TestClaudeConfig_visibility_all_agents(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if !m.ClaudeConfigVisible() {
		t.Fatal("should be visible for claude")
	}
	m.CycleAITool("next") // -> opencode
	if !m.ClaudeConfigVisible() {
		t.Fatal("should stay visible for non-claude agents (subscriptions are shared)")
	}
}

func TestSettings_shows_config_row_for_claude(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.OpenSettings()
	view := m.renderSettingsForTest()
	if !strings.Contains(view, "Subscription") {
		t.Fatalf("settings should show Plan row:\n%s", view)
	}
	if !strings.Contains(view, "Standard Claude") {
		t.Fatalf("should show current config name")
	}
}

func TestSettings_shows_config_row_for_non_claude(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.CycleAITool("next") // opencode
	m.OpenSettings()
	view := m.renderSettingsForTest()
	if !strings.Contains(view, "Subscription") {
		t.Fatalf("Plan row must be shown for non-claude agents (subscriptions are shared):\n%s", view)
	}
}

func TestSettings_nav_count_includes_config_for_all_agents(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if got := m.settingsItemCount(); got != 9 {
		t.Fatalf("claude should have 9 settings items (incl. Plan + Login), got %d", got)
	}
	m.CycleAITool("next")
	if got := m.settingsItemCount(); got != 9 {
		t.Fatalf("non-claude should also have 9 settings items (shared Plan + Login), got %d", got)
	}
}

// renderSettingsForTest is a test-only accessor for the settings box render.
func (m *MainMenuModel) renderSettingsForTest() string { return m.renderSettingsBox() }

// OpenSettings is a test/utility entry point that enters settings mode.
func (m *MainMenuModel) OpenSettings() { m.activeTab = TabSettings; m.settingsSelected = 0 }
