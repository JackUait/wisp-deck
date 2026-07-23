package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The corpus is a recording, not a guess: every width in
// testdata/tmux_widths.json was captured from a live tmux pane by printing the
// string and reading back where the cursor landed (CSI 6n). It covers the
// scripts of the world through real strings lifted from shipped locale files,
// the constructs that segment strangely (Indic conjuncts, Thai SARA AM,
// Myanmar's spacing vowels, Hangul written out in jamo, halfwidth kana, flags,
// VS16 emoji, skin tones, soft hyphen), and one entry for every codepoint class
// where tmux and runewidth were ever found to disagree.
//
// Regenerate with: WISP_DECK_TMUX_WIDTH_E2E=1 go test ./internal/tui/ -run TestCellWidth_matches_a_live_tmux
type widthCase struct {
	S string `json:"s"`
	W int    `json:"w"`
}

func loadWidthCorpus(t *testing.T) []widthCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/tmux_widths.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var cases []widthCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(cases) < 500 {
		t.Fatalf("corpus shrank to %d cases; it is the evidence, do not trim it", len(cases))
	}
	return cases
}

// knownSlack is how many corpus entries may read WIDER than tmux painted them.
// Exactly four do, all the same thing: a Hangul jamo standing outside a
// syllable. tmux composes such an orphan onto whichever cell precedes it, which
// cellWidth cannot see from inside the segment, so it reserves a cell rather
// than risk an overflow (see clusterWidth). Korean written normally — as
// syllables, precomposed or spelled out in full — is exact, as is every other
// script here. Keep this number pinned: a change that shifts widths across the
// board would otherwise hide inside a loose bound.
const knownSlack = 4

func TestCellWidth_matches_the_recorded_terminal_widths(t *testing.T) {
	cases := loadWidthCorpus(t)
	slack := 0
	for _, c := range cases {
		got := cellWidth(c.S)
		if got == c.W {
			continue
		}
		slack++
		if slack <= 10 {
			t.Logf("cellWidth = %d, tmux painted %d: %q", got, c.W, c.S)
		}
	}
	if slack > knownSlack {
		t.Errorf("%d/%d corpus entries no longer match the terminal, allowed %d", slack, len(cases), knownSlack)
	}
}

// Under-counting is the failure that breaks a layout: text the column cannot
// hold spills past its edge and shoves everything after it sideways.
// Over-counting only leaves a blank cell. This asserts the safe direction
// holds even for input the width model gets wrong.
func TestCellWidth_never_under_counts(t *testing.T) {
	for _, c := range loadWidthCorpus(t) {
		if got := cellWidth(c.S); got < c.W {
			t.Errorf("cellWidth = %d but tmux needs %d cells — this overflows the column: %q", got, c.W, c.S)
		}
	}
}

// Whatever the width model says, the two columns must stay locked together:
// every row of a side-by-side diff has to land on the same column. This runs
// over the whole corpus, so it holds for every script in it.
func TestRenderSideBySide_rows_stay_aligned_for_every_script(t *testing.T) {
	cases := loadWidthCorpus(t)
	for _, cw := range []int{100, 121, 160} {
		for i := 0; i+3 < len(cases); i += 4 {
			body := strings.Join([]string{
				" " + cases[i].S,
				"-" + cases[i+1].S,
				"+" + cases[i+2].S,
				" " + cases[i+3].S,
			}, "\n")
			rows := strings.Split(renderSideBySide(body, cw), "\n")
			want := termWidth(rows[0])
			for j, row := range rows[1:] {
				if got := termWidth(row); got != want {
					t.Fatalf("cw=%d: row %d is %d cells, row 0 is %d — the divider drifts.\nbody:\n%s",
						cw, j+1, got, want, body)
				}
			}
		}
	}
}

// Same guarantee for the single-column view.
func TestNumberLines_rows_stay_within_width_for_every_script(t *testing.T) {
	const width = 100
	for _, c := range loadWidthCorpus(t) {
		for _, marker := range []string{" ", "+", "-"} {
			for _, row := range strings.Split(numberLines(marker+c.S, width), "\n") {
				if got := termWidth(row); got > width {
					t.Fatalf("row is %d cells, wider than the %d-cell view: %q", got, width, row)
				}
			}
		}
	}
}

// fitColumn and wrapColumns are the two places a column's contents are cut, and
// neither may overflow — for any script, at any width.
func TestColumnFitters_never_overflow_for_every_script(t *testing.T) {
	for _, c := range loadWidthCorpus(t) {
		for _, w := range []int{1, 7, 40, 80} {
			if got := termWidth(fitColumn(c.S, w)); got != w {
				t.Fatalf("fitColumn(%q, %d) is %d cells, want exactly %d", c.S, w, got, w)
			}
			if got := termWidth(tintColumn(c.S, w, diffAddBgSeq)); got != w {
				t.Fatalf("tintColumn(%q, %d) is %d cells, want exactly %d", c.S, w, got, w)
			}
			for _, row := range wrapColumns(c.S, w) {
				if got := termWidth(row); got > w {
					t.Fatalf("wrapColumns(%q, %d) produced a %d-cell row: %q", c.S, w, got, row)
				}
			}
		}
	}
}

// Wrapping may re-flow a line but must never drop or duplicate a character.
func TestWrapColumns_is_lossless_for_every_script(t *testing.T) {
	for _, c := range loadWidthCorpus(t) {
		for _, w := range []int{3, 17, 64} { // >= 2 so every glyph can be seated
			var b strings.Builder
			for _, row := range wrapColumns(c.S, w) {
				b.WriteString(row)
			}
			if b.String() != c.S {
				t.Fatalf("wrapColumns(%q, %d) round-tripped to %q", c.S, w, b.String())
			}
		}
	}
}

// The invariant a user can see: whatever is in the file, every row of the
// rendered popup is exactly as wide as the popup. A row that is even one cell
// over pushes the right border — and the side-by-side divider — off its column,
// which is what a "broken" file preview looks like.
func TestDiffView_every_rendered_row_is_exactly_the_popup_width(t *testing.T) {
	cases := loadWidthCorpus(t)
	for _, size := range [][2]int{{120, 24}, {100, 20}, {180, 30}} {
		w, h := size[0], size[1]
		for i := 0; i+2 < len(cases); i += 3 {
			body := " " + cases[i].S + "\n-" + cases[i+1].S + "\n+" + cases[i+2].S + "\n"
			for _, mode := range []int{diffModeInline, diffModeSideBySide} {
				m := sizeDiff(NewDiffView("locales/"+cases[i].S+".json", body), w, h)
				m.mode, m.modeForced = mode, true
				for row, line := range strings.Split(m.View(), "\n") {
					if got := termWidth(line); got != w {
						t.Fatalf("%dx%d mode=%d: row %d is %d cells, want %d\nbody: %q\nrow:  %q",
							w, h, mode, row, got, w, body, line)
					}
				}
			}
		}
	}
}
