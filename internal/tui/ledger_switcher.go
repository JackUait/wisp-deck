package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func (m *LedgerModel) handleAccountSwitchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.accountSwitchOpen = false
		return nil
	case "up", "k":
		m.accountSwitchCursor = m.previousReadySwitchOption(m.accountSwitchCursor)
	case "down", "j":
		m.accountSwitchCursor = m.nextReadySwitchOption(m.accountSwitchCursor)
	case "enter":
		if m.accountSwitchCursor < 0 || m.accountSwitchCursor >= len(m.session.SwitchOptions) {
			return nil
		}
		option := m.session.SwitchOptions[m.accountSwitchCursor]
		if !option.Ready {
			return nil
		}
		return m.applyAccountSwitch(option.Choice)
	}
	return nil
}

func (m *LedgerModel) handleAccountSwitchMouse(msg tea.MouseMsg) tea.Cmd {
	card := m.accountSwitchCard()
	left, top := ledgerCenteredCard(m.width, m.height, card)
	firstRow := top + 4 // border + top padding + title + blank line
	index := msg.Y - firstRow
	cardWidth := lipgloss.Width(card)
	onRow := msg.X >= left && msg.X < left+cardWidth &&
		index >= 0 && index < len(m.session.SwitchOptions)
	if msg.Action == tea.MouseActionMotion {
		if onRow && m.session.SwitchOptions[index].Ready {
			m.accountSwitchCursor = index
		}
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !onRow {
		m.accountSwitchOpen = false
		return nil
	}
	option := m.session.SwitchOptions[index]
	if !option.Ready {
		return nil
	}
	m.accountSwitchCursor = index
	return m.applyAccountSwitch(option.Choice)
}

func (m *LedgerModel) nextReadySwitchOption(from int) int {
	for index := from + 1; index < len(m.session.SwitchOptions); index++ {
		if m.session.SwitchOptions[index].Ready {
			return index
		}
	}
	return from
}

func (m *LedgerModel) previousReadySwitchOption(from int) int {
	for index := from - 1; index >= 0; index-- {
		if m.session.SwitchOptions[index].Ready {
			return index
		}
	}
	return from
}

func (m *LedgerModel) accountSwitchCard() string {
	gray := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	activeDot := lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render("●")
	maxLabel := m.width - 16
	if maxLabel < 8 {
		maxLabel = 8
	}
	lines := []string{title.Render("Switch agent"), ""}
	for index, option := range m.session.SwitchOptions {
		color := option.Color
		if color == 0 {
			color = 244
		}
		if index != m.accountSwitchCursor && !option.Active {
			color = 244
		}
		if !option.Ready {
			color = 244
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("%d", color)))
		glyph := option.Glyph
		if glyph == "" {
			glyph = "󰀄"
		}
		label := runewidth.Truncate(option.Label, maxLabel, "…")
		marker := "  "
		if index == m.accountSwitchCursor {
			marker = style.Bold(true).Render("▌ ")
			label = style.Bold(true).Render(glyph + " " + label)
		} else {
			label = style.Render(glyph + " " + label)
		}
		line := marker + label
		if option.Active {
			line += "  " + activeDot
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", gray.Render("↑↓ move · ⏎ switch · esc cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

func ledgerCenteredCard(width, height int, card string) (left, top int) {
	left = (width - lipgloss.Width(card)) / 2
	top = (height - lipgloss.Height(card)) / 2
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	return left, top
}

func (m *LedgerModel) overlayAccountSwitch(base string) string {
	card := m.accountSwitchCard()
	cardLines := strings.Split(card, "\n")
	left, top := ledgerCenteredCard(m.width, m.height, card)
	cardWidth := lipgloss.Width(card)
	right := left + cardWidth
	if right > m.width {
		right = m.width
	}
	background := strings.Split(base, "\n")
	dim := aboutDimStyle()
	rowAt := func(y int) []rune {
		row := make([]rune, m.width)
		for index := range row {
			row[index] = ' '
		}
		if y < len(background) {
			plain := aboutAnsiRe.ReplaceAllString(background[y], "")
			for index, r := range []rune(plain) {
				if index >= m.width {
					break
				}
				row[index] = r
			}
		}
		return row
	}
	var out strings.Builder
	for y := 0; y < m.height; y++ {
		row := rowAt(y)
		cardIndex := y - top
		if cardIndex >= 0 && cardIndex < len(cardLines) {
			out.WriteString(dim.Render(string(row[:left])))
			out.WriteString(cardLines[cardIndex])
			if right < m.width {
				out.WriteString(dim.Render(string(row[right:])))
			}
		} else {
			out.WriteString(dim.Render(string(row)))
		}
		if y < m.height-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
