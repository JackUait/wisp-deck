package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ConfigMenuItem represents a single item in the config menu.
type ConfigMenuItem struct {
	ItemTitle string
	ItemDesc  string
	Action    string
	Status    string
}

// Title returns the item title (kept for interface compatibility).
func (i ConfigMenuItem) Title() string { return i.ItemTitle }

// Description returns the item description (kept for interface compatibility).
func (i ConfigMenuItem) Description() string { return i.ItemDesc }

// FilterValue returns the filter value (kept for interface compatibility).
func (i ConfigMenuItem) FilterValue() string { return i.ItemTitle }

// ConfigMenuOptions holds options for creating a ConfigMenuModel.
type ConfigMenuOptions struct {
	Version string
	// AutoSwitch is the current account-rotation setting ("on"/"off"); anything
	// other than "on" displays as Off.
	AutoSwitch string
}

// ConfigMenuModel is a custom-rendered Bubbletea model for the config menu.
type ConfigMenuModel struct {
	items    []ConfigMenuItem
	cursor   int
	hover    int // row under the pointer, or -1 (transient; never moves the cursor)
	selected *ConfigMenuItem
	quitting bool
	width    int
}

// GetConfigMenuItems returns the config menu items.
func GetConfigMenuItems() []ConfigMenuItem {
	return []ConfigMenuItem{
		{ItemTitle: "Manage Claude configs", ItemDesc: "Add, rename, or delete Claude settings profiles", Action: "manage-claude-configs"},
		{ItemTitle: "Auto-switch Claude accounts", ItemDesc: "Rotate between accounts as quota runs out (needs 2+ accounts)", Action: "toggle-auto-switch"},
		{ItemTitle: "Reinstall / Update", ItemDesc: "Re-run the installer", Action: "reinstall"},
	}
}

// onOffStatus renders a setting value as "On"/"Off" for display.
func onOffStatus(v string) string {
	if v == "on" {
		return "On"
	}
	return "Off"
}

// NewConfigMenu creates a new ConfigMenuModel with the given options.
func NewConfigMenu(opts ConfigMenuOptions) ConfigMenuModel {
	items := GetConfigMenuItems()

	for i := range items {
		switch items[i].Action {
		case "toggle-auto-switch":
			items[i].Status = onOffStatus(opts.AutoSwitch)
		case "reinstall":
			if opts.Version != "" {
				items[i].Status = "v" + opts.Version
			}
		}
	}

	return ConfigMenuModel{
		items: items,
		hover: -1,
	}
}

// Init implements tea.Model.
func (m ConfigMenuModel) Init() tea.Cmd {
	return nil
}

// Cursor returns the current cursor position.
func (m ConfigMenuModel) Cursor() int {
	return m.cursor
}

// Update implements tea.Model.
func (m ConfigMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEscape:
			m.selected = &ConfigMenuItem{Action: "quit"}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyUp:
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(m.items) - 1
			}
			return m, nil

		case tea.KeyDown:
			m.cursor++
			if m.cursor >= len(m.items) {
				m.cursor = 0
			}
			return m, nil

		case tea.KeyEnter:
			if m.cursor < len(m.items) {
				item := m.items[m.cursor]
				m.selected = &item
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil

		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				r := TranslateRune(msg.Runes[0])
				switch r {
				case 'j':
					m.cursor++
					if m.cursor >= len(m.items) {
						m.cursor = 0
					}
					return m, nil
				case 'k':
					m.cursor--
					if m.cursor < 0 {
						m.cursor = len(m.items) - 1
					}
					return m, nil
				}
			}
		}
	}

	return m, nil
}

// configMenuItemAt maps an absolute screen row to a menu item index, or -1. The
// box renders at the screen origin (AltScreen, no centering): row 0 is the top
// border, row 1 the top padding, then each item takes a title row, a description
// row, and a trailing blank — so item i occupies rows 2+3i (title) and 3+3i.
func (m ConfigMenuModel) configMenuItemAt(y int) int {
	row := y - 2
	if row < 0 {
		return -1
	}
	if row%3 == 2 { // the blank spacer between items
		return -1
	}
	i := row / 3
	if i >= 0 && i < len(m.items) {
		return i
	}
	return -1
}

// handleMouse gives the config menu pointer parity: hover highlights the row
// under the pointer (a transient layer that clears the moment the pointer leaves
// a row and never moves the keyboard cursor), left-click selects the item under
// it (the Enter action), and the wheel scrolls the cursor.
func (m ConfigMenuModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.cursor = (m.cursor + 1) % len(m.items)
		return m, nil
	case tea.MouseButtonWheelUp:
		m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
		return m, nil
	}
	i := m.configMenuItemAt(msg.Y)
	// Require an actual glyph under the pointer so trailing padding inside the box
	// doesn't register as the row.
	if i >= 0 && !frameCellHasGlyph(strings.Split(m.View(), "\n"), msg.X, msg.Y) {
		i = -1
	}
	switch msg.Action {
	case tea.MouseActionMotion:
		m.hover = i // i is -1 when the pointer is off every row, clearing the highlight
		return m, nil
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft && i >= 0 {
			item := m.items[i]
			m.selected = &item
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m ConfigMenuModel) View() string {
	if m.quitting {
		return ""
	}

	// Use the primary color from titleStyle for the border
	borderColor := titleStyle.GetForeground()

	// Total box width (border + padding + content)
	boxWidth := 56
	if m.width > 0 && m.width < boxWidth {
		boxWidth = m.width
	}

	// Inner content width: boxWidth - 2(border) - 2(padding)
	innerWidth := boxWidth - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	// Build the items content
	var content strings.Builder
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	for i, item := range m.items {
		// Cursor + title. The keyboard cursor shows a bright \u25b8; a hovered-but-not-
		// cursor row shows a dim \u25b8 so the pointer target reads as clickable without
		// masquerading as the keyboard selection.
		var line string
		switch {
		case i == m.cursor:
			line = selectedItemStyle.Render(fmt.Sprintf(" %s %s", "\u25b8", item.ItemTitle))
		case i == m.hover:
			line = dimStyle.Render(" \u25b8 ") + item.ItemTitle
		default:
			line = fmt.Sprintf("   %s", item.ItemTitle)
		}

		// Status right-aligned on the title line
		if item.Status != "" {
			statusStr := dimStyle.Render(item.Status)
			// Calculate visible widths for alignment
			titleVisible := lipgloss.Width(line)
			statusVisible := lipgloss.Width(statusStr)
			gap := innerWidth - titleVisible - statusVisible
			if gap < 2 {
				gap = 2
			}
			line += strings.Repeat(" ", gap) + statusStr
		}

		content.WriteString(line)
		content.WriteString("\n")

		// Description on next line
		content.WriteString("     " + dimStyle.Render(item.ItemDesc))
		if i < len(m.items)-1 {
			content.WriteString("\n\n")
		} else {
			content.WriteString("\n")
		}
	}

	// Help text
	content.WriteString("\n")
	content.WriteString(hintStyle.Render(" \u2191/\u2193 navigate \u2022 Enter select \u2022 Esc quit"))

	// Create border style
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 1).
		Width(boxWidth)

	box := borderStyle.Render(content.String())

	// Overlay title on the top border
	title := " Wisp Deck Configuration "
	titleRendered := lipgloss.NewStyle().
		Foreground(borderColor).
		Bold(true).
		Render(title)

	lines := strings.Split(box, "\n")
	if len(lines) > 0 {
		topBorder := lines[0]
		// Place title after the first few border chars
		topRunes := []rune(topBorder)
		titleRunes := []rune(titleRendered)
		insertPos := 2 // after the rounded corner and a border char

		if len(topRunes) > insertPos+len(titleRunes) {
			// Replace characters in the top border with the title
			result := make([]rune, 0, len(topRunes))
			result = append(result, topRunes[:insertPos]...)
			result = append(result, titleRunes...)
			result = append(result, topRunes[insertPos+len(titleRunes):]...)
			lines[0] = string(result)
		}
		box = strings.Join(lines, "\n")
	}

	return box
}

// Selected returns the selected menu item, or nil if none was selected.
func (m ConfigMenuModel) Selected() *ConfigMenuItem {
	return m.selected
}
