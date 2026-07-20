package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/ledger"
)

// The branch name lives on the header's LEFT side (it moved out of the
// footer), with the changed-file stamp still right-aligned on the same line.
func TestLedgerHeaderShowsBranchOnLeft(t *testing.T) {
	raw := renderLedgerHeader(ledger.Metadata{
		Branch: "feature/perf", TotalFiles: 8, Added: 654, Deleted: 25,
	}, 80)[0]

	plain := stripANSI(raw)
	if !strings.HasPrefix(plain, " feature/perf") {
		t.Fatalf("header does not lead with the branch name: %q", plain)
	}
	if !strings.Contains(plain, "8 files  +654 −25") {
		t.Fatalf("header lost the changed-file stamp: %q", plain)
	}
}

// An empty branch (detached HEAD) renders as "detached", exactly as the
// footer used to label it.
func TestLedgerHeaderShowsDetachedWhenBranchEmpty(t *testing.T) {
	raw := renderLedgerHeader(ledger.Metadata{TotalFiles: 1, Added: 1}, 80)[0]

	if plain := stripANSI(raw); !strings.HasPrefix(plain, " detached") {
		t.Fatalf("empty branch does not render as detached: %q", plain)
	}
}

// A branch wider than the pane truncates rather than displacing the stamp or
// overflowing the row.
func TestLedgerHeaderTruncatesLongBranchBeforeStamp(t *testing.T) {
	raw := renderLedgerHeader(ledger.Metadata{
		Branch: strings.Repeat("b", 100), TotalFiles: 2, Added: 5, Deleted: 1,
	}, 40)[0]

	plain := stripANSI(raw)
	if !strings.Contains(plain, "bbb") {
		t.Fatalf("truncated branch not on the header at all: %q", plain)
	}
	if !strings.Contains(plain, "2 files  +5 −1") {
		t.Fatalf("stamp displaced by long branch: %q", plain)
	}
	if w := visibleRuneWidth(plain); w > 40 {
		t.Fatalf("header row overflows the pane: %d cols > 40 (%q)", w, plain)
	}
}

// The footer keeps the commits to push (↑N) and pull (↓M) but no longer names
// the branch — that moved to the header.
func TestLedgerFooterKeepsAheadBehindWithoutBranchName(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)

	lines := strings.Split(m.View(), "\n")
	footer := stripANSI(lines[len(lines)-1])
	if strings.Contains(footer, "feature/native-ledger") {
		t.Fatalf("footer still names the branch: %q", footer)
	}
	for _, want := range []string{"↑3", "↓2"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer lost %q: %q", want, footer)
		}
	}
}

// With the branch gone and nothing else to report, the footer is just the
// account pill — no dangling " · " separator left behind.
func TestLedgerFooterPillAloneHasNoDanglingSeparator(t *testing.T) {
	rows := []ledger.Row{{Kind: ledger.RowGroup, Label: "modified", Count: 1}}
	snapshot := ledger.NewSnapshot(1, rows, ledger.Metadata{Branch: "main", TotalFiles: 1})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 78},
	}})

	lines := strings.Split(m.View(), "\n")
	footer := strings.TrimRight(stripANSI(lines[len(lines)-1]), " ")
	if strings.Contains(footer, "·") {
		t.Fatalf("footer keeps a separator with nothing after the pill: %q", footer)
	}
	if !strings.Contains(footer, "Personal") {
		t.Fatalf("footer lost the account pill: %q", footer)
	}
}
