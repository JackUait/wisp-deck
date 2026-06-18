package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/jackuait/ghost-tab/internal/tui"
)

func testTools() []models.AITool {
	return []models.AITool{
		{Name: "claude", Command: "claude", Installed: true},
		{Name: "codex", Command: "codex", Installed: true},
		{Name: "opencode", Command: "opencode", Installed: false},
	}
}

func TestNewMultiSelect_InitialState(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	result := model.Result()
	if result != nil {
		t.Error("Expected result to be nil before any confirmation")
	}
}

func TestNewMultiSelect_ClaudeAlwaysPreChecked(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: false},
		{Name: "codex", Command: "codex", Installed: true},
	}
	model := tui.NewMultiSelect(tools)

	// Claude should be pre-checked even if not installed
	checked := model.Checked()
	if !checked[0] {
		t.Error("Expected claude to be pre-checked regardless of install status")
	}
}

func TestNewMultiSelect_InstalledToolsPreChecked(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	checked := model.Checked()

	// claude (installed) -> checked
	if !checked[0] {
		t.Error("Expected claude (installed) to be pre-checked")
	}
	// codex (installed) -> checked
	if !checked[1] {
		t.Error("Expected codex (installed) to be pre-checked")
	}
	// opencode (not installed) -> not checked
	if checked[2] {
		t.Error("Expected opencode (not installed) to not be pre-checked")
	}
}

func TestMultiSelect_CursorStartsAtZero(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	if model.Cursor() != 0 {
		t.Errorf("Expected cursor at 0, got %d", model.Cursor())
	}
}

func TestMultiSelect_NavigateDown(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Press down
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	m := updated.(tui.MultiSelectModel)

	if m.Cursor() != 1 {
		t.Errorf("Expected cursor at 1 after down, got %d", m.Cursor())
	}
}

func TestMultiSelect_NavigateUp(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Move down first, then up
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	m := updated.(tui.MultiSelectModel)

	if m.Cursor() != 1 {
		t.Errorf("Expected cursor at 1, got %d", m.Cursor())
	}
}

func TestMultiSelect_NavigateWithJK(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Press j (down)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m := updated.(tui.MultiSelectModel)
	if m.Cursor() != 1 {
		t.Errorf("Expected cursor at 1 after j, got %d", m.Cursor())
	}

	// Press k (up)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(tui.MultiSelectModel)
	if m.Cursor() != 0 {
		t.Errorf("Expected cursor at 0 after k, got %d", m.Cursor())
	}
}

func TestMultiSelect_CursorWrapsAtBottom(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Navigate past the end
	var updated tea.Model = model
	for i := 0; i < 3; i++ {
		updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m := updated.(tui.MultiSelectModel)

	if m.Cursor() != 0 {
		t.Errorf("Expected cursor to wrap to 0, got %d", m.Cursor())
	}
}

func TestMultiSelect_CursorWrapsAtTop(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Navigate up from position 0
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	m := updated.(tui.MultiSelectModel)

	if m.Cursor() != 2 {
		t.Errorf("Expected cursor to wrap to 2, got %d", m.Cursor())
	}
}

func TestMultiSelect_SpaceTogglesCheckbox(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// claude starts checked; toggle it off
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m := updated.(tui.MultiSelectModel)

	if m.Checked()[0] {
		t.Error("Expected claude to be unchecked after space toggle")
	}

	// Toggle it back on
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(tui.MultiSelectModel)

	if !m.Checked()[0] {
		t.Error("Expected claude to be checked after second space toggle")
	}
}

func TestMultiSelect_ToggleOnDifferentItem(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Navigate to opencode (index 2, not installed, unchecked)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	// Toggle opencode on
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m := updated.(tui.MultiSelectModel)

	if !m.Checked()[2] {
		t.Error("Expected opencode to be checked after toggle")
	}
}

func TestMultiSelect_EnterConfirmsWithSelection(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// claude and codex are pre-checked; press Enter
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(tui.MultiSelectModel)

	// Should have quit command
	if cmd == nil {
		t.Error("Expected a quit command on enter with selections")
	}

	result := m.Result()
	if result == nil {
		t.Fatal("Expected non-nil result after enter")
	}
	if !result.Confirmed {
		t.Error("Expected confirmed to be true")
	}
	if len(result.Tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(result.Tools))
	}
	if result.Tools[0] != "claude" {
		t.Errorf("Expected first tool 'claude', got %q", result.Tools[0])
	}
	if result.Tools[1] != "codex" {
		t.Errorf("Expected second tool 'codex', got %q", result.Tools[1])
	}
}

func TestMultiSelect_EnterWithNoSelectionShowsError(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: false},
	}
	model := tui.NewMultiSelect(tools)

	// claude is still pre-checked; uncheck it
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	// Try to confirm with no selections
	updated, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(tui.MultiSelectModel)

	// Should NOT quit — should show error
	if cmd != nil {
		t.Error("Expected no quit command when no tools selected")
	}

	result := m.Result()
	if result != nil {
		t.Error("Expected nil result when no tools selected")
	}

	// Check that error message is set
	if m.ErrorMsg() == "" {
		t.Error("Expected error message when trying to confirm with no selections")
	}
}

func TestMultiSelect_CtrlCCancels(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m := updated.(tui.MultiSelectModel)

	if cmd == nil {
		t.Error("Expected quit command on ctrl+c")
	}

	result := m.Result()
	if result == nil {
		t.Fatal("Expected non-nil result after cancel")
	}
	if result.Confirmed {
		t.Error("Expected confirmed to be false on cancel")
	}
}

func TestMultiSelect_EscCancels(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m := updated.(tui.MultiSelectModel)

	if cmd == nil {
		t.Error("Expected quit command on esc")
	}

	result := m.Result()
	if result == nil {
		t.Fatal("Expected non-nil result after esc cancel")
	}
	if result.Confirmed {
		t.Error("Expected confirmed to be false on esc")
	}
}

func TestMultiSelect_ViewContainsToolNames(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	view := model.View()

	// Should contain all tool display names
	expectedNames := []string{"Claude Code", "Codex CLI", "OpenCode"}
	for _, name := range expectedNames {
		if !containsString(view, name) {
			t.Errorf("Expected view to contain %q", name)
		}
	}
}

func TestMultiSelect_ViewContainsInstalledTag(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	view := model.View()

	if !containsString(view, "(installed)") {
		t.Error("Expected view to contain '(installed)' for installed tools")
	}
}

func TestMultiSelect_ViewShowsCheckboxState(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	view := model.View()

	// Should have [x] for checked items
	if !containsString(view, "[x]") {
		t.Error("Expected view to contain '[x]' for checked items")
	}
	// Should have [ ] for unchecked items
	if !containsString(view, "[ ]") {
		t.Error("Expected view to contain '[ ]' for unchecked items")
	}
}

func TestMultiSelect_ViewShowsCursor(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	view := model.View()

	// Should show cursor indicator on current item
	if !containsString(view, "❯") {
		t.Error("Expected view to contain cursor '❯'")
	}
}

func TestMultiSelect_ViewShowsHints(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	view := model.View()

	if !containsString(view, "navigate") {
		t.Error("Expected view to contain navigation hint")
	}
	if !containsString(view, "toggle") {
		t.Error("Expected view to contain toggle hint")
	}
	if !containsString(view, "confirm") {
		t.Error("Expected view to contain confirm hint")
	}
}

func TestMultiSelect_ViewHiddenWhenQuitting(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Cancel to enter quitting state
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m := updated.(tui.MultiSelectModel)

	view := m.View()
	if view != "" {
		t.Errorf("Expected empty view when quitting, got %q", view)
	}
}

func TestMultiSelect_ResultToolsInSelectionOrder(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	// Pre-checked: claude (0), codex (1)
	// Uncheck codex, check opencode
	// Navigate to codex (index 1) and uncheck
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	// Navigate to opencode (index 2) and check
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	// Confirm
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(tui.MultiSelectModel)

	result := m.Result()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	// Should be in list order: claude, opencode
	expected := []string{"claude", "opencode"}
	if len(result.Tools) != len(expected) {
		t.Fatalf("Expected %d tools, got %d", len(expected), len(result.Tools))
	}
	for i, tool := range expected {
		if result.Tools[i] != tool {
			t.Errorf("Expected tool[%d]=%q, got %q", i, tool, result.Tools[i])
		}
	}
}

func TestMultiSelect_ErrorClearsOnNextAction(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: false},
	}
	model := tui.NewMultiSelect(tools)

	// Uncheck claude
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	// Try to confirm (should fail with error)
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(tui.MultiSelectModel)
	if m.ErrorMsg() == "" {
		t.Fatal("Expected error message")
	}

	// Any next key should clear the error
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(tui.MultiSelectModel)
	if m.ErrorMsg() != "" {
		t.Error("Expected error to be cleared after next action")
	}
}

func TestMultiSelect_InitReturnsNil(t *testing.T) {
	tools := testTools()
	model := tui.NewMultiSelect(tools)

	cmd := model.Init()
	if cmd != nil {
		t.Error("Expected Init() to return nil")
	}
}

// containsString checks if s contains substr (helper for test readability).
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
