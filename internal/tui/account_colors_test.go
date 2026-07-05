package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// acctColorMenu builds a two-account menu wired to a temp colors file seeded with
// a known dir→index mapping, so color assertions are deterministic.
func acctColorMenu(t *testing.T, colors string) *MainMenuModel {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	if err := os.WriteFile(list, []byte("Work:work\nPersonal:personal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude-account-colors"), []byte(colors), 0o644); err != nil {
		t.Fatal(err)
	}
	m := acctTestMenu("claude")
	m.SetClaudeAccountPaths(list, filepath.Join(dir, "accounts"))
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}, {Label: "Personal", Dir: "personal"}})
	return m
}

const seededColors = "default:141\nwork:78\npersonal:203\n"

// The top switcher row paints the active login's label in its own color.
func TestAccountColors_switcherRow_usesAccountColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := acctColorMenu(t, seededColors)
	m.SetActiveClaudeAccount("work")
	row := m.renderAccountRow("│", "│")
	want := lipgloss.NewStyle().Foreground(lipgloss.Color("78")).Render("Work")
	if !strings.Contains(row, want) {
		t.Fatalf("switcher row should paint Work in color 78:\n%q", row)
	}
}

// The Settings "Account" row paints the active login's value in its color.
func TestAccountColors_settingsRow_usesAccountColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := acctColorMenu(t, seededColors)
	m.SetActiveClaudeAccount("personal")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	want := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("[Personal]")
	if !strings.Contains(out, want) {
		t.Fatalf("settings Account row should paint [Personal] in color 203:\n%s", out)
	}
}

// The login-management panel paints each login's label in its own color — both
// the Default row and every managed row.
func TestAccountColors_loginPanel_usesPerAccountColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := acctColorMenu(t, seededColors)
	m.SetActiveClaudeAccount("work")
	m.openAccountMenu()
	// Move the keyboard cursor off every row so no row is overridden by the
	// cursor's bold-primary highlight — each label shows its own account color.
	m.accountMenuCursor = m.accountMenuAddRow()
	panel := m.renderAccountMenuPanel()

	work := lipgloss.NewStyle().Foreground(lipgloss.Color("78")).Render("Work")
	personal := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("Personal")
	def := lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Render("Default")
	if !strings.Contains(panel, work) {
		t.Errorf("panel should paint Work in 78:\n%s", panel)
	}
	if !strings.Contains(panel, personal) {
		t.Errorf("panel should paint Personal in 203:\n%s", panel)
	}
	if !strings.Contains(panel, def) {
		t.Errorf("panel should paint Default in 141:\n%s", panel)
	}
}
