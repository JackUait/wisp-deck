package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
)

func newPanelMenu(t *testing.T) (*MainMenuModel, string, string) {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-configs.list")
	cfgDir := filepath.Join(dir, "claude-configs")
	ptr := filepath.Join(dir, "claude-config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(cfgDir, "personal.json"), []byte("{}"), 0o644)
	os.WriteFile(list, []byte("Work:work.json\nPersonal:personal.json\n"), 0o644)

	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "codex"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigPaths(list, cfgDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("")
	m.EnterSettings()
	m.settingsSelected = 4
	return m, list, ptr
}

func key(t *testing.T, m *MainMenuModel, msg tea.KeyMsg) *MainMenuModel {
	t.Helper()
	upd, _ := m.Update(msg)
	got, ok := upd.(*MainMenuModel)
	if !ok {
		t.Fatalf("Update returned %T", upd)
	}
	return got
}

func TestConfigPanel_EnterOpensPanel(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.ConfigPanelOpen() {
		t.Fatal("Enter on Claude Config row should open the panel")
	}
}

func TestConfigPanel_EscClosesPanel(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ConfigPanelOpen() {
		t.Fatal("Esc should close the panel")
	}
	if !m.InSettingsMode() {
		t.Fatal("Esc from panel should return to settings, not exit settings")
	}
}

func TestConfigPanel_EnterSelectsActive(t *testing.T) {
	m, _, ptr := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open
	m = key(t, m, tea.KeyMsg{Type: tea.KeyDown})  // cursor -> Work
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // select
	if m.ConfigPanelOpen() {
		t.Fatal("selecting should close the panel")
	}
	if m.CurrentClaudeConfigName() != "Work" {
		t.Fatalf("active = %q, want Work", m.CurrentClaudeConfigName())
	}
	data, _ := os.ReadFile(ptr)
	if strings.TrimSpace(string(data)) != "work.json" {
		t.Fatalf("pointer = %q", data)
	}
}

func TestConfigPanel_AddCreatesAndSelects(t *testing.T) {
	m, list, ptr := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})                  // open
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}) // add mode
	m.configPanelInput.SetValue("New One")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // confirm add

	if _, err := os.Stat(filepath.Dir(list) + "/claude-configs/new-one.json"); err != nil {
		t.Fatalf("new config file not created: %v", err)
	}
	listData, _ := os.ReadFile(list)
	if !strings.Contains(string(listData), "New One:new-one.json") {
		t.Fatalf("list = %q", listData)
	}
	if len(m.claudeConfigs) != 3 {
		t.Fatalf("configs len = %d, want 3", len(m.claudeConfigs))
	}
	if m.CurrentClaudeConfigName() != "New One" {
		t.Fatalf("add should select the new config, got %q", m.CurrentClaudeConfigName())
	}
	data, _ := os.ReadFile(ptr)
	if strings.TrimSpace(string(data)) != "new-one.json" {
		t.Fatalf("pointer = %q", data)
	}
	if m.configPanelMode != "" {
		t.Fatalf("panel should return to list mode, got %q", m.configPanelMode)
	}
}

func TestConfigPanel_AddEmptyNameIgnored(t *testing.T) {
	m, list, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.configPanelInput.SetValue("   ")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	listData, _ := os.ReadFile(list)
	if strings.Count(string(listData), "\n") != 2 {
		t.Fatalf("empty add should not append a line: %q", listData)
	}
}

func TestConfigPanel_RenameChangesName(t *testing.T) {
	m, list, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})                     // open
	m = key(t, m, tea.KeyMsg{Type: tea.KeyDown})                     // -> Work
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // rename mode
	m.configPanelInput.SetValue("Day Job")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	listData, _ := os.ReadFile(list)
	if !strings.Contains(string(listData), "Day Job:work.json") {
		t.Fatalf("list = %q", listData)
	}
	if strings.Contains(string(listData), "Work:work.json") {
		t.Fatalf("old name remains: %q", listData)
	}
}

func TestConfigPanel_RenameNotOfferedOnStandard(t *testing.T) {
	m, list, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})                     // open, cursor on Standard (0)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // should be a no-op
	if m.configPanelMode != "" {
		t.Fatalf("rename must not open for Standard, mode = %q", m.configPanelMode)
	}
	listData, _ := os.ReadFile(list)
	if !strings.Contains(string(listData), "Work:work.json") {
		t.Fatalf("list mutated: %q", listData)
	}
}

func TestConfigPanel_DeleteActiveResetsToStandard(t *testing.T) {
	m, _, ptr := newPanelMenu(t)
	m.SetActiveClaudeConfig("personal.json")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})                     // open
	m = key(t, m, tea.KeyMsg{Type: tea.KeyDown})                     // Work
	m = key(t, m, tea.KeyMsg{Type: tea.KeyDown})                     // Personal
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}) // delete confirm
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}) // yes

	if len(m.claudeConfigs) != 1 {
		t.Fatalf("configs len = %d, want 1", len(m.claudeConfigs))
	}
	if m.CurrentClaudeConfigName() != "Standard Claude" {
		t.Fatalf("active after deleting active config = %q", m.CurrentClaudeConfigName())
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatal("pointer should be cleared")
	}
}

func TestConfigPanel_DeleteCancelKeepsConfig(t *testing.T) {
	m, list, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyDown}) // Work
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}) // cancel
	listData, _ := os.ReadFile(list)
	if !strings.Contains(string(listData), "Work:work.json") {
		t.Fatalf("config wrongly deleted: %q", listData)
	}
	if m.configPanelMode != "" {
		t.Fatalf("cancel should return to list, mode = %q", m.configPanelMode)
	}
}

func TestSettings_ConfigRow_HintsManage(t *testing.T) {
	m, _, _ := newPanelMenu(t) // settingsSelected == 4, panel closed
	view := m.View()
	if !strings.Contains(view, "manage") {
		t.Fatalf("settings footer should hint ⏎ manage on the Claude Config row:\n%s", view)
	}
}

func TestConfigPanel_RendersList(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	for _, want := range []string{"Standard Claude", "Work", "Personal", "add", "rename", "delete"} {
		if !strings.Contains(view, want) {
			t.Errorf("panel view missing %q", want)
		}
	}
}
