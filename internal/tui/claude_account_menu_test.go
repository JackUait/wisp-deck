package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Enter on the focused LOGIN row opens the inline login-management panel (the
// view mirroring Plan's model-map panel), rather than bouncing straight out to
// the add-account flow.
func TestAccountMenu_enterOnLoginRowOpensPanel(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.focus = FocusAccount
	m.focusEnter()
	if !m.accountMenuOpen {
		t.Fatalf("Enter on LOGIN row should open the login panel")
	}
	if r := m.Result(); r != nil {
		t.Fatalf("opening the panel must not set a result, got %+v", r)
	}
}

// Enter on the Settings Login row (index 6) opens the same panel.
func TestAccountMenu_settingsEnterOpensPanel(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetActiveTab(TabSettings)
	m.settingsSelected = 6
	m.settingsEnter()
	if !m.accountMenuOpen {
		t.Fatalf("Enter on Settings Login row should open the login panel")
	}
}

// The panel lists Default, every managed login, and an add row.
func TestAccountMenu_renderListsLoginsAndAddRow(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
	})
	m.openAccountMenu()
	out := stripAnsi(m.renderAccountMenuPanel())
	for _, want := range []string{"Claude Logins", "Default", "Work", "Personal", "Add new login"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel missing %q:\n%s", want, out)
		}
	}
}

// The active login is flagged so the user can see which one is in effect.
func TestAccountMenu_marksActiveLogin(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.SetActiveClaudeAccount("work")
	m.openAccountMenu()
	out := stripAnsi(m.renderAccountMenuPanel())
	if !strings.Contains(out, "(active)") {
		t.Errorf("panel should flag the active login:\n%s", out)
	}
}

// ↓ then Enter switches the active login to the highlighted row and persists it.
func TestAccountMenu_navAndSwitchPersists(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
	})
	m.SetClaudeAccountFile(ptr)
	m.openAccountMenu() // cursor starts on Default (active)

	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})  // -> Work
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEnter}) // switch to Work

	if m.CurrentClaudeAccountDir() != "work" {
		t.Fatalf("Enter should switch active to work, got %q", m.CurrentClaudeAccountDir())
	}
	if m.accountMenuOpen {
		t.Errorf("switching should close the panel")
	}
	if b, _ := os.ReadFile(ptr); strings.TrimSpace(string(b)) != "work" {
		t.Errorf("pointer should persist 'work', got %q", string(b))
	}
}

// Enter on the add row exits with the add-account action (browser OAuth runs in
// wrapper.sh, not inside the alt-screen TUI).
func TestAccountMenu_addRowExitsWithAddAccount(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	// Walk down to the add row (Default, Work, Add).
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEnter})
	r := m.Result()
	if r == nil || r.Action != "add-account" {
		t.Fatalf("Enter on add row should add-account, got %+v", r)
	}
}

// 'a' adds a login from anywhere in the panel.
func TestAccountMenu_aKeyAddsAccount(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	r := m.Result()
	if r == nil || r.Action != "add-account" {
		t.Fatalf("'a' should add-account, got %+v", r)
	}
}

// 'd' then 'y' removes the highlighted managed login: its registry line and
// config dir are gone and the in-memory list shrinks.
func TestAccountMenu_deleteRemovesLogin(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	listFile := filepath.Join(accountsDir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listFile, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.SetClaudeAccountPaths(listFile, accountsDir)
	m.SetClaudeAccountFile(ptr)
	m.openAccountMenu()

	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})                      // -> Work
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}) // confirm
	if !m.accountMenuConfirm {
		t.Fatalf("'d' on a managed login should ask to confirm")
	}
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}) // remove

	if len(m.claudeAccounts) != 0 {
		t.Errorf("login list should be empty after removal, got %d", len(m.claudeAccounts))
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "work")); !os.IsNotExist(err) {
		t.Errorf("removed login's config dir should be gone")
	}
	if b, _ := os.ReadFile(listFile); strings.Contains(string(b), "work") {
		t.Errorf("registry should no longer list work, got %q", string(b))
	}
}

// Default can never be deleted — 'd' on it is a no-op (no confirm).
func TestAccountMenu_defaultNotDeletable(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu() // cursor on Default (0)
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.accountMenuConfirm {
		t.Errorf("Default login must not be deletable")
	}
}

// Esc closes the panel without leaving the menu.
func TestAccountMenu_escCloses(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEsc})
	if m.accountMenuOpen {
		t.Errorf("Esc should close the login panel")
	}
	if r := m.Result(); r != nil {
		t.Errorf("Esc should not set a result, got %+v", r)
	}
}
