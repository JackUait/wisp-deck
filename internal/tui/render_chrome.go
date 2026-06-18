package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// boxBorders returns the rounded-box border strings shared by every tab body.
func (m *MainMenuModel) boxBorders() (top, separator, bottom, leftBorder, rightBorder string) {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	hLine := strings.Repeat("─", menuInnerWidth)
	top = dimStyle.Render("╭" + hLine + "╮")
	separator = dimStyle.Render("├" + hLine + "┤")
	bottom = dimStyle.Render("╰" + hLine + "╯")
	leftBorder = dimStyle.Render("│")
	rightBorder = strings.Repeat(" ", menuPadding) + dimStyle.Render("│")
	return
}

// menuTabLabels is the ordered list of top-level tab labels.
var menuTabLabels = []string{"Projects", "Settings", "Stats"}

// renderTabBar renders the Projects · Settings · Stats row. The active tab is
// wrapped in block accents and styled bold; inactive tabs are dimmed.
func (m *MainMenuModel) renderTabBar(leftBorder, rightBorder string) string {
	// When the tab bar holds focus, the active tab brightens to signal that ←/→
	// will switch sections; otherwise it stays the dimmer Primary.
	activeColor := m.theme.Primary
	if m.focus == FocusTabs {
		activeColor = m.theme.Bright
	}
	activeStyle := lipgloss.NewStyle().Foreground(activeColor).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)

	var parts []string
	for i, label := range menuTabLabels {
		if MenuTab(i) == m.activeTab {
			parts = append(parts, activeStyle.Render("▌"+label+"▐"))
		} else {
			parts = append(parts, inactiveStyle.Render(" "+label+" "))
		}
	}
	content := strings.Join(parts, "  ")
	gap := menuContentWidth - lipgloss.Width(content) - 1
	if gap < 0 {
		gap = 0
	}
	return leftBorder + " " + content + strings.Repeat(" ", gap) + rightBorder
}
