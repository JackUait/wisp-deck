package tui

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/ledger"
	"github.com/mattn/go-runewidth"
)

const (
	// The pane has no top header: the first rendered line is the first
	// snapshot row. The changed-file stamp moved into the footer.
	ledgerHeaderHeight = 0
	ledgerFooterHeight = 1
)

// LedgerSource loads immutable repository snapshots.
type LedgerSource interface {
	Load(context.Context, string, uint64) (ledger.Snapshot, error)
}

// LedgerSessionSource loads the relaunch context and current account identity.
type LedgerSessionSource interface {
	Load(context.Context, string) (ledger.SessionContext, error)
}

// LedgerOptions configures initial native-ledger state.
type LedgerOptions struct {
	ProjectDir      string
	Loading         bool
	RefreshError    error
	RefreshInterval time.Duration
	Mutator         ledger.Mutator
	Popup           ledger.Popup
	BackdropCache   ledger.BackdropCache
	Tool            string
	SessionSource   LedgerSessionSource
	SessionPath     string
	AccountSwitcher ledger.AccountSwitcher
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
	popup               ledger.Popup
	backdropCache       ledger.BackdropCache
	tool                string
	backdropRefreshing  bool
	backdropRefreshedAt time.Time
	opening             bool
	openedPath          string
	openCancel          context.CancelFunc
	sessionSource       LedgerSessionSource
	sessionPath         string
	session             ledger.SessionContext
	sessionLoading      bool
	accountSwitcher     ledger.AccountSwitcher
	accountHover        bool
	switchingAccount    bool
	accountCancel       context.CancelFunc
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

type ledgerPopupDoneMsg struct {
	path   string
	result ledger.OpenResult
	err    error
}

type ledgerBackdropReadyMsg struct {
	err error
}

type ledgerSessionMsg struct {
	session ledger.SessionContext
	err     error
}

type ledgerAccountSwitchDoneMsg struct {
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
		popup:               options.Popup,
		backdropCache:       options.BackdropCache,
		tool:                options.Tool,
		sessionSource:       options.SessionSource,
		sessionPath:         options.SessionPath,
		accountSwitcher:     options.AccountSwitcher,
		renderRow:           renderLedgerRow,
	}
}

// Init schedules the initial snapshot without running Git on the Tea loop.
func (m *LedgerModel) Init() tea.Cmd {
	if m.sessionSource != nil && m.sessionPath != "" {
		return tea.Batch(m.startLoad(), m.loadSession())
	}
	return m.startLoad()
}

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
		return m, m.withBackdropRefresh(m.handleLedgerMouse(msg))
	case tea.KeyMsg:
		return m, m.withBackdropRefresh(m.handleLedgerKey(msg))
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
		// Reload the session identity on every refresh, exactly like the bash
		// renderer recomputes the pill each build tick. wrapper.sh writes the
		// relaunch context AFTER the tmux batch that spawns this pane, so the
		// first load can find no file — or a half-written one — and a one-shot
		// load would leave the pill hidden for the pane's whole life.
		return m, tea.Batch(m.scheduleRefresh(), m.loadSession())
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
	case ledgerPopupDoneMsg:
		if msg.path != m.openedPath {
			return m, nil
		}
		m.finishOpen()
		if msg.err != nil {
			m.actionError = msg.err
		} else {
			m.actionError = nil
		}
		return m, tea.Batch(m.startLoad(), m.refreshBackdrop())
	case ledgerBackdropReadyMsg:
		m.backdropRefreshing = false
		return m, nil
	case ledgerSessionMsg:
		m.sessionLoading = false
		if msg.err != nil {
			// Keep the last good identity. A reload fails for reasons that say
			// nothing about the session (the context being rewritten by a
			// mid-session switch, a tmux hiccup); discarding it would blink the
			// pill — the pane's identity and its only switch affordance — out of
			// the footer until the next tick repaired it.
			return m, nil
		}
		m.session = msg.session
		return m, nil
	case ledgerAccountSwitchDoneMsg:
		m.finishAccountSwitch()
		m.actionError = msg.err
		return m, tea.Batch(m.startLoad(), m.loadSession(), m.refreshBackdrop())
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

// withBackdropRefresh arms a backdrop rebuild alongside whatever the
// interaction itself produced.
//
// An interaction that OPENED a popup is excluded: that popup is already reading
// the backdrop, so rebuilding now could only race it, and a click has never
// been allowed to wait on or start one. The hover or keystroke that necessarily
// preceded the click is what armed it.
func (m *LedgerModel) withBackdropRefresh(handled tea.Cmd) tea.Cmd {
	if m.opening {
		return handled
	}
	return tea.Batch(handled, m.refreshBackdrop())
}

// backdropRefreshThrottle is the shortest gap between two backdrop rebuilds.
// A mouse crossing the pane emits a motion event per cell, and one rebuild
// spawns a tmux capture of every pane, so the burst has to collapse to one.
const backdropRefreshThrottle = 750 * time.Millisecond

// refreshBackdrop rebuilds the dimmed screen painted behind the diff popup.
//
// It is armed by user interaction, never by the data refresh. The backdrop is
// only ever read when a popup opens, and a popup cannot open until the user has
// moved onto a row, so an unattended pane needs none at all -- it used to
// rebuild one every 2 seconds regardless, spawning a `tmux capture-pane` per
// pane and rewriting a temp file, in every open session, forever.
func (m *LedgerModel) refreshBackdrop() tea.Cmd {
	if m.backdropCache == nil || m.backdropRefreshing {
		return nil
	}
	now := time.Now()
	if !m.backdropRefreshedAt.IsZero() && now.Sub(m.backdropRefreshedAt) < backdropRefreshThrottle {
		return nil
	}
	m.backdropRefreshedAt = now
	m.backdropRefreshing = true
	return func() tea.Msg {
		return ledgerBackdropReadyMsg{err: m.backdropCache.Refresh(context.Background())}
	}
}

func (m *LedgerModel) loadSession() tea.Cmd {
	if m.sessionSource == nil || m.sessionPath == "" || m.sessionLoading {
		return nil
	}
	m.sessionLoading = true
	return func() tea.Msg {
		session, err := m.sessionSource.Load(context.Background(), m.sessionPath)
		return ledgerSessionMsg{session: session, err: err}
	}
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
	if m.ledgerAccountHit(msg.X, msg.Y) {
		return m.openAccountSwitch()
	}
	if cmd, handled := m.handleLedgerDiscardClick(msg); handled {
		return cmd
	}
	m.hoverLedgerMouse(msg)
	if msg.X >= 0 && msg.X < 3 {
		m.toggleHoveredSelection()
		return nil
	}
	return m.openHovered()
}

func (m *LedgerModel) hoverLedgerMouse(msg tea.MouseMsg) {
	m.accountHover = m.ledgerAccountHit(msg.X, msg.Y)
	if msg.X < 0 || msg.X >= m.width {
		m.state.HoverScreenRow(0)
		return
	}
	// Bubble Tea mouse coordinates are zero-based; ledger.State accepts the
	// terminal's one-based screen row.
	m.state.HoverScreenRow(msg.Y + 1)
}

// ledgerPillText is the footer pill's drawable text: pad, identity glyph (the
// account person unless the pill carries its own, e.g. the subscription
// spark), label, pad. Width and render must agree, so both go through here.
func ledgerPillText(pill *ledger.SessionPill) string {
	glyph := pill.Glyph
	if glyph == "" {
		glyph = "󰀄"
	}
	return " " + glyph + " " + pill.Label + " "
}

// ledgerPillStyle is the pill's color, plus the hover highlight while the
// pointer sits over its click span.
func ledgerPillStyle(pill *ledger.SessionPill, pillHover bool) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("%d", pill.Color)))
	if pillHover {
		style = style.Background(lipgloss.Color("238"))
	}
	return style
}

func renderLedgerPill(pill *ledger.SessionPill, pillHover bool) string {
	return ledgerPillStyle(pill, pillHover).Render(ledgerPillText(pill))
}

func ledgerAccountPillWidth(pill *ledger.SessionPill) int {
	if pill == nil {
		return 0
	}
	return visibleRuneWidth(ledgerPillText(pill))
}

func (m *LedgerModel) ledgerAccountHit(x, y int) bool {
	width := ledgerAccountPillWidth(m.session.Pill)
	if width > m.width {
		width = m.width
	}
	return width > 0 && y == m.height-1 && x >= 0 && x < width
}

// openAccountSwitch floats the switcher popup over the agent pane. The popup
// (account-switch.sh's open_account_switcher) both presents the rows and applies
// the choice, so this returns a Tea command and never renders an in-ledger card.
// The eligibility guard keeps a click from spawning a popup with nothing to pick.
func (m *LedgerModel) openAccountSwitch() tea.Cmd {
	if m.accountSwitcher == nil || m.session.Pill == nil || m.switchingAccount ||
		len(m.session.SwitchOptions) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.accountCancel = cancel
	m.switchingAccount = true
	m.accountHover = false
	m.actionError = nil
	session := m.session
	return func() tea.Msg {
		return ledgerAccountSwitchDoneMsg{err: m.accountSwitcher.OpenSwitcher(ctx, session)}
	}
}

func (m *LedgerModel) finishAccountSwitch() {
	if m.accountCancel != nil {
		m.accountCancel()
		m.accountCancel = nil
	}
	m.switchingAccount = false
	m.accountHover = false
}

func (m *LedgerModel) handleLedgerKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyCtrlC {
		m.cancelLedgerWork()
		return tea.Quit
	}
	if msg.Type == tea.KeyEsc && m.discardArmed && !m.discarding {
		m.cancelDiscard()
		return nil
	}
	if msg.Type == tea.KeyEnter && !m.discardArmed && !m.discarding {
		return m.openHovered()
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

func (m *LedgerModel) cancelLedgerWork() {
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
	if m.openCancel != nil {
		m.openCancel()
		m.openCancel = nil
	}
	if m.accountCancel != nil {
		m.accountCancel()
		m.accountCancel = nil
	}
}

func (m *LedgerModel) hoveredRow() (ledger.Row, bool) {
	if m.state.Hovered == (ledger.RowID{}) {
		return ledger.Row{}, false
	}
	index, ok := m.state.Snapshot.Index(m.state.Hovered)
	if !ok || index < 0 || index >= len(m.state.Snapshot.Rows) {
		return ledger.Row{}, false
	}
	row := m.state.Snapshot.Rows[index]
	if row.Kind != ledger.RowFile || row.ID != m.state.Hovered || row.Path == "" {
		return ledger.Row{}, false
	}
	return row, true
}

func (m *LedgerModel) hoveredPath() (string, bool) {
	row, ok := m.hoveredRow()
	if !ok {
		return "", false
	}
	return row.Path, true
}

// opensImagePreview reports whether clicking row should open the image PREVIEW
// popup rather than the diff popup: an extension the pager decodes, whose file
// is on disk right now. Presence is checked directly (not via row.NewBytes,
// which git hydrates only for BINARY changes) so a text-tracked image like an
// SVG previews too, and so a deleted image still falls back to the diff instead
// of cat-ing a path that is gone. Same two conditions the shell renderer
// applies — `is_image_file` and `[ -f ]`.
func opensImagePreview(projectDir string, row ledger.Row) bool {
	if row.Path == "" || !IsPreviewableImage(row.Path) {
		return false
	}
	info, err := os.Stat(filepath.Join(projectDir, row.Path))
	return err == nil && info.Mode().IsRegular()
}

func (m *LedgerModel) openHovered() tea.Cmd {
	row, ok := m.hoveredRow()
	if !ok || m.popup == nil || m.opening {
		return nil
	}
	backdrop := ""
	if m.backdropCache != nil {
		if latest, ready := m.backdropCache.Latest(); ready {
			backdrop = latest
		}
	}
	image := opensImagePreview(m.projectDir, row)
	status := "modified"
	if row.ID.Group == ledger.GroupNew {
		status = "added"
	}
	request := ledger.OpenRequest{
		ProjectDir: m.projectDir, Path: row.Path, Tool: m.tool,
		BackdropFile: backdrop, Tracked: row.ID.Group != ledger.GroupNew,
		Image: image, Status: status,
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.openCancel = cancel
	m.opening = true
	m.openedPath = row.Path
	m.actionError = nil
	return func() tea.Msg {
		result, err := m.popup.Open(ctx, request)
		if err == nil && result.Discard && m.mutator != nil {
			err = m.mutator.Discard(ctx, m.projectDir, []string{row.Path})
		}
		return ledgerPopupDoneMsg{path: row.Path, result: result, err: err}
	}
}

func (m *LedgerModel) finishOpen() {
	if m.openCancel != nil {
		m.openCancel()
		m.openCancel = nil
	}
	m.opening = false
	m.openedPath = ""
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

	visible := m.state.VisibleRows()
	if len(m.state.Snapshot.Rows) == 0 {
		switch {
		case m.loading:
			lines = append(lines, ledgerFitPlain(" loading changes…", width))
		case m.refreshError != nil:
			lines = append(lines, ledgerFitPlain(" refresh failed · "+m.refreshError.Error(), width))
		default:
			lines = append(lines,
				renderLedgerEmptyState(currentTheme, width, m.state.ViewportHeight())...)
		}
	} else {
		for index, row := range visible {
			visual := ledger.RowVisualState{
				Hovered:  row.Kind == ledger.RowFile && row.ID == m.state.Hovered,
				Selected: row.Kind == ledger.RowFile && m.state.IsSelected(row.Path),
			}
			line := m.renderRow(row, width, visual)
			if m.state.Scroll+index == 0 && row.Kind == ledger.RowGroup {
				if control, _ := m.ledgerDiscardControl(); control != "" {
					line = renderLedgerGroupRow(row, width, control)
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
	lines = append(lines, renderLedgerFooter(m.state, width, m.actionError, m.session.Pill, m.accountHover, m.sessionTool()))
	return strings.Join(lines, "\n")
}

// ledgerStampText is the plain changed-file summary ("8 files  +356 −0"), or
// "" when nothing changed.
func ledgerStampText(metadata ledger.Metadata) string {
	if metadata.TotalFiles <= 0 {
		return ""
	}
	unit := "files"
	if metadata.TotalFiles == 1 {
		unit = "file"
	}
	return fmt.Sprintf("%d %s  +%d −%d", metadata.TotalFiles, unit, metadata.Added, metadata.Deleted)
}

// renderLedgerStamp colors the changed-file summary: dim text with a green
// +added and a red −deleted. Returns "" when nothing changed.
func renderLedgerStamp(metadata ledger.Metadata) string {
	text := ledgerStampText(metadata)
	if text == "" {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	added := fmt.Sprintf("+%d", metadata.Added)
	deleted := fmt.Sprintf("−%d", metadata.Deleted)
	addedAt := strings.LastIndex(text, added)
	deletedAt := strings.LastIndex(text, deleted)
	if addedAt >= 0 && deletedAt >= addedAt+len(added) {
		green := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
		red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		return dim.Render(text[:addedAt]) +
			green.Render(added) +
			dim.Render(text[addedAt+len(added):deletedAt]) +
			red.Render(deleted) +
			dim.Render(text[deletedAt+len(deleted):])
	}
	return dim.Render(text)
}

func renderLedgerRow(row ledger.Row, width int, visual ledger.RowVisualState) string {
	switch row.Kind {
	case ledger.RowGroup:
		return renderLedgerGroupRow(row, width, "")
	case ledger.RowSpacer:
		return ""
	case ledger.RowFile:
		return renderLedgerFileRow(row, width, visual)
	default:
		return ""
	}
}

func renderLedgerGroupRow(row ledger.Row, width int, control string) string {
	color, ok := ledgerGroupColor(row.Group)
	if !ok {
		line := ledgerGroupLabel(row)
		if control != "" {
			line += "  " + control
		}
		return ledgerFitPlain(line, width)
	}

	title := lipgloss.NewStyle().Foreground(color)
	dot := title.Bold(true).Render("●")
	label := title.Render(row.Label)
	count := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(fmt.Sprintf("(%d)", row.Count))
	line := " " + dot + " " + label + "  " + count
	if control != "" {
		line += "  " + control
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

func ledgerGroupColor(group ledger.Group) (lipgloss.Color, bool) {
	switch group {
	case ledger.GroupStaged:
		return lipgloss.Color("2"), true
	case ledger.GroupModified:
		return lipgloss.Color("3"), true
	case ledger.GroupNew:
		return lipgloss.Color("6"), true
	default:
		return lipgloss.Color(""), false
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
	rowStyle := lipgloss.NewStyle()
	if visual.Hovered {
		rowStyle = rowStyle.Background(lipgloss.Color("238"))
	}

	indent := "   "
	if visual.Selected {
		indent = rowStyle.Bold(true).Foreground(lipgloss.Color("2")).Render(" ☑ ")
	} else if visual.Hovered {
		indent = rowStyle.Faint(true).Render(" ☐ ")
	} else {
		indent = rowStyle.Render(indent)
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
		prefix = indent + rowStyle.Render(fmt.Sprintf("%-9s  ", figure))
	} else {
		add := rowStyle.Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("+%-3d", row.Added))
		del := rowStyle.Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("−%-3d", row.Deleted))
		prefix = indent + add + rowStyle.Render(" ") + del + rowStyle.Render("  ")
	}

	nameWidth := width - lipgloss.Width(prefix)
	if nameWidth < 1 {
		nameWidth = 1
	}
	name := runewidth.Truncate(path.Base(row.Path), nameWidth, "…")
	// The filename takes its section title's color (yellow under "modified",
	// green under "staged", cyan under "new") so a section reads as one block.
	nameColor := currentTheme.Text
	if groupColor, ok := ledgerGroupColor(row.ID.Group); ok {
		nameColor = groupColor
	}
	line := prefix + rowStyle.Foreground(nameColor).Render(name)
	if visual.Hovered {
		if padding := width - lipgloss.Width(line); padding > 0 {
			line += rowStyle.Render(strings.Repeat(" ", padding))
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// renderLedgerFooter draws the account pill (and any status/error) on the left
// and the changed-file stamp right-aligned, with a 1-col right margin.
func renderLedgerFooter(state *ledger.State, width int, actionError error, pill *ledger.SessionPill, pillHover bool, tool string) string {
	metadata := state.Snapshot.Metadata
	stampWidth := visibleRuneWidth(ledgerStampText(metadata))
	leftBudget := width
	if stampWidth > 0 {
		// Reserve the stamp plus a leading gap and a 1-col right margin.
		leftBudget = width - stampWidth - 2
		if leftBudget < 0 {
			leftBudget = 0
		}
	}
	left := renderLedgerFooterLeft(state, leftBudget, actionError, pill, pillHover, tool)
	if stampWidth == 0 {
		return left
	}
	pad := width - 1 - stampWidth - lipgloss.Width(left)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + renderLedgerStamp(metadata)
}

func renderLedgerFooterLeft(state *ledger.State, width int, actionError error, pill *ledger.SessionPill, pillHover bool, tool string) string {
	if width <= 0 {
		return ""
	}
	// An action error shares the footer with the pill rather than replacing it.
	// actionError is sticky — it survives until some later action succeeds — so
	// taking the row over would hide the pane's identity, and its only switch
	// affordance, for an unbounded time.
	if actionError != nil {
		message := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render
		if pill == nil {
			return message(ledgerFitPlain(" "+actionError.Error(), width))
		}
		pillRendered := renderLedgerPill(pill, pillHover)
		remaining := width - lipgloss.Width(pillRendered) - 3
		if remaining < 1 {
			return pillRendered
		}
		return pillRendered + message(" · "+ledgerFitPlain(actionError.Error(), remaining))
	}
	// The upstream divergence (↑N/↓M) followed the branch name into the Claude
	// statusline, where it sits right of the branch it describes — so a Claude
	// pane's footer would only duplicate it. Only Claude Code renders that
	// statusline, though: a Codex pane has nowhere else to read the counts, so
	// there they stay on the footer.
	parts := []string{}
	if ledgerToolNeedsDivergence(tool) {
		metadata := state.Snapshot.Metadata
		if metadata.Ahead > 0 {
			parts = append(parts, fmt.Sprintf("↑%d", metadata.Ahead))
		}
		if metadata.Behind > 0 {
			parts = append(parts, fmt.Sprintf("↓%d", metadata.Behind))
		}
	}
	if state.MaxScroll() > 0 {
		first := state.Scroll + 1
		last := state.Scroll + state.ViewportHeight()
		if last > len(state.Snapshot.Rows) {
			last = len(state.Snapshot.Rows)
		}
		parts = append(parts, fmt.Sprintf("%d-%d/%d", first, last, len(state.Snapshot.Rows)))
	}
	status := strings.Join(parts, " · ")
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	if pill == nil {
		if status == "" {
			return ""
		}
		return dim.Render(ledgerFitPlain(" "+status, width))
	}
	pillText := ledgerPillText(pill)
	pillStyle := ledgerPillStyle(pill, pillHover)
	pillRendered := pillStyle.Render(pillText)
	if status == "" {
		return pillStyle.Render(ledgerFitPlain(pillText, width))
	}
	remaining := width - lipgloss.Width(pillRendered) - 3
	if remaining < 0 {
		return pillStyle.Render(ledgerFitPlain(pillText, width))
	}
	return pillRendered + dim.Render(" · "+ledgerFitPlain(status, remaining))
}

// sessionTool is the pane's CURRENT agent. The relaunch context wins because a
// mid-session tool switch rewrites it under a live pane; the launch flag covers
// every tick before that context has loaded (it can be absent, half-written, or
// simply late — see the reload comment in Update).
func (m *LedgerModel) sessionTool() string {
	if m.session.Tool != "" {
		return m.session.Tool
	}
	return m.tool
}

// ledgerToolNeedsDivergence reports whether this pane's agent leaves the
// upstream counts homeless. The empty tool is Claude (the relaunch context's own
// default), so a missing or not-yet-loaded session context never double-prints
// what the statusline already shows.
func ledgerToolNeedsDivergence(tool string) bool {
	return tool == "codex"
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
