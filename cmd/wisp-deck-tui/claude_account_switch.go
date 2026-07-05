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
)

// switchRow is one selectable login in the switcher. Dir is "" for the implicit
// Default (Keychain) login.
type switchRow struct {
	Label string
	Dir   string
}

// loadSwitchRows builds the ordered row list — Default first, then each managed
// login from the list file — and resolves the initial cursor to the currently
// active row (0 when Default is active or the pointer is absent).
func loadSwitchRows(listFile, defaultLabelFile, pointerFile string) ([]switchRow, int) {
	rows := []switchRow{{Label: claudeaccount.GetDefaultLabel(defaultLabelFile), Dir: ""}}
	for _, acc := range claudeaccount.Load(listFile) {
		rows = append(rows, switchRow{Label: acc.Label, Dir: acc.Dir})
	}
	active := claudeaccount.GetActive(pointerFile)
	cursor := 0
	for i, r := range rows {
		if r.Dir == active {
			cursor = i
			break
		}
	}
	return rows, cursor
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
	rows, cursor := loadSwitchRows(casList, casDefaultLabel, casPointer)
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
	out, err := selectResultJSON(casPointer, rows[m.cursor].Dir, prevActive)
	if err != nil {
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
			firstRowY, cardLeft, cardWidth := accountSwitchLayout(m.width, m.height, len(m.rows), m.contentWidth())
			idx := msg.Y - firstRowY
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

	lines := []string{titleStyle.Render("Switch Claude login"), ""}

	for i, r := range m.rows {
		color := claudeaccount.ColorFor(m.colorsFile, r.Dir)
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color)))

		marker := "  "
		label := labelStyle.Render("󰀄 " + r.Label)
		if i == m.cursor {
			marker = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color))).Bold(true).Render("▌ ")
			label = labelStyle.Bold(true).Render("󰀄 " + r.Label)
		}
		line := marker + label
		if i == m.active {
			line += "  " + activeDot
		}
		lines = append(lines, line)
	}

	lines = append(lines, "", dimStyle.Render("↑↓ move · ⏎ switch · esc cancel"))
	return lines
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

// accountSwitchDim renders the close area (everything outside the card) as a
// half-transparent backdrop: a dim dark background tint (the scrim) with the
// captured screen behind it drawn faint/gray, so the session shows through
// darkened rather than as a solid opaque block.
var accountSwitchDim = lipgloss.NewStyle().
	Faint(true).
	Foreground(lipgloss.Color("240")).
	Background(lipgloss.Color("#14141b"))

func (m accountSwitchModel) View() string {
	// Paint nothing until the popup's size arrives: bubbletea's first real frame
	// is then already the full-screen composite, not a small partial card that
	// would leave the popup's edge rows stale (mirrors the diff modal's !ready gate).
	if m.width == 0 || m.height == 0 {
		return ""
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(accountSwitchPadY, accountSwitchPadX).
		Render(strings.Join(m.innerLines(), "\n"))
	firstRowY, cardLeft, cardWidth := accountSwitchLayout(m.width, m.height, len(m.rows), m.contentWidth())
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
	rootCmd.AddCommand(claudeAccountSwitchCmd)
}
