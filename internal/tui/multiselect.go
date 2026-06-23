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
	hover    int // tool row under the pointer, or -1 (transient; never moves the cursor)
	result   *MultiSelectResult
	quitting bool
	errorMsg string
	btnHover bool // pointer is over the Confirm button
}

// multiSelectConfirmLabel is the clickable Confirm button text (mouse parity for
// the Enter key).
const multiSelectConfirmLabel = "[ Confirm ]"

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
		hover:   -1,
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
	case tea.MouseMsg:
		return m.handleMouse(msg)

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
			return m, m.confirm()

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

// confirm collects the checked tools, validating that at least one is picked.
// Shared by the Enter key and the Confirm button. Returns the resulting command.
func (m *MultiSelectModel) confirm() tea.Cmd {
	var selected []string
	for i, t := range m.tools {
		if m.checked[i] {
			selected = append(selected, t.Name)
		}
	}
	if len(selected) == 0 {
		m.errorMsg = "Select at least one AI tool"
		return nil
	}
	m.result = &MultiSelectResult{Tools: selected, Confirmed: true}
	m.quitting = true
	return tea.Quit
}

// multiSelectConfirmRow returns the screen row of the Confirm button. The view
// renders at the origin: title(0) blank(1) tools(2..) blank Confirm.
func (m MultiSelectModel) multiSelectConfirmRow() int {
	return 2 + len(m.tools) + 1
}

// handleMouse gives the multi-select pointer parity: hover highlights the tool
// row under the pointer (a transient layer that clears the moment the pointer
// leaves a row and never moves the keyboard cursor) or the Confirm button,
// clicking a tool toggles its checkbox, clicking Confirm finalizes, and the
// wheel scrolls the cursor.
func (m MultiSelectModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.cursor = (m.cursor + 1) % len(m.tools)
		return m, nil
	case tea.MouseButtonWheelUp:
		m.cursor = (m.cursor - 1 + len(m.tools)) % len(m.tools)
		return m, nil
	}

	toolIdx := -1
	if i := msg.Y - 2; i >= 0 && i < len(m.tools) {
		toolIdx = i
	}
	// Require a glyph under the pointer so the blank area past a tool's label
	// doesn't register as that row.
	if toolIdx >= 0 && !frameCellHasGlyph(strings.Split(m.View(), "\n"), msg.X, msg.Y) {
		toolIdx = -1
	}
	onConfirm := msg.Y == m.multiSelectConfirmRow() &&
		msg.X >= 2 && msg.X < 2+lipgloss.Width(multiSelectConfirmLabel)

	switch msg.Action {
	case tea.MouseActionMotion:
		m.btnHover = onConfirm
		// Transient highlight only: it clears (toolIdx == -1) the moment the pointer
		// leaves the tool rows, and never moves the keyboard cursor.
		m.hover = toolIdx
		return m, nil
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		m.errorMsg = ""
		if onConfirm {
			return m, m.confirm()
		}
		if toolIdx >= 0 {
			m.cursor = toolIdx
			m.checked[toolIdx] = !m.checked[toolIdx]
		}
		return m, nil
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

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	for i, tool := range m.tools {
		// Cursor indicator. The keyboard cursor is a bright ❯; a hovered-but-not-
		// cursor row gets a dim ❯ so the pointer target reads as distinct from the
		// keyboard selection. The marker clears once the pointer leaves the row.
		switch {
		case i == m.cursor:
			b.WriteString(selectedItemStyle.Render("  ❯ "))
		case i == m.hover:
			b.WriteString(dimStyle.Render("  ❯ "))
		default:
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

	// Clickable Confirm button (mouse parity for Enter); kept directly under the
	// tool list so its row is fixed regardless of the optional error line.
	b.WriteString("\n")
	confirmStyle := hintStyle
	if m.btnHover {
		confirmStyle = selectedItemStyle.Copy().Reverse(true)
	}
	b.WriteString("  " + confirmStyle.Render(multiSelectConfirmLabel) + "\n")

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
	case "opencode":
		return base + " (anomalyco)"
	default:
		return base
	}
}
