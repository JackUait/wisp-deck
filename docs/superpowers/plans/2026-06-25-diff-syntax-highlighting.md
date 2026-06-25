# Diff Popup Syntax Highlighting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add correct, language-aware syntax highlighting to the file-diff popup opened from the ledger's file list, with add/delete shown as a background tint over syntax-highlighted code.

**Architecture:** Move all coloring out of git into the Go pager. git emits a structural (uncolored) diff; the new `highlight.go` module syntax-highlights it with chroma by reconstructing and whole-tokenizing the old/new file versions (so multi-line constructs don't bleed). The pager stores the highlighted body in a separate field, keeps `m.content` raw, and the existing renderers add a green/red background tint to changed rows.

**Tech Stack:** Go 1.25, Bubbletea, lipgloss, `github.com/alecthomas/chroma/v2` (new), bash, tmux.

## Global Constraints

- Module path: `github.com/jackuait/wisp-deck`. Go version floor: `go 1.25.0` (see `go.mod`).
- Ghostty is the only supported terminal and supports truecolor — emit `\x1b[38;2;…m` / `\x1b[48;2;…m` SGR directly; no 256/16 color-profile degradation needed (YAGNI).
- Syntax-highlight ANSI must use **foreground-only** sequences: set foreground with `\x1b[38;2;R;G;Bm` and close with `\x1b[39m` (foreground-default), never `\x1b[0m`. A full reset inside a line would clear the row background tint.
- Add/delete background colors are semantic and **fixed** (never themed), consistent with the existing fixed green/red add/delete foreground styling in `diffview.go`.
- The diff body's `+` / `-` / leading-space markers are structural and must survive highlighting: every output line keeps its original marker as the first visible character so `classifyDiffLine` / `dropMarker` still work.
- TDD: write each test first, run it, watch it fail, then implement. Run `shellcheck` on any modified `.sh`. Run `./run-tests.sh` before declaring done. Commit after each task.

---

### Task 1: `highlightDiff` syntax-highlighting module

Adds the chroma dependency and a pure function that turns an uncolored unified-diff body into a syntax-highlighted body with markers preserved. No Bubbletea, no I/O — fully unit-testable.

**Files:**
- Create: `internal/tui/highlight.go`
- Create: `internal/tui/highlight_test.go`
- Modify: `go.mod`, `go.sum` (chroma dependency)

**Interfaces:**
- Produces: `func highlightDiff(body, filename string) string` — input is the structural diff body (lines prefixed with `+`/`-`/space, no ANSI) and the file path; output is the same body with per-line truecolor foreground SGR added and markers intact. Unknown/unsupported language → body returned unchanged.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/highlight_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestHighlightDiff -v`
Expected: FAIL — `undefined: highlightDiff` (compile error).

- [ ] **Step 3: Add the chroma dependency**

Run:
```bash
go get github.com/alecthomas/chroma/v2@latest
go mod tidy
```
Expected: `go.mod` gains `github.com/alecthomas/chroma/v2`, `go.sum` updated. Verify it builds the existing tree: `go build ./...` (Expected: no output, exit 0).

- [ ] **Step 4: Write the implementation**

Create `internal/tui/highlight.go`:

```go
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

// highlightDiff syntax-highlights the code in an uncolored unified-diff body.
// It reconstructs the old and new file versions from the diff and tokenizes
// each WHOLE file (so multi-line strings/comments don't bleed), then maps the
// colored lines back onto the diff: context and '+' lines from the new file,
// '-' lines from the old file. Each output line keeps its original marker as
// the first character, and only foreground SGR (\x1b[38;2;..m … \x1b[39m) is
// emitted so a later row background tint survives. Unknown language → body
// returned unchanged.
func highlightDiff(body, filename string) string {
	lexer := lexers.Match(filepath.Base(filename))
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
	for _, tok := range it.Tokens() {
		entry := style.Get(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for j, part := range parts {
			if j > 0 {
				lines = append(lines, cur.String())
				cur.Reset()
			}
			if part == "" {
				continue
			}
			if entry.Colour.IsSet() {
				c := entry.Colour
				fmt.Fprintf(&cur, "\x1b[38;2;%d;%d;%dm%s\x1b[39m",
					c.Red(), c.Green(), c.Blue(), part)
			} else {
				cur.WriteString(part)
			}
		}
	}
	lines = append(lines, cur.String())

	if len(lines) != len(srcLines) {
		return srcLines
	}
	return lines
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/tui/ -run TestHighlightDiff -v`
Expected: PASS (all four subtests).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/highlight.go internal/tui/highlight_test.go go.mod go.sum
git commit -m "feat(diff): add chroma syntax-highlighting module for the diff popup"
```

---

### Task 2: Highlight the body inside `NewDiffView`

Wire `highlightDiff` into the pager. Keep `m.content` raw (so status/count derivation and the existing exact-match test are untouched); store the highlighted body in a new field that only `bodyContent()` reads.

**Files:**
- Modify: `internal/tui/diffview.go` (struct field; `NewDiffView`; `bodyContent`)
- Modify: `internal/tui/diffview_test.go` (one existing test updated; one new test)

**Interfaces:**
- Consumes: `highlightDiff(body, filename string) string` (Task 1).
- Produces: `DiffViewModel.highlighted string` (unexported field); `bodyContent()` now collapses/returns the highlighted body. `m.content` remains the raw, uncolored body.

- [ ] **Step 1: Write the failing test (and fix the one test that now sees colored body)**

In `internal/tui/diffview_test.go`, the existing `TestDiffView_View_shows_title_controls_and_content` checks the **raw** view for body text; the body is now colored, so its content check must strip ANSI. Change its body-content assertion:

```go
	// was: if !strings.Contains(out, "unique-marker") {
	if !strings.Contains(stripA(out), "unique-marker") {
		t.Error("view should show the diff content")
	}
```

Then add a new test (append near the other `NewDiffView` tests):

```go
// NewDiffView keeps m.content raw but renders a syntax-highlighted body for a
// recognized language, so the on-screen code carries truecolor fg escapes while
// the stored content stays uncolored.
func TestNewDiffView_highlights_body_but_keeps_content_raw(t *testing.T) {
	raw := " package main\n+var x = 1\n"
	m := sizeDiff(NewDiffView("main.go", raw), 120, 30)
	if m.content != raw {
		t.Errorf("m.content should stay raw, got %q", m.content)
	}
	if !strings.Contains(m.View(), "\x1b[38;2;") {
		t.Errorf("rendered body should be syntax-highlighted (truecolor fg), got:\n%s", m.View())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestNewDiffView_highlights_body_but_keeps_content_raw' -v`
Expected: FAIL — the rendered body has no `\x1b[38;2;` because highlighting isn't wired in yet.

- [ ] **Step 3: Write the implementation**

In `internal/tui/diffview.go`, add the field to the struct (after `content string`):

```go
	content  string
	highlighted string // syntax-highlighted body; m.content stays raw
```

In `NewDiffView`, set it in the returned struct literal (add the field; `content` stays raw):

```go
	return DiffViewModel{
		title:       title,
		content:     content,
		highlighted: highlightDiff(content, title),
		added:       added,
		deleted:     deleted,
		status:      diffStatus(content),
		singleView:  single,
		compact:     true,
		collapsible: !single && hasCollapsibleContext(content, diffContextLines),
		hoverMode:   -1,
		hoverCtx:    -1,
	}
```

Change `bodyContent` to read the highlighted body:

```go
func (m DiffViewModel) bodyContent() string {
	if m.compact && m.collapsible {
		return collapseContext(m.highlighted, diffContextLines)
	}
	return m.highlighted
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestNewDiffView|TestDiffView_View_shows_title_controls_and_content' -v`
Expected: PASS.

- [ ] **Step 5: Run the full tui suite to catch regressions**

Run: `go test ./internal/tui/ -count=1`
Expected: PASS (all existing diffview tests strip ANSI or check chrome, so they stay green).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/diffview.go internal/tui/diffview_test.go
git commit -m "feat(diff): render the diff body syntax-highlighted in the popup"
```

---

### Task 3: Green/red background tint on changed rows

Give added rows a fixed dark-green background and removed rows a fixed dark-red background, spanning the row width, layered over the syntax foreground. Applies in both inline and side-by-side renderers.

**Files:**
- Modify: `internal/tui/diffview.go` (`numberLines` gains a width param + tint; `renderBodyMode`; `renderSideBySide` / `sbsCellStr` tint; new `tintColumn` helper and bg constants)
- Modify: `internal/tui/diffview_test.go` (update the two direct `numberLines(...)` call sites; add tint tests)

**Interfaces:**
- Consumes: existing `classifyDiffLine`, `fitColumn`, `diffGutterStyle`.
- Produces:
  - `const diffAddBgSeq = "\x1b[48;2;20;38;27m"` and `const diffDelBgSeq = "\x1b[48;2;46;20;24m"`
  - `func tintColumn(s string, width int, bgSeq string) string` — wraps fg-only content `s` with `bgSeq`, fits/pads to `width` visible columns, ends with a single `\x1b[0m`.
  - `numberLines(content string, width int) string` (signature changed — added `width`).

- [ ] **Step 1: Write the failing tests**

In `internal/tui/diffview_test.go`, first update the two existing **direct** `numberLines` calls to pass a width (the new signature). At ~line 244:

```go
	out := diffAnsiSeq.ReplaceAllString(numberLines(b.String(), 80), "")
```

At ~line 725 (the `TestNumberLines...`-style test that calls `numberLines(content)` directly — search for `numberLines(` in the test file and update the remaining direct call):

```go
	out := stripA(numberLines(content, 80))
```

Then add the tint tests:

```go
// An added line carries the green background tint; a removed line the red tint;
// a context line carries neither.
func TestNumberLines_tints_changed_rows(t *testing.T) {
	out := numberLines(" ctx\n+add\n-del\n", 40)
	lines := strings.Split(out, "\n")
	if strings.Contains(lines[0], diffAddBgSeq) || strings.Contains(lines[0], diffDelBgSeq) {
		t.Errorf("context row must not be tinted, got %q", lines[0])
	}
	if !strings.Contains(lines[1], diffAddBgSeq) {
		t.Errorf("added row should carry the green bg tint, got %q", lines[1])
	}
	if !strings.Contains(lines[2], diffDelBgSeq) {
		t.Errorf("removed row should carry the red bg tint, got %q", lines[2])
	}
}

// The tint spans the full row width (padded to width), so the highlight reads as
// a full-row band rather than stopping at the end of the text.
func TestTintColumn_pads_to_width(t *testing.T) {
	got := tintColumn("ab", 6, diffAddBgSeq)
	if !strings.HasPrefix(got, diffAddBgSeq) {
		t.Errorf("tint should open with the bg sequence, got %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("tint should end with a full reset, got %q", got)
	}
	if w := lipgloss.Width(got); w != 6 {
		t.Errorf("tinted column visible width = %d, want 6", w)
	}
}

// Side-by-side: the removed cell (left) is red-tinted and the added cell (right)
// is green-tinted.
func TestRenderSideBySide_tints_change_cells(t *testing.T) {
	out := renderSideBySide(" ctx\n-del\n+add\n", 120)
	if !strings.Contains(out, diffDelBgSeq) {
		t.Errorf("a removed cell should carry the red bg tint, got:\n%s", out)
	}
	if !strings.Contains(out, diffAddBgSeq) {
		t.Errorf("an added cell should carry the green bg tint, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestNumberLines_tints_changed_rows|TestTintColumn_pads_to_width|TestRenderSideBySide_tints_change_cells' -v`
Expected: FAIL — `undefined: tintColumn`, `undefined: diffAddBgSeq`, and `numberLines` arity error from the new calls.

- [ ] **Step 3: Write the implementation**

In `internal/tui/diffview.go`, add the constants and helper (near the other diff styles):

```go
// Fixed semantic background tints for changed rows: a dark green band behind
// added lines, dark red behind removed. Truecolor (Ghostty); foreground syntax
// color shows on top. Emitted as raw SGR rather than a lipgloss style so they
// compose with the foreground-only syntax escapes without an intervening reset.
const (
	diffAddBgSeq = "\x1b[48;2;20;38;27m"
	diffDelBgSeq = "\x1b[48;2;46;20;24m"
)

// tintColumn paints a full-width background band behind fg-only content s: it
// fits/truncates s to width visible columns (copying ANSI escapes verbatim, not
// counting them toward width), pads the remainder with spaces, and wraps the
// whole thing in bgSeq … reset so the band spans the row. s must carry only
// foreground SGR (no \x1b[0m), so the band isn't cleared mid-line.
func tintColumn(s string, width int, bgSeq string) string {
	if width < 0 {
		width = 0
	}
	rs := []rune(s)
	var b strings.Builder
	b.WriteString(bgSeq)
	vis := 0
	for i := 0; i < len(rs) && vis < width; {
		if rs[i] == '\x1b' {
			j := i
			for j < len(rs) && rs[j] != 'm' {
				j++
			}
			if j < len(rs) {
				j++
			}
			b.WriteString(string(rs[i:j]))
			i = j
			continue
		}
		b.WriteRune(rs[i])
		vis++
		i++
	}
	if vis < width {
		b.WriteString(strings.Repeat(" ", width-vis))
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
```

Change `numberLines` to take a `width` and tint changed rows. Replace the existing `numberLines` body so each rendered row is `gutter + (tinted code or plain code)`; the gutter stays untinted, the code area (everything after the gutter) carries the band out to `width`:

```go
func numberLines(content string, width int) string {
	lines := strings.Split(content, "\n")
	maxNo := 0
	for _, ln := range lines {
		if cnt, ok := isGapLine(ln); ok {
			maxNo += cnt
			continue
		}
		if ln != "" && !isRemovedLine(ln) {
			maxNo++
		}
	}
	w := len(itoa(maxNo))
	if w < 1 {
		w = 1
	}
	var b strings.Builder
	n := 0
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if cnt, ok := isGapLine(ln); ok {
			n += cnt
			b.WriteString(diffGutterStyle.Render(strings.Repeat(" ", w) + " │ "))
			b.WriteString(diffGapRowStyle.Render("⋯ " + itoa(cnt) + " unchanged lines"))
			continue
		}
		if ln == "" {
			continue
		}
		kind, _ := classifyDiffLine(ln)
		var num string
		if kind == diffDel {
			num = strings.Repeat(" ", w)
		} else {
			n++
			num = fmt.Sprintf("%*d", w, n)
		}
		gutter := diffGutterStyle.Render(num + " │ ")
		b.WriteString(gutter)
		codeW := width - lipgloss.Width(gutter)
		switch kind {
		case diffAdd:
			b.WriteString(tintColumn(ln, codeW, diffAddBgSeq))
		case diffDel:
			b.WriteString(tintColumn(ln, codeW, diffDelBgSeq))
		default:
			b.WriteString(ln)
		}
	}
	return b.String()
}
```

Update `renderBodyMode` to pass the width to `numberLines`:

```go
func renderBodyMode(content string, cw, mode int) string {
	if mode == diffModeSideBySide {
		return renderSideBySide(content, cw)
	}
	return numberLines(content, cw)
}
```

Tint the side-by-side change cells. In `renderSideBySide`, the `flush` closure emits paired del/add cells; tint the left (del) cell red and the right (add) cell green. Change `sbsCellStr` to take a `bgSeq` and the `emit`/`flush` calls to pass the right tint. Replace `sbsCellStr` with:

```go
// sbsCellStr renders one column: a dim right-aligned line-number gutter then the
// fitted text. A blank cell (no == 0) yields an all-space gutter and text. When
// bgSeq != "" the text area is painted with that background band.
func sbsCellStr(c sbsCell, gw, textW int, bgSeq string) string {
	gutter := diffGutterStyle.Render(fmt.Sprintf("%*d ", gw, c.no))
	if c.no == 0 {
		gutter = diffGutterStyle.Render(strings.Repeat(" ", gw) + " ")
	}
	if bgSeq == "" {
		return gutter + fitColumn(c.text, textW)
	}
	return gutter + tintColumn(c.text, textW, bgSeq)
}
```

In `renderSideBySide`, update `emit` and `flush` so context cells pass `""` and change cells pass the tint. Replace the `emit` closure and the `flush` loop body:

```go
	emit := func(l, r sbsCell, lbg, rbg string) {
		rows = append(rows, sbsCellStr(l, gw, textW, lbg)+diffGutterStyle.Render(" │ ")+sbsCellStr(r, gw, textW, rbg))
	}
	var dels, adds []sbsCell
	flush := func() {
		n := maxInt(len(dels), len(adds))
		for i := 0; i < n; i++ {
			var l, r sbsCell
			lbg, rbg := "", ""
			if i < len(dels) {
				l = dels[i]
				lbg = diffDelBgSeq
			}
			if i < len(adds) {
				r = adds[i]
				rbg = diffAddBgSeq
			}
			emit(l, r, lbg, rbg)
		}
		dels, adds = nil, nil
	}
```

And update the context `emit` call (the `diffContext` case) to pass no tint:

```go
		case diffContext:
			flush()
			oldNo++
			newNo++
			emit(sbsCell{oldNo, text}, sbsCell{newNo, text}, "", "")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestNumberLines_tints_changed_rows|TestTintColumn_pads_to_width|TestRenderSideBySide_tints_change_cells' -v`
Expected: PASS.

- [ ] **Step 5: Run the full tui suite**

Run: `go test ./internal/tui/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/diffview.go internal/tui/diffview_test.go
git commit -m "feat(diff): tint added/removed rows with a green/red background band"
```

---

### Task 4: Emit an uncolored diff from the pipeline

The pager now owns all coloring, so git must stop coloring. Change `open_diff_popup` to pass `--color=never`.

**Files:**
- Modify: `lib/compact-view.sh:307` (`--color=always` → `--color=never`)
- Modify: `test/bash/compact_view_test.go:570` (assertion)

**Interfaces:** none (shell command string change).

- [ ] **Step 1: Update the failing test first**

In `test/bash/compact_view_test.go`, in `TestOpenDiffPopup_builds_whole_file_diff_popup`, change the final assertion:

```go
	// was: assertContains(t, got, "color=always")
	assertContains(t, got, "color=never")
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./test/bash/ -run TestOpenDiffPopup_builds_whole_file_diff_popup -v`
Expected: FAIL — the script still emits `color=always`.

- [ ] **Step 3: Write the implementation**

In `lib/compact-view.sh`, line 307, change `--color=always` to `--color=never`:

```bash
    "git -C ${qd} --no-pager diff HEAD -U999999 --color=never -- ${qf} | ${strip} | wisp-deck-tui diff-view --ai-tool ${qtool} --title ${qf} ${backdrop_arg}"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./test/bash/ -run TestOpenDiffPopup -v`
Expected: PASS (both `_builds_whole_file_diff_popup` and `_quotes_path_with_spaces`).

- [ ] **Step 5: shellcheck the modified script**

Run: `shellcheck lib/compact-view.sh`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add lib/compact-view.sh test/bash/compact_view_test.go
git commit -m "feat(diff): feed an uncolored diff to the pager (it now owns coloring)"
```

---

### Task 5: Full verification and binary rebuild

Confirm the whole suite is green, scripts lint clean, and the developer's local TUI binary reflects the change.

**Files:** none (verification only).

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: exit 0, no output.

- [ ] **Step 2: Run the full test suite**

Run: `./run-tests.sh`
Expected: PASS (all packages).

- [ ] **Step 3: shellcheck all scripts**

Run: `shellcheck lib/*.sh lib/terminals/*.sh bin/wisp-deck wrapper.sh`
Expected: no output, exit 0.

- [ ] **Step 4: Rebuild the local TUI binary so the change is visible when running the app**

Run: `go build -o ~/.local/bin/wisp-deck-tui ./cmd/wisp-deck-tui`
Expected: exit 0. (A release with fresh release-asset binaries is a separate, user-initiated step per CLAUDE.md — out of scope here.)

- [ ] **Step 5: Manual smoke check (optional but recommended)**

Open a real session, click a changed code file in the ledger's file list, and confirm: the code is syntax-highlighted, added lines have a green band, removed lines a red band, and inline ↔ side-by-side / changes ↔ full toggles still work.

---

## Self-Review

**Spec coverage:**
- Pipeline change (`--color=never`) → Task 4. ✓
- chroma engine, embedded, pure-Go → Task 1 (dependency + module). ✓
- Language detection by filename, plain fallback → Task 1 (`lexers.Match(filepath.Base(...))`, `nil` → passthrough). ✓
- Whole-document tokenization (old/new reconstruction, no bleed) → Task 1 (`highlightDiff`, `TestHighlightDiff_multiline_string_no_bleed`). ✓
- Foreground-only color → Task 1 (`\x1b[38;2;…m … \x1b[39m`; test asserts no `\x1b[0m`). ✓
- Background tint over syntax fg, semantic/fixed → Task 3. ✓
- Markers preserved / classification untouched → Task 1 (test) + design keeping `classifyDiffLine`/`dropMarker`. ✓
- Highlight once, toggles stay instant → Task 2 (`highlighted` field set in `NewDiffView`, read by `bodyContent`). ✓
- Existing pager features preserved; full suite green → Tasks 2/3 step "run full tui suite", Task 5. ✓
- Spec note: content-analysis fallback before plain. **Deliberate simplification:** the plan uses `lexers.Match` only (deterministic, testable) and treats no-match as plain passthrough; chroma content `Analyse` is omitted as YAGNI. This narrows detection slightly but never mis-highlights. Acceptable scope reduction.

**Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to". Every code step shows complete code. ✓

**Type consistency:** `highlightDiff(body, filename string) string`, `highlightSource(source, lexer, style)`, `tintColumn(s, width, bgSeq)`, `numberLines(content, width)`, `sbsCellStr(c, gw, textW, bgSeq)`, `emit(l, r, lbg, rbg)`, constants `diffAddBgSeq`/`diffDelBgSeq`, field `highlighted` — all names used consistently across tasks. The `numberLines` arity change is propagated to both its production caller (`renderBodyMode`) and both test call sites. ✓
