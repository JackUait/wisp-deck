package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/muesli/termenv"
)

// withTrueColor forces a real color profile for the duration of a test so
// lipgloss keeps the ANSI styling that distinguishes hover/cursor/plain rows
// (it strips color in a non-TTY test process otherwise), restoring it after.
func withTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// --- config menu (top-level "Ghost Tab Configuration") ---

func TestConfigMenu_geometryMatchesRender(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{})
	m.width = 80
	m.cursor = 1
	lines := strings.Split(m.View(), "\n")
	markerRow := -1
	for i, l := range lines {
		if strings.Contains(l, "▸") { // ▸ cursor marker
			markerRow = i
			break
		}
	}
	if markerRow < 0 {
		t.Fatal("could not find cursor marker in rendered config menu")
	}
	if got := m.configMenuItemAt(markerRow); got != 1 {
		t.Errorf("configMenuItemAt(%d) = %d, want 1 (cursor row mismatch)", markerRow, got)
	}
}

func TestConfigMenu_clickSelectsItem(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{})
	m.width = 80
	msg := tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	upd, cmd := m.Update(msg)
	got := upd.(ConfigMenuModel)
	if got.Selected() == nil || got.Selected().Action != "manage-claude-configs" {
		t.Fatalf("click on first item should select it, got %v", got.Selected())
	}
	if cmd == nil {
		t.Error("selecting an item should emit a quit command")
	}
}

func TestConfigMenu_hoverSetsHoverNotCursor(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{})
	m.width = 80
	upd, _ := m.Update(tea.MouseMsg{X: 5, Y: 5, Action: tea.MouseActionMotion})
	got := upd.(ConfigMenuModel)
	if got.hover != 1 {
		t.Errorf("hover set hover index to %d, want 1", got.hover)
	}
	if got.Cursor() != 0 {
		t.Errorf("hover must not move the keyboard cursor; got %d, want 0", got.Cursor())
	}
}

func TestConfigMenu_hoverRendersDistinctly(t *testing.T) {
	withTrueColor(t)
	m := NewConfigMenu(ConfigMenuOptions{})
	m.width = 80
	plain := m.View()
	m.hover = 1 // a non-cursor row (cursor defaults to 0)
	hovered := m.View()
	if plain == hovered {
		t.Error("hovering an item should change the rendered config menu, but output was identical")
	}
}

func TestConfigMenu_hoverIgnoresTrailingPadding(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{})
	m.width = 80
	// Y=2 is the first item's title row; X=5 is on the text.
	upd, _ := m.Update(tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionMotion})
	if got := upd.(ConfigMenuModel); got.hover != 0 {
		t.Fatalf("hovering the item text should set hover 0, got %d", got.hover)
	}
	// Far right of the same row is blank padding inside the box — must not hover.
	upd, _ = m.Update(tea.MouseMsg{X: 48, Y: 2, Action: tea.MouseActionMotion})
	if got := upd.(ConfigMenuModel); got.hover != -1 {
		t.Errorf("hovering trailing padding should leave hover -1, got %d", got.hover)
	}
}

func TestConfigMenu_hoverClearsWhenPointerLeaves(t *testing.T) {
	m := NewConfigMenu(ConfigMenuOptions{})
	m.width = 80
	// Hover an item, then move onto the spacer row between items (no item there).
	upd, _ := m.Update(tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionMotion})
	m = upd.(ConfigMenuModel)
	if m.hover != 0 {
		t.Fatalf("precondition: hover should be 0, got %d", m.hover)
	}
	upd, _ = m.Update(tea.MouseMsg{X: 5, Y: 4, Action: tea.MouseActionMotion}) // spacer row
	got := upd.(ConfigMenuModel)
	if got.hover != -1 {
		t.Errorf("moving the pointer off the rows should clear hover to -1, got %d", got.hover)
	}
}

// --- multi-select AI tools ---

func TestMultiSelect_clickTogglesAndConfirm(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Installed: true}, {Name: "opencode"}}
	m := NewMultiSelect(tools)
	if m.Checked()[1] {
		t.Fatal("precondition: opencode should start unchecked")
	}
	// Click opencode (tool index 1, screen row 3) to toggle it on.
	upd, _ := m.Update(tea.MouseMsg{X: 6, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = upd.(MultiSelectModel)
	if !m.Checked()[1] {
		t.Fatal("clicking the opencode row should check it")
	}
	// Click the Confirm button.
	upd, cmd := m.Update(tea.MouseMsg{X: 3, Y: m.multiSelectConfirmRow(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	got := upd.(MultiSelectModel)
	if got.Result() == nil || !got.Result().Confirmed {
		t.Fatalf("clicking Confirm should produce a confirmed result, got %v", got.Result())
	}
	if cmd == nil {
		t.Error("Confirm should emit a quit command")
	}
}

func TestMultiSelect_hoverSetsHoverNotCursor(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Installed: true}, {Name: "opencode"}}
	m := NewMultiSelect(tools)
	upd, _ := m.Update(tea.MouseMsg{X: 6, Y: 3, Action: tea.MouseActionMotion})
	got := upd.(MultiSelectModel)
	if got.hover != 1 {
		t.Errorf("hover set hover index to %d, want 1", got.hover)
	}
	if got.Cursor() != 0 {
		t.Errorf("hover must not move the keyboard cursor; got %d, want 0", got.Cursor())
	}
}

func TestMultiSelect_hoverRendersDistinctly(t *testing.T) {
	withTrueColor(t)
	tools := []models.AITool{{Name: "claude", Installed: true}, {Name: "opencode"}}
	m := NewMultiSelect(tools)
	plain := m.View()
	m.hover = 1 // a non-cursor row (cursor defaults to 0)
	hovered := m.View()
	if plain == hovered {
		t.Error("hovering a tool should change the rendered multi-select, but output was identical")
	}
}

func TestMultiSelect_hoverClearsWhenPointerLeaves(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Installed: true}, {Name: "opencode"}}
	m := NewMultiSelect(tools)
	upd, _ := m.Update(tea.MouseMsg{X: 6, Y: 3, Action: tea.MouseActionMotion})
	m = upd.(MultiSelectModel)
	if m.hover != 1 {
		t.Fatalf("precondition: hover should be 1, got %d", m.hover)
	}
	// Move onto the title row (Y=0), which has no tool.
	upd, _ = m.Update(tea.MouseMsg{X: 6, Y: 0, Action: tea.MouseActionMotion})
	got := upd.(MultiSelectModel)
	if got.hover != -1 {
		t.Errorf("moving the pointer off the tool rows should clear hover to -1, got %d", got.hover)
	}
}

// --- claude config management menu ---

func TestClaudeConfigMenu_clickAddRowStartsAdd(t *testing.T) {
	configs := []ClaudeConfig{{Name: "Work", File: "work.json"}, {Name: "Personal", File: "personal.json"}}
	m := NewClaudeConfigMenu(configs)
	// Add row is the last list index (= len(configs)), at screen row 2+len.
	addRow := 2 + m.addRowIndex()
	upd, _ := m.Update(tea.MouseMsg{X: 5, Y: addRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	got := upd.(ClaudeConfigMenuModel)
	if got.mode != ccmAddInput {
		t.Errorf("clicking the Add row should start add-input mode, got mode %v", got.mode)
	}
}

func TestClaudeConfigMenu_hoverSetsHoverNotCursor(t *testing.T) {
	configs := []ClaudeConfig{{Name: "Work", File: "work.json"}, {Name: "Personal", File: "personal.json"}}
	m := NewClaudeConfigMenu(configs)
	upd, _ := m.Update(tea.MouseMsg{X: 5, Y: 3, Action: tea.MouseActionMotion}) // second config (row 3)
	got := upd.(ClaudeConfigMenuModel)
	if got.hover != 1 {
		t.Errorf("hover set hover index to %d, want 1", got.hover)
	}
	if got.cursor != 0 {
		t.Errorf("hover must not move the keyboard cursor; got %d, want 0", got.cursor)
	}
}

func TestClaudeConfigMenu_hoverRendersDistinctly(t *testing.T) {
	withTrueColor(t)
	configs := []ClaudeConfig{{Name: "Work", File: "work.json"}, {Name: "Personal", File: "personal.json"}}
	m := NewClaudeConfigMenu(configs)
	plain := m.View()
	m.hover = 1 // a non-cursor row (cursor defaults to 0)
	hovered := m.View()
	if plain == hovered {
		t.Error("hovering a config should change the rendered menu, but output was identical")
	}
}

func TestClaudeConfigMenu_hoverClearsWhenPointerLeaves(t *testing.T) {
	configs := []ClaudeConfig{{Name: "Work", File: "work.json"}, {Name: "Personal", File: "personal.json"}}
	m := NewClaudeConfigMenu(configs)
	upd, _ := m.Update(tea.MouseMsg{X: 5, Y: 3, Action: tea.MouseActionMotion})
	m = upd.(ClaudeConfigMenuModel)
	if m.hover != 1 {
		t.Fatalf("precondition: hover should be 1, got %d", m.hover)
	}
	// Move onto the top border row (Y=0), off every list row.
	upd, _ = m.Update(tea.MouseMsg{X: 5, Y: 0, Action: tea.MouseActionMotion})
	got := upd.(ClaudeConfigMenuModel)
	if got.hover != -1 {
		t.Errorf("moving the pointer off the rows should clear hover to -1, got %d", got.hover)
	}
}

// --- confirm dialog ---

func TestConfirmDialog_clickYesAndNo(t *testing.T) {
	row := NewConfirmDialog("Delete this?").confirmButtonRow()

	yes := NewConfirmDialog("Delete this?")
	upd, cmd := yes.Update(tea.MouseMsg{X: 2, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	got := upd.(ConfirmDialogModel)
	if !got.Confirmed {
		t.Error("clicking Yes should set Confirmed = true")
	}
	if cmd == nil {
		t.Error("clicking Yes should emit a quit command")
	}

	no := NewConfirmDialog("Delete this?")
	// No button starts after "[ Yes ]" + the gap.
	noX := len(confirmYesLabel) + len(confirmGap) + 1
	upd2, cmd2 := no.Update(tea.MouseMsg{X: noX, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	got2 := upd2.(ConfirmDialogModel)
	if got2.Confirmed {
		t.Error("clicking No should leave Confirmed = false")
	}
	if cmd2 == nil {
		t.Error("clicking No should still emit a quit command")
	}
}

func TestConfirmDialog_hoverHighlightsButton(t *testing.T) {
	m := NewConfirmDialog("Delete this?")
	upd, _ := m.Update(tea.MouseMsg{X: 2, Y: m.confirmButtonRow(), Action: tea.MouseActionMotion})
	got := upd.(ConfirmDialogModel)
	if got.btnHover != 1 {
		t.Errorf("hovering Yes should set btnHover = 1, got %d", got.btnHover)
	}
}
