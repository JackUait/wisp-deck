package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/models"
)

// aiToolInstallDoneMsg reports the outcome of a background tool install, the way
// worktreeDoneMsg reports a background `git worktree add`.
type aiToolInstallDoneMsg struct {
	tool string
	err  error
}

// installableTools are the tools this panel may install. Claude Code is
// excluded: its installer is `curl -fsSL … | bash`, which has no business
// running from inside a TUI. The bash function each one calls owns the npm/Node
// fallbacks, so that logic lives in exactly one place (lib/install.sh).
var installableTools = map[string]string{
	"opencode": "ensure_opencode",
	"codex":    "ensure_codex",
}

// installCmdFor builds the command that installs a tool by calling the existing
// bash installer. tui.sh is sourced too because ensure_* calls success/info/warn.
// Returns an error for claude and for unknown tools, so "not installable" is
// enforced here rather than only in the key handler.
//
// The script text is a constant per tool and libDir travels in the environment
// as WISP_DECK_LIB_DIR: interpolating the path into the script would let a
// libDir containing $(…) or `…` execute as a command substitution, since bash
// expands those inside double quotes. The function name comes from the fixed
// installableTools map, never from caller input.
func installCmdFor(tool, libDir string) (*exec.Cmd, error) {
	fn, ok := installableTools[tool]
	if !ok {
		return nil, fmt.Errorf("%q cannot be installed from the settings panel", tool)
	}
	if libDir == "" {
		return nil, errors.New("no lib directory: pass --lib-dir or set WISP_DECK_LIB_DIR")
	}
	script := `source "$WISP_DECK_LIB_DIR/tui.sh" && source "$WISP_DECK_LIB_DIR/install.sh" && ` + fn
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "WISP_DECK_LIB_DIR="+libDir)
	return cmd, nil
}

// openAIToolsPanel shows the tool list, detecting installed state afresh so a
// tool installed outside the app since launch shows up correctly.
func (m *MainMenuModel) openAIToolsPanel() {
	m.aiToolRows = m.detect()
	m.aiToolsPanelOpen = true
	m.aiToolsCursor = 0
	m.aiToolsErr = nil
}

// detect returns the known tools and their installed state, through an injectable
// seam so tests never depend on the machine's PATH.
func (m *MainMenuModel) detect() []models.AITool {
	if m.detectAITools != nil {
		return m.detectAITools()
	}
	return models.DetectAITools()
}

// updateAIToolsPanel handles keys while the panel is open.
func (m *MainMenuModel) updateAIToolsPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.aiToolsPanelOpen = false
		return m, nil
	case tea.KeyUp:
		if m.aiToolsCursor > 0 {
			m.aiToolsCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.aiToolsCursor < len(m.aiToolRows)-1 {
			m.aiToolsCursor++
		}
		return m, nil
	case tea.KeyEnter:
		return m, m.installFocusedTool()
	case tea.KeyRunes:
		if string(msg.Runes) == "d" {
			m.setFocusedToolDefault()
		}
		return m, nil
	}
	return m, nil
}

// focusedTool returns the row under the cursor, or nil.
func (m *MainMenuModel) focusedTool() *models.AITool {
	if m.aiToolsCursor < 0 || m.aiToolsCursor >= len(m.aiToolRows) {
		return nil
	}
	return &m.aiToolRows[m.aiToolsCursor]
}

// installFocusedTool starts a background install of the highlighted tool. An
// already-installed tool, claude, and an unknown tool all yield no command.
func (m *MainMenuModel) installFocusedTool() tea.Cmd {
	tool := m.focusedTool()
	if tool == nil || tool.Installed || m.aiToolInstalling != "" {
		return nil
	}
	cmd, err := installCmdFor(tool.Name, m.libDir)
	if err != nil {
		m.aiToolsErr = err
		return nil
	}
	name := tool.Name
	m.aiToolInstalling = name
	m.aiToolsErr = nil
	return func() tea.Msg {
		if err := cmd.Run(); err != nil {
			return aiToolInstallDoneMsg{tool: name, err: err}
		}
		return aiToolInstallDoneMsg{tool: name}
	}
}

// setFocusedToolDefault makes the highlighted tool the default for new sessions,
// reusing the same persistence path as the main menu's AI-tool row. A tool that
// is not installed cannot become the default.
func (m *MainMenuModel) setFocusedToolDefault() {
	tool := m.focusedTool()
	if tool == nil || !tool.Installed {
		return
	}
	for i, name := range m.aiTools {
		if name == tool.Name {
			m.selectedAI = i
			m.theme = ResolveTheme(name, m.themePref)
			m.persistAITool()
			return
		}
	}
}

// applyAIToolInstallDone folds an install result back into the model.
func (m *MainMenuModel) applyAIToolInstallDone(msg aiToolInstallDoneMsg) {
	m.aiToolInstalling = ""
	display := models.DisplayName(msg.tool)

	if msg.err != nil {
		m.feedbackMsg = "Failed to install " + display + ": " + msg.err.Error()
		m.feedbackStyle = "error"
		return
	}

	m.aiToolRows = m.detect()
	// The menu is the project selector shown before tmux launches, so a
	// just-installed tool must become selectable for the session about to start.
	// Without this the AI-tool row would still only cycle what bash detected.
	var present bool
	for _, name := range m.aiTools {
		if name == msg.tool {
			present = true
		}
	}
	if !present {
		m.aiTools = append(m.aiTools, msg.tool)
	}
	m.feedbackMsg = "Installed " + display
	m.feedbackStyle = "success"
}

// renderAIToolsPanel draws the tool-management box below the menu, mirroring the
// account panel's chrome.
func (m *MainMenuModel) renderAIToolsPanel() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	grayStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	hLine := strings.Repeat("─", menuInnerWidth)
	topBorder := dimStyle.Render("╭" + hLine + "╮")
	separator := dimStyle.Render("├" + hLine + "┤")
	bottomBorder := dimStyle.Render("╰" + hLine + "╯")
	leftBorder := dimStyle.Render("│")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("│")

	row := func(left, right string) string {
		gap := menuContentWidth - 1 - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		return leftBorder + " " + left + strings.Repeat(" ", gap) + right + rightBorder
	}

	lines := []string{topBorder, row(primaryBoldStyle.Render("AI tools"), ""), separator}

	for i, tool := range m.aiToolRows {
		marker := "  "
		if i == m.aiToolsCursor {
			marker = primaryBoldStyle.Render("▌ ")
		}
		bullet := grayStyle.Render("○")
		if tool.Installed {
			bullet = greenStyle.Render("●")
		}
		left := marker + bullet + " " + models.DisplayName(tool.Name)

		var right string
		switch {
		case m.aiToolInstalling == tool.Name:
			right = grayStyle.Render("installing…")
		case tool.Installed && tool.Name == m.CurrentAITool():
			right = greenStyle.Render("default")
		case tool.Installed:
			right = grayStyle.Render("installed")
		case installableTools[tool.Name] != "":
			right = helpStyle.Render("⏎ install")
		default:
			right = grayStyle.Render("not installed")
		}
		lines = append(lines, row(left, right))
	}

	if m.aiToolsErr != nil {
		lines = append(lines, row(errStyle.Render(m.aiToolsErr.Error()), ""))
	}

	lines = append(lines, separator)
	sep := dimStyle.Render(" · ")
	help := helpStyle.Render("⏎ install") + sep + helpStyle.Render("d default") + sep + helpStyle.Render("esc close")
	lines = append(lines, row(help, ""), bottomBorder)
	return strings.Join(lines, "\n")
}
