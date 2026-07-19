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

// The main-page subscription switcher was removed, so Claude's header no longer
// carries a PLAN row and the Claude and non-Claude menus are the same height.
func TestCalculateLayout_noSubscriptionRowHeight(t *testing.T) {
	lc := subTestMenu("claude").CalculateLayout(120, 50)
	lx := subTestMenu("opencode").CalculateLayout(120, 50)
	if lc.MenuHeight != lx.MenuHeight {
		t.Errorf("claude menu height %d should equal opencode height %d now the PLAN row is gone", lc.MenuHeight, lx.MenuHeight)
	}
}
