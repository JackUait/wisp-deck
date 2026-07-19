package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// newUnifiedModalMenu builds a subscription-modal menu that also has managed
// Claude logins (registered on disk), so the unified profiles pane shows both
// sections and login mutations have a real registry to work on.
func newUnifiedModalMenu(t *testing.T) *MainMenuModel {
	t.Helper()
	m := newSubscriptionModalMenu(t)
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	accountsDir := filepath.Join(dir, "claude-accounts")
	for _, label := range []string{"Work", "Personal"} {
		if _, err := claudeaccount.Add(list, accountsDir, label); err != nil {
			t.Fatal(err)
		}
	}
	m.SetClaudeAccountFile(filepath.Join(dir, "claude-account"))
	m.SetClaudeAccountPaths(list, accountsDir)
	m.SetClaudeDefaultLabelFile(filepath.Join(dir, "claude-account-default-label"))
	m.SetClaudeAccounts(LoadClaudeAccountsList(list))
	return m
}

// The profiles pane's row model appends the logins section after the
// subscription rows: subs, + Add profile, Default + managed logins, + Add login.
func TestSubscriptionModal_rowModelIncludesLogins(t *testing.T) {
	m := newUnifiedModalMenu(t)
	subs := len(m.subscriptionProfiles()) // Standard + 3 configs = 4
	logins := m.subscriptionLoginRows()
	if len(logins) != 3 {
		t.Fatalf("login rows = %d, want Default plus 2 managed", len(logins))
	}
	if logins[0].Dir != "" || logins[1].Label != "Work" || logins[2].Label != "Personal" {
		t.Fatalf("login rows = %+v", logins)
	}
	if got := m.subscriptionLoginRowStart(); got != subs+1 {
		t.Errorf("login start = %d, want %d", got, subs+1)
	}
	if got := m.subscriptionAddLoginRow(); got != subs+4 {
		t.Errorf("add-login row = %d, want %d", got, subs+4)
	}
	if got := m.subscriptionLastRow(); got != m.subscriptionAddLoginRow() {
		t.Errorf("last row = %d, want add-login row", got)
	}
}

// The active login carries an Active marker independent of the active
// subscription.
func TestSubscriptionModal_loginRowsMarkActiveLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.SetActiveClaudeAccount("personal")
	logins := m.subscriptionLoginRows()
	if logins[0].Active || logins[1].Active || !logins[2].Active {
		t.Fatalf("active flags = %v %v %v, want only Personal",
			logins[0].Active, logins[1].Active, logins[2].Active)
	}
}

// The profiles pane renders both sections with their add rows.
func TestSubscriptionModal_profilePaneShowsLoginSection(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	out := stripAnsi(strings.Join(m.subscriptionProfileLines(subscriptionListWidth, 16), "\n"))
	for _, want := range []string{
		"SUBSCRIPTIONS", "Standard Claude", "+ Add profile",
		"LOGINS", "Default", "Work", "Personal", "+ Add login",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("profiles pane missing %q:\n%s", want, out)
		}
	}
}

// The add-profile detection must not fire on the login rows that now sit past it.
func TestSubscriptionModal_onAddRowOnlyAtAddProfile(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = len(m.subscriptionProfiles())
	if !m.subscriptionModalOnAddRow() {
		t.Fatal("cursor on + Add profile must report the add row")
	}
	m.subscriptionModal.profileCursor = m.subscriptionLoginRowStart()
	if m.subscriptionModalOnAddRow() {
		t.Fatal("cursor on a login row must NOT report the subscription add row")
	}
	if !m.subscriptionModalOnLoginRow() {
		t.Fatal("cursor on first login row must report a login row")
	}
	if got := m.subscriptionModalLoginIndex(); got != 0 {
		t.Fatalf("login index = %d, want 0 (Default)", got)
	}
	m.subscriptionModal.profileCursor = m.subscriptionAddLoginRow()
	if !m.subscriptionModalOnAddLoginRow() {
		t.Fatal("cursor on + Add login must report the add-login row")
	}
}

// Arrow keys walk from the subscriptions through the logins and clamp on the
// add-login row.
func TestSubscriptionModal_cursorWalksIntoLogins(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	for i := 0; i < 20; i++ {
		m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := m.subscriptionModal.profileCursor; got != m.subscriptionAddLoginRow() {
		t.Fatalf("cursor = %d, want clamp at add-login row %d", got, m.subscriptionAddLoginRow())
	}
}

// putCursorOnLogin moves the profiles-pane cursor onto login row idx.
func putCursorOnLogin(m *MainMenuModel, idx int) {
	m.subscriptionModal.profileCursor = m.subscriptionLoginRowStart() + idx
	m.subscriptionModal.draft = subscriptionDraft{}
}

// Enter on a login row (wide layout) switches the active login and persists
// the pointer, keeping the modal open.
func TestSubscriptionModal_enterOnLoginRowSwitchesLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 2) // Personal
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.selectedAccount != 2 {
		t.Fatalf("selectedAccount = %d, want 2 (Personal)", m.selectedAccount)
	}
	data, err := os.ReadFile(m.claudeAccountFile)
	if err != nil {
		t.Fatalf("pointer file not written: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "personal" {
		t.Fatalf("pointer = %q, want personal", got)
	}
	if !m.subscriptionModal.open {
		t.Fatal("modal must stay open after switching login")
	}
}

// 'u' works on login rows too, mirroring the subscription Use shortcut.
func TestSubscriptionModal_useKeySwitchesLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1) // Work
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.selectedAccount != 1 {
		t.Fatalf("selectedAccount = %d, want 1 (Work)", m.selectedAccount)
	}
}

// 'r' on a managed login renames it in the registry.
func TestSubscriptionModal_renameManagedLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1) // Work
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.subscriptionModal.mode != subscriptionRename {
		t.Fatalf("mode = %v, want subscriptionRename", m.subscriptionModal.mode)
	}
	m.subscriptionModal.input.SetValue("Office")
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	logins := m.subscriptionLoginRows()
	if logins[1].Label != "Office" {
		t.Fatalf("login label = %q, want Office", logins[1].Label)
	}
	data, _ := os.ReadFile(m.claudeAccountsList)
	if !strings.Contains(string(data), "Office:work") {
		t.Fatalf("registry not renamed:\n%s", data)
	}
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("mode after rename = %v, want browse", m.subscriptionModal.mode)
	}
}

// 'r' on the Default row renames the implicit Default label.
func TestSubscriptionModal_renameDefaultLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m.subscriptionModal.input.SetValue("Main")
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.DefaultAccountLabel(); got != "Main" {
		t.Fatalf("Default label = %q, want Main", got)
	}
	data, err := os.ReadFile(m.claudeDefaultLabelFile)
	if err != nil || strings.TrimSpace(string(data)) != "Main" {
		t.Fatalf("default label file = %q err %v, want Main", data, err)
	}
}

// 'd' on a managed login asks for confirmation, then removes it.
func TestSubscriptionModal_deleteManagedLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 2) // Personal
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.subscriptionModal.mode != subscriptionDeleteConfirm {
		t.Fatalf("mode = %v, want subscriptionDeleteConfirm", m.subscriptionModal.mode)
	}
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	if got := len(m.subscriptionLoginRows()); got != 2 {
		t.Fatalf("login rows after delete = %d, want 2 (Default + Work)", got)
	}
	data, _ := os.ReadFile(m.claudeAccountsList)
	if strings.Contains(string(data), "personal") {
		t.Fatalf("registry still lists personal:\n%s", data)
	}
}

// The Default login is implicit and can never be deleted.
func TestSubscriptionModal_deleteDefaultLoginIsNoop(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 0)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Fatalf("mode = %v, want browse (Default is not deletable)", m.subscriptionModal.mode)
	}
}

// Enter on + Add login opens the label input; submitting registers the login
// and makes it active.
func TestSubscriptionModal_addLoginFlow(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = m.subscriptionAddLoginRow()
	m.subscriptionModal.draft = subscriptionDraft{}
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.subscriptionModal.mode != subscriptionLoginName {
		t.Fatalf("mode = %v, want subscriptionLoginName", m.subscriptionModal.mode)
	}
	m.subscriptionModal.input.SetValue("Team")
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	logins := m.subscriptionLoginRows()
	if len(logins) != 4 || logins[3].Label != "Team" {
		t.Fatalf("login rows after add = %+v, want Team appended", logins)
	}
	if !logins[3].Active {
		t.Fatal("new login must become the active login")
	}
	if _, err := os.Stat(filepath.Join(m.claudeAccountsDir, logins[3].Dir)); err != nil {
		t.Fatalf("account config dir missing: %v", err)
	}
	if got := m.subscriptionModal.profileCursor; got != m.subscriptionLoginRowStart()+3 {
		t.Fatalf("cursor = %d, want the new login row %d", got, m.subscriptionLoginRowStart()+3)
	}
}

// The details pane for a login shows its actions; Delete only for managed.
func TestSubscriptionModal_loginDetailPaneActions(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.SetActiveClaudeAccount("work")
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1) // Work
	out := stripAnsi(strings.Join(m.subscriptionDetailLines(m.subscriptionDetailPaneWidth(), 16), "\n"))
	for _, want := range []string{"LOGIN", "Work", "[ Use ]", "[ Rename ]", "[ Delete ]", "Active"} {
		if !strings.Contains(out, want) {
			t.Errorf("login detail pane missing %q:\n%s", want, out)
		}
	}
	putCursorOnLogin(m, 0) // Default
	out = stripAnsi(strings.Join(m.subscriptionDetailLines(m.subscriptionDetailPaneWidth(), 16), "\n"))
	if strings.Contains(out, "[ Delete ]") {
		t.Errorf("Default login must not offer Delete:\n%s", out)
	}
}

// The details pane for + Add login invites creating one.
func TestSubscriptionModal_addLoginDetailPane(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = m.subscriptionAddLoginRow()
	m.subscriptionModal.draft = subscriptionDraft{}
	out := stripAnsi(strings.Join(m.subscriptionDetailLines(m.subscriptionDetailPaneWidth(), 16), "\n"))
	for _, want := range []string{"ADD LOGIN", "[ Create login ]"} {
		if !strings.Contains(out, want) {
			t.Errorf("add-login detail pane missing %q:\n%s", want, out)
		}
	}
}

// Clicking a login row moves the cursor (preview) without switching the login.
func TestSubscriptionModalMouse_clickLoginRowPreviews(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	x, y := subscriptionCardCell(t, m, "Personal")
	if target := m.subscriptionModalTarget(x, y); target.kind != subscriptionHitLogin || target.index != 2 {
		t.Fatalf("login target = %+v, want login index 2", target)
	}
	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)
	if got.subscriptionModal.profileCursor != got.subscriptionLoginRowStart()+2 {
		t.Fatalf("click cursor = %d, want login row %d",
			got.subscriptionModal.profileCursor, got.subscriptionLoginRowStart()+2)
	}
	if got.selectedAccount != 0 {
		t.Fatalf("login click switched account to %d without Use", got.selectedAccount)
	}
}

// Clicking + Add login opens the label input.
func TestSubscriptionModalMouse_clickAddLoginOpensInput(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	x, y := subscriptionCardCell(t, m, "+ Add login")
	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)
	if got.subscriptionModal.mode != subscriptionLoginName {
		t.Fatalf("mode = %v, want subscriptionLoginName", got.subscriptionModal.mode)
	}
}

// Clicking [ Use ] in a login's details switches the active login.
func TestSubscriptionModalMouse_clickUseSwitchesLogin(t *testing.T) {
	m := newUnifiedModalMenu(t)
	m.openSubscriptionModal()
	putCursorOnLogin(m, 1) // Work
	x, y := subscriptionCardCell(t, m, "[ Use ]")
	if target := m.subscriptionModalTarget(x, y); target.kind != subscriptionHitUse {
		t.Fatalf("[ Use ] target = %+v, want subscriptionHitUse", target)
	}
	updated, _ := m.Update(subscriptionScreenMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	got := updated.(*MainMenuModel)
	if got.selectedAccount != 1 {
		t.Fatalf("selectedAccount = %d, want 1 (Work)", got.selectedAccount)
	}
}
