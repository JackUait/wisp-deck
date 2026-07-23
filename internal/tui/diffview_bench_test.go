package tui

import (
	"strings"
	"testing"
)

// diffBodyFrom turns lines into a unified-diff body of the shape the pager
// receives: mostly context, with edits sprinkled through.
func diffBodyFrom(lines []string, want int) string {
	var b strings.Builder
	for i := 0; i < want; i++ {
		switch i % 40 {
		case 7:
			b.WriteString("-")
		case 8:
			b.WriteString("+")
		default:
			b.WriteString(" ")
		}
		b.WriteString(lines[i%len(lines)])
		b.WriteByte('\n')
	}
	return b.String()
}

// The case that started this: a locale file written in non-Latin scripts, laid
// out in both views — what opening its preview costs.
func BenchmarkRenderBody_localeFile(b *testing.B) {
	var lines []string
	for _, c := range loadWidthCorpus(&testing.T{}) {
		if strings.HasPrefix(c.S, `  "`) { // the real shipped-locale entries
			lines = append(lines, c.S)
		}
	}
	if len(lines) == 0 {
		b.Skip("no locale lines in corpus")
	}
	body := highlightDiff(diffBodyFrom(lines, 4000), "messages.json")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderSideBySide(body, 160)
		_ = numberLines(body, 160)
	}
}

func BenchmarkRenderBody_asciiCode(b *testing.B) {
	body := highlightDiff(diffBodyFrom([]string{
		"const value = compute(index, options) // ordinary code line",
		"function handle(request: Request): Response {",
		"  return new Response(JSON.stringify(payload), { status: 200 })",
		"}",
	}, 4000), "app.ts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderSideBySide(body, 160)
		_ = numberLines(body, 160)
	}
}
