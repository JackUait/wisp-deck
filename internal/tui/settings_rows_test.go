package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

func rowTestMenu() *MainMenuModel {
	return NewMainMenu([]models.Project{{Name: "p", Path: "/p"}},
		[]string{"claude", "opencode"}, "claude", "none")
}

// Settings rows are keyed by integer index. Naming them keeps the render, the
// key handlers and settingsSections from drifting apart — previously
// settingsEnter hardcoded `case 7` for Account while loginRowIndex() computed
// settingsItemCount()-2, an equality that held only by coincidence.
func TestSettingsRowConstants_are_distinct_and_contiguous(t *testing.T) {
	rows := map[string]int{
		"mascot":         rowMascot,
		"tabTitle":       rowTabTitle,
		"idleSound":      rowIdleSound,
		"theme":          rowTheme,
		"projectsFolder": rowProjectsFolder,
		"usageBars":      rowUsageBars,
		"subscription":   rowSubscription,
		"account":        rowAccount,
		"autoSwitch":     rowAutoSwitch,
		"aiTools":        rowAITools,
	}
	seen := map[int]string{}
	max := -1
	for name, idx := range rows {
		if other, dup := seen[idx]; dup {
			t.Errorf("rows %q and %q share index %d", name, other, idx)
		}
		seen[idx] = name
		if idx > max {
			max = idx
		}
	}
	m := rowTestMenu()
	if got := m.settingsItemCount(); got != max+1 {
		t.Errorf("settingsItemCount() = %d, want %d (highest row index + 1)", got, max+1)
	}
}

// The accessors must agree with the constants; this is the collision that
// silently made ↵ on Account toggle Auto-switch when a row was appended.
func TestSettingsRowAccessors_match_constants(t *testing.T) {
	m := rowTestMenu()
	if got := m.loginRowIndex(); got != rowAccount {
		t.Errorf("loginRowIndex() = %d, want rowAccount (%d)", got, rowAccount)
	}
	if got := m.autoSwitchRowIndex(); got != rowAutoSwitch {
		t.Errorf("autoSwitchRowIndex() = %d, want rowAutoSwitch (%d)", got, rowAutoSwitch)
	}
}

// Regression: ↵ on the Account row opens the login panel, not the auto-switch
// toggle. This is the exact breakage adding a tenth row would have caused.
func TestSettingsEnter_on_account_row_opens_account_panel(t *testing.T) {
	m := rowTestMenu()
	m.settingsSelected = m.loginRowIndex()
	before := m.AutoSwitchEnabled()

	m.settingsEnter()

	if !m.accountMenuOpen {
		t.Error("↵ on the Account row must open the account panel")
	}
	if m.AutoSwitchEnabled() != before {
		t.Error("↵ on the Account row must not toggle auto-switch")
	}
}

func TestSettingsSections_include_a_Tools_section_with_the_ai_tools_row(t *testing.T) {
	m := rowTestMenu()
	var found bool
	for _, s := range m.settingsSections() {
		if s.title != "Tools" {
			continue
		}
		found = true
		if len(s.indices) != 1 || s.indices[0] != rowAITools {
			t.Errorf("Tools section indices = %v, want [%d]", s.indices, rowAITools)
		}
	}
	if !found {
		t.Fatalf("no Tools section in %v", m.settingsSections())
	}
}

// Every row index must be reachable through the visual order, or a row renders
// but cannot be focused.
func TestSettingsItemOrder_covers_every_row(t *testing.T) {
	m := rowTestMenu()
	order := m.settingsItemOrder()
	if len(order) != m.settingsItemCount() {
		t.Errorf("settingsItemOrder() has %d entries, want %d", len(order), m.settingsItemCount())
	}
	if m.settingsVisualPos(rowAITools) < 0 {
		t.Error("rowAITools is not reachable in the visual order")
	}
}

// The settings help row is chosen by row index. It previously used
// settingsItemCount()-1, which silently retargets whenever a row is appended —
// the same arithmetic-index bug the constants exist to prevent.
func TestSettingsHelpRow_matches_the_focused_row(t *testing.T) {
	cases := []struct {
		row  int
		want string
	}{
		{rowProjectsFolder, "⏎ edit"},
		{rowAITools, "⏎ manage"},
		{rowAccount, "⏎ manage"},
		{rowMascot, "←→ cycle"},
		{rowTheme, "←→ cycle"},
	}
	for _, tc := range cases {
		m := rowTestMenu()
		m.SetActiveTab(TabSettings)
		m.settingsSelected = tc.row
		out := m.renderSettingsBox()
		if !strings.Contains(out, tc.want) {
			t.Errorf("row %d: help row should offer %q:\n%s", tc.row, tc.want, out)
		}
	}
}

// The AI tools row is opened with ⏎, never cycled with ←→.
func TestSettingsHelpRow_ai_tools_does_not_advertise_cycling(t *testing.T) {
	m := rowTestMenu()
	m.SetActiveTab(TabSettings)
	m.settingsSelected = rowAITools
	if out := m.renderSettingsBox(); strings.Contains(out, "←→ cycle") {
		t.Errorf("AI tools row must not advertise ←→ cycle:\n%s", out)
	}
}
