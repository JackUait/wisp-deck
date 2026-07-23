package tui

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Chroma returns one token per character for text its lexer doesn't recognise,
// which is every character of a non-Latin string value. Wrapping each in its
// own SGR pair inflates a Bengali line about eightfold — paid on every row the
// pager measures and every row the terminal draws.
func TestHighlightSource_coalesces_a_run_of_one_color(t *testing.T) {
	lexer := lexers.Match("messages.json")
	if lexer == nil {
		t.Skip("no json lexer")
	}
	src := `  "tools.video.errorNotMediaUrl": "ভিডিও ফাইলের লিঙ্ক দিন",`
	line := highlightSource(src, lexer, styles.Get(diffSyntaxStyle))[0]

	opens := strings.Count(line, "\x1b[38;2;")
	if opens > 6 {
		t.Errorf("line opens %d color spans for %d tokens of text; runs are not being coalesced:\n%q",
			opens, len([]rune(src)), line)
	}
	if stripA(line) != src {
		t.Errorf("highlighting altered the text:\n got %q\nwant %q", stripA(line), src)
	}
}

// A color left open at a line break would bleed into the next row, which the
// pager re-opens colors for independently.
func TestHighlightSource_closes_every_color_at_the_line_end(t *testing.T) {
	lexer := lexers.Match("app.ts")
	if lexer == nil {
		t.Skip("no ts lexer")
	}
	src := "const a = \"one\"\nconst b = \"two\"\nconst c = 3\n"
	for i, line := range highlightSource(src, lexer, styles.Get(diffSyntaxStyle)) {
		opens := strings.Count(line, "\x1b[38;2;")
		closes := strings.Count(line, "\x1b[39m")
		if opens != closes {
			t.Errorf("line %d leaves %d of %d colors open: %q", i, opens-closes, opens, line)
		}
	}
}
