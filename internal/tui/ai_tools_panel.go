package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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

// installTickMsg drives the install progress bar. The bar needs its own ticker:
// bobTickCmd is armed only in Init and only when the mascot is animated, so a bar
// hung off it would sit frozen for anyone running Mascot [None].
type installTickMsg struct{}

const (
	// installPctCeiling caps the progress bar below 100%. `npm install -g` reports
	// no usable progress when run detached, so the percentage is driven by elapsed
	// ticks rather than by work done. Holding short of 100% means a wedged install
	// parks at the ceiling instead of claiming to have finished; the row only
	// leaves the bar behind when npm actually returns.
	installPctCeiling = 0.9
	// installPctEase is the fraction of the remaining distance covered per tick,
	// giving a fast start that slows as it approaches the ceiling. Tuned against a
	// real `npm install -g`, which takes tens of seconds: at 0.06 the bar hit the
	// ceiling in under 6s and then sat pinned for the rest of the install. At
	// 0.008 it passes ~35% at 5s and ~85% at 30s. See the pacing test.
	installPctEase = 0.008
	// installTickInterval is the bar's refresh period.
	installTickInterval = 80 * time.Millisecond
	// installBarWidth is the bar's cell count, sized to fit the panel's right
	// column beside the longest tool name.
	installBarWidth = 24
)

// advanceInstallPct eases pct toward installPctCeiling without ever reaching it.
func advanceInstallPct(pct float64) float64 {
	if pct >= installPctCeiling {
		return installPctCeiling
	}
	return pct + (installPctCeiling-pct)*installPctEase
}

// installTickCmd schedules the next progress-bar frame.
func installTickCmd() tea.Cmd {
	return tea.Tick(installTickInterval, func(time.Time) tea.Msg { return installTickMsg{} })
}

// applyInstallTick advances the bar and reports whether the ticker should re-arm.
// It stops as soon as no install is running, so no ticker outlives its work.
func (m *MainMenuModel) applyInstallTick() bool {
	if m.aiToolInstalling == "" {
		return false
	}
	m.aiToolInstallPct = advanceInstallPct(m.aiToolInstallPct)
	return true
}

// renderProgressBar draws a width-cell bar followed by the percentage.
func renderProgressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	fillStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	restStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	bar := fillStyle.Render(strings.Repeat("█", filled)) +
		restStyle.Render(strings.Repeat("░", width-filled))
	return bar + restStyle.Render(fmt.Sprintf(" %3.0f%%", pct*100))
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
	m.aiToolInstallPct = 0
	m.aiToolsErr = nil
	install := func() tea.Msg {
		if err := cmd.Run(); err != nil {
			return aiToolInstallDoneMsg{tool: name, err: err}
		}
		return aiToolInstallDoneMsg{tool: name}
	}
	return tea.Batch(install, installTickCmd())
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
	m.aiToolInstallPct = 0
	display := models.DisplayName(msg.tool)

	if msg.err != nil {
		// feedbackMsg is only rendered on the Projects tab, so the panel carries
		// its own error line — otherwise a failure here would be invisible.
		m.aiToolsErr = fmt.Errorf("Failed to install %s: %w", display, msg.err)
		m.feedbackMsg = "Failed to install " + display + ": " + msg.err.Error()
		m.feedbackStyle = "error"
		return
	}

	m.aiToolsErr = nil
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
			right = renderProgressBar(m.aiToolInstallPct, installBarWidth)
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
