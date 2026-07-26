package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderSettingsItem renders a single settings item row with state right-aligned.
func (m *MainMenuModel) renderSettingsItem(index int, label, stateText string, stateStyle, brightBoldStyle lipgloss.Style, leftBorder, rightBorder string) string {
	stateRendered := stateStyle.Render(stateText)
	selectedBgStyle := lipgloss.NewStyle().Background(lipgloss.Color(menuSurface))
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
	// Hovered-but-unselected row: a faint cursor marker + subtle wash marks the
	// pointer target, matching the off-focus selected treatment so the two read
	// as the same "this row would act" affordance.
	if m.isHovered(regionSettings) && m.hover.index == index {
		marker := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("▌")
		labelText := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(label)
		prefix := " " + marker + labelText
		gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(stateRendered) - 1
		if gap < 1 {
			gap = 1
		}
		wash := lipgloss.NewStyle().Background(lipgloss.Color(menuSurface))
		return leftBorder + wash.Render(prefix+strings.Repeat(" ", gap)+stateRendered+" ") + rightBorder
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

	// Shared chrome: top border + PLAN switcher + title row + switcher gap +
	// tab bar + separator. The PLAN row mirrors the Projects header so the chrome
	// lines up identically on every tab.
	lines = append(lines, top)
	if m.accountRowCount() > 0 {
		lines = append(lines, m.renderAccountRow(leftBorder, rightBorder))
	}
	if m.subscriptionRowCount() > 0 {
		lines = append(lines, m.renderSubscriptionRow(leftBorder, rightBorder))
	}
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))
	lines = append(lines, m.renderHeaderGapRow(leftBorder, rightBorder))
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)

	// Empty row
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	lines = append(lines, emptyRow)

	// Each settings row is rendered against its stable handler index, then emitted
	// grouped under section headers (see settingsSections). itemLines holds the
	// rendered line(s) for each index.
	itemLines := map[int][]string{}

	// Ghost Display item
	ghostLabel := "Mascot"
	ghostState := "[" + ghostDisplayLabel(m.ghostDisplay) + "]"
	itemLines[0] = []string{m.renderSettingsItem(0, ghostLabel, ghostState, stateStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// Tab Title item
	var tabTitleColor lipgloss.Color
	switch m.tabTitle {
	case "full":
		tabTitleColor = lipgloss.Color("114") // green
	case "model":
		tabTitleColor = lipgloss.Color("75") // blue — AI tool sets its own title
	default:
		tabTitleColor = lipgloss.Color("220") // yellow (project only)
	}
	tabTitleStyle := lipgloss.NewStyle().Foreground(tabTitleColor)
	tabLabel := "Tab title"
	tabState := "[" + tabTitleLabel(m.tabTitle) + "]"
	itemLines[1] = []string{m.renderSettingsItem(1, tabLabel, tabState, tabTitleStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// Sound Notifications item
	var soundColor lipgloss.Color
	if m.soundName != "" {
		soundColor = lipgloss.Color("114") // green
	} else {
		soundColor = lipgloss.Color("241") // gray
	}
	soundStyle := lipgloss.NewStyle().Foreground(soundColor)
	soundLabel := "Idle sound"
	soundState := "[Off]"
	if m.soundName != "" {
		soundState = "[" + m.soundName + "]"
	}
	itemLines[2] = []string{m.renderSettingsItem(2, soundLabel, soundState, soundStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// Theme item — the state swatch is painted in the live (resolved) accent so
	// the row previews the chosen palette's color.
	themeStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)
	themeState := "[" + themeLabel(m.themePref) + "]"
	itemLines[3] = []string{m.renderSettingsItem(3, "Theme", themeState, themeStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// Usage bars item — which statusline usage pills show (7d / 5h / both / none).
	// Green when a bar is shown, gray when off (none), mirroring the other rows.
	var usageColor lipgloss.Color
	if m.usageBars == "none" {
		usageColor = lipgloss.Color("241") // gray (off)
	} else {
		usageColor = lipgloss.Color("114") // green
	}
	usageStyle := lipgloss.NewStyle().Foreground(usageColor)
	usageState := "[" + usageBarsLabel(m.usageBars) + "]"
	itemLines[rowUsageBars] = []string{m.renderSettingsItem(rowUsageBars, "Usage bars", usageState, usageStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// AI tools item — opens the tool panel (install a tool, pick the default).
	// The state shows the current default; gray when nothing is selectable yet.
	toolsColor := lipgloss.Color("114") // green
	if m.CurrentAITool() == "" {
		toolsColor = lipgloss.Color("241")
	}
	toolsStyle := lipgloss.NewStyle().Foreground(toolsColor)
	toolsState := "[" + AIToolDisplayName(m.CurrentAITool()) + "]"
	itemLines[rowAITools] = []string{m.renderSettingsItem(rowAITools, "AI tools", toolsState, toolsStyle, primaryBoldStyle, leftBorder, rightBorder)}

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
		itemLines[rowSubscription] = []string{m.renderSettingsItem(rowSubscription, "Subscription", state, cfgStyle, primaryBoldStyle, leftBorder, rightBorder)}
	}

	// Auto-switch accounts item: toggles the automatic account-rotation proxy
	// (distinct from the Login row above, which switches the active login by hand).
	var autoColor lipgloss.Color
	autoState := "[Off]"
	if m.AutoSwitchEnabled() {
		autoColor = lipgloss.Color("114") // green when on
		autoState = "[On]"
	} else {
		autoColor = lipgloss.Color("241") // gray when off
	}
	autoStyle := lipgloss.NewStyle().Foreground(autoColor)
	itemLines[m.autoSwitchRowIndex()] = []string{m.renderSettingsItem(m.autoSwitchRowIndex(), "Auto-switch accounts", autoState, autoStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// Keep-awake item: holds the kernel sleep veto while an agent is mid-turn so
	// a closed lid does not suspend the machine.
	var wakeColor lipgloss.Color
	wakeState := "[Off]"
	if m.KeepAwakeEnabled() {
		wakeColor = lipgloss.Color("114") // green when on
		wakeState = "[On]"
	} else {
		wakeColor = lipgloss.Color("241") // gray when off
	}
	wakeStyle := lipgloss.NewStyle().Foreground(wakeColor)
	itemLines[m.keepAwakeRowIndex()] = []string{m.renderSettingsItem(m.keepAwakeRowIndex(), "Keep awake while working", wakeState, wakeStyle, primaryBoldStyle, leftBorder, rightBorder)}

	// Emit each section: a header row (blank-separated from the prior section)
	// followed by its item rows in visual order. The initial emptyRow above stands
	// in for the blank before the first header.
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	for si, section := range m.settingsSections() {
		if si > 0 {
			lines = append(lines, emptyRow)
		}
		headerText := headerStyle.Render(strings.ToUpper(section.title))
		headerPrefix := " " + headerText
		headerGap := menuContentWidth - lipgloss.Width(headerPrefix)
		if headerGap < 0 {
			headerGap = 0
		}
		lines = append(lines, leftBorder+headerPrefix+strings.Repeat(" ", headerGap)+rightBorder)
		for _, idx := range section.indices {
			lines = append(lines, itemLines[idx]...)
		}
	}

	// Empty row
	lines = append(lines, emptyRow)

	// Separator before help
	lines = append(lines, separator)

	// Help row — ⏎ manage for the rows that open a panel (Subscription, AI
	// tools), ← → cycle for everything else (Theme, Ghost, Tab, …). Keyed off
	// the row constants: this arm previously matched settingsItemCount()-1,
	// which silently retargeted when a row was appended.
	sep := dimStyle.Render(" · ")
	var cycleOrEdit string
	switch {
	case m.settingsSelected == rowSubscription && m.ClaudeConfigVisible():
		cycleOrEdit = helpStyle.Render("⏎ manage")
	case m.settingsSelected == rowAITools:
		cycleOrEdit = helpStyle.Render("⏎ manage")
	default:
		cycleOrEdit = helpStyle.Render("←→ cycle")
	}
	helpContent := helpStyle.Render("↑↓ navigate") + sep + cycleOrEdit + sep + helpStyle.Render("a about") + sep + helpStyle.Render("Esc close")
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
