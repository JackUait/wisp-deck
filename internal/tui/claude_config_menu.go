package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ClaudeConfigMenuResult is the chosen action emitted as JSON by the subcommand.
type ClaudeConfigMenuResult struct {
	Action string `json:"action"`         // add | rename | delete | quit
	File   string `json:"file,omitempty"` // target filename for rename/delete
	Name   string `json:"name,omitempty"` // entered name for add/rename
}

type ccmMode int

const (
	ccmList ccmMode = iota
	ccmAddInput
	ccmRenameInput
	ccmDeleteConfirm
)

// ClaudeConfigMenuModel is a Bubbletea model for managing Claude config files.
type ClaudeConfigMenuModel struct {
	configs  []ClaudeConfig // managed configs (no Standard)
	cursor   int            // 0..len(configs)-1 = configs; len = "Add new config…"
	mode     ccmMode
	input    textinput.Model
	result   *ClaudeConfigMenuResult
	quitting bool
	width    int
}

// NewClaudeConfigMenu creates a new config management menu model.
func NewClaudeConfigMenu(configs []ClaudeConfig) ClaudeConfigMenuModel {
	ti := textinput.New()
	ti.Placeholder = "config name"
	return ClaudeConfigMenuModel{configs: configs, input: ti}
}

// Init implements tea.Model.
func (m ClaudeConfigMenuModel) Init() tea.Cmd { return nil }

// Result returns the chosen action, or nil if none yet.
func (m ClaudeConfigMenuModel) Result() *ClaudeConfigMenuResult { return m.result }

// addRowIndex returns the index of the "Add new config…" row.
func (m ClaudeConfigMenuModel) addRowIndex() int { return len(m.configs) }

// Update implements tea.Model.
func (m ClaudeConfigMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch m.mode {
	case ccmAddInput, ccmRenameInput:
		switch key.Type {
		case tea.KeyEnter:
			name := m.input.Value()
			if name == "" {
				return m, nil
			}
			if m.mode == ccmAddInput {
				m.result = &ClaudeConfigMenuResult{Action: "add", Name: name}
			} else {
				m.result = &ClaudeConfigMenuResult{Action: "rename", File: m.configs[m.cursor].File, Name: name}
			}
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.mode = ccmList
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case ccmDeleteConfirm:
		switch key.String() {
		case "y", "Y":
			m.result = &ClaudeConfigMenuResult{Action: "delete", File: m.configs[m.cursor].File}
			m.quitting = true
			return m, tea.Quit
		default:
			m.mode = ccmList
			return m, nil
		}

	default: // ccmList
		switch key.String() {
		case "q", "esc", "ctrl+c":
			m.result = &ClaudeConfigMenuResult{Action: "quit"}
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < m.addRowIndex() {
				m.cursor++
			}
		case "a":
			m.mode = ccmAddInput
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case "r":
			if m.cursor < len(m.configs) {
				m.mode = ccmRenameInput
				m.input.SetValue(m.configs[m.cursor].Name)
				m.input.Focus()
				return m, textinput.Blink
			}
		case "d":
			if m.cursor < len(m.configs) {
				m.mode = ccmDeleteConfirm
			}
		case "enter":
			if m.cursor == m.addRowIndex() {
				m.mode = ccmAddInput
				m.input.SetValue("")
				m.input.Focus()
				return m, textinput.Blink
			}
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m ClaudeConfigMenuModel) View() string {
	if m.quitting {
		return ""
	}
	return renderClaudeConfigMenu(m)
}

func renderClaudeConfigMenu(m ClaudeConfigMenuModel) string {
	borderColor := titleStyle.GetForeground()

	boxWidth := 56
	if m.width > 0 && m.width < boxWidth {
		boxWidth = m.width
	}

	innerWidth := boxWidth - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	var content strings.Builder

	// Render input or delete confirm overlay at top if in those modes.
	switch m.mode {
	case ccmAddInput:
		content.WriteString(hintStyle.Render("New config name:"))
		content.WriteString("\n")
		content.WriteString(m.input.View())
		content.WriteString("\n\n")
		content.WriteString(hintStyle.Render("Enter confirm • Esc cancel"))
		return renderBox(content.String(), boxWidth, borderColor, " Manage Configs ")

	case ccmRenameInput:
		content.WriteString(hintStyle.Render("Rename config:"))
		content.WriteString("\n")
		content.WriteString(m.input.View())
		content.WriteString("\n\n")
		content.WriteString(hintStyle.Render("Enter confirm • Esc cancel"))
		return renderBox(content.String(), boxWidth, borderColor, " Manage Configs ")

	case ccmDeleteConfirm:
		var name string
		if m.cursor < len(m.configs) {
			name = m.configs[m.cursor].Name
		}
		content.WriteString(fmt.Sprintf("Delete %q? ", name))
		content.WriteString(hintStyle.Render("[y/N]"))
		return renderBox(content.String(), boxWidth, borderColor, " Manage Configs ")
	}

	// ccmList: render config rows + add row + help footer
	for i, cfg := range m.configs {
		var line string
		if i == m.cursor {
			line = selectedItemStyle.Render(fmt.Sprintf(" ▸ %s", cfg.Name))
		} else {
			line = fmt.Sprintf("   %s", cfg.Name)
		}

		// Right-align filename
		fileStr := hintStyle.Render(cfg.File)
		lineVisible := lipgloss.Width(line)
		fileVisible := lipgloss.Width(fileStr)
		gap := innerWidth - lineVisible - fileVisible
		if gap < 2 {
			gap = 2
		}
		line += strings.Repeat(" ", gap) + fileStr

		content.WriteString(line)
		content.WriteString("\n")
	}

	// Add row
	addIdx := m.addRowIndex()
	if m.cursor == addIdx {
		content.WriteString(selectedItemStyle.Render(fmt.Sprintf(" ▸ Add new config…")))
	} else {
		content.WriteString(fmt.Sprintf("   Add new config…"))
	}

	// Help footer
	content.WriteString("\n\n")
	content.WriteString(hintStyle.Render(" ↑/↓ move • a add • r rename • d delete • q quit"))

	return renderBox(content.String(), boxWidth, borderColor, " Manage Configs ")
}

// renderBox draws a rounded-border box with an inset title on the top border.
func renderBox(inner string, boxWidth int, borderColor lipgloss.TerminalColor, title string) string {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 1).
		Width(boxWidth)

	box := borderStyle.Render(inner)

	titleRendered := lipgloss.NewStyle().
		Foreground(borderColor).
		Bold(true).
		Render(title)

	lines := strings.Split(box, "\n")
	if len(lines) > 0 {
		// The top border line may carry ANSI colour sequences from the border
		// style, so we cannot slice by raw rune index.  Instead, walk the
		// string tracking *visible* column position and split at the two points
		// we need: after column insertPos, and after column insertPos+titleVis.
		titleVis := lipgloss.Width(titleRendered)
		insertPos := 2
		top := lines[0]

		// Collect the string up to visible column insertPos, then skip
		// titleVis visible columns, then take the rest.
		var before, after strings.Builder
		col := 0
		i := 0
		runes := []rune(top)
		n := len(runes)

		for i < n {
			r := runes[i]
			if r == '\x1b' {
				// Consume entire escape sequence (skip non-printable bytes).
				seq := []rune{r}
				i++
				for i < n && runes[i] != 'm' {
					seq = append(seq, runes[i])
					i++
				}
				if i < n {
					seq = append(seq, runes[i]) // 'm'
					i++
				}
				if col <= insertPos {
					before.WriteString(string(seq))
				} else {
					after.WriteString(string(seq))
				}
				continue
			}
			if col < insertPos {
				before.WriteRune(r)
			} else if col >= insertPos+titleVis {
				after.WriteRune(r)
			}
			col++
			i++
		}

		if col > insertPos+titleVis {
			lines[0] = before.String() + titleRendered + after.String()
		}
		box = strings.Join(lines, "\n")
	}

	return box
}
