package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/jackuait/ghost-tab/internal/tui"
)

func TestNewAIToolSelector(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: true},
		{Name: "opencode", Command: "opencode", Installed: false},
	}

	model := tui.NewAIToolSelector(tools)

	if model.Selected() != nil {
		t.Error("Expected selected to be nil initially")
	}
}

func TestAIToolSelectorSelected(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: true},
	}

	model := tui.NewAIToolSelector(tools)

	// Simulate selection by accessing through the model
	selected := model.Selected()

	if selected != nil {
		t.Error("Expected selected to be nil before any selection")
	}
}

func TestAIToolSelector_InitReturnsNil(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Command: "claude", Installed: true}}
	m := tui.NewAIToolSelector(tools)
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestAIToolSelector_EscCancels(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Command: "claude", Installed: true}}
	m := tui.NewAIToolSelector(tools)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Error("Esc should return quit command")
	}
	result := updated.(tui.AIToolSelectorModel)
	if result.Selected() != nil {
		t.Error("Esc should not select anything")
	}
}

func TestAIToolSelector_CtrlCCancels(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Command: "claude", Installed: true}}
	m := tui.NewAIToolSelector(tools)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("Ctrl+C should return quit command")
	}
	result := updated.(tui.AIToolSelectorModel)
	if result.Selected() != nil {
		t.Error("Ctrl+C should not select anything")
	}
}

func TestAIToolSelector_EnterSelectsInstalled(t *testing.T) {
	tools := []models.AITool{
		{Name: "claude", Command: "claude", Installed: true},
		{Name: "opencode", Command: "opencode", Installed: false},
	}
	m := tui.NewAIToolSelector(tools)
	// First item (claude, installed) is selected by default
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("Enter should return quit command")
	}
	result := updated.(tui.AIToolSelectorModel)
	if result.Selected() == nil {
		t.Error("Enter on installed tool should select it")
	}
	if result.Selected().Name != "claude" {
		t.Errorf("Expected claude, got %s", result.Selected().Name)
	}
}

func TestAIToolSelector_EnterSkipsUninstalled(t *testing.T) {
	tools := []models.AITool{
		{Name: "opencode", Command: "opencode", Installed: false},
	}
	m := tui.NewAIToolSelector(tools)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(tui.AIToolSelectorModel)
	if result.Selected() != nil {
		t.Error("Enter on uninstalled tool should not select it")
	}
}

func TestAIToolSelector_WindowSizeMsg(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Command: "claude", Installed: true}}
	m := tui.NewAIToolSelector(tools)
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil {
		t.Error("WindowSizeMsg should return nil cmd")
	}
	_ = updated // should not panic
}

func TestAIToolSelector_ViewNonEmpty(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Command: "claude", Installed: true}}
	m := tui.NewAIToolSelector(tools)
	// Need to set size first for list to render
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := updated.(tui.AIToolSelectorModel).View()
	if view == "" {
		t.Error("View should not be empty before quitting")
	}
}

func TestAIToolSelector_ViewEmptyAfterQuit(t *testing.T) {
	tools := []models.AITool{{Name: "claude", Command: "claude", Installed: true}}
	m := tui.NewAIToolSelector(tools)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(tui.AIToolSelectorModel)
	if result.View() != "" {
		t.Error("View should be empty after quitting")
	}
}
