package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/jackuait/wisp-deck/internal/ledger"
)

// The top-of-pane header and its separator rule are gone: the first rendered
// line is the first snapshot row, never a stamp or a horizontal rule.
func TestLedgerViewHasNoTopHeaderOrSeparator(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)

	lines := strings.Split(m.View(), "\n")
	first := stripANSI(lines[0])
	if strings.Contains(first, "─") {
		t.Fatalf("top of pane still draws a separator rule: %q", first)
	}
	if strings.Contains(first, "files") || strings.Contains(first, "file ") {
		t.Fatalf("top of pane still draws the changed-file stamp: %q", first)
	}
	if !strings.Contains(first, "modified") {
		t.Fatalf("first line is not the first snapshot group row: %q", first)
	}
}

// The changed-file stamp now lives in the footer: right-aligned, with the
// account pill still on the left.
func TestLedgerFooterShowsRightAlignedFileStamp(t *testing.T) {
	rows := []ledger.Row{{Kind: ledger.RowGroup, Group: ledger.GroupNew, Label: "new", Count: 8}}
	snapshot := ledger.NewSnapshot(1, rows, ledger.Metadata{TotalFiles: 8, Added: 356, Deleted: 0})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude", Pill: &ledger.SessionPill{Label: "Personal", Color: 78},
	}})

	lines := strings.Split(m.View(), "\n")
	footer := stripANSI(lines[len(lines)-1])
	if !strings.Contains(footer, "8 files  +356 −0") {
		t.Fatalf("footer lost the changed-file stamp: %q", footer)
	}
	if !strings.Contains(footer, "Personal") {
		t.Fatalf("footer lost the account pill: %q", footer)
	}
	if !strings.HasSuffix(strings.TrimRight(footer, " "), "8 files  +356 −0") {
		t.Fatalf("stamp is not right-aligned: %q", footer)
	}
	if strings.Index(footer, "Personal") > strings.Index(footer, "8 files") {
		t.Fatalf("pill should sit left of the stamp: %q", footer)
	}
	if w := visibleRuneWidth(footer); w > 80 {
		t.Fatalf("footer overflows the pane: %d cols > 80 (%q)", w, footer)
	}
}

// The footer stamp keeps the diff coloring: green +added, red −deleted.
func TestLedgerFooterStampColorsAddedAndDeleted(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	rows := []ledger.Row{{Kind: ledger.RowGroup, Group: ledger.GroupModified, Label: "modified", Count: 2}}
	snapshot := ledger.NewSnapshot(1, rows, ledger.Metadata{TotalFiles: 2, Added: 654, Deleted: 25})
	m := NewLedgerModel(fakeLedgerSource{}, snapshot, LedgerOptions{})
	sizeLedger(m, 80, 14)

	footer := strings.Split(m.View(), "\n")
	raw := footer[len(footer)-1]
	if !ledgerSGRActiveAt(raw, "+654", "32m") {
		t.Fatalf("added count is not green: %q", raw)
	}
	if !ledgerSGRActiveAt(raw, "−25", "31m") {
		t.Fatalf("deleted count is not red: %q", raw)
	}
}
