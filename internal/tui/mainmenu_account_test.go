package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/models"
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
	if strings.Contains(out, "LOGIN") {
		t.Errorf("LOGIN row should be hidden with a single account:\n%s", out)
	}
}

// Once a managed account exists the LOGIN row appears at the very top — above
// the AGENT row — with cycle chevrons and as a reachable focus stop.
func TestAccountRow_shownAtTopWhenAccountsExist(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	if got := m.accountRowCount(); got != 1 {
		t.Fatalf("accountRowCount with 1 account: got %d, want 1", got)
	}
	if !m.accountFocusable() {
		t.Errorf("LOGIN row should be focusable with a managed account")
	}
	out := stripAnsi(m.renderMenuBox())
	loginIdx := strings.Index(out, "LOGIN")
	agentIdx := strings.Index(out, "AGENT")
	if loginIdx < 0 || agentIdx < 0 {
		t.Fatalf("LOGIN/AGENT rows missing:\n%s", out)
	}
	if !(loginIdx < agentIdx) {
		t.Errorf("LOGIN row must be above AGENT row (login=%d agent=%d)", loginIdx, agentIdx)
	}
	row := stripAnsi(m.renderAccountRow("│", "│"))
	if !strings.Contains(row, "Default") || !strings.Contains(row, "◂") || !strings.Contains(row, "▸") {
		t.Errorf("expected Default with chevrons: %q", row)
	}
}

// The 'L' key adds a native login regardless of whether the row is visible — it
// is the entry point for the first account (when the row is still hidden).
func TestAccount_LKeyTriggersAddAccount(t *testing.T) {
	m := acctTestMenu("claude") // no managed accounts → row hidden
	m.handleRune('L')
	r := m.Result()
	if r == nil || r.Action != "add-account" {
		t.Fatalf("'L' should set action add-account, got %+v", r)
	}
}

// Enter on the focused LOGIN row also adds a login (convenience once visible).
func TestAccountRow_enterTriggersAddAccount(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.focus = FocusAccount
	m.focusEnter()
	r := m.Result()
	if r == nil || r.Action != "add-account" {
		t.Fatalf("Enter on LOGIN row should set action add-account, got %+v", r)
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

func TestAccount_focusableOnlyWithAccounts(t *testing.T) {
	m := acctTestMenu("claude")
	if m.accountFocusable() {
		t.Errorf("should not be focusable with only Default")
	}
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	if !m.accountFocusable() {
		t.Errorf("should be focusable with one managed account (Default + Work)")
	}
}

// Up from the AI switcher reaches FocusAccount only when the row is shown.
func TestAccount_focusUpReachesAccountOnlyWhenShown(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAI {
		t.Errorf("Up from AI with no accounts should stay at FocusAI, got %v", m.Focus())
	}

	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.SetFocus(FocusAI)
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.Focus() != FocusAccount {
		t.Errorf("Up from AI with an account should reach FocusAccount, got %v", m.Focus())
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

// When the LOGIN row is shown it sits above the title and shifts the body down
// by one: top, LOGIN, title, subscription, switcher-gap, tab bar, separator,
// leading blank (8) → first project at row 8.
func TestMapRowToItem_accountRowShiftsBody(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	if got := m.MapRowToItem(7); got != -1 {
		t.Errorf("row 7 should be the leading blank (-1) with the LOGIN row present, got %d", got)
	}
	if got := m.MapRowToItem(8); got != 0 {
		t.Errorf("first project should be at row 8 with the LOGIN row, got %d", got)
	}
}
