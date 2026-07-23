package tui

import (
	"strings"
	"testing"
)

// Astro files have no upstream chroma lexer; wisp-deck maps them onto the
// HTML lexer so an .astro diff still gets syntax color instead of passing
// through as plain text.
func TestHighlightDiff_colorizes_astro(t *testing.T) {
	body := " <h1>Title</h1>\n+<p>hello</p>\n"
	got := highlightDiff(body, "Card.astro")
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Errorf("Astro content should gain truecolor fg escapes, got: %q", got)
	}
}

// An unknown (empty) filename has no lexer, so the body is returned verbatim.
func TestHighlightDiff_unknown_language_passes_through(t *testing.T) {
	body := " context\n+added\n-removed\n"
	if got := highlightDiff(body, ""); got != body {
		t.Errorf("unknown language should pass through unchanged.\n got: %q\nwant: %q", got, body)
	}
}

// A known language gets truecolor foreground sequences injected into the code.
func TestHighlightDiff_colorizes_known_language(t *testing.T) {
	body := " package main\n+func main() {}\n"
	got := highlightDiff(body, "main.go")
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Errorf("Go content should gain truecolor fg escapes, got: %q", got)
	}
	if strings.Contains(got, "\x1b[0m") {
		t.Errorf("highlighting must not emit a full reset (it would clear row bg), got: %q", got)
	}
}

// Markers and line count survive highlighting: each output line's first visible
// character (after stripping ANSI) equals the input line's marker.
func TestHighlightDiff_preserves_markers_and_line_count(t *testing.T) {
	body := " ctx\n+add\n-del\n"
	got := highlightDiff(body, "x.go")
	in := strings.Split(body, "\n")
	out := strings.Split(got, "\n")
	if len(in) != len(out) {
		t.Fatalf("line count changed: in=%d out=%d", len(in), len(out))
	}
	for i := range in {
		if in[i] == "" {
			continue
		}
		stripped := diffAnsiSeq.ReplaceAllString(out[i], "")
		if stripped == "" || stripped[0] != in[i][0] {
			t.Errorf("line %d marker changed: in=%q out(stripped)=%q", i, in[i], stripped)
		}
	}
}

// Whole-file tokenization (not line-by-line) keeps a multi-line raw string a
// single string token, so its continuation line is still syntax-colored. Naive
// per-line highlighting would mis-tokenize " second`" on its own.
func TestHighlightDiff_multiline_string_no_bleed(t *testing.T) {
	body := " s := `first\n second`\n"
	got := highlightDiff(body, "x.go")
	cont := strings.Split(got, "\n")[1] // " second`"
	if !strings.Contains(cont, "\x1b[38;2;") {
		t.Errorf("continuation line of a multi-line string should be colored, got: %q", cont)
	}
}
