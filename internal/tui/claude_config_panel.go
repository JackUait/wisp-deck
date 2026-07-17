package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// Clickable Save / Cancel button labels in the model-map panel. Defined here so
// the renderer and the mouse hit-test agree on their widths.
const (
	modelMapSaveLabel   = "[ Save ]"
	modelMapCancelLabel = "[ Cancel ]"
)

// openModelMap opens the model mapping panel for the active non-Standard config.
func (m *MainMenuModel) openModelMap() {
	file := m.CurrentClaudeConfigFile()
	if file == "" {
		return
	}
	m.modelMapOpen = true
	m.modelMapCursor = 0
	provider := m.currentClaudeConfigProvider()
	m.modelMapModels = claudeconfig.ProviderModels[provider.Key]
	m.modelMap = claudeconfig.ReadModelMappings(m.claudeConfigsDir, file, m.modelMapModels)
	m.modelMapErr = nil
	m.modelMapKeyMode = false
	m.modelMapHover = -1
	m.modelMapSlotHover = -1
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
		m.syncOpenCode()
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
				if m.modelMapUsesAPIKey() {
					return m, m.enterModelMapKeyInput()
				}
			}
		}
	}
	return m, nil
}

// enterModelMapKeyInput opens the API key text input within the model map panel.
func (m *MainMenuModel) enterModelMapKeyInput() tea.Cmd {
	file := m.CurrentClaudeConfigFile()
	if file == "" || !m.modelMapUsesAPIKey() {
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

func (m *MainMenuModel) modelMapUsesAPIKey() bool {
	return m.currentClaudeConfigProvider().Auth == claudeconfig.AuthAPIKey
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
			m.syncOpenCode()
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

	faintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	models := m.modelMapModels
	for i, alias := range claudeconfig.AnthropicAliases {
		// The keyboard cursor shows a bright ▌; a hovered-but-not-cursor slot shows
		// a faint ▌ so the pointer target reads as distinct, and it clears the moment
		// the pointer leaves the slots.
		var prefix string
		switch {
		case i == m.modelMapCursor:
			prefix = " " + primaryBoldStyle.Render("▌")
		case i == m.modelMapSlotHover:
			prefix = " " + faintStyle.Render("▌")
		default:
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

	// Authentication row. API-key providers expose the existing editor; Codex
	// ChatGPT profiles show their launch-time authentication source read-only.
	file := m.CurrentClaudeConfigFile()
	if m.modelMapUsesAPIKey() {
		apiKey := claudeconfig.ReadAPIKey(m.claudeConfigsDir, file)
		apiKeyStatus := dimStyle.Render("(not set)")
		if apiKey != "" {
			apiKeyStatus = greenStyle.Render("••••••••")
		}
		keyPrefix := "    "
		keyLabelStyle := helpStyle
		if m.modelMapHover == 4 {
			keyPrefix = " " + primaryBoldStyle.Render("▌") + "  "
			keyLabelStyle = primaryBoldStyle
		}
		lines = append(lines, pad(keyPrefix+keyLabelStyle.Render("API Key")+dimStyle.Render(" →  ")+apiKeyStatus+dimStyle.Render("  press 'e' to edit")))
	} else {
		lines = append(lines, pad("    "+helpStyle.Render("Authentication")+dimStyle.Render(" →  ")+greenStyle.Render("codex login")))
	}

	// Save / Cancel buttons — the click-friendly equivalents of ⏎ and Esc, so a
	// pointer-only user can finalize or discard their mapping changes. Kept at a
	// fixed row (right after the API key row) so hit-testing doesn't shift with
	// the optional error line below.
	saveStyle := helpStyle
	cancelStyle := helpStyle
	btnHover := lipgloss.NewStyle().Foreground(m.theme.Bright).Bold(true).Reverse(true)
	if m.modelMapHover == 5 {
		saveStyle = btnHover
	}
	if m.modelMapHover == 6 {
		cancelStyle = btnHover
	}
	lines = append(lines, pad("   "+saveStyle.Render(modelMapSaveLabel)+"   "+cancelStyle.Render(modelMapCancelLabel)))

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
	helpLine := helpStyle.Render("↑↓ slot") + sep + helpStyle.Render("←→ model")
	if m.modelMapUsesAPIKey() {
		helpLine += sep + helpStyle.Render("e api key")
	}
	helpLine += sep + helpStyle.Render("⏎ save") + sep + helpStyle.Render("Esc cancel")
	lines = append(lines, pad(helpLine))

	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}

// configAPIKeyIndicator returns a display string showing mapping status for a config.
// Mappings are counted against the config's own provider models, so a value
// belonging to a different provider isn't mis-counted.
func configAPIKeyIndicator(configsDir, file, name string) string {
	if file == "" {
		return ""
	}
	provider := claudeconfig.ProviderForConfig(configsDir, claudeconfig.Config{Name: name, File: file})
	mappings := claudeconfig.ReadModelMappings(configsDir, file, claudeconfig.ProviderModels[provider.Key])
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
