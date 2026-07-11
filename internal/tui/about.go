package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// updateAbout handles key input while the About card is open. Any of Esc /
// Ctrl+C / 'a' / Enter dismisses it; everything else is swallowed so the card
// stays modal.
func (m *MainMenuModel) updateAbout(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC, tea.KeyEnter:
		m.aboutOpen = false
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'a' || msg.Runes[0] == 'A' || msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.aboutOpen = false
		}
	}
	return m, nil
}

// renderAboutPanel draws the About credit card below the menu, reusing the same
// box chrome as the account / model-map panels.
func (m *MainMenuModel) renderAboutPanel() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))

	hLine := strings.Repeat("─", menuInnerWidth)
	topBorder := dimStyle.Render("╭" + hLine + "╮")
	separator := dimStyle.Render("├" + hLine + "┤")
	bottomBorder := dimStyle.Render("╰" + hLine + "╯")
	leftBorder := dimStyle.Render("│")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("│")

	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	// pad places left-aligned content within the content column, right border kept
	// aligned regardless of the content's rendered width.
	pad := func(content string) string {
		gap := menuContentWidth - lipgloss.Width(content) - 1
		if gap < 0 {
			gap = 0
		}
		return leftBorder + " " + content + strings.Repeat(" ", gap) + rightBorder
	}

	var lines []string
	lines = append(lines, topBorder)
	lines = append(lines, pad(primaryBoldStyle.Render("About Wisp Deck")))
	lines = append(lines, separator)
	lines = append(lines, emptyRow)
	lines = append(lines, pad("Made by Evgeniy Pyatkov (@jackuait)"))
	lines = append(lines, pad(dimStyle.Render("Telegram: @that_ai_guy — https://t.me/that_ai_guy")))
	lines = append(lines, emptyRow)
	lines = append(lines, separator)
	lines = append(lines, pad(helpStyle.Render("Esc close")))
	lines = append(lines, bottomBorder)

	return strings.Join(lines, "\n")
}
