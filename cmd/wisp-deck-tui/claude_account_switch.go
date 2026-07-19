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
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
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
	casConfigs      string
	casConfigsDir   string
	casActiveConfig string
)

// switchRow is one selectable entry in the switcher: a claude login (Dir set,
// "" = the implicit Default/Keychain login), a subscription/backend (Config set
// — the settings filename launched via `claude --settings`), or another AI
// agent (Tool set, e.g. "opencode"). Exactly one of Dir/Config/Tool is
// meaningful per row. Ready is honored only for subscription rows: a keyless
// API provider is shown but not selectable.
type switchRow struct {
	Label  string
	Dir    string
	Tool   string
	Config string
	Ready  bool
}

// selectable reports whether the row can be chosen. Account and agent rows
// (no Config) are always selectable; a subscription row needs Ready.
func (r switchRow) selectable() bool {
	return r.Config == "" || r.Ready
}

// switchRowsInput is the full set of inputs buildSwitchRows needs: the claude
// accounts, the subscription backends, and the other AI agents, plus which of
// each the pane currently runs (so the cursor and active dot land on it).
type switchRowsInput struct {
	listFile         string
	defaultLabelFile string
	active           string   // account dir THIS pane runs ("" = Default)
	activeTool       string   // tool THIS pane runs (default "claude")
	tools            []string // other available agents
	configsList      string   // subscriptions list file (name:file)
	configsDir       string   // subscriptions dir (for readiness)
	activeConfig     string   // subscription filename THIS pane runs ("" = standard)
}

// switchRowsForActive builds the ordered row list — Default first, then each
// managed login from the list file — and resolves the initial cursor to the row
// whose dir is active ("" = the Default row). active is the account THIS pane
// runs (bash passes it via --active from the tmux session env): the global
// pointer can't serve here, because a switch in another session (or the
// launcher) flips it without changing what this pane runs — the popup would
// mark the wrong login and read as the account having "switched back".
func switchRowsForActive(listFile, defaultLabelFile, active string) ([]switchRow, int) {
	return buildSwitchRows(switchRowsInput{listFile: listFile, defaultLabelFile: defaultLabelFile, active: active})
}

// switchRowsForSession is switchRowsForActive extended with agent rows: after
// the claude account rows, one row per other available AI tool (bash passes
// them via --tools). "claude" never gets an agent row — the account rows ARE
// claude. activeTool is the tool THIS pane runs; when it is a non-claude
// agent, the cursor (and active dot) lands on that agent's row instead of a
// claude account.
func switchRowsForSession(listFile, defaultLabelFile, active, activeTool string, tools []string) ([]switchRow, int) {
	return buildSwitchRows(switchRowsInput{
		listFile: listFile, defaultLabelFile: defaultLabelFile,
		active: active, activeTool: activeTool, tools: tools,
	})
}

// buildSwitchRows assembles the switcher's rows in display order —
// [Default, …logins…, …subscriptions…, …agents…] — and resolves the initial
// cursor. Priority for the active row: a non-claude agent the pane runs, else
// the subscription backend it runs, else its claude login. Subscriptions come
// from the claude-configs list; a keyless API provider is included but flagged
// not Ready (shown dimmed, not selectable).
func buildSwitchRows(in switchRowsInput) ([]switchRow, int) {
	rows := []switchRow{{Label: claudeaccount.GetDefaultLabel(in.defaultLabelFile), Dir: ""}}
	for _, acc := range claudeaccount.Load(in.listFile) {
		rows = append(rows, switchRow{Label: acc.Label, Dir: acc.Dir})
	}
	for _, cfg := range claudeconfig.Load(in.configsList) {
		rows = append(rows, switchRow{
			Label:  cfg.Name,
			Config: cfg.File,
			Ready:  claudeconfig.ConfigReady(in.configsDir, cfg),
		})
	}
	for _, tool := range in.tools {
		if tool == "" || tool == "claude" {
			continue
		}
		rows = append(rows, switchRow{Label: models.DisplayName(tool), Tool: tool})
	}
	cursor := 0
	for i, r := range rows {
		if in.activeTool != "" && in.activeTool != "claude" {
			if r.Tool == in.activeTool {
				cursor = i
				break
			}
			continue
		}
		if in.activeConfig != "" {
			if r.Config == in.activeConfig {
				cursor = i
				break
			}
			continue
		}
		if r.Tool == "" && r.Config == "" && r.Dir == in.active {
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
	if r.Config != "" {
		return "config:" + r.Config
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

// configResultJSON is the JSON for a subscription-row choice. Like an agent
// choice it never touches the claude account pointer; the bash side owns
// setting the active-config pointer and relaunching. changed reports whether
// the chosen subscription differs from the one the pane was running.
func configResultJSON(file, activeConfig string) (string, error) {
	out, err := json.Marshal(struct {
		Selected bool   `json:"selected"`
		Config   string `json:"config"`
		Changed  bool   `json:"changed"`
	}{Selected: true, Config: file, Changed: file != activeConfig})
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
	rows, cursor := buildSwitchRows(switchRowsInput{
		listFile:         casList,
		defaultLabelFile: casDefaultLabel,
		active:           active,
		activeTool:       activeTool,
		tools:            tools,
		configsList:      casConfigs,
		configsDir:       casConfigsDir,
		activeConfig:     casActiveConfig,
	})
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
	switch {
	case chosen.Tool != "":
		out, err = selectToolResultJSON(chosen.Tool, activeTool)
	case chosen.Config != "":
		out, err = configResultJSON(chosen.Config, casActiveConfig)
	default:
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

// titleText is the popup's title, rendered on the card's top border: with
// agent rows present the popup switches between agents, not just claude
// logins — the title says so.
func (m accountSwitchModel) titleText() string {
	if m.headerLines() > 0 {
		return "Switch agent"
	}
	return "Switch Claude login"
}

func (m accountSwitchModel) Init() tea.Cmd { return nil }

// nextSelectable / prevSelectable move the cursor to the next/previous
// selectable row, skipping over not-ready subscription rows. With no
// selectable row in that direction the cursor stays put.
func (m accountSwitchModel) nextSelectable(from int) int {
	for i := from + 1; i < len(m.rows); i++ {
		if m.rows[i].selectable() {
			return i
		}
	}
	return from
}

func (m accountSwitchModel) prevSelectable(from int) int {
	for i := from - 1; i >= 0; i-- {
		if m.rows[i].selectable() {
			return i
		}
	}
	return from
}

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
			m.cursor = m.prevSelectable(m.cursor)
		case "down", "j":
			m.cursor = m.nextSelectable(m.cursor)
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
				if !m.rows[idx].selectable() {
					// A not-ready subscription: ignore the click, keep the popup open.
					return m, nil
				}
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
	accountSwitchPadX = 1 // slim — agent rows hug the card's left edge
	accountSwitchPadY = 1 // top only — the help footer hugs the bottom border
	// accountSwitchPadBottom is 0: the dim help line reads as the card's
	// closing chrome, so it sits directly above the border instead of
	// floating a padding row above it.
	accountSwitchPadBottom = 0
	accountSwitchBorder    = 1
	accountSwitchHeader    = 0 // the title sits on the border, rows start at the top
	accountSwitchFooter    = 2 // blank line + help below the rows
)

// accountSwitchLayout maps the centered card onto screen coordinates so a mouse
// click can be resolved to a login row. contentW is the widest inner line. It
// returns the screen Y of the first login row and the card's left column + width.
func accountSwitchLayout(termW, termH, numRows, contentW int) (firstRowY, cardLeft, cardWidth int) {
	cardWidth = contentW + 2*accountSwitchPadX + 2*accountSwitchBorder
	innerH := accountSwitchHeader + numRows + accountSwitchFooter
	cardHeight := innerH + accountSwitchPadY + accountSwitchPadBottom + 2*accountSwitchBorder
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
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activeDot := lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render("●")

	// The title lives on the card's top border (see View), so the content
	// block starts right at the rows. With agent rows present the claude
	// logins render as a subgroup under a non-selectable "Claude" header so
	// they visibly belong to the Claude agent while the other agents stay
	// top-level rows.
	grouped := m.headerLines() > 0
	var lines []string
	// Inactive rows gray out so the brand colors highlight only the running
	// row and the cursor. The 244 matches the footer's dim gray.
	const grayRow = 244
	if grouped {
		// The header is "active" while the pane runs claude or the cursor sits
		// on one of its nested logins; otherwise it grays with the rest.
		headerColor := grayRow
		if m.rows[m.active].Tool == "" || m.rows[m.cursor].Tool == "" {
			headerColor = toolRowColor("claude")
		}
		headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(headerColor)))
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
		if r.Config != "" {
			// Subscription rows: one shared accent + the subscription spark.
			color = configRowColor()
			glyph = configRowGlyph()
		}
		if i != m.cursor && i != m.active {
			color = grayRow
		}
		if r.Config != "" && !r.Ready {
			// A not-ready subscription stays dimmed even under the cursor — it
			// can't be selected until it's set up in the Subscription modal.
			color = grayRow
		}
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color)))

		marker := "  "
		label := labelStyle.Render(glyph + " " + r.Label)
		if i == m.cursor {
			marker = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color))).Bold(true).Render("▌ ")
			label = labelStyle.Bold(true).Render(glyph + " " + r.Label)
		}
		// Nested login rows indent BEFORE the marker so the cursor bar moves in
		// with the row instead of floating at the card's left edge. Four
		// columns past the agent rows, so the hierarchy reads clearly.
		indent := ""
		if grouped && r.Tool == "" {
			indent = "    "
		}
		line := indent + marker + label
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

// configRowGlyph is the mark for a subscription/backend row — a four-pointed
// spark, distinct from the account person and the agent brand icons.
func configRowGlyph() string { return "✦" }

// configRowColor is the accent hue for a ready subscription row: one shared
// blue, distinct from claude orange / codex teal / opencode purple.
func configRowColor() int { return 111 }

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
		Padding(accountSwitchPadY, accountSwitchPadX, accountSwitchPadBottom, accountSwitchPadX)
}

func (m accountSwitchModel) View() string {
	// Paint nothing until the popup's size arrives: bubbletea's first real frame
	// is then already the full-screen composite, not a small partial card that
	// would leave the popup's edge rows stale (mirrors the diff modal's !ready gate).
	if m.width == 0 || m.height == 0 {
		return ""
	}
	card := accountSwitchCardStyle().Render(strings.Join(m.innerLines(), "\n"))
	card = embedBorderTitle(card, m.titleText(), m.contentWidth()+2*accountSwitchPadX)
	firstRowY, cardLeft, cardWidth := accountSwitchLayout(m.width, m.height, len(m.rows)+m.headerLines(), m.contentWidth())
	cardTop := firstRowY - accountSwitchBorder - accountSwitchPadY - accountSwitchHeader
	return m.composite(card, cardLeft, cardTop, cardWidth)
}

// embedBorderTitle rewrites the card's top border line as
// "╭─ <title> ─────╮" — lipgloss has no border-title support, so the line is
// reconstructed by hand. innerW is the width between the two corner glyphs; a
// title too wide to fit (with its dashes and spaces) leaves the border as-is.
func embedBorderTitle(card, title string, innerW int) string {
	rest := innerW - lipgloss.Width(title) - 3 // "─ " before, " " after
	if rest < 1 {
		return card
	}
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	top := borderStyle.Render("╭─") + " " + titleStyle.Render(title) + " " +
		borderStyle.Render(strings.Repeat("─", rest)+"╮")
	lines := strings.Split(card, "\n")
	lines[0] = top
	return strings.Join(lines, "\n")
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
	claudeAccountSwitchCmd.Flags().StringVar(&casConfigs, "configs", "", "Path to subscriptions list (name:file), each shown as a backend row under Claude")
	claudeAccountSwitchCmd.Flags().StringVar(&casConfigsDir, "configs-dir", "", "Path to the subscriptions directory (for readiness checks)")
	claudeAccountSwitchCmd.Flags().StringVar(&casActiveConfig, "active-config", "", "Subscription filename THIS pane is running (empty = standard Claude); marks the active row")
	rootCmd.AddCommand(claudeAccountSwitchCmd)
}
