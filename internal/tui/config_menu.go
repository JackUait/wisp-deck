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
	TerminalName string
	Version      string
}

// ConfigMenuModel is a custom-rendered Bubbletea model for the config menu.
type ConfigMenuModel struct {
	items    []ConfigMenuItem
	cursor   int
	selected *ConfigMenuItem
	quitting bool
	width    int
}

// GetConfigMenuItems returns the config menu items.
func GetConfigMenuItems() []ConfigMenuItem {
	return []ConfigMenuItem{
		{ItemTitle: "Terminals", ItemDesc: "Add, remove, or switch terminal emulator", Action: "manage-terminals"},
		{ItemTitle: "Manage Claude configs", ItemDesc: "Add, rename, or delete Claude settings profiles", Action: "manage-claude-configs"},
		{ItemTitle: "Reinstall / Update", ItemDesc: "Re-run the installer", Action: "reinstall"},
	}
}

// NewConfigMenu creates a new ConfigMenuModel with the given options.
func NewConfigMenu(opts ConfigMenuOptions) ConfigMenuModel {
	items := GetConfigMenuItems()

	// Set status for Terminals item
	if opts.TerminalName != "" {
		items[0].Status = opts.TerminalName
	} else {
		items[0].Status = "not set"
	}

	// Set status for Reinstall / Update item
	if opts.Version != "" {
		items[2].Status = "v" + opts.Version
	}

	return ConfigMenuModel{
		items: items,
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
		// Cursor + title
		var line string
		if i == m.cursor {
			line = selectedItemStyle.Render(fmt.Sprintf(" %s %s", "\u25b8", item.ItemTitle))
		} else {
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
	title := " Ghost Tab Configuration "
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
