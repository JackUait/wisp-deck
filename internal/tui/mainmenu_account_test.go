package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
)

func acctTestMenu(tool string) *MainMenuModel {
	projects := []models.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m := NewMainMenu(projects, []string{"claude", "opencode"}, tool, "animated")
	m.SetSize(100, 40)
	return m
}

// With only the Default login (no managed accounts) the LOGIN row is hidden
// entirely — a single-account user sees no extra switcher row.
func TestAccountRow_hiddenWhenOnlyDefault(t *testing.T) {
	m := acctTestMenu("claude")
	if got := m.accountRowCount(); got != 0 {
		t.Fatalf("accountRowCount with only Default: got %d, want 0", got)
	}
	if m.accountFocusable() {
		t.Errorf("LOGIN row should not be focusable when hidden")
	}
	out := stripAnsi(m.renderMenuBox())
	if strings.Contains(out, iconLogin) {
		t.Errorf("LOGIN row should be hidden with a single account:\n%s", out)
	}
}

// The top-of-page account switcher is removed: even with managed accounts the
// LOGIN switcher row never renders in the header (account switching lives in
// Settings › Account, the 'L' key, and the mid-session ledger pill instead). The
// AGENT row still renders and now hosts the wordmark.
func TestAccountRow_notShownEvenWithAccounts(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	if got := m.accountRowCount(); got != 0 {
		t.Fatalf("accountRowCount must be 0 (header switcher removed), got %d", got)
	}
	if m.accountFocusable() {
		t.Errorf("LOGIN header row must not be focusable (switcher removed)")
	}
	out := stripAnsi(m.renderMenuBox())
	if strings.Contains(out, iconLogin) {
		t.Errorf("LOGIN switcher must not render in the header:\n%s", out)
	}
	if !strings.Contains(out, iconAgent) {
		t.Errorf("AGENT row should still render:\n%s", out)
	}
}

// The 'L' key opens the login panel straight into the inline label input,
// regardless of whether the row is visible — it is the entry point for the first
// account (when the row is still hidden).
func TestAccount_LKeyOpensInlineInput(t *testing.T) {
	m := acctTestMenu("claude") // no managed accounts → row hidden
	m.handleRune('L')
	if !m.accountMenuOpen || !m.accountMenuInputMode {
		t.Fatalf("'L' should open the login panel in label-input mode (open=%v input=%v)", m.accountMenuOpen, m.accountMenuInputMode)
	}
	if r := m.Result(); r != nil {
		t.Fatalf("'L' should not set a result, got %+v", r)
	}
}

func TestAccount_setActiveByDir_andLabels(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
	})
	if m.CurrentClaudeAccountLabel() != "Default" || m.CurrentClaudeAccountDir() != "" {
		t.Fatalf("initial should be Default, got %q/%q", m.CurrentClaudeAccountLabel(), m.CurrentClaudeAccountDir())
	}
	m.SetActiveClaudeAccount("personal")
	if m.CurrentClaudeAccountLabel() != "Personal" || m.CurrentClaudeAccountDir() != "personal" {
		t.Errorf("got %q/%q", m.CurrentClaudeAccountLabel(), m.CurrentClaudeAccountDir())
	}
	// Unknown dir falls back to Default.
	m.SetActiveClaudeAccount("ghost")
	if m.CurrentClaudeAccountDir() != "" {
		t.Errorf("unknown dir should reset to Default, got %q", m.CurrentClaudeAccountDir())
	}
}

// The header account switcher is removed, so its LOGIN row is never a reachable
// focus stop — not even with managed accounts.
func TestAccount_neverFocusableInHeader(t *testing.T) {
	m := acctTestMenu("claude")
	if m.accountFocusable() {
		t.Errorf("should not be focusable with only Default")
	}
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	if m.accountFocusable() {
		t.Errorf("header LOGIN switcher removed: must never be focusable, even with accounts")
	}
}

// Up from the AI switcher never reaches an account row (the header switcher was
// removed), so focus stays at FocusAI even with managed accounts.
func TestAccount_focusUpFromAIStaysAtAI(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AI with no accounts should stay at FocusAI, got %v", m.Focus())
	}

	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AI must stay at FocusAI even with an account (header switcher removed), got %v", m.Focus())
	}
}

// Cycling walks Default → managed accounts → Default and persists the active
// dir to the pointer file (Default removes the pointer).
func TestAccount_cyclePersistsPointer(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
	})
	m.SetClaudeAccountFile(ptr)

	m.CycleAccount("next") // Default -> Work
	if got := m.CurrentClaudeAccountDir(); got != "work" {
		t.Fatalf("after next: got %q, want work", got)
	}
	if b, _ := os.ReadFile(ptr); strings.TrimSpace(string(b)) != "work" {
		t.Errorf("pointer should be 'work', got %q", string(b))
	}

	m.CycleAccount("next") // Work -> Personal
	m.CycleAccount("next") // Personal -> Default (wrap)
	if got := m.CurrentClaudeAccountDir(); got != "" {
		t.Fatalf("after wrap: got %q, want Default", got)
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Errorf("Default should remove the pointer file")
	}

	m.CycleAccount("prev") // Default -> Personal (wrap back)
	if got := m.CurrentClaudeAccountDir(); got != "personal" {
		t.Errorf("after prev wrap: got %q, want personal", got)
	}
}

// The Settings tab shows a "Account" row (the account-management entry point),
// always present even with only the Default login.
func TestSettingsLoginRow_shown(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetActiveTab(TabSettings)
	out := stripAnsi(m.renderSettingsBox())
	if !strings.Contains(out, "Account") {
		t.Errorf("settings box missing 'Login' row:\n%s", out)
	}
	if !strings.Contains(out, "Default") {
		t.Errorf("Login row should show the active account (Default):\n%s", out)
	}
}

// Enter on the Settings Login row opens the login-management panel.
func TestSettingsLoginRow_enterOpensPanel(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = m.loginRowIndex()
	m.settingsEnter()
	if !m.accountMenuOpen {
		t.Fatalf("Enter on Login settings row should open the login panel")
	}
}

// ←→ on the Settings Login row cycles the active account.
func TestSettingsLoginRow_cyclesAccount(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.SetActiveTab(TabSettings)
	m.settingsSelected = m.loginRowIndex()
	m.settingsValueRight()
	if m.CurrentClaudeAccountDir() != "work" {
		t.Errorf("→ on Login row should switch to work, got %q", m.CurrentClaudeAccountDir())
	}
	m.settingsValueLeft()
	if m.CurrentClaudeAccountDir() != "" {
		t.Errorf("← on Login row should switch back to Default, got %q", m.CurrentClaudeAccountDir())
	}
}

// When the LOGIN row is shown it sits above the title and shifts the body down
// by one: top, LOGIN, title, subscription, switcher-gap, tab bar, separator,
// leading blank (8) → first project at row 8.
// With the header account switcher removed, managed accounts no longer shift the
// body down — the row-to-item mapping is identical with or without them.
func TestMapRowToItem_accountsDoNotShiftBody(t *testing.T) {
	m := acctTestMenu("claude")
	base7 := m.MapRowToItem(7)
	base8 := m.MapRowToItem(8)
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	if got := m.MapRowToItem(7); got != base7 {
		t.Errorf("account must not shift row 7 mapping: got %d, want %d", got, base7)
	}
	if got := m.MapRowToItem(8); got != base8 {
		t.Errorf("account must not shift row 8 mapping: got %d, want %d", got, base8)
	}
}
