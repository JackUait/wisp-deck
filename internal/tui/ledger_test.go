package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

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
		Plan: "Max", TotalFiles: n, Added: n * 12, Deleted: n * 3,
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
		Branch: "feature/perf", Ahead: 2, Behind: 1, Plan: "Max",
		TotalFiles: 2, Added: 12, Deleted: 3,
	})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.state.ToggleSelected("src/selected.go")
	m.state.Hovered = rows[2].ID

	raw := m.View()
	plain := stripANSI(raw)

	for _, want := range []string{
		"Max", "2 files", "+12", "−3", "modified", "(2)",
		"☑", "selected.go", "☐", "+1.5KB", "image.png",
		"feature/perf", "↑2", "↓1",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(raw, "48;5;238") {
		t.Errorf("hover row lacks full-row background style: %q", raw)
	}
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
		{name: "empty", options: LedgerOptions{}, want: "no changes"},
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
