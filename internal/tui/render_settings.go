package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderSettingsItem renders a single settings item row with state right-aligned.
func (m *MainMenuModel) renderSettingsItem(index int, label, stateText string, stateStyle, brightBoldStyle lipgloss.Style, leftBorder, rightBorder string) string {
	stateRendered := stateStyle.Render(stateText)
	selectedBgStyle := lipgloss.NewStyle().Background(lipgloss.Color("236"))
	if m.settingsSelected == index {
		// Loud selection only when the body holds focus; off-focus the row drops
		// to a faint neutral cursor marker with no wash, so the nav pill is the
		// only thing reading as "selected" (matches the Projects list).
		markerStyle := brightBoldStyle
		labelStyle := brightBoldStyle
		washStyle := selectedBgStyle
		if m.focus != FocusBody {
			markerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
			labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
			washStyle = lipgloss.NewStyle()
		}
		marker := markerStyle.Render("▌")
		labelText := labelStyle.Render(label)
		prefix := " " + marker + labelText
		gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(stateRendered) - 1
		if gap < 1 {
			gap = 1
		}
		return leftBorder + washStyle.Render(prefix+strings.Repeat(" ", gap)+stateRendered+" ") + rightBorder
	}
	prefix := "    " + label
	gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(stateRendered) - 1
	if gap < 1 {
		gap = 1
	}
	return leftBorder + prefix + strings.Repeat(" ", gap) + stateRendered + " " + rightBorder
}

// renderSettingsBox builds the Settings tab box string: shared chrome (top border +
// title row + tab bar + separator) followed by the existing settings item rows and
// help row.
func (m *MainMenuModel) renderSettingsBox() string {
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)

	// State color depends on ghost display mode.
	var stateColor lipgloss.Color
	switch m.ghostDisplay {
	case "animated":
		stateColor = lipgloss.Color("114") // green
	case "static":
		stateColor = lipgloss.Color("220") // yellow
	default:
		stateColor = lipgloss.Color("241") // gray
	}
	stateStyle := lipgloss.NewStyle().Foreground(stateColor)

	top, separator, bottomBorder, leftBorder, rightBorder := m.boxBorders()

	var lines []string

	// Shared chrome: top border + title row + PLAN switcher + switcher gap +
	// tab bar + separator. The PLAN row mirrors the Projects header so the chrome
	// lines up identically on every tab.
	lines = append(lines, top)
	if m.accountRowCount() > 0 {
		lines = append(lines, m.renderAccountRow(leftBorder, rightBorder))
	}
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))
	if m.subscriptionRowCount() > 0 {
		lines = append(lines, m.renderSubscriptionRow(leftBorder, rightBorder))
	}
	lines = append(lines, m.emptyMenuRow(leftBorder, rightBorder))
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)

	// Empty row
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	lines = append(lines, emptyRow)

	// Ghost Display item
	ghostLabel := "Ghost Display"
	ghostState := "[" + ghostDisplayLabel(m.ghostDisplay) + "]"
	lines = append(lines, m.renderSettingsItem(0, ghostLabel, ghostState, stateStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Tab Title item
	var tabTitleColor lipgloss.Color
	if m.tabTitle == "full" {
		tabTitleColor = lipgloss.Color("114") // green
	} else {
		tabTitleColor = lipgloss.Color("220") // yellow
	}
	tabTitleStyle := lipgloss.NewStyle().Foreground(tabTitleColor)
	tabLabel := "Tab Title"
	tabState := "[" + tabTitleLabel(m.tabTitle) + "]"
	lines = append(lines, m.renderSettingsItem(1, tabLabel, tabState, tabTitleStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Sound Notifications item
	var soundColor lipgloss.Color
	if m.soundName != "" {
		soundColor = lipgloss.Color("114") // green
	} else {
		soundColor = lipgloss.Color("241") // gray
	}
	soundStyle := lipgloss.NewStyle().Foreground(soundColor)
	soundLabel := "Sound"
	soundState := "[Off]"
	if m.soundName != "" {
		soundState = "[" + m.soundName + "]"
	}
	lines = append(lines, m.renderSettingsItem(2, soundLabel, soundState, soundStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Panel item
	var panelColor lipgloss.Color
	if m.panelMode == "compact" {
		panelColor = lipgloss.Color("114") // green (default)
	} else {
		panelColor = lipgloss.Color("220") // yellow (non-default)
	}
	panelStyle := lipgloss.NewStyle().Foreground(panelColor)
	panelLabel := "Panel"
	panelState := "[" + panelModeLabel(m.panelMode) + "]"
	lines = append(lines, m.renderSettingsItem(3, panelLabel, panelState, panelStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Default projects dir item
	var rootState string
	if m.projectsRoot == "" {
		rootState = "(not set)"
	} else {
		rootState = shortenHomePath(m.projectsRoot)
	}
	rootColor := lipgloss.Color("241") // gray when not set
	if m.projectsRoot != "" {
		rootColor = lipgloss.Color("114") // green when set
	}
	rootStyle := lipgloss.NewStyle().Foreground(rootColor)
	if m.settingsInputMode && m.settingsSelected == 4 {
		// Render inline text input
		inputView := m.settingsInput.View()
		inputWidth := lipgloss.Width(inputView)
		inputPadding := menuContentWidth - inputWidth - 1
		if inputPadding < 0 {
			inputPadding = 0
		}
		inputRow := leftBorder + " " + inputView + strings.Repeat(" ", inputPadding) + rightBorder
		lines = append(lines, inputRow)
		if m.settingsInputErr != nil {
			errText := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.settingsInputErr.Error())
			errPadding := menuContentWidth - lipgloss.Width(errText) - 1
			if errPadding < 0 {
				errPadding = 0
			}
			errRow := leftBorder + " " + errText + strings.Repeat(" ", errPadding) + rightBorder
			lines = append(lines, errRow)
		}
	} else {
		lines = append(lines, m.renderSettingsItem(4, "Default projects dir", rootState, rootStyle, primaryBoldStyle, leftBorder, rightBorder))
	}

	// Claude Config item (only for the claude tool)
	if m.ClaudeConfigVisible() {
		cfgName := m.CurrentClaudeConfigName()
		var cfgColor lipgloss.Color
		if m.CurrentClaudeConfigFile() != "" {
			cfgColor = lipgloss.Color("114") // green when a config is active
		} else {
			cfgColor = lipgloss.Color("241") // gray for Standard
		}
		cfgStyle := lipgloss.NewStyle().Foreground(cfgColor)
		cfgFile := m.CurrentClaudeConfigFile()
		state := "[" + cfgName + "]"
		if cfgFile != "" {
			indicator := configAPIKeyIndicator(m.claudeConfigsDir, cfgFile, cfgName)
			dimIndicator := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(" " + indicator)
			state = state + dimIndicator
		}
		lines = append(lines, m.renderSettingsItem(5, "Plan", state, cfgStyle, primaryBoldStyle, leftBorder, rightBorder))
	}

	// Login item: the active native Claude account (manage logins here — ←→
	// switches, ⏎ adds one). Always shown as the account-management entry point,
	// even when the top LOGIN switcher row is hidden (single login).
	loginLabel := m.CurrentClaudeAccountLabel()
	var loginColor lipgloss.Color
	if m.CurrentClaudeAccountDir() != "" {
		loginColor = lipgloss.Color("114") // green when a non-Default login is active
	} else {
		loginColor = lipgloss.Color("241") // gray for Default
	}
	loginStyle := lipgloss.NewStyle().Foreground(loginColor)
	lines = append(lines, m.renderSettingsItem(6, "Login", "["+loginLabel+"]", loginStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Empty row
	lines = append(lines, emptyRow)

	// Separator before help
	lines = append(lines, separator)

	// Help row — show ⏎ edit hint for item 3 (projects dir), ← → cycle for others
	sep := dimStyle.Render(" · ")
	var cycleOrEdit string
	switch m.settingsSelected {
	case 4:
		cycleOrEdit = helpStyle.Render("⏎ edit")
	case 5:
		if m.selectedConfig > 0 {
			cycleOrEdit = helpStyle.Render("←→ cycle") + sep + helpStyle.Render("⏎ map models")
		} else {
			cycleOrEdit = helpStyle.Render("←→ cycle")
		}
	case 6:
		if m.accountFocusable() {
			cycleOrEdit = helpStyle.Render("←→ switch") + sep + helpStyle.Render("⏎ manage")
		} else {
			cycleOrEdit = helpStyle.Render("⏎ manage")
		}
	default:
		cycleOrEdit = helpStyle.Render("←→ cycle")
	}
	helpContent := helpStyle.Render("↑↓ navigate") + sep + cycleOrEdit + sep + helpStyle.Render("Esc close")
	helpContentWidth := lipgloss.Width(helpContent)
	helpPadding := menuContentWidth - helpContentWidth - 1
	if helpPadding < 0 {
		helpPadding = 0
	}
	helpRow := leftBorder + " " + helpContent + strings.Repeat(" ", helpPadding) + rightBorder
	lines = append(lines, helpRow)

	// Bottom border
	lines = append(lines, bottomBorder)

	return strings.Join(lines, "\n")
}
