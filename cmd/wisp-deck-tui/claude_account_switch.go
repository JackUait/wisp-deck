package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/jackuait/wisp-deck/internal/tui"
	"github.com/jackuait/wisp-deck/internal/util"
)

// The account switcher is a standalone interactive popup (launched from a tmux
// display-popup mid-session). It lists the implicit Default login plus every
// managed login, lets the user pick one, writes the active-account pointer, and
// reports the choice as one line of JSON so bash can decide whether to relaunch
// `claude`. Unlike the inline switcher in the main menu, it does not couple to
// MainMenuModel — the pure selection/row logic is factored out below and the
// Bubbletea model is a thin shell over it.

var (
	casList         string
	casAccountDir   string
	casPointer      string
	casColors       string
	casDefaultLabel string
	casBackdrop     string
	casActive       string
	casResultFile   string
	casTools        string
	casActiveTool   string
)

// switchRow is one selectable entry in the switcher: a claude login (Dir set,
// "" = the implicit Default/Keychain login) or another AI agent (Tool set,
// e.g. "opencode"). Exactly one dimension is meaningful per row — claude
// account rows carry no Tool, agent rows carry no Dir.
type switchRow struct {
	Label string
	Dir   string
	Tool  string
}

// switchRowsForActive builds the ordered row list — Default first, then each
// managed login from the list file — and resolves the initial cursor to the row
// whose dir is active ("" = the Default row). active is the account THIS pane
// runs (bash passes it via --active from the tmux session env): the global
// pointer can't serve here, because a switch in another session (or the
// launcher) flips it without changing what this pane runs — the popup would
// mark the wrong login and read as the account having "switched back".
func switchRowsForActive(listFile, defaultLabelFile, active string) ([]switchRow, int) {
	return switchRowsForSession(listFile, defaultLabelFile, active, "", nil)
}

// switchRowsForSession is switchRowsForActive extended with agent rows: after
// the claude account rows, one row per other available AI tool (bash passes
// them via --tools). "claude" never gets an agent row — the account rows ARE
// claude. activeTool is the tool THIS pane runs; when it is a non-claude
// agent, the cursor (and active dot) lands on that agent's row instead of a
// claude account.
func switchRowsForSession(listFile, defaultLabelFile, active, activeTool string, tools []string) ([]switchRow, int) {
	rows := []switchRow{{Label: claudeaccount.GetDefaultLabel(defaultLabelFile), Dir: ""}}
	for _, acc := range claudeaccount.Load(listFile) {
		rows = append(rows, switchRow{Label: acc.Label, Dir: acc.Dir})
	}
	for _, tool := range tools {
		if tool == "" || tool == "claude" {
			continue
		}
		rows = append(rows, switchRow{Label: models.DisplayName(tool), Tool: tool})
	}
	cursor := 0
	for i, r := range rows {
		if activeTool != "" && activeTool != "claude" {
			if r.Tool == activeTool {
				cursor = i
				break
			}
			continue
		}
		if r.Tool == "" && r.Dir == active {
			cursor = i
			break
		}
	}
	return rows, cursor
}

// switchResultValue is the line a chosen row reports through the result file:
// the account dir for claude login rows, "tool:<name>" for agent rows — the
// prefix is what lets the bash side tell an agent switch apart from a dir.
func switchResultValue(r switchRow) string {
	if r.Tool != "" {
		return "tool:" + r.Tool
	}
	return r.Dir
}

// loadSwitchRows is switchRowsForActive keyed on the global pointer — the
// fallback when the caller didn't say which account the pane runs (--active
// absent, e.g. an older bash lib driving a newer binary).
func loadSwitchRows(listFile, defaultLabelFile, pointerFile string) ([]switchRow, int) {
	return switchRowsForActive(listFile, defaultLabelFile, claudeaccount.GetActive(pointerFile))
}

// writeSwitchResult reports the chosen dir ("" = Default) through the result
// file the bash side passed via --result-file. display-popup swallows the
// popup's stdout, and the global pointer can't distinguish "picked the account
// the pointer already names" from "cancelled" — the file's very existence is
// the selection signal. An empty path (flag not passed) is a no-op.
func writeSwitchResult(path, dir string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(dir+"\n"), 0644)
}

// selectResultJSON persists the chosen dir as the active account and returns the
// one-line JSON describing the selection. changed reports whether chosenDir
// differs from the dir that was active when the command started.
func selectResultJSON(pointerFile, chosenDir, prevActive string) (string, error) {
	if err := claudeaccount.SetActive(pointerFile, chosenDir); err != nil {
		return "", err
	}
	out, err := json.Marshal(struct {
		Selected bool   `json:"selected"`
		Dir      string `json:"dir"`
		Changed  bool   `json:"changed"`
	}{Selected: true, Dir: chosenDir, Changed: chosenDir != prevActive})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// selectToolResultJSON is the JSON for an agent-row choice. Unlike an account
// choice it never touches the claude account pointer — the account stays
// whatever it was for the next claude launch. changed reports whether the
// chosen agent differs from the tool the pane was running.
func selectToolResultJSON(tool, activeTool string) (string, error) {
	out, err := json.Marshal(struct {
		Selected bool   `json:"selected"`
		Tool     string `json:"tool"`
		Changed  bool   `json:"changed"`
	}{Selected: true, Tool: tool, Changed: tool != activeTool})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// cancelResultJSON returns the JSON emitted when the user cancels (no pointer
// write).
func cancelResultJSON() string {
	out, _ := json.Marshal(struct {
		Selected bool `json:"selected"`
	}{Selected: false})
	return string(out)
}

var claudeAccountSwitchCmd = &cobra.Command{
	Use:   "claude-account-switch",
	Short: "Interactive popup to switch the active Claude login",
	Long:  "Lists the native Claude logins, lets the user pick one, writes the active-account pointer, and reports the choice as JSON for a bash caller to relaunch claude.",
	RunE:  runClaudeAccountSwitch,
}

func runClaudeAccountSwitch(cmd *cobra.Command, args []string) error {
	if casList == "" || casPointer == "" {
		return fmt.Errorf("--list and --pointer are required")
	}
	// The active row is the account THIS pane runs (--active, from the tmux
	// session env). Only when the caller didn't say (older bash lib) fall back
	// to the global pointer.
	active := claudeaccount.GetActive(casPointer)
	if cmd.Flags().Changed("active") {
		active = casActive
	}
	// Other available agents (comma-separated) and the tool this pane runs.
	// Absent flags mean an older bash lib: claude-only rows, as before.
	var tools []string
	for _, tool := range strings.Split(casTools, ",") {
		if tool = strings.TrimSpace(tool); tool != "" {
			tools = append(tools, tool)
		}
	}
	activeTool := casActiveTool
	if activeTool == "" {
		activeTool = "claude"
	}
	rows, cursor := switchRowsForSession(casList, casDefaultLabel, active, activeTool, tools)
	prevActive := claudeaccount.GetActive(casPointer)

	model := newAccountSwitchModel(rows, cursor, casColors)
	// Show the screen behind the (full-screen) popup dimmed around the card. Best
	// effort: an unreadable/missing backdrop file just leaves the margin blank.
	if casBackdrop != "" {
		if raw, err := os.ReadFile(casBackdrop); err == nil {
			model = model.withBackdrop(tui.ParseBackdrop(string(raw)))
		}
	}

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	defer cleanup()

	opts := append([]tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}, ttyOpts...)
	final, err := tea.NewProgram(model, opts...).Run()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	m := final.(accountSwitchModel)
	if !m.chosen {
		fmt.Fprintln(cmd.OutOrStdout(), cancelResultJSON())
		return nil
	}
	chosen := rows[m.cursor]
	var out string
	if chosen.Tool != "" {
		out, err = selectToolResultJSON(chosen.Tool, activeTool)
	} else {
		out, err = selectResultJSON(casPointer, chosen.Dir, prevActive)
	}
	if err != nil {
		return err
	}
	if err := writeSwitchResult(casResultFile, switchResultValue(chosen)); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}

// accountSwitchModel is the thin Bubbletea shell: it tracks the cursor, the
// terminal size (for centering + mouse mapping), which row is the active login,
// and whether a row was chosen; all persistence lives in the pure helpers above.
type accountSwitchModel struct {
	rows       []switchRow
	cursor     int
	active     int
	colorsFile string
	chosen     bool
	width      int
	height     int
	backdrop   []string
}

func newAccountSwitchModel(rows []switchRow, cursor int, colorsFile string) accountSwitchModel {
	// The switcher opens with the cursor on the active login, so the initial
	// cursor is also the active-row marker.
	return accountSwitchModel{rows: rows, cursor: cursor, active: cursor, colorsFile: colorsFile}
}

// withBackdrop attaches a captured screen (rows of plain text) shown dimmed
// behind the card, so the full-screen popup reads as a modal over the session
// rather than a blank void.
func (m accountSwitchModel) withBackdrop(rows []string) accountSwitchModel {
	m.backdrop = rows
	return m
}

// headerLines is the number of non-selectable group-header lines rendered
// inside the rows block: 1 for the "󰚩 Claude" subgroup header when agent rows
// are present, 0 for the legacy claude-only popup. Layout and mouse mapping
// must both add it, or clicks land one row off.
func (m accountSwitchModel) headerLines() int {
	for _, r := range m.rows {
		if r.Tool != "" {
			return 1
		}
	}
	return 0
}

func (m accountSwitchModel) Init() tea.Cmd { return nil }

func (m accountSwitchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "enter":
			m.chosen = true
			return m, tea.Quit
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			firstRowY, cardLeft, cardWidth := accountSwitchLayout(m.width, m.height, len(m.rows)+m.headerLines(), m.contentWidth())
			// The group header line (when present) sits at firstRowY and is not
			// selectable — the first real row is one line below it.
			idx := msg.Y - firstRowY - m.headerLines()
			onCard := msg.X >= cardLeft && msg.X < cardLeft+cardWidth
			if onCard && idx >= 0 && idx < len(m.rows) {
				m.cursor = idx
				m.chosen = true
				return m, tea.Quit
			}
			// A click anywhere else — the margin around the card — closes the popup
			// without switching, i.e. clicking outside the menu dismisses it.
			m.chosen = false
			return m, tea.Quit
		}
	}
	return m, nil
}

// accountSwitch card geometry (kept in sync with the lipgloss styles in View):
// the rounded border is 1 cell, the padding is 1 row / 3 cols, and the content
// block is a title line, a blank line, one line per login, a blank line, and a
// help line.
const (
	accountSwitchPadX   = 3
	accountSwitchPadY   = 1
	accountSwitchBorder = 1
	accountSwitchHeader = 2 // title + blank line above the rows
	accountSwitchFooter = 2 // blank line + help below the rows
)

// accountSwitchLayout maps the centered card onto screen coordinates so a mouse
// click can be resolved to a login row. contentW is the widest inner line. It
// returns the screen Y of the first login row and the card's left column + width.
func accountSwitchLayout(termW, termH, numRows, contentW int) (firstRowY, cardLeft, cardWidth int) {
	cardWidth = contentW + 2*accountSwitchPadX + 2*accountSwitchBorder
	innerH := accountSwitchHeader + numRows + accountSwitchFooter
	cardHeight := innerH + 2*accountSwitchPadY + 2*accountSwitchBorder
	cardLeft = (termW - cardWidth) / 2
	if cardLeft < 0 {
		cardLeft = 0
	}
	cardTop := (termH - cardHeight) / 2
	if cardTop < 0 {
		cardTop = 0
	}
	firstRowY = cardTop + accountSwitchBorder + accountSwitchPadY + accountSwitchHeader
	return firstRowY, cardLeft, cardWidth
}

// innerLines renders the card's content block (title, blank, rows, blank, help),
// shared by View and contentWidth so their geometry can never drift apart.
func (m accountSwitchModel) innerLines() []string {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activeDot := lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render("●")

	// With agent rows present the popup switches between agents, not just
	// claude logins — the title says so, and the claude logins render as a
	// subgroup under a non-selectable "󰚩 Claude" header so they visibly belong
	// to the Claude agent while the other agents stay top-level rows.
	grouped := m.headerLines() > 0
	title := "Switch Claude login"
	if grouped {
		title = "Switch agent"
	}
	lines := []string{titleStyle.Render(title), ""}
	if grouped {
		headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(toolRowColor("claude"))))
		lines = append(lines, "  "+headerStyle.Render(toolRowGlyph("claude")+" Claude"))
	}

	for i, r := range m.rows {
		color := claudeaccount.ColorFor(m.colorsFile, r.Dir)
		glyph := "󰀄"
		if r.Tool != "" {
			// Agent rows: the tool's brand hue (mirrors get_tool_accent in
			// lib/tmux-session.sh) and the tool's own icon instead of the person.
			color = toolRowColor(r.Tool)
			glyph = toolRowGlyph(r.Tool)
		}
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color)))

		marker := "  "
		label := labelStyle.Render(glyph + " " + r.Label)
		if i == m.cursor {
			marker = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color))).Bold(true).Render("▌ ")
			label = labelStyle.Bold(true).Render(glyph + " " + r.Label)
		}
		indent := ""
		if grouped && r.Tool == "" {
			indent = "  "
		}
		line := marker + indent + label
		if i == m.active {
			line += "  " + activeDot
		}
		lines = append(lines, line)
	}

	lines = append(lines, "", dimStyle.Render("↑↓ move · ⏎ switch · esc cancel"))
	return lines
}

// toolRowGlyph is a tool's icon in the switcher. The real brand logos don't
// exist as Nerd Font glyphs, so each tool gets the closest evocative mark:
// a radiating flare for Claude (its starburst-spark logo), the six-spoked
// asterisk for Codex (the OpenAI knot has six loops), and a boxed square for
// OpenCode (its square terminal logo). Unknown tools fall back to the old
// generic robot.
func toolRowGlyph(tool string) string {
	switch tool {
	case "claude":
		return "󰵲"
	case "codex":
		return "󰛄"
	case "opencode":
		return "▣"
	default:
		return "󰚩"
	}
}

// toolRowColor is the 256-color hue for an agent row — kept in sync with
// get_tool_accent in lib/tmux-session.sh so the row matches the tool's pane
// border accent.
func toolRowColor(tool string) int {
	switch tool {
	case "opencode":
		return 141 // brand purple
	case "codex":
		return 36 // brand teal
	default:
		return 209 // orange (claude default)
	}
}

// contentWidth is the width of the widest inner line, used to size and center the
// card and to bound mouse clicks horizontally.
func (m accountSwitchModel) contentWidth() int {
	w := 0
	for _, l := range m.innerLines() {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}

// accountSwitchDim renders the close area (everything outside the card). Like the
// file-list diff modal, it dims by FAINTNESS only — no dark background tint — so
// the surround stays the same gray as the card. That is what lets the card's
// gray-filled corners read as cleanly rounded: a terminal cell is a single solid
// color, so a gray corner can only round against a same-gray surround (a dark
// scrim would leave the corner a hard square). The captured session shows through
// faint/gray around the card.
var accountSwitchDim = lipgloss.NewStyle().
	Faint(true).
	Foreground(lipgloss.Color("240"))

// accountSwitchCardStyle is the card chrome: a thin rounded border and NO
// background of its own, mirroring the diff modal's box. Both the card and the
// faint surround fall back to the terminal's default gray, so they are guaranteed
// identical and the rounded border glyphs round the corners with the gray filling
// through on both sides. The border color is a light gray so the card still reads
// as a distinct panel over the same-gray surround.
func accountSwitchCardStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(accountSwitchPadY, accountSwitchPadX)
}

func (m accountSwitchModel) View() string {
	// Paint nothing until the popup's size arrives: bubbletea's first real frame
	// is then already the full-screen composite, not a small partial card that
	// would leave the popup's edge rows stale (mirrors the diff modal's !ready gate).
	if m.width == 0 || m.height == 0 {
		return ""
	}
	card := accountSwitchCardStyle().Render(strings.Join(m.innerLines(), "\n"))
	firstRowY, cardLeft, cardWidth := accountSwitchLayout(m.width, m.height, len(m.rows)+m.headerLines(), m.contentWidth())
	cardTop := firstRowY - accountSwitchBorder - accountSwitchPadY - accountSwitchHeader
	return m.composite(card, cardLeft, cardTop, cardWidth)
}

// composite lays the card over the dimmed backdrop at (cardLeft, cardTop). Rows
// not covered by the card show the dimmed capture full-width; covered rows show
// the dimmed capture in the left/right margins with the card line in between. An
// empty backdrop yields blank (space) margins, so the standalone popup still works.
func (m accountSwitchModel) composite(card string, cardLeft, cardTop, cardWidth int) string {
	cardLines := strings.Split(card, "\n")
	if cardLeft < 0 {
		cardLeft = 0
	}
	right := cardLeft + cardWidth
	if right > m.width {
		right = m.width
	}
	bgRow := func(y int) []rune {
		row := make([]rune, m.width)
		for i := range row {
			row[i] = ' '
		}
		if y < len(m.backdrop) {
			for i, r := range []rune(m.backdrop[y]) {
				if i >= m.width {
					break
				}
				row[i] = r
			}
		}
		return row
	}
	var b strings.Builder
	for y := 0; y < m.height; y++ {
		row := bgRow(y)
		ci := y - cardTop
		if ci >= 0 && ci < len(cardLines) {
			b.WriteString(accountSwitchDim.Render(string(row[:cardLeft])))
			b.WriteString(cardLines[ci])
			if right < m.width {
				b.WriteString(accountSwitchDim.Render(string(row[right:])))
			}
		} else {
			b.WriteString(accountSwitchDim.Render(string(row)))
		}
		if y < m.height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func init() {
	claudeAccountSwitchCmd.Flags().StringVar(&casList, "list", "", "Path to accounts list (label:dir)")
	claudeAccountSwitchCmd.Flags().StringVar(&casAccountDir, "accounts-dir", "", "Path to accounts directory")
	claudeAccountSwitchCmd.Flags().StringVar(&casPointer, "pointer", "", "Path to active account pointer file")
	claudeAccountSwitchCmd.Flags().StringVar(&casColors, "colors", "", "Path to account colors file")
	claudeAccountSwitchCmd.Flags().StringVar(&casDefaultLabel, "default-label", "", "Path to the Default login's label file")
	claudeAccountSwitchCmd.Flags().StringVar(&casBackdrop, "backdrop-file", "", "File with a serialized screen capture shown dimmed behind the popup")
	claudeAccountSwitchCmd.Flags().StringVar(&casActive, "active", "", "Dir of the account THIS pane is running (empty = Default); marks the active row")
	claudeAccountSwitchCmd.Flags().StringVar(&casResultFile, "result-file", "", "File to write the chosen dir to on selection (absent on cancel)")
	claudeAccountSwitchCmd.Flags().StringVar(&casTools, "tools", "", "Comma-separated other available AI tools, each shown as an agent row after the logins")
	claudeAccountSwitchCmd.Flags().StringVar(&casActiveTool, "active-tool", "", "Tool THIS pane is running (default claude); marks the active row for non-claude agents")
	rootCmd.AddCommand(claudeAccountSwitchCmd)
}
