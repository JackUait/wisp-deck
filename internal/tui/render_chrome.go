package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// boxBorderColor is the color of the outer box border. Idle, it is a neutral
// gray so the chrome recedes and the accent is free to mark only the selected
// row. When a focus region outside the body is active the border brightens to
// Primary so the user can see which box "owns" the keyboard at a glance.
func (m *MainMenuModel) boxBorderColor() lipgloss.Color {
	if m.focus != FocusBody {
		return m.theme.Primary
	}
	return lipgloss.Color("240") // neutral gray (matches the rest of the grays)
}

// boxBorders returns the rounded-box border strings shared by every tab body.
func (m *MainMenuModel) boxBorders() (top, separator, bottom, leftBorder, rightBorder string) {
	borderStyle := lipgloss.NewStyle().Foreground(m.boxBorderColor())
	// Inner separators stay a touch dimmer than the outer border so the box
	// reads as one frame rather than a stack of equally-weighted rules.
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	hLine := strings.Repeat("─", menuInnerWidth)
	top = borderStyle.Render("╭" + hLine + "╮")
	separator = sepStyle.Render("├" + hLine + "┤")
	bottom = borderStyle.Render("╰" + hLine + "╯")
	leftBorder = borderStyle.Render("│")
	rightBorder = strings.Repeat(" ", menuPadding) + borderStyle.Render("│")
	return
}

// menuTabLabels is the ordered list of top-level tab labels.
var menuTabLabels = []string{"Projects", "Settings", "Stats"}

// renderTabBar renders the Projects · Settings · Stats row. The active tab is
// wrapped in block accents and styled bold; inactive tabs are dimmed.
func (m *MainMenuModel) renderTabBar(leftBorder, rightBorder string) string {
	navFocused := m.focus == FocusTabs

	// Two treatments for the active tab, by focus:
	//   • navigation focused → a solid filled pill (dark text on Primary), so it
	//     unmistakably reads as "this section is selected, ←/→ switches it".
	//   • body focused → a quiet bold underline that just marks the current
	//     section without competing with the project list's selection.
	// Inactive tabs brighten slightly while navigating to read as reachable.
	// Both treatments keep the " label " width so the row math is unchanged.
	var activeStyle lipgloss.Style
	inactiveColor := lipgloss.Color("245")
	if navFocused {
		activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")). // near-black ink on the pill
			Background(m.theme.Primary).
			Bold(true)
		inactiveColor = lipgloss.Color("250")
	} else {
		activeStyle = lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true).Underline(true)
	}
	inactiveStyle := lipgloss.NewStyle().Foreground(inactiveColor)

	var parts []string
	for i, label := range menuTabLabels {
		if MenuTab(i) == m.activeTab {
			parts = append(parts, activeStyle.Render(" "+label+" "))
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
