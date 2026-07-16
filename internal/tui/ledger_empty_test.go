package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/ledger"
)

// A clean working tree used to render a bare " no changes" label in the pane's
// top-left corner. It now draws the tool mascot, centered, with a caption.

// mascotRows returns the plain-text lines of a rendered placeholder that carry
// mascot pixels.
func mascotRows(lines []string) []string {
	var art []string
	for _, line := range lines {
		if strings.Contains(stripANSI(line), "█") {
			art = append(art, stripANSI(line))
		}
	}
	return art
}

func TestRenderLedgerEmptyState_draws_a_centered_mascot_and_caption(t *testing.T) {
	const width, height = 60, 27
	lines := renderLedgerEmptyState("claude", ThemeForTool("claude"), width, height)

	if len(lines) > height {
		t.Fatalf("placeholder is %d rows, viewport is %d", len(lines), height)
	}
	art := mascotRows(lines)
	if len(art) < 10 {
		t.Fatalf("expected a multi-row mascot, got %d block rows:\n%s",
			len(art), stripANSI(strings.Join(lines, "\n")))
	}
	if !strings.Contains(stripANSI(strings.Join(lines, "\n")), "working tree clean") {
		t.Errorf("expected a caption under the mascot:\n%s", stripANSI(strings.Join(lines, "\n")))
	}

	// The mascot's widest row (its 24-block body) is centered in the pane.
	minLead := width
	for _, line := range art {
		lead := len([]rune(line)) - len([]rune(strings.TrimLeft(line, " ")))
		if lead < minLead {
			minLead = lead
		}
		if got := visibleRuneWidth(strings.TrimRight(line, " ")); got > width {
			t.Errorf("art row overflows width %d (%d cols): %q", width, got, line)
		}
	}
	if want := (width - 24) / 2; minLead != want {
		t.Errorf("mascot left edge at column %d, want %d (centered)", minLead, want)
	}
}

// The placeholder sits in the middle of the viewport, not jammed against the
// separator rule.
func TestRenderLedgerEmptyState_centers_vertically(t *testing.T) {
	lines := renderLedgerEmptyState("claude", ThemeForTool("claude"), 60, 27)
	blanks := 0
	for _, line := range lines {
		if strings.TrimSpace(stripANSI(line)) != "" {
			break
		}
		blanks++
	}
	if blanks < 3 {
		t.Errorf("expected the art pushed toward the middle, got %d leading blank rows", blanks)
	}
}

// A pane too narrow or too short for the 28x15 art degrades to the caption
// alone rather than a wrapped, mangled mascot.
func TestRenderLedgerEmptyState_falls_back_to_the_caption_when_it_cannot_fit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{"narrow", 20, 27},
		{"short", 60, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderLedgerEmptyState("claude", ThemeForTool("claude"), tc.width, tc.height)
			if art := mascotRows(lines); len(art) != 0 {
				t.Errorf("expected no mascot in a %s pane:\n%s", tc.name, strings.Join(lines, "\n"))
			}
			if !strings.Contains(stripANSI(strings.Join(lines, "\n")), "working tree clean") {
				t.Errorf("expected the caption to survive:\n%s", strings.Join(lines, "\n"))
			}
			if len(lines) > tc.height {
				t.Errorf("placeholder is %d rows, viewport is %d", len(lines), tc.height)
			}
		})
	}
}

// The mascot follows the session's tool, so an OpenCode pane gets Brace and not
// the Claude ghost.
func TestRenderLedgerEmptyState_uses_the_session_tool_mascot(t *testing.T) {
	claude := mascotRows(renderLedgerEmptyState("claude", ThemeForTool("claude"), 60, 27))
	opencode := mascotRows(renderLedgerEmptyState("opencode", ThemeForTool("opencode"), 60, 27))
	if len(opencode) == 0 {
		t.Fatal("opencode pane drew no mascot")
	}
	if strings.Join(claude, "\n") == strings.Join(opencode, "\n") {
		t.Error("opencode pane drew the claude mascot shape")
	}
}

// End to end: the ledger view renders the placeholder for an empty snapshot and
// still fits the pane exactly (an overflow would push the footer off-screen).
func TestLedgerView_empty_snapshot_renders_the_mascot_placeholder(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledger.NewSnapshot(0, nil, ledger.Metadata{}),
		LedgerOptions{Tool: "claude"})
	sizeLedger(m, 60, 30)

	view := stripANSI(m.View())
	if !strings.Contains(view, "█") {
		t.Errorf("empty snapshot should draw the mascot:\n%s", view)
	}
	if !strings.Contains(view, "working tree clean") {
		t.Errorf("empty snapshot should caption the mascot:\n%s", view)
	}
	if strings.Contains(view, "no changes") {
		t.Errorf("the bare \"no changes\" label must be gone:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != 30 {
		t.Errorf("view is %d rows, pane is 30", got)
	}
}
