package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The PLAN switcher row sits under the AGENT picker on the Projects tab. The
// header chrome is shared across tabs, so the PLAN row must render on Settings
// and Stats too — otherwise the header jumps between tabs and the focus ring
// lands on an invisible stop.
func TestPlanRow_rendersOnSettingsTab(t *testing.T) {
	m := subTestMenu("claude")
	m.SetActiveTab(TabSettings)
	out := stripAnsi(m.renderSettingsBox())
	if !strings.Contains(out, iconPlan) {
		t.Errorf("settings box should carry the PLAN switcher row:\n%s", out)
	}
}

func TestPlanRow_rendersOnStatsTab(t *testing.T) {
	m := subTestMenu("claude")
	m.SetActiveTab(TabStats)
	out := stripAnsi(m.renderStatsBox())
	if !strings.Contains(out, iconPlan) {
		t.Errorf("stats box should carry the PLAN switcher row:\n%s", out)
	}
}

// Now that the PLAN row renders on every tab, its focus stop must be reachable
// on Settings and Stats too (when a keyed config exists).
func TestPlanRow_focusReachableOnAllTabs(t *testing.T) {
	for _, tab := range []MenuTab{TabSettings, TabStats} {
		m := subFocusMenu(t, "claude", true)
		m.SetActiveTab(tab)
		if !m.subscriptionFocusable() {
			t.Errorf("tab %v: subscription should be focusable now its row renders on every tab", tab)
		}

		// Up from AI must stop on the subscription row.
		m.SetFocus(FocusAI)
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		if m.Focus() != FocusSubscription {
			t.Errorf("tab %v: Up from AI = %v, want FocusSubscription", tab, m.Focus())
		}

		// Down from the subscription row must return to the AI switcher.
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if m.Focus() != FocusAI {
			t.Errorf("tab %v: Down from subscription = %v, want FocusAI", tab, m.Focus())
		}
	}
}
