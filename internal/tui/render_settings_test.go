package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderSettingsBox_hasTabBar(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "Settings") || !strings.Contains(out, "Projects") {
		t.Errorf("settings box missing tab bar: %q", out)
	}
}

func TestRenderSettingsBox_settingsTabAccented(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// Active tab is wrapped in bold accent brackets.
	want := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true).Render("[Settings]")
	if !strings.Contains(out, want) {
		t.Errorf("active Settings tab should be bracketed and bold, got:\n%s", out)
	}
}

func TestRenderSettingsBox_hasChromeStructure(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// Must have top border, title row, tab bar, separator
	for _, glyph := range []string{"╭", "╮", "╰", "╯", "│"} {
		if !strings.Contains(out, glyph) {
			t.Errorf("settings box missing border glyph %q:\n%s", glyph, out)
		}
	}
}

func TestRenderSettingsBox_preservesSettingsRows(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// Core settings items must still appear
	for _, label := range []string{"Mascot", "Tab title", "Idle sound"} {
		if !strings.Contains(out, label) {
			t.Errorf("settings box missing row %q:\n%s", label, out)
		}
	}
}

func TestRenderSettingsBox_hasSectionHeadersInOrder(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	headers := []string{"APPEARANCE", "NOTIFICATIONS", "PROJECTS", "ACCOUNT"}
	last := -1
	for _, h := range headers {
		idx := strings.Index(out, h)
		if idx < 0 {
			t.Fatalf("settings box missing section header %q:\n%s", h, out)
		}
		if idx <= last {
			t.Errorf("section header %q out of order:\n%s", h, out)
		}
		last = idx
	}
}

func TestRenderSettingsBox_appearanceItemsGroupedAboveNotifications(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	// Usage bars (an Appearance item) must render above Idle sound, which now
	// lives under the Notifications header further down.
	usage := strings.Index(out, "Usage bars")
	notif := strings.Index(out, "NOTIFICATIONS")
	idle := strings.Index(out, "Idle sound")
	if !(usage < notif && notif < idle) {
		t.Errorf("expected Usage bars < NOTIFICATIONS < Idle sound, got %d, %d, %d:\n%s",
			usage, notif, idle, out)
	}
}

func TestSettingsItemOrder_groupsAppearanceThenSections(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	got := m.settingsItemOrder()
	// Appearance, Tools, Notifications, Projects, Account.
	want := []int{0, 1, 3, 5, 9, 2, 4, 6, 7, 8}
	if len(got) != len(want) {
		t.Fatalf("settingsItemOrder len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("settingsItemOrder=%v want %v", got, want)
		}
	}
}

func TestSettingsStep_movesInVisualOrder(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.settingsSelected = 5 // Usage bars — last Appearance item
	m.settingsStep(1, false)
	if m.settingsSelected != rowAITools {
		t.Fatalf("down from Usage bars → %d, want %d (AI tools)", m.settingsSelected, rowAITools)
	}
	m.settingsStep(1, false)
	if m.settingsSelected != 2 {
		t.Fatalf("down from AI tools → %d, want 2 (Idle sound)", m.settingsSelected)
	}
	m.settingsStep(1, false)
	if m.settingsSelected != 4 {
		t.Fatalf("down from Idle sound → %d, want 4 (Projects folder)", m.settingsSelected)
	}
	m.settingsStep(-1, false)
	if m.settingsSelected != 2 {
		t.Fatalf("up from Projects folder → %d, want 2 (Idle sound)", m.settingsSelected)
	}
}

func TestSettingsStep_clampsAndWraps(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	// Clamp at the last visual item (Auto-switch, index 8).
	m.settingsSelected = 8
	m.settingsStep(1, false)
	if m.settingsSelected != 8 {
		t.Fatalf("down past last clamped to %d, want 8", m.settingsSelected)
	}
	// Wrap from last back to first (Mascot, index 0).
	m.settingsStep(1, true)
	if m.settingsSelected != 0 {
		t.Fatalf("wrap down from last → %d, want 0", m.settingsSelected)
	}
	// Wrap from first back to last.
	m.settingsStep(-1, true)
	if m.settingsSelected != 8 {
		t.Fatalf("wrap up from first → %d, want 8", m.settingsSelected)
	}
}
