package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/jackuait/ghost-tab/internal/tui"
)

func TestFilterProjects_AfterThemeApply(t *testing.T) {
	// Apply a non-default theme, then verify FilterProjects still works correctly
	tui.ApplyTheme(tui.ThemeForTool("opencode"))

	projects := []models.Project{
		{Name: "web-app", Path: "/home/user/web-app"},
	}
	filtered := tui.FilterProjects(projects, "web")
	if len(filtered) != 1 {
		t.Errorf("expected 1 result, got %d", len(filtered))
	}
}

func TestFilterProjects(t *testing.T) {
	projects := []models.Project{
		{Name: "web-app", Path: "/home/user/web-app"},
		{Name: "cli-tool", Path: "/home/user/cli-tool"},
		{Name: "data-service", Path: "/opt/data-service"},
	}

	tests := []struct {
		name     string
		filter   string
		expected int
	}{
		{"no filter", "", 3},
		{"filter web", "web", 1},
		{"filter cli", "cli", 1},
		{"filter data", "data", 1},
		{"no match", "xyz", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := tui.FilterProjects(projects, tt.filter)
			if len(filtered) != tt.expected {
				t.Errorf("FilterProjects with %q: expected %d, got %d", tt.filter, tt.expected, len(filtered))
			}
		})
	}
}

func TestProjectSelector_New(t *testing.T) {
	projects := []models.Project{
		{Name: "app", Path: "/tmp/app"},
		{Name: "web", Path: "/tmp/web"},
	}
	m := tui.NewProjectSelector(projects)
	if m.Selected() != nil {
		t.Error("Selected should be nil initially")
	}
}

func TestProjectSelector_InitReturnsNil(t *testing.T) {
	m := tui.NewProjectSelector([]models.Project{{Name: "a", Path: "/a"}})
	if m.Init() != nil {
		t.Error("Init should return nil")
	}
}

func TestProjectSelector_EnterSelectsProject(t *testing.T) {
	projects := []models.Project{
		{Name: "app", Path: "/tmp/app"},
	}
	m := tui.NewProjectSelector(projects)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("Enter should return quit command")
	}
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() == nil {
		t.Fatal("Enter should select current project")
	}
	if result.Selected().Name != "app" {
		t.Errorf("Expected 'app', got %q", result.Selected().Name)
	}
}

func TestProjectSelector_EscCancels(t *testing.T) {
	m := tui.NewProjectSelector([]models.Project{{Name: "a", Path: "/a"}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Error("Esc should return quit command")
	}
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() != nil {
		t.Error("Esc should not select anything")
	}
}

func TestProjectSelector_CtrlCCancels(t *testing.T) {
	m := tui.NewProjectSelector([]models.Project{{Name: "a", Path: "/a"}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("Ctrl+C should return quit command")
	}
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() != nil {
		t.Error("Ctrl+C should not select anything")
	}
}

func TestProjectSelector_WindowSizeMsg(t *testing.T) {
	m := tui.NewProjectSelector([]models.Project{{Name: "a", Path: "/a"}})
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil {
		t.Error("WindowSizeMsg should return nil cmd")
	}
	_ = updated
}

func TestProjectSelector_ViewNonEmpty(t *testing.T) {
	m := tui.NewProjectSelector([]models.Project{{Name: "app", Path: "/tmp/app"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := updated.(tui.ProjectSelectorModel).View()
	if view == "" {
		t.Error("View should not be empty before quitting")
	}
}

func TestProjectSelector_ViewEmptyAfterQuit(t *testing.T) {
	m := tui.NewProjectSelector([]models.Project{{Name: "a", Path: "/a"}})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(tui.ProjectSelectorModel)
	if result.View() != "" {
		t.Error("View should be empty after quitting")
	}
}

func TestProjectSelector_NumberKeySelectsProject(t *testing.T) {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
		{Name: "gamma", Path: "/tmp/gamma"},
	}
	m := tui.NewProjectSelector(projects)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd == nil {
		t.Fatal("Number key should return quit command")
	}
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() == nil {
		t.Fatal("Number key should select a project")
	}
	if result.Selected().Name != "alpha" {
		t.Errorf("Expected 'alpha', got %q", result.Selected().Name)
	}
	if result.Selected().Path != "/tmp/alpha" {
		t.Errorf("Expected '/tmp/alpha', got %q", result.Selected().Path)
	}
}

func TestProjectSelector_NumberKeyOutOfRange(t *testing.T) {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := tui.NewProjectSelector(projects)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	if cmd != nil {
		t.Error("Out-of-range number key should not return quit command")
	}
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() != nil {
		t.Error("Out-of-range number key should not select anything")
	}
}

func TestProjectSelector_NumberKeyZeroIgnored(t *testing.T) {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
	}
	m := tui.NewProjectSelector(projects)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	if cmd != nil {
		t.Error("Zero key should not return quit command")
	}
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() != nil {
		t.Error("Zero key should not select anything")
	}
}

func TestProjectSelector_EmptyList(t *testing.T) {
	// Selected() is nil initially on empty list
	m := tui.NewProjectSelector(nil)
	if m.Selected() != nil {
		t.Error("Selected should be nil initially on empty list")
	}

	// Enter on empty list should not panic and should not select anything
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(tui.ProjectSelectorModel)
	if result.Selected() != nil {
		t.Error("Enter on empty list should not select anything")
	}

	// Esc on empty list should return quit command, Selected still nil
	m2 := tui.NewProjectSelector(nil)
	updated2, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd2 == nil {
		t.Error("Esc should return quit command even on empty list")
	}
	result2 := updated2.(tui.ProjectSelectorModel)
	if result2.Selected() != nil {
		t.Error("Esc on empty list should not select anything")
	}

	// WindowSizeMsg then View() should not panic on empty list
	m3 := tui.NewProjectSelector(nil)
	updated3, _ := m3.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_ = updated3.(tui.ProjectSelectorModel).View()
}
