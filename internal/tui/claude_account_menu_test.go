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

// Enter on the add row opens the inline label input (no TUI exit, no result).
func TestAccountMenu_addRowOpensInlineInput(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	// Walk down to the add row (Default, Work, Add).
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.accountMenuInputMode {
		t.Fatalf("Enter on add row should open the inline label input")
	}
	if !m.accountMenuOpen {
		t.Errorf("the panel should stay open while entering a label")
	}
	if r := m.Result(); r != nil {
		t.Errorf("opening the input must not set a result, got %+v", r)
	}
}

// 'a' opens the inline label input from anywhere in the panel.
func TestAccountMenu_aKeyOpensInlineInput(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !m.accountMenuInputMode {
		t.Fatalf("'a' should open the inline label input")
	}
}

// Typing a label and pressing Enter registers the login in-process (dir + list
// entry) and exits with the login-account action carrying the new dir, so
// wrapper.sh only runs the browser `claude auth login`.
func TestAccountMenu_inlineSubmitRegistersAndExitsForLogin(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	listFile := filepath.Join(accountsDir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")

	m := acctTestMenu("claude")
	m.SetClaudeAccountPaths(listFile, accountsDir)
	m.SetClaudeAccountFile(ptr)
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}) // input mode
	m.accountMenuInput.SetValue("Work")
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEnter})

	r := m.Result()
	if r == nil || r.Action != "login-account" {
		t.Fatalf("submit should exit with login-account, got %+v", r)
	}
	if r.AccountDir != "work" {
		t.Errorf("result should carry the new dir 'work', got %q", r.AccountDir)
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "work")); err != nil {
		t.Errorf("the account config dir should have been created: %v", err)
	}
	if b, _ := os.ReadFile(listFile); !strings.Contains(string(b), "Work:work") {
		t.Errorf("registry should list the new login, got %q", string(b))
	}
}

// Esc in the label input cancels back to the list without registering anything.
func TestAccountMenu_inlineInputEscCancels(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	listFile := filepath.Join(accountsDir, "claude-accounts.list")
	m := acctTestMenu("claude")
	m.SetClaudeAccountPaths(listFile, accountsDir)
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.accountMenuInput.SetValue("Work")
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEsc})

	if m.accountMenuInputMode {
		t.Errorf("Esc should leave input mode")
	}
	if !m.accountMenuOpen {
		t.Errorf("Esc from the input should return to the login list, not close the panel")
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "work")); !os.IsNotExist(err) {
		t.Errorf("cancelling must not register an account")
	}
}

// The label input renders inside the panel.
func TestAccountMenu_inlineInputRenders(t *testing.T) {
	m := acctTestMenu("claude")
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	out := stripAnsi(m.renderAccountMenuPanel())
	if !strings.Contains(out, "New login") {
		t.Errorf("input mode should render the new-login field:\n%s", out)
	}
}

// 'r' on a managed login opens the inline rename field, prefilled with its
// current label.
func TestAccountMenu_rKeyOpensRenameInput(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown}) // -> Work
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if !m.accountMenuInputMode {
		t.Fatalf("'r' on a managed login should open the rename input")
	}
	if m.accountMenuInput.Value() != "Work" {
		t.Errorf("rename field should be prefilled with the current label, got %q", m.accountMenuInput.Value())
	}
}

// 'r' on Default is a no-op — the implicit login has no editable label.
func TestAccountMenu_rKeyOnDefaultNoop(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu() // cursor on Default (0)
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.accountMenuInputMode {
		t.Errorf("Default login must not be renamable")
	}
}

// Editing the label and pressing Enter renames the login in-process and stays in
// the panel (no browser login, no menu exit).
func TestAccountMenu_renameSubmitUpdatesLabel(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	listFile := filepath.Join(accountsDir, "claude-accounts.list")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listFile, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.SetClaudeAccountPaths(listFile, accountsDir)
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})                      // -> Work
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // rename input
	m.accountMenuInput.SetValue("Day Job")
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyEnter})

	if m.accountMenuInputMode {
		t.Errorf("submitting the rename should leave input mode")
	}
	if !m.accountMenuOpen {
		t.Errorf("rename should stay in the panel, not exit the menu")
	}
	if r := m.Result(); r != nil {
		t.Errorf("rename is in-process; it must not set a result, got %+v", r)
	}
	if len(m.claudeAccounts) != 1 || m.claudeAccounts[0].Label != "Day Job" || m.claudeAccounts[0].Dir != "work" {
		t.Errorf("in-memory list should show the new label on the same dir, got %+v", m.claudeAccounts)
	}
	if b, _ := os.ReadFile(listFile); !strings.Contains(string(b), "Day Job:work") {
		t.Errorf("registry should persist the rename, got %q", string(b))
	}
}

// The rename field is clearly labelled as a rename (not an add).
func TestAccountMenu_renameInputRenders(t *testing.T) {
	m := acctTestMenu("claude")
	m.SetClaudeAccounts([]ClaudeAccount{{Label: "Work", Dir: "work"}})
	m.openAccountMenu()
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyDown})
	m.updateAccountMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	out := stripAnsi(m.renderAccountMenuPanel())
	if !strings.Contains(out, "Rename") {
		t.Errorf("rename mode should render a rename field:\n%s", out)
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
