package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
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

// accountSwitchModel is the thin Bubbletea shell: it only tracks the cursor and
// whether a row was chosen; all persistence lives in the pure helpers above.
type accountSwitchModel struct {
	rows       []switchRow
	cursor     int
	colorsFile string
	chosen     bool
}

func newAccountSwitchModel(rows []switchRow, cursor int, colorsFile string) accountSwitchModel {
	return accountSwitchModel{rows: rows, cursor: cursor, colorsFile: colorsFile}
}

func (m accountSwitchModel) Init() tea.Cmd { return nil }

func (m accountSwitchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
			// Rows are rendered one per line starting at the row after the title
			// and its blank line (title at y=0, blank at y=1, first row at y=2).
			idx := msg.Y - accountSwitchFirstRowY
			if idx >= 0 && idx < len(m.rows) {
				m.cursor = idx
				m.chosen = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// accountSwitchFirstRowY is the screen row of the first login (title line + one
// blank line precede it), used to map a mouse click back to a row index.
const accountSwitchFirstRowY = 2

func (m accountSwitchModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	var b strings.Builder
	b.WriteString(titleStyle.Render("Switch Claude login"))
	b.WriteString("\n\n")

	for i, r := range m.rows {
		color := claudeaccount.ColorFor(m.colorsFile, r.Dir)
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(color)))

		cursor := "  "
		if i == m.cursor {
			cursor = lipgloss.NewStyle().Bold(true).Render("▌ ")
		}
		label := labelStyle.Render("󰀄 " + r.Label)
		if i == m.cursor {
			label = labelStyle.Bold(true).Render("󰀄 " + r.Label)
		}
		line := cursor + label
		if i == m.cursor {
			line += "   " + lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render("●")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑↓ move · ⏎ switch · esc cancel"))
	return b.String()
}

func init() {
	claudeAccountSwitchCmd.Flags().StringVar(&casList, "list", "", "Path to accounts list (label:dir)")
	claudeAccountSwitchCmd.Flags().StringVar(&casAccountDir, "accounts-dir", "", "Path to accounts directory")
	claudeAccountSwitchCmd.Flags().StringVar(&casPointer, "pointer", "", "Path to active account pointer file")
	claudeAccountSwitchCmd.Flags().StringVar(&casColors, "colors", "", "Path to account colors file")
	claudeAccountSwitchCmd.Flags().StringVar(&casDefaultLabel, "default-label", "", "Path to the Default login's label file")
	rootCmd.AddCommand(claudeAccountSwitchCmd)
}
