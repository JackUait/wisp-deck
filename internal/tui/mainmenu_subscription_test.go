package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func subTestMenu(tool string) *MainMenuModel {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "codex"}, tool, "animated")
	m.SetSize(100, 40)
	return m
}

// The settings row formerly labelled "Config" is now "Subscription".
func TestSettings_SubscriptionLabelRenamed(t *testing.T) {
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

// Non-Claude tools have no subscription, so the line is hidden.
func TestMainPage_NoSubscription_NonClaude(t *testing.T) {
	m := subTestMenu("codex")
	out := stripAnsi(m.renderMenuBox())
	if strings.Contains(out, "Standard Claude") {
		t.Errorf("non-claude main page should not show a subscription line:\n%s", out)
	}
}

// The subscription row shifts the project rows down by one; click mapping and
// the layout height must stay in sync.
func TestMapRowToItem_accountsForSubscriptionRow(t *testing.T) {
	mClaude := subTestMenu("claude")
	if got := mClaude.MapRowToItem(6); got != 0 {
		t.Errorf("claude: first project should be at row 6, MapRowToItem(6)=%d", got)
	}

	mCodex := subTestMenu("codex")
	if got := mCodex.MapRowToItem(5); got != 0 {
		t.Errorf("codex: first project should be at row 5, MapRowToItem(5)=%d", got)
	}
}

func TestCalculateLayout_subscriptionRowAddsHeight(t *testing.T) {
	mClaude := subTestMenu("claude")
	mCodex := subTestMenu("codex")
	lc := mClaude.CalculateLayout(120, 50)
	lx := mCodex.CalculateLayout(120, 50)
	if lc.MenuHeight != lx.MenuHeight+1 {
		t.Errorf("claude menu height %d should be codex height %d + 1 (subscription row)", lc.MenuHeight, lx.MenuHeight)
	}
}
