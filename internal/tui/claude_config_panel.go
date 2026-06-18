package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/ghost-tab/internal/claudeconfig"
)

// openModelMap opens the model mapping panel for the active non-Standard config.
func (m *MainMenuModel) openModelMap() {
	file := m.CurrentClaudeConfigFile()
	if file == "" {
		return
	}
	m.modelMapOpen = true
	m.modelMapCursor = 0
	m.modelMapModels = claudeconfig.ModelsForConfig(m.CurrentClaudeConfigName())
	m.modelMap = claudeconfig.ReadModelMappings(m.claudeConfigsDir, file, m.modelMapModels)
	m.modelMapErr = nil
	m.modelMapKeyMode = false
}

// updateModelMap handles key events while the model mapping panel is open.
func (m *MainMenuModel) updateModelMap(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelMapKeyMode {
		return m.updateModelMapKeyInput(msg)
	}
	n := len(m.modelMapModels)
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.modelMapOpen = false
		return m, nil
	case tea.KeyEnter:
		file := m.CurrentClaudeConfigFile()
		if file == "" {
			m.modelMapOpen = false
			return m, nil
		}
		if err := claudeconfig.WriteModelMappings(m.claudeConfigsDir, file, m.modelMap, m.modelMapModels); err != nil {
			m.modelMapErr = err
			return m, nil
		}
		m.modelMapOpen = false
		return m, nil
	case tea.KeyUp:
		m.modelMapCursor = (m.modelMapCursor - 1 + 4) % 4
		return m, nil
	case tea.KeyDown:
		m.modelMapCursor = (m.modelMapCursor + 1) % 4
		return m, nil
	case tea.KeyLeft:
		cur := m.modelMap[m.modelMapCursor]
		if cur <= -1 {
			m.modelMap[m.modelMapCursor] = n - 1
		} else {
			m.modelMap[m.modelMapCursor] = cur - 1
		}
		return m, nil
	case tea.KeyRight:
		cur := m.modelMap[m.modelMapCursor]
		if cur >= n-1 {
			m.modelMap[m.modelMapCursor] = -1
		} else {
			m.modelMap[m.modelMapCursor] = cur + 1
		}
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 'k':
				m.modelMapCursor = (m.modelMapCursor - 1 + 4) % 4
				return m, nil
			case 'j':
				m.modelMapCursor = (m.modelMapCursor + 1) % 4
				return m, nil
			case 'e':
				return m, m.enterModelMapKeyInput()
			}
		}
	}
	return m, nil
}

// enterModelMapKeyInput opens the API key text input within the model map panel.
func (m *MainMenuModel) enterModelMapKeyInput() tea.Cmd {
	file := m.CurrentClaudeConfigFile()
	if file == "" {
		return nil
	}
	ti := textinput.New()
	ti.Width = menuContentWidth - 11
	ti.Placeholder = "API key"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.SetValue(claudeconfig.ReadAPIKey(m.claudeConfigsDir, file))
	ti.Focus()
	m.modelMapKeyInput = ti
	m.modelMapKeyMode = true
	m.modelMapErr = nil
	return textinput.Blink
}

// updateModelMapKeyInput handles key events while entering the API key.
func (m *MainMenuModel) updateModelMapKeyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.modelMapKeyMode = false
		m.modelMapKeyInput.Blur()
		return m, nil
	case tea.KeyEnter:
		file := m.CurrentClaudeConfigFile()
		if file != "" {
			key := strings.TrimSpace(m.modelMapKeyInput.Value())
			if err := claudeconfig.WriteAPIKey(m.claudeConfigsDir, file, key); err != nil {
				m.modelMapErr = err
				return m, nil
			}
		}
		m.modelMapKeyMode = false
		m.modelMapKeyInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.modelMapKeyInput, cmd = m.modelMapKeyInput.Update(msg)
	m.modelMapErr = nil
	return m, cmd
}

// renderModelMapPanel draws the model mapping box below the settings box.
func (m *MainMenuModel) renderModelMapPanel() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))

	hLine := strings.Repeat("─", menuInnerWidth)
	topBorder := dimStyle.Render("╭" + hLine + "╮")
	separator := dimStyle.Render("├" + hLine + "┤")
	bottomBorder := dimStyle.Render("╰" + hLine + "╯")
	leftBorder := dimStyle.Render("│")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("│")

	pad := func(content string) string {
		gap := menuContentWidth - lipgloss.Width(content) - 1
		if gap < 0 {
			gap = 0
		}
		return leftBorder + " " + content + strings.Repeat(" ", gap) + rightBorder
	}
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder

	name := m.CurrentClaudeConfigName()
	var lines []string
	lines = append(lines, topBorder)
	lines = append(lines, pad(primaryBoldStyle.Render("Model Mapping: "+name)))
	lines = append(lines, separator)
	lines = append(lines, emptyRow)

	models := m.modelMapModels
	for i, alias := range claudeconfig.AnthropicAliases {
		var prefix string
		if i == m.modelMapCursor {
			prefix = "  " + primaryBoldStyle.Render("▎") + " "
		} else {
			prefix = "    "
		}
		aliasLabel := primaryBoldStyle.Render(fmt.Sprintf("%-8s", alias))
		arrow := dimStyle.Render(" →  ")
		var modelStr string
		idx := m.modelMap[i]
		if idx >= 0 && idx < len(models) {
			modelStr = greenStyle.Render(models[idx])
		} else {
			modelStr = dimStyle.Render("(none)")
		}
		navHint := ""
		if i == m.modelMapCursor {
			navHint = dimStyle.Render(" ◀▶")
		}
		content := prefix + aliasLabel + arrow + modelStr + navHint
		lines = append(lines, pad(content))
	}

	lines = append(lines, emptyRow)

	// API key row
	file := m.CurrentClaudeConfigFile()
	apiKey := claudeconfig.ReadAPIKey(m.claudeConfigsDir, file)
	apiKeyStatus := dimStyle.Render("(not set)")
	if apiKey != "" {
		apiKeyStatus = greenStyle.Render("••••••••")
	}
	lines = append(lines, pad("    "+helpStyle.Render("API Key")+dimStyle.Render(" →  ")+apiKeyStatus+dimStyle.Render("  press 'e' to edit")))

	if m.modelMapKeyMode {
		lines = append(lines, emptyRow)
		lines = append(lines, pad("  "+m.modelMapKeyInput.View()))
	}

	if m.modelMapErr != nil {
		lines = append(lines, emptyRow)
		lines = append(lines, pad(errStyle.Render(m.modelMapErr.Error())))
	}

	lines = append(lines, separator)

	sep := dimStyle.Render(" · ")
	helpLine := helpStyle.Render("↑↓ slot") + sep + helpStyle.Render("←→ model") + sep + helpStyle.Render("e api key") + sep + helpStyle.Render("⏎ save") + sep + helpStyle.Render("Esc cancel")
	lines = append(lines, pad(helpLine))

	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}

// configAPIKeyIndicator returns a display string showing mapping status for a config.
func configAPIKeyIndicator(configsDir, file string) string {
	if file == "" {
		return ""
	}
	mappings := claudeconfig.ReadModelMappings(configsDir, file, claudeconfig.AllModels())
	mapped := 0
	for _, v := range mappings {
		if v >= 0 {
			mapped++
		}
	}
	if mapped > 0 {
		return fmt.Sprintf("%d mapped", mapped)
	}
	return "unmapped"
}
