package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/muesli/termenv"
)

func TestSubscriptionRow_standardIsPrimary(t *testing.T) {
	// Force a real color profile so the foreground color is emitted.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := subTestMenu("claude") // Standard Claude (no custom config)
	row := m.renderSubscriptionRow("│", "│")
	name := m.CurrentClaudeConfigName()
	want := lipgloss.NewStyle().Foreground(m.theme.Primary).Render(name)
	if !strings.Contains(row, want) {
		t.Errorf("standard subscription name should be orange (Primary), got: %q", row)
	}
}

func subTestMenu(tool string) *MainMenuModel {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "opencode"}, tool, "animated")
	m.SetSize(100, 40)
	return m
}

// The Claude plan/config settings row is labelled "Subscription" (clearer than
// the older "Config"/"Plan" names).
func TestSettings_PlanLabel(t *testing.T) {
	m := subTestMenu("claude")
	m.SetActiveTab(TabSettings)
	out := stripAnsi(m.renderSettingsBox())
	if !strings.Contains(out, "Subscription") {
		t.Errorf("settings box missing 'Subscription' row:\n%s", out)
	}
	if strings.Contains(out, "Config") {
		t.Errorf("settings box still shows old 'Config' label:\n%s", out)
	}
}

// The current subscription is shown on the main page (Claude only).
func TestMainPage_ShowsSubscription_Claude(t *testing.T) {
	m := subTestMenu("claude")
	out := stripAnsi(m.renderMenuBox())
	if !strings.Contains(out, "Standard Claude") {
		t.Errorf("main page missing current subscription:\n%s", out)
	}
}

func TestMainPage_ShowsActiveSubscriptionName(t *testing.T) {
	m := subTestMenu("claude")
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "Work", File: "work.json"}})
	m.SetActiveClaudeConfig("work.json")
	out := stripAnsi(m.renderMenuBox())
	if !strings.Contains(out, "Work") {
		t.Errorf("main page missing active subscription 'Work':\n%s", out)
	}
}

// The PLAN line names a Claude subscription, so it is hidden for other agents.
func TestMainPage_HidesSubscription_NonClaude(t *testing.T) {
	m := subTestMenu("opencode")
	out := stripAnsi(m.renderMenuBox())
	if strings.Contains(out, "Standard Claude") {
		t.Errorf("non-claude main page should not show the subscription line:\n%s", out)
	}
}

// The subscription row shifts the project rows down by one; click mapping and
// the layout height must stay in sync.
func TestMapRowToItem_accountsForSubscriptionRow(t *testing.T) {
	// Header rows: top, subscription, title, switcher-gap, tab bar, separator,
	// leading blank(6) — so the first project lands at row 7 under Claude.
	// Asserting row 6 maps to -1 (and row 7 to item 0) is what makes this catch a
	// regression: without the subscription row the first project would sit at row 6.
	m := subTestMenu("claude")
	if got := m.MapRowToItem(6); got != -1 {
		t.Errorf("row 6 should be the leading blank (-1) once the subscription row is present, got %d", got)
	}
	if got := m.MapRowToItem(7); got != 0 {
		t.Errorf("first project should be at row 7, MapRowToItem(7)=%d", got)
	}
}

// Claude's header carries one extra row (the subscription line), so its menu is
// exactly one line taller than a non-Claude agent's.
func TestCalculateLayout_subscriptionRowAddsHeightForClaudeOnly(t *testing.T) {
	lc := subTestMenu("claude").CalculateLayout(120, 50)
	lx := subTestMenu("opencode").CalculateLayout(120, 50)
	if lc.MenuHeight != lx.MenuHeight+1 {
		t.Errorf("claude menu height %d should be one more than opencode height %d (subscription row)", lc.MenuHeight, lx.MenuHeight)
	}
}
