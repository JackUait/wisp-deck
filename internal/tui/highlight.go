package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// diffSyntaxStyle is the chroma style used for code in the diff popup. A dark
// style to match the pager's dark chrome; styles.Get falls back gracefully if
// the name is ever unavailable.
const diffSyntaxStyle = "github-dark"

// lexerFor resolves the chroma lexer for a filename, adding mappings for
// extensions chroma ships no lexer for. Astro (.astro) is HTML-first — an HTML
// template plus an optional TypeScript frontmatter fence — so it borrows the
// HTML lexer, which colors the markup, embedded <script>, and <style> and
// leaves the frontmatter as plain text rather than dropping all color.
func lexerFor(basename string) chroma.Lexer {
	if strings.HasSuffix(basename, ".astro") {
		return lexers.Get("HTML")
	}
	return lexers.Match(basename)
}

// highlightDiff syntax-highlights the code in an uncolored unified-diff body.
// It reconstructs the old and new file versions from the diff and tokenizes
// each WHOLE file (so multi-line strings/comments don't bleed), then maps the
// colored lines back onto the diff: context and '+' lines from the new file,
// '-' lines from the old file. Each output line keeps its original marker as
// the first character, and only foreground SGR (\x1b[38;2;..m … \x1b[39m) is
// emitted so a later row background tint survives. Unknown language → body
// returned unchanged.
func highlightDiff(body, filename string) string {
	lexer := lexerFor(filepath.Base(filename))
	if lexer == nil {
		return body
	}
	style := styles.Get(diffSyntaxStyle)

	lines := strings.Split(body, "\n")

	// ref records, for each diff line, where its highlighted text comes from.
	type ref struct {
		fromOld bool
		idx     int
		marker  byte // 0 = no marker (blank line or non-standard prefix)
		blank   bool
	}
	refs := make([]ref, len(lines))
	var newSrc, oldSrc []string

	for i, ln := range lines {
		if ln == "" {
			refs[i] = ref{blank: true}
			continue
		}
		switch ln[0] {
		case '+':
			refs[i] = ref{fromOld: false, idx: len(newSrc), marker: '+'}
			newSrc = append(newSrc, ln[1:])
		case '-':
			refs[i] = ref{fromOld: true, idx: len(oldSrc), marker: '-'}
			oldSrc = append(oldSrc, ln[1:])
		case ' ':
			// Context: present in both files; display from the new file.
			refs[i] = ref{fromOld: false, idx: len(newSrc), marker: ' '}
			newSrc = append(newSrc, ln[1:])
			oldSrc = append(oldSrc, ln[1:])
		default:
			// No standard marker (e.g. a "\ No newline" note): treat the whole
			// line as code with no marker, present in both files.
			refs[i] = ref{fromOld: false, idx: len(newSrc), marker: 0}
			newSrc = append(newSrc, ln)
			oldSrc = append(oldSrc, ln)
		}
	}

	newHL := highlightSource(strings.Join(newSrc, "\n"), lexer, style)
	oldHL := highlightSource(strings.Join(oldSrc, "\n"), lexer, style)

	out := make([]string, len(lines))
	for i, r := range refs {
		if r.blank {
			out[i] = ""
			continue
		}
		var code string
		if r.fromOld {
			code = oldHL[r.idx]
		} else {
			code = newHL[r.idx]
		}
		if r.marker != 0 {
			out[i] = string(r.marker) + code
		} else {
			out[i] = code
		}
	}
	return strings.Join(out, "\n")
}

// highlightSource tokenizes a whole source string and returns one colored
// string per source line, emitting foreground-only truecolor SGR. If
// tokenization fails or produces a line count that doesn't match the source
// (some lexers append a trailing newline), it falls back to the plain lines so
// alignment with the diff is never broken.
func highlightSource(source string, lexer chroma.Lexer, style *chroma.Style) []string {
	srcLines := strings.Split(source, "\n")
	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return srcLines
	}

	var lines []string
	var cur strings.Builder
	open := "" // the color currently left open on this line, if any
	for _, tok := range it.Tokens() {
		entry := style.Get(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for j, part := range parts {
			if j > 0 {
				if open != "" { // never let a color cross a line boundary
					cur.WriteString("\x1b[39m")
					open = ""
				}
				lines = append(lines, cur.String())
				cur.Reset()
			}
			if part == "" {
				continue
			}
			// Coalesce a run of same-colored tokens under one SGR pair. Chroma
			// hands back one token per character for text its lexer doesn't
			// recognise — which is every character of a Bengali or Japanese
			// string value — and wrapping each of those in its own escape pair
			// costs about eight bytes of escape per byte of text, on every row
			// the pager lays out and every row the terminal then draws.
			colour := ""
			if entry.Colour.IsSet() {
				c := entry.Colour
				colour = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.Red(), c.Green(), c.Blue())
			}
			if colour != open {
				if open != "" {
					cur.WriteString("\x1b[39m")
				}
				cur.WriteString(colour)
				open = colour
			}
			cur.WriteString(part)
		}
	}
	if open != "" {
		cur.WriteString("\x1b[39m")
	}
	lines = append(lines, cur.String())

	if len(lines) != len(srcLines) {
		return srcLines
	}
	return lines
}
