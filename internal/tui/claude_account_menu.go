package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
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
	m.accountMenuRenameRow = -1
	m.accountMenuErr = nil
	m.accountMenuHover = -1
}

// enterAccountAddInput opens the inline label text input for a new login.
func (m *MainMenuModel) enterAccountAddInput() tea.Cmd {
	// The field is rendered manually in renderAccountMenuPanel, so this model is
	// only the value/keystroke buffer.
	ti := textinput.New()
	ti.Focus()
	m.accountMenuInput = ti
	m.accountMenuInputMode = true
	m.accountMenuRenameRow = -1
	m.accountMenuConfirm = false
	m.accountMenuErr = nil
	return textinput.Blink
}

// enterAccountRenameInput opens the inline label input prefilled with the login
// at cursor row's current label, ready to be edited in place. Row 0 is the
// implicit Default login; rows 1..len are managed logins.
func (m *MainMenuModel) enterAccountRenameInput(row int) tea.Cmd {
	if row < 0 || row > len(m.claudeAccounts) {
		return nil
	}
	label := m.DefaultAccountLabel()
	if row >= 1 {
		label = m.claudeAccounts[row-1].Label
	}
	ti := textinput.New()
	ti.SetValue(label)
	ti.Focus()
	m.accountMenuInput = ti
	m.accountMenuInputMode = true
	m.accountMenuRenameRow = row
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
				// Default (row 0) and managed logins (1..len) all have an editable
				// label; only the add row does not.
				if m.accountMenuCursor <= len(m.claudeAccounts) {
					return m, m.enterAccountRenameInput(m.accountMenuCursor)
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
		m.accountMenuRenameRow = -1
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
	if m.accountMenuRenameRow >= 0 {
		return m.submitAccountRename(label)
	}
	return m.submitAccountAdd(label)
}

// accountLoginDoneMsg is delivered after the interactive `claude auth login`
// (run via tea.ExecProcess) returns, so the panel can activate the new login and
// stay put.
type accountLoginDoneMsg struct {
	dir string
	err error
}

// submitAccountAdd registers the new login in-process (config dir + registry
// entry), then runs the interactive browser `claude auth login` in place via
// tea.ExecProcess so the user returns to this panel when it finishes — no drop
// out to the bash menu loop.
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
	// Show the new login in the list immediately; leave input mode but keep the
	// panel open so it's there when the login returns.
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.accountMenuInputMode = false
	m.accountMenuRenameRow = -1
	m.accountMenuErr = nil
	return m, m.beginAccountLogin(dir)
}

// newClaudeLoginCmd builds the `claude auth login` command for an account's
// isolated CLAUDE_CONFIG_DIR, with its stdio pinned to the inherited controlling
// terminal (os.Stdin / fd 0).
//
// This pinning is the fix for a hard crash: `claude` is a Bun-compiled binary
// that dies on startup with "EINVAL: invalid argument, kqueue" (then
// "process.stdout.isTTY undefined") when its stdout is a freshly-opened /dev/tty
// fd. tea.ExecProcess, left to its defaults, hands the child the program's
// WithOutput tty — which util.TUITeaOptions opens fresh via open("/dev/tty") —
// so the login would crash before it could read the pasted OAuth code, showing
// up as "login didn't finish". fd 0 is the read/write pane tty inherited at
// launch, which Bun accepts; menu-tui.sh captures only stdout (fd 1), leaving
// fd 0 free, so reusing it for the child's write streams is safe here.
func newClaudeLoginCmd(bin, configDir string) *exec.Cmd {
	c := exec.Command(bin, "auth", "login")
	c.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdin
	c.Stderr = os.Stdin
	return c
}

// beginAccountLogin returns a command that runs `claude auth login` under the new
// account's isolated CLAUDE_CONFIG_DIR. tea.ExecProcess releases the alt-screen
// for the interactive OAuth and restores the TUI afterward. If `claude` isn't on
// PATH the account is still registered (it will prompt for login on first use).
func (m *MainMenuModel) beginAccountLogin(dir string) tea.Cmd {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return func() tea.Msg { return accountLoginDoneMsg{dir: dir, err: err} }
	}
	c := newClaudeLoginCmd(bin, filepath.Join(m.claudeAccountsDir, dir))
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return accountLoginDoneMsg{dir: dir, err: err}
	})
}

// handleAccountLoginDone keeps the panel open after the interactive login. On
// success it switches to the new login; on failure it leaves the active login
// untouched (the account is still registered, so the user can switch to it to
// retry) and shows a note.
func (m *MainMenuModel) handleAccountLoginDone(msg accountLoginDoneMsg) (tea.Model, tea.Cmd) {
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.accountMenuOpen = true
	m.accountMenuInputMode = false
	if msg.err != nil {
		m.accountMenuErr = errors.New("login didn't finish — switch to this login to retry")
		return m, nil
	}
	m.SetActiveClaudeAccount(msg.dir)
	m.persistClaudeAccount()
	return m, nil
}

// submitAccountRename changes the highlighted login's label in place (the config
// dir and its login are untouched) and stays in the panel. Row 0 renames the
// implicit Default login (persisted to its own label file); rows 1..len rename a
// managed login in the registry.
func (m *MainMenuModel) submitAccountRename(label string) (tea.Model, tea.Cmd) {
	row := m.accountMenuRenameRow
	defer func() {
		m.accountMenuInputMode = false
		m.accountMenuRenameRow = -1
	}()

	if row == 0 {
		// Rename the implicit Default login.
		if m.claudeDefaultLabelFile != "" {
			if err := claudeaccount.SetDefaultLabel(m.claudeDefaultLabelFile, label); err != nil {
				m.accountMenuErr = err
				return m, nil
			}
		}
		m.SetClaudeDefaultLabel(label)
		return m, nil
	}

	idx := row - 1
	if idx < 0 || idx >= len(m.claudeAccounts) {
		return m, nil
	}
	if m.claudeAccountsList == "" {
		m.accountMenuErr = errors.New("login storage is not configured")
		return m, nil
	}
	dir := m.claudeAccounts[idx].Dir
	if err := claudeaccount.Rename(m.claudeAccountsList, dir, label); err != nil {
		m.accountMenuErr = err
		return m, nil
	}
	// Reload labels; the active dir is unchanged, so re-apply it to keep the
	// selection in sync after the in-place edit.
	activeDir := m.CurrentClaudeAccountDir()
	m.SetClaudeAccounts(LoadClaudeAccountsList(m.claudeAccountsList))
	m.SetActiveClaudeAccount(activeDir)
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

	faintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	// loginRow renders one selectable login. The keyboard cursor shows a bright ▌
	// marker; a hovered-but-not-cursor row shows a faint ▌ so the pointer target
	// reads as distinct, and it clears the moment the pointer leaves the row.
	loginRow := func(cursorOn, hoverOn, active bool, label, dirName string) string {
		var prefix, labelRendered string
		switch {
		case cursorOn:
			prefix = " " + primaryBoldStyle.Render("▌")
			labelRendered = primaryBoldStyle.Render(label)
		case hoverOn:
			prefix = " " + faintStyle.Render("▌")
			if active {
				labelRendered = greenStyle.Render(label)
			} else {
				labelRendered = label
			}
		default:
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

	// hoverOn marks a transient pointer highlight that yields to the keyboard cursor.
	hoverOn := func(idx int) bool { return m.accountMenuHover == idx && m.accountMenuCursor != idx }
	// Default (implicit) login at index 0.
	lines = append(lines, loginRow(m.accountMenuCursor == 0, hoverOn(0), m.selectedAccount == 0, m.DefaultAccountLabel(), "default"))
	// Managed logins.
	for i, acc := range m.claudeAccounts {
		cursorOn := m.accountMenuCursor == i+1
		active := m.selectedAccount == i+1
		lines = append(lines, loginRow(cursorOn, hoverOn(i+1), active, acc.Label, acc.Dir))
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
		if m.accountMenuRenameRow >= 0 {
			tag = "Rename"
		}
		lines = append(lines, pad("    "+primaryBoldStyle.Render(tag)+"   "+field))
	} else {
		// Add row.
		addRow := m.accountMenuAddRow()
		var addContent string
		switch {
		case m.accountMenuCursor == addRow:
			addContent = " " + primaryBoldStyle.Render("▌") + primaryBoldStyle.Render("+ Add new login…")
		case hoverOn(addRow):
			addContent = " " + faintStyle.Render("▌") + helpStyle.Render("+ Add new login…")
		default:
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
		if m.accountMenuRenameRow >= 0 {
			verb = "⏎ rename"
		}
		help = hints(verb, "Esc cancel")
	case m.accountMenuConfirm:
		help = hints("y remove", "n cancel")
	case m.accountMenuCursor == m.accountMenuAddRow():
		help = hints("↑↓ move", "⏎ add login", "Esc close")
	case m.accountMenuCursor >= 1 && m.accountMenuCursor <= len(m.claudeAccounts):
		// On a managed login: switch, rename and remove.
		help = hints("↑↓ move", "⏎ switch", "a add", "r rename", "d remove", "Esc close")
	default:
		// On Default: switch, add and rename (it has a label, but can't be removed).
		help = hints("↑↓ move", "⏎ switch", "a add", "r rename", "Esc close")
	}
	lines = append(lines, pad(help))

	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}
