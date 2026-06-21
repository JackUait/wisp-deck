package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/tui"
)

func TestLogo_InitialFrame(t *testing.T) {
	m := tui.NewLogo("claude")
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() should return commands for tick and quit timer")
	}
}

func TestLogo_QuitOnKeypress(t *testing.T) {
	m := tui.NewLogo("claude")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Error("keypress should trigger quit command")
	}
	_ = updated
}

func TestLogo_AllToolsRender(t *testing.T) {
	tools := []string{"claude", "opencode"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			m := tui.NewLogo(tool)
			view := m.View()
			if view == "" {
				t.Errorf("Logo view for %q should not be empty", tool)
			}
		})
	}
}
