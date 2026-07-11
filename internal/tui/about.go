package tui

import (
	"regexp"
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

// handleAboutMouse owns all mouse input while the About overlay is open: a
// left click anywhere outside the card dismisses it (mirroring the
// account-switch popup); everything else is swallowed so nothing falls
// through to the menu underneath.
func (m *MainMenuModel) handleAboutMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		left, top, w, h := m.aboutCardLayout()
		onCard := msg.X >= left && msg.X < left+w && msg.Y >= top && msg.Y < top+h
		if !onCard {
			m.aboutOpen = false
		}
	}
	return m, nil
}

// aboutInnerLines is the card's content block: the version (when known), the
// credits, and a dim help footer hugging the bottom border — mirroring the
// account-switch card's shape (title on the border, footer as closing chrome).
func (m *MainMenuModel) aboutInnerLines() []string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	versionStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)

	var lines []string
	if m.appVersion != "" {
		lines = append(lines, versionStyle.Render("Version "+m.appVersion), "")
	}
	lines = append(lines,
		"Made by Evgeniy Pyatkov (@jackuait)",
		dimStyle.Render("Telegram: @that_ai_guy — https://t.me/that_ai_guy"),
		"",
		dimStyle.Render("Esc close"),
	)
	return lines
}

// aboutContentWidth is the widest inner line, sizing the card and bounding
// mouse clicks.
func (m *MainMenuModel) aboutContentWidth() int {
	w := 0
	for _, l := range m.aboutInnerLines() {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}

// About card chrome constants (kept in sync with aboutCardStyle).
const (
	aboutCardPadX = 2
	aboutCardPadY = 1 // top only — the help footer hugs the bottom border
)

// renderAboutCard draws the floating About card with the same chrome as the
// account-switch modal: a thin rounded gray border, NO background of its own
// (so its corners round cleanly against the same-gray faint surround), and the
// title embedded in the top border.
func (m *MainMenuModel) renderAboutCard() string {
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(aboutCardPadY, aboutCardPadX, 0, aboutCardPadX).
		Render(strings.Join(m.aboutInnerLines(), "\n"))
	return embedAboutBorderTitle(card, "About Wisp Deck", m.aboutContentWidth()+2*aboutCardPadX)
}

// embedAboutBorderTitle rewrites the card's top border as
// "╭─ <title> ─────╮" — lipgloss has no border-title support. innerW is the
// width between the corner glyphs; a title too wide to fit leaves the border
// as-is.
func embedAboutBorderTitle(card, title string, innerW int) string {
	rest := innerW - lipgloss.Width(title) - 3 // "─ " before, " " after
	if rest < 1 {
		return card
	}
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	top := borderStyle.Render("╭─") + " " + titleStyle.Render(title) + " " +
		borderStyle.Render(strings.Repeat("─", rest)+"╮")
	lines := strings.Split(card, "\n")
	lines[0] = top
	return strings.Join(lines, "\n")
}

// aboutCardLayout returns the centered card's absolute screen geometry
// (left, top, width, height), shared by the renderer and the mouse handler so
// they can never drift apart.
func (m *MainMenuModel) aboutCardLayout() (left, top, w, h int) {
	w = m.aboutContentWidth() + 2*aboutCardPadX + 2 // + borders
	h = len(m.aboutInnerLines()) + aboutCardPadY + 2
	left = (m.width - w) / 2
	if left < 0 {
		left = 0
	}
	top = (m.height - h) / 2
	if top < 0 {
		top = 0
	}
	return left, top, w, h
}

// aboutDimStyle dims the screen around the card by FAINTNESS only — no dark
// background tint — matching the account-switch and diff modals: a terminal
// cell is one solid color, so the card's corners can only round against a
// same-gray surround. Built per render, NOT held in a package var: the app
// rebinds lipgloss's default renderer to /dev/tty at startup (stdout is bash's
// capture pipe), and a style created at package init would keep the pre-switch
// Ascii renderer and paint the backdrop plain.
func aboutDimStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Faint(true).
		Foreground(lipgloss.Color("240"))
}

var aboutAnsiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// overlayAbout composites the About card over the already-placed full-screen
// view: every backdrop line is stripped of its colors and repainted faint
// gray, with the card lines laid centered on top.
func (m *MainMenuModel) overlayAbout(placed string) string {
	bg := strings.Split(placed, "\n")
	aboutDim := aboutDimStyle()
	cardLeft, cardTop, cardWidth, _ := m.aboutCardLayout()
	cardLines := strings.Split(m.renderAboutCard(), "\n")

	bgRow := func(y int) []rune {
		row := make([]rune, m.width)
		for i := range row {
			row[i] = ' '
		}
		if y < len(bg) {
			for i, r := range []rune(aboutAnsiRe.ReplaceAllString(bg[y], "")) {
				if i >= m.width {
					break
				}
				row[i] = r
			}
		}
		return row
	}

	right := cardLeft + cardWidth
	if right > m.width {
		right = m.width
	}
	var b strings.Builder
	for y := 0; y < m.height; y++ {
		row := bgRow(y)
		ci := y - cardTop
		if ci >= 0 && ci < len(cardLines) {
			b.WriteString(aboutDim.Render(string(row[:cardLeft])))
			b.WriteString(cardLines[ci])
			if right < m.width {
				b.WriteString(aboutDim.Render(string(row[right:])))
			}
		} else {
			b.WriteString(aboutDim.Render(string(row)))
		}
		if y < m.height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
