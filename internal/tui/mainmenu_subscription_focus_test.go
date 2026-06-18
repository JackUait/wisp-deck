package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
)

// subFocusMenu builds a Claude menu with one custom subscription so the
// subscription focus stop is reachable.
func subFocusMenu(t *testing.T, tool string, withConfigs bool) *MainMenuModel {
	t.Helper()
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "codex"}, tool, "animated")
	m.SetSize(100, 40)
	if withConfigs {
		m.SetClaudeConfigs([]ClaudeConfig{{Name: "Work", File: "work.json"}})
	}
	return m
}

func TestSubFocus_downFromAIGoesToSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusSubscription {
		t.Errorf("Down from AI = %v, want FocusSubscription", m.Focus())
	}
}

func TestSubFocus_downFromAISkipsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("Down from AI (no configs) = %v, want FocusTabs", m.Focus())
	}
}

func TestSubFocus_downFromAISkipsWhenNonClaude(t *testing.T) {
	m := subFocusMenu(t, "codex", true)
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("Down from AI (codex) = %v, want FocusTabs", m.Focus())
	}
}

func TestSubFocus_downFromSubscriptionGoesToTabs(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.Focus() != FocusTabs {
		t.Errorf("Down from subscription = %v, want FocusTabs", m.Focus())
	}
}

func TestSubFocus_upFromTabsGoesToSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusSubscription {
		t.Errorf("Up from tabs = %v, want FocusSubscription", m.Focus())
	}
}

func TestSubFocus_upFromTabsSkipsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	m.SetFocus(FocusTabs)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from tabs (no configs) = %v, want FocusAI", m.Focus())
	}
}

func TestSubFocus_upFromSubscriptionGoesToAI(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from subscription = %v, want FocusAI", m.Focus())
	}
}

func TestSubFocus_rightCyclesSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentClaudeConfigName() != "Work" {
		t.Errorf("Right on subscription = %q, want Work", m.CurrentClaudeConfigName())
	}
}

func TestSubFocus_leftCyclesSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.CurrentClaudeConfigName() != "Work" {
		t.Errorf("Left on subscription (wrap) = %q, want Work", m.CurrentClaudeConfigName())
	}
}

func TestSubFocus_rightOnSubscriptionDoesNotCycleAI(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.CurrentAITool() != "claude" {
		t.Errorf("Right on subscription changed AI to %q, want claude", m.CurrentAITool())
	}
}

func TestSubscriptionRow_showsChevronsWhenFocusable(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	row := stripAnsi(m.renderSubscriptionRow("│", "│"))
	if !strings.Contains(row, "◂") || !strings.Contains(row, "▸") {
		t.Errorf("subscription row missing cycle chevrons when focusable:\n%s", row)
	}
}

func TestSubscriptionRow_noChevronsWhenNoConfigs(t *testing.T) {
	m := subFocusMenu(t, "claude", false)
	row := stripAnsi(m.renderSubscriptionRow("│", "│"))
	if strings.Contains(row, "◂") || strings.Contains(row, "▸") {
		t.Errorf("subscription row should not show chevrons with no custom configs:\n%s", row)
	}
}

func TestSubFocus_helpHintMentionsSubscription(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	hint := m.focusHint()
	if !strings.Contains(strings.ToLower(hint), "subscription") {
		t.Errorf("focus hint for subscription = %q, want it to mention subscription", hint)
	}
}
