package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The Tab Title setting has three modes:
//   full    -> "Project · Tool"  (watcher writes the title)
//   project -> "Project Only"    (watcher writes the title)
//   model   -> "Model Set"       (watcher leaves the AI tool's own title alone)

func TestCycleTabTitle_fullToProject(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")
	m.CycleTabTitle()
	if m.TabTitle() != "project" {
		t.Errorf("expected project after cycling from full, got %q", m.TabTitle())
	}
}

func TestCycleTabTitle_projectToModel(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("project")
	m.CycleTabTitle()
	if m.TabTitle() != "model" {
		t.Errorf("expected model after cycling from project, got %q", m.TabTitle())
	}
}

func TestCycleTabTitle_modelToFull(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("model")
	m.CycleTabTitle()
	if m.TabTitle() != "full" {
		t.Errorf("expected full after cycling from model, got %q", m.TabTitle())
	}
}

func TestCycleTabTitleReverse_fullToModel(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")
	m.CycleTabTitleReverse()
	if m.TabTitle() != "model" {
		t.Errorf("expected model (reverse from full), got %q", m.TabTitle())
	}
}

func TestCycleTabTitleReverse_modelToProject(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("model")
	m.CycleTabTitleReverse()
	if m.TabTitle() != "project" {
		t.Errorf("expected project (reverse from model), got %q", m.TabTitle())
	}
}

func TestCycleTabTitleReverse_projectToFull(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("project")
	m.CycleTabTitleReverse()
	if m.TabTitle() != "full" {
		t.Errorf("expected full (reverse from project), got %q", m.TabTitle())
	}
}

func TestTabTitleLabel_model(t *testing.T) {
	if got := tabTitleLabel("model"); got != "Model Set" {
		t.Errorf("tabTitleLabel(\"model\") = %q, want \"Model Set\"", got)
	}
}

func TestRenderSettingsBox_tabTitleRowShowsModelSet(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetTabTitle("model")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "[Model Set]") {
		t.Errorf("settings box should show [Model Set] for model mode:\n%s", out)
	}
}

func TestRenderSettingsBox_tabTitleModelColor(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetTabTitle("model")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// model = distinct blue (75)
	blueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	expected := blueStyle.Render("Model Set")
	if !strings.Contains(out, expected) {
		t.Errorf("tab title row should use blue for model mode:\n%s", out)
	}
}

func TestSettingsValueLeft_tabTitleReverseCycles(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetTabTitle("full")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = 1 // Tab Title row
	m.settingsValueLeft()
	if m.TabTitle() != "model" {
		t.Errorf("expected model (reverse) after left on tab title row, got %q", m.TabTitle())
	}
}
