package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/ghost-tab/internal/claudeconfig"
)

// openConfigPanel opens the inline Claude config management panel, starting in
// list mode with the cursor on the currently-active entry.
func (m *MainMenuModel) openConfigPanel() {
	m.configPanelOpen = true
	m.configPanelMode = ""
	m.configPanelCursor = m.selectedConfig
	m.configPanelErr = nil
}

// reloadClaudeConfigs re-reads the list and pointer after a mutation, refreshes
// the active selection, and clamps the panel cursor into range.
func (m *MainMenuModel) reloadClaudeConfigs() {
	m.claudeConfigs = LoadClaudeConfigsList(m.claudeConfigsList)
	m.SetActiveClaudeConfig(ReadActiveClaudeConfig(m.claudeConfigFile))
	maxIdx := len(m.claudeConfigs) // Standard is index 0
	if m.configPanelCursor > maxIdx {
		m.configPanelCursor = maxIdx
	}
	if m.configPanelCursor < 0 {
		m.configPanelCursor = 0
	}
}

// enterConfigInput switches the panel into add/rename mode with a focused input.
func (m *MainMenuModel) enterConfigInput(mode string) tea.Cmd {
	m.configPanelMode = mode
	m.configPanelErr = nil
	ti := textinput.New()
	ti.Width = menuContentWidth - 11
	if mode == "rename" {
		ti.Placeholder = "new name"
		ti.SetValue(m.claudeConfigs[m.configPanelCursor-1].Name)
	} else {
		ti.Placeholder = "config name"
	}
	ti.Focus()
	m.configPanelInput = ti
	return textinput.Blink
}

// updateConfigPanel routes key events while the inline config panel is open.
func (m *MainMenuModel) updateConfigPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.configPanelMode {
	case "add", "rename":
		return m.updateConfigPanelInput(msg)
	case "delete":
		return m.updateConfigPanelDelete(msg)
	}

	n := len(m.claudeConfigs) + 1 // +1 for Standard
	switch msg.Type {
	case tea.KeyEsc:
		m.configPanelOpen = false
		return m, nil
	case tea.KeyCtrlC:
		m.configPanelOpen = false
		m.settingsMode = false
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyUp:
		m.configPanelCursor = (m.configPanelCursor - 1 + n) % n
		return m, nil
	case tea.KeyDown:
		m.configPanelCursor = (m.configPanelCursor + 1) % n
		return m, nil
	case tea.KeyEnter:
		m.selectedConfig = m.configPanelCursor
		m.persistClaudeConfig()
		m.configPanelOpen = false
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'j':
				m.configPanelCursor = (m.configPanelCursor + 1) % n
				return m, nil
			case 'k':
				m.configPanelCursor = (m.configPanelCursor - 1 + n) % n
				return m, nil
			case 'a':
				return m, m.enterConfigInput("add")
			case 'r':
				if m.configPanelCursor > 0 {
					return m, m.enterConfigInput("rename")
				}
			case 'd':
				if m.configPanelCursor > 0 {
					m.configPanelMode = "delete"
					m.configPanelErr = nil
				}
			}
		}
	}
	return m, nil
}

// updateConfigPanelInput handles the add/rename text entry.
func (m *MainMenuModel) updateConfigPanelInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.configPanelMode = ""
		m.configPanelInput.Blur()
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.configPanelInput.Value())
		if name == "" {
			m.configPanelMode = ""
			m.configPanelInput.Blur()
			return m, nil
		}
		if m.configPanelMode == "add" {
			file, err := claudeconfig.Add(m.claudeConfigsList, m.claudeConfigsDir, name)
			if err != nil {
				m.configPanelErr = err
				return m, nil
			}
			// New config becomes active.
			_ = claudeconfig.SetActive(m.claudeConfigFile, file)
			m.reloadClaudeConfigs()
			m.configPanelCursor = m.selectedConfig
		} else { // rename
			file := m.claudeConfigs[m.configPanelCursor-1].File
			if err := claudeconfig.Rename(m.claudeConfigsList, file, name); err != nil {
				m.configPanelErr = err
				return m, nil
			}
			m.reloadClaudeConfigs()
		}
		m.configPanelMode = ""
		m.configPanelInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.configPanelInput, cmd = m.configPanelInput.Update(msg)
	return m, cmd
}

// updateConfigPanelDelete handles the delete confirmation.
func (m *MainMenuModel) updateConfigPanelDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := false
	switch msg.Type {
	case tea.KeyEnter:
		confirm = true
	case tea.KeyEsc:
		m.configPanelMode = ""
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'y':
				confirm = true
			case 'n':
				m.configPanelMode = ""
				return m, nil
			}
		}
	}
	if !confirm {
		return m, nil
	}
	if m.configPanelCursor > 0 && m.configPanelCursor <= len(m.claudeConfigs) {
		file := m.claudeConfigs[m.configPanelCursor-1].File
		if err := claudeconfig.Delete(m.claudeConfigsList, m.claudeConfigsDir, m.claudeConfigFile, file); err != nil {
			m.configPanelErr = err
		} else {
			m.reloadClaudeConfigs()
		}
	}
	m.configPanelMode = ""
	return m, nil
}

// renderConfigPanel draws the inline config management box, mirroring the
// settings box border style.
func (m *MainMenuModel) renderConfigPanel() string {
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

	// pad builds a full-width content row from already-styled text.
	pad := func(content string) string {
		gap := menuContentWidth - lipgloss.Width(content) - 1
		if gap < 0 {
			gap = 0
		}
		return leftBorder + " " + content + strings.Repeat(" ", gap) + rightBorder
	}
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder

	var lines []string
	lines = append(lines, topBorder)
	lines = append(lines, pad(primaryBoldStyle.Render("Claude Configs")))
	lines = append(lines, separator)
	lines = append(lines, emptyRow)

	// Item rows: index 0 = Standard, then configs.
	names := []string{"Standard Claude"}
	for _, c := range m.claudeConfigs {
		names = append(names, c.Name)
	}
	for i, name := range names {
		var prefix string
		if i == m.configPanelCursor {
			prefix = "  " + primaryBoldStyle.Render("▎") + " " + primaryBoldStyle.Render(name)
		} else {
			prefix = "    " + name
		}
		state := ""
		if i == m.selectedConfig {
			state = greenStyle.Render("active")
		}
		gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(state) - 1
		if gap < 1 {
			gap = 1
		}
		lines = append(lines, leftBorder+prefix+strings.Repeat(" ", gap)+state+" "+rightBorder)
	}

	lines = append(lines, emptyRow)

	// Mode-specific row.
	switch m.configPanelMode {
	case "add", "rename":
		label := "New config name:"
		if m.configPanelMode == "rename" {
			label = "Rename to:"
		}
		lines = append(lines, pad(helpStyle.Render(label)))
		lines = append(lines, pad(m.configPanelInput.View()))
	case "delete":
		name := ""
		if m.configPanelCursor > 0 && m.configPanelCursor <= len(m.claudeConfigs) {
			name = m.claudeConfigs[m.configPanelCursor-1].Name
		}
		lines = append(lines, pad(errStyle.Render("Delete \""+name+"\"?")))
	}
	if m.configPanelErr != nil {
		lines = append(lines, pad(errStyle.Render(m.configPanelErr.Error())))
	}

	lines = append(lines, separator)

	// Help rows depend on mode.
	sep := dimStyle.Render(" · ")
	switch m.configPanelMode {
	case "add", "rename":
		lines = append(lines, pad(helpStyle.Render("⏎ save")+sep+helpStyle.Render("Esc cancel")))
	case "delete":
		lines = append(lines, pad(helpStyle.Render("y delete")+sep+helpStyle.Render("n cancel")))
	default:
		lines = append(lines, pad(helpStyle.Render("↑↓ move")+sep+helpStyle.Render("⏎ select")+sep+helpStyle.Render("Esc back")))
		lines = append(lines, pad(helpStyle.Render("a add")+sep+helpStyle.Render("r rename")+sep+helpStyle.Render("d delete")))
	}

	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}
