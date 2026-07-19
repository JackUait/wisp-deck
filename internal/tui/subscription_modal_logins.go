package tui

import (
	"errors"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
)

// Login rows of the unified subscription modal: the LOGINS section shares the
// profiles pane and lifecycle modes with the subscription profiles, but its
// actions mutate the Claude login registry (internal/claudeaccount) instead of
// the subscription config files.

// subscriptionLoginActions lists the details-pane action rows for the login
// under the cursor. The implicit Default login cannot be deleted.
func (m *MainMenuModel) subscriptionLoginActions() []int {
	actions := []int{subscriptionDetailUse, subscriptionDetailRename}
	if m.subscriptionModalLoginIndex() >= 1 {
		actions = append(actions, subscriptionDetailDelete)
	}
	return actions
}

// subscriptionModalLoginName is the display label of the login under the
// cursor (used by the delete confirmation).
func (m *MainMenuModel) subscriptionModalLoginName() string {
	rows := m.subscriptionLoginRows()
	idx := m.subscriptionModalLoginIndex()
	if idx < 0 || idx >= len(rows) {
		return ""
	}
	return rows[idx].Label
}

// useSubscriptionLogin switches the active Claude login to the row under the
// cursor and persists the pointer; the modal stays open.
func (m *MainMenuModel) useSubscriptionLogin() {
	idx := m.subscriptionModalLoginIndex()
	if idx < 0 || idx > len(m.claudeAccounts) {
		return
	}
	m.selectedAccount = idx
	m.persistClaudeAccount()
	m.subscriptionModal.err = nil
}

// startSubscriptionLoginAdd opens the label input for a new login.
func (m *MainMenuModel) startSubscriptionLoginAdd() tea.Cmd {
	input := textinput.New()
	input.Width = 36
	input.Placeholder = "Work, Personal…"
	input.Focus()
	m.subscriptionModal.input = input
	m.enterSubscriptionLifecycle(subscriptionLoginName)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
	return textinput.Blink
}

// addSubscriptionLogin registers the login in-process (config dir + registry
// entry) and makes it active. It deliberately does NOT run `claude auth login`:
// the isolated CLAUDE_CONFIG_DIR starts empty, so the user is signed in the
// first time Claude launches under this login.
func (m *MainMenuModel) addSubscriptionLogin(label string) {
	if m.claudeAccountsList == "" || m.claudeAccountsDir == "" {
		m.subscriptionModal.err = errors.New("login storage is not configured")
		return
	}
	dir, err := claudeaccount.Add(m.claudeAccountsList, m.claudeAccountsDir, label)
	if err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.SetActiveClaudeAccount(dir)
	m.persistClaudeAccount()
	m.subscriptionModal.profileCursor = m.subscriptionLoginRowStart() + m.selectedAccount
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.pane = subscriptionProfilesPane
	m.subscriptionModal.input.Blur()
	m.subscriptionModal.err = nil
	m.ensureSubscriptionProfileVisible()
}

// startSubscriptionLoginRename opens the label input prefilled with the login
// under the cursor. Row 0 renames the implicit Default's display label.
func (m *MainMenuModel) startSubscriptionLoginRename() {
	idx := m.subscriptionModalLoginIndex()
	if idx < 0 || idx > len(m.claudeAccounts) {
		return
	}
	label := m.DefaultAccountLabel()
	if idx >= 1 {
		label = m.claudeAccounts[idx-1].Label
	}
	m.subscriptionModal.input = m.newSubscriptionNameInput(label)
	m.enterSubscriptionLifecycle(subscriptionRename)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
}

// renameSubscriptionLogin commits the label edit: Default's label goes to its
// own file, managed logins are renamed in the registry (dir untouched).
func (m *MainMenuModel) renameSubscriptionLogin(label string) {
	idx := m.subscriptionModalLoginIndex()
	if idx == 0 {
		if m.claudeDefaultLabelFile != "" {
			if err := claudeaccount.SetDefaultLabel(m.claudeDefaultLabelFile, label); err != nil {
				m.subscriptionModal.err = err
				return
			}
		}
		m.SetClaudeDefaultLabel(label)
	} else {
		if idx < 1 || idx > len(m.claudeAccounts) {
			return
		}
		if m.claudeAccountsList == "" {
			m.subscriptionModal.err = errors.New("login storage is not configured")
			return
		}
		dir := m.claudeAccounts[idx-1].Dir
		if err := claudeaccount.Rename(m.claudeAccountsList, dir, label); err != nil {
			m.subscriptionModal.err = err
			return
		}
		activeDir := m.CurrentClaudeAccountDir()
		m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
		m.SetActiveClaudeAccount(activeDir)
	}
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.pane = subscriptionProfilesPane
	m.subscriptionModal.input.Blur()
	m.subscriptionModal.err = nil
}

// startSubscriptionLoginDelete asks to remove the managed login under the
// cursor; Default (row 0) is implicit and not deletable.
func (m *MainMenuModel) startSubscriptionLoginDelete() {
	idx := m.subscriptionModalLoginIndex()
	if idx < 1 || idx > len(m.claudeAccounts) {
		return
	}
	m.enterSubscriptionLifecycle(subscriptionDeleteConfirm)
	m.subscriptionModal.pane = subscriptionDetailsPane
	m.subscriptionModal.err = nil
}

// deleteSubscriptionLogin removes the confirmed login (registry line + config
// dir), reverting to Default if it was active.
func (m *MainMenuModel) deleteSubscriptionLogin() {
	idx := m.subscriptionModalLoginIndex()
	if idx < 1 || idx > len(m.claudeAccounts) {
		m.subscriptionModal.mode = subscriptionBrowse
		return
	}
	dir := m.claudeAccounts[idx-1].Dir
	if err := claudeaccount.Remove(m.claudeAccountsList, m.claudeAccountsDir, m.claudeAccountFile, dir); err != nil {
		m.subscriptionModal.err = err
		return
	}
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.SetActiveClaudeAccount(ReadActiveClaudeAccount(m.claudeAccountFile))
	if m.subscriptionModal.profileCursor > m.subscriptionAddLoginRow() {
		m.subscriptionModal.profileCursor = m.subscriptionAddLoginRow()
	}
	m.subscriptionModal.mode = subscriptionBrowse
	m.subscriptionModal.pane = subscriptionProfilesPane
	m.subscriptionModal.err = nil
}

// subscriptionLoginDetailLines renders the details pane for a login row.
func (m *MainMenuModel) subscriptionLoginDetailLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))

	rows := m.subscriptionLoginRows()
	idx := m.subscriptionModalLoginIndex()
	if idx < 0 || idx >= len(rows) {
		return modalWindow(nil, 0, height, width)
	}
	login := rows[idx]

	nameStyle := green
	if c, ok := m.accountColor(login.Dir); ok {
		nameStyle = lipgloss.NewStyle().Foreground(c)
	}
	loginLabel := dim.Bold(true).Render("LOGIN     ")
	nameWidth := width - lipgloss.Width(loginLabel)
	if nameWidth < 1 {
		nameWidth = 1
	}
	dirText := login.Dir
	if dirText == "" {
		dirText = "default (~/.claude)"
	}
	status := "Not active"
	statusStyle := dim
	if login.Active {
		status = "Active"
		statusStyle = green
	}
	valueRow := func(name, value string, style lipgloss.Style) string {
		return "  " + label.Render(padTo14(name)) + style.Render(modalTruncate(value, width-17))
	}
	lines := []string{
		loginLabel + nameStyle.Render(modalTruncate(login.Label, nameWidth)),
		"",
		subscriptionSectionLine("CONNECTION", width, dim.Bold(true), dim),
		valueRow("Config dir", dirText, dim),
		valueRow("Status", status, statusStyle),
		dim.Render("Signs in on first launch under this login."),
	}
	if m.subscriptionModal.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).
			Render(modalTruncate(m.subscriptionModal.err.Error(), width)))
	}
	lines = append(lines,
		"",
		subscriptionSectionLine("ACTIONS", width, dim.Bold(true), dim),
	)
	use := m.subscriptionActionLabel(subscriptionHitUse, subscriptionDetailUse, "[ Use ]", accent, label)
	rename := m.subscriptionActionLabel(subscriptionHitRename, subscriptionDetailRename, "[ Rename ]", accent, label)
	actions := use + "  " + rename
	if idx >= 1 {
		actions += "  " + m.subscriptionActionLabel(subscriptionHitDelete, subscriptionDetailDelete, "[ Delete ]", accent, label)
	}
	lines = append(lines, actions)
	return modalWindow(lines, 0, height, width)
}

// padTo14 left-aligns a detail label into the shared 15-column label gutter.
func padTo14(name string) string {
	name = modalTruncate(name, 14)
	for len(name) < 15 {
		name += " "
	}
	return name
}

// subscriptionAddLoginDetailLines renders the details pane for + Add login.
func (m *MainMenuModel) subscriptionAddLoginDetailLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	lines := []string{
		accent.Render("ADD LOGIN"),
		dim.Render("Register another Claude Code account."),
		"",
		dim.Render("The login gets its own isolated config dir and"),
		dim.Render("signs in the first time Claude launches under it."),
		"",
		accent.Render("[ Create login ]"),
	}
	return modalWindow(lines, 0, height, width)
}
