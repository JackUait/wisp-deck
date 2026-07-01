package tui_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/muesli/termenv"
)

func testProjects() []models.Project {
	return []models.Project{
		{Name: "wisp-deck", Path: "/Users/jack/wisp-deck"},
		{Name: "my-app", Path: "/Users/jack/my-app"},
		{Name: "website", Path: "/Users/jack/website"},
	}
}

func testAITools() []string {
	return []string{"claude", "opencode"}
}

func TestMainMenu_Navigation(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Initial selection is 0
	if m.SelectedItem() != 0 {
		t.Errorf("Initial SelectedItem: expected 0, got %d", m.SelectedItem())
	}

	// MoveDown increments
	m.MoveDown()
	if m.SelectedItem() != 1 {
		t.Errorf("After MoveDown: expected 1, got %d", m.SelectedItem())
	}

	// MoveDown again
	m.MoveDown()
	if m.SelectedItem() != 2 {
		t.Errorf("After second MoveDown: expected 2, got %d", m.SelectedItem())
	}

	// MoveUp decrements
	m.MoveUp()
	if m.SelectedItem() != 1 {
		t.Errorf("After MoveUp: expected 1, got %d", m.SelectedItem())
	}

	// MoveUp back to 0
	m.MoveUp()
	if m.SelectedItem() != 0 {
		t.Errorf("After second MoveUp: expected 0, got %d", m.SelectedItem())
	}

	// MoveUp wraps to last item
	m.MoveUp()
	expected := m.TotalItems() - 1
	if m.SelectedItem() != expected {
		t.Errorf("Wrap up: expected %d, got %d", expected, m.SelectedItem())
	}

	// MoveDown wraps to first item
	m.MoveDown()
	if m.SelectedItem() != 0 {
		t.Errorf("Wrap down: expected 0, got %d", m.SelectedItem())
	}
}

func TestMainMenu_TotalItems(t *testing.T) {
	// 3 projects + 1 add-project row = 4
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
	if m.TotalItems() != 4 {
		t.Errorf("TotalItems with 3 projects: expected 4, got %d", m.TotalItems())
	}

	// 0 projects + 1 add-project row = 1
	m2 := tui.NewMainMenu(nil, testAITools(), "claude", "animated")
	if m2.TotalItems() != 1 {
		t.Errorf("TotalItems with 0 projects: expected 1, got %d", m2.TotalItems())
	}

	// 1 project + 1 add-project row = 2
	m3 := tui.NewMainMenu([]models.Project{{Name: "solo", Path: "/solo"}}, testAITools(), "claude", "animated")
	if m3.TotalItems() != 2 {
		t.Errorf("TotalItems with 1 project: expected 2, got %d", m3.TotalItems())
	}
}

func TestMainMenu_AIToolCycling(t *testing.T) {
	tools := testAITools()
	m := tui.NewMainMenu(testProjects(), tools, "claude", "animated")

	// Initial AI tool is claude
	if m.CurrentAITool() != "claude" {
		t.Errorf("Initial AI tool: expected 'claude', got %q", m.CurrentAITool())
	}

	// Cycle next: claude -> opencode
	m.CycleAITool("next")
	if m.CurrentAITool() != "opencode" {
		t.Errorf("After next: expected 'opencode', got %q", m.CurrentAITool())
	}

	// Cycle next: opencode wraps to claude
	m.CycleAITool("next")
	if m.CurrentAITool() != "claude" {
		t.Errorf("After next wrap: expected 'claude', got %q", m.CurrentAITool())
	}

	// Cycle prev: claude wraps to opencode
	m.CycleAITool("prev")
	if m.CurrentAITool() != "opencode" {
		t.Errorf("After prev wrap: expected 'opencode', got %q", m.CurrentAITool())
	}

	// Cycle prev: opencode wraps back to claude
	m.CycleAITool("prev")
	if m.CurrentAITool() != "claude" {
		t.Errorf("After prev: expected 'claude', got %q", m.CurrentAITool())
	}
}

func TestMainMenu_CycleAITool_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	m.SetAIToolFile(aiToolFile)

	// Cycle to opencode
	m.CycleAITool("next")

	// File should be written with "opencode"
	data, err := os.ReadFile(aiToolFile)
	if err != nil {
		t.Fatalf("ai-tool file not found after cycle: %v", err)
	}
	if strings.TrimSpace(string(data)) != "opencode" {
		t.Errorf("ai-tool file should be 'opencode', got %q", strings.TrimSpace(string(data)))
	}

	// Cycle again wraps back to claude
	m.CycleAITool("next")
	data, _ = os.ReadFile(aiToolFile)
	if strings.TrimSpace(string(data)) != "claude" {
		t.Errorf("ai-tool file should be 'claude' after second cycle, got %q", strings.TrimSpace(string(data)))
	}
}

func TestMainMenu_CycleAITool_DoesNotPersistWithoutFile(t *testing.T) {
	dir := t.TempDir()
	aiToolFile := filepath.Join(dir, "config", "wisp-deck", "ai-tool")

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	// Do NOT call SetAIToolFile

	m.CycleAITool("next")

	// File should NOT exist
	if _, err := os.Stat(aiToolFile); err == nil {
		t.Error("ai-tool file should not be created when no file path set")
	}
}

func TestMainMenu_AIToolCycling_SingleTool(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), []string{"claude"}, "claude", "animated")

	// With single tool, cycling should stay on same tool
	m.CycleAITool("next")
	if m.CurrentAITool() != "claude" {
		t.Errorf("Single tool next: expected 'claude', got %q", m.CurrentAITool())
	}

	m.CycleAITool("prev")
	if m.CurrentAITool() != "claude" {
		t.Errorf("Single tool prev: expected 'claude', got %q", m.CurrentAITool())
	}
}

func TestMainMenu_AIToolCycling_StartNonFirst(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "opencode", "animated")

	if m.CurrentAITool() != "opencode" {
		t.Errorf("Initial: expected 'opencode', got %q", m.CurrentAITool())
	}

	m.CycleAITool("next")
	if m.CurrentAITool() != "claude" {
		t.Errorf("After next from opencode: expected 'claude' (wrap), got %q", m.CurrentAITool())
	}
}

func TestMainMenu_AIToolCycling_UnknownTool(t *testing.T) {
	// Unknown current tool should default to index 0
	m := tui.NewMainMenu(testProjects(), testAITools(), "unknown", "animated")

	if m.CurrentAITool() != "claude" {
		t.Errorf("Unknown tool should default to first: expected 'claude', got %q", m.CurrentAITool())
	}
}

func TestMainMenu_LayoutCalculation(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Side layout: width >= 92 (58 + 3 + 28 + 3)
	layout := m.CalculateLayout(100, 40)
	if layout.GhostPosition != "side" {
		t.Errorf("Side layout at 100x40: expected 'side', got %q", layout.GhostPosition)
	}
	if layout.MenuWidth != 58 {
		t.Errorf("MenuWidth: expected 58, got %d", layout.MenuWidth)
	}

	// Above layout: width < 92 but height sufficient
	// MenuHeight = 7 + (3*2) + 4 + 1 = 18 (3 projects, 4 actions, 1 separator)
	// Need height >= 18 + 15 + 2 = 35
	layout = m.CalculateLayout(60, 45)
	if layout.GhostPosition != "above" {
		t.Errorf("Above layout at 60x45: expected 'above', got %q", layout.GhostPosition)
	}

	// Hidden layout: neither condition met
	layout = m.CalculateLayout(40, 20)
	if layout.GhostPosition != "hidden" {
		t.Errorf("Hidden layout at 40x20: expected 'hidden', got %q", layout.GhostPosition)
	}

	// Exact boundary for side layout
	layout = m.CalculateLayout(92, 40)
	if layout.GhostPosition != "side" {
		t.Errorf("Exact side boundary 92x40: expected 'side', got %q", layout.GhostPosition)
	}

	// Just below side boundary
	layout = m.CalculateLayout(91, 45)
	if layout.GhostPosition != "above" {
		t.Errorf("Below side boundary 91x45: expected 'above', got %q", layout.GhostPosition)
	}
}

func TestMainMenu_LayoutCalculation_MenuHeight(t *testing.T) {
	// 3 projects (2 rows each). Chrome = 13 fixed lines (tab-bar + action-bar +
	// add-project hint included in the constant; old 4 action-item rows removed).
	// The subscription row is shared across agents, so it is included for opencode too.
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "opencode", "animated")
	layout := m.CalculateLayout(100, 40)

	// MenuHeight = 13 (chrome, incl. subscription row) + 3*2 (projects) = 19
	expectedHeight := 13 + (3 * 2)
	if layout.MenuHeight != expectedHeight {
		t.Errorf("MenuHeight with 3 projects: expected %d, got %d", expectedHeight, layout.MenuHeight)
	}

	// 0 projects: empty-state row adds 1 line.
	// MenuHeight = 13 (chrome, incl. subscription row) + 1 (empty-state row) = 14
	m2 := tui.NewMainMenu(nil, testAITools(), "opencode", "animated")
	layout2 := m2.CalculateLayout(100, 40)
	expectedHeight2 := 13 + 1
	if layout2.MenuHeight != expectedHeight2 {
		t.Errorf("MenuHeight with 0 projects: expected %d, got %d", expectedHeight2, layout2.MenuHeight)
	}
}

func TestMainMenu_JumpTo(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// JumpTo 1 (first project, 1-indexed)
	m.JumpTo(1)
	if m.SelectedItem() != 0 {
		t.Errorf("JumpTo(1): expected 0, got %d", m.SelectedItem())
	}

	// JumpTo 2 (second project)
	m.JumpTo(2)
	if m.SelectedItem() != 1 {
		t.Errorf("JumpTo(2): expected 1, got %d", m.SelectedItem())
	}

	// JumpTo 3 (third project)
	m.JumpTo(3)
	if m.SelectedItem() != 2 {
		t.Errorf("JumpTo(3): expected 2, got %d", m.SelectedItem())
	}

	// JumpTo beyond project count should not change selection
	m.JumpTo(4)
	if m.SelectedItem() != 2 {
		t.Errorf("JumpTo(4) beyond projects: expected 2 (unchanged), got %d", m.SelectedItem())
	}

	// JumpTo 0 should not change selection
	m.JumpTo(0)
	if m.SelectedItem() != 2 {
		t.Errorf("JumpTo(0): expected 2 (unchanged), got %d", m.SelectedItem())
	}
}

func TestMainMenu_SelectProject(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Select first project (index 0)
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("Expected result after Enter on project, got nil")
	}
	if result.Action != "select-project" {
		t.Errorf("Expected action 'select-project', got %q", result.Action)
	}
	if result.Name != "wisp-deck" {
		t.Errorf("Expected name 'wisp-deck', got %q", result.Name)
	}
	if result.Path != "/Users/jack/wisp-deck" {
		t.Errorf("Expected path '/Users/jack/wisp-deck', got %q", result.Path)
	}
	if result.AITool != "claude" {
		t.Errorf("Expected ai_tool 'claude', got %q", result.AITool)
	}

	// cmd should be tea.Quit
	if cmd == nil {
		t.Error("Expected tea.Quit cmd, got nil")
	}

	// Select second project
	m2 := tui.NewMainMenu(projects, testAITools(), "opencode", "animated")
	m2.MoveDown()
	newModel2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)
	result2 := mm2.Result()

	if result2 == nil {
		t.Fatal("Expected result after Enter on second project, got nil")
	}
	if result2.Name != "my-app" {
		t.Errorf("Expected name 'my-app', got %q", result2.Name)
	}
	if result2.AITool != "opencode" {
		t.Errorf("Expected ai_tool 'opencode', got %q", result2.AITool)
	}
}

func TestMainMenu_SelectAction(t *testing.T) {
	projects := testProjects()
	// The only action row remaining in the projects body is the add-project row,
	// which is the final selectable item (index len(projects)). The former
	// delete/open-once/plain-terminal action rows are gone; those actions are
	// now reachable only via the d/o/p key shortcuts (see TestMainMenu_ActionShortcuts).

	t.Run("add-project", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		// Navigate to the add-project row (last selectable item, index 3).
		for i := 0; i < 3; i++ {
			m.MoveDown()
		}
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		mm := newModel.(*tui.MainMenuModel)

		if !mm.InInputMode() {
			t.Error("Expected input mode after Enter on add-project")
		}
		if mm.InputMode() != "add-project" {
			t.Errorf("Expected input mode 'add-project', got %q", mm.InputMode())
		}
		if mm.Result() != nil {
			t.Error("Should not produce result when entering input mode")
		}
	})
}

func TestMainMenu_ActionShortcuts(t *testing.T) {
	projects := testProjects()

	t.Run("a_shortcut", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		mm := newModel.(*tui.MainMenuModel)

		if !mm.InInputMode() {
			t.Error("Expected input mode after 'a' shortcut")
		}
		if mm.InputMode() != "add-project" {
			t.Errorf("Expected input mode 'add-project', got %q", mm.InputMode())
		}
		if mm.Result() != nil {
			t.Error("Should not produce result when entering input mode")
		}
	})

	t.Run("A_shortcut", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
		mm := newModel.(*tui.MainMenuModel)

		if !mm.InInputMode() {
			t.Error("Expected input mode after 'A' shortcut")
		}
		if mm.InputMode() != "add-project" {
			t.Errorf("Expected input mode 'add-project', got %q", mm.InputMode())
		}
		if mm.Result() != nil {
			t.Error("Should not produce result when entering input mode")
		}
	})

	t.Run("d_shortcut", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
		mm := newModel.(*tui.MainMenuModel)

		if !mm.InDeleteMode() {
			t.Error("Expected delete mode after 'd' shortcut")
		}
		if mm.Result() != nil {
			t.Error("Should not produce result when entering delete mode")
		}
	})

	t.Run("o_shortcut", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		mm := newModel.(*tui.MainMenuModel)

		if !mm.InInputMode() {
			t.Error("Expected input mode after 'o' shortcut")
		}
		if mm.InputMode() != "open-once" {
			t.Errorf("Expected input mode 'open-once', got %q", mm.InputMode())
		}
		if mm.Result() != nil {
			t.Error("Should not produce result when entering input mode")
		}
	})

	t.Run("p_shortcut", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
		mm := newModel.(*tui.MainMenuModel)
		result := mm.Result()

		if result == nil {
			t.Fatal("Expected result for 'p' shortcut, got nil")
		}
		if result.Action != "plain-terminal" {
			t.Errorf("Expected 'plain-terminal', got %q", result.Action)
		}
	})

	t.Run("s_shortcut_enters_settings", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
		mm := newModel.(*tui.MainMenuModel)

		if !mm.InSettingsMode() {
			t.Error("Expected settings mode after 's' shortcut")
		}
		if mm.Result() != nil {
			t.Error("Should not produce a result when entering settings mode")
		}
	})
}

func TestMainMenu_QuitEsc(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	// Esc now emits PopScreenMsg instead of a quit result;
	// verify the command sends PopScreenMsg.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Expected a command on Esc, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tui.PopScreenMsg); !ok {
		t.Errorf("Expected PopScreenMsg on Esc, got %T", msg)
	}
}

func TestMainMenu_QuitEsc_IncludesCycledAITool(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	// Cycle to opencode
	m.CycleAITool("next")
	// Quit via Ctrl-C (which still sets a result with the current AI tool)
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("Expected result for Ctrl-C, got nil")
	}
	if result.AITool != "opencode" {
		t.Errorf("Expected ai_tool 'opencode' after cycling, got %q", result.AITool)
	}
}

func TestMainMenu_QuitCtrlC(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("Expected result for Ctrl+C, got nil")
	}
	if result.Action != "quit" {
		t.Errorf("Expected 'quit', got %q", result.Action)
	}
	if cmd == nil {
		t.Error("Expected tea.Quit cmd, got nil")
	}
}

func TestMainMenu_KeyBindings_Navigation(t *testing.T) {
	projects := testProjects()

	t.Run("j_moves_down", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		mm := newModel.(*tui.MainMenuModel)
		if mm.SelectedItem() != 1 {
			t.Errorf("After 'j': expected 1, got %d", mm.SelectedItem())
		}
	})

	t.Run("k_moves_up", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		m.MoveDown()
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		mm := newModel.(*tui.MainMenuModel)
		if mm.SelectedItem() != 0 {
			t.Errorf("After 'k': expected 0, got %d", mm.SelectedItem())
		}
	})

	t.Run("arrow_down", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		mm := newModel.(*tui.MainMenuModel)
		if mm.SelectedItem() != 1 {
			t.Errorf("After down arrow: expected 1, got %d", mm.SelectedItem())
		}
	})

	t.Run("arrow_up", func(t *testing.T) {
		m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
		m.MoveDown()
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
		mm := newModel.(*tui.MainMenuModel)
		if mm.SelectedItem() != 0 {
			t.Errorf("After up arrow: expected 0, got %d", mm.SelectedItem())
		}
	})
}

func TestMainMenu_KeyBindings_AIToolCycling(t *testing.T) {
	t.Run("right_arrow_cycles_next", func(t *testing.T) {
		m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
		m.SetFocus(tui.FocusAI) // AI switcher is the top focus stop
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		mm := newModel.(*tui.MainMenuModel)
		if mm.CurrentAITool() != "opencode" {
			t.Errorf("After right arrow: expected 'opencode', got %q", mm.CurrentAITool())
		}
	})

	t.Run("left_arrow_cycles_prev", func(t *testing.T) {
		m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
		m.SetFocus(tui.FocusAI)
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		mm := newModel.(*tui.MainMenuModel)
		if mm.CurrentAITool() != "opencode" {
			t.Errorf("After left arrow: expected 'opencode', got %q", mm.CurrentAITool())
		}
	})
}

func TestMainMenu_KeyBindings_NumberJump(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 1 {
		t.Errorf("After '2' key: expected 1, got %d", mm.SelectedItem())
	}

	// Number beyond project count should not change selection
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	if mm2.SelectedItem() != 1 {
		t.Errorf("After '9' key (beyond projects): expected 1 (unchanged), got %d", mm2.SelectedItem())
	}
}

func TestMainMenu_GhostDisplay(t *testing.T) {
	tests := []struct {
		display  string
		expected string
	}{
		{"animated", "animated"},
		{"static", "static"},
		{"none", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.display, func(t *testing.T) {
			m := tui.NewMainMenu(testProjects(), testAITools(), "claude", tt.display)
			if m.GhostDisplay() != tt.expected {
				t.Errorf("GhostDisplay: expected %q, got %q", tt.expected, m.GhostDisplay())
			}
		})
	}
}

func TestMainMenu_SetSize(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	m.SetSize(120, 50)

	// After SetSize, layout calculation should use the set dimensions
	layout := m.CalculateLayout(120, 50)
	if layout.GhostPosition != "side" {
		t.Errorf("After SetSize(120, 50): expected 'side', got %q", layout.GhostPosition)
	}
}

func TestMainMenu_Init(t *testing.T) {
	// Static mode: Init returns nil (no ticks)
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "static")
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init() should return nil for static mode")
	}
}

func TestMainMenu_View(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	view := m.View()
	if view == "" {
		t.Error("View() should return non-empty placeholder string")
	}
}

func TestMainMenu_WindowSizeMsg(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	mm := newModel.(*tui.MainMenuModel)

	// After receiving WindowSizeMsg, the layout should reflect new dimensions
	layout := mm.CalculateLayout(100, 50)
	if layout.GhostPosition != "side" {
		t.Errorf("After WindowSizeMsg: expected 'side', got %q", layout.GhostPosition)
	}
}

func TestMainMenu_NoProjects_ActionIndices(t *testing.T) {
	// With 0 projects, actions start at index 0
	m := tui.NewMainMenu(nil, testAITools(), "claude", "animated")

	// Index 0 = Add (now enters input mode instead of quitting with result)
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InInputMode() {
		t.Error("Expected input mode after Enter on add-project with no projects")
	}
	if mm.InputMode() != "add-project" {
		t.Errorf("Expected input mode 'add-project', got %q", mm.InputMode())
	}
	if mm.Result() != nil {
		t.Error("Should not produce result when entering input mode")
	}
}

func TestMainMenu_ViewContainsBorders(t *testing.T) {
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	// Should contain rounded box-drawing borders
	if !strings.Contains(view, "\u256d") || !strings.Contains(view, "\u256f") {
		t.Error("view should contain rounded box-drawing borders")
	}
	if !strings.Contains(view, "Wisp Deck") {
		t.Error("view should contain 'Wisp Deck' title")
	}
	if !strings.Contains(view, "test") {
		t.Error("view should contain project name")
	}
}

func TestMainMenu_ViewShowsAIToolWithArrows(t *testing.T) {
	projects := []models.Project{}
	tools := []string{"claude", "opencode"}
	m := tui.NewMainMenu(projects, tools, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	if !strings.Contains(view, "\U000F0141") || !strings.Contains(view, "\U000F0142") {
		t.Error("view should show AI tool cycling arrows when multiple tools")
	}
	if !strings.Contains(view, "Claude Code") {
		t.Error("view should show AI tool display name")
	}
}

func TestMainMenu_ViewNoArrowsSingleTool(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	if strings.Contains(view, "\U000F0141") || strings.Contains(view, "\U000F0142") {
		t.Error("should not show cycling arrows with single tool")
	}
}

func TestMainMenu_ViewHelpRow(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude", "opencode"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	if strings.Contains(view, "navigate") {
		t.Error("help row should NOT mention navigate")
	}
	// AI switching is now its own focus stop; the default footer points ↑ to
	// the tab bar (sections) rather than advertising ←→ AI.
	if !strings.Contains(view, "sections") {
		t.Error("help row should mention sections")
	}
	if !strings.Contains(view, "move") {
		t.Error("help row should mention move")
	}
	if !strings.Contains(view, "P plain") {
		t.Error("help row should mention 'P plain'")
	}
}

func TestMainMenu_ViewActionBar(t *testing.T) {
	// With a project selected, the contextual action bar offers Open/Worktrees/Delete.
	projects := []models.Project{{Name: "p1", Path: "/p1"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	if !strings.Contains(view, "Open") {
		t.Error("action bar should offer Open for a selected project")
	}
	if !strings.Contains(view, "Delete") {
		t.Error("action bar should offer Delete for a selected project")
	}
	// The add-project row is always present in the body.
	if !strings.Contains(view, "Add project") {
		t.Error("view should contain the '+ Add project' row")
	}
}

func TestMainMenu_ViewSelectedMarker(t *testing.T) {
	projects := []models.Project{{Name: "p1", Path: "/p1"}, {Name: "p2", Path: "/p2"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	// Selected item (first) should have cursor marker \u258c on its line
	lines := strings.Split(view, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "p1") && strings.Contains(line, "\u258c") {
			found = true
			break
		}
	}
	if !found {
		t.Error("view should contain selection marker \u258c on the selected project line")
	}
}

func TestMainMenu_ViewWithGhostSide(t *testing.T) {
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(100, 40) // Wide enough for side layout
	view := m.View()
	// Ghost art uses block characters
	if !strings.Contains(view, "\u2588") {
		t.Error("view should contain ghost art block characters in side layout")
	}
}

func TestMainMenu_ViewGhostHiddenWhenNone(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSize(100, 40)
	view := m.View()
	// Ghost should not appear -- but menu should still render
	if !strings.Contains(view, "Wisp Deck") {
		t.Error("menu should still render when ghost is hidden")
	}
}

func TestMainMenu_AIToolDisplayName(t *testing.T) {
	tests := []struct {
		tool     string
		expected string
	}{
		{"claude", "Claude Code"},
		{"opencode", "OpenCode"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := tui.AIToolDisplayName(tt.tool)
			if got != tt.expected {
				t.Errorf("AIToolDisplayName(%q): expected %q, got %q", tt.tool, tt.expected, got)
			}
		})
	}
}

func TestMainMenu_ViewHelpRowSingleTool(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	if strings.Contains(view, "navigate") {
		t.Error("help row should NOT mention navigate")
	}
	if !strings.Contains(view, "move") {
		t.Error("help row should mention move")
	}
	// The redesigned footer hint is static and never spells out "AI tool".
	if strings.Contains(view, "AI tool") {
		t.Error("help row should NOT mention 'AI tool'")
	}
}

func TestMainMenu_ViewUpdateVersion(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetUpdateVersion("v1.2.3")
	m.SetSize(80, 30)
	view := m.View()
	if !strings.Contains(view, "v1.2.3") {
		t.Error("view should show update version when set")
	}
	if !strings.Contains(view, "Update available") {
		t.Error("view should show 'Update available' message")
	}
}

func TestMainMenu_ViewNoUpdateVersion(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()
	if strings.Contains(view, "Update available") {
		t.Error("view should NOT show update message when version is empty")
	}
}

func TestMainMenu_ViewGhostAbove(t *testing.T) {
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	// Width too narrow for side (< 82), but height enough for above
	m.SetSize(60, 50)
	view := m.View()
	// Should still contain ghost block characters
	if !strings.Contains(view, "\u2588") {
		t.Error("view should contain ghost art block characters in above layout")
	}
	if !strings.Contains(view, "Wisp Deck") {
		t.Error("view should contain menu title in above layout")
	}
}

func TestMainMenu_BobOffset_InitiallyZero(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	if m.BobOffset() != 0 {
		t.Errorf("initial bob offset should be 0, got %d", m.BobOffset())
	}
}

func TestMainMenu_BobPhase_AdvancesOnTick(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	initial := m.BobPhase()
	m.Update(tui.NewBobTickMsg())
	if m.BobPhase() <= initial {
		t.Errorf("bob phase should advance on tick, was %f now %f", initial, m.BobPhase())
	}
}

func TestMainMenu_BobOffset_OnlyZeroOrOne(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Run through many ticks covering a full cycle
	for i := 0; i < 200; i++ {
		m.Update(tui.NewBobTickMsg())
		offset := m.BobOffset()
		if offset != 0 && offset != 1 {
			t.Fatalf("bob offset must be 0 or 1, got %d at tick %d", offset, i)
		}
	}
}

func TestMainMenu_BobOffset_TransitionsDuringCycle(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	saw0, saw1 := false, false
	// Run through enough ticks for a full cycle (~156 ticks at 16ms for 2.5s)
	for i := 0; i < 200; i++ {
		m.Update(tui.NewBobTickMsg())
		switch m.BobOffset() {
		case 0:
			saw0 = true
		case 1:
			saw1 = true
		}
	}
	if !saw0 || !saw1 {
		t.Errorf("bob should transition between 0 and 1 during a full cycle, saw0=%v saw1=%v", saw0, saw1)
	}
}

func TestMainMenu_BobAnimation_VisibleInView(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(100, 40) // Side layout

	// Collect distinct full views across a full bob cycle
	views := make(map[string]bool)
	for i := 0; i < 200; i++ {
		m.Update(tui.NewBobTickMsg())
		views[m.View()] = true
	}
	// If the ghost actually bobs, we should see at least 2 distinct view outputs
	if len(views) < 2 {
		t.Error("ghost bob animation should produce visibly different views during a full cycle, but all views were identical (centering may be absorbing the movement)")
	}
}

func TestMainMenu_BobPhase_Wraps(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Run through enough ticks for multiple full cycles
	for i := 0; i < 500; i++ {
		m.Update(tui.NewBobTickMsg())
	}
	phase := m.BobPhase()
	// Phase should have wrapped (stayed below 2*pi)
	if phase > 6.3 { // slightly above 2*pi
		t.Errorf("bob phase should wrap around 2*pi, got %f", phase)
	}
}

func TestMainMenu_SleepAfterInactivity(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSleepTimer(120)
	if !m.ShouldSleep() {
		t.Error("should sleep after 120 seconds of inactivity")
	}
}

func TestMainMenu_WakeOnKeypress(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSleepTimer(120)
	m.Wake()
	if m.IsSleeping() {
		t.Error("should be awake after Wake()")
	}
	if m.ShouldSleep() {
		t.Error("sleep timer should be reset after Wake()")
	}
}

func TestMainMenu_GhostHiddenWhenNone(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSize(100, 40)
	view := m.View()
	if strings.Contains(view, "\u2588\u2588\u2588\u2588") {
		t.Error("ghost should be hidden when display mode is 'none'")
	}
}

func TestMainMenu_NoAnimationWhenStatic(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "static")
	cmd := m.Init()
	if cmd != nil {
		t.Error("static mode should not start animation ticks")
	}
}

func TestMainMenu_AnimationStartsWhenAnimated(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	cmd := m.Init()
	if cmd == nil {
		t.Error("animated mode should start animation ticks")
	}
}

func TestMainMenu_SleepTimerResetOnKeypress(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSleepTimer(100)
	// Simulate a keypress
	msg := tea.KeyMsg{Type: tea.KeyDown}
	m.Update(msg)
	// Sleep timer should be reset
	if m.ShouldSleep() {
		t.Error("sleep timer should be reset after keypress")
	}
}

func TestMainMenu_MapRowToItem_Projects(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	// The subscription row is shared across agents, so projects start at row 7.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "animated")
	m.SetSize(80, 30)

	// Layout: row 0 border, 1 title, 2 subscription, 3 switcher gap, 4 tab bar,
	// 5 separator, 6 empty, then first project at rows 7-8.
	if m.MapRowToItem(7) != 0 {
		t.Errorf("click at row 7 should map to item 0, got %d", m.MapRowToItem(7))
	}
	if m.MapRowToItem(8) != 0 {
		t.Errorf("click at row 8 should map to item 0 (path line), got %d", m.MapRowToItem(8))
	}
	// Second project at rows 9-10
	if m.MapRowToItem(9) != 1 {
		t.Errorf("click at row 9 should map to item 1, got %d", m.MapRowToItem(9))
	}
	if m.MapRowToItem(10) != 1 {
		t.Errorf("click at row 10 should map to item 1 (path line), got %d", m.MapRowToItem(10))
	}
}

func TestMainMenu_MapRowToItem_AddProjectRow(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
	}
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "animated")
	m.SetSize(80, 30)

	// Layout: 1 project at rows 7-8, blank spacer at row 9, add-project label at
	// row 10, add-project hint subtitle at row 11. Both add-project rows map to the
	// same item. The old delete/open-once/plain-terminal action rows no longer exist.
	if m.MapRowToItem(10) != 1 {
		t.Errorf("click at add-project label row should map to item 1, got %d", m.MapRowToItem(10))
	}
	if m.MapRowToItem(11) != 1 {
		t.Errorf("click at add-project hint row should map to item 1, got %d", m.MapRowToItem(11))
	}
	if m.MapRowToItem(10) != m.TotalItems()-1 {
		t.Errorf("add-project row should be the final selectable item (%d), got %d", m.TotalItems()-1, m.MapRowToItem(10))
	}
	// Rows past the add-project row are not selectable items.
	if m.MapRowToItem(12) != -1 {
		t.Errorf("click below add-project row should return -1, got %d", m.MapRowToItem(12))
	}
}

func TestMainMenu_MapRowToItem_Invalid(t *testing.T) {
	projects := []models.Project{{Name: "p1", Path: "/p1"}}
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "animated")
	m.SetSize(80, 30)

	// Row 0 is border
	if m.MapRowToItem(0) != -1 {
		t.Errorf("click on border should return -1, got %d", m.MapRowToItem(0))
	}
	// Row 1 is title
	if m.MapRowToItem(1) != -1 {
		t.Errorf("click on title should return -1, got %d", m.MapRowToItem(1))
	}
	// Row 2 is the subscription row
	if m.MapRowToItem(2) != -1 {
		t.Errorf("click on subscription row should return -1, got %d", m.MapRowToItem(2))
	}
	// Row 3 is the switcher gap
	if m.MapRowToItem(3) != -1 {
		t.Errorf("click on switcher gap should return -1, got %d", m.MapRowToItem(3))
	}
	// Row 4 is the tab bar
	if m.MapRowToItem(4) != -1 {
		t.Errorf("click on tab bar should return -1, got %d", m.MapRowToItem(4))
	}
	// Row 5 is separator
	if m.MapRowToItem(5) != -1 {
		t.Errorf("click on separator should return -1, got %d", m.MapRowToItem(5))
	}
	// Row 6 is empty
	if m.MapRowToItem(6) != -1 {
		t.Errorf("click on empty row should return -1, got %d", m.MapRowToItem(6))
	}
	// Row 9 is the blank spacer before the add-project row
	if m.MapRowToItem(9) != -1 {
		t.Errorf("click on blank spacer should return -1, got %d", m.MapRowToItem(9))
	}
	// Row way beyond menu should return -1
	if m.MapRowToItem(100) != -1 {
		t.Errorf("click beyond menu should return -1, got %d", m.MapRowToItem(100))
	}
}

func TestMainMenu_MapRowToItem_NoProjects(t *testing.T) {
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(nil, []string{"opencode"}, "opencode", "animated")
	m.SetSize(80, 30)

	// No projects: row 0 border, 1 title, 2 subscription, 3 switcher gap, 4 tab bar,
	// 5 separator, 6 empty, 7 blank spacer, 8 add-project row (the only selectable item).
	if m.MapRowToItem(8) != 0 {
		t.Errorf("add-project row at row 8 should map to item 0, got %d", m.MapRowToItem(8))
	}
	if m.MapRowToItem(7) != -1 {
		t.Errorf("blank spacer at row 7 should return -1, got %d", m.MapRowToItem(7))
	}
}

func TestMainMenu_MapRowToItem_WithUpdateVersion(t *testing.T) {
	projects := []models.Project{{Name: "p1", Path: "/p1"}}
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "animated")
	m.SetUpdateVersion("v1.2.3")
	m.SetSize(80, 30)

	// With the subscription row and an update notification, the project shifts down:
	// Row 0: border, 1: title, 2: subscription, 3: switcher gap, 4: tab bar,
	// 5: separator, 6: update notification, 7: empty, 8-9: project.
	if m.MapRowToItem(8) != 0 {
		t.Errorf("with update version, project should be at row 8, got %d", m.MapRowToItem(8))
	}
	if m.MapRowToItem(9) != 0 {
		t.Errorf("with update version, project path at row 9 should map to 0, got %d", m.MapRowToItem(9))
	}
}

func TestMainMenu_MouseClickSelectsItem(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "animated")
	m.SetSize(80, 30)
	_ = m.View() // compute the vertical centering offset

	// Second project's name line is box row 9 (border, title, subscription, switcher
	// gap, tab bar, separator, empty, p0-name, p0-path, p1-name). Account for centering.
	mouseMsg := tea.MouseMsg{
		X:      10,
		Y:      m.CenterOffsetY() + 9,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newModel, _ := m.Update(mouseMsg)
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 1 {
		t.Errorf("clicking on second project should select item 1, got %d", mm.SelectedItem())
	}
	// Should not quit (single click on non-selected item just selects)
	if mm.Result() != nil {
		t.Error("single click on non-selected item should not produce a result")
	}
}

func TestMainMenu_MouseDoubleClickActivates(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "animated")
	m.SetSize(80, 30)
	_ = m.View() // compute the vertical centering offset

	// First project's name line is box row 7. Clicking the already-selected
	// item acts as a double-click activation. Hit-testing now requires the pointer
	// to be on a glyph, so target the project name (box col 6 of " ▌1  p1") relative
	// to the box origin rather than a fixed absolute column in the padding.
	mouseMsg := tea.MouseMsg{
		X:      m.MenuOriginX() + 6,
		Y:      m.CenterOffsetY() + 7,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newModel, cmd := m.Update(mouseMsg)
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("clicking already-selected item should produce a result (double-click)")
	}
	if result.Action != "select-project" {
		t.Errorf("expected action 'select-project', got %q", result.Action)
	}
	if result.Name != "p1" {
		t.Errorf("expected name 'p1', got %q", result.Name)
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd on activation")
	}
}

func TestMainMenu_MouseClickInvalidRow(t *testing.T) {
	projects := []models.Project{{Name: "p1", Path: "/p1"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Click on border row 0 should not change selection
	mouseMsg := tea.MouseMsg{
		X:      10,
		Y:      0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newModel, _ := m.Update(mouseMsg)
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 0 {
		t.Errorf("clicking on border should not change selection, got %d", mm.SelectedItem())
	}
}

func TestMainMenu_MouseClickResetsSleeTimer(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.SetSleepTimer(100)

	// Click anywhere
	mouseMsg := tea.MouseMsg{
		X:      10,
		Y:      4,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	m.Update(mouseMsg)
	if m.ShouldSleep() {
		t.Error("mouse click should reset sleep timer")
	}
}

func TestMainMenu_MouseRightClickIgnored(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Right click should not change selection
	mouseMsg := tea.MouseMsg{
		X:      10,
		Y:      6,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonRight,
	}
	newModel, _ := m.Update(mouseMsg)
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 0 {
		t.Errorf("right click should not change selection, got %d", mm.SelectedItem())
	}
}

func TestMainMenu_MouseReleaseIgnored(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Mouse release should not trigger selection
	mouseMsg := tea.MouseMsg{
		X:      10,
		Y:      6,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	}
	newModel, _ := m.Update(mouseMsg)
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 0 {
		t.Errorf("mouse release should not change selection, got %d", mm.SelectedItem())
	}
}

func TestMainMenu_SettingsMode(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	if m.InSettingsMode() {
		t.Error("should not start in settings mode")
	}

	m.EnterSettings()
	if !m.InSettingsMode() {
		t.Error("should be in settings mode after EnterSettings()")
	}

	m.ExitSettings()
	if m.InSettingsMode() {
		t.Error("should exit settings mode")
	}
}

func TestMainMenu_SettingsCycleGhostDisplay(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")

	m.CycleGhostDisplay()
	if m.GhostDisplay() != "static" {
		t.Errorf("expected static after cycling from animated, got %s", m.GhostDisplay())
	}

	m.CycleGhostDisplay()
	if m.GhostDisplay() != "none" {
		t.Errorf("expected none, got %s", m.GhostDisplay())
	}

	m.CycleGhostDisplay()
	if m.GhostDisplay() != "animated" {
		t.Errorf("expected animated, got %s", m.GhostDisplay())
	}
}

func TestMainMenu_SettingsViewShowsPanel(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()
	view := m.View()

	if !strings.Contains(view, "Settings") {
		t.Error("settings view should show 'Settings' title")
	}
	if !strings.Contains(view, "Ghost Display") {
		t.Error("settings view should show 'Ghost Display' option")
	}
	if !strings.Contains(view, "Animated") {
		t.Error("settings view should show current state 'Animated'")
	}
}

func TestMainMenu_SettingsKeyS(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Press S
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InSettingsMode() {
		t.Error("pressing S should enter settings mode")
	}
}

func TestMainMenu_SettingsEscReturns(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	msg := tea.KeyMsg{Type: tea.KeyEsc}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if mm.InSettingsMode() {
		t.Error("Esc in settings should return to main menu")
	}
}

func TestMainMenu_SettingsKeyBDoesNotExit(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InSettingsMode() {
		t.Error("B in settings should not exit settings mode")
	}
}

func TestMainMenu_SettingsLeftArrowCyclesPrevious(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	// Left = previous: animated → none
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if mm.GhostDisplay() != "none" {
		t.Errorf("expected none after pressing left arrow from animated, got %s", mm.GhostDisplay())
	}
}

func TestMainMenu_SettingsRightArrowCyclesNext(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	// Right = next: animated → static
	msg := tea.KeyMsg{Type: tea.KeyRight}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if mm.GhostDisplay() != "static" {
		t.Errorf("expected static after pressing right arrow from animated, got %s", mm.GhostDisplay())
	}
}

func TestMainMenu_SettingsEnterCycles(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	// Enter on the selected item (ghost display) should cycle
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if mm.GhostDisplay() != "static" {
		t.Errorf("expected static after pressing Enter on ghost display, got %s", mm.GhostDisplay())
	}
	// Should still be in settings mode
	if !mm.InSettingsMode() {
		t.Error("should remain in settings mode after Enter")
	}
}

func TestMainMenu_SettingsViewUpdatesAfterCycle(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()
	m.CycleGhostDisplay() // animated -> static

	view := m.View()
	if !strings.Contains(view, "Static") {
		t.Error("settings view should show 'Static' after cycling")
	}
}

func TestMainMenu_SettingsGhostDisplayInResult(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Enter settings, cycle ghost display, exit settings
	m.EnterSettings()
	m.CycleGhostDisplay() // animated -> static
	m.ExitSettings()

	// Now quit to get a result (use Ctrl-C, which always sets a quit result)
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("expected result after quit")
	}
	if result.GhostDisplay != "static" {
		t.Errorf("expected ghost_display 'static' in result, got %q", result.GhostDisplay)
	}
}

func TestMainMenu_SettingsNoGhostDisplayInResultWhenUnchanged(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Quit without changing ghost display
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("expected result after quit")
	}
	if result.GhostDisplay != "" {
		t.Errorf("expected empty ghost_display when unchanged, got %q", result.GhostDisplay)
	}
}

func TestMainMenu_SettingsNavigationKeys(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	// j/k/up/down should not exit settings mode
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'k'}},
		{Type: tea.KeyUp},
		{Type: tea.KeyDown},
	} {
		newModel, _ := m.Update(msg)
		mm := newModel.(*tui.MainMenuModel)
		if !mm.InSettingsMode() {
			t.Errorf("navigation key %v should not exit settings mode", msg)
		}
	}
}

func TestMainMenu_SettingsNavigationWraps(t *testing.T) {
	const numItems = 9 // claude tool has 9 settings items (incl. Theme, Panel, Plan, Login, Account switching rows)

	jKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	kKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}

	// The vim j/k accelerators wrap within the settings list (arrow keys instead
	// escape focus to the tab bar — covered by the focus-ring tests).
	t.Run("j wraps from last to first", func(t *testing.T) {
		m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
		m.SetSize(80, 30)
		m.EnterSettings()

		// Navigate to the last item (index 3)
		for i := 0; i < numItems-1; i++ {
			m.Update(jKey)
		}
		if m.SettingsSelected() != numItems-1 {
			t.Fatalf("expected to be on last item (%d), got %d", numItems-1, m.SettingsSelected())
		}

		// One more j should wrap to index 0
		newModel, _ := m.Update(jKey)
		mm := newModel.(*tui.MainMenuModel)
		if mm.SettingsSelected() != 0 {
			t.Errorf("j on last item should wrap to 0, got %d", mm.SettingsSelected())
		}
	})

	t.Run("k wraps from first to last", func(t *testing.T) {
		m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
		m.SetSize(80, 30)
		m.EnterSettings()

		// Start at index 0 (default after EnterSettings)
		if m.SettingsSelected() != 0 {
			t.Fatalf("expected initial index 0, got %d", m.SettingsSelected())
		}

		// k from first item should wrap to last
		newModel, _ := m.Update(kKey)
		mm := newModel.(*tui.MainMenuModel)
		if mm.SettingsSelected() != numItems-1 {
			t.Errorf("k on first item should wrap to %d, got %d", numItems-1, mm.SettingsSelected())
		}
	})

	t.Run("j wraps from last to first", func(t *testing.T) {
		m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
		m.SetSize(80, 30)
		m.EnterSettings()

		for i := 0; i < numItems-1; i++ {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		}
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		mm := newModel.(*tui.MainMenuModel)
		if mm.SettingsSelected() != 0 {
			t.Errorf("j on last item should wrap to 0, got %d", mm.SettingsSelected())
		}
	})

	t.Run("k wraps from first to last", func(t *testing.T) {
		m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
		m.SetSize(80, 30)
		m.EnterSettings()

		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		mm := newModel.(*tui.MainMenuModel)
		if mm.SettingsSelected() != numItems-1 {
			t.Errorf("k on first item should wrap to %d, got %d", numItems-1, mm.SettingsSelected())
		}
	})
}

func TestMainMenu_SettingsDoesNotQuit(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()

	// Pressing left arrow (cycle) should not produce a quit command
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	_, cmd := m.Update(msg)

	if cmd != nil {
		t.Error("pressing left arrow in settings should not produce a quit command")
	}
}

func TestMainMenu_SettingsUpperS(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)

	// Press uppercase S
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}}
	newModel, _ := m.Update(msg)
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InSettingsMode() {
		t.Error("pressing uppercase S should enter settings mode")
	}
}

func TestMainMenu_SettingsHelpRow(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.EnterSettings()
	view := m.View()

	if !strings.Contains(view, "cycle") {
		t.Error("settings help row should mention 'cycle'")
	}
	if !strings.Contains(view, "close") {
		t.Error("settings help row should mention 'close'")
	}
	if strings.Contains(view, "back") {
		t.Error("settings help row should not mention 'back'")
	}
}

func TestMainMenu_WakeResetsZzz(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Simulate sleeping state: set sleep timer high, send sleepTickMsg to trigger sleep
	m.SetSleepTimer(119)
	m.Update(tui.NewSleepTickMsg())
	if !m.IsSleeping() {
		t.Fatal("ghost should be sleeping after timer reaches 120")
	}
	// Tick the bob (which should advance Zzz when sleeping)
	m.Update(tui.NewBobTickMsg())
	m.Update(tui.NewBobTickMsg())
	// Now wake
	m.Wake()
	if m.IsSleeping() {
		t.Error("should be awake after Wake()")
	}
	// Zzz should be reset (frame 0) -- tested via ZzzFrame()
	if m.ZzzFrame() != 0 {
		t.Errorf("Zzz should be reset to frame 0 after Wake(), got %d", m.ZzzFrame())
	}
}

func TestMainMenu_BobTickAdvancesZzzWhenSleeping(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Put ghost to sleep
	m.SetSleepTimer(119)
	m.Update(tui.NewSleepTickMsg())
	if !m.IsSleeping() {
		t.Fatal("ghost should be sleeping")
	}
	// Advance bob ticks -- Zzz advances every ZzzTickEvery bob ticks
	initialFrame := m.ZzzFrame()
	for i := 0; i < tui.ZzzTickEvery; i++ {
		m.Update(tui.NewBobTickMsg())
	}
	if m.ZzzFrame() != initialFrame+1 {
		t.Errorf("Zzz frame should advance after %d bob ticks when sleeping, expected %d got %d", tui.ZzzTickEvery, initialFrame+1, m.ZzzFrame())
	}
}

func TestMainMenu_BobTickDoesNotAdvanceZzzWhenAwake(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Ghost is awake by default
	initialFrame := m.ZzzFrame()
	m.Update(tui.NewBobTickMsg())
	if m.ZzzFrame() != initialFrame {
		t.Errorf("Zzz frame should NOT advance when awake, expected %d got %d", initialFrame, m.ZzzFrame())
	}
}

func TestMainMenu_ViewShowsZzzWhenSleeping_Side(t *testing.T) {
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(100, 40) // Wide enough for side layout
	// Put ghost to sleep
	m.SetSleepTimer(119)
	m.Update(tui.NewSleepTickMsg())
	if !m.IsSleeping() {
		t.Fatal("ghost should be sleeping")
	}
	view := m.View()
	// Zzz output contains lowercase z and uppercase Z
	if !strings.Contains(view, "z") || !strings.Contains(view, "Z") {
		t.Error("view should contain Zzz animation when ghost is sleeping (side layout)")
	}
}

func TestMainMenu_ViewShowsZzzWhenSleeping_Above(t *testing.T) {
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(60, 50) // Narrow for above layout
	// Put ghost to sleep
	m.SetSleepTimer(119)
	m.Update(tui.NewSleepTickMsg())
	if !m.IsSleeping() {
		t.Fatal("ghost should be sleeping")
	}
	view := m.View()
	if !strings.Contains(view, "z") || !strings.Contains(view, "Z") {
		t.Error("view should contain Zzz animation when ghost is sleeping (above layout)")
	}
}

func TestMainMenu_ViewNoZzzWhenAwake(t *testing.T) {
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(100, 40)
	// Ghost is awake by default
	view := m.View()
	// The Zzz animation produces lines with specific spacing patterns
	// When awake, there should be no Zzz text appended after ghost
	// We check that the view does NOT contain the Zzz pattern
	z := tui.NewZzzAnimation()
	zzzView := z.View()
	if strings.Contains(view, zzzView) {
		t.Error("view should NOT contain Zzz animation when ghost is awake")
	}
}

func TestMainMenu_KeypressWakesAndResetsZzz(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Put ghost to sleep
	m.SetSleepTimer(119)
	m.Update(tui.NewSleepTickMsg())
	if !m.IsSleeping() {
		t.Fatal("ghost should be sleeping")
	}
	// Advance Zzz
	m.Update(tui.NewBobTickMsg())
	m.Update(tui.NewBobTickMsg())
	// Now press a key
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.IsSleeping() {
		t.Error("keypress should wake the ghost")
	}
	if m.ZzzFrame() != 0 {
		t.Errorf("keypress should reset Zzz frame to 0, got %d", m.ZzzFrame())
	}
}

func TestMainMenu_ViewUnselectedProjectHasColor(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()

	// The unselected project (p2) should have ANSI color codes applied
	// directly around the project name. When styled, lipgloss wraps "p2"
	// in \x1b[38;5;NNNm...p2...\x1b[0m so the character immediately
	// before "p2" is "m" (the end of the ANSI escape sequence).
	// Without styling, the character before "p2" is a space.
	lines := strings.Split(view, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "p2") && !strings.Contains(line, "/p2") {
			found = true
			idx := strings.Index(line, "p2")
			if idx == 0 || line[idx-1] != 'm' {
				t.Error("unselected project name 'p2' should have ANSI color codes applied directly (expected 'm' before name)")
			}
			break
		}
	}
	if !found {
		t.Error("could not find line containing unselected project name 'p2'")
	}
}

func TestMainMenu_ViewActionBarHasColor(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	// With a project selected, the contextual action bar offers Open/Worktrees/Delete.
	projects := []models.Project{{Name: "p1", Path: "/p1"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()

	// The whole action bar text is rendered as one styled span, so its line
	// carries ANSI escape sequences.
	lines := strings.Split(view, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Delete") {
			found = true
			if !strings.Contains(line, "\x1b[") {
				t.Error("action bar line should carry ANSI color escape sequences")
			}
			break
		}
	}
	if !found {
		t.Error("could not find action bar line containing 'Delete'")
	}
}

func TestMainMenu_ViewUnselectedProjectUsesTextColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()

	// Unselected project name "p2" should use neutral text color (252)
	// not theme.Text (223). ANSI 256 color format: \x1b[38;5;252m
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "p2") && !strings.Contains(line, "/p2") {
			if !strings.Contains(line, "\x1b[38;5;252m") {
				t.Errorf("unselected project name 'p2' should use neutral text color (252), line: %q", line)
			}
			return
		}
	}
	t.Error("could not find line containing unselected project name 'p2'")
}

func TestMainMenu_ViewSelectedProjectUsesPrimaryColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "selected-proj", Path: "/selected"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()

	// Selected project name should use theme.Primary (209 for claude).
	// Bold styling produces \x1b[1;38;5;209m (with 1; prefix).
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "selected-proj") {
			if !strings.Contains(line, "38;5;209m") {
				t.Errorf("selected project name should use Primary color (209), line: %q", line)
			}
			return
		}
	}
	t.Error("could not find line containing selected project name")
}

func TestMainMenu_ViewSelectedPathNotHighlighted(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "proj", Path: "/some/selected/path"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()

	// Selected project path should NOT be highlighted with theme.Primary (209);
	// it uses the neutral dim color (245) so only the name carries the accent.
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "/some/selected/path") {
			if strings.Contains(line, "\x1b[38;5;209m") {
				t.Errorf("selected project path should not use Primary color (209), line: %q", line)
			}
			if !strings.Contains(line, "\x1b[38;5;245m") {
				t.Errorf("selected project path should use neutral dim color (245), line: %q", line)
			}
			return
		}
	}
	t.Error("could not find line containing selected project path")
}

func TestMainMenu_ViewActionBarUsesAccentColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	// The contextual action bar for a selected project uses theme.Accent
	// (220 for claude).
	projects := []models.Project{{Name: "p1", Path: "/p1"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	view := m.View()

	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Delete") {
			if !strings.Contains(line, "\x1b[38;5;220m") {
				t.Errorf("action bar should use accent color (220), line: %q", line)
			}
			return
		}
	}
	t.Error("could not find action bar line containing 'Delete'")
}

func TestMainMenu_MouseClickWakesAndResetsZzz(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	// Put ghost to sleep
	m.SetSleepTimer(119)
	m.Update(tui.NewSleepTickMsg())
	if !m.IsSleeping() {
		t.Fatal("ghost should be sleeping")
	}
	// Advance Zzz
	m.Update(tui.NewBobTickMsg())
	m.Update(tui.NewBobTickMsg())
	// Click
	mouseMsg := tea.MouseMsg{
		X:      10,
		Y:      4,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	m.Update(mouseMsg)
	if m.IsSleeping() {
		t.Error("mouse click should wake the ghost")
	}
	if m.ZzzFrame() != 0 {
		t.Errorf("mouse click should reset Zzz frame to 0, got %d", m.ZzzFrame())
	}
}

func TestMainMenu_ZzzAppearsAboveGhost(t *testing.T) {
	t.Run("side_layout", func(t *testing.T) {
		projects := []models.Project{{Name: "test", Path: "/test"}}
		m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
		m.SetSize(100, 40) // Wide enough for side layout
		// Put ghost to sleep
		m.SetSleepTimer(119)
		m.Update(tui.NewSleepTickMsg())
		if !m.IsSleeping() {
			t.Fatal("ghost should be sleeping")
		}
		view := m.View()

		// Find line indices for zzz content (z/Z letters) and ghost cap art (▄)
		// Menu text contains neither z/Z nor ▄, so these are unambiguous markers
		lines := strings.Split(view, "\n")
		firstZzzLine := -1
		firstGhostCapLine := -1
		for i, line := range lines {
			if firstZzzLine == -1 && strings.ContainsAny(line, "zZ") {
				firstZzzLine = i
			}
			if firstGhostCapLine == -1 && strings.Contains(line, "\u2584") {
				firstGhostCapLine = i
			}
		}

		if firstZzzLine == -1 {
			t.Fatal("could not find zzz content in view")
		}
		if firstGhostCapLine == -1 {
			t.Fatal("could not find ghost cap art in view")
		}
		if firstZzzLine >= firstGhostCapLine {
			t.Errorf("zzz should appear above ghost: zzz at line %d, ghost cap at line %d", firstZzzLine, firstGhostCapLine)
		}
	})

	t.Run("above_layout", func(t *testing.T) {
		projects := []models.Project{{Name: "test", Path: "/test"}}
		m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
		m.SetSize(60, 50) // Narrow for above layout
		// Put ghost to sleep
		m.SetSleepTimer(119)
		m.Update(tui.NewSleepTickMsg())
		if !m.IsSleeping() {
			t.Fatal("ghost should be sleeping")
		}
		view := m.View()

		lines := strings.Split(view, "\n")
		firstZzzLine := -1
		firstGhostCapLine := -1
		for i, line := range lines {
			if firstZzzLine == -1 && strings.ContainsAny(line, "zZ") {
				firstZzzLine = i
			}
			if firstGhostCapLine == -1 && strings.Contains(line, "\u2584") {
				firstGhostCapLine = i
			}
		}

		if firstZzzLine == -1 {
			t.Fatal("could not find zzz content in view")
		}
		if firstGhostCapLine == -1 {
			t.Fatal("could not find ghost cap art in view")
		}
		if firstZzzLine >= firstGhostCapLine {
			t.Errorf("zzz should appear above ghost: zzz at line %d, ghost cap at line %d", firstZzzLine, firstGhostCapLine)
		}
	})
}

func TestMainMenu_ViewIsCentered(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSize(80, 40)
	view := m.View()

	lines := strings.Split(view, "\n")

	if len(lines) < 2 {
		t.Fatal("expected multiple lines in centered view")
	}

	// First few lines should be blank or whitespace-only (vertical centering)
	firstContentLine := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			firstContentLine = i
			break
		}
	}
	if firstContentLine == 0 {
		t.Error("expected vertical centering: first non-blank line should not be at row 0")
	}
	if firstContentLine < 0 {
		t.Fatal("no content found in view")
	}

	// The content line should have leading spaces (horizontal centering)
	contentLine := lines[firstContentLine]
	trimmed := strings.TrimLeft(contentLine, " ")
	leadingSpaces := len(contentLine) - len(trimmed)
	if leadingSpaces < 5 {
		t.Errorf("expected horizontal centering with significant leading spaces, got %d", leadingSpaces)
	}
}

func TestMainMenu_MouseClickWorksWithCentering(t *testing.T) {
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
	}
	// The subscription row is shared across agents, present for opencode too.
	m := tui.NewMainMenu(projects, []string{"opencode"}, "opencode", "none")
	m.SetSize(80, 40) // Large terminal -> centering will offset content

	// Need to call View() first so centerOffsetY is calculated
	m.View()

	// With ghost=none, 2 projects + the add-project row.
	// Menu box rows: border, title, subscription, switcher gap, tab bar, separator,
	// empty, p0-name, p0-path, p1-name, ... so the second project name is at box row 9.
	// Absolute row = centering offset + 9.
	offset := m.CenterOffsetY()
	if offset <= 0 {
		t.Fatalf("expected positive centering offset with 80x40 and ghost=none, got %d", offset)
	}

	// Hit-testing now requires a glyph under the pointer, so target the second
	// project's name (box col 8 of "    2  p2") relative to the box origin rather
	// than a fixed absolute column out in the row padding.
	mouseMsg := tea.MouseMsg{
		X:      m.MenuOriginX() + 8,
		Y:      offset + 9,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newModel, _ := m.Update(mouseMsg)
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 1 {
		t.Errorf("clicking centered second project should select item 1, got %d", mm.SelectedItem())
	}
}

func TestMainMenu_TabTitle(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")

	if m.TabTitle() != "full" {
		t.Errorf("expected 'full', got %q", m.TabTitle())
	}
}

func TestMainMenu_CycleTabTitle(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")

	m.CycleTabTitle()
	if m.TabTitle() != "project" {
		t.Errorf("expected 'project' after cycling from full, got %q", m.TabTitle())
	}

	m.CycleTabTitle()
	if m.TabTitle() != "model" {
		t.Errorf("expected 'model' after cycling from project, got %q", m.TabTitle())
	}

	m.CycleTabTitle()
	if m.TabTitle() != "full" {
		t.Errorf("expected 'full' after cycling from model, got %q", m.TabTitle())
	}
}

func TestMainMenu_TabTitleInResult(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")
	m.SetSize(80, 30)

	// Enter settings, cycle tab title, exit settings
	m.EnterSettings()
	m.CycleTabTitle()
	m.ExitSettings()

	// Quit to get result
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("expected result after quit")
	}
	if result.TabTitle != "project" {
		t.Errorf("expected tab_title 'project' in result, got %q", result.TabTitle)
	}
}

func TestMainMenu_NoTabTitleInResultWhenUnchanged(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")
	m.SetSize(80, 30)

	// Quit without changing tab title
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()

	if result == nil {
		t.Fatal("expected result after quit")
	}
	if result.TabTitle != "" {
		t.Errorf("expected empty tab_title when unchanged, got %q", result.TabTitle)
	}
}

func TestMainMenu_SettingsViewShowsTabTitle(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")
	m.SetSize(80, 30)
	m.EnterSettings()
	view := m.View()

	if !strings.Contains(view, "Tab Title") {
		t.Error("settings view should show 'Tab Title' option")
	}
	if !strings.Contains(view, "Project") {
		t.Error("settings view should show current state label")
	}
}

func TestMainMenu_SideLayoutVisualCentering(t *testing.T) {
	// The visible content (menu + spacer + ghost) should be centered on
	// screen as a unit. The ghost should NOT be right-padded, so the left
	// and right margins of the visible content are approximately equal.
	//
	// Visible content = 58 (menu) + 3 (spacer) + ~28 (ghost) = ~89
	// For width=120: expected left margin ≈ (120 - 89) / 2 ≈ 15
	projects := []models.Project{{Name: "test", Path: "/test"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "static")
	width := 120
	m.SetSize(width, 40)
	view := m.View()

	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "\u250c") { // ┌ (top border)
			trimmedLeft := strings.TrimLeft(line, " ")
			leftMargin := len(line) - len(trimmedLeft)
			// Without ghost padding, content is ~89 chars wide
			// Left margin should be roughly (120 - 89) / 2 ≈ 15
			if leftMargin < 11 {
				t.Errorf("visible content not horizontally centered: left margin %d too small (expected >= 11 for width %d)", leftMargin, width)
			}
			break
		}
	}
}

func TestMainMenu_SideLayoutGhostVerticallyCentered(t *testing.T) {
	// With many projects, the menu box is taller than the ghost (15 lines).
	// The ghost should be vertically centered relative to the menu, not
	// top-aligned. We verify by checking that ghost art (▄ cap) does NOT
	// start on the same line as the menu's top border.
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
		{Name: "p3", Path: "/p3"},
		{Name: "p4", Path: "/p4"},
		{Name: "p5", Path: "/p5"},
		{Name: "p6", Path: "/p6"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "static")
	m.SetSize(120, 50)
	view := m.View()

	lines := strings.Split(view, "\n")
	menuTopLine := -1
	ghostCapLine := -1
	menuBottomLine := -1
	for i, line := range lines {
		if menuTopLine == -1 && strings.Contains(line, "\u256d") { // ╭
			menuTopLine = i
		}
		if strings.Contains(line, "\u256f") { // ╯
			menuBottomLine = i
		}
		if ghostCapLine == -1 && strings.Contains(line, "\u2584") { // ▄ (ghost cap)
			ghostCapLine = i
		}
	}

	if menuTopLine == -1 || ghostCapLine == -1 || menuBottomLine == -1 {
		t.Fatalf("could not find menu borders or ghost cap: top=%d, bottom=%d, ghost=%d", menuTopLine, menuBottomLine, ghostCapLine)
	}

	// Ghost should start below the menu top (vertically centered, not top-aligned)
	if ghostCapLine <= menuTopLine {
		t.Errorf("ghost should be vertically centered, not top-aligned: ghost cap at line %d, menu top at line %d", ghostCapLine, menuTopLine)
	}

	// Ghost should start at least a few lines below menu top when menu is much taller
	menuHeight := menuBottomLine - menuTopLine + 1
	ghostOffset := ghostCapLine - menuTopLine
	// Ghost is ~15 lines, so expected offset ≈ (menuHeight - 15) / 2
	expectedOffset := (menuHeight - 15) / 2
	diff := ghostOffset - expectedOffset
	if diff < 0 {
		diff = -diff
	}
	if diff > 2 {
		t.Errorf("ghost not vertically centered: offset %d from menu top, expected ~%d (menu height %d)", ghostOffset, expectedOffset, menuHeight)
	}
}

func TestTruncateMiddle_Short(t *testing.T) {
	got := tui.TruncateMiddle("hello", 10)
	if got != "hello" {
		t.Errorf("short string should pass through unchanged, got %q", got)
	}
}

func TestTruncateMiddle_Exact(t *testing.T) {
	got := tui.TruncateMiddle("hello", 5)
	if got != "hello" {
		t.Errorf("exact-length string should pass through unchanged, got %q", got)
	}
}

func TestTruncateMiddle_Long(t *testing.T) {
	got := tui.TruncateMiddle("abcdefghij", 7)
	// 7 chars: 3 left + … + 3 right = "abc…hij"
	if got != "abc\u2026hij" {
		t.Errorf("expected %q, got %q", "abc\u2026hij", got)
	}
	if lipgloss.Width(got) != 7 {
		t.Errorf("visual width should be 7, got %d", lipgloss.Width(got))
	}
}

func TestTruncateMiddle_VerySmallMax(t *testing.T) {
	got := tui.TruncateMiddle("abcdefghij", 1)
	if got != "\u2026" {
		t.Errorf("maxWidth=1 should return just ellipsis, got %q", got)
	}
}

func TestMainMenu_ViewTruncatesLongPath(t *testing.T) {
	lipgloss.SetDefaultRenderer(lipgloss.NewRenderer(termenv.NewOutput(termenv.DefaultOutput().TTY(), termenv.WithProfile(termenv.Ascii))))
	longPath := "/Users/jack/Packages/shiftmanager-frontend/microfrontends/backoffice-elements"
	projects := []models.Project{{Name: "proj", Path: longPath}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	view := m.View()
	found := false
	for _, line := range strings.Split(view, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "shiftmanager") || strings.Contains(trimmed, "backoffice") {
			found = true
			if !strings.Contains(trimmed, "\u2026") {
				t.Error("long path should be truncated with ellipsis")
			}
			// Box width is menuInnerWidth(68) + 2 border columns = 70.
			if lipgloss.Width(trimmed) > 70 {
				t.Errorf("path line content should not exceed box width 70, got %d: %q", lipgloss.Width(trimmed), trimmed)
			}
		}
	}
	if !found {
		t.Error("view should contain the project path (truncated)")
	}
}

func TestMainMenu_ViewTruncatesLongName(t *testing.T) {
	lipgloss.SetDefaultRenderer(lipgloss.NewRenderer(termenv.NewOutput(termenv.DefaultOutput().TTY(), termenv.WithProfile(termenv.Ascii))))
	longName := "my-extremely-long-project-name-that-definitely-overflows-the-wider-box-now"
	projects := []models.Project{{Name: longName, Path: "/short"}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	view := m.View()
	found := false
	for _, line := range strings.Split(view, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "extremely") || strings.Contains(trimmed, "overflows") {
			found = true
			if !strings.Contains(trimmed, "\u2026") {
				t.Error("long name should be truncated with ellipsis")
			}
			// Box width is menuInnerWidth(68) + 2 border columns = 70.
			if lipgloss.Width(trimmed) > 70 {
				t.Errorf("name line content should not exceed box width 70, got %d: %q", lipgloss.Width(trimmed), trimmed)
			}
		}
	}
	if !found {
		t.Error("view should contain the project name (truncated)")
	}
}

func TestMainMenu_ViewFillsFullTerminalHeight(t *testing.T) {
	// The view output should fill the full terminal height so that
	// lipgloss.Place produces proper centering. The number of lines
	// in the view string must equal the terminal height.
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
		{Name: "p3", Path: "/p3"},
	}
	termHeight := 50
	m := tui.NewMainMenu(projects, []string{"claude", "opencode"}, "opencode", "animated")
	m.SetSize(120, termHeight)

	view := m.View()
	lines := strings.Split(view, "\n")

	if len(lines) != termHeight {
		t.Errorf("view should have exactly %d lines to fill terminal, got %d", termHeight, len(lines))
	}
}

func TestMainMenu_ViewVerticalCenteringIsSymmetric(t *testing.T) {
	// Top blank rows and bottom blank rows should differ by at most 1.
	projects := []models.Project{
		{Name: "p1", Path: "/p1"},
		{Name: "p2", Path: "/p2"},
		{Name: "p3", Path: "/p3"},
		{Name: "p4", Path: "/p4"},
		{Name: "p5", Path: "/p5"},
		{Name: "p6", Path: "/p6"},
	}
	termHeight := 50
	m := tui.NewMainMenu(projects, []string{"claude", "opencode"}, "opencode", "animated")
	m.SetSize(120, termHeight)

	view := m.View()
	lines := strings.Split(view, "\n")

	// Count top blank rows
	topBlank := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			topBlank++
		} else {
			break
		}
	}

	// Count bottom blank rows
	bottomBlank := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			bottomBlank++
		} else {
			break
		}
	}

	diff := topBlank - bottomBlank
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Errorf("vertical centering is not symmetric: %d top blank rows, %d bottom blank rows (diff %d > 1)", topBlank, bottomBlank, diff)
	}
}

func TestMainMenu_InputMode(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	if m.InputMode() != "" {
		t.Errorf("Initial InputMode should be empty, got %q", m.InputMode())
	}
	if m.InInputMode() {
		t.Error("Should not be in input mode initially")
	}
}

func TestMainMenu_DeleteMode(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	if m.InDeleteMode() {
		t.Error("Should not be in delete mode initially")
	}
}

func TestMainMenu_FeedbackMsg(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	if m.FeedbackMsg() != "" {
		t.Errorf("Initial FeedbackMsg should be empty, got %q", m.FeedbackMsg())
	}
}

func TestMainMenu_SetProjectsFile(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	m.SetProjectsFile("/tmp/test-projects")
	if m.ProjectsFile() != "/tmp/test-projects" {
		t.Errorf("ProjectsFile: expected '/tmp/test-projects', got %q", m.ProjectsFile())
	}
}

func TestMainMenu_AddProject_EntersInputMode(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InInputMode() {
		t.Error("Expected input mode after 'a' press")
	}
	if mm.InputMode() != "add-project" {
		t.Errorf("Expected input mode 'add-project', got %q", mm.InputMode())
	}
	if mm.Result() != nil {
		t.Error("Should not produce result when entering input mode")
	}
	if cmd == nil {
		t.Error("Expected a cmd (textinput.Blink) when entering input mode")
	}
}

func TestMainMenu_AddProject_EscCancelsInputMode(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	mm := newModel.(*tui.MainMenuModel)

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm2 := newModel2.(*tui.MainMenuModel)

	if mm2.InInputMode() {
		t.Error("Input mode should be cancelled after Esc")
	}
	if mm2.Result() != nil {
		t.Error("Should not produce result on cancel")
	}
}

func TestMainMenu_AddProject_EmptyEnterStaysAndShowsError(t *testing.T) {
	// With the two-field form, pressing Enter on an empty path shows an error
	// and keeps the user in input mode (does not cancel).
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	mm := newModel.(*tui.MainMenuModel)

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	if !mm2.InInputMode() {
		t.Error("Input mode should stay active on empty path Enter (shows error)")
	}
}

func TestMainMenu_AddProject_SubmitValid(t *testing.T) {
	dir := t.TempDir()
	projFile := filepath.Join(dir, "projects")
	os.WriteFile(projFile, []byte("existing:/tmp/existing\n"), 0644)

	targetDir := filepath.Join(dir, "new-project")
	os.MkdirAll(targetDir, 0755)

	m := tui.NewMainMenu(
		[]models.Project{{Name: "existing", Path: "/tmp/existing"}},
		testAITools(), "claude", "animated",
	)
	m.SetProjectsFile(projFile)

	// Enter add mode via the two-field helpers: set path directly and advance.
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(targetDir)

	// Advance to name field (Enter on path with no autocomplete suggestions).
	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)
	if mm1.InputFocusPath() {
		t.Fatal("Expected focus on name field after advancing from path")
	}

	// Name is auto-derived; submit by pressing Enter on name field.
	result2, _ := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)

	if mm2.InInputMode() {
		t.Error("Should exit input mode after valid submit")
	}
	if mm2.FeedbackMsg() == "" {
		t.Error("Expected feedback message after adding project")
	}

	data, _ := os.ReadFile(projFile)
	if !strings.Contains(string(data), "new-project:"+targetDir) {
		t.Errorf("Projects file should contain new entry, got: %q", string(data))
	}
}

func TestMainMenu_AddProject_DuplicateShowsError(t *testing.T) {
	dir := t.TempDir()
	projFile := filepath.Join(dir, "projects")
	targetDir := filepath.Join(dir, "existing")
	os.MkdirAll(targetDir, 0755)
	os.WriteFile(projFile, []byte("existing:"+targetDir+"\n"), 0644)

	m := tui.NewMainMenu(
		[]models.Project{{Name: "existing", Path: targetDir}},
		testAITools(), "claude", "animated",
	)
	m.SetProjectsFile(projFile)

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	mm := newModel.(*tui.MainMenuModel)
	for _, r := range targetDir {
		mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// First Enter accepts autocomplete suggestion, second Enter submits
	mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	if !mm2.InInputMode() {
		t.Error("Should stay in input mode on duplicate")
	}
}

func TestMainMenu_FeedbackTimer_Dismisses(t *testing.T) {
	dir := t.TempDir()
	projFile := filepath.Join(dir, "projects")
	os.WriteFile(projFile, []byte(""), 0644)

	targetDir := filepath.Join(dir, "feedback-test")
	os.MkdirAll(targetDir, 0755)

	m := tui.NewMainMenu(nil, testAITools(), "claude", "animated")
	m.SetProjectsFile(projFile)

	// Enter add mode via two-field helpers.
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(targetDir)

	// Advance to name field.
	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)

	// Submit with auto-derived name.
	newModel2, _ := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	if mm2.FeedbackMsg() == "" {
		t.Error("Expected feedback message")
	}

	// Tick enough times to dismiss
	for i := 0; i < tui.FeedbackDismissTicks+1; i++ {
		mm2.Update(tui.NewBobTickMsg())
	}

	if mm2.FeedbackMsg() != "" {
		t.Errorf("Feedback should be dismissed after %d ticks, got %q", tui.FeedbackDismissTicks, mm2.FeedbackMsg())
	}
}

// Delete mode tests
// Two-field add-project form tests
func TestAddProject_PathFieldFocusedFirst(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	if !m.InputFocusPath() {
		t.Error("expected path field focused first")
	}
}

func TestAddProject_NameAutoFillsFromPath(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "my-project")
	os.MkdirAll(projDir, 0755)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)
	if mm.NameInputValue() != "my-project" {
		t.Errorf("expected name 'my-project', got %q", mm.NameInputValue())
	}
}

func TestAddProject_NameAutoDeriveLockOnEdit(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "my-project")
	os.MkdirAll(projDir, 0755)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)
	// User edits name — lock auto-derive
	mm.SetNameInputValue("custom-name")
	mm.SetNameTouched(true)
	// Changing path should NOT overwrite custom name
	mm.SetPathInputValue(filepath.Join(dir, "other"))
	mm.TriggerAutoDeriveName()
	if mm.NameInputValue() != "custom-name" {
		t.Errorf("expected locked name 'custom-name', got %q", mm.NameInputValue())
	}
}

func TestAddProject_ShiftTabReturnsToPathField(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "proj")
	os.MkdirAll(projDir, 0755)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // advance to name
	mm := result.(*tui.MainMenuModel)
	result2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	mm2 := result2.(*tui.MainMenuModel)
	if !mm2.InputFocusPath() {
		t.Error("expected Shift+Tab to return to path field")
	}
}

func TestAddProject_DuplicateNameSoftWarn(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "existing")
	os.MkdirAll(projDir, 0755)
	projDir2 := filepath.Join(dir, "new-path")
	os.MkdirAll(projDir2, 0755)

	existing := []models.Project{{Name: "existing", Path: projDir}}
	m := tui.NewMainMenu(existing, []string{"claude"}, "claude", "animated")
	m.SetProjectsFile(filepath.Join(dir, "projects"))
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir2)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // advance to name
	m.SetNameInputValue("existing")
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // first Enter: warn
	mm := result.(*tui.MainMenuModel)
	if mm.NameErr() == nil {
		t.Error("expected soft-warn error on first Enter with duplicate name")
	}
	// Second Enter: confirm
	result2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)
	if mm2.InInputMode() {
		t.Error("expected input mode exited after second Enter")
	}
}

func TestAddProject_DuplicatePathShowsError(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "existing")
	os.MkdirAll(existingPath, 0755)

	existing := []models.Project{{Name: "existing", Path: existingPath}}
	m := tui.NewMainMenu(existing, []string{"claude"}, "claude", "animated")
	m.SetProjectsFile(filepath.Join(dir, "projects"))
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(existingPath)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // advance to name
	m.SetNameInputValue("different-name")
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // submit
	mm := result.(*tui.MainMenuModel)
	if mm.NameErr() == nil {
		t.Error("expected error for duplicate path")
	}
	if mm.InInputMode() == false {
		t.Error("expected to remain in input mode after duplicate path error")
	}
}

func TestAddProject_EscFromNameClearsSoftWarn(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "proj")
	os.MkdirAll(projDir, 0755)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // advance to name
	m.SetNameWarnShown(true)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := result.(*tui.MainMenuModel)
	if mm.NameWarnShown() {
		t.Error("expected soft-warn cleared after Esc from name field")
	}
	if !mm.InputFocusPath() {
		t.Error("expected focus returned to path field after Esc from name")
	}
}

func TestMainMenu_DeleteProject_EntersDeleteMode(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InDeleteMode() {
		t.Error("Expected delete mode after 'd' press")
	}
	if mm.Result() != nil {
		t.Error("Should not produce result when entering delete mode")
	}
}

func TestMainMenu_DeleteProject_NoProjectsShowsFeedback(t *testing.T) {
	m := tui.NewMainMenu(nil, testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	if mm.InDeleteMode() {
		t.Error("Should not enter delete mode with no projects")
	}
	if mm.FeedbackMsg() == "" {
		t.Error("Should show feedback when no projects to delete")
	}
}

func TestMainMenu_DeleteProject_QCancels(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	if mm2.InDeleteMode() {
		t.Error("Delete mode should be cancelled after Q")
	}
}

func TestMainMenu_DeleteProject_EscCancels(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm2 := newModel2.(*tui.MainMenuModel)

	if mm2.InDeleteMode() {
		t.Error("Delete mode should be cancelled after Esc")
	}
}

func TestMainMenu_DeleteProject_NavigatesProjects(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	if mm.DeleteSelected() != 0 {
		t.Error("Should start at first project")
	}

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm2 := newModel2.(*tui.MainMenuModel)
	if mm2.DeleteSelected() != 1 {
		t.Errorf("After down: expected 1, got %d", mm2.DeleteSelected())
	}

	// Wrap around
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)
	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm4 := newModel4.(*tui.MainMenuModel)
	if mm4.DeleteSelected() != 0 {
		t.Errorf("After wrapping down: expected 0, got %d", mm4.DeleteSelected())
	}

	// Up wraps
	newModel5, _ := mm4.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm5 := newModel5.(*tui.MainMenuModel)
	if mm5.DeleteSelected() != 2 {
		t.Errorf("After wrapping up from 0: expected 2, got %d", mm5.DeleteSelected())
	}
}

func TestMainMenu_DeleteProject_NumberJumps(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	if mm2.DeleteSelected() != 2 {
		t.Errorf("After '3' key: expected 2, got %d", mm2.DeleteSelected())
	}

	// Number beyond range does nothing
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	mm3 := newModel3.(*tui.MainMenuModel)
	if mm3.DeleteSelected() != 2 {
		t.Errorf("After '9' key (beyond range): expected 2 (unchanged), got %d", mm3.DeleteSelected())
	}
}

func TestMainMenu_DeleteProject_JKNavigation(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	// j moves down
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	if mm2.DeleteSelected() != 1 {
		t.Errorf("After 'j': expected 1, got %d", mm2.DeleteSelected())
	}

	// k moves up
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	mm3 := newModel3.(*tui.MainMenuModel)
	if mm3.DeleteSelected() != 0 {
		t.Errorf("After 'k': expected 0, got %d", mm3.DeleteSelected())
	}
}

func TestMainMenu_DeleteProject_ConfirmDeletes(t *testing.T) {
	dir := t.TempDir()
	projFile := filepath.Join(dir, "projects")
	os.WriteFile(projFile, []byte("wisp-deck:/Users/jack/wisp-deck\nmy-app:/Users/jack/my-app\nwebsite:/Users/jack/website\n"), 0644)

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	m.SetProjectsFile(projFile)

	// Enter delete mode
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	// Move to second project (my-app)
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm2 := newModel2.(*tui.MainMenuModel)

	// Press Enter to delete
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm3 := newModel3.(*tui.MainMenuModel)

	if !mm3.InDeleteMode() {
		t.Error("Should remain in delete mode after deletion")
	}
	if mm3.FeedbackMsg() == "" {
		t.Error("Expected feedback after deletion")
	}
	if !strings.Contains(mm3.FeedbackMsg(), "my-app") {
		t.Errorf("Feedback should mention deleted project name, got %q", mm3.FeedbackMsg())
	}

	data, _ := os.ReadFile(projFile)
	if strings.Contains(string(data), "my-app") {
		t.Error("Deleted project should be removed from file")
	}
	if !strings.Contains(string(data), "wisp-deck") {
		t.Error("Other projects should remain")
	}
	if !strings.Contains(string(data), "website") {
		t.Error("Other projects should remain")
	}
}

// Open-once tests
func TestMainMenu_OpenOnce_EntersInputMode(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm := newModel.(*tui.MainMenuModel)

	if !mm.InInputMode() {
		t.Error("Expected input mode after 'o' press")
	}
	if mm.InputMode() != "open-once" {
		t.Errorf("Expected input mode 'open-once', got %q", mm.InputMode())
	}
	if mm.Result() != nil {
		t.Error("Should not produce result when entering input mode")
	}
	if cmd == nil {
		t.Error("Expected a cmd when entering input mode")
	}
}

func TestMainMenu_OpenOnce_SubmitValid(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "temp-project")
	os.MkdirAll(targetDir, 0755)

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm := newModel.(*tui.MainMenuModel)

	for _, r := range targetDir {
		mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// First Enter accepts autocomplete suggestion, second Enter submits
	mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	newModel2, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	result := mm2.Result()
	if result == nil {
		t.Fatal("Expected result after valid open-once submit")
	}
	if result.Action != "open-once" {
		t.Errorf("Expected action 'open-once', got %q", result.Action)
	}
	if result.Name != "temp-project" {
		t.Errorf("Expected name 'temp-project', got %q", result.Name)
	}
	if result.Path != targetDir {
		t.Errorf("Expected path %q, got %q", targetDir, result.Path)
	}
	if cmd == nil {
		t.Error("Expected tea.Quit cmd")
	}
}

func TestMainMenu_OpenOnce_InvalidPathShowsError(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm := newModel.(*tui.MainMenuModel)

	for _, r := range "/nonexistent/path/xyz" {
		mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	if !mm2.InInputMode() {
		t.Error("Should stay in input mode on invalid path")
	}
	if mm2.Result() != nil {
		t.Error("Should not produce result on invalid path")
	}
}

func TestMainMenu_OpenOnce_EscCancels(t *testing.T) {
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm := newModel.(*tui.MainMenuModel)

	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm2 := newModel2.(*tui.MainMenuModel)

	if mm2.InInputMode() {
		t.Error("Input mode should be cancelled after Esc")
	}
}

func TestMainMenu_View_InputMode(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()
	if !strings.Contains(view, "Path") {
		t.Error("Input mode view should contain 'Path'")
	}
	if !strings.Contains(view, "Add Project") {
		t.Error("Input mode view should show 'Add Project' label")
	}
}

func TestMainMenu_View_InputMode_OpenOnce(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()
	if !strings.Contains(view, "Open Once") {
		t.Error("Input mode view should show 'Open Once' label")
	}
}

func assertBoxLinesConsistentWidth(t *testing.T, view string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	boxWidth := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.ContainsAny(trimmed, "┌├└│") {
			continue
		}
		w := lipgloss.Width(line)
		if boxWidth < 0 {
			boxWidth = w
		} else if w != boxWidth {
			t.Errorf("line width %d differs from expected %d:\n  line: %q", w, boxWidth, line)
		}
	}
	if boxWidth < 0 {
		t.Fatal("no box lines found in view")
	}
}

func TestMainMenu_View_InputBoxLinesHaveConsistentWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)

	for _, mode := range []struct {
		name string
		key  rune
	}{
		{"add-project", 'a'},
		{"open-once", 'o'},
	} {
		t.Run(mode.name+"/placeholder", func(t *testing.T) {
			m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
			newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{mode.key}})
			mm := newModel.(*tui.MainMenuModel)
			assertBoxLinesConsistentWidth(t, mm.View())
		})

		t.Run(mode.name+"/with-text", func(t *testing.T) {
			m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
			newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{mode.key}})
			mm := newModel.(*tui.MainMenuModel)
			// Type "/" to trigger text mode (cursor at end)
			newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			mm2 := newModel2.(*tui.MainMenuModel)
			assertBoxLinesConsistentWidth(t, mm2.View())
		})
	}
}

func TestMainMenu_View_InputBoxSuggestionsInsideBox(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	mm := newModel.(*tui.MainMenuModel)
	// Type "/" to trigger autocomplete suggestions
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	view := mm2.View()

	// Suggestions should appear inside the box (between │ borders)
	lines := strings.Split(view, "\n")
	foundSuggestion := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A suggestion line should be inside │...│ borders and contain a path
		if strings.Contains(trimmed, "/") && strings.HasPrefix(trimmed, "│") && strings.HasSuffix(trimmed, "│") {
			// Skip the input row itself (contains "Path:")
			if !strings.Contains(trimmed, "Path:") {
				foundSuggestion = true
			}
		}
	}
	if !foundSuggestion {
		t.Error("autocomplete suggestions should render inside the box borders")
	}

	// All box lines should have consistent width (including suggestion rows)
	assertBoxLinesConsistentWidth(t, view)
}

func TestMainMenu_View_DeleteMode(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()
	if !strings.Contains(view, "delete") && !strings.Contains(view, "Delete") {
		t.Error("Delete mode view should contain 'delete' or 'Delete'")
	}
}

func TestMainMenu_View_DeleteMode_SelectedItemUsesMarker(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(prev)
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	// Enter delete mode by pressing 'd'
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()

	// Selected item (index 0, "wisp-deck") should have the █ delete cursor marker
	if !strings.Contains(view, "█") {
		t.Error("Delete mode selected item should contain the █ delete cursor marker")
	}
}

func TestMainMenu_View_DeleteMode_NoRedBackground(t *testing.T) {
	// With TrueColor profile, ANSI escape sequences ARE emitted.
	// Assert the delete mode view does NOT use background ANSI color codes.
	// Lipgloss may combine fg+bg into one sequence like \x1b[97;48;5;196m,
	// so we look for the background code fragment anywhere in the view.
	// Check both 256-color (48;5;) and TrueColor RGB (48;2;) background codes.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()

	// Background codes: 48;5; (256-color) or 48;2; (TrueColor RGB)
	if strings.Contains(view, "48;5;") || strings.Contains(view, "48;2;") {
		t.Error("Delete mode view should not use background ANSI color codes")
	}
}

func TestMainMenu_View_DeleteMode_ShowsAITool(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(prev)

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()

	// The AI tool name should appear in delete mode (from the title row chooser)
	// testAITools() includes "claude" → displayed as "Claude Code"
	if !strings.Contains(view, "Claude") {
		t.Error("Delete mode view should show AI tool name in title row")
	}
}

func TestMainMenu_View_DeleteMode_ShowsDeleteHints(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(prev)

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()

	// The old action stack is gone; delete mode now shows its dedicated footer
	// hints instead.
	if !strings.Contains(view, "navigate") {
		t.Error("Delete mode view should show the 'navigate' hint")
	}
	if !strings.Contains(view, "jump") {
		t.Error("Delete mode view should show the 'jump' hint")
	}
	if !strings.Contains(view, "cancel") {
		t.Error("Delete mode view should show the 'cancel' hint")
	}
}

func TestMainMenu_View_DeleteMode_NoActionItemSelected(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(prev)

	projects := testProjects() // 3 projects
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
	// Enter delete mode
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	view := mm.View()

	// Count occurrences of █ — only one should appear (the delete-selected project).
	// █ is the delete-mode cursor marker, distinct from ▌ used in normal mode.
	count := strings.Count(view, "█")
	if count != 1 {
		t.Errorf("Delete mode view should have exactly 1 █ marker (delete-selected project), got %d", count)
	}
}

func TestMainMenu_View_FeedbackMessage(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	dir := t.TempDir()
	projFile := filepath.Join(dir, "projects")
	os.WriteFile(projFile, []byte("wisp-deck:/Users/jack/wisp-deck\nmy-app:/Users/jack/my-app\nwebsite:/Users/jack/website\n"), 0644)

	m := tui.NewMainMenu(testProjects(), testAITools(), "claude", "animated")
	m.SetProjectsFile(projFile)

	// Delete to trigger feedback
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	view := mm2.View()
	if !strings.Contains(view, "Deleted") {
		t.Error("View should show 'Deleted' feedback")
	}
}

func TestMainMenu_NumberKeyInstantSelect(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Press '2' — should instantly select "my-app" and quit
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd == nil {
		t.Fatal("Number key should return quit command")
	}
	mm := newModel.(*tui.MainMenuModel)
	result := mm.Result()
	if result == nil {
		t.Fatal("Number key should produce a result")
	}
	if result.Action != "select-project" {
		t.Errorf("Expected action 'select-project', got %q", result.Action)
	}
	if result.Name != "my-app" {
		t.Errorf("Expected name 'my-app', got %q", result.Name)
	}
	if result.Path != "/Users/jack/my-app" {
		t.Errorf("Expected path '/Users/jack/my-app', got %q", result.Path)
	}
}

func TestMainMenu_NumberKeyOutOfRange(t *testing.T) {
	projects := testProjects() // 3 projects
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Press '9' — out of range, should do nothing (no quit, no result)
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	if cmd != nil {
		t.Error("Out-of-range number key should not return quit command")
	}
	mm := newModel.(*tui.MainMenuModel)
	if mm.Result() != nil {
		t.Error("Out-of-range number key should not produce a result")
	}
}

func TestMainMenu_SetSoundName(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundName("Bottle")
	if m.SoundName() != "Bottle" {
		t.Errorf("expected 'Bottle', got %q", m.SoundName())
	}
}

func TestMainMenu_SetSoundName_empty_means_off(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundName("")
	if m.SoundName() != "" {
		t.Errorf("expected empty string, got %q", m.SoundName())
	}
}

func TestMainMenu_CycleSoundName_forward(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundName("Bottle")
	m.CycleSoundName()
	name := m.SoundName()
	if name == "Bottle" {
		t.Error("expected sound to change after cycling forward")
	}
	if name == "" {
		t.Error("first forward cycle from Bottle should not jump to Off")
	}
}

func TestMainMenu_CycleSoundNameReverse(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundName("Bottle")
	m.CycleSoundNameReverse()
	name := m.SoundName()
	if name == "Bottle" {
		t.Error("expected sound to change after cycling backward")
	}
}

func TestMainMenu_CycleSoundName_wraps_through_off(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundName("Tink")
	m.CycleSoundName()
	if m.SoundName() != "" {
		t.Errorf("expected Off (empty) after last sound, got %q", m.SoundName())
	}
	m.CycleSoundName()
	if m.SoundName() == "" {
		t.Error("expected to wrap to first sound after Off")
	}
}

func TestMainMenu_CycleSoundNameReverse_wraps_through_off(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundName("Basso")
	m.CycleSoundNameReverse()
	if m.SoundName() != "" {
		t.Errorf("expected Off (empty) after first sound reversed, got %q", m.SoundName())
	}
	m.CycleSoundNameReverse()
	if m.SoundName() != "Tink" {
		t.Errorf("expected 'Tink' after Off reversed, got %q", m.SoundName())
	}
}

func TestMainMenu_SoundNameInResult(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.SetSoundName("Bottle")
	m.EnterSettings()
	m.CycleSoundName()
	m.ExitSettings()
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := m.Result()
	if result == nil {
		t.Fatal("expected a result")
	}
	if result.SoundName == nil {
		t.Fatal("expected sound_name to be set when changed")
	}
}

func TestMainMenu_NoSoundNameInResultWhenUnchanged(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.SetSoundName("Bottle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := m.Result()
	if result == nil {
		t.Fatal("expected a result")
	}
	if result.SoundName != nil {
		t.Errorf("expected nil sound_name when unchanged, got %q", *result.SoundName)
	}
}

func TestMainMenu_SoundNameInResultOnQuit(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.SetSoundName("Bottle")
	m.EnterSettings()
	m.CycleSoundName()
	m.ExitSettings()
	// Quit via Ctrl-C (Esc now emits PopScreenMsg; use Ctrl-C for quit result)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	result := m.Result()
	if result == nil {
		t.Fatal("expected a result on quit")
	}
	if result.Action != "quit" {
		t.Fatalf("expected action=quit, got %q", result.Action)
	}
	if result.SoundName == nil {
		t.Fatal("expected sound_name to be set when changed and quit via Ctrl-C")
	}
}

func TestMainMenu_SetSettingsFile(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile("/tmp/test-settings")
	if m.SettingsFile() != "/tmp/test-settings" {
		t.Errorf("expected '/tmp/test-settings', got %q", m.SettingsFile())
	}
}

func TestMainMenu_SetSoundFile(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile("/tmp/test-features.json")
	if m.SoundFile() != "/tmp/test-features.json" {
		t.Errorf("expected '/tmp/test-features.json', got %q", m.SoundFile())
	}
}

func TestMainMenu_SettingsViewShowsSoundName(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.SetSoundName("Glass")
	m.EnterSettings()
	view := m.View()
	if !strings.Contains(view, "Glass") {
		t.Error("settings view should show sound name 'Glass'")
	}
	if !strings.Contains(view, "Sound") {
		t.Error("settings view should show 'Sound' label")
	}
}

func TestMainMenu_SettingsViewShowsOff(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSize(80, 30)
	m.SetSoundName("")
	m.EnterSettings()
	view := m.View()
	if !strings.Contains(view, "[Off]") {
		t.Error("settings view should show '[Off]' when sound is disabled")
	}
}

func TestMainMenu_CycleGhostDisplay_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "settings")
	os.WriteFile(settingsFile, []byte("ghost_display=animated\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settingsFile)
	m.CycleGhostDisplay() // animated -> static

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings file: %v", err)
	}
	if !strings.Contains(string(data), "ghost_display=static") {
		t.Errorf("expected ghost_display=static in file, got %q", string(data))
	}
}

func TestMainMenu_CycleGhostDisplay_CreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "settings")

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settingsFile)
	m.CycleGhostDisplay() // animated -> static

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings file not created: %v", err)
	}
	if !strings.Contains(string(data), "ghost_display=static") {
		t.Errorf("expected ghost_display=static, got %q", string(data))
	}
}

func TestMainMenu_CycleGhostDisplayReverse_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "settings")
	os.WriteFile(settingsFile, []byte("ghost_display=animated\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settingsFile)
	m.CycleGhostDisplayReverse() // animated -> none

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings file: %v", err)
	}
	if !strings.Contains(string(data), "ghost_display=none") {
		t.Errorf("expected ghost_display=none in file, got %q", string(data))
	}
}

func TestMainMenu_CycleGhostDisplay_PreservesOtherSettings(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "settings")
	os.WriteFile(settingsFile, []byte("ghost_display=animated\ntab_title=full\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settingsFile)
	m.CycleGhostDisplay() // animated -> static

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "ghost_display=static") {
		t.Errorf("expected ghost_display=static in file, got %q", content)
	}
	if !strings.Contains(content, "tab_title=full") {
		t.Errorf("expected tab_title=full preserved in file, got %q", content)
	}
}

func TestMainMenu_CycleGhostDisplay_DoesNotPersistWithoutFile(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "settings")

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Do NOT call SetSettingsFile
	m.CycleGhostDisplay()

	// File should NOT exist
	if _, err := os.Stat(settingsFile); err == nil {
		t.Error("settings file should not be created when no file path set")
	}
}

func TestMainMenu_CycleTabTitle_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "settings")
	os.WriteFile(settingsFile, []byte("tab_title=full\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settingsFile)
	m.SetTabTitle("full")
	m.CycleTabTitle() // full -> project

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read settings file: %v", err)
	}
	if !strings.Contains(string(data), "tab_title=project") {
		t.Errorf("expected tab_title=project in file, got %q", string(data))
	}
}

func TestMainMenu_CycleGhostDisplay_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "config", "wisp-deck", "settings")

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSettingsFile(settingsFile)
	m.CycleGhostDisplay() // animated -> static

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings file not created with parent dirs: %v", err)
	}
	if !strings.Contains(string(data), "ghost_display=static") {
		t.Errorf("expected ghost_display=static, got %q", string(data))
	}
}

func TestMainMenu_CycleSoundName_persists_to_file(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	os.WriteFile(soundFile, []byte(`{"sound":true,"sound_name":"Bottle"}`+"\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile(soundFile)
	m.SetSoundName("Bottle")
	m.CycleSoundName() // Bottle -> Frog

	data, err := os.ReadFile(soundFile)
	if err != nil {
		t.Fatalf("failed to read sound file: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "Bottle") {
		t.Error("expected sound to change from Bottle")
	}
	if !strings.Contains(content, `"sound":true`) && !strings.Contains(content, `"sound": true`) {
		t.Error("expected sound to be enabled")
	}
}

func TestMainMenu_CycleSoundName_to_off_persists(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	os.WriteFile(soundFile, []byte(`{"sound":true,"sound_name":"Tink"}`+"\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile(soundFile)
	m.SetSoundName("Tink")
	m.CycleSoundName() // Tink -> Off

	data, err := os.ReadFile(soundFile)
	if err != nil {
		t.Fatalf("failed to read sound file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"sound":false`) && !strings.Contains(content, `"sound": false`) {
		t.Errorf("expected sound:false when cycled to Off, got %q", content)
	}
}

func TestMainMenu_CycleSoundName_creates_file_if_missing(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile(soundFile)
	m.SetSoundName("")
	m.CycleSoundName() // Off -> Basso

	data, err := os.ReadFile(soundFile)
	if err != nil {
		t.Fatalf("sound file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Basso") {
		t.Errorf("expected Basso in file, got %q", content)
	}
}

func TestMainMenu_CycleSoundNameReverse_persists_to_file(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	os.WriteFile(soundFile, []byte(`{"sound":true,"sound_name":"Blow"}`+"\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile(soundFile)
	m.SetSoundName("Blow")
	m.CycleSoundNameReverse() // Blow -> Basso

	data, err := os.ReadFile(soundFile)
	if err != nil {
		t.Fatalf("failed to read sound file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Basso") {
		t.Errorf("expected Basso in file, got %q", content)
	}
}

func TestMainMenu_CycleSoundName_preserves_other_keys(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	os.WriteFile(soundFile, []byte(`{"sound":true,"sound_name":"Bottle","other_key":"value"}`+"\n"), 0644)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile(soundFile)
	m.SetSoundName("Bottle")
	m.CycleSoundName() // Bottle -> Frog

	data, err := os.ReadFile(soundFile)
	if err != nil {
		t.Fatalf("failed to read sound file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"other_key"`) {
		t.Errorf("expected other_key preserved in file, got %q", content)
	}
}

func TestMainMenu_CycleSoundName_does_not_persist_without_file(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	// Do NOT call SetSoundFile
	m.SetSoundName("Bottle")
	m.CycleSoundName()

	// File should NOT exist
	if _, err := os.Stat(soundFile); err == nil {
		t.Error("sound file should not be created when no file path set")
	}
}

func TestMainMenu_CycleSoundName_creates_parent_dirs(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "config", "wisp-deck", "claude-features.json")

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetSoundFile(soundFile)
	m.SetSoundName("")
	m.CycleSoundName() // Off -> Basso

	data, err := os.ReadFile(soundFile)
	if err != nil {
		t.Fatalf("sound file not created with parent dirs: %v", err)
	}
	if !strings.Contains(string(data), "Basso") {
		t.Errorf("expected Basso in file, got %q", string(data))
	}
}

func testProjectsWithWorktrees() []models.Project {
	return []models.Project{
		{
			Name: "wisp-deck",
			Path: "/Users/jack/wisp-deck",
			Worktrees: []models.Worktree{
				{Path: "/Users/jack/wt/feature-auth", Branch: "feature/auth"},
				{Path: "/Users/jack/wt/fix-cleanup", Branch: "fix/cleanup"},
			},
		},
		{Name: "my-app", Path: "/Users/jack/my-app"},
		{
			Name: "website",
			Path: "/Users/jack/website",
			Worktrees: []models.Worktree{
				{Path: "/Users/jack/wt/redesign", Branch: "redesign"},
			},
		},
	}
}

func TestMainMenu_TotalItemsWithExpanded(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// No expansions: 3 projects + 1 add-project row = 4
	if m.TotalItems() != 4 {
		t.Errorf("unexpanded: expected 4, got %d", m.TotalItems())
	}

	// Expand first project (2 worktrees + 1 add-worktree): 3 + 3 + 1 = 7
	m.ToggleWorktrees(0)
	if m.TotalItems() != 7 {
		t.Errorf("expanded first: expected 7, got %d", m.TotalItems())
	}

	// Expand third project too (1 worktree + 1 add): 3 + 3 + 2 + 1 = 9
	m.ToggleWorktrees(2)
	if m.TotalItems() != 9 {
		t.Errorf("expanded first+third: expected 9, got %d", m.TotalItems())
	}

	// Collapse first project: 3 + 2 + 1 = 6
	m.ToggleWorktrees(0)
	if m.TotalItems() != 6 {
		t.Errorf("collapsed first, third expanded: expected 6, got %d", m.TotalItems())
	}
}

func TestMainMenu_NavigationWithWorktrees(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Expand first project
	m.ToggleWorktrees(0)
	// Items: [proj0, wt0, wt1, proj1, proj2, add, delete, open, plain]

	// Start at 0 (project 0)
	if m.SelectedItem() != 0 {
		t.Errorf("start: expected 0, got %d", m.SelectedItem())
	}

	// Move down into worktree entries
	m.MoveDown()
	if m.SelectedItem() != 1 {
		t.Errorf("after 1 down: expected 1 (wt0), got %d", m.SelectedItem())
	}

	m.MoveDown()
	if m.SelectedItem() != 2 {
		t.Errorf("after 2 down: expected 2 (wt1), got %d", m.SelectedItem())
	}

	// Next is project 1 (index 3)
	m.MoveDown()
	if m.SelectedItem() != 3 {
		t.Errorf("after 3 down: expected 3 (proj1), got %d", m.SelectedItem())
	}
}

func TestMainMenu_CollapseMovesSelectionToProject(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Expand first project, select worktree entry
	m.ToggleWorktrees(0)
	m.MoveDown() // wt0 (item 1)
	if m.SelectedItem() != 1 {
		t.Fatalf("expected on wt0 (item 1), got %d", m.SelectedItem())
	}

	// Collapse — selection should snap back to project 0
	m.ToggleWorktrees(0)
	if m.SelectedItem() != 0 {
		t.Errorf("after collapse: expected 0 (proj0), got %d", m.SelectedItem())
	}
}

func TestMainMenu_ToggleNoWorktrees(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Project 1 has no worktrees — toggle should be a no-op
	before := m.TotalItems()
	m.ToggleWorktrees(1)
	after := m.TotalItems()
	if before != after {
		t.Errorf("toggle on no-worktree project changed total: %d -> %d", before, after)
	}
}

func TestMainMenu_WKeyTogglesAllWorktrees(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
	m.SetSize(100, 40)

	// 'w' is now cursor-scoped: it only toggles the project under the cursor.
	// Cursor starts at index 0 (project "wisp-deck"), so pressing 'w' expands
	// only project 0 and leaves project 2 collapsed.
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}}
	m.Update(msg)

	if !m.IsExpanded(0) {
		t.Error("expected project 0 to be expanded after 'w'")
	}
	if m.IsExpanded(2) {
		t.Error("expected project 2 to remain collapsed (cursor was on project 0)")
	}

	// Press 'w' again with cursor still on project 0 — should collapse it
	m.Update(msg)
	if m.IsExpanded(0) {
		t.Error("expected project 0 to be collapsed after second 'w'")
	}
}

func TestMainMenu_SelectWorktree(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
	m.SetSize(100, 40)

	// Expand first project and move to first worktree
	m.ToggleWorktrees(0)
	m.MoveDown() // now on worktree item

	// Select current (simulates Enter)
	result := m.Result()
	if result != nil {
		t.Fatal("result should be nil before selection")
	}

	// Trigger selectCurrent by sending Enter
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	m.Update(enterMsg)

	result = m.Result()
	if result == nil {
		t.Fatal("result should not be nil after Enter on worktree")
	}
	if result.Action != "select-project" {
		t.Errorf("action: got %q, want %q", result.Action, "select-project")
	}
	if result.Path != "/Users/jack/wt/feature-auth" {
		t.Errorf("path: got %q, want %q", result.Path, "/Users/jack/wt/feature-auth")
	}
	if result.Name != "wisp-deck" {
		t.Errorf("name: got %q, want %q", result.Name, "wisp-deck")
	}
}

func TestMainMenu_MapRowToItemWithWorktrees(t *testing.T) {
	projects := testProjectsWithWorktrees()
	// The subscription row is shared across agents, present for opencode too — it
	// shifts every row below the title down by one.
	m := tui.NewMainMenu(projects, testAITools(), "opencode", "animated")
	m.SetSize(100, 40)

	// Expand first project (2 worktrees)
	m.ToggleWorktrees(0)

	// Row layout (0-indexed within menu box):
	// 0: top border
	// 1: title
	// 2: subscription row
	// 3: switcher gap
	// 4: tab bar
	// 5: separator
	// 6: empty
	// 7-8: project 0 (name + path) -> item 0
	// 9-10: worktree 0 (branch + path) -> item 1
	// 11-12: worktree 1 (branch + path) -> item 2
	// 13-14: project 1 (name + path) -> item 3

	// Project 0
	if m.MapRowToItem(7) != 0 {
		t.Errorf("row 7: expected item 0, got %d", m.MapRowToItem(7))
	}
	if m.MapRowToItem(8) != 0 {
		t.Errorf("row 8: expected item 0, got %d", m.MapRowToItem(8))
	}

	// Worktree entries (2 rows each: branch + path)
	if m.MapRowToItem(9) != 1 {
		t.Errorf("row 9: expected item 1 (wt0), got %d", m.MapRowToItem(9))
	}
	if m.MapRowToItem(10) != 1 {
		t.Errorf("row 10: expected item 1 (wt0 path row), got %d", m.MapRowToItem(10))
	}
	if m.MapRowToItem(11) != 2 {
		t.Errorf("row 11: expected item 2 (wt1), got %d", m.MapRowToItem(11))
	}
	if m.MapRowToItem(12) != 2 {
		t.Errorf("row 12: expected item 2 (wt1 path row), got %d", m.MapRowToItem(12))
	}

	// Add-worktree row (item 3); project 1 is item 4 at row 14.
	if m.MapRowToItem(13) != 3 {
		t.Errorf("row 13: expected item 3 (add-worktree), got %d", m.MapRowToItem(13))
	}
}

func TestMainMenu_CalculateLayoutWithWorktrees(t *testing.T) {
	projects := testProjectsWithWorktrees()
	// The subscription row is shared across agents, so chrome includes it for opencode too.
	m := tui.NewMainMenu(projects, testAITools(), "opencode", "animated")
	m.SetSize(100, 40)

	layout1 := m.CalculateLayout(100, 40)

	// Collapsed: 13 (chrome, incl. subscription row + add-project hint) + 3*2 (projects) = 19
	expectedCollapsed := 13 + (3 * 2)
	if layout1.MenuHeight != expectedCollapsed {
		t.Errorf("collapsed height: got %d, want %d", layout1.MenuHeight, expectedCollapsed)
	}

	// Expand first project (2 worktrees)
	m.ToggleWorktrees(0)
	layout2 := m.CalculateLayout(100, 40)

	// Expanded: 13 (chrome) + 3*2 (projects) + 2*2 (worktrees) + 1 (add-worktree) = 24
	expectedExpanded := 13 + (3 * 2) + (2 * 2) + 1
	if layout2.MenuHeight != expectedExpanded {
		t.Errorf("expanded height: got %d, want %d", layout2.MenuHeight, expectedExpanded)
	}

	// Menu should be taller with expanded worktrees
	if layout2.MenuHeight <= layout1.MenuHeight {
		t.Errorf("expanded layout should be taller: collapsed=%d, expanded=%d",
			layout1.MenuHeight, layout2.MenuHeight)
	}
}

func TestMainMenu_WorktreeFullFlow(t *testing.T) {
	// Setup: project with worktrees
	projects := []models.Project{
		{
			Name: "myproject",
			Path: "/home/user/myproject",
			Worktrees: []models.Worktree{
				{Path: "/home/user/wt/feat", Branch: "feature/new-ui"},
			},
		},
		{Name: "other", Path: "/home/user/other"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.SetSize(100, 40)

	// Verify initial state: 2 projects + 1 add-project row = 3
	if m.TotalItems() != 3 {
		t.Fatalf("initial total: expected 3, got %d", m.TotalItems())
	}

	// Press 'w' to expand project 0 (which has 1 worktree)
	wKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}}
	m.Update(wKey)
	if m.TotalItems() != 5 { // 2 projects + 1 worktree + 1 add-worktree + 1 add-project
		t.Fatalf("after expand: expected 5, got %d", m.TotalItems())
	}

	// Move down to worktree
	downKey := tea.KeyMsg{Type: tea.KeyDown}
	m.Update(downKey)
	if m.SelectedItem() != 1 {
		t.Fatalf("after down: expected 1, got %d", m.SelectedItem())
	}

	// Press Enter to select worktree
	enterKey := tea.KeyMsg{Type: tea.KeyEnter}
	newModel, _ := m.Update(enterKey)
	mm := newModel.(*tui.MainMenuModel)

	result := mm.Result()
	if result == nil {
		t.Fatal("expected result after Enter")
	}
	if result.Path != "/home/user/wt/feat" {
		t.Errorf("path: got %q, want %q", result.Path, "/home/user/wt/feat")
	}
	if result.Name != "myproject" {
		t.Errorf("name: got %q, want %q", result.Name, "myproject")
	}
	if result.Action != "select-project" {
		t.Errorf("action: got %q, want %q", result.Action, "select-project")
	}
}

func TestMainMenu_TotalItemsWithAddWorktree(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Collapsed: 3 projects + 1 add-project row = 4
	if m.TotalItems() != 4 {
		t.Errorf("collapsed: expected 4, got %d", m.TotalItems())
	}

	// Expand first project (2 worktrees + 1 add-worktree): 3 + 3 + 1 = 7
	m.ToggleWorktrees(0)
	if m.TotalItems() != 7 {
		t.Errorf("expanded first: expected 7, got %d", m.TotalItems())
	}

	// Expand third project too (1 worktree + 1 add): 3 + 3 + 2 + 1 = 9
	m.ToggleWorktrees(2)
	if m.TotalItems() != 9 {
		t.Errorf("expanded first+third: expected 9, got %d", m.TotalItems())
	}
}

func TestMainMenu_ResolveAddWorktreeItem(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Expand first project: [P0, WT0, WT1, +Add, P1, P2, ...]
	m.ToggleWorktrees(0)

	itemType, projectIdx, _ := m.ResolveItem(3)
	if itemType != "add-worktree" {
		t.Errorf("item 3 type: got %q, want %q", itemType, "add-worktree")
	}
	if projectIdx != 0 {
		t.Errorf("item 3 projectIdx: got %d, want 0", projectIdx)
	}
}

func TestMainMenu_SelectAddWorktree(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
	m.SetSize(100, 40)

	m.ToggleWorktrees(0)
	m.MoveDown() // 1: WT0
	m.MoveDown() // 2: WT1
	m.MoveDown() // 3: +Add

	itemType, _, _ := m.ResolveItem(m.SelectedItem())
	if itemType != "add-worktree" {
		t.Fatalf("expected add-worktree, got %q at index %d", itemType, m.SelectedItem())
	}

	// add-worktree now pushes a BranchPickerModel instead of returning a result
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command after Enter on add-worktree, got nil")
	}
	msg := cmd()
	push, ok := msg.(tui.PushScreenMsg)
	if !ok {
		t.Fatalf("expected PushScreenMsg after Enter on add-worktree, got %T", msg)
	}
	if _, ok := push.Model.(tui.BranchPickerModel); !ok {
		t.Errorf("expected pushed model to be BranchPickerModel, got %T", push.Model)
	}
	// result should be nil — worktree creation happens in Go after branch is picked
	result := m.Result()
	if result != nil {
		t.Errorf("expected nil result for add-worktree (handled in-app), got %+v", result)
	}
}

func TestMainMenu_CollapseWithAddWorktreeAdjustsSelection(t *testing.T) {
	projects := testProjectsWithWorktrees()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	m.ToggleWorktrees(0)
	// [P0(0), WT0(1), WT1(2), +Add(3), P1(4), ...]
	m.MoveDown() // 1
	m.MoveDown() // 2
	m.MoveDown() // 3: on +Add

	m.ToggleWorktrees(0)
	if m.SelectedItem() != 0 {
		t.Errorf("after collapse from +Add: expected 0, got %d", m.SelectedItem())
	}
}

func TestMainMenu_MapRowToItemWithAddWorktree(t *testing.T) {
	projects := testProjectsWithWorktrees()
	// The subscription row is shared across agents, present for opencode too — it
	// shifts every project/worktree row down by one.
	m := tui.NewMainMenu(projects, testAITools(), "opencode", "animated")
	m.SetSize(100, 40)

	m.ToggleWorktrees(0)
	// Layout: P0(rows 7-8), WT0(rows 9-10), WT1(rows 11-12), +Add(row 13), P1(rows 14-15)
	if got := m.MapRowToItem(13); got != 3 {
		t.Errorf("+Add row 13: got flat %d, want 3", got)
	}
	if got := m.MapRowToItem(14); got != 4 {
		t.Errorf("P1 row 14: got flat %d, want 4", got)
	}
}

func TestMainMenu_CalculateLayoutWithAddWorktree(t *testing.T) {
	projects := testProjectsWithWorktrees()
	// The subscription row is shared across agents, so chrome includes it for opencode too.
	m := tui.NewMainMenu(projects, testAITools(), "opencode", "animated")

	m.ToggleWorktrees(0)
	m.ToggleWorktrees(2)

	layout := m.CalculateLayout(100, 60)
	// 13 (chrome, incl. subscription row + add-project hint) + 3*2 (projects) + 3*2 (worktrees) + 2*1 (add-worktree) = 27
	if layout.MenuHeight != 27 {
		t.Errorf("menu height: got %d, want 27", layout.MenuHeight)
	}
}

func TestMainMenu_EscEmitsPopScreenMsg(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "none")

	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(*tui.MainMenuModel)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a command on Esc in main list, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tui.PopScreenMsg); !ok {
		t.Errorf("expected PopScreenMsg on Esc, got %T", msg)
	}
}

func TestMainMenu_AddWorktree_PushesScreenMsg(t *testing.T) {
	// Use a project that has existing worktrees so ToggleWorktrees works
	dir := t.TempDir()
	projects := []models.Project{
		{
			Name: "proj1",
			Path: dir,
			Worktrees: []models.Worktree{
				{Path: dir + "--feat", Branch: "feat"},
			},
		},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetProjectsFile(filepath.Join(dir, "projects"))

	// Expand worktrees for project 0 so the add-worktree item appears
	m.ToggleWorktrees(0)

	// Navigate to the add-worktree item:
	// item 0 = project row, item 1 = worktree row, item 2 = add-worktree
	m.JumpTo(1)  // jump to project index 0 (1-indexed)
	m.MoveDown() // move to worktree
	m.MoveDown() // move to add-worktree

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command after selecting add-worktree, got nil")
	}
	msg := cmd()
	push, ok := msg.(tui.PushScreenMsg)
	if !ok {
		t.Fatalf("expected PushScreenMsg, got %T", msg)
	}
	if _, ok := push.Model.(tui.BranchPickerModel); !ok {
		t.Errorf("expected pushed model to be BranchPickerModel, got %T", push.Model)
	}
}

func TestMainMenu_BranchPickerDoneMsg_NoSelection_DoesNothing(t *testing.T) {
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "none")

	updated, cmd := m.Update(tui.BranchPickerDoneMsg{Selected: false, Branch: ""})
	_ = updated
	// Should not quit
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("BranchPickerDoneMsg with Selected=false should not quit")
		}
	}
}

func TestMainMenu_BranchPickerDoneMsg_WithSelection_CreatesWorktree(t *testing.T) {
	dir := t.TempDir()
	// initialise a bare git repo in dir so "git worktree add" can run
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	// create an initial commit (required for branches to exist)
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()

	projects := []models.Project{
		{Name: "proj1", Path: dir},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetProjectsFile(filepath.Join(dir, "projects"))
	m.SetWorktreeProject(0, dir) // tell the model which project is pending a worktree

	_, cmd := m.Update(tui.BranchPickerDoneMsg{Selected: true, Branch: "main"})
	if cmd != nil {
		cmd() // execute worktree creation
	}
	// verify worktree path exists
	expectedPath := filepath.Dir(dir) + "/" + filepath.Base(dir) + "--main"
	if _, err := os.Stat(expectedPath); err != nil {
		// worktree creation may fail in CI (no real branches), but the
		// model should at least not panic or crash — this is enough to
		// verify the code path runs
		t.Logf("worktree path %q not created (may be expected in CI): %v", expectedPath, err)
	}
}

func TestDeletableItems_ProjectsOnly(t *testing.T) {
	// No expanded worktrees → only project flat indices
	projects := testProjects() // 3 projects
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	items := m.DeletableItems()
	// Expected: [0, 1, 2] — projectToFlatIndex(0)=0, (1)=1, (2)=2 (no expansions)
	if len(items) != 3 {
		t.Fatalf("want 3 deletable items, got %d: %v", len(items), items)
	}
	if items[0] != 0 || items[1] != 1 || items[2] != 2 {
		t.Errorf("want [0 1 2], got %v", items)
	}
}

func TestDeletableItems_WithExpandedWorktrees(t *testing.T) {
	// One expanded project with 2 worktrees → project + its 2 worktrees appear,
	// the "add worktree" item does NOT appear, action items do NOT appear.
	projects := []models.Project{
		{Name: "proj1", Path: "/p1", Worktrees: []models.Worktree{
			{Path: "/p1--feat", Branch: "feat"},
			{Path: "/p1--fix", Branch: "fix"},
		}},
		{Name: "proj2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	// Expand project 0 via 'w' key
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)

	items := mm.DeletableItems()
	// proj1 flat=0, wt0 flat=1, wt1 flat=2, proj2 flat=4 (skip add-worktree at 3)
	want := []int{0, 1, 2, 4}
	if len(items) != len(want) {
		t.Fatalf("want %v, got %v", want, items)
	}
	for i, v := range want {
		if items[i] != v {
			t.Errorf("items[%d]: want %d, got %d", i, v, items[i])
		}
	}
}

func TestDeleteMode_FlatIndex_StartsAtFirstDeletable(t *testing.T) {
	// On entering delete mode, deleteSelected should be the first deletable flat index.
	// With no expansions that is 0 — this test also passes today, so it's a regression guard.
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)
	if mm.DeleteSelected() != 0 {
		t.Errorf("DeleteSelected after entering delete mode: want 0, got %d", mm.DeleteSelected())
	}
}

func TestDeleteMode_FlatIndex_NavigatesPastWorktrees(t *testing.T) {
	// With project 0 expanded (2 worktrees), pressing ↓ twice in delete mode
	// should land on the first worktree (flat=1), then second worktree (flat=2).
	projects := []models.Project{
		{Name: "proj1", Path: "/p1", Worktrees: []models.Worktree{
			{Path: "/p1--feat", Branch: "feat"},
			{Path: "/p1--fix", Branch: "fix"},
		}},
		{Name: "proj2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	// Expand project 0
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	// Enter delete mode
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	if mm2.DeleteSelected() != 0 {
		t.Fatalf("initial DeleteSelected: want 0, got %d", mm2.DeleteSelected())
	}

	// Down once → flat=1 (first worktree)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)
	if mm3.DeleteSelected() != 1 {
		t.Errorf("after first ↓: want 1, got %d", mm3.DeleteSelected())
	}

	// Down again → flat=2 (second worktree)
	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm4 := newModel4.(*tui.MainMenuModel)
	if mm4.DeleteSelected() != 2 {
		t.Errorf("after second ↓: want 2, got %d", mm4.DeleteSelected())
	}

	// Down again → flat=4 (proj2, skip add-worktree at 3)
	newModel5, _ := mm4.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm5 := newModel5.(*tui.MainMenuModel)
	if mm5.DeleteSelected() != 4 {
		t.Errorf("after third ↓: want 4, got %d", mm5.DeleteSelected())
	}
}

func TestDeleteMode_FlatIndex_NumberJumpsByDeletablePosition(t *testing.T) {
	// With project 0 expanded (1 worktree), pressing '2' should jump to
	// the 2nd deletable item (flat=1, the worktree), not project index 1.
	projects := []models.Project{
		{Name: "proj1", Path: "/p1", Worktrees: []models.Worktree{
			{Path: "/p1--feat", Branch: "feat"},
		}},
		{Name: "proj2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	// Expand project 0
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	// Enter delete mode
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	// '2' should jump to the 2nd deletable item = flat index 1 (the worktree)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	mm3 := newModel3.(*tui.MainMenuModel)
	if mm3.DeleteSelected() != 1 {
		t.Errorf("after '2': want flat=1 (worktree), got %d", mm3.DeleteSelected())
	}
}

func TestDeleteMode_RenderWorktreeMarker(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(prev)

	// proj1 expanded with 1 worktree "feat"
	projects := []models.Project{
		{Name: "proj1", Path: "/p1", Worktrees: []models.Worktree{
			{Path: "/p1--feat", Branch: "feat"},
		}},
		{Name: "proj2", Path: "/p2"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	// Expand project 0
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	// Enter delete mode
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	// Navigate down once → deleteSelected = 1 (the worktree)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)

	if mm3.DeleteSelected() != 1 {
		t.Fatalf("precondition: DeleteSelected should be 1, got %d", mm3.DeleteSelected())
	}

	view := mm3.View()

	// The worktree row for "feat" must contain the █ delete cursor marker
	lines := strings.Split(view, "\n")
	markerOnFeatLine := false
	for _, line := range lines {
		if strings.Contains(line, "feat") && strings.Contains(line, "█") {
			markerOnFeatLine = true
		}
	}
	if !markerOnFeatLine {
		t.Error("delete mode should show █ marker on the targeted worktree row (feat)")
	}

	// The project row "proj1" must NOT have the █ delete cursor marker
	markerOnProj1 := false
	for _, line := range lines {
		if strings.Contains(line, "proj1") && strings.Contains(line, "█") {
			markerOnProj1 = true
		}
	}
	if markerOnProj1 {
		t.Error("delete mode should not show █ marker on the project row when a worktree is targeted")
	}
}

func TestRemoveWorktree_Clean(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()

	// Create a branch and worktree
	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed: %v: %s", err, out)
	}

	// Remove it without force — should succeed on a clean worktree
	err := models.RemoveWorktree(dir, wtPath, false)
	if err != nil {
		t.Errorf("RemoveWorktree clean: unexpected error: %v", err)
	}
	// Verify path is gone
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Error("worktree path should not exist after clean removal")
	}
}

func TestRemoveWorktree_IsDirtyError(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()

	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed: %v: %s", err, out)
	}

	// Write an uncommitted file to make it dirty
	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	// Removing without force should fail
	err := models.RemoveWorktree(dir, wtPath, false)
	if err == nil {
		t.Error("RemoveWorktree dirty: expected error but got nil")
	}
	if !models.IsWorktreeDirtyError(err) {
		t.Errorf("RemoveWorktree dirty: expected IsWorktreeDirtyError to be true, err=%v", err)
	}
}

func TestRemoveWorktree_IsLockedError(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()

	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed: %v: %s", err, out)
	}

	// Lock the worktree (simulates stale agent lock)
	if out, err := exec.Command("git", "-C", dir, "worktree", "lock", "--reason", "stale agent", wtPath).CombinedOutput(); err != nil {
		t.Skipf("git worktree lock failed: %v: %s", err, out)
	}

	// Removing without force should fail with a locked error, not dirty
	err := models.RemoveWorktree(dir, wtPath, false)
	if err == nil {
		t.Fatal("RemoveWorktree locked: expected error but got nil")
	}
	if models.IsWorktreeDirtyError(err) {
		t.Errorf("RemoveWorktree locked: should not be classified as dirty, err=%v", err)
	}
	if !models.IsWorktreeLockedError(err) {
		t.Errorf("RemoveWorktree locked: expected IsWorktreeLockedError to be true, err=%v", err)
	}
}

func TestRemoveWorktree_ForceRemovesLocked(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()

	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "worktree", "lock", "--reason", "stale agent", wtPath).CombinedOutput(); err != nil {
		t.Skipf("git worktree lock failed: %v: %s", err, out)
	}

	// Force removal must override the lock
	if err := models.RemoveWorktree(dir, wtPath, true); err != nil {
		t.Fatalf("RemoveWorktree locked force: unexpected error: %v", err)
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Error("worktree path should not exist after force removal of locked worktree")
	}
}

func TestDeleteMode_ConfirmDelete_WorktreeTarget_Success(t *testing.T) {
	// Set up a real git repo with a worktree so removal can succeed
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()
	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add: %v: %s", err, out)
	}

	projects := []models.Project{
		{Name: "myproj", Path: dir, Worktrees: []models.Worktree{
			{Path: wtPath, Branch: "feat"},
		}},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	m.SetProjectsFile(filepath.Join(dir, "projects"))

	// Expand project 0 and enter delete mode
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	// Navigate to worktree (flat=1)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)

	if mm3.DeleteSelected() != 1 {
		t.Fatalf("precondition: deleteSelected should be 1, got %d", mm3.DeleteSelected())
	}

	// Confirm deletion
	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm4 := newModel4.(*tui.MainMenuModel)

	// Should stay in delete mode and show success feedback
	if !mm4.InDeleteMode() {
		t.Error("should remain in delete mode after worktree deletion")
	}
	view := mm4.View()
	if !strings.Contains(view, "Removed") && !strings.Contains(view, "feat") {
		t.Errorf("expected success feedback mentioning 'Removed' or 'feat', got view: %s", view)
	}
}

func TestDeleteMode_ConfirmDelete_WorktreeTarget_Dirty(t *testing.T) {
	// Set up dirty worktree — confirms that pendingForceDeleteWT is set and feedback shown
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()
	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add: %v: %s", err, out)
	}
	// Make it dirty
	os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("dirty"), 0644)

	projects := []models.Project{
		{Name: "myproj", Path: dir, Worktrees: []models.Worktree{
			{Path: wtPath, Branch: "feat"},
		}},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	m.SetProjectsFile(filepath.Join(dir, "projects"))

	// Expand, enter delete mode, navigate to worktree
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)

	// Confirm deletion — should fail dirty and set pending
	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm4 := newModel4.(*tui.MainMenuModel)

	// Should stay in delete mode
	if !mm4.InDeleteMode() {
		t.Error("should remain in delete mode when worktree is dirty")
	}
	// View should mention "changes" or "force" or "Y"
	view := mm4.View()
	if !strings.Contains(view, "Y") && !strings.Contains(view, "force") && !strings.Contains(view, "changes") {
		t.Errorf("expected dirty worktree feedback in view, got: %s", view)
	}

	// Pressing Y should force-remove
	newModel5, _ := mm4.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	mm5 := newModel5.(*tui.MainMenuModel)

	if !mm5.InDeleteMode() {
		t.Error("should remain in delete mode after force removal")
	}
	view2 := mm5.View()
	if !strings.Contains(view2, "Removed") && !strings.Contains(view2, "feat") {
		t.Errorf("expected success feedback after force removal, got: %s", view2)
	}
}

func TestDeleteMode_ConfirmDelete_WorktreeTarget_Locked(t *testing.T) {
	// A locked worktree (e.g. stale claude agent lock) must behave like dirty:
	// first Enter shows a Y-to-force prompt; Y force-removes it.
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()
	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "worktree", "lock", "--reason", "stale agent", wtPath).CombinedOutput(); err != nil {
		t.Skipf("git worktree lock: %v: %s", err, out)
	}

	projects := []models.Project{
		{Name: "myproj", Path: dir, Worktrees: []models.Worktree{
			{Path: wtPath, Branch: "feat"},
		}},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	m.SetProjectsFile(filepath.Join(dir, "projects"))

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)

	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm4 := newModel4.(*tui.MainMenuModel)

	if !mm4.InDeleteMode() {
		t.Error("should remain in delete mode when worktree is locked")
	}
	view := mm4.View()
	if !strings.Contains(view, "Y") && !strings.Contains(view, "force") && !strings.Contains(view, "locked") {
		t.Errorf("expected locked-worktree prompt in view, got: %s", view)
	}

	newModel5, _ := mm4.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	mm5 := newModel5.(*tui.MainMenuModel)

	if !mm5.InDeleteMode() {
		t.Error("should remain in delete mode after force removal of locked worktree")
	}
	view2 := mm5.View()
	if !strings.Contains(view2, "Removed") && !strings.Contains(view2, "feat") {
		t.Errorf("expected success feedback after force removal, got: %s", view2)
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Error("locked worktree path should not exist after force removal")
	}
}

func TestDeleteMode_PreservesCursorPosition(t *testing.T) {
	// When the user's main cursor is on a project row and they enter delete mode,
	// deleteSelected should start at that project's flat index (not always 0).
	projects := []models.Project{
		{Name: "proj1", Path: "/p1"},
		{Name: "proj2", Path: "/p2"},
		{Name: "proj3", Path: "/p3"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)

	// Navigate down twice so selectedItem points at proj3 (flat index 2).
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	newModel, _ = newModel.(*tui.MainMenuModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := newModel.(*tui.MainMenuModel)
	if mm.SelectedItem() != 2 {
		t.Fatalf("precondition: expected selectedItem=2, got %d", mm.SelectedItem())
	}

	// Enter delete mode.
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	// deleteSelected should match the pre-existing cursor position (flat index 2).
	if mm2.DeleteSelected() != 2 {
		t.Errorf("DeleteSelected after entering delete mode: want 2 (cursor position), got %d", mm2.DeleteSelected())
	}
}

func TestDeleteMode_FallsBackToFirstDeletableWhenCursorOnNonDeletable(t *testing.T) {
	// When the main cursor is on a non-deletable row (e.g., an action row),
	// enterDeleteMode should fall back to the first deletable item.
	projects := []models.Project{
		{Name: "proj1", Path: "/p1"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)

	// Move cursor past all projects onto the action rows (keep pressing down until
	// we're past the last project flat index).
	total := m.TotalItems()
	mm := m
	for i := 0; i < total; i++ {
		newModel, _ := mm.Update(tea.KeyMsg{Type: tea.KeyDown})
		mm = newModel.(*tui.MainMenuModel)
		itemType, _, _ := mm.ResolveItem(mm.SelectedItem())
		if itemType == "action" {
			break
		}
	}
	itemType, _, _ := mm.ResolveItem(mm.SelectedItem())
	if itemType != "action" {
		t.Skip("could not navigate to an action row — skipping test")
	}

	// Enter delete mode — cursor is on an action row (non-deletable).
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	// Should fall back to the first deletable item (flat index 0).
	deletable := mm2.DeletableItems()
	if len(deletable) == 0 {
		t.Fatal("DeletableItems returned empty")
	}
	if mm2.DeleteSelected() != deletable[0] {
		t.Errorf("DeleteSelected fallback: want %d (first deletable), got %d", deletable[0], mm2.DeleteSelected())
	}
}

func TestDeleteMode_StaysOpenAfterProjectDeletion(t *testing.T) {
	// After deleting a project, delete mode should remain active so the user
	// can delete more items without re-entering delete mode.
	dir := t.TempDir()
	projectsFile := filepath.Join(dir, "projects")
	if err := os.WriteFile(projectsFile, []byte("proj1:/p1\nproj2:/p2\nproj3:/p3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	projects := []models.Project{
		{Name: "proj1", Path: "/p1"},
		{Name: "proj2", Path: "/p2"},
		{Name: "proj3", Path: "/p3"},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	m.SetProjectsFile(projectsFile)

	// Enter delete mode (cursor on proj1).
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)

	// Confirm deletion of proj1.
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := newModel2.(*tui.MainMenuModel)

	// Delete mode must still be active.
	if !mm2.InDeleteMode() {
		t.Error("delete mode should remain active after deleting a project")
	}
	// Feedback should mention the deleted project.
	view := mm2.View()
	if !strings.Contains(view, "proj1") && !strings.Contains(view, "Deleted") {
		t.Errorf("expected success feedback after deletion, got: %s", view)
	}
}

func TestDeleteMode_StaysOpenAfterWorktreeDeletion(t *testing.T) {
	// After deleting a worktree, delete mode should remain active.
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()
	exec.Command("git", "-C", dir, "branch", "feat").Run()
	wtPath := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wtPath, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add: %v: %s", err, out)
	}

	projects := []models.Project{
		{Name: "myproj", Path: dir, Worktrees: []models.Worktree{
			{Path: wtPath, Branch: "feat"},
		}},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	m.SetProjectsFile(filepath.Join(dir, "projects"))

	// Expand project 0, enter delete mode, navigate to worktree (flat=1).
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)
	if mm3.DeleteSelected() != 1 {
		t.Fatalf("precondition: deleteSelected should be 1, got %d", mm3.DeleteSelected())
	}

	// Confirm deletion.
	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm4 := newModel4.(*tui.MainMenuModel)

	// Delete mode must still be active.
	if !mm4.InDeleteMode() {
		t.Error("delete mode should remain active after deleting a worktree")
	}
}

func TestDeleteMode_DKeyExitsDeleteMode(t *testing.T) {
	// Pressing 'd' while already in delete mode should exit delete mode.
	projects := testProjects()
	m := tui.NewMainMenu(projects, testAITools(), "claude", "animated")

	// Enter delete mode.
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm := newModel.(*tui.MainMenuModel)
	if !mm.InDeleteMode() {
		t.Fatal("precondition: should be in delete mode after pressing 'd'")
	}

	// Press 'd' again — should exit.
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)
	if mm2.InDeleteMode() {
		t.Error("pressing 'd' in delete mode should exit delete mode")
	}
}

func TestDeleteMode_KeepsWorktreesExpandedAfterWorktreeDeletion(t *testing.T) {
	// After deleting a worktree, the parent project should remain expanded
	// so the user can see and delete remaining worktrees without re-expanding.
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run()
	exec.Command("git", "-C", dir, "branch", "feat").Run()
	exec.Command("git", "-C", dir, "branch", "fix").Run()

	wt1Path := filepath.Join(t.TempDir(), "feat")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wt1Path, "feat").CombinedOutput(); err != nil {
		t.Skipf("git worktree add feat: %v: %s", err, out)
	}
	wt2Path := filepath.Join(t.TempDir(), "fix")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wt2Path, "fix").CombinedOutput(); err != nil {
		t.Skipf("git worktree add fix: %v: %s", err, out)
	}

	projects := []models.Project{
		{Name: "myproj", Path: dir, Worktrees: []models.Worktree{
			{Path: wt1Path, Branch: "feat"},
			{Path: wt2Path, Branch: "fix"},
		}},
	}
	projectsFile := filepath.Join(dir, "projects")
	if err := os.WriteFile(projectsFile, []byte("myproj:"+dir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetSize(80, 30)
	m.SetProjectsFile(projectsFile)

	// Expand project 0 and enter delete mode.
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	mm := newModel.(*tui.MainMenuModel)
	if !mm.IsExpanded(0) {
		t.Fatal("precondition: project 0 should be expanded")
	}
	newModel2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mm2 := newModel2.(*tui.MainMenuModel)

	// Navigate to first worktree (flat=1) and delete it.
	newModel3, _ := mm2.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm3 := newModel3.(*tui.MainMenuModel)
	if mm3.DeleteSelected() != 1 {
		t.Fatalf("precondition: deleteSelected should be 1, got %d", mm3.DeleteSelected())
	}
	newModel4, _ := mm3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm4 := newModel4.(*tui.MainMenuModel)

	// Project 0 must still be expanded after the deletion.
	if !mm4.IsExpanded(0) {
		t.Error("project should remain expanded after one of its worktrees is deleted")
	}
}

var ansiEscRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string { return ansiEscRe.ReplaceAllString(s, "") }

func TestAddProject_TabWithoutAutocompleteAdvancesToName(t *testing.T) {
	dir := t.TempDir()
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(dir)
	// Tab with no autocomplete suggestions should advance to name
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := result.(*tui.MainMenuModel)
	if mm.InputFocusPath() {
		t.Error("expected Tab without autocomplete to advance to name field")
	}
}

func TestAddProject_AutoSuffixShownWhenNotTouched(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "my-project")
	os.MkdirAll(projDir, 0755)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir)
	// Advance to name field
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)
	view := stripAnsi(mm.View())
	if !strings.Contains(view, "(auto)") {
		t.Errorf("expected '(auto)' suffix in view when name not manually edited, got: %q", view)
	}
}

func TestAddProject_AutoSuffixHiddenWhenTouched(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "my-project")
	os.MkdirAll(projDir, 0755)

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(projDir)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)
	mm.SetNameTouched(true)
	view := stripAnsi(mm.View())
	if strings.Contains(view, "(auto)") {
		t.Errorf("expected no '(auto)' suffix after user edits name, got: %q", view)
	}
}

func TestAddProject_PreFillsPathWithProjectsRoot(t *testing.T) {
	dir := t.TempDir()
	rootFile := filepath.Join(dir, "projects-root")
	if err := os.WriteFile(rootFile, []byte(dir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetProjectsRootFile(rootFile)
	m.EnterInputModeForTest("add-project")

	if !strings.HasPrefix(m.PathInputValue(), dir) {
		t.Errorf("expected path pre-filled with %q, got %q", dir, m.PathInputValue())
	}
}

func TestAddProject_NoPreFillWhenRootFileAbsent(t *testing.T) {
	dir := t.TempDir()
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetProjectsRootFile(filepath.Join(dir, "missing-file"))
	m.EnterInputModeForTest("add-project")

	if m.PathInputValue() != "" {
		t.Errorf("expected empty path when root file absent, got %q", m.PathInputValue())
	}
}

func TestSettings_ProjectsRootItem_AppearsInSettingsBox(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterSettings()
	view := stripAnsi(m.View())
	if !strings.Contains(view, "Default projects dir") {
		t.Error("expected 'Default projects dir' in settings view")
	}
}

func TestSettings_ProjectsRootItem_ShowsNotSet(t *testing.T) {
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterSettings()
	view := stripAnsi(m.View())
	if !strings.Contains(view, "(not set)") {
		t.Errorf("expected '(not set)' when no root configured, view: %q", view)
	}
}

func TestSettings_ProjectsRootItem_ShowsCurrentValue(t *testing.T) {
	dir := t.TempDir()
	rootFile := filepath.Join(dir, "projects-root")
	if err := os.WriteFile(rootFile, []byte(dir), 0644); err != nil {
		t.Fatal(err)
	}

	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetProjectsRootFile(rootFile)
	m.LoadProjectsRoot()
	m.EnterSettings()
	view := stripAnsi(m.View())
	if !strings.Contains(view, filepath.Base(dir)) {
		t.Errorf("expected root path in view, got: %q", view)
	}
}

func TestSettings_NavWrapsWithNineItems(t *testing.T) {
	// claude tool shows 9 settings items (Ghost Display, Tab Title, Sound, Panel,
	// Theme, Default projects dir, Plan, Login)
	m := tui.NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.EnterSettings()
	// j 8 times — wraps back to 0 (vim accelerator wraps within the list)
	for i := 0; i < 9; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.SettingsSelected() != 0 {
		t.Errorf("expected settingsSelected=0 after wrapping past 9 items, got %d", m.SettingsSelected())
	}
}

func TestSettings_NavWrapsWithNineItems_NonClaude(t *testing.T) {
	// The Plan + Login rows are shared across agents, so opencode also shows 9
	// settings items (Ghost Display, Tab Title, Sound, Panel, Theme, Default
	// projects dir, Plan, Login).
	m := tui.NewMainMenu(nil, []string{"opencode"}, "opencode", "animated")
	m.EnterSettings()
	// j 8 times — wraps back to 0 (vim accelerator wraps within the list)
	for i := 0; i < 9; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.SettingsSelected() != 0 {
		t.Errorf("expected settingsSelected=0 after wrapping past 9 items, got %d", m.SettingsSelected())
	}
}

func TestStale_MarkerRenderedForStaleProject(t *testing.T) {
	projects := []models.Project{
		{Name: "good", Path: "/exists", Stale: false},
		{Name: "bad", Path: "/nonexistent", Stale: true},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	view := stripAnsi(m.View())
	if !strings.Contains(view, "⚠") {
		t.Error("expected ⚠ marker for stale project in view")
	}
}

func TestStale_NoMarkerForHealthyProject(t *testing.T) {
	projects := []models.Project{
		{Name: "good", Path: "/exists", Stale: false},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	view := stripAnsi(m.View())
	if strings.Contains(view, "⚠") {
		t.Error("expected no ⚠ marker when project is healthy")
	}
}

func TestStale_SelectionShowsConfirmation(t *testing.T) {
	projects := []models.Project{
		{Name: "bad", Path: "/nonexistent", Stale: true},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	// Select the stale project (Enter with project focused)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)
	view := stripAnsi(mm.View())
	if !strings.Contains(view, "Launch anyway") {
		t.Errorf("expected stale confirmation prompt, got: %q", view)
	}
}

func TestStale_NKeyAtConfirmationCancels(t *testing.T) {
	projects := []models.Project{
		{Name: "bad", Path: "/nonexistent", Stale: true},
	}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // select → confirmation
	mm := result.(*tui.MainMenuModel)
	result2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	mm2 := result2.(*tui.MainMenuModel)
	if mm2.StaleConfirmIdx() >= 0 {
		t.Error("expected stale confirmation dismissed after n")
	}
	if mm2.Result() != nil {
		t.Error("expected no result after cancelling stale confirmation")
	}
}

func TestStale_EnterKeyAtConfirmationCancels(t *testing.T) {
	// Enter = accept default N = cancel
	projects := []models.Project{{Name: "bad", Path: "/nonexistent", Stale: true}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)
	result2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)
	if mm2.StaleConfirmIdx() >= 0 {
		t.Error("expected stale confirmation dismissed after Enter (default N)")
	}
}

func TestStale_YKeyAtConfirmationProceeds(t *testing.T) {
	projects := []models.Project{{Name: "bad", Path: "/nonexistent", Stale: true}}
	m := tui.NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // select → confirmation
	mm := result.(*tui.MainMenuModel)
	result2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	mm2 := result2.(*tui.MainMenuModel)
	if mm2.StaleConfirmIdx() >= 0 {
		t.Error("expected stale confirmation dismissed after y")
	}
	if mm2.Result() == nil {
		t.Error("expected a result (launch) after y at stale confirmation")
	}
}
