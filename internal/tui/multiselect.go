package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/models"
)

// MultiSelectResult holds the output of a multi-select interaction.
type MultiSelectResult struct {
	Tools     []string `json:"tools"`
	Confirmed bool     `json:"confirmed"`
}

// MultiSelectModel is a checkbox-style multi-select TUI for AI tools.
type MultiSelectModel struct {
	tools    []models.AITool
	checked  []bool
	cursor   int
	result   *MultiSelectResult
	quitting bool
	errorMsg string
}

// NewMultiSelect creates a new multi-select model.
// Claude is always pre-checked. Other tools are pre-checked only if installed.
func NewMultiSelect(tools []models.AITool) MultiSelectModel {
	checked := make([]bool, len(tools))
	for i, t := range tools {
		if t.Name == "claude" {
			checked[i] = true
		} else if t.Installed {
			checked[i] = true
		}
	}

	return MultiSelectModel{
		tools:   tools,
		checked: checked,
		cursor:  0,
	}
}

// Cursor returns the current cursor position.
func (m MultiSelectModel) Cursor() int {
	return m.cursor
}

// Checked returns the checked state of each item.
func (m MultiSelectModel) Checked() []bool {
	out := make([]bool, len(m.checked))
	copy(out, m.checked)
	return out
}

// Result returns the selection result, or nil if not yet confirmed/cancelled.
func (m MultiSelectModel) Result() *MultiSelectResult {
	return m.result
}

// ErrorMsg returns the current error message, or empty string if none.
func (m MultiSelectModel) ErrorMsg() string {
	return m.errorMsg
}

// Init implements tea.Model.
func (m MultiSelectModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m MultiSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Clear error on any key press (before processing the key)
		if m.errorMsg != "" {
			m.errorMsg = ""
		}

		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEscape:
			m.result = &MultiSelectResult{Confirmed: false}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyUp:
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(m.tools) - 1
			}
			return m, nil

		case tea.KeyDown:
			m.cursor++
			if m.cursor >= len(m.tools) {
				m.cursor = 0
			}
			return m, nil

		case tea.KeySpace:
			m.checked[m.cursor] = !m.checked[m.cursor]
			return m, nil

		case tea.KeyEnter:
			// Collect selected tools in list order
			var selected []string
			for i, t := range m.tools {
				if m.checked[i] {
					selected = append(selected, t.Name)
				}
			}

			if len(selected) == 0 {
				m.errorMsg = "Select at least one AI tool"
				return m, nil
			}

			m.result = &MultiSelectResult{
				Tools:     selected,
				Confirmed: true,
			}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				r := TranslateRune(msg.Runes[0])
				switch r {
				case ' ':
					m.checked[m.cursor] = !m.checked[m.cursor]
					return m, nil
				case 'k':
					m.cursor--
					if m.cursor < 0 {
						m.cursor = len(m.tools) - 1
					}
					return m, nil
				case 'j':
					m.cursor++
					if m.cursor >= len(m.tools) {
						m.cursor = 0
					}
					return m, nil
				}
			}
		}
	}

	return m, nil
}

// View implements tea.Model.
func (m MultiSelectModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	b.WriteString(titleStyle.Render("Select AI Tools"))
	b.WriteString("\n\n")

	installedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))

	for i, tool := range m.tools {
		// Cursor indicator
		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render("  ❯ "))
		} else {
			b.WriteString("    ")
		}

		// Checkbox
		if m.checked[i] {
			b.WriteString("[x] ")
		} else {
			b.WriteString("[ ] ")
		}

		// Tool display name
		displayName := installerToolDisplayName(tool.Name)
		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render(displayName))
		} else {
			b.WriteString(displayName)
		}

		// Installed tag
		if tool.Installed {
			b.WriteString("  ")
			b.WriteString(installedStyle.Render("(installed)"))
		}

		b.WriteString("\n")
	}

	// Error message
	if m.errorMsg != "" {
		b.WriteString("\n")
		errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		b.WriteString(errorStyle.Render(fmt.Sprintf("  %s", m.errorMsg)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(hintStyle.Render("  ↑↓ navigate  Space toggle  Enter confirm  Esc cancel"))

	return b.String()
}

// installerToolDisplayName returns the display name for an AI tool in the
// installer context, including the organization in parentheses.
func installerToolDisplayName(name string) string {
	base := AIToolDisplayName(name)
	switch name {
	case "codex":
		return base + " (OpenAI)"
	case "opencode":
		return base + " (anomalyco)"
	default:
		return base
	}
}
