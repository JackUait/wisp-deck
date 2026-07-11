package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func aKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}} }

func TestAbout_opensOnAInSettings(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody

	m.Update(aKey())

	if !m.aboutOpen {
		t.Fatalf("pressing 'a' on the Settings tab should open About")
	}
	if m.inputMode != "" {
		t.Errorf("pressing 'a' on Settings should not enter add-project mode, got inputMode=%q", m.inputMode)
	}

	view := m.View()
	for _, want := range []string{"Made by Evgeniy Pyatkov (@jackuait)", "@that_ai_guy"} {
		if !strings.Contains(view, want) {
			t.Errorf("About panel missing %q:\n%s", want, view)
		}
	}
}

func TestAbout_aOnProjectsStillAddsProject(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabProjects)
	m.focus = FocusBody

	m.Update(aKey())

	if m.aboutOpen {
		t.Errorf("'a' on the Projects tab must not open About")
	}
	if m.inputMode != "add-project" {
		t.Errorf("'a' on Projects should enter add-project, got inputMode=%q", m.inputMode)
	}
}

func TestAbout_escCloses(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(aKey())
	if !m.aboutOpen {
		t.Fatalf("precondition: About should be open")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.aboutOpen {
		t.Errorf("Esc should close the About panel")
	}
}

func TestAbout_showsAppVersion(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetAppVersion("2.23.0")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(aKey())

	if view := m.View(); !strings.Contains(view, "Version 2.23.0") {
		t.Errorf("About panel missing the app version:\n%s", view)
	}
}

func TestAbout_omitsVersionLineWhenUnset(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.focus = FocusBody
	m.Update(aKey())

	if view := m.View(); strings.Contains(view, "Version") {
		t.Errorf("About panel should not render a Version line without a version:\n%s", view)
	}
}
