package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/ledger"
)

// The branch name lives in the Claude statusline; the footer stamp names
// neither the branch nor a detached HEAD, only the changed-file summary.
func TestLedgerFooterStampOmitsBranch(t *testing.T) {
	rows := []ledger.Row{{Kind: ledger.RowGroup, Group: ledger.GroupModified, Label: "modified", Count: 8}}
	snapshot := ledger.NewSnapshot(1, rows, ledger.Metadata{
		Branch: "feature/perf", TotalFiles: 8, Added: 654, Deleted: 25,
	})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)

	plain := stripANSI(m.View())
	if strings.Contains(plain, "feature/perf") {
		t.Fatalf("footer still names the branch: %q", plain)
	}
	if strings.Contains(plain, "detached") {
		t.Fatalf("footer labels a detached HEAD it no longer tracks: %q", plain)
	}
	if !strings.Contains(plain, "8 files  +654 −25") {
		t.Fatalf("footer lost the changed-file stamp: %q", plain)
	}
}

// Even at a narrow width the footer row never overflows the pane.
func TestLedgerFooterStampFitsNarrowPane(t *testing.T) {
	rows := []ledger.Row{{Kind: ledger.RowGroup, Group: ledger.GroupModified, Label: "modified", Count: 2}}
	snapshot := ledger.NewSnapshot(1, rows, ledger.Metadata{TotalFiles: 2, Added: 5, Deleted: 1})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 40, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 78},
	}})

	lines := strings.Split(m.View(), "\n")
	footer := stripANSI(lines[len(lines)-1])
	if !strings.Contains(footer, "2 files  +5 −1") {
		t.Fatalf("footer lost the stamp: %q", footer)
	}
	if w := visibleRuneWidth(footer); w > 40 {
		t.Fatalf("footer row overflows the pane: %d cols > 40 (%q)", w, footer)
	}
}

// The commits to push (↑N) and pull (↓M) followed the branch name into the
// Claude statusline — the footer names neither.
func TestLedgerFooterOmitsAheadBehind(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)

	plain := stripANSI(m.View())
	if strings.Contains(plain, "feature/native-ledger") {
		t.Fatalf("footer still names the branch: %q", plain)
	}
	for _, gone := range []string{"↑3", "↓2"} {
		if strings.Contains(plain, gone) {
			t.Fatalf("footer still carries %q — it belongs to the statusline now: %q", gone, plain)
		}
	}
}

// With the divergence gone, an account pill and a scroll position still share
// the footer with a single separator between them — and nothing dangles when
// the list fits.
func TestLedgerFooterPillAndScrollKeepOneSeparator(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 78},
	}})

	lines := strings.Split(m.View(), "\n")
	footer := strings.TrimRight(stripANSI(lines[len(lines)-1]), " ")
	if !strings.Contains(footer, "Personal") {
		t.Fatalf("footer lost the account pill: %q", footer)
	}
	if n := strings.Count(footer, "·"); n > 1 {
		t.Fatalf("footer keeps a stale separator from the dropped divergence (%d): %q", n, footer)
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
