package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/models"
)

// BranchDeletedMsg is sent after an async branch deletion completes.
type BranchDeletedMsg struct {
	Branch string
	Err    error
}

// BranchPickerDoneMsg is relayed by AppModel to MainMenuModel after the branch
// picker is popped from the navigation stack.
type BranchPickerDoneMsg struct {
	Selected bool
	Branch   string
}

// BranchPickerModel lets the user pick a branch from a filterable list.
// It renders with box-drawing borders matching the main menu style.
type BranchPickerModel struct {
	allBranches    []string
	filtered       []string
	filtering      bool // whether filter mode is active (activated by '/')
	filterText     string
	cursor         int // index in filtered list
	hover          int // filtered-list index under the pointer, or -1 (transient)
	offset         int // scroll offset for visible window
	selected       *string
	quitting       bool
	width          int
	height         int
	theme          AIToolTheme
	projectPath    string
	deleteMode     bool
	deleteSelected int
	deleteOffset   int
	feedback       string
	feedbackIsErr  bool
}

// NewBranchPicker creates a branch picker with the given branch names, theme, and project path.
func NewBranchPicker(branches []string, theme AIToolTheme, projectPath string) BranchPickerModel {
	filtered := make([]string, len(branches))
	copy(filtered, branches)

	return BranchPickerModel{
		allBranches: branches,
		filtered:    filtered,
		theme:       theme,
		projectPath: projectPath,
		hover:       -1,
	}
}

func (m BranchPickerModel) Init() tea.Cmd {
	return nil
}

func (m BranchPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampScroll()
		return m, nil

	case BranchDeletedMsg:
		m.deleteMode = false
		if msg.Err != nil {
			m.feedback = msg.Err.Error()
			m.feedbackIsErr = true
		} else {
			m.feedback = "Deleted " + msg.Branch
			m.feedbackIsErr = false
			m.removeBranch(msg.Branch)
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		// Clear feedback on any keypress after deletion
		if m.feedback != "" {
			m.feedback = ""
			m.feedbackIsErr = false
			return m, nil
		}

		if m.deleteMode {
			return m.updateDeleteMode(msg)
		}

		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.filtering {
				m.filtering = false
				m.filterText = ""
				m.applyFilter()
				return m, nil
			}
			// Ctrl-C quits immediately; Esc pops the navigation stack.
			if msg.Type == tea.KeyCtrlC {
				m.quitting = true
				return m, tea.Quit
			}
			return m, func() tea.Msg { return PopScreenMsg{} }

		case tea.KeyEnter:
			if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
				name := m.filtered[m.cursor]
				m.selected = &name
			}
			m.quitting = true
			return m, func() tea.Msg { return PopScreenMsg{} }

		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
				m.clampScroll()
			}
			return m, nil

		case tea.KeyDown:
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.clampScroll()
			}
			return m, nil

		case tea.KeyBackspace:
			if m.filtering && len(m.filterText) > 0 {
				m.filterText = m.filterText[:len(m.filterText)-1]
				m.applyFilter()
			}
			return m, nil

		case tea.KeyRunes:
			r := msg.Runes[0]
			// '/' activates filter mode
			if !m.filtering && r == '/' {
				m.filtering = true
				return m, nil
			}
			// When not filtering, handle command keys
			if !m.filtering {
				if r == 'k' {
					if m.cursor > 0 {
						m.cursor--
						m.clampScroll()
					}
					return m, nil
				}
				if r == 'j' {
					if m.cursor < len(m.filtered)-1 {
						m.cursor++
						m.clampScroll()
					}
					return m, nil
				}
				if r == 'd' && len(m.filtered) > 0 {
					m.deleteMode = true
					m.deleteSelected = 0
					return m, nil
				}
				return m, nil
			}
			// In filter mode, add to filter text
			m.filterText += string(msg.Runes)
			m.applyFilter()
			return m, nil
		}
	}

	return m, nil
}

func (m BranchPickerModel) updateDeleteMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.deleteMode = false
		return m, nil

	case tea.KeyUp:
		if m.deleteSelected > 0 {
			m.deleteSelected--
		} else {
			m.deleteSelected = len(m.allBranches) - 1
		}
		m.clampDeleteScroll()
		return m, nil

	case tea.KeyDown:
		if m.deleteSelected < len(m.allBranches)-1 {
			m.deleteSelected++
		} else {
			m.deleteSelected = 0
		}
		m.clampDeleteScroll()
		return m, nil

	case tea.KeyEnter:
		if m.deleteSelected < len(m.allBranches) {
			branch := m.allBranches[m.deleteSelected]
			projectPath := m.projectPath
			return m, func() tea.Msg {
				err := models.DeleteBranch(projectPath, branch)
				return BranchDeletedMsg{Branch: branch, Err: err}
			}
		}
		return m, nil

	case tea.KeyRunes:
		r := msg.Runes[0]
		switch {
		case r == 'q' || r == 'Q':
			m.deleteMode = false
			return m, nil
		case r == 'k':
			if m.deleteSelected > 0 {
				m.deleteSelected--
			} else {
				m.deleteSelected = len(m.allBranches) - 1
			}
			m.clampDeleteScroll()
			return m, nil
		case r == 'j':
			if m.deleteSelected < len(m.allBranches)-1 {
				m.deleteSelected++
			} else {
				m.deleteSelected = 0
			}
			m.clampDeleteScroll()
			return m, nil
		}
	}
	return m, nil
}

func (m *BranchPickerModel) removeBranch(branch string) {
	var newAll []string
	for _, b := range m.allBranches {
		if b != branch {
			newAll = append(newAll, b)
		}
	}
	m.allBranches = newAll
	m.applyFilter()
	if m.cursor >= len(m.filtered) && m.cursor > 0 {
		m.cursor = len(m.filtered) - 1
	}
	m.clampScroll()
}

func (m *BranchPickerModel) applyFilter() {
	if m.filterText == "" {
		m.filtered = make([]string, len(m.allBranches))
		copy(m.filtered, m.allBranches)
	} else {
		lower := strings.ToLower(m.filterText)
		m.filtered = nil
		for _, b := range m.allBranches {
			if strings.Contains(strings.ToLower(b), lower) {
				m.filtered = append(m.filtered, b)
			}
		}
	}
	m.cursor = 0
	m.offset = 0
	m.hover = -1
}

func (m *BranchPickerModel) clampScroll() {
	visible := m.visibleItemCount()
	if visible <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
}

// visibleItemCount returns how many branch items fit in the select box.
// Box layout: top border, title row, separator, filter row, empty row,
// ... items ..., empty row, separator, help row, bottom border.
// That's 9 fixed rows. Items get the rest.
func (m BranchPickerModel) visibleItemCount() int {
	count := m.height - 9
	if count < 1 {
		count = 1
	}
	return count
}

// deleteVisibleCount returns how many branch items fit in the delete box.
// Box layout: top border, title row, separator, empty row,
// ... items ..., empty row, separator, help row, bottom border.
// That's 8 fixed rows. Items get the rest.
func (m BranchPickerModel) deleteVisibleCount() int {
	count := m.height - 8
	if count < 1 {
		count = 1
	}
	return count
}

func (m *BranchPickerModel) clampDeleteScroll() {
	visible := m.deleteVisibleCount()
	if visible <= 0 {
		return
	}
	if m.deleteSelected < m.deleteOffset {
		m.deleteOffset = m.deleteSelected
	}
	if m.deleteSelected >= m.deleteOffset+visible {
		m.deleteOffset = m.deleteSelected - visible + 1
	}
}

func (m BranchPickerModel) View() string {
	if m.quitting {
		return ""
	}

	if m.deleteMode {
		return m.renderDeleteBox()
	}

	return m.renderSelectBox()
}

func (m BranchPickerModel) renderSelectBox() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(m.theme.Text)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("76"))

	hLine := strings.Repeat("\u2500", menuInnerWidth)
	topBorder := dimStyle.Render("\u256d" + hLine + "\u256e")
	separator := dimStyle.Render("\u251c" + hLine + "\u2524")
	bottomBorder := dimStyle.Render("\u2570" + hLine + "\u256f")
	leftBorder := dimStyle.Render("\u2502")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("\u2502")

	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder

	var lines []string

	// Top border
	lines = append(lines, topBorder)

	// Title row
	title := primaryBoldStyle.Render("Select Branch")
	titlePadding := menuContentWidth - lipgloss.Width(title) - 1
	if titlePadding < 0 {
		titlePadding = 0
	}
	lines = append(lines, leftBorder+" "+title+strings.Repeat(" ", titlePadding)+rightBorder)

	// Separator
	lines = append(lines, separator)

	// Filter row
	var filterContent string
	if m.filtering {
		filterPrompt := dimStyle.Render("/")
		if m.filterText == "" {
			filterContent = "  " + filterPrompt + " " + dimStyle.Render("type to filter...") + dimStyle.Render("│")
		} else {
			filterContent = "  " + filterPrompt + " " + textStyle.Render(m.filterText) + dimStyle.Render("│")
		}
	} else {
		filterContent = "  " + dimStyle.Render("/ to filter")
	}
	filterPadding := menuContentWidth - lipgloss.Width(filterContent)
	if filterPadding < 0 {
		filterPadding = 0
	}
	lines = append(lines, leftBorder+filterContent+strings.Repeat(" ", filterPadding)+rightBorder)

	// Empty line before items
	lines = append(lines, emptyRow)

	// Branch items
	visible := m.visibleItemCount()
	if len(m.filtered) == 0 {
		noItems := "  " + dimStyle.Render("No matching branches")
		noItemsPadding := menuContentWidth - lipgloss.Width(noItems)
		if noItemsPadding < 0 {
			noItemsPadding = 0
		}
		lines = append(lines, leftBorder+noItems+strings.Repeat(" ", noItemsPadding)+rightBorder)
	} else {
		end := m.offset + visible
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		for i := m.offset; i < end; i++ {
			branch := m.filtered[i]
			selected := i == m.cursor

			truncBranch := TruncateMiddle(branch, menuContentWidth-7)

			var row string
			if selected {
				selectedBgStyle := lipgloss.NewStyle().Background(lipgloss.Color("236"))
				marker := primaryBoldStyle.Render("\u258c")
				branchText := primaryBoldStyle.Render(truncBranch)
				content := " " + marker + branchText
				padding := menuContentWidth - lipgloss.Width(content)
				if padding < 0 {
					padding = 0
				}
				row = leftBorder + selectedBgStyle.Render(content+strings.Repeat(" ", padding)) + rightBorder
			} else {
				// A hovered-but-unselected branch gets a faint wash so the pointer
				// target is visible; it clears the moment the pointer leaves the row.
				branchText := primaryStyle.Render(truncBranch)
				content := "    " + branchText
				padding := menuContentWidth - lipgloss.Width(content)
				if padding < 0 {
					padding = 0
				}
				if i == m.hover {
					hoverBgStyle := lipgloss.NewStyle().Background(lipgloss.Color("236"))
					row = leftBorder + hoverBgStyle.Render(content+strings.Repeat(" ", padding)) + rightBorder
				} else {
					row = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
				}
			}
			lines = append(lines, row)
		}
	}

	// Empty line after items
	lines = append(lines, emptyRow)

	// Separator before help
	lines = append(lines, separator)

	// Help row
	var helpContent string
	if m.feedback != "" {
		if m.feedbackIsErr {
			helpContent = errorStyle.Render(m.feedback)
		} else {
			helpContent = successStyle.Render(m.feedback)
		}
	} else {
		helpText := "\u2191/k up \u00b7 \u2193/j down \u00b7 enter select \u00b7 / filter \u00b7 d delete \u00b7 esc back"
		helpContent = helpStyle.Render(helpText)
	}
	helpPadding := menuContentWidth - lipgloss.Width(helpContent) - 1
	if helpPadding < 0 {
		helpPadding = 0
	}
	lines = append(lines, leftBorder+" "+helpContent+strings.Repeat(" ", helpPadding)+rightBorder)

	// Bottom border
	lines = append(lines, bottomBorder)

	return m.centerBox(lines)
}

func (m BranchPickerModel) renderDeleteBox() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	deleteHighlight := lipgloss.NewStyle().Background(lipgloss.Color("196")).Foreground(lipgloss.Color("15"))

	hLine := strings.Repeat("\u2500", menuInnerWidth)
	topBorder := dimStyle.Render("\u256d" + hLine + "\u256e")
	separator := dimStyle.Render("\u251c" + hLine + "\u2524")
	bottomBorder := dimStyle.Render("\u2570" + hLine + "\u256f")
	leftBorder := dimStyle.Render("\u2502")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("\u2502")
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder

	var lines []string

	lines = append(lines, topBorder)

	// Title row with "· Delete" suffix
	title := primaryBoldStyle.Render("Select Branch")
	titleContent := title + " " + dimStyle.Render("\u00b7 Delete")
	titlePadding := menuContentWidth - lipgloss.Width(titleContent) - 1
	if titlePadding < 0 {
		titlePadding = 0
	}
	lines = append(lines, leftBorder+" "+titleContent+strings.Repeat(" ", titlePadding)+rightBorder)
	lines = append(lines, separator)
	lines = append(lines, emptyRow)

	// Branch items — selected in red highlight, others dimmed
	visible := m.deleteVisibleCount()
	end := m.deleteOffset + visible
	if end > len(m.allBranches) {
		end = len(m.allBranches)
	}
	for i := m.deleteOffset; i < end; i++ {
		branch := m.allBranches[i]
		selected := m.deleteSelected == i
		truncBranch := TruncateMiddle(branch, menuContentWidth-6)

		var row string
		if selected {
			nameText := deleteHighlight.Render(" " + truncBranch + " ")
			content := "  " + nameText
			padding := menuContentWidth - lipgloss.Width(content)
			if padding < 0 {
				padding = 0
			}
			row = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
		} else {
			nameText := dimStyle.Render(truncBranch)
			content := "    " + nameText
			padding := menuContentWidth - lipgloss.Width(content)
			if padding < 0 {
				padding = 0
			}
			row = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
		}
		lines = append(lines, row)
	}

	lines = append(lines, emptyRow)
	lines = append(lines, separator)

	// Help row
	helpText := "\u2191\u2193 navigate  \u23ce delete  Q cancel"
	helpContent := helpStyle.Render(helpText)
	helpPadding := menuContentWidth - lipgloss.Width(helpContent) - 1
	if helpPadding < 0 {
		helpPadding = 0
	}
	lines = append(lines, leftBorder+" "+helpContent+strings.Repeat(" ", helpPadding)+rightBorder)
	lines = append(lines, bottomBorder)

	return m.centerBox(lines)
}

// selectBoxItemRows returns how many branch rows the select box renders (the
// "no matching branches" placeholder counts as one).
func (m BranchPickerModel) selectBoxItemRows() int {
	if len(m.filtered) == 0 {
		return 1
	}
	if v := m.visibleItemCount(); len(m.filtered) > v {
		return v
	}
	return len(m.filtered)
}

// mouseToFilteredIndex maps an absolute pointer coordinate to an index in the
// filtered branch list, or -1. The select box layout is top(0) title(1) sep(2)
// filter(3) blank(4) then items from row 5; the box is centered by centerBox,
// so the same centering math recovers the box origin.
func (m BranchPickerModel) mouseToFilteredIndex(x, y int) int {
	const boxWidth = menuInnerWidth + 2
	originX := 0
	if m.width > 0 {
		if lp := (m.width - boxWidth) / 2; lp > 0 {
			originX = lp
		}
	}
	if x < originX || x >= originX+boxWidth {
		return -1
	}
	boxLines := 5 + m.selectBoxItemRows() + 4 // header(5) + items + footer(4)
	originY := 0
	if m.height > 0 {
		if tp := (m.height - boxLines) / 2; tp > 0 {
			originY = tp
		}
	}
	const firstItemRow = 5
	idx := (y - originY - firstItemRow) + m.offset
	end := m.offset + m.visibleItemCount()
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	if idx >= m.offset && idx < end {
		return idx
	}
	return -1
}

// handleMouse gives the branch picker pointer parity: hover moves the cursor,
// left-click selects the branch under it (the Enter action), and the wheel
// scrolls. The delete/filter sub-modes and post-action feedback own input.
func (m BranchPickerModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.deleteMode || m.filtering || m.feedback != "" {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.clampScroll()
		}
		return m, nil
	case tea.MouseButtonWheelUp:
		if m.cursor > 0 {
			m.cursor--
			m.clampScroll()
		}
		return m, nil
	}

	idx := m.mouseToFilteredIndex(msg.X, msg.Y)
	// Require a glyph under the pointer so the blank area past a branch name
	// doesn't register as that row.
	if idx >= 0 && !frameCellHasGlyph(strings.Split(m.View(), "\n"), msg.X, msg.Y) {
		idx = -1
	}
	switch msg.Action {
	case tea.MouseActionMotion:
		// Transient highlight only: idx is -1 when the pointer is off every branch
		// row, clearing the hover; the keyboard cursor stays where it was.
		m.hover = idx
		return m, nil
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft && idx >= 0 {
			name := m.filtered[idx]
			m.selected = &name
			m.quitting = true
			return m, func() tea.Msg { return PopScreenMsg{} }
		}
	}
	return m, nil
}

func (m BranchPickerModel) centerBox(lines []string) string {
	box := strings.Join(lines, "\n")

	// Center horizontally
	if m.width > 0 {
		boxWidth := menuInnerWidth + 2
		leftPad := (m.width - boxWidth) / 2
		if leftPad > 0 {
			padStr := strings.Repeat(" ", leftPad)
			padded := make([]string, len(lines))
			for i, line := range lines {
				padded[i] = padStr + line
			}
			box = strings.Join(padded, "\n")
		}
	}

	// Center vertically
	if m.height > 0 {
		boxLines := strings.Count(box, "\n") + 1
		topPad := (m.height - boxLines) / 2
		if topPad > 0 {
			box = strings.Repeat("\n", topPad) + box
		}
	}

	return box
}

// Selected returns the selected branch name, or nil if cancelled.
func (m BranchPickerModel) Selected() *string {
	return m.selected
}

// PopResult implements tui.ResultProvider. Returns a BranchPickerDoneMsg
// that AppModel relays to MainMenuModel when this screen is popped.
func (m BranchPickerModel) PopResult() tea.Msg {
	if m.selected != nil {
		return BranchPickerDoneMsg{Selected: true, Branch: *m.selected}
	}
	return nil
}
