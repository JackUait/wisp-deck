package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/ledger"
)

// A clean working tree used to render a bare " no changes" label in the pane's
// top-left corner. It now draws a small, muted wisp with a caption. The mascot
// is deliberately unobtrusive: it marks an idle state, so it must not out-shout
// the changesets the ledger exists to show.

// The art box: half-block pixels pack two rows per cell, so a 14x12 pixel wisp
// occupies 14 columns and 6 rows.
const (
	testMascotCols = 14
	testMascotRows = 6
)

// mascotRows returns the plain-text lines of a rendered placeholder that carry
// mascot pixels (any block glyph, whole or half).
func mascotRows(lines []string) []string {
	var art []string
	for _, line := range lines {
		plain := stripANSI(line)
		if strings.ContainsAny(plain, "█▀▄") {
			art = append(art, plain)
		}
	}
	return art
}

func TestRenderLedgerEmptyState_draws_a_centered_mascot_and_caption(t *testing.T) {
	const width, height = 60, 27
	lines := renderLedgerEmptyState(ThemeForTool("claude"), width, height)

	if len(lines) > height {
		t.Fatalf("placeholder is %d rows, viewport is %d", len(lines), height)
	}
	art := mascotRows(lines)
	if len(art) != testMascotRows {
		t.Fatalf("expected a %d-row mascot, got %d:\n%s",
			testMascotRows, len(art), stripANSI(strings.Join(lines, "\n")))
	}
	if !strings.Contains(stripANSI(strings.Join(lines, "\n")), "working tree clean") {
		t.Errorf("expected a caption under the mascot:\n%s", stripANSI(strings.Join(lines, "\n")))
	}

	// The mascot is centered in the pane.
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
	if want := (width - testMascotCols) / 2; minLead != want {
		t.Errorf("mascot left edge at column %d, want %d (centered)", minLead, want)
	}
}

// The mascot stays small: a clean tree is the ledger's resting state, and the
// first cut filled half the pane with saturated orange.
func TestRenderLedgerEmptyState_mascot_stays_small(t *testing.T) {
	art := mascotRows(renderLedgerEmptyState(ThemeForTool("claude"), 60, 27))
	if len(art) > 8 {
		t.Errorf("mascot is %d rows tall, want at most 8", len(art))
	}
	for _, line := range art {
		if got := visibleRuneWidth(strings.TrimRight(line, " ")); got > (60-testMascotCols)/2+16 {
			t.Errorf("mascot row is too wide: %q", line)
		}
	}
}

// The mascot is painted in the theme's MUTED (sleep) palette, never the full
// saturation the splash mascot uses — it should recede, not draw the eye. And
// it sleeps: closed eyes with blush cheeks, not the hollow black stare an
// open-eyed face collapses into at this scale.
func TestRenderLedgerEmptyState_mascot_is_muted_and_asleep(t *testing.T) {
	theme := ThemeForTool("claude")
	rendered := strings.Join(renderLedgerEmptyState(theme, 60, 27), "\n")

	if !strings.Contains(rendered, AnsiFromThemeColor(theme.SleepPrimary)) {
		t.Errorf("mascot missing the muted body color (%s)", theme.SleepPrimary)
	}
	if !strings.Contains(rendered, AnsiFromThemeColor(theme.SleepBlush)) {
		t.Errorf("mascot missing its blush cheeks (%s)", theme.SleepBlush)
	}
	for _, loud := range []struct {
		name  string
		color string
	}{
		{"Primary", string(theme.Primary)},
		{"Accent", string(theme.Accent)},
		{"EyeWhite", string(theme.EyeWhite)},
	} {
		if strings.Contains(rendered, "38;5;"+loud.color+"m") {
			t.Errorf("mascot uses the loud %s color (%s); expected the sleep palette",
				loud.name, loud.color)
		}
	}
}

// The wisp is a scaled-down splash ghost, not a separate character: it wears
// the pale cap rim across its crown and the dark feet band that the big
// mascots (GhostForTheme) wear — in their muted sleep tones.
func TestRenderLedgerEmptyState_mascot_echoes_the_splash_ghost(t *testing.T) {
	theme := ThemeForTool("claude")
	rendered := strings.Join(renderLedgerEmptyState(theme, 60, 27), "\n")

	if !strings.Contains(rendered, AnsiFromThemeColor(theme.SleepCap)) {
		t.Errorf("mascot missing the splash ghost's pale cap rim (%s)", theme.SleepCap)
	}
	if !strings.Contains(rendered, AnsiFromThemeColor(theme.SleepDarkFeet)) {
		t.Errorf("mascot missing the splash ghost's dark feet band (%s)", theme.SleepDarkFeet)
	}
	if !strings.Contains(rendered, AnsiFromThemeColor(theme.SleepDim)) {
		t.Errorf("mascot missing the splash ghost's darker lower body (%s)", theme.SleepDim)
	}
}

// The placeholder sits in the middle of the viewport, not jammed against the
// separator rule.
func TestRenderLedgerEmptyState_centers_vertically(t *testing.T) {
	lines := renderLedgerEmptyState(ThemeForTool("claude"), 60, 27)
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

// A pane too narrow or too short for the art degrades to the caption alone
// rather than a wrapped, mangled mascot.
func TestRenderLedgerEmptyState_falls_back_to_the_caption_when_it_cannot_fit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{"narrow", 12, 27},
		{"short", 60, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderLedgerEmptyState(ThemeForTool("claude"), tc.width, tc.height)
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

// The mascot follows the session's theme, so an OpenCode pane's wisp is violet
// rather than the claude orange — the shape itself is shared.
func TestRenderLedgerEmptyState_paints_the_mascot_in_the_theme(t *testing.T) {
	claude := renderLedgerEmptyState(ThemeForTool("claude"), 60, 27)
	opencode := renderLedgerEmptyState(ThemeForTool("opencode"), 60, 27)

	if !strings.Contains(strings.Join(claude, "\n"),
		AnsiFromThemeColor(ThemeForTool("claude").SleepPrimary)) {
		t.Error("claude mascot missing its muted hue")
	}
	if !strings.Contains(strings.Join(opencode, "\n"),
		AnsiFromThemeColor(ThemeForTool("opencode").SleepPrimary)) {
		t.Error("opencode mascot missing its muted hue")
	}
	if strings.Join(mascotRows(claude), "\n") != strings.Join(mascotRows(opencode), "\n") {
		t.Error("theme changed the mascot shape; it should only change its colors")
	}
}

// End to end: the ledger view renders the placeholder for an empty snapshot and
// still fits the pane exactly (an overflow would push the footer off-screen).
func TestLedgerView_empty_snapshot_renders_the_mascot_placeholder(t *testing.T) {
	m := NewLedgerModel(fakeLedgerSource{}, ledger.NewSnapshot(0, nil, ledger.Metadata{}),
		LedgerOptions{Tool: "claude"})
	sizeLedger(m, 60, 30)

	view := stripANSI(m.View())
	if !strings.ContainsAny(view, "█▀▄") {
		t.Errorf("empty snapshot should draw the mascot:\n%s", view)
	}
	if !strings.Contains(view, "working tree clean") {
		t.Errorf("empty snapshot should caption the mascot:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != 30 {
		t.Errorf("view is %d rows, pane is 30", got)
	}
}
