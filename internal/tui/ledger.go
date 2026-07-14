package tui

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

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
	ProjectDir      string
	Loading         bool
	RefreshError    error
	RefreshInterval time.Duration
	Mutator         ledger.Mutator
}

// LedgerModel renders the changes ledger from a viewport-bounded state slice.
type LedgerModel struct {
	source              LedgerSource
	projectDir          string
	state               *ledger.State
	width               int
	height              int
	loading             bool
	refreshError        error
	refreshInterval     time.Duration
	requestedGeneration uint64
	loadCancel          context.CancelFunc
	mutator             ledger.Mutator
	discardArmed        bool
	discardPaths        []string
	discarding          bool
	actionError         error
	renderRow           func(ledger.Row, int, ledger.RowVisualState) string
}

type ledgerSnapshotMsg struct {
	generation uint64
	snapshot   ledger.Snapshot
}

type ledgerLoadErrMsg struct {
	generation uint64
	err        error
}

type ledgerRefreshTickMsg struct{}

type ledgerDiscardDoneMsg struct {
	err error
}

// NewLedgerModel creates a native ledger model around an immutable snapshot.
func NewLedgerModel(source LedgerSource, snapshot ledger.Snapshot, options LedgerOptions) *LedgerModel {
	state := ledger.NewState(snapshot)
	state.Resize(80, 24, ledgerHeaderHeight, ledgerFooterHeight)
	interval := options.RefreshInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &LedgerModel{
		source:              source,
		projectDir:          options.ProjectDir,
		state:               state,
		width:               80,
		height:              24,
		loading:             options.Loading,
		refreshError:        options.RefreshError,
		refreshInterval:     interval,
		requestedGeneration: snapshot.Generation,
		mutator:             options.Mutator,
		renderRow:           renderLedgerRow,
	}
}

// Init schedules the initial snapshot without running Git on the Tea loop.
func (m *LedgerModel) Init() tea.Cmd { return m.startLoad() }

// Update applies constant-time interaction changes and generation-checked load
// results. All blocking work is returned as a Tea command.
func (m *LedgerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.state.Resize(msg.Width, msg.Height, ledgerHeaderHeight, ledgerFooterHeight)
		return m, nil
	case tea.MouseMsg:
		return m, m.handleLedgerMouse(msg)
	case tea.KeyMsg:
		return m, m.handleLedgerKey(msg)
	case ledgerRefreshTickMsg:
		return m, m.startLoad()
	case ledgerSnapshotMsg:
		if msg.generation != m.requestedGeneration {
			return m, nil
		}
		m.finishLoad()
		m.state.ReplaceSnapshot(msg.snapshot)
		m.loading = false
		m.refreshError = nil
		return m, m.scheduleRefresh()
	case ledgerLoadErrMsg:
		if msg.generation != m.requestedGeneration {
			return m, nil
		}
		m.finishLoad()
		m.loading = false
		m.refreshError = msg.err
		return m, m.scheduleRefresh()
	case ledgerDiscardDoneMsg:
		m.discarding = false
		if msg.err != nil {
			m.actionError = msg.err
			return m, nil
		}
		m.discardArmed = false
		m.discardPaths = nil
		m.actionError = nil
		m.state.Selected = make(map[string]struct{})
		return m, m.startLoad()
	default:
		return m, nil
	}
}

func (m *LedgerModel) startLoad() tea.Cmd {
	if m.source == nil {
		return nil
	}
	if m.loadCancel != nil {
		m.loadCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.loadCancel = cancel
	m.requestedGeneration++
	generation := m.requestedGeneration
	m.loading = true
	return func() tea.Msg {
		snapshot, err := m.source.Load(ctx, m.projectDir, generation)
		if err != nil {
			return ledgerLoadErrMsg{generation: generation, err: err}
		}
		snapshot.Generation = generation
		return ledgerSnapshotMsg{generation: generation, snapshot: snapshot}
	}
}

func (m *LedgerModel) finishLoad() {
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
}

func (m *LedgerModel) scheduleRefresh() tea.Cmd {
	return tea.Tick(m.refreshInterval, func(time.Time) tea.Msg {
		return ledgerRefreshTickMsg{}
	})
}

func (m *LedgerModel) handleLedgerMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.state.ScrollBy(3)
		m.hoverLedgerMouse(msg)
		return nil
	case tea.MouseButtonWheelUp:
		m.state.ScrollBy(-3)
		m.hoverLedgerMouse(msg)
		return nil
	}
	if msg.Action == tea.MouseActionMotion {
		m.hoverLedgerMouse(msg)
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if cmd, handled := m.handleLedgerDiscardClick(msg); handled {
		return cmd
	}
	m.hoverLedgerMouse(msg)
	if msg.X >= 0 && msg.X < 3 {
		m.toggleHoveredSelection()
	}
	return nil
}

func (m *LedgerModel) hoverLedgerMouse(msg tea.MouseMsg) {
	if msg.X < 0 || msg.X >= m.width {
		m.state.HoverScreenRow(0)
		return
	}
	// Bubble Tea mouse coordinates are zero-based; ledger.State accepts the
	// terminal's one-based screen row.
	m.state.HoverScreenRow(msg.Y + 1)
}

func (m *LedgerModel) handleLedgerKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyEsc && m.discardArmed && !m.discarding {
		m.cancelDiscard()
		return nil
	}
	switch msg.Type {
	case tea.KeyDown:
		m.state.ScrollBy(1)
		return nil
	case tea.KeyUp:
		m.state.ScrollBy(-1)
		return nil
	case tea.KeyPgDown:
		m.state.PageBy(1)
		return nil
	case tea.KeyPgUp:
		m.state.PageBy(-1)
		return nil
	case tea.KeyHome:
		m.state.ScrollTo(0)
		return nil
	case tea.KeyEnd:
		m.state.ScrollTo(m.state.MaxScroll())
		return nil
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return nil
	}
	switch msg.Runes[0] {
	case 'x':
		if !m.discardArmed && !m.discarding {
			m.toggleHoveredSelection()
		}
	case 'd':
		if m.discarding {
			return nil
		}
		if m.discardArmed {
			m.cancelDiscard()
		} else {
			m.armDiscard()
		}
	case 'y':
		return m.startDiscard()
	case 'n':
		if m.discardArmed && !m.discarding {
			m.cancelDiscard()
		}
	case 'j':
		m.state.ScrollBy(1)
	case 'k':
		m.state.ScrollBy(-1)
	case ' ', 'f':
		m.state.PageBy(1)
	case 'b':
		m.state.PageBy(-1)
	case 'g':
		m.state.ScrollTo(0)
	case 'G':
		m.state.ScrollTo(m.state.MaxScroll())
	}
	return nil
}

func (m *LedgerModel) hoveredPath() (string, bool) {
	if m.state.Hovered == (ledger.RowID{}) {
		return "", false
	}
	index, ok := m.state.Snapshot.Index(m.state.Hovered)
	if !ok || index < 0 || index >= len(m.state.Snapshot.Rows) {
		return "", false
	}
	row := m.state.Snapshot.Rows[index]
	if row.Kind != ledger.RowFile || row.ID != m.state.Hovered || row.Path == "" {
		return "", false
	}
	return row.Path, true
}

func (m *LedgerModel) toggleHoveredSelection() {
	if path, ok := m.hoveredPath(); ok {
		m.state.ToggleSelected(path)
		m.actionError = nil
	}
}

func (m *LedgerModel) armDiscard() {
	paths := make([]string, 0, len(m.state.Selected))
	for path := range m.state.Selected {
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		if path, ok := m.hoveredPath(); ok {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return
	}
	sort.Strings(paths)
	m.discardPaths = paths
	m.discardArmed = true
	m.actionError = nil
	m.state.ScrollTo(0)
}

func (m *LedgerModel) cancelDiscard() {
	m.discardArmed = false
	m.discardPaths = nil
	m.actionError = nil
}

func (m *LedgerModel) startDiscard() tea.Cmd {
	if !m.discardArmed || m.discarding || m.mutator == nil || len(m.discardPaths) == 0 {
		return nil
	}
	paths := append([]string(nil), m.discardPaths...)
	m.discarding = true
	m.actionError = nil
	return func() tea.Msg {
		err := m.mutator.Discard(context.Background(), m.projectDir, paths)
		return ledgerDiscardDoneMsg{err: err}
	}
}

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
		for index, row := range visible {
			visual := ledger.RowVisualState{
				Hovered:  row.Kind == ledger.RowFile && row.ID == m.state.Hovered,
				Selected: row.Kind == ledger.RowFile && m.state.IsSelected(row.Path),
			}
			line := m.renderRow(row, width, visual)
			if m.state.Scroll+index == 0 && row.Kind == ledger.RowGroup {
				if control, _ := m.ledgerDiscardControl(); control != "" {
					line = ledgerFitPlain(ledgerGroupLabel(row)+"  "+control, width)
				}
			}
			lines = append(lines, line)
		}
	}

	bodyLines := len(lines) - ledgerHeaderHeight
	for bodyLines < m.state.ViewportHeight() {
		lines = append(lines, "")
		bodyLines++
	}
	lines = append(lines, renderLedgerFooter(m.state, width, m.actionError))
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
		return ledgerFitPlain(ledgerGroupLabel(row), width)
	case ledger.RowSpacer:
		return ""
	case ledger.RowFile:
		return renderLedgerFileRow(row, width, visual)
	default:
		return ""
	}
}

func ledgerGroupLabel(row ledger.Row) string {
	return fmt.Sprintf(" ● %s  (%d)", row.Label, row.Count)
}

type ledgerDiscardSpans struct {
	discardStart int
	discardEnd   int
	yesStart     int
	yesEnd       int
	noStart      int
	noEnd        int
}

func (m *LedgerModel) ledgerDiscardControl() (string, ledgerDiscardSpans) {
	if m.state.Scroll != 0 || len(m.state.Snapshot.Rows) == 0 || m.state.Snapshot.Rows[0].Kind != ledger.RowGroup {
		return "", ledgerDiscardSpans{}
	}
	base := visibleRuneWidth(ledgerGroupLabel(m.state.Snapshot.Rows[0])) + 2
	spans := ledgerDiscardSpans{}
	if m.discardArmed {
		count := len(m.discardPaths)
		unit := "files"
		if count == 1 {
			unit = "file"
		}
		prefix := fmt.Sprintf("Discard %d %s? ", count, unit)
		spans.yesStart = base + visibleRuneWidth(prefix)
		spans.yesEnd = spans.yesStart + visibleRuneWidth("[ yes ]")
		spans.noStart = spans.yesEnd + 1
		spans.noEnd = spans.noStart + visibleRuneWidth("[ no ]")
		if spans.noEnd > m.width {
			return ledgerFitPlain(prefix+"[ yes ] [ no ]", m.width-base), ledgerDiscardSpans{}
		}
		return prefix + "[ yes ] [ no ]", spans
	}
	count := len(m.state.Selected)
	if count == 0 {
		return "", spans
	}
	control := fmt.Sprintf("[ discard %d ]", count)
	spans.discardStart = base
	spans.discardEnd = base + visibleRuneWidth(control)
	if spans.discardEnd > m.width {
		return ledgerFitPlain(control, m.width-base), ledgerDiscardSpans{}
	}
	return control, spans
}

func (m *LedgerModel) handleLedgerDiscardClick(msg tea.MouseMsg) (tea.Cmd, bool) {
	if msg.Y != ledgerHeaderHeight || m.state.Scroll != 0 {
		return nil, false
	}
	_, spans := m.ledgerDiscardControl()
	inside := func(start, end int) bool { return end > start && msg.X >= start && msg.X < end }
	if m.discardArmed {
		if inside(spans.yesStart, spans.yesEnd) {
			return m.startDiscard(), true
		}
		if inside(spans.noStart, spans.noEnd) {
			if !m.discarding {
				m.cancelDiscard()
			}
			return nil, true
		}
		return nil, false
	}
	if inside(spans.discardStart, spans.discardEnd) {
		m.armDiscard()
		return nil, true
	}
	return nil, false
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

func renderLedgerFooter(state *ledger.State, width int, actionError error) string {
	if actionError != nil {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(
			ledgerFitPlain(" discard failed · "+actionError.Error(), width),
		)
	}
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
