package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
)

func TestConfigMenuItems(t *testing.T) {
	items := tui.GetConfigMenuItems()
	if len(items) != 3 {
		t.Errorf("Expected 3 menu items, got %d", len(items))
	}
	if items[0].Action != "manage-claude-configs" {
		t.Errorf("Expected first action 'manage-claude-configs', got %q", items[0].Action)
	}
	if items[1].Action != "toggle-auto-switch" {
		t.Errorf("Expected second action 'toggle-auto-switch', got %q", items[1].Action)
	}
	if items[2].Action != "reinstall" {
		t.Errorf("Expected third action 'reinstall', got %q", items[2].Action)
	}
}

func TestConfigMenu_New(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	if m.Selected() != nil {
		t.Error("Selected should be nil initially")
	}
}

func TestConfigMenu_NewWithOptions(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{
		Version: "2.6.0",
	})
	if m.Selected() != nil {
		t.Error("Selected should be nil initially")
	}
}

func TestConfigMenu_InitReturnsNil(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestConfigMenu_EnterSelectsFirstItem(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("Enter should return quit command")
	}
	result := updated.(tui.ConfigMenuModel)
	if result.Selected() == nil {
		t.Fatal("Enter should select current item")
	}
	if result.Selected().Action != "manage-claude-configs" {
		t.Errorf("Expected 'manage-claude-configs', got %q", result.Selected().Action)
	}
}

func TestConfigMenu_DownThenEnterSelectsSecondItem(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, cmd := updated.(tui.ConfigMenuModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("Enter should return quit command")
	}
	result := updated.(tui.ConfigMenuModel)
	if result.Selected() == nil {
		t.Fatal("Enter should select current item")
	}
	if result.Selected().Action != "toggle-auto-switch" {
		t.Errorf("Expected 'toggle-auto-switch', got %q", result.Selected().Action)
	}
}

func TestConfigMenu_CursorWrapsDown(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	// Three items: three Downs wrap back to the first.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(tui.ConfigMenuModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(tui.ConfigMenuModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(tui.ConfigMenuModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(tui.ConfigMenuModel)
	if result.Selected().Action != "manage-claude-configs" {
		t.Errorf("Expected wrap to first item, got %q", result.Selected().Action)
	}
}

func TestConfigMenu_CursorWrapsUp(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated, _ = updated.(tui.ConfigMenuModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(tui.ConfigMenuModel)
	if result.Selected().Action != "reinstall" {
		t.Errorf("Expected wrap to last item, got %q", result.Selected().Action)
	}
}

func TestConfigMenu_JKNavigation(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	updated, _ = updated.(tui.ConfigMenuModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(tui.ConfigMenuModel)
	if result.Selected().Action != "toggle-auto-switch" {
		t.Errorf("Expected 'j' to move down, got %q", result.Selected().Action)
	}
}

func TestConfigMenu_EscSelectsQuit(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Error("Esc should return quit command")
	}
	result := updated.(tui.ConfigMenuModel)
	if result.Selected() == nil || result.Selected().Action != "quit" {
		t.Errorf("Expected quit action")
	}
}

func TestConfigMenu_CtrlCSelectsQuit(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("Ctrl+C should return quit command")
	}
	result := updated.(tui.ConfigMenuModel)
	if result.Selected() == nil || result.Selected().Action != "quit" {
		t.Errorf("Expected quit action")
	}
}

func TestConfigMenu_ViewContainsBorder(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{
		Version: "2.6.0",
	})
	view := m.View()
	if !strings.Contains(view, "Wisp Deck Configuration") {
		t.Error("View should contain title")
	}
	if !strings.Contains(view, "Manage Claude configs") {
		t.Error("View should contain Manage Claude configs item")
	}
	if !strings.Contains(view, "Reinstall") {
		t.Error("View should contain Reinstall item")
	}
}

func TestConfigMenu_ViewShowsVersion(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{
		Version: "2.6.0",
	})
	view := m.View()
	if !strings.Contains(view, "v2.6.0") {
		t.Error("View should show version with v prefix")
	}
}

func TestConfigMenu_ViewEmptyAfterQuit(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(tui.ConfigMenuModel)
	if result.View() != "" {
		t.Error("View should be empty after quitting")
	}
}

func TestConfigMenu_WindowSizeMsg(t *testing.T) {
	m := tui.NewConfigMenu(tui.ConfigMenuOptions{})
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil {
		t.Error("WindowSizeMsg should return nil cmd")
	}
}
