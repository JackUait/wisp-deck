package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/ledger"
	"github.com/muesli/termenv"
)

type fakeLedgerSource struct {
	snapshot ledger.Snapshot
	err      error
}

func (s fakeLedgerSource) Load(context.Context, string, uint64) (ledger.Snapshot, error) {
	return s.snapshot, s.err
}

func ledgerTestSnapshot(n int) ledger.Snapshot {
	rows := []ledger.Row{{Kind: ledger.RowGroup, Label: "modified", Count: n}}
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("src/file-%06d.go", i)
		rows = append(rows, ledger.Row{
			Kind: ledger.RowFile,
			ID:   ledger.RowID{Group: ledger.GroupModified, Path: path},
			Path: path, Added: 12, Deleted: 3,
		})
	}
	rows = append(rows, ledger.Row{Kind: ledger.RowSpacer})
	return ledger.NewSnapshot(1, rows, ledger.Metadata{
		Branch: "feature/native-ledger", Ahead: 3, Behind: 2,
		TotalFiles: n, Added: n * 12, Deleted: n * 3,
	})
}

func sizeLedger(m *LedgerModel, width, height int) {
	m.width = width
	m.height = height
	m.state.Resize(width, height, ledgerHeaderHeight, ledgerFooterHeight)
}

func TestLedgerViewRendersOnlyViewportRows(t *testing.T) {
	snapshot := ledgerTestSnapshot(100_000)
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 100, 30)
	m.state.ScrollTo(90_000)
	seen := 0
	m.renderRow = func(row ledger.Row, _ int, _ ledger.RowVisualState) string {
		seen++
		return row.Path
	}

	_ = m.View()

	if seen > m.state.ViewportHeight() {
		t.Fatalf("rendered %d rows for viewport %d", seen, m.state.ViewportHeight())
	}
	if seen != len(m.state.VisibleRows()) {
		t.Fatalf("rendered %d rows, visible slice has %d", seen, len(m.state.VisibleRows()))
	}
}

func TestLedgerViewScaleInvariant(t *testing.T) {
	counts := make([]int, 0, 2)
	for _, total := range []int{1_000, 100_000} {
		m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(total), LedgerOptions{})
		sizeLedger(m, 100, 40)
		m.state.ScrollTo(total / 2)
		seen := 0
		m.renderRow = func(row ledger.Row, _ int, _ ledger.RowVisualState) string {
			seen++
			return row.Path
		}
		_ = m.View()
		counts = append(counts, seen)
	}
	if counts[0] != counts[1] {
		t.Fatalf("rendered row counts = %v; total-list size affected View work", counts)
	}
}

func TestLedgerViewRendersFileStatesAndMetadata(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	rows := []ledger.Row{
		{Kind: ledger.RowGroup, Label: "modified", Count: 2},
		{Kind: ledger.RowFile, ID: ledger.RowID{Group: ledger.GroupModified, Path: "src/selected.go"}, Path: "src/selected.go", Added: 12, Deleted: 3},
		{Kind: ledger.RowFile, ID: ledger.RowID{Group: ledger.GroupModified, Path: "assets/image.png"}, Path: "assets/image.png", Binary: true, OldBytes: 1024, NewBytes: 2560},
		{Kind: ledger.RowSpacer},
	}
	snapshot := ledger.NewSnapshot(2, rows, ledger.Metadata{
		Branch: "feature/perf", Ahead: 2, Behind: 1,
		TotalFiles: 2, Added: 12, Deleted: 3,
	})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.ToggleSelected("src/selected.go")
	m.state.Hovered = rows[2].ID

	raw := m.View()
	plain := stripANSI(raw)

	if strings.Contains(plain, "Max") {
		t.Errorf("view still renders the subscription plan in the header:\n%s", plain)
	}
	for _, want := range []string{
		"2 files", "+12", "−3", "modified", "(2)",
		"☑", "selected.go", "☐", "+1.5KB", "image.png",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q:\n%s", want, plain)
		}
	}
	// The branch name and its upstream divergence both moved to the Claude
	// statusline; the ledger never shows either.
	for _, gone := range []string{"feature/perf", "↑2", "↓1"} {
		if strings.Contains(plain, gone) {
			t.Errorf("view still renders %q:\n%s", gone, plain)
		}
	}
	if !strings.Contains(raw, "48;5;238") {
		t.Errorf("hover row lacks full-row background style: %q", raw)
	}
}

// The changed-file line totals carry the diff colors — see the footer stamp
// tests in ledger_footer_stamp_test.go, which now own the stamp.

func TestLedgerGroupRowsRenderStatusColors(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	tests := []struct {
		name  string
		group ledger.Group
		color string
	}{
		{name: "staged", group: ledger.GroupStaged, color: "32m"},
		{name: "modified", group: ledger.GroupModified, color: "33m"},
		{name: "new", group: ledger.GroupNew, color: "36m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := ledger.Row{Kind: ledger.RowGroup, Group: tt.group, Label: tt.name, Count: 3}
			raw := renderLedgerRow(row, 80, ledger.RowVisualState{})

			for _, target := range []string{"●", tt.name} {
				if !ledgerSGRActiveAt(raw, target, tt.color) {
					t.Fatalf("%q does not carry status color %q: %q", target, tt.color, raw)
				}
			}
		})
	}
}

func TestLedgerGroupStatusColorSurvivesDiscardControl(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	path := "tooltip.ts"
	rows := []ledger.Row{
		{Kind: ledger.RowGroup, Group: ledger.GroupModified, Label: "modified", Count: 1},
		{Kind: ledger.RowFile, ID: ledger.RowID{Group: ledger.GroupModified, Path: path}, Path: path, Added: 27, Deleted: 32},
	}
	m := NewLedgerModel(nil, ledger.NewSnapshot(1, rows, ledger.Metadata{TotalFiles: 1}), LedgerOptions{})
	sizeLedger(m, 80, 10)
	m.state.ToggleSelected(path)

	raw := strings.Split(m.View(), "\n")[ledgerHeaderHeight]
	if !strings.Contains(stripANSI(raw), "[ discard 1 ]") {
		t.Fatalf("discard control missing from group row: %q", raw)
	}
	if !ledgerSGRActiveAt(raw, "modified", "33m") {
		t.Fatalf("discard control stripped the group status color: %q", raw)
	}
}

func TestLedgerHoverBackgroundCoversFilename(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	row := ledger.Row{
		Kind: ledger.RowFile,
		ID:   ledger.RowID{Group: ledger.GroupModified, Path: "tooltip.ts"},
		Path: "tooltip.ts", Added: 27, Deleted: 32,
	}
	raw := renderLedgerFileRow(row, 80, ledger.RowVisualState{Hovered: true})

	if !ledgerSGRActiveAt(raw, "tooltip.ts", "48;5;238") {
		t.Fatalf("filename is outside the hover background: %q", raw)
	}
}

func TestLedgerHoveredFileRowNeverExceedsNarrowPane(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	row := ledger.Row{
		Kind: ledger.RowFile,
		ID:   ledger.RowID{Group: ledger.GroupModified, Path: "tooltip.ts"},
		Path: "tooltip.ts", Added: 27, Deleted: 32,
	}
	for width := 1; width <= 14; width++ {
		raw := renderLedgerFileRow(row, width, ledger.RowVisualState{Hovered: true})
		if got := lipgloss.Width(raw); got > width {
			t.Errorf("width %d rendered %d cells: %q", width, got, raw)
		}
	}
}

// ledgerSGRActiveAt reports whether an SGR fragment is asserted after the last
// reset before target. This catches style holes that a simple escape-presence
// assertion misses: an earlier span may have enabled a background and then
// reset it before the filename begins.
func ledgerSGRActiveAt(value, target, fragment string) bool {
	index := strings.Index(value, target)
	if index < 0 {
		return false
	}
	prefix := value[:index]
	return strings.LastIndex(prefix, fragment) > strings.LastIndex(prefix, "\x1b[0m")
}

func TestLedgerViewTruncatesLongBasenameToPane(t *testing.T) {
	path := "src/this-is-an-extremely-long-file-name-that-cannot-fit.go"
	rows := []ledger.Row{
		{Kind: ledger.RowGroup, Label: "new", Count: 1},
		{Kind: ledger.RowFile, ID: ledger.RowID{Group: ledger.GroupNew, Path: path}, Path: path, Added: 1},
	}
	m := NewLedgerModel(fakeLedgerSource{}, ledger.NewSnapshot(1, rows, ledger.Metadata{TotalFiles: 1, Added: 1}), LedgerOptions{})
	sizeLedger(m, 32, 10)

	plain := stripANSI(m.View())

	if !strings.Contains(plain, "…") {
		t.Fatalf("long filename was not truncated:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if width := visibleRuneWidth(line); width > 32 {
			t.Errorf("line width = %d, want <= 32: %q", width, line)
		}
	}
}

func TestLedgerViewShowsLoadingErrorAndEmptyStates(t *testing.T) {
	tests := []struct {
		name    string
		options LedgerOptions
		want    string
	}{
		{name: "loading", options: LedgerOptions{Loading: true}, want: "loading changes"},
		{name: "error", options: LedgerOptions{RefreshError: errors.New("git unavailable")}, want: "git unavailable"},
		{name: "empty", options: LedgerOptions{}, want: "working tree clean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewLedgerModel(fakeLedgerSource{}, ledger.NewSnapshot(0, nil, ledger.Metadata{}), tt.options)
			sizeLedger(m, 60, 10)
			if got := stripANSI(m.View()); !strings.Contains(got, tt.want) {
				t.Fatalf("view missing %q:\n%s", tt.want, got)
			}
		})
	}
}

func TestLedgerMouseMotionMapsVisibleRowAndSameRowIsNoOp(t *testing.T) {
	snapshot := ledgerTestSnapshot(100)
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.ScrollTo(50)
	want := snapshot.Rows[51].ID // Y=1 is the second viewport row (zero-based, no top header).

	updated, cmd := m.Update(tea.MouseMsg{X: 10, Y: 1, Action: tea.MouseActionMotion})

	if updated != m || cmd != nil {
		t.Fatalf("motion returned model=%T cmd=%v; want same model and nil command", updated, cmd)
	}
	if m.state.Hovered != want {
		t.Fatalf("hover = %v, want %v", m.state.Hovered, want)
	}
	updated, cmd = m.Update(tea.MouseMsg{X: 11, Y: 1, Action: tea.MouseActionMotion})
	if updated != m || cmd != nil || m.state.Hovered != want {
		t.Fatalf("same-row motion changed state: model=%T cmd=%v hover=%v", updated, cmd, m.state.Hovered)
	}
}

func TestLedgerMouseOutsidePaneClearsHover(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(10), LedgerOptions{})
	sizeLedger(m, 40, 12)
	m.Update(tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionMotion})
	if m.state.Hovered == (ledger.RowID{}) {
		t.Fatal("precondition: file row should be hovered")
	}

	m.Update(tea.MouseMsg{X: 41, Y: 3, Action: tea.MouseActionMotion})

	if m.state.Hovered != (ledger.RowID{}) {
		t.Fatalf("hover = %v, want clear", m.state.Hovered)
	}
}

func TestLedgerMouseAtRightmostPaneCellStillHovers(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(10), LedgerOptions{})
	sizeLedger(m, 40, 12)
	m.Update(tea.MouseMsg{X: 39, Y: 3, Action: tea.MouseActionMotion})

	if m.state.Hovered == (ledger.RowID{}) {
		t.Fatal("rightmost in-pane cell was treated as outside the ledger")
	}
}

func TestLedgerMouseHoverDoesNotScheduleIdleExpiry(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(10), LedgerOptions{})
	sizeLedger(m, 40, 12)

	_, timeout := m.Update(tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionMotion})
	if m.state.Hovered == (ledger.RowID{}) {
		t.Fatal("mouse motion did not establish hover")
	}
	if timeout != nil {
		t.Fatal("stationary hover scheduled an idle expiry")
	}
}

func TestLedgerScrollMouseWheelKeepsHoverUnderPointer(t *testing.T) {
	snapshot := ledgerTestSnapshot(100)
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.ScrollTo(10)

	m.Update(tea.MouseMsg{X: 10, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})

	if m.state.Scroll != 13 {
		t.Fatalf("scroll = %d, want 13", m.state.Scroll)
	}
	want := snapshot.Rows[18].ID // Y=5, offset=Y (no top header): Rows[scroll+5].
	if m.state.Hovered != want {
		t.Fatalf("hover after wheel = %v, want %v", m.state.Hovered, want)
	}
}

func TestLedgerScrollKeyboardBindings(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyMsg
		start int
		want  int
	}{
		{name: "j", key: ledgerRuneKey('j'), start: 10, want: 11},
		{name: "down", key: tea.KeyMsg{Type: tea.KeyDown}, start: 10, want: 11},
		{name: "k", key: ledgerRuneKey('k'), start: 10, want: 9},
		{name: "up", key: tea.KeyMsg{Type: tea.KeyUp}, start: 10, want: 9},
		{name: "space", key: ledgerRuneKey(' '), start: 10, want: 23},
		{name: "page down", key: tea.KeyMsg{Type: tea.KeyPgDown}, start: 10, want: 23},
		{name: "b", key: ledgerRuneKey('b'), start: 20, want: 7},
		{name: "page up", key: tea.KeyMsg{Type: tea.KeyPgUp}, start: 20, want: 7},
		{name: "g", key: ledgerRuneKey('g'), start: 20, want: 0},
		{name: "home", key: tea.KeyMsg{Type: tea.KeyHome}, start: 20, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(100), LedgerOptions{})
			sizeLedger(m, 80, 14)
			m.state.ScrollTo(tt.start)

			m.Update(tt.key)

			if m.state.Scroll != tt.want {
				t.Fatalf("scroll = %d, want %d", m.state.Scroll, tt.want)
			}
		})
	}

	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(100), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerRuneKey('G'))
	if m.state.Scroll != m.state.MaxScroll() {
		t.Fatalf("G scroll = %d, want %d", m.state.Scroll, m.state.MaxScroll())
	}
}

func TestLedgerUpdateResizeDoesNotLoadGit(t *testing.T) {
	source := &recordingLedgerSource{snapshot: ledgerTestSnapshot(10)}
	m := NewLedgerModel(source, source.snapshot, LedgerOptions{})

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 42})

	if cmd != nil {
		t.Fatalf("resize command = %v, want nil", cmd)
	}
	if m.width != 120 || m.height != 42 || m.state.ViewportHeight() != 41 {
		t.Fatalf("geometry = %dx%d viewport=%d", m.width, m.height, m.state.ViewportHeight())
	}
	if source.CallCount() != 0 {
		t.Fatalf("resize triggered %d Git loads", source.CallCount())
	}
}

type recordingLedgerSource struct {
	mu         sync.Mutex
	snapshot   ledger.Snapshot
	err        error
	calls      int
	generation uint64
}

func (s *recordingLedgerSource) Load(_ context.Context, _ string, generation uint64) (ledger.Snapshot, error) {
	s.mu.Lock()
	s.calls++
	s.generation = generation
	snapshot, err := s.snapshot, s.err
	s.mu.Unlock()
	snapshot.Generation = generation
	return snapshot, err
}

func (s *recordingLedgerSource) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestLedgerRefreshInitLoadsSnapshotAsynchronously(t *testing.T) {
	source := &recordingLedgerSource{snapshot: ledgerTestSnapshot(20)}
	empty := ledger.NewSnapshot(0, nil, ledger.Metadata{})
	m := NewLedgerModel(source, empty, LedgerOptions{ProjectDir: "/repo", RefreshInterval: time.Hour})

	cmd := m.Init()

	if cmd == nil {
		t.Fatal("Init returned no load command")
	}
	if source.CallCount() != 0 {
		t.Fatal("Init blocked on the source instead of returning a command")
	}
	msg := cmd()
	loaded, ok := msg.(ledgerSnapshotMsg)
	if !ok {
		t.Fatalf("load message = %T, want ledgerSnapshotMsg", msg)
	}
	if loaded.generation != 1 || source.CallCount() != 1 {
		t.Fatalf("generation=%d calls=%d, want 1 and 1", loaded.generation, source.CallCount())
	}
}

func TestLedgerRefreshIgnoresStaleGeneration(t *testing.T) {
	current := ledgerTestSnapshot(10)
	current.Generation = 10
	m := NewLedgerModel(fakeLedgerSource{}, current, LedgerOptions{RefreshInterval: time.Hour})
	m.requestedGeneration = 10
	stale := ledgerTestSnapshot(999)
	stale.Generation = 9

	_, cmd := m.Update(ledgerSnapshotMsg{generation: 9, snapshot: stale})

	if cmd != nil {
		t.Fatalf("stale snapshot scheduled command %v", cmd)
	}
	if m.state.Snapshot.Generation != 10 || m.state.Snapshot.Metadata.TotalFiles != 10 {
		t.Fatalf("stale snapshot replaced current: %#v", m.state.Snapshot.Metadata)
	}
}

func TestLedgerRefreshAcceptsLatestAndSchedulesNextTick(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledger.NewSnapshot(0, nil, ledger.Metadata{}), LedgerOptions{RefreshInterval: time.Hour})
	m.requestedGeneration = 4
	next := ledgerTestSnapshot(25)
	next.Generation = 4

	_, cmd := m.Update(ledgerSnapshotMsg{generation: 4, snapshot: next})

	if cmd == nil {
		t.Fatal("accepted snapshot did not schedule the next refresh")
	}
	if m.state.Snapshot.Generation != 4 || m.state.Snapshot.Metadata.TotalFiles != 25 || m.loading {
		t.Fatalf("accepted state = generation %d files %d loading=%v", m.state.Snapshot.Generation, m.state.Snapshot.Metadata.TotalFiles, m.loading)
	}
}

func TestLedgerRefreshErrorRetainsSnapshotAndSchedulesRetry(t *testing.T) {
	current := ledgerTestSnapshot(10)
	current.Generation = 10
	m := NewLedgerModel(fakeLedgerSource{}, current, LedgerOptions{RefreshInterval: time.Hour})
	m.requestedGeneration = 11
	wantErr := errors.New("git busy")

	_, cmd := m.Update(ledgerLoadErrMsg{generation: 11, err: wantErr})

	if cmd == nil {
		t.Fatal("load error did not schedule a retry")
	}
	if m.state.Snapshot.Generation != 10 || !errors.Is(m.refreshError, wantErr) || m.loading {
		t.Fatalf("error state = generation %d err=%v loading=%v", m.state.Snapshot.Generation, m.refreshError, m.loading)
	}
}

func TestLedgerRefreshTickStartsNewGeneration(t *testing.T) {
	source := &recordingLedgerSource{snapshot: ledgerTestSnapshot(10)}
	m := NewLedgerModel(source, source.snapshot, LedgerOptions{ProjectDir: "/repo", RefreshInterval: time.Hour})
	m.requestedGeneration = 7

	_, cmd := m.Update(ledgerRefreshTickMsg{})

	if cmd == nil || m.requestedGeneration != 8 || m.loading {
		t.Fatalf("refresh tick: cmd=%v generation=%d loading=%v", cmd, m.requestedGeneration, m.loading)
	}
	msg := cmd()
	if loaded, ok := msg.(ledgerSnapshotMsg); !ok || loaded.generation != 8 {
		t.Fatalf("refresh result = %#v", msg)
	}
}

func TestLedgerRefreshTickKeepsLoadedEmptyStateVisible(t *testing.T) {
	empty := ledger.NewSnapshot(7, nil, ledger.Metadata{})
	source := &recordingLedgerSource{snapshot: empty}
	m := NewLedgerModel(source, empty, LedgerOptions{ProjectDir: "/repo", RefreshInterval: time.Hour})
	sizeLedger(m, 60, 10)

	_, cmd := m.Update(ledgerRefreshTickMsg{})

	if cmd == nil {
		t.Fatal("refresh tick returned no load command")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "working tree clean") {
		t.Fatalf("background refresh hid the accepted empty state:\n%s", view)
	}
	if strings.Contains(view, "loading changes") {
		t.Fatalf("background refresh exposed a transient loading state:\n%s", view)
	}
}

type blockingLedgerSource struct {
	started chan context.Context
}

func (s blockingLedgerSource) Load(ctx context.Context, _ string, _ uint64) (ledger.Snapshot, error) {
	s.started <- ctx
	<-ctx.Done()
	return ledger.Snapshot{}, ctx.Err()
}

func TestLedgerRefreshSupersedesAndCancelsPriorLoad(t *testing.T) {
	source := blockingLedgerSource{started: make(chan context.Context, 2)}
	m := NewLedgerModel(source, ledger.NewSnapshot(0, nil, ledger.Metadata{}), LedgerOptions{RefreshInterval: time.Hour})
	_, firstCmd := m.Update(ledgerRefreshTickMsg{})
	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- firstCmd() }()
	firstContext := <-source.started

	_, secondCmd := m.Update(ledgerRefreshTickMsg{})

	if secondCmd == nil {
		t.Fatal("superseding refresh returned no command")
	}
	select {
	case <-firstContext.Done():
	case <-time.After(time.Second):
		t.Fatal("superseded load context was not cancelled")
	}
	select {
	case msg := <-firstDone:
		failed, ok := msg.(ledgerLoadErrMsg)
		if !ok || !errors.Is(failed.err, context.Canceled) {
			t.Fatalf("superseded load result = %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("superseded load command did not return")
	}
}

func ledgerRuneKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

type fakeLedgerMutator struct {
	mu    sync.Mutex
	calls int
	dir   string
	paths []string
	err   error
}

func (m *fakeLedgerMutator) Discard(_ context.Context, dir string, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.dir = dir
	m.paths = append([]string(nil), paths...)
	return m.err
}

func (m *fakeLedgerMutator) result() (int, string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.dir, append([]string(nil), m.paths...)
}

func TestLedgerSelectionKeyboardAndCheckboxClickToggleHoveredPath(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	m := NewLedgerModel(nil, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.Hovered = snapshot.Rows[1].ID

	m.Update(ledgerRuneKey('x'))
	if !m.state.IsSelected(snapshot.Rows[1].Path) {
		t.Fatal("x did not select the hovered path")
	}
	m.Update(tea.MouseMsg{X: 1, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.state.IsSelected(snapshot.Rows[1].Path) {
		t.Fatal("checkbox click did not deselect the file row")
	}
	m.Update(tea.MouseMsg{X: 4, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.state.IsSelected(snapshot.Rows[1].Path) {
		t.Fatal("click outside the checkbox changed selection")
	}
}

func TestLedgerSelectionDiscardArmsSelectedOrHoveredAndSnapsTop(t *testing.T) {
	snapshot := ledgerTestSnapshot(100)
	m := NewLedgerModel(nil, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.ToggleSelected(snapshot.Rows[10].Path)
	m.state.ToggleSelected(snapshot.Rows[20].Path)
	m.state.ScrollTo(50)

	m.Update(ledgerRuneKey('d'))

	if !m.discardArmed || m.state.Scroll != 0 {
		t.Fatalf("armed=%v scroll=%d, want true and 0", m.discardArmed, m.state.Scroll)
	}
	if got := strings.Join(m.discardPaths, ","); got != snapshot.Rows[10].Path+","+snapshot.Rows[20].Path {
		t.Fatalf("discard paths = %q", got)
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "Discard 2 files? [ yes ] [ no ]") {
		t.Fatalf("armed confirmation missing:\n%s", out)
	}

	m.Update(ledgerRuneKey('n'))
	if m.discardArmed || len(m.state.Selected) != 2 {
		t.Fatalf("n cancel armed=%v selected=%v", m.discardArmed, m.state.Selected)
	}

	m.state.Selected = make(map[string]struct{})
	m.state.Hovered = snapshot.Rows[2].ID
	m.Update(ledgerRuneKey('d'))
	if !m.discardArmed || len(m.discardPaths) != 1 || m.discardPaths[0] != snapshot.Rows[2].Path {
		t.Fatalf("hover fallback armed=%v paths=%v", m.discardArmed, m.discardPaths)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.discardArmed {
		t.Fatal("Esc did not cancel discard confirmation")
	}
}

func TestLedgerSelectionDiscardClickSpansMatchRenderedControls(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	m := NewLedgerModel(nil, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.ToggleSelected(snapshot.Rows[1].Path)
	plain := stripANSI(m.View())
	x, y, ok := cellOf(plain, "discard 1")
	if !ok {
		t.Fatalf("discard button missing:\n%s", plain)
	}

	m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !m.discardArmed {
		t.Fatal("clicking rendered discard button did not arm")
	}
	plain = stripANSI(m.View())
	noX, noY, ok := cellOf(plain, "no")
	if !ok {
		t.Fatalf("no button missing:\n%s", plain)
	}
	m.Update(tea.MouseMsg{X: noX, Y: noY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.discardArmed {
		t.Fatal("clicking rendered no button did not cancel")
	}
}

func TestLedgerSelectionDiscardRunsAsyncAndBlocksDuplicateConfirmation(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	mutator := &fakeLedgerMutator{}
	m := NewLedgerModel(nil, snapshot, LedgerOptions{ProjectDir: "/repo", Mutator: mutator})
	m.state.ToggleSelected(snapshot.Rows[2].Path)
	m.Update(ledgerRuneKey('d'))

	_, cmd := m.Update(ledgerRuneKey('y'))
	if cmd == nil || !m.discarding {
		t.Fatalf("confirm cmd=%v discarding=%v", cmd, m.discarding)
	}
	_, duplicate := m.Update(ledgerRuneKey('y'))
	if duplicate != nil {
		t.Fatal("duplicate confirmation started a second command")
	}
	if calls, _, _ := mutator.result(); calls != 0 {
		t.Fatal("discard ran synchronously on the Tea update loop")
	}
	msg := cmd()
	if calls, dir, paths := mutator.result(); calls != 1 || dir != "/repo" || strings.Join(paths, ",") != snapshot.Rows[2].Path {
		t.Fatalf("mutator calls=%d dir=%q paths=%v", calls, dir, paths)
	}
	m.Update(msg)
	if m.discarding || m.discardArmed || len(m.state.Selected) != 0 || m.actionError != nil {
		t.Fatalf("completed discard state: active=%v armed=%v selected=%v err=%v", m.discarding, m.discardArmed, m.state.Selected, m.actionError)
	}
}

func TestLedgerSelectionDiscardFailureRetainsSelectionAndShowsPath(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	wantErr := errors.New("discard \"src/file-000001.go\": permission denied")
	mutator := &fakeLedgerMutator{err: wantErr}
	m := NewLedgerModel(nil, snapshot, LedgerOptions{Mutator: mutator})
	m.state.ToggleSelected(snapshot.Rows[2].Path)
	m.Update(ledgerRuneKey('d'))
	_, cmd := m.Update(ledgerRuneKey('y'))

	m.Update(cmd())

	if m.discarding || !m.discardArmed || !m.state.IsSelected(snapshot.Rows[2].Path) || !errors.Is(m.actionError, wantErr) {
		t.Fatalf("failure state: active=%v armed=%v selected=%v err=%v", m.discarding, m.discardArmed, m.state.Selected, m.actionError)
	}
	if out := stripANSI(m.View()); !strings.Contains(out, snapshot.Rows[2].Path) {
		t.Fatalf("failing path not rendered:\n%s", out)
	}
}

func TestLedgerSelectionStaleHoverDoesNotArmOrMutate(t *testing.T) {
	mutator := &fakeLedgerMutator{}
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{Mutator: mutator})
	m.state.Hovered = ledger.RowID{Group: ledger.GroupModified, Path: "missing.txt"}

	m.Update(ledgerRuneKey('x'))
	m.Update(ledgerRuneKey('d'))
	_, cmd := m.Update(ledgerRuneKey('y'))

	if len(m.state.Selected) != 0 || m.discardArmed || cmd != nil {
		t.Fatalf("stale hover selected=%v armed=%v cmd=%v", m.state.Selected, m.discardArmed, cmd)
	}
	if calls, _, _ := mutator.result(); calls != 0 {
		t.Fatalf("stale hover caused %d mutations", calls)
	}
}

type fakeLedgerPopup struct {
	mu      sync.Mutex
	calls   int
	request ledger.OpenRequest
	started chan struct{}
	release chan struct{}
	result  ledger.OpenResult
	err     error
}

func (p *fakeLedgerPopup) Open(ctx context.Context, request ledger.OpenRequest) (ledger.OpenResult, error) {
	p.mu.Lock()
	p.calls++
	p.request = request
	p.mu.Unlock()
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return ledger.OpenResult{}, ctx.Err()
		}
	}
	return p.result, p.err
}

func (p *fakeLedgerPopup) recorded() (int, ledger.OpenRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.request
}

type fakeBackdropCache struct {
	mu           sync.Mutex
	path         string
	ready        bool
	refreshCalls int
	refreshErr   error
	closed       bool
}

func (c *fakeBackdropCache) Latest() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path, c.ready
}

func (c *fakeBackdropCache) Refresh(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshCalls++
	return c.refreshErr
}

func (c *fakeBackdropCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeBackdropCache) refreshCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshCalls
}

func TestLedgerOpenClickStartsPopupOffInputLoopOnCacheMiss(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	popup := &fakeLedgerPopup{started: make(chan struct{}, 1), release: make(chan struct{})}
	cache := &fakeBackdropCache{}
	m := NewLedgerModel(nil, snapshot, LedgerOptions{
		ProjectDir: "/repo", Popup: popup, BackdropCache: cache, Tool: "opencode",
	})
	sizeLedger(m, 80, 14)

	_, cmd := m.Update(tea.MouseMsg{X: 12, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if cmd == nil || !m.opening {
		t.Fatalf("click cmd=%v opening=%v", cmd, m.opening)
	}
	if calls, _ := popup.recorded(); calls != 0 {
		t.Fatal("popup blocked the Tea update loop")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case <-popup.started:
	case <-time.After(time.Second):
		t.Fatal("popup did not start immediately on a cache miss")
	}
	if cache.refreshCount() != 0 {
		t.Fatal("click waited on or started backdrop refresh")
	}
	_, request := popup.recorded()
	if request.Path != snapshot.Rows[1].Path || request.ProjectDir != "/repo" || request.Tool != "opencode" || request.BackdropFile != "" {
		t.Fatalf("popup request = %#v", request)
	}
	close(popup.release)
	msg := <-done
	m.Update(msg)
	if m.opening {
		t.Fatal("popup completion did not clear opening state")
	}
}

func TestLedgerOpenUsesCachedBackdropAndImageMetadata(t *testing.T) {
	row := ledger.Row{
		Kind: ledger.RowFile, ID: ledger.RowID{Group: ledger.GroupNew, Path: "art/shot.png"},
		Path: "art/shot.png", Binary: true, NewBytes: 2048,
	}
	snapshot := ledger.NewSnapshot(1, []ledger.Row{
		{Kind: ledger.RowGroup, Label: "new", Count: 1}, row,
	}, ledger.Metadata{TotalFiles: 1})
	popup := &fakeLedgerPopup{}
	cache := &fakeBackdropCache{path: "/tmp/cached-backdrop", ready: true}
	// The image gate stats the file: a preview can only show bytes that are on
	// disk, so the row needs a real one behind it.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "art"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "art", "shot.png"), []byte("bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	m := NewLedgerModel(nil, snapshot, LedgerOptions{ProjectDir: repo, Popup: popup, BackdropCache: cache})
	sizeLedger(m, 80, 14)

	_, cmd := m.Update(tea.MouseMsg{X: 12, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatal("image click returned no popup command")
	}
	_ = cmd()
	_, request := popup.recorded()

	if request.BackdropFile != "/tmp/cached-backdrop" || !request.Image || request.Tracked || request.Status != "added" {
		t.Fatalf("image popup request = %#v", request)
	}
}

func TestLedgerOpenCompletionDiscardsDecisionAndRefreshesState(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	popup := &fakeLedgerPopup{result: ledger.OpenResult{Discard: true}}
	mutator := &fakeLedgerMutator{}
	cache := &fakeBackdropCache{}
	source := &recordingLedgerSource{snapshot: snapshot}
	m := NewLedgerModel(source, snapshot, LedgerOptions{
		ProjectDir: "/repo", Popup: popup, BackdropCache: cache, Mutator: mutator,
	})
	sizeLedger(m, 80, 14)

	_, openCmd := m.Update(tea.MouseMsg{X: 12, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	popupDone := openCmd()
	if calls, dir, paths := mutator.result(); calls != 1 || dir != "/repo" || strings.Join(paths, ",") != snapshot.Rows[1].Path {
		t.Fatalf("popup discard calls=%d dir=%q paths=%v", calls, dir, paths)
	}
	_, refreshCmd := m.Update(popupDone)
	if refreshCmd == nil {
		t.Fatal("popup completion scheduled no refresh work")
	}
	batch, ok := refreshCmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("popup refresh message = %T, want tea.BatchMsg", refreshCmd())
	}
	for _, command := range batch {
		if command != nil {
			_ = command()
		}
	}
	if source.CallCount() != 1 || cache.refreshCount() != 1 {
		t.Fatalf("completion refreshes: source=%d backdrop=%d", source.CallCount(), cache.refreshCount())
	}
}

func TestLedgerOpenEnterActivatesHoveredAndStaleRowsDoNothing(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	popup := &fakeLedgerPopup{}
	m := NewLedgerModel(nil, snapshot, LedgerOptions{Popup: popup})
	m.state.Hovered = snapshot.Rows[2].ID

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter returned no popup command for hovered row")
	}
	_ = cmd()
	if calls, request := popup.recorded(); calls != 1 || request.Path != snapshot.Rows[2].Path {
		t.Fatalf("Enter popup calls=%d request=%#v", calls, request)
	}

	m.opening = false
	m.state.Hovered = ledger.RowID{Group: ledger.GroupModified, Path: "missing.go"}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("stale hovered row opened a popup")
	}
}

type fakeLedgerSessionSource struct {
	mu      sync.Mutex
	session ledger.SessionContext
	err     error
	calls   int
}

func (s *fakeLedgerSessionSource) Load(context.Context, string) (ledger.SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.session, s.err
}

func (s *fakeLedgerSessionSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakeAccountSwitcher struct {
	mu      sync.Mutex
	calls   int
	session ledger.SessionContext
	started chan struct{}
	release chan struct{}
	err     error
}

func (s *fakeAccountSwitcher) OpenSwitcher(ctx context.Context, session ledger.SessionContext) error {
	s.mu.Lock()
	s.calls++
	s.session = session
	s.mu.Unlock()
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}

func (s *fakeAccountSwitcher) recorded() (int, ledger.SessionContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.session
}

func TestLedgerAccountPillHidesWhenIneligible(t *testing.T) {
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{Tool: "claude"}})

	if view := stripANSI(m.View()); strings.Contains(view, "󰀄") {
		t.Fatalf("ineligible session rendered account pill:\n%s", view)
	}
}

func TestLedgerAccountPillRendersColorAndExactHoverSpan(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 170},
	}})

	raw := m.View()
	if plain := stripANSI(raw); !strings.Contains(plain, "󰀄 Personal") {
		t.Fatalf("account pill missing:\n%s", plain)
	}
	if !strings.Contains(raw, "38;5;170") {
		t.Fatalf("account color missing: %q", raw)
	}
	width := ledgerAccountPillWidth(m.session.Pill)
	m.Update(tea.MouseMsg{X: width - 1, Y: m.height - 1, Action: tea.MouseActionMotion})
	if !m.accountHover || !strings.Contains(m.View(), "48;5;238") {
		t.Fatal("last pill cell did not render hover highlight")
	}
	m.Update(tea.MouseMsg{X: width, Y: m.height - 1, Action: tea.MouseActionMotion})
	if m.accountHover {
		t.Fatal("cell after pill remained in hover hit span")
	}
}

// Clicking the account pill floats the switcher popup over the agent pane rather
// than painting an in-ledger card: it returns a Tea command that runs
// OpenSwitcher asynchronously with the pane's session, marks the pane switching
// so a second click is a no-op, and on completion clears that flag and reloads
// the session + snapshot (the popup already applied the choice).
func TestLedgerAccountClickOpensSwitcherPopupOverAgentPane(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	source := &recordingLedgerSource{snapshot: snapshot}
	sessionSource := &fakeLedgerSessionSource{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 39},
	}}
	switcher := &fakeAccountSwitcher{started: make(chan struct{}, 1), release: make(chan struct{})}
	m := NewLedgerModel(source, snapshot, LedgerOptions{
		SessionSource: sessionSource, SessionPath: "/tmp/relaunch", AccountSwitcher: switcher,
	})
	sizeLedger(m, 80, 14)
	m.session = ledger.SessionContext{
		RelaunchFile: "/tmp/relaunch", Tool: "claude",
		Pill: &ledger.SessionPill{Label: "Work", Color: 170},
		SwitchOptions: []ledger.SwitchOption{
			{Choice: ledger.SwitchChoice{Kind: ledger.SwitchAccount, Value: "work"}, Label: "Work", Color: 170, Ready: true, Active: true},
			{Choice: ledger.SwitchChoice{Kind: ledger.SwitchSubscription, Value: "chatgpt.json"}, Label: "ChatGPT", Color: 205, Glyph: "✦", Ready: true},
		},
	}

	_, cmd := m.Update(tea.MouseMsg{X: 1, Y: m.height - 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	// The click returns the popup command and marks the pane switching; no card is
	// painted in the ledger, and nothing runs until the command is executed.
	if cmd == nil || !m.switchingAccount {
		t.Fatalf("account click cmd=%v switching=%v", cmd, m.switchingAccount)
	}
	if view := stripANSI(m.View()); strings.Contains(view, "Switch agent") {
		t.Fatalf("switcher must not paint an in-ledger card:\n%s", view)
	}
	if calls, _ := switcher.recorded(); calls != 0 {
		t.Fatal("account switch ran synchronously")
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case <-switcher.started:
	case <-time.After(time.Second):
		t.Fatal("switcher popup command did not start")
	}
	close(switcher.release)
	message := <-done

	_, refresh := m.Update(message)
	if refresh == nil || m.switchingAccount {
		t.Fatalf("switch completion refresh=%v switching=%v", refresh, m.switchingAccount)
	}
	batch, ok := refresh().(tea.BatchMsg)
	if !ok {
		t.Fatalf("switch completion = %T, want Tea batch", refresh())
	}
	for _, command := range batch {
		if command == nil {
			continue
		}
		if loaded := command(); loaded != nil {
			m.Update(loaded)
		}
	}
	if sessionSource.callCount() != 1 || source.CallCount() != 1 {
		t.Fatalf("reload calls: session=%d snapshot=%d", sessionSource.callCount(), source.CallCount())
	}
	if m.session.Pill == nil || m.session.Pill.Label != "Personal" {
		t.Fatalf("reloaded session = %#v", m.session)
	}
	if calls, session := switcher.recorded(); calls != 1 ||
		session.Pill == nil || session.Pill.Label != "Work" ||
		session.RelaunchFile != "/tmp/relaunch" {
		t.Fatalf("switcher calls=%d session=%#v", calls, session)
	}
}

// A second click while a switcher popup is already open is a no-op: the pane is
// already switching, so no additional command is returned.
func TestLedgerAccountClickIgnoredWhileSwitching(t *testing.T) {
	switcher := &fakeAccountSwitcher{}
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{AccountSwitcher: switcher})
	sizeLedger(m, 80, 14)
	m.session = ledger.SessionContext{
		RelaunchFile: "/tmp/relaunch",
		Pill:         &ledger.SessionPill{Label: "Work", Color: 170},
		SwitchOptions: []ledger.SwitchOption{{
			Choice: ledger.SwitchChoice{Kind: ledger.SwitchAccount, Value: "work"},
			Label:  "Work", Color: 170, Ready: true, Active: true,
		}},
	}
	if cmd := m.openAccountSwitch(); cmd == nil {
		t.Fatal("first open should return the popup command")
	}
	if cmd := m.openAccountSwitch(); cmd != nil {
		t.Fatal("second open while switching must be a no-op")
	}
}

// The subscription lives in the account pill, never the changed-file stamp:
// the footer stamp carries only file counts and line totals.
func TestLedgerFooterStampOmitsSubscriptionPlan(t *testing.T) {
	rows := []ledger.Row{{Kind: ledger.RowGroup, Group: ledger.GroupModified, Label: "modified", Count: 8}}
	snapshot := ledger.NewSnapshot(1, rows, ledger.Metadata{TotalFiles: 8, Added: 654, Deleted: 25})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)

	plain := stripANSI(m.View())
	if strings.Contains(plain, "OpenAI / ChatGPT") {
		t.Fatalf("footer stamp still renders the subscription plan: %q", plain)
	}
	if !strings.Contains(plain, "8 files  +654 −25") {
		t.Fatalf("footer lost the changed-file stamp: %q", plain)
	}
}

// A subscription pill carries the subscription spark (✦) instead of the
// account person glyph, so the footer states which identity kind runs.
func TestLedgerFooterPillGlyphFollowsIdentityKind(t *testing.T) {
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Xiaomi MiMo", Color: 205, Glyph: "✦"},
	}})
	plain := stripANSI(m.View())
	if !strings.Contains(plain, "✦ Xiaomi MiMo") {
		t.Fatalf("subscription pill missing spark glyph:\n%s", plain)
	}
	if strings.Contains(plain, "󰀄") {
		t.Fatalf("subscription pill still renders the account glyph:\n%s", plain)
	}

	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 78},
	}})
	if plain := stripANSI(m.View()); !strings.Contains(plain, "󰀄 Personal") {
		t.Fatalf("account pill lost the person glyph:\n%s", plain)
	}
}

// A file row's NAME is tinted with its section title's color — yellow under
// "modified", green under "staged", cyan under "new" — so a section reads as
// one colored block. Binary (byte-delta) rows follow the same rule.
func TestLedgerFileRowFilenameCarriesGroupColor(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	tests := []struct {
		name   string
		group  ledger.Group
		binary bool
		color  string
	}{
		{name: "staged", group: ledger.GroupStaged, color: "32m"},
		{name: "modified", group: ledger.GroupModified, color: "33m"},
		{name: "new", group: ledger.GroupNew, color: "36m"},
		{name: "binary modified", group: ledger.GroupModified, binary: true, color: "33m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := ledger.Row{
				Kind: ledger.RowFile,
				ID:   ledger.RowID{Group: tt.group, Path: "tooltip.ts"},
				Path: "tooltip.ts", Added: 27, Deleted: 32,
				Binary: tt.binary, OldBytes: 100, NewBytes: 300,
			}
			raw := renderLedgerFileRow(row, 80, ledger.RowVisualState{})
			if !ledgerSGRActiveAt(raw, "tooltip.ts", tt.color) {
				t.Fatalf("filename does not carry group color %q: %q", tt.color, raw)
			}
		})
	}
}
