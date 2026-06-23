package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/ghost-tab/internal/claudeaccount"
)

// The login-management panel mirrors the Plan model-map panel: an inline box
// that opens below the menu when Enter is pressed on the LOGIN row. It lists the
// implicit Default login, every managed login, and a trailing "add" row. Rows
// are addressed by accountMenuCursor: 0 = Default, 1..len = managed logins,
// len+1 = the add row. Adding a login is fully inline — the label is typed in a
// text input here, the account is registered in-process, and only the browser
// `claude auth login` runs outside the alt-screen TUI (in wrapper.sh).

// accountMenuAddRow returns the cursor index of the "Add new login…" row.
func (m *MainMenuModel) accountMenuAddRow() int { return len(m.claudeAccounts) + 1 }

// openAccountMenu shows the login panel, starting the cursor on the active login.
func (m *MainMenuModel) openAccountMenu() {
	m.accountMenuOpen = true
	m.accountMenuCursor = m.selectedAccount
	if m.accountMenuCursor < 0 || m.accountMenuCursor > m.accountMenuAddRow() {
		m.accountMenuCursor = 0
	}
	m.accountMenuConfirm = false
	m.accountMenuInputMode = false
	m.accountMenuRenameIdx = -1
	m.accountMenuErr = nil
}

// enterAccountAddInput opens the inline label text input for a new login.
func (m *MainMenuModel) enterAccountAddInput() tea.Cmd {
	// The field is rendered manually in renderAccountMenuPanel, so this model is
	// only the value/keystroke buffer.
	ti := textinput.New()
	ti.Focus()
	m.accountMenuInput = ti
	m.accountMenuInputMode = true
	m.accountMenuRenameIdx = -1
	m.accountMenuConfirm = false
	m.accountMenuErr = nil
	return textinput.Blink
}

// enterAccountRenameInput opens the inline label input prefilled with the managed
// login's current label, ready to be edited in place.
func (m *MainMenuModel) enterAccountRenameInput(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.claudeAccounts) {
		return nil
	}
	ti := textinput.New()
	ti.SetValue(m.claudeAccounts[idx].Label)
	ti.Focus()
	m.accountMenuInput = ti
	m.accountMenuInputMode = true
	m.accountMenuRenameIdx = idx
	m.accountMenuConfirm = false
	m.accountMenuErr = nil
	return textinput.Blink
}

// updateAccountMenu handles key events while the login panel is open.
func (m *MainMenuModel) updateAccountMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.accountMenuInputMode {
		return m.updateAccountAddInput(msg)
	}
	if m.accountMenuConfirm {
		switch msg.String() {
		case "y", "Y":
			m.confirmRemoveAccount()
		default:
			m.accountMenuConfirm = false
		}
		return m, nil
	}

	addRow := m.accountMenuAddRow()
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.accountMenuOpen = false
		return m, nil
	case tea.KeyUp:
		if m.accountMenuCursor > 0 {
			m.accountMenuCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.accountMenuCursor < addRow {
			m.accountMenuCursor++
		}
		return m, nil
	case tea.KeyEnter:
		if m.accountMenuCursor == addRow {
			return m, m.enterAccountAddInput()
		}
		// Switch the active login to the highlighted row and close.
		m.selectedAccount = m.accountMenuCursor
		m.persistClaudeAccount()
		m.accountMenuOpen = false
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'k':
				if m.accountMenuCursor > 0 {
					m.accountMenuCursor--
				}
			case 'j':
				if m.accountMenuCursor < addRow {
					m.accountMenuCursor++
				}
			case 'a':
				return m, m.enterAccountAddInput()
			case 'r':
				// Only managed logins (1..len) have an editable label.
				if m.accountMenuCursor >= 1 && m.accountMenuCursor <= len(m.claudeAccounts) {
					return m, m.enterAccountRenameInput(m.accountMenuCursor - 1)
				}
			case 'd':
				// Only managed logins (1..len) are removable; Default is implicit.
				if m.accountMenuCursor >= 1 && m.accountMenuCursor <= len(m.claudeAccounts) {
					m.accountMenuConfirm = true
				}
			}
		}
		return m, nil
	}
	return m, nil
}

// updateAccountAddInput handles key events while typing a login label (add or
// rename).
func (m *MainMenuModel) updateAccountAddInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.accountMenuInputMode = false
		m.accountMenuRenameIdx = -1
		m.accountMenuInput.Blur()
		return m, nil
	case tea.KeyEnter:
		return m.submitAccountInput()
	}
	var cmd tea.Cmd
	m.accountMenuInput, cmd = m.accountMenuInput.Update(msg)
	m.accountMenuErr = nil
	return m, cmd
}

// submitAccountInput commits the inline label field: a rename edits the label in
// place (stays in the panel), an add registers the login and exits to run the
// browser auth.
func (m *MainMenuModel) submitAccountInput() (tea.Model, tea.Cmd) {
	label := strings.TrimSpace(m.accountMenuInput.Value())
	if label == "" {
		return m, nil // wait for a non-empty label
	}
	if m.accountMenuRenameIdx >= 0 {
		return m.submitAccountRename(label)
	}
	return m.submitAccountAdd(label)
}

// submitAccountAdd registers the new login in-process (config dir + registry
// entry) and exits with the login-account action carrying its dir, so wrapper.sh
// runs only the interactive browser `claude auth login` that can't live inside
// the alt-screen TUI.
func (m *MainMenuModel) submitAccountAdd(label string) (tea.Model, tea.Cmd) {
	if m.claudeAccountsList == "" || m.claudeAccountsDir == "" {
		m.accountMenuErr = errors.New("login storage is not configured")
		m.accountMenuInputMode = false
		return m, nil
	}
	dir, err := claudeaccount.Add(m.claudeAccountsList, m.claudeAccountsDir, label)
	if err != nil {
		m.accountMenuErr = err
		m.accountMenuInputMode = false
		return m, nil
	}
	m.accountMenuInputMode = false
	m.accountMenuOpen = false
	m.setActionResult("login-account")
	m.result.AccountDir = dir
	return m, tea.Quit
}

// submitAccountRename changes the highlighted login's label in place (the config
// dir and its login are untouched) and stays in the panel.
func (m *MainMenuModel) submitAccountRename(label string) (tea.Model, tea.Cmd) {
	idx := m.accountMenuRenameIdx
	if idx < 0 || idx >= len(m.claudeAccounts) {
		m.accountMenuInputMode = false
		m.accountMenuRenameIdx = -1
		return m, nil
	}
	if m.claudeAccountsList == "" {
		m.accountMenuErr = errors.New("login storage is not configured")
		m.accountMenuInputMode = false
		return m, nil
	}
	dir := m.claudeAccounts[idx].Dir
	if err := claudeaccount.Rename(m.claudeAccountsList, dir, label); err != nil {
		m.accountMenuErr = err
		m.accountMenuInputMode = false
		return m, nil
	}
	// Reload labels; the active dir is unchanged, so re-apply it to keep the
	// selection in sync after the in-place edit.
	activeDir := m.CurrentClaudeAccountDir()
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.SetActiveClaudeAccount(activeDir)
	m.accountMenuInputMode = false
	m.accountMenuRenameIdx = -1
	return m, nil
}

// confirmRemoveAccount deletes the highlighted managed login (registry line +
// config dir), reverting to Default if it was active, then reloads the list.
func (m *MainMenuModel) confirmRemoveAccount() {
	m.accountMenuConfirm = false
	idx := m.accountMenuCursor - 1
	if idx < 0 || idx >= len(m.claudeAccounts) {
		return
	}
	if m.claudeAccountsList == "" || m.claudeAccountsDir == "" {
		return
	}
	dir := m.claudeAccounts[idx].Dir
	if err := claudeaccount.Remove(m.claudeAccountsList, m.claudeAccountsDir, m.claudeAccountFile, dir); err != nil {
		m.accountMenuErr = err
		return
	}
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.SetActiveClaudeAccount(ReadActiveClaudeAccount(m.claudeAccountFile))
	if m.accountMenuCursor > m.accountMenuAddRow() {
		m.accountMenuCursor = m.accountMenuAddRow()
	}
}

// renderAccountMenuPanel draws the login-management box below the menu, using the
// same chrome as the model-map panel.
func (m *MainMenuModel) renderAccountMenuPanel() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	hLine := strings.Repeat("─", menuInnerWidth)
	topBorder := dimStyle.Render("╭" + hLine + "╮")
	separator := dimStyle.Render("├" + hLine + "┤")
	bottomBorder := dimStyle.Render("╰" + hLine + "╯")
	leftBorder := dimStyle.Render("│")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("│")

	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	pad := func(content string) string {
		gap := menuContentWidth - lipgloss.Width(content) - 1
		if gap < 0 {
			gap = 0
		}
		return leftBorder + " " + content + strings.Repeat(" ", gap) + rightBorder
	}
	// row places left-aligned content and an optional right-aligned status within
	// the content column (mirrors the model-map panel's spacing).
	row := func(left, right string) string {
		gap := menuContentWidth - 1 - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return leftBorder + " " + left + strings.Repeat(" ", gap) + right + rightBorder
	}

	// loginRow renders one selectable login with the cursor marker and a
	// right-aligned status (the active tag, or the dim config-dir name).
	loginRow := func(cursorOn, active bool, label, dirName string) string {
		var prefix, labelRendered string
		if cursorOn {
			prefix = " " + primaryBoldStyle.Render("▌")
			labelRendered = primaryBoldStyle.Render(label)
		} else {
			prefix = "    "
			if active {
				labelRendered = greenStyle.Render(label)
			} else {
				labelRendered = label
			}
		}
		var right string
		switch {
		case active:
			right = greenStyle.Render("(active)")
		case dirName != "":
			right = dimStyle.Render(dirName)
		}
		return row(prefix+labelRendered, right)
	}

	var lines []string
	lines = append(lines, topBorder)
	lines = append(lines, pad(primaryBoldStyle.Render("Claude Logins")))
	lines = append(lines, separator)
	lines = append(lines, emptyRow)

	// Default (implicit) login at index 0.
	lines = append(lines, loginRow(m.accountMenuCursor == 0, m.selectedAccount == 0, "Default", "default"))
	// Managed logins.
	for i, acc := range m.claudeAccounts {
		cursorOn := m.accountMenuCursor == i+1
		active := m.selectedAccount == i+1
		lines = append(lines, loginRow(cursorOn, active, acc.Label, acc.Dir))
	}

	lines = append(lines, emptyRow)

	if m.accountMenuInputMode {
		// Inline label entry for a new login — a single labeled field that sits
		// where the add row was, keeping the panel's vertical rhythm. The field is
		// rendered by hand (not textinput.View) because bubbles self-pads the view
		// to an inconsistent width that overflows the box; here the width is
		// deterministic so pad() keeps the right border steady while typing.
		promptStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)
		var field string
		if v := m.accountMenuInput.Value(); v == "" {
			field = promptStyle.Render("❯ ") + lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Work, Personal…")
		} else {
			cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
			field = promptStyle.Render("❯ ") + lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(v) + cursor
		}
		tag := "New login"
		if m.accountMenuRenameIdx >= 0 {
			tag = "Rename"
		}
		lines = append(lines, pad("    "+primaryBoldStyle.Render(tag)+"   "+field))
	} else {
		// Add row.
		addRow := m.accountMenuAddRow()
		var addContent string
		if m.accountMenuCursor == addRow {
			addContent = " " + primaryBoldStyle.Render("▌") + primaryBoldStyle.Render("+ Add new login…")
		} else {
			addContent = "    " + helpStyle.Render("+ Add new login…")
		}
		lines = append(lines, row(addContent, ""))
	}
	lines = append(lines, emptyRow)

	if m.accountMenuConfirm && m.accountMenuCursor >= 1 && m.accountMenuCursor <= len(m.claudeAccounts) {
		name := m.claudeAccounts[m.accountMenuCursor-1].Label
		lines = append(lines, pad(errStyle.Render(fmt.Sprintf("Remove %q? ", name))+helpStyle.Render("[y/N]")))
	}
	if m.accountMenuErr != nil {
		lines = append(lines, pad(errStyle.Render(m.accountMenuErr.Error())))
	}

	lines = append(lines, separator)

	sep := dimStyle.Render(" · ")
	hints := func(parts ...string) string {
		rendered := make([]string, len(parts))
		for i, p := range parts {
			rendered[i] = helpStyle.Render(p)
		}
		return strings.Join(rendered, sep)
	}
	var help string
	switch {
	case m.accountMenuInputMode:
		verb := "⏎ create login"
		if m.accountMenuRenameIdx >= 0 {
			verb = "⏎ rename"
		}
		help = hints(verb, "Esc cancel")
	case m.accountMenuConfirm:
		help = hints("y remove", "n cancel")
	case m.accountMenuCursor == m.accountMenuAddRow():
		help = hints("↑↓ move", "⏎ add login", "Esc close")
	case m.accountMenuCursor >= 1 && m.accountMenuCursor <= len(m.claudeAccounts):
		// On a managed login: rename and remove are available.
		help = hints("↑↓ move", "⏎ switch", "a add", "r rename", "d remove", "Esc close")
	default:
		// On Default: only switch/add (the implicit login can't be edited).
		help = hints("↑↓ move", "⏎ switch", "a add", "Esc close")
	}
	lines = append(lines, pad(help))

	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}
