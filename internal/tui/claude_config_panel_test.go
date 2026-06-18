package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/claudeconfig"
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


func TestModelMap_EnterOnNonStandardOpensPanel(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	if m.CurrentClaudeConfigName() != "Work" {
		t.Fatalf("expected Work, got %q", m.CurrentClaudeConfigName())
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.APIKeyInputOpen() {
		t.Fatal("Enter on non-Standard config should open model mapping panel")
	}
}

func TestModelMap_EnterOnStandardDoesNothing(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.APIKeyInputOpen() {
		t.Fatal("Enter on Standard should NOT open model mapping panel")
	}
	if !m.InSettingsMode() {
		t.Fatal("Enter on Standard should stay in settings")
	}
}

func TestModelMap_EscCancels(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.APIKeyInputOpen() {
		t.Fatal("expected model mapping panel open")
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.APIKeyInputOpen() {
		t.Fatal("Esc should close model mapping panel")
	}
	if !m.InSettingsMode() {
		t.Fatal("should still be in settings mode")
	}
}

func TestModelMap_NavigateSlots(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.modelMapCursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.modelMapCursor)
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modelMapCursor != 1 {
		t.Fatalf("cursor should be 1 after Down, got %d", m.modelMapCursor)
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.modelMapCursor != 0 {
		t.Fatalf("cursor should be 0 after Up, got %d", m.modelMapCursor)
	}
}

func TestModelMap_CycleModels(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.modelMap[0] != -1 {
		t.Fatalf("initial model should be -1 (unmapped), got %d", m.modelMap[0])
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.modelMap[0] != 0 {
		t.Fatalf("model should be 0 after Right from unmapped, got %d", m.modelMap[0])
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.modelMap[0] != 1 {
		t.Fatalf("model should be 1 after Right, got %d", m.modelMap[0])
	}
	m = key(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.modelMap[0] != 0 {
		t.Fatalf("model should be 0 after Left, got %d", m.modelMap[0])
	}
}

func TestModelMap_EnterSavesMappings(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next") // Work
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Set opus to glm-5.2 (index 0), sonnet to glm-5 (index 1)
	m.modelMap[0] = 0
	m.modelMap[1] = 1
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.APIKeyInputOpen() {
		t.Fatal("Enter should save and close model mapping panel")
	}
	mappings := claudeconfig.ReadModelMappings(m.claudeConfigsDir, "work.json", m.modelMapModels)
	if mappings[0] != 0 {
		t.Fatalf("opus mapping = %d, want 0", mappings[0])
	}
	if mappings[1] != 1 {
		t.Fatalf("sonnet mapping = %d, want 1", mappings[1])
	}
}

func TestModelMap_ShowsMappedIndicator(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte(`{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.2"}}`), 0o644)
	os.WriteFile(filepath.Join(cfgDir, "personal.json"), []byte("{}"), 0o644)
	list := filepath.Join(dir, "claude-configs.list")
	os.WriteFile(list, []byte("Work:work.json\nPersonal:personal.json\n"), 0o644)
	ptr := filepath.Join(dir, "claude-config")

	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "codex"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigPaths(list, cfgDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("work.json")
	m.EnterSettings()
	m.settingsSelected = 4

	view := m.View()
	if !strings.Contains(view, "1 mapped") {
		t.Fatalf("config row should show '1 mapped' indicator:\n%s", view)
	}
}

func TestModelMap_ShowsUnmappedIndicator(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.SetActiveClaudeConfig("work.json")
	m.settingsSelected = 4
	view := m.View()
	if !strings.Contains(view, "unmapped") {
		t.Fatalf("config row should show 'unmapped' indicator:\n%s", view)
	}
}

func TestModelMap_WrapAround(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	n := len(m.modelMapModels)
	// Cycle: -1 (none) → 0 → 1 → ... → n-1 → -1
	// From unmapped (-1): Right -> 0
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.modelMap[0] != 0 {
		t.Fatalf("right from none: expected 0, got %d", m.modelMap[0])
	}
	// From n-1: Right -> -1 (none)
	m.modelMap[0] = n - 1
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.modelMap[0] != -1 {
		t.Fatalf("right from last: expected -1, got %d", m.modelMap[0])
	}
	// From -1 (none): Left -> n-1
	m = key(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.modelMap[0] != n-1 {
		t.Fatalf("left from none: expected %d, got %d", n-1, m.modelMap[0])
	}
	// From 0: Left -> -1 (none)
	m.modelMap[0] = 0
	m = key(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.modelMap[0] != -1 {
		t.Fatalf("left from 0: expected -1, got %d", m.modelMap[0])
	}
}

func TestModelMap_APIKeyInput_EKeyOpens(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open model map
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if !m.modelMapKeyMode {
		t.Fatal("'e' should open API key input")
	}
}

func TestModelMap_APIKeyInput_EscCancels(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.modelMapKeyMode {
		t.Fatal("Esc should close API key input")
	}
	if !m.modelMapOpen {
		t.Fatal("should still be in model map panel")
	}
}

func TestModelMap_APIKeyInput_EnterSaves(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next") // Work
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m.modelMapKeyInput.SetValue("sk-test-key")
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.modelMapKeyMode {
		t.Fatal("Enter should save and close key input")
	}
	saved := claudeconfig.ReadAPIKey(m.claudeConfigsDir, "work.json")
	if saved != "sk-test-key" {
		t.Fatalf("API key = %q, want sk-test-key", saved)
	}
}

func TestModelMap_APIKeyInput_ShowsInPanel(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-abc"}}`), 0o644)
	os.WriteFile(filepath.Join(cfgDir, "personal.json"), []byte("{}"), 0o644)
	list := filepath.Join(dir, "claude-configs.list")
	os.WriteFile(list, []byte("Work:work.json\nPersonal:personal.json\n"), 0o644)
	ptr := filepath.Join(dir, "claude-config")

	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "codex"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigPaths(list, cfgDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("work.json")
	m.EnterSettings()
	m.settingsSelected = 4
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	if !strings.Contains(view, "API Key") {
		t.Fatalf("panel should show API Key row:\n%s", view)
	}
	if !strings.Contains(view, "••••••••") {
		t.Fatalf("panel should show masked key:\n%s", view)
	}
}
