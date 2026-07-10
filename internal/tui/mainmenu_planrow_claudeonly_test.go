package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The PLAN/subscription line describes a Claude subscription, so it only belongs
// in the header while Claude is the active agent.
func TestPlanRow_hiddenForNonClaudeAgent(t *testing.T) {
	boxes := map[string]func(m *MainMenuModel) string{
		"projects": func(m *MainMenuModel) string { return m.renderMenuBox() },
		"settings": func(m *MainMenuModel) string { return m.renderSettingsBox() },
		"stats":    func(m *MainMenuModel) string { return m.renderStatsBox() },
	}
	for name, render := range boxes {
		t.Run(name, func(t *testing.T) {
			m := subTestMenu("opencode")
			out := stripAnsi(render(m))
			if strings.Contains(out, iconPlan) {
				t.Errorf("%s box should not carry the PLAN row for opencode:\n%s", name, out)
			}
			if !strings.Contains(out, iconAgent) {
				t.Errorf("%s box lost the AGENT row:\n%s", name, out)
			}
		})
	}
}

func TestPlanRow_countIsZeroForNonClaudeAgent(t *testing.T) {
	if got := subTestMenu("opencode").subscriptionRowCount(); got != 0 {
		t.Errorf("subscriptionRowCount() for opencode = %d, want 0", got)
	}
	if got := subTestMenu("claude").subscriptionRowCount(); got != 1 {
		t.Errorf("subscriptionRowCount() for claude = %d, want 1", got)
	}
}

// With the PLAN row gone, the AGENT row is topmost and reclaims the wordmark.
func TestPlanRow_wordmarkFallsBackToAgentRowForNonClaude(t *testing.T) {
	m := subTestMenu("opencode")
	lines := strings.Split(stripAnsi(m.renderMenuBox()), "\n")
	if !strings.Contains(lines[1], iconAgent) {
		t.Fatalf("expected the AGENT row directly under the top border, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "Wisp Deck") {
		t.Errorf("AGENT row should carry the wordmark when it is topmost, got %q", lines[1])
	}
}

// A hidden row must never be a focus stop, even when keyed configs exist.
func TestPlanRow_notFocusableForNonClaudeAgent(t *testing.T) {
	m := subFocusMenu(t, "opencode", true)
	if m.subscriptionFocusable() {
		t.Error("subscription should not be focusable while a non-Claude agent is active")
	}
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AGENT (opencode) = %v, want FocusAI (no PLAN row above)", m.Focus())
	}
}

// Cycling away from Claude removes the row, so the project rows shift up by one
// and click mapping has to follow.
func TestPlanRow_clickMappingShiftsWhenRowHidden(t *testing.T) {
	m := subTestMenu("opencode")
	// Header rows: top, title, switcher-gap, tab bar, separator, leading blank (5)
	// — so the first project lands at row 5 without the subscription row.
	if got := m.MapRowToItem(5); got != -1 {
		t.Errorf("row 5 should be the leading blank (-1) for opencode, got %d", got)
	}
	if got := m.MapRowToItem(6); got != 0 {
		t.Errorf("row 6 should be the first project for opencode, got %d", got)
	}
}

// Switching the agent off Claude while the PLAN row holds focus must not strand
// focus on a row that no longer renders.
func TestPlanRow_focusLeavesRowWhenAgentCyclesAway(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	m.SetFocus(FocusSubscription)
	m.CycleAITool("next") // -> opencode, PLAN row disappears
	if m.Focus() == FocusSubscription {
		t.Error("focus should leave the subscription row once it stops rendering")
	}
}
