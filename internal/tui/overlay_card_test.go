package tui

import (
	"strings"
	"testing"
)

// overlayTestScreen builds a placed full-screen string of 'x' runes.
func overlayTestScreen(w, h int) string {
	row := strings.Repeat("x", w)
	rows := make([]string, h)
	for i := range rows {
		rows[i] = row
	}
	return strings.Join(rows, "\n")
}

func TestOverlayCard_PlacesCardLinesAtOrigin(t *testing.T) {
	m := newTestMenu()
	m.width, m.height = 20, 5
	card := []string{"AB", "CD"}

	out := m.overlayCard(overlayTestScreen(20, 5), card, 3, 1, 2)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("overlay should keep screen height 5, got %d lines", len(lines))
	}

	row1 := stripAnsi(lines[1])
	if row1[3:5] != "AB" {
		t.Errorf("card row 0 should land at x=3 on row 1, got %q", row1)
	}
	row2 := stripAnsi(lines[2])
	if row2[3:5] != "CD" {
		t.Errorf("card row 1 should land at x=3 on row 2, got %q", row2)
	}
	// Backdrop rows outside the card keep their (repainted) content.
	if got := stripAnsi(lines[0]); got != strings.Repeat("x", 20) {
		t.Errorf("row 0 should be untouched backdrop content, got %q", got)
	}
	if !strings.Contains(row1[:3], "xxx") {
		t.Errorf("backdrop left of card should keep content, got %q", row1[:3])
	}
}

func TestOverlayAbout_MatchesOverlayCardComposition(t *testing.T) {
	m := newTestMenu()
	m.width, m.height = 80, 24
	placed := overlayTestScreen(80, 24)

	left, top, w, _ := m.aboutCardLayout()
	want := m.overlayCard(placed, strings.Split(m.renderAboutCard(), "\n"), left, top, w)
	if got := m.overlayAbout(placed); got != want {
		t.Error("overlayAbout should be a thin wrapper over overlayCard (outputs differ)")
	}
}
