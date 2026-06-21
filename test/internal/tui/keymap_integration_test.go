package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/jackuait/ghost-tab/internal/tui"
)

// runeKey creates a tea.KeyMsg for a single rune (simulates a keypress).
func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// TestNonEnglish_MainMenu_RussianNavigation verifies j/k navigation works
// with Russian keyboard layout active.
func TestNonEnglish_MainMenu_RussianNavigation(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")

	if m.SelectedItem() != 0 {
		t.Fatal("expected initial selection at 0")
	}

	// Russian 'о' is on the physical 'j' key (move down)
	newModel, _ := m.Update(runeKey('о'))
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 1 {
		t.Errorf("Russian 'о' (j key) should move down: expected 1, got %d", mm.SelectedItem())
	}

	// Russian 'л' is on the physical 'k' key (move up)
	newModel, _ = mm.Update(runeKey('л'))
	mm = newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 0 {
		t.Errorf("Russian 'л' (k key) should move up: expected 0, got %d", mm.SelectedItem())
	}
}

// TestNonEnglish_MainMenu_RussianActions verifies action shortcuts work
// with Russian keyboard layout active.
func TestNonEnglish_MainMenu_RussianActions(t *testing.T) {
	t.Run("settings via Russian ы (s key)", func(t *testing.T) {
		m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
		newModel, _ := m.Update(runeKey('ы'))
		mm := newModel.(*tui.MainMenuModel)
		if !mm.InSettingsMode() {
			t.Error("Russian 'ы' (s key) should enter settings mode")
		}
	})

	t.Run("add-project via Russian ф (a key)", func(t *testing.T) {
		m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
		newModel, _ := m.Update(runeKey('ф'))
		mm := newModel.(*tui.MainMenuModel)
		if !mm.InInputMode() {
			t.Error("Russian 'ф' (a key) should enter input mode")
		}
		if mm.InputMode() != "add-project" {
			t.Errorf("Expected input mode 'add-project', got %q", mm.InputMode())
		}
	})

	t.Run("delete via Russian в (d key)", func(t *testing.T) {
		m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
		newModel, _ := m.Update(runeKey('в'))
		mm := newModel.(*tui.MainMenuModel)
		if !mm.InDeleteMode() {
			t.Error("Russian 'в' (d key) should enter delete mode")
		}
	})

	t.Run("plain-terminal via Russian з (p key)", func(t *testing.T) {
		m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
		newModel, cmd := m.Update(runeKey('з'))
		mm := newModel.(*tui.MainMenuModel)
		result := mm.Result()
		if result == nil {
			t.Fatal("Russian 'з' (p key) should produce result")
		}
		if result.Action != "plain-terminal" {
			t.Errorf("Expected action 'plain-terminal', got %q", result.Action)
		}
		if cmd == nil {
			t.Error("Expected tea.Quit cmd")
		}
	})
}

// TestNonEnglish_MainMenu_HebrewNavigation verifies navigation with Hebrew layout.
func TestNonEnglish_MainMenu_HebrewNavigation(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")

	// Hebrew 'ח' is on the physical 'j' key
	newModel, _ := m.Update(runeKey('ח'))
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 1 {
		t.Errorf("Hebrew 'ח' (j key) should move down: expected 1, got %d", mm.SelectedItem())
	}

	// Hebrew 'ל' is on the physical 'k' key
	newModel, _ = mm.Update(runeKey('ל'))
	mm = newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 0 {
		t.Errorf("Hebrew 'ל' (k key) should move up: expected 0, got %d", mm.SelectedItem())
	}
}

// TestNonEnglish_MainMenu_ArabicSettings verifies settings shortcut with Arabic layout.
func TestNonEnglish_MainMenu_ArabicSettings(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")

	// Arabic 'س' is on the physical 's' key
	newModel, _ := m.Update(runeKey('س'))
	mm := newModel.(*tui.MainMenuModel)
	if !mm.InSettingsMode() {
		t.Error("Arabic 'س' (s key) should enter settings mode")
	}
}

// TestNonEnglish_Confirm_RussianY verifies confirmation with Russian 'н' (y key).
func TestNonEnglish_Confirm_RussianY(t *testing.T) {
	m := tui.NewConfirmDialog("Delete?")
	updated, cmd := m.Update(runeKey('н'))
	result := updated.(tui.ConfirmDialogModel)

	if !result.Confirmed {
		t.Error("Russian 'н' (y key) should confirm")
	}
	if cmd == nil {
		t.Error("Expected tea.Quit command")
	}
}

// TestNonEnglish_Confirm_RussianN verifies denial with Russian 'т' (n key).
func TestNonEnglish_Confirm_RussianN(t *testing.T) {
	m := tui.NewConfirmDialog("Delete?")
	updated, cmd := m.Update(runeKey('т'))
	result := updated.(tui.ConfirmDialogModel)

	if result.Confirmed {
		t.Error("Russian 'т' (n key) should deny")
	}
	if cmd == nil {
		t.Error("Expected tea.Quit command")
	}
}

// TestNonEnglish_Confirm_HebrewY verifies confirmation with Hebrew 'ט' (y key).
func TestNonEnglish_Confirm_HebrewY(t *testing.T) {
	m := tui.NewConfirmDialog("Delete?")
	updated, cmd := m.Update(runeKey('ט'))
	result := updated.(tui.ConfirmDialogModel)

	if !result.Confirmed {
		t.Error("Hebrew 'ט' (y key) should confirm")
	}
	if cmd == nil {
		t.Error("Expected tea.Quit command")
	}
}

// TestNonEnglish_MultiSelect_RussianJK verifies j/k navigation in multiselect
// with Russian layout.
func TestNonEnglish_MultiSelect_RussianJK(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: true},
		{Name: "opencode", Command: "opencode", Installed: true},
	}
	model := tui.NewMultiSelect(tools)

	// Russian 'о' is on physical 'j' key (move down)
	updated, _ := model.Update(runeKey('о'))
	m := updated.(tui.MultiSelectModel)
	if m.Cursor() != 1 {
		t.Errorf("Russian 'о' (j key) should move cursor down: expected 1, got %d", m.Cursor())
	}

	// Russian 'л' is on physical 'k' key (move up)
	updated, _ = m.Update(runeKey('л'))
	m = updated.(tui.MultiSelectModel)
	if m.Cursor() != 0 {
		t.Errorf("Russian 'л' (k key) should move cursor up: expected 0, got %d", m.Cursor())
	}
}

// TestNonEnglish_DeleteMode_RussianQ verifies quit-delete with Russian 'й' (q key).
func TestNonEnglish_DeleteMode_RussianQ(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")

	// Enter delete mode first
	newModel, _ := m.Update(runeKey('d'))
	mm := newModel.(*tui.MainMenuModel)
	if !mm.InDeleteMode() {
		t.Fatal("Should be in delete mode")
	}

	// Russian 'й' is on physical 'q' key (quit delete mode)
	newModel, _ = mm.Update(runeKey('й'))
	mm = newModel.(*tui.MainMenuModel)
	if mm.InDeleteMode() {
		t.Error("Russian 'й' (q key) should exit delete mode")
	}
}

// TestNonEnglish_Settings_RussianB verifies 'b' key no longer exits settings (only Esc does).
func TestNonEnglish_Settings_RussianB(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")

	// Enter settings mode first
	newModel, _ := m.Update(runeKey('s'))
	mm := newModel.(*tui.MainMenuModel)
	if !mm.InSettingsMode() {
		t.Fatal("Should be in settings mode")
	}

	// Russian 'и' is on physical 'b' key — should NOT exit settings anymore
	newModel, _ = mm.Update(runeKey('и'))
	mm = newModel.(*tui.MainMenuModel)
	if !mm.InSettingsMode() {
		t.Error("Russian 'и' (b key) should not exit settings mode")
	}
}
