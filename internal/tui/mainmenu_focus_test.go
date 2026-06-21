package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
)

func focusTestMenu() *MainMenuModel {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "opencode"}, "claude", "animated")
	m.SetSize(100, 40)
	return m
}

func TestFocus_defaultsToBody(t *testing.T) {
	m := focusTestMenu()
	if m.Focus() != FocusBody {
		t.Errorf("default focus = %v, want FocusBody", m.Focus())
	}
}

func TestFocus_upFromBodyFirstItemGoesToTabs(t *testing.T) {
	m := focusTestMenu()
	// selectedItem starts at 0 (first item)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusTabs {
		t.Errorf("focus after Up at first item = %v, want FocusTabs", m.Focus())
	}
	if m.SelectedItem() != 0 {
		t.Errorf("selectedItem moved to %d, want 0 (stays put when escaping to tabs)", m.SelectedItem())
	}
}

func TestFocus_upFromBodyNonFirstMoves(t *testing.T) {
	m := focusTestMenu()
	m.MoveDown() // selectedItem = 1
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusBody {
		t.Errorf("focus after Up at non-first = %v, want FocusBody", m.Focus())
	}
	if m.SelectedItem() != 0 {
		t.Errorf("selectedItem = %d, want 0", m.SelectedItem())
	}
}

func TestFocus_upFromTabsGoesToAI(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("focus after Up from tabs = %v, want FocusAI", m.Focus())
	}
}

func TestFocus_upFromAIStays(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("focus after Up from AI = %v, want FocusAI (top stop)", m.Focus())
	}
}

func TestFocus_downFromAIGoesToTabs(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("focus after Down from AI = %v, want FocusTabs", m.Focus())
	}
}

func TestFocus_downFromTabsGoesToBody(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusBody {
		t.Errorf("focus after Down from tabs = %v, want FocusBody", m.Focus())
	}
}

func TestFocus_leftRightOnTabsCyclesTab(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.ActiveTab() != TabSettings {
		t.Errorf("Right on tabs = %v, want TabSettings", m.ActiveTab())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.ActiveTab() != TabProjects {
		t.Errorf("Left on tabs = %v, want TabProjects", m.ActiveTab())
	}
}

func TestFocus_tabSwitchDoesNotCycleAI(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentAITool() != "claude" {
		t.Errorf("Right on tabs changed AI to %q, want unchanged claude", m.CurrentAITool())
	}
}

func TestFocus_enterOnTabsEntersBody(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	m.SetActiveTab(TabSettings)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Focus() != FocusBody {
		t.Errorf("Enter on tabs = %v, want FocusBody", m.Focus())
	}
}

func TestFocus_leftRightOnAICyclesTool(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentAITool() != "opencode" {
		t.Errorf("Right on AI = %q, want opencode", m.CurrentAITool())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.CurrentAITool() != "claude" {
		t.Errorf("Left on AI = %q, want claude", m.CurrentAITool())
	}
}

func TestFocus_bodyLeftRightDoesNotCycleAI(t *testing.T) {
	m := focusTestMenu()
	// default focus body, projects tab
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentAITool() != "claude" {
		t.Errorf("Right in projects body changed AI to %q, want unchanged", m.CurrentAITool())
	}
}

func TestFocus_settingsUpFromFirstRowGoesToTabs(t *testing.T) {
	m := focusTestMenu()
	m.EnterSettings() // activeTab=Settings, focus=Body, settingsSelected=0
	if m.Focus() != FocusBody {
		t.Fatalf("EnterSettings focus = %v, want FocusBody", m.Focus())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusTabs {
		t.Errorf("Up from settings row 0 = %v, want FocusTabs", m.Focus())
	}
}

func TestFocus_statsUpFromOffsetZeroGoesToTabs(t *testing.T) {
	m := focusTestMenu()
	m.SetActiveTab(TabStats)
	m.SetFocus(FocusBody)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusTabs {
		t.Errorf("Up from stats offset 0 = %v, want FocusTabs", m.Focus())
	}
}

func TestFocus_sAcceleratorFocusesBody(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	m.handleRune('s')
	if m.ActiveTab() != TabSettings {
		t.Errorf("'s' tab = %v, want Settings", m.ActiveTab())
	}
	if m.Focus() != FocusBody {
		t.Errorf("'s' focus = %v, want FocusBody", m.Focus())
	}
}
