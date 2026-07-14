package tui

import (
	"context"
	"fmt"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/ledger"
	"github.com/mattn/go-runewidth"
)

const (
	ledgerHeaderHeight = 2
	ledgerFooterHeight = 1
)

// LedgerSource loads immutable repository snapshots.
type LedgerSource interface {
	Load(context.Context, string, uint64) (ledger.Snapshot, error)
}

// LedgerOptions configures initial native-ledger state.
type LedgerOptions struct {
	ProjectDir   string
	Loading      bool
	RefreshError error
}

// LedgerModel renders the changes ledger from a viewport-bounded state slice.
type LedgerModel struct {
	source       LedgerSource
	projectDir   string
	state        *ledger.State
	width        int
	height       int
	loading      bool
	refreshError error
	renderRow    func(ledger.Row, int, ledger.RowVisualState) string
}

// NewLedgerModel creates a native ledger model around an immutable snapshot.
func NewLedgerModel(source LedgerSource, snapshot ledger.Snapshot, options LedgerOptions) *LedgerModel {
	state := ledger.NewState(snapshot)
	state.Resize(80, 24, ledgerHeaderHeight, ledgerFooterHeight)
	return &LedgerModel{
		source:       source,
		projectDir:   options.ProjectDir,
		state:        state,
		width:        80,
		height:       24,
		loading:      options.Loading,
		refreshError: options.RefreshError,
		renderRow:    renderLedgerRow,
	}
}

// Init is intentionally inert until the asynchronous update loop is attached.
func (m *LedgerModel) Init() tea.Cmd { return nil }

// Update is intentionally inert until input and refresh behavior is added.
func (m *LedgerModel) Update(_ tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

// View renders fixed chrome and only the currently visible snapshot rows.
func (m *LedgerModel) View() string {
	width := m.width
	if width < 1 {
		width = 1
	}
	lines := make([]string, 0, m.height)
	lines = append(lines, renderLedgerHeader(m.state.Snapshot.Metadata, width)...)

	visible := m.state.VisibleRows()
	if len(m.state.Snapshot.Rows) == 0 {
		message := " no changes"
		switch {
		case m.loading:
			message = " loading changes…"
		case m.refreshError != nil:
			message = " refresh failed · " + m.refreshError.Error()
		}
		lines = append(lines, ledgerFitPlain(message, width))
	} else {
		for _, row := range visible {
			visual := ledger.RowVisualState{
				Hovered:  row.Kind == ledger.RowFile && row.ID == m.state.Hovered,
				Selected: row.Kind == ledger.RowFile && m.state.IsSelected(row.Path),
			}
			lines = append(lines, m.renderRow(row, width, visual))
		}
	}

	bodyLines := len(lines) - ledgerHeaderHeight
	for bodyLines < m.state.ViewportHeight() {
		lines = append(lines, "")
		bodyLines++
	}
	lines = append(lines, renderLedgerFooter(m.state, width))
	return strings.Join(lines, "\n")
}

func renderLedgerHeader(metadata ledger.Metadata, width int) []string {
	plan := metadata.Plan
	stamp := ""
	if metadata.TotalFiles > 0 {
		unit := "files"
		if metadata.TotalFiles == 1 {
			unit = "file"
		}
		stamp = fmt.Sprintf("%d %s  +%d −%d", metadata.TotalFiles, unit, metadata.Added, metadata.Deleted)
	}
	available := width - visibleRuneWidth(plan) - visibleRuneWidth(stamp) - 2
	if available < 1 {
		available = 1
	}
	line := " " + plan + strings.Repeat(" ", available) + stamp
	line = ledgerFitPlain(line, width)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ruleWidth := width - 2
	if ruleWidth < 0 {
		ruleWidth = 0
	}
	rule := " " + strings.Repeat("─", ruleWidth)
	return []string{dim.Render(line), dim.Faint(true).Render(rule)}
}

func renderLedgerRow(row ledger.Row, width int, visual ledger.RowVisualState) string {
	switch row.Kind {
	case ledger.RowGroup:
		label := fmt.Sprintf(" ● %s  (%d)", row.Label, row.Count)
		return ledgerFitPlain(label, width)
	case ledger.RowSpacer:
		return ""
	case ledger.RowFile:
		return renderLedgerFileRow(row, width, visual)
	default:
		return ""
	}
}

func renderLedgerFileRow(row ledger.Row, width int, visual ledger.RowVisualState) string {
	indent := "   "
	if visual.Selected {
		indent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")).Render(" ☑ ")
	} else if visual.Hovered {
		indent = lipgloss.NewStyle().Faint(true).Render(" ☐ ")
	}

	var prefix string
	if row.Binary {
		delta := row.NewBytes - row.OldBytes
		figure := "±0"
		if delta > 0 {
			figure = "+" + ledgerHumanBytes(delta)
		} else if delta < 0 {
			figure = "−" + ledgerHumanBytes(-delta)
		}
		prefix = indent + fmt.Sprintf("%-9s  ", figure)
	} else {
		add := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("+%-3d", row.Added))
		del := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("−%-3d", row.Deleted))
		prefix = indent + add + " " + del + "  "
	}

	nameWidth := width - lipgloss.Width(prefix)
	if nameWidth < 1 {
		nameWidth = 1
	}
	name := runewidth.Truncate(path.Base(row.Path), nameWidth, "…")
	line := prefix + lipgloss.NewStyle().Foreground(currentTheme.Text).Render(name)
	if visual.Hovered {
		line = lipgloss.NewStyle().
			Background(lipgloss.Color("238")).
			Width(width).
			MaxWidth(width).
			Render(line)
	}
	return line
}

func renderLedgerFooter(state *ledger.State, width int) string {
	metadata := state.Snapshot.Metadata
	branch := metadata.Branch
	if branch == "" {
		branch = "detached"
	}
	parts := []string{" " + branch}
	if metadata.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", metadata.Ahead))
	}
	if metadata.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", metadata.Behind))
	}
	if state.MaxScroll() > 0 {
		first := state.Scroll + 1
		last := state.Scroll + state.ViewportHeight()
		if last > len(state.Snapshot.Rows) {
			last = len(state.Snapshot.Rows)
		}
		parts = append(parts, fmt.Sprintf("%d-%d/%d", first, last, len(state.Snapshot.Rows)))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(
		ledgerFitPlain(strings.Join(parts, " · "), width),
	)
}

func ledgerHumanBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func ledgerFitPlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(value, width, "…")
}

func visibleRuneWidth(value string) int {
	return runewidth.StringWidth(value)
}
