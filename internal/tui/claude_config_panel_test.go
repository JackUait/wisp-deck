package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/claudeconfig"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/muesli/termenv"
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

	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "opencode"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigPaths(list, cfgDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("")
	m.EnterSettings()
	m.settingsSelected = 5
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

func TestModelMapTarget_mapsCoordinates(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	saveStart, _, cancelStart, _ := modelMapButtonRanges()
	cases := []struct {
		boxX, panelY int
		wantKind     int
		wantIndex    int
	}{
		{5, 4, mmModel, 0}, // first slot
		{5, 7, mmModel, 3}, // last slot
		{5, 8, mmNone, 0},  // blank
		{5, 9, mmKey, 0},   // API key row
		{saveStart, 10, mmSave, 0},
		{cancelStart, 10, mmCancel, 0},
	}
	for _, c := range cases {
		k, i := m.modelMapTarget(c.boxX, c.panelY)
		if k != c.wantKind || (c.wantKind == mmModel && i != c.wantIndex) {
			t.Errorf("modelMapTarget(%d,%d) = (%d,%d), want (%d,%d)", c.boxX, c.panelY, k, i, c.wantKind, c.wantIndex)
		}
	}
}

func TestUpdate_clickModelSlot_cyclesModel(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	m.width, m.height = 100, 60
	_ = m.View()
	if m.modelMap[0] != -1 {
		t.Fatalf("precondition modelMap[0] = %d, want -1", m.modelMap[0])
	}
	msg := tea.MouseMsg{X: m.menuOriginX + 5, Y: m.modalOriginY + 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	upd, _ := m.Update(msg)
	got := upd.(*MainMenuModel)
	if got.modelMap[0] != 0 {
		t.Errorf("after clicking slot 0, modelMap[0] = %d, want 0", got.modelMap[0])
	}
}

func TestUpdate_clickSaveButton_persistsAndCloses(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	m.modelMap[0] = 0
	m.modelMap[1] = 1
	m.width, m.height = 100, 60
	_ = m.View()
	saveStart, _, _, _ := modelMapButtonRanges()
	msg := tea.MouseMsg{X: m.menuOriginX + saveStart, Y: m.modalOriginY + 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	upd, _ := m.Update(msg)
	got := upd.(*MainMenuModel)
	if got.modelMapOpen {
		t.Error("clicking Save should close the model map panel")
	}
	mappings := claudeconfig.ReadModelMappings(got.claudeConfigsDir, "work.json", got.modelMapModels)
	if mappings[0] != 0 || mappings[1] != 1 {
		t.Errorf("Save did not persist mappings: got %v, want [0]=0 [1]=1", mappings)
	}
}

func TestUpdate_clickCancelButton_closesWithoutSaving(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	m.modelMap[0] = 0
	m.width, m.height = 100, 60
	_ = m.View()
	_, _, cancelStart, _ := modelMapButtonRanges()
	msg := tea.MouseMsg{X: m.menuOriginX + cancelStart, Y: m.modalOriginY + 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	upd, _ := m.Update(msg)
	got := upd.(*MainMenuModel)
	if got.modelMapOpen {
		t.Error("clicking Cancel should close the model map panel")
	}
	mappings := claudeconfig.ReadModelMappings(got.claudeConfigsDir, "work.json", got.modelMapModels)
	if mappings[0] == 0 {
		t.Error("Cancel should not have persisted the in-progress mapping")
	}
}

func TestUpdate_clickPlanSettingsRow_opensModelMap(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next") // select Work (a custom config)
	m.width, m.height = 100, 60
	_ = m.View()
	// Plan is settings item 5.
	row := m.firstSettingsItemRow() + 5
	msg := tea.MouseMsg{X: m.menuOriginX + 5, Y: m.menuOriginY + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	upd, _ := m.Update(msg)
	got := upd.(*MainMenuModel)
	if !got.modelMapOpen {
		t.Error("clicking the Plan settings row (custom config) should open the model map")
	}
}

func TestUpdate_hoverModelSlot_setsHoverNotCursor(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	m.modelMapCursor = 0
	m.width, m.height = 100, 60
	_ = m.View()
	msg := tea.MouseMsg{X: m.menuOriginX + 5, Y: m.modalOriginY + 6, Action: tea.MouseActionMotion}
	upd, _ := m.Update(msg)
	got := upd.(*MainMenuModel)
	if got.modelMapSlotHover != 2 {
		t.Errorf("hover set modelMapSlotHover to %d, want 2", got.modelMapSlotHover)
	}
	if got.modelMapCursor != 0 {
		t.Errorf("hover must not move modelMapCursor; got %d, want 0", got.modelMapCursor)
	}
	if !got.modelMapOpen {
		t.Error("hover must not close the model map")
	}
}

func TestUpdate_hoverModelSlot_clearsWhenPointerLeaves(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	m.width, m.height = 100, 60
	_ = m.View()
	upd, _ := m.Update(tea.MouseMsg{X: m.menuOriginX + 5, Y: m.modalOriginY + 6, Action: tea.MouseActionMotion})
	mm := upd.(*MainMenuModel)
	if mm.modelMapSlotHover != 2 {
		t.Fatalf("precondition: modelMapSlotHover should be 2, got %d", mm.modelMapSlotHover)
	}
	// Panel row 8 is the blank row between the slots and the API-key row.
	upd, _ = mm.Update(tea.MouseMsg{X: mm.menuOriginX + 5, Y: mm.modalOriginY + 8, Action: tea.MouseActionMotion})
	got := upd.(*MainMenuModel)
	if got.modelMapSlotHover != -1 {
		t.Errorf("moving off the slots should clear modelMapSlotHover to -1, got %d", got.modelMapSlotHover)
	}
}

func TestModelMap_hoverSlotRendersDistinctly(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m, _, _ := newPanelMenu(t)
	m.CycleClaudeConfig("next")
	m.openModelMap()
	m.modelMapCursor = 0
	m.width, m.height = 100, 60
	plain := m.View()
	m.modelMapSlotHover = 2 // a non-cursor slot
	hovered := m.View()
	if plain == hovered {
		t.Error("hovering a model slot should change the rendered panel, but output was identical")
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

	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "opencode"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigPaths(list, cfgDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("work.json")
	m.EnterSettings()
	m.settingsSelected = 5

	view := m.View()
	if !strings.Contains(view, "1 mapped") {
		t.Fatalf("config row should show '1 mapped' indicator:\n%s", view)
	}
}

func TestModelMap_ShowsUnmappedIndicator(t *testing.T) {
	m, _, _ := newPanelMenu(t)
	m.SetActiveClaudeConfig("work.json")
	m.settingsSelected = 5
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

	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "opencode"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigPaths(list, cfgDir)
	m.SetClaudeConfigs(LoadClaudeConfigsList(list))
	m.SetActiveClaudeConfig("work.json")
	m.EnterSettings()
	m.settingsSelected = 5
	m = key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	if !strings.Contains(view, "API Key") {
		t.Fatalf("panel should show API Key row:\n%s", view)
	}
	if !strings.Contains(view, "••••••••") {
		t.Fatalf("panel should show masked key:\n%s", view)
	}
}
