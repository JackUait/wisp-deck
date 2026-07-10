package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// headerRowIndices locates the PLAN row, the AGENT row and the tab bar within a
// rendered box.
func headerRowIndices(t *testing.T, box string) (planIdx, agentIdx, tabIdx int) {
	t.Helper()
	planIdx, agentIdx, tabIdx = -1, -1, -1
	lines := strings.Split(stripAnsi(box), "\n")
	for i, l := range lines {
		if agentIdx == -1 && strings.Contains(l, iconAgent) {
			agentIdx = i
		}
		if planIdx == -1 && strings.Contains(l, iconPlan) {
			planIdx = i
		}
		if tabIdx == -1 && strings.Contains(l, "Projects") && strings.Contains(l, "Stats") {
			tabIdx = i
		}
	}
	if planIdx < 0 || agentIdx < 0 || tabIdx < 0 {
		t.Fatalf("missing rows: plan=%d agent=%d tab=%d\n%s", planIdx, agentIdx, tabIdx, box)
	}
	return planIdx, agentIdx, tabIdx
}

// The PLAN switcher is the topmost header row and the AGENT picker sits beneath
// it, on every tab.
func TestHeaderOrder_planAboveAgentOnEveryTab(t *testing.T) {
	tests := []struct {
		name string
		box  func(m *MainMenuModel) string
	}{
		{"projects", func(m *MainMenuModel) string { return m.renderMenuBox() }},
		{"settings", func(m *MainMenuModel) string { return m.renderSettingsBox() }},
		{"stats", func(m *MainMenuModel) string { return m.renderStatsBox() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := subTestMenu("claude")
			planIdx, agentIdx, tabIdx := headerRowIndices(t, tt.box(m))
			if !(planIdx < agentIdx && agentIdx < tabIdx) {
				t.Errorf("expected PLAN < AGENT < tab bar, got plan=%d agent=%d tab=%d", planIdx, agentIdx, tabIdx)
			}
		})
	}
}

// The "Wisp Deck" wordmark right-aligns on the topmost header row, which is now
// the PLAN row.
func TestHeaderOrder_wordmarkRidesTheTopRow(t *testing.T) {
	m := subTestMenu("claude")
	lines := strings.Split(stripAnsi(m.renderMenuBox()), "\n")
	planIdx, agentIdx, _ := headerRowIndices(t, m.renderMenuBox())
	if !strings.Contains(lines[planIdx], "Wisp Deck") {
		t.Errorf("PLAN row should carry the wordmark, got %q", lines[planIdx])
	}
	if strings.Contains(lines[agentIdx], "Wisp Deck") {
		t.Errorf("AGENT row should no longer carry the wordmark, got %q", lines[agentIdx])
	}
}

// Mouse hit-testing maps the box-relative rows in the same order they render.
func TestHeaderOrder_rowIndicesFollowRenderOrder(t *testing.T) {
	m := subTestMenu("claude")
	if got := m.subscriptionRowIndex(); got != 1 {
		t.Errorf("subscriptionRowIndex() = %d, want 1 (directly under the top border)", got)
	}
	if got := m.titleRowIndex(); got != 2 {
		t.Errorf("titleRowIndex() = %d, want 2 (below the PLAN row)", got)
	}
}

// The focus ring walks the header top-to-bottom: PLAN → AGENT → tab bar.
func TestHeaderOrder_focusRingWalksPlanThenAgent(t *testing.T) {
	tests := []struct {
		name string
		from focusRegion
		key  tea.KeyType
		want focusRegion
	}{
		{"up from agent lands on plan", FocusAI, tea.KeyUp, FocusSubscription},
		{"down from plan lands on agent", FocusSubscription, tea.KeyDown, FocusAI},
		{"down from agent leaves the header", FocusAI, tea.KeyDown, FocusTabs},
		{"up from tabs lands on agent", FocusTabs, tea.KeyUp, FocusAI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := subFocusMenu(t, "claude", true)
			m.SetFocus(tt.from)
			m.Update(tea.KeyMsg{Type: tt.key})
			if m.Focus() != tt.want {
				t.Errorf("got %v, want %v", m.Focus(), tt.want)
			}
		})
	}
}
