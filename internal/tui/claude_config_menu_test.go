package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func testConfigs() []ClaudeConfig {
	return []ClaudeConfig{
		{Name: "Work", File: "work.json"},
		{Name: "Personal", File: "personal.json"},
	}
}

func pressKey(m ClaudeConfigMenuModel, key string) ClaudeConfigMenuModel {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return updated.(ClaudeConfigMenuModel)
}

func pressSpecial(m ClaudeConfigMenuModel, keyType tea.KeyType) (ClaudeConfigMenuModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: keyType})
	return updated.(ClaudeConfigMenuModel), cmd
}

func TestClaudeConfigMenu_QKey_EmitsQuit(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m2 := pressKey(m, "q")
	if m2.Result() == nil {
		t.Fatal("expected result, got nil")
	}
	if m2.Result().Action != "quit" {
		t.Errorf("expected action=quit, got %q", m2.Result().Action)
	}
}

func TestClaudeConfigMenu_Escape_EmitsQuit(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m2, cmd := pressSpecial(m, tea.KeyEscape)
	if m2.Result() == nil {
		t.Fatal("expected result, got nil")
	}
	if m2.Result().Action != "quit" {
		t.Errorf("expected action=quit, got %q", m2.Result().Action)
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestClaudeConfigMenu_CtrlC_EmitsQuit(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m2, _ := pressSpecial(m, tea.KeyCtrlC)
	if m2.Result() == nil || m2.Result().Action != "quit" {
		t.Errorf("ctrl+c should emit quit, got %v", m2.Result())
	}
}

func TestClaudeConfigMenu_Navigation_DownUp(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	if m.cursor != 0 {
		t.Fatalf("initial cursor should be 0, got %d", m.cursor)
	}
	m = pressKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("after j cursor should be 1, got %d", m.cursor)
	}
	m = pressKey(m, "k")
	if m.cursor != 0 {
		t.Errorf("after k cursor should be 0, got %d", m.cursor)
	}
}

func TestClaudeConfigMenu_Navigation_BoundedByAddRow(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	// addRowIndex = 2 (len of 2-item configs)
	// Press down 3 times — should stop at 2 (add row), not go to 3
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, "j") // should stay at 2
	if m.cursor != 2 {
		t.Errorf("cursor should clamp at addRowIndex=2, got %d", m.cursor)
	}
}

func TestClaudeConfigMenu_AKey_EntersAddMode(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m = pressKey(m, "a")
	if m.mode != ccmAddInput {
		t.Errorf("expected ccmAddInput mode, got %d", m.mode)
	}
}

func TestClaudeConfigMenu_AddFlow_EmitsAdd(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m = pressKey(m, "a")
	// Type "new-cfg" character by character via key messages
	for _, ch := range "new-cfg" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated.(ClaudeConfigMenuModel)
	}
	// Press enter
	m2, cmd := pressSpecial(m, tea.KeyEnter)
	if m2.Result() == nil {
		t.Fatal("expected result after enter in add mode")
	}
	if m2.Result().Action != "add" {
		t.Errorf("expected action=add, got %q", m2.Result().Action)
	}
	if m2.Result().Name != "new-cfg" {
		t.Errorf("expected name=new-cfg, got %q", m2.Result().Name)
	}
	if m2.Result().File != "" {
		t.Errorf("expected no file for add, got %q", m2.Result().File)
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestClaudeConfigMenu_AddFlow_EscCancels(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m = pressKey(m, "a")
	m2, _ := pressSpecial(m, tea.KeyEscape)
	if m2.mode != ccmList {
		t.Errorf("esc in add mode should return to ccmList, got %d", m2.mode)
	}
	if m2.Result() != nil {
		t.Error("esc in add mode should not set result")
	}
}

func TestClaudeConfigMenu_AddFlow_EmptyNameNoQuit(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m = pressKey(m, "a")
	// Enter with empty input should not quit
	m2, cmd := pressSpecial(m, tea.KeyEnter)
	if m2.Result() != nil {
		t.Error("empty name should not produce result")
	}
	if cmd != nil {
		t.Error("empty name enter should not return quit cmd")
	}
}

func TestClaudeConfigMenu_RenameFlow_EmitsRename(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	// cursor at 0 = "Work" / work.json
	m = pressKey(m, "r")
	if m.mode != ccmRenameInput {
		t.Fatalf("expected ccmRenameInput mode, got %d", m.mode)
	}
	// Clear existing text by pressing enter with pre-populated value would rename
	// Instead override: SetValue clears, then type new name
	m.input.SetValue("WorkRenamed")
	m2, cmd := pressSpecial(m, tea.KeyEnter)
	if m2.Result() == nil {
		t.Fatal("expected result after rename enter")
	}
	if m2.Result().Action != "rename" {
		t.Errorf("expected action=rename, got %q", m2.Result().Action)
	}
	if m2.Result().File != "work.json" {
		t.Errorf("expected file=work.json, got %q", m2.Result().File)
	}
	if m2.Result().Name != "WorkRenamed" {
		t.Errorf("expected name=WorkRenamed, got %q", m2.Result().Name)
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestClaudeConfigMenu_RenameOnAddRow_NoOp(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	// Move to add row (index 2)
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	if m.cursor != m.addRowIndex() {
		t.Fatalf("expected cursor at add row, got %d", m.cursor)
	}
	m2 := pressKey(m, "r")
	if m2.mode != ccmList {
		t.Errorf("r on add row should stay in list mode, got %d", m2.mode)
	}
}

func TestClaudeConfigMenu_DeleteFlow_YConfirmsDelete(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	// cursor 0 = "Work" / work.json
	m = pressKey(m, "d")
	if m.mode != ccmDeleteConfirm {
		t.Fatalf("expected ccmDeleteConfirm mode, got %d", m.mode)
	}
	m2 := pressKey(m, "y")
	if m2.Result() == nil {
		t.Fatal("expected result after y confirm")
	}
	if m2.Result().Action != "delete" {
		t.Errorf("expected action=delete, got %q", m2.Result().Action)
	}
	if m2.Result().File != "work.json" {
		t.Errorf("expected file=work.json, got %q", m2.Result().File)
	}
}

func TestClaudeConfigMenu_DeleteFlow_NCancels(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	m = pressKey(m, "d")
	m2 := pressKey(m, "n")
	if m2.mode != ccmList {
		t.Errorf("n in delete confirm should return to list, got %d", m2.mode)
	}
	if m2.Result() != nil {
		t.Error("n should not produce result")
	}
}

func TestClaudeConfigMenu_DeleteOnAddRow_NoOp(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	// move to add row
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m2 := pressKey(m, "d")
	if m2.mode != ccmList {
		t.Errorf("d on add row should not enter delete confirm, got %d", m2.mode)
	}
}

func TestClaudeConfigMenu_EnterOnAddRow_EntersAddMode(t *testing.T) {
	m := NewClaudeConfigMenu(testConfigs())
	// move to add row
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m2, _ := pressSpecial(m, tea.KeyEnter)
	if m2.mode != ccmAddInput {
		t.Errorf("enter on add row should enter ccmAddInput, got %d", m2.mode)
	}
}

func TestClaudeConfigMenu_EmptyConfigs_AddRowIsIndex0(t *testing.T) {
	m := NewClaudeConfigMenu([]ClaudeConfig{})
	if m.addRowIndex() != 0 {
		t.Errorf("add row index with empty configs should be 0, got %d", m.addRowIndex())
	}
}

// TestClaudeConfigMenu_BoxTopBorderWidth asserts the first line of the rendered
// view has the correct visible width (boxWidth) even when the title contains
// ANSI escape sequences (styled foreground + bold).
// Forces TrueColor so lipgloss emits ANSI sequences — reproduces the bug where
// len(titleRunes) over-skips border runes and corrupts the top border width.
func TestClaudeConfigMenu_BoxTopBorderWidth(t *testing.T) {
	// Force TrueColor profile so titleRendered contains ANSI escapes.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := NewClaudeConfigMenu(testConfigs())
	// Force a window size so boxWidth stays at 56 (default).
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(ClaudeConfigMenuModel)

	view := m.View()
	if view == "" {
		t.Fatal("View() returned empty string")
	}

	lines := strings.Split(view, "\n")
	topLine := lines[0]
	got := lipgloss.Width(topLine)

	// boxWidth is 56; lipgloss border adds 2 cols (left + right), so the
	// rendered outer width is boxWidth + 2 = 58.
	const wantWidth = 58
	if got != wantWidth {
		t.Errorf("top border visible width = %d, want %d (ANSI escape leak in renderBox?)", got, wantWidth)
	}
}
