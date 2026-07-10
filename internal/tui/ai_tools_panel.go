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
	if m.aiToolInstalling == "" && m.aiToolRemoving == "" {
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

// removableTools are the tools the panel may uninstall from the system.
// Claude Code is excluded, mirroring install: its curl installer has no clean
// uninstall path and wisp-deck's own statusline/account machinery depend on it.
var removableTools = map[string]string{
	"opencode": "remove_opencode",
	"codex":    "remove_codex",
}

// removeUninstallCmd is the human-readable command shown in the warning modal.
var removeUninstallCmd = map[string]string{
	"opencode": "npm uninstall -g opencode-ai",
	"codex":    "npm uninstall -g @openai/codex",
}

// aiToolRemoveDoneMsg reports the outcome of a background tool removal.
type aiToolRemoveDoneMsg struct {
	tool string
	err  error
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
	return bashLibCmd(installableTools, tool, libDir, "installed")
}

// removeCmdFor builds the command that uninstalls a tool via the bash removers,
// with the same injection-safe shape as installCmdFor.
func removeCmdFor(tool, libDir string) (*exec.Cmd, error) {
	return bashLibCmd(removableTools, tool, libDir, "removed")
}

func bashLibCmd(fns map[string]string, tool, libDir, verb string) (*exec.Cmd, error) {
	fn, ok := fns[tool]
	if !ok {
		return nil, fmt.Errorf("%q cannot be %s from the settings panel", tool, verb)
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
// seam so tests never depend on the machine's PATH. Disabled state is overlaid
// from the disabled-tools file, which PATH detection knows nothing about.
func (m *MainMenuModel) detect() []models.AITool {
	var rows []models.AITool
	if m.detectAITools != nil {
		rows = m.detectAITools()
	} else {
		rows = models.DetectAITools()
	}
	disabled := models.LoadDisabledTools(m.disabledToolsFile)
	for i := range rows {
		rows[i].Disabled = disabled[rows[i].Name]
	}
	return rows
}

// updateAIToolsPanel handles keys while the panel is open.
func (m *MainMenuModel) updateAIToolsPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The remove warning modal captures every key while open.
	if m.aiToolRemovePending != "" {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.aiToolRemovePending = ""
		case tea.KeyEnter:
			return m, m.startPendingRemoval()
		}
		return m, nil
	}

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
		switch string(msg.Runes) {
		case "d":
			m.setFocusedToolDefault()
		case "x":
			m.toggleFocusedToolDisabled()
		case "r":
			m.openRemoveModal()
		}
		return m, nil
	}
	return m, nil
}

// toggleFocusedToolDisabled flips the focused tool's disabled state. Disabling
// the current default hands the default to the first enabled tool, so a session
// can never launch with a tool the user just hid.
func (m *MainMenuModel) toggleFocusedToolDisabled() {
	tool := m.focusedTool()
	if tool == nil || m.disabledToolsFile == "" {
		return
	}
	nowDisabled, err := models.ToggleDisabledTool(m.disabledToolsFile, tool.Name)
	if err != nil {
		m.aiToolsErr = err
		return
	}
	tool.Disabled = nowDisabled
	if nowDisabled && tool.Name == m.CurrentAITool() {
		disabled := models.LoadDisabledTools(m.disabledToolsFile)
		for i, name := range m.aiTools {
			if !disabled[name] {
				m.selectedAI = i
				m.theme = ResolveTheme(name, m.themePref)
				m.persistAITool()
				break
			}
		}
	}
}

// openRemoveModal arms the warning modal for the focused tool. Claude, a
// not-installed tool, and any in-flight install/removal all refuse.
func (m *MainMenuModel) openRemoveModal() {
	tool := m.focusedTool()
	if tool == nil || !tool.Installed || m.aiToolInstalling != "" || m.aiToolRemoving != "" {
		return
	}
	if _, removable := removableTools[tool.Name]; !removable {
		return
	}
	m.aiToolRemovePending = tool.Name
}

// startPendingRemoval launches the confirmed removal in the background, with
// the same progress treatment as an install.
func (m *MainMenuModel) startPendingRemoval() tea.Cmd {
	name := m.aiToolRemovePending
	m.aiToolRemovePending = ""
	cmd, err := removeCmdFor(name, m.libDir)
	if err != nil {
		m.aiToolsErr = err
		return nil
	}
	m.aiToolRemoving = name
	m.aiToolInstallPct = 0
	m.aiToolsErr = nil
	remove := func() tea.Msg {
		if err := cmd.Run(); err != nil {
			return aiToolRemoveDoneMsg{tool: name, err: err}
		}
		return aiToolRemoveDoneMsg{tool: name}
	}
	return tea.Batch(remove, installTickCmd())
}

// applyAIToolRemoveDone folds a removal result back into the model.
func (m *MainMenuModel) applyAIToolRemoveDone(msg aiToolRemoveDoneMsg) {
	m.aiToolRemoving = ""
	m.aiToolInstallPct = 0
	display := models.DisplayName(msg.tool)

	if msg.err != nil {
		m.aiToolsErr = fmt.Errorf("Failed to remove %s: %w", display, msg.err)
		m.feedbackMsg = "Failed to remove " + display + ": " + msg.err.Error()
		m.feedbackStyle = "error"
		return
	}

	m.aiToolsErr = nil
	m.aiToolRows = m.detect()
	// A removed tool must stop being selectable for the session about to
	// start; hand the default to the first survivor if it was the default.
	stillInstalled := false
	for _, r := range m.aiToolRows {
		if r.Name == msg.tool && r.Installed {
			stillInstalled = true
		}
	}
	if !stillInstalled {
		wasDefault := m.CurrentAITool() == msg.tool
		var kept []string
		for _, name := range m.aiTools {
			if name != msg.tool {
				kept = append(kept, name)
			}
		}
		m.aiTools = kept
		if wasDefault && len(m.aiTools) > 0 {
			m.selectedAI = 0
			m.theme = ResolveTheme(m.aiTools[0], m.themePref)
			m.persistAITool()
		} else if m.selectedAI >= len(m.aiTools) && len(m.aiTools) > 0 {
			m.selectedAI = len(m.aiTools) - 1
		}
	}
	m.feedbackMsg = "Removed " + display
	m.feedbackStyle = "success"
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
	if tool == nil || !tool.Installed || tool.Disabled {
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

	sep := dimStyle.Render(" · ")

	// The remove warning modal replaces the tool list while armed.
	if m.aiToolRemovePending != "" {
		display := models.DisplayName(m.aiToolRemovePending)
		lines := []string{topBorder,
			row(errStyle.Bold(true).Render("Remove "+display+"?"), ""),
			separator,
			row(helpStyle.Render("Runs "+removeUninstallCmd[m.aiToolRemovePending]), ""),
			row(helpStyle.Render("— removes it from this system."), ""),
		}
		if m.aiToolRemovePending == "opencode" {
			lines = append(lines,
				row(grayStyle.Render("OpenCode stays launchable via npx;"), ""),
				row(grayStyle.Render("press x to hide it from wisp-deck."), ""))
		}
		help := errStyle.Render("⏎ remove") + sep + helpStyle.Render("esc cancel")
		lines = append(lines, separator, row(help, ""), bottomBorder)
		return strings.Join(lines, "\n")
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
		name := models.DisplayName(tool.Name)
		if tool.Disabled {
			name = grayStyle.Render(name)
		}
		left := marker + bullet + " " + name

		var right string
		switch {
		case m.aiToolInstalling == tool.Name || m.aiToolRemoving == tool.Name:
			right = renderProgressBar(m.aiToolInstallPct, installBarWidth)
		case tool.Disabled:
			right = grayStyle.Render("disabled")
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
	help := helpStyle.Render("⏎ install") + sep + helpStyle.Render("d default") + sep +
		helpStyle.Render("x disable") + sep + helpStyle.Render("r remove") + sep + helpStyle.Render("esc close")
	lines = append(lines, row(help, ""), bottomBorder)
	return strings.Join(lines, "\n")
}
