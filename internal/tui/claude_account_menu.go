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
	m.accountMenuErr = nil
}

// enterAccountAddInput opens the inline label text input for a new login.
func (m *MainMenuModel) enterAccountAddInput() tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "Work, Personal…"
	ti.Width = menuContentWidth - 12
	ti.Focus()
	m.accountMenuInput = ti
	m.accountMenuInputMode = true
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

// updateAccountAddInput handles key events while typing a new login's label.
func (m *MainMenuModel) updateAccountAddInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.accountMenuInputMode = false
		m.accountMenuInput.Blur()
		return m, nil
	case tea.KeyEnter:
		return m.submitAccountAddInput()
	}
	var cmd tea.Cmd
	m.accountMenuInput, cmd = m.accountMenuInput.Update(msg)
	m.accountMenuErr = nil
	return m, cmd
}

// submitAccountAddInput registers the new login in-process (config dir + registry
// entry) and exits with the login-account action carrying its dir, so wrapper.sh
// runs only the interactive browser `claude auth login` that can't live inside
// the alt-screen TUI.
func (m *MainMenuModel) submitAccountAddInput() (tea.Model, tea.Cmd) {
	label := strings.TrimSpace(m.accountMenuInput.Value())
	if label == "" {
		return m, nil // wait for a non-empty label
	}
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
		// Inline label entry for a new login.
		lines = append(lines, pad(primaryBoldStyle.Render("Add a Claude login")))
		lines = append(lines, pad(dimStyle.Render("Label (e.g. Work, Personal):")))
		lines = append(lines, pad(m.accountMenuInput.View()))
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
	var help string
	switch {
	case m.accountMenuInputMode:
		help = helpStyle.Render("⏎ create login") + sep + helpStyle.Render("Esc cancel")
	case m.accountMenuConfirm:
		help = helpStyle.Render("y remove") + sep + helpStyle.Render("n cancel")
	case m.accountMenuCursor == m.accountMenuAddRow():
		help = helpStyle.Render("↑↓ move") + sep + helpStyle.Render("⏎ add login") + sep + helpStyle.Render("Esc close")
	default:
		help = helpStyle.Render("↑↓ move") + sep + helpStyle.Render("⏎ switch") + sep + helpStyle.Render("a add") + sep + helpStyle.Render("d remove") + sep + helpStyle.Render("Esc close")
	}
	lines = append(lines, pad(help))

	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}
