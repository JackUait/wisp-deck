package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The PLAN/subscription switcher was removed from the main-page header. Choosing
// a backend now lives in Settings › Subscription and the mid-session account
// popup / ledger pill, so the header must not render or focus a PLAN row for any
// agent — including Claude.
func TestPlanRow_removedFromHeaderForClaude(t *testing.T) {
	m := subTestMenu("claude")
	if got := m.subscriptionRowCount(); got != 0 {
		t.Errorf("subscriptionRowCount() for claude = %d, want 0 (switcher removed)", got)
	}
	out := stripAnsi(m.renderMenuBox())
	if strings.Contains(out, iconPlan) {
		t.Errorf("main-page header must not carry the PLAN switcher for claude:\n%s", out)
	}
}

// The removed row is never a focus stop, even with a keyed subscription, and Up
// from the AGENT switcher no longer lands on it.
func TestPlanRow_notFocusableAfterRemoval(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	if m.subscriptionFocusable() {
		t.Error("subscription must not be a focus stop after removal")
	}
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AGENT = %v, want FocusAI (no PLAN row above)", m.Focus())
	}
}
