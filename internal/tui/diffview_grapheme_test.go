package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// Real lines from a Bengali locale file — the shape that broke the file
// preview. Bengali writes a syllable as a base consonant plus combining vowel
// signs / viramas: "ভি" is two codepoints but ONE terminal cell, so measuring
// the line a rune at a time counts nearly twice the columns the terminal
// actually paints, and the column layout wraps and truncates far too early.
const (
	bnCode  = `    "tools.video.errorNotMediaUrl": "ভিডিও ফাইলের লিঙ্ক দিন (.mp4, .webm, .mov)",`
	bnShort = "অথবা" // clusters: অ | থ | বা  → 3 cells, 4 runes
)

// termWidth is what the terminal actually paints for s, ignoring ANSI color.
// Pinned to a live tmux by TestCellWidth_matches_the_terminal below.
func termWidth(s string) int {
	return cellWidth(diffAnsiSeq.ReplaceAllString(s, ""))
}

// TestCellWidth_matches_the_terminal is the oracle the rest of this file leans
// on. Every want here was captured from a real tmux 3.6a pane by printing the
// string and reading #{cursor_x} — i.e. the number of cells the terminal that
// draws the popup really consumed. Nothing else in the process agrees: summing
// runewidth.RuneWidth per rune (what the layout used to do) over-counts every
// combining mark, uniseg.StringWidth over-counts Indic spacing marks, and
// runewidth.StringWidth over the whole string under-counts Indic conjuncts
// because this repo's tables are newer than tmux's.
func TestCellWidth_matches_the_terminal(t *testing.T) {
	tests := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"é", 1},
		{"日本語", 6},      // CJK: two cells each
		{"👨‍👩‍👦", 2},    // ZWJ sequence: one glyph, two cells
		{"ক্ষ", 2},      // Bengali conjunct
		{"আপলোড", 4},    // 5 runes, 4 cells
		{"ভিডিওটি", 4},  // 7 runes, 4 cells
		{"পরিবর্তন", 6}, // 8 runes, 6 cells
		{"অথবা", 3},     // 4 runes, 3 cells
		{"লিঙ্ক", 3},
		{"ভিডিও ফাইলের লিঙ্ক দিন", 15}, // 22 runes, 15 cells
		{"สวัสดีครับ", 7},              // Thai tone/vowel marks
		{"مرحبا", 5},                   // Arabic
		{bnShort, 3},
		{bnCode, 74},

		// Scripts whose marks segment in ways that fooled earlier attempts.
		{"မင်္ဂလာပါ ကမ္ဘာ", 9}, // Myanmar: spacing vowels ride on their base
		{"กำ", 2},   // Thai SARA AM: a spacing vowel that does NOT
		{"ｶﾞ", 2},   // halfwidth kana + voiced mark: two cells
		{"ප්‍ර", 1}, // Sinhala conjunct written with ZWJ
		{"ஜ்‍ஞ", 1}, // Tamil conjunct written with ZWJ
		{"a‍b", 2},  // ...but ZWJ does not glue ASCII together
		{"각", 2},    // Hangul spelled out in jamo
		{"한글", 4},
		{"ᬅᬆ", 2},       // Balinese
		{"ក្ខ", 2},      // Khmer
		{"བཀྲ", 2},      // Tibetan
		{"ދިވެހި", 3},   // Dhivehi (Thaana)
		{"ᏣᎳᎩ", 3},      // Cherokee
		{"ⵜⴰⵎⴰⵣⵉⵖⵜ", 8}, // Tifinagh
		{"𞤀𞤁", 2},       // Adlam
		{"ߒߞߏ", 3},      // N'Ko

		// Emoji and symbols, where the two width tables disagree most.
		{"👍🏽", 2},     // skin tone rides on the emoji
		{"🏽", 2},      // ...but is a glyph of its own when alone
		{"☝🏽", 4},     // ...and after a BMP emoji, tmux charges for both
		{"❤️", 2},     // VS16 promotes a one-cell glyph to emoji presentation
		{"❤", 1},      // without it, one cell
		{"#️⃣", 2},    // keycap
		{"🇺🇸", 2},     // one flag
		{"🇺🇸🇯🇵🇩🇪", 2}, // adjacent flags share one slot on tmux's grid
		{"🇺🇸 English", 10},
		{"☰", 2}, // trigram
		{"䷀", 2}, // Yijing hexagram
		{"𝌆", 2}, // Tai Xuan Jing
		{"­", 1}, // soft hyphen: a format control the terminal prints
		{"⁦", 0}, // bidi isolate: one it does not
	}
	for _, tt := range tests {
		if got := cellWidth(tt.s); got != tt.want {
			t.Errorf("cellWidth(%q) = %d, want %d (measured in tmux)", tt.s, got, tt.want)
		}
	}
	// The measurement that broke the preview: summing runewidth per rune reads
	// this line as half again as wide as tmux draws it. Kept as a live
	// demonstration so the table above can't be mistaken for a no-op.
	perRune := 0
	for _, r := range bnCode {
		perRune += runewidth.RuneWidth(r)
	}
	if perRune <= 74 {
		t.Errorf("per-rune sum = %d; expected it to over-count the 74-cell line", perRune)
	}
}

func TestWrapColumns_keeps_a_bengali_line_that_fits_on_one_row(t *testing.T) {
	w := termWidth(bnCode)
	rows := wrapColumns(bnCode, w)
	if len(rows) != 1 {
		t.Fatalf("wrapColumns split a line that fits %d columns into %d rows: %q", w, len(rows), rows)
	}
}

func TestWrapColumns_bengali_rows_never_exceed_the_column(t *testing.T) {
	const w = 40
	rows := wrapColumns(bnCode, w)
	var joined strings.Builder
	for i, row := range rows {
		if got := termWidth(row); got > w {
			t.Errorf("row %d occupies %d columns, want <= %d: %q", i, got, w, row)
		}
		joined.WriteString(diffAnsiSeq.ReplaceAllString(row, ""))
	}
	if joined.String() != bnCode {
		t.Errorf("wrapping lost text:\n got %q\nwant %q", joined.String(), bnCode)
	}
}

func TestFitColumn_keeps_a_bengali_line_that_fits(t *testing.T) {
	w := termWidth(bnCode) + 5
	got := diffAnsiSeq.ReplaceAllString(fitColumn(bnCode, w), "")
	if !strings.HasPrefix(got, bnCode) {
		t.Errorf("fitColumn truncated a line that fits %d columns:\n got %q\nwant prefix %q", w, got, bnCode)
	}
	if termWidth(got) != w {
		t.Errorf("fitColumn produced %d columns, want %d", termWidth(got), w)
	}
}

func TestFitColumn_never_orphans_a_combining_mark(t *testing.T) {
	// bnShort is exactly 3 cells, so all of it must survive a 3-column fit —
	// including the vowel sign on the final consonant.
	got := diffAnsiSeq.ReplaceAllString(fitColumn(bnShort, 3), "")
	if !strings.Contains(got, "বা") {
		t.Errorf("fitColumn dropped a combining mark from its base: got %q, want %q", got, bnShort)
	}
}

func TestFitColumn_bengali_is_exactly_width_columns(t *testing.T) {
	for _, w := range []int{5, 20, 40} {
		if got := termWidth(fitColumn(bnCode, w)); got != w {
			t.Errorf("fitColumn(bn, %d) occupies %d columns, want %d", w, got, w)
		}
	}
}

func TestTintColumn_bengali_band_spans_the_whole_column(t *testing.T) {
	for _, w := range []int{5, 20, 40} {
		if got := termWidth(tintColumn(bnCode, w, diffAddBgSeq)); got != w {
			t.Errorf("tintColumn(bn, %d) painted %d columns, want %d", w, got, w)
		}
	}
}

func TestWrapColumns_zwj_emoji_counts_as_one_glyph(t *testing.T) {
	const family = "👨‍👩‍👦 done" // the ZWJ sequence is 2 cells, not 6
	w := termWidth(family)
	if rows := wrapColumns(family, w); len(rows) != 1 {
		t.Errorf("wrapColumns split a %d-column ZWJ emoji line into %d rows: %q", w, len(rows), rows)
	}
}

func TestExpandTabsLine_measures_bengali_by_cells(t *testing.T) {
	// "অথবা" is 3 cells, so the tab after it advances to column 4.
	got := expandTabsLine(" "+bnShort+"\tx", 4)
	want := " " + bnShort + " x"
	if got != want {
		t.Errorf("expandTabsLine landed the tab on the wrong stop:\n got %q\nwant %q", got, want)
	}
}

// The screenshot symptom, at the level the user sees it: with Bengali content
// the two columns and the " │ " divider between them drifted apart row by row.
// Every rendered row must occupy the same number of cells.
func TestRenderSideBySide_bengali_rows_are_all_the_same_width(t *testing.T) {
	const cw = 120
	body := strings.Join([]string{
		` "tools.video.moreOptions": "আরও বিকল্প",`,
		`-  "tools.video.uploading": "আপলোড হচ্ছে…",`,
		`+  "tools.video.errorNotMediaUrl": "ভিডিও ফাইলের লিঙ্ক দিন (.mp4, .webm, .mov) অথবা এমবেড কোড",`,
		` "tools.video.errorReplace": "প্রতিস্থাপন",`,
	}, "\n")

	// (cw-3)/2 per column, so a row lands on cw or cw-1 depending on parity —
	// what matters is that every row lands on the SAME column, which is what
	// keeps the divider vertical.
	rows := strings.Split(renderSideBySide(body, cw), "\n")
	want := termWidth(rows[0])
	if want < cw-1 || want > cw {
		t.Fatalf("first row occupies %d cells, want %d or %d", want, cw-1, cw)
	}
	for i, row := range rows[1:] {
		if got := termWidth(row); got != want {
			t.Errorf("row %d occupies %d cells, want %d (divider drifts): %q", i+1, got, want, row)
		}
	}
}

func TestTruncatePath_measures_by_cells(t *testing.T) {
	const p = "src/অথবা/messages.json"
	got := truncatePath(p, termWidth(p))
	if got != p {
		t.Errorf("truncatePath shortened a path that already fits:\n got %q\nwant %q", got, p)
	}
}

// The header carries the file path, so a path in a non-Latin script has to be
// measured the same way the body is — otherwise the right-anchored Discard
// button lands on the wrong column or wraps out of reach of its click target.
func TestDiffView_header_row_fits_a_non_latin_path(t *testing.T) {
	for _, path := range []string{
		"apps/web/messages/bn.json",
		"apps/web/বার্তা/messages/bn.json",
		"apps/web/メッセージ/ja.json",
		"apps/web/сообщения/ru.json",
	} {
		m := sizeDiff(NewDiffView(path, " line one\n line two\n"), 120, 24)
		for i, row := range strings.Split(m.View(), "\n") {
			if got := termWidth(row); got > 120 {
				t.Errorf("path %q: view row %d is %d cells, wider than the 120-cell popup", path, i, got)
			}
		}
	}
}

// A column too narrow to seat a glyph must still not overflow: the glyph is
// stood in for rather than allowed to overhang into the next column.
func TestWrapColumns_never_overhangs_a_column_too_narrow_for_the_glyph(t *testing.T) {
	for _, s := range []string{"你好世界", "👍🏽ok", "日本語テスト"} {
		for _, w := range []int{1, 2, 3} {
			for _, row := range wrapColumns(s, w) {
				if got := termWidth(row); got > w {
					t.Errorf("wrapColumns(%q, %d) produced a %d-cell row %q", s, w, got, row)
				}
			}
		}
	}
}
