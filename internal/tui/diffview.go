package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// DiffViewModel is a scrollable pager for a structural (uncolored) git diff,
// which it syntax-highlights itself (see highlightDiff), shown inside the
// click-to-open popup. Unlike less, it closes on a single Esc press because
// Bubbletea's input parser emits a distinct KeyEscape for a lone Esc and parses
// arrow-key escape sequences separately. q and ctrl+c also quit. The viewport
// bubble handles scrolling (↑↓/j/k, space/b page, u/d half-page, mouse wheel);
// g/G jump to the top/bottom.
type DiffViewModel struct {
	title       string
	content     string
	highlighted string // syntax-highlighted body; m.content stays raw
	status      string // "added" | "deleted" | "modified", derived from the diff
	added    int
	deleted  int
	width       int // full popup (window) size; the box floats centered within it
	height      int
	backdrop    []string // dimmed screen snapshot shown behind the box (may be nil)
	mode        int      // diffModeInline | diffModeSideBySide
	modeForced  bool     // true once the user picks a view (stops width auto-pick)
	singleView  bool     // whole-file add/delete: lock to inline, hide the switcher
	compact     bool     // true = changes-only (collapsed context); false = full file
	collapsible bool     // true when the full diff has context worth collapsing
	viewport    viewport.Model
	ready       bool
	quitting    bool
	hoverMode   int // layout-switch tab under the pointer, or -1 (none)
	hoverCtx    int // context-switch tab under the pointer, or -1 (none)
	// Discard: the title row carries a [ Discard ] button. Clicking it (or 'd')
	// arms a Yes/No confirm step rather than discarding outright, since the op is
	// irreversible. Confirming sets discardRequested and quits; the caller reads
	// DiscardRequested() and runs the git restore.
	discardArmed     bool
	discardRequested bool
}

// DiscardRequested reports whether the user confirmed discarding the file's
// working-tree changes. The caller acts on it after the program exits.
func (m DiffViewModel) DiscardRequested() bool { return m.discardRequested }

// View modes and the clickable tab labels that switch between them. The labels
// have fixed visible widths so the click hit-boxes (tabAt) stay stable.
const (
	diffModeInline = iota
	diffModeSideBySide
)

const (
	diffTabIndent     = 1
	diffTabInlineText = "[ Inline ]"
	diffTabSxsText    = "[ Side-by-side ]"
	// The context (changes-only vs full) switcher sits to the right of the layout
	// switcher, separated by a gap. Its labels have fixed widths so contextTabAt's
	// hit-boxes stay stable.
	diffCtxTabGap      = 4
	diffTabIconGap     = 2 // spaces between a group's icon and its first button
	diffTabChangesText = "[ Changes ]"
	diffTabFullText    = "[ Full ]"
)

// Nerd Font group-label icons shown to the left of each switcher: a columns
// glyph for the inline/side-by-side layout group, and an eye glyph for the
// changes-only/full-file visibility group.
const (
	diffLayoutIcon = "" // nf-fa-columns
	diffCtxIcon    = "" // nf-fa-eye
)

// Context-switch tab ids, returned by contextTabAt.
const (
	ctxTabChanges = iota
	ctxTabFull
)

// Discard control labels. The bracketed labels carry no lipgloss padding, so a
// label's visible width equals its rune length and the click hit-boxes
// (discardButtonSpan / discardConfirmSpans) line up exactly with the render.
const (
	diffDiscardLabel = "[ Discard ]"
	diffDiscardYes   = "[ Yes ]"
	diffDiscardNo    = "[ No ]"
	diffDiscardGap   = 1 // columns between the Yes and No confirm chips
)

// diffContextLines is how many unchanged lines the changes-only view keeps
// around each change (git's own default), the rest collapsed into a marker.
const diffContextLines = 3

// diffTabWidth is how many columns a tab expands to in the rendered diff. Tabs
// are expanded to spaces before layout (see expandTabs) so the fixed-width
// columns line up: a literal tab's on-screen width depends on the terminal's
// tab stops, which the column-fitting math can't know, so a surviving tab would
// desync the column widths and shove the side-by-side divider off its column.
const diffTabWidth = 4

// expandTabs replaces tabs with spaces in a raw (un-highlighted) diff body so no
// tab survives into the laid-out columns. Each line is expanded from its own
// column 0; a leading diff marker (+/-/space) is kept verbatim and the code
// after it is expanded from column 0, matching how the marker is later dropped
// and the code placed at the cell's left edge. Runs are advanced by display
// width so a wide rune before a tab still lands the tab on the right stop.
func expandTabs(content string, tabWidth int) string {
	if !strings.Contains(content, "\t") {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = expandTabsLine(ln, tabWidth)
	}
	return strings.Join(lines, "\n")
}

// expandTabsLine expands the tabs in one raw diff line (see expandTabs).
func expandTabsLine(line string, tabWidth int) string {
	if line == "" || !strings.ContainsRune(line, '\t') {
		return line
	}
	marker, code := "", line
	if c := line[0]; c == '+' || c == '-' || c == ' ' {
		marker, code = line[:1], line[1:]
	}
	var b strings.Builder
	col := 0
	for _, r := range code {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += runewidth.RuneWidth(r)
	}
	return marker + b.String()
}

var (
	diffTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("208")). // orange, matching the popup border
			Padding(0, 1)

	diffAddStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	diffDelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red

	// File-status badge: a filled chip whose color matches the kind of change —
	// green for added, red for deleted, orange for modified.
	diffStatusBadgeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Padding(0, 1)
	diffStatusColors     = map[string]lipgloss.Color{
		"added":    lipgloss.Color("2"),
		"deleted":  lipgloss.Color("1"),
		"modified": lipgloss.Color("208"),
	}

	diffRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))

	// View-switch tab buttons: the active mode is an orange chip, the other dim.
	diffTabActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("208"))
	diffTabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	// Hovered (but inactive) tab: brightened + underlined so the pointer target
	// reads as clickable without looking active.
	diffTabHoverStyle = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("255"))
	// Group-label icon: a dim accent glyph to the left of each switcher group.
	diffTabIconStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))

	// Discard button: a red chip — destructive, so it reads as a warning. The
	// armed-confirm Yes chip is red (the destructive choice); No is dim.
	diffDiscardStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("1"))
	diffDiscardYesStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("1"))
	diffDiscardNoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	diffBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 1)

	// The floating modal frame: a rounded, ORANGE border. The popup runs
	// full-screen and borderless (see open_diff_popup), so this border — drawn
	// by the pager — is what the user sees as "the window"; the area around it is
	// the click-to-close margin, which shows the dimmed backdrop.
	diffBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("208"))

	// The captured screen behind the popup, rendered faint/gray so the bright
	// box clearly floats above it.
	diffDimStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))
)

// applyDiffChrome repaints the popup furniture (border, rule, active tab, icon,
// title, modified badge) in the active theme's UI accent. The vars above keep
// their claude-orange (208) defaults so rendering still works in tests that
// never call ApplyTheme; ApplyTheme invokes this so OpenCode sessions go purple
// while claude stays orange. The green/red add/delete colors are intentionally
// left fixed — they carry meaning, not theme.
func applyDiffChrome(accent lipgloss.Color) {
	diffTitleStyle = diffTitleStyle.Foreground(accent)
	diffStatusColors["modified"] = accent
	diffRuleStyle = diffRuleStyle.Foreground(accent)
	diffTabActiveStyle = diffTabActiveStyle.Background(accent)
	diffTabIconStyle = diffTabIconStyle.Foreground(accent)
	diffBoxStyle = diffBoxStyle.BorderForeground(accent)
}

// ParseBackdrop composites the serialized screen capture produced by
// open_diff_popup into W×H rows of plain text. The format is a "W H" header
// line, then one or more "PANE <left> <top>" blocks, each followed by that
// pane's captured lines and an "ENDPANE" sentinel. Each pane's lines are placed
// onto a space-filled grid at its window position; pane borders (which
// capture-pane omits) stay blank, which is invisible once the grid is dimmed.
// Returns nil if the header is missing or malformed.
func ParseBackdrop(data string) []string {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 {
		return nil
	}
	var w, h int
	if _, err := fmt.Sscanf(lines[0], "%d %d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return nil
	}
	grid := make([][]rune, h)
	for y := range grid {
		grid[y] = make([]rune, w)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}
	for i := 1; i < len(lines); {
		var left, top int
		if n, _ := fmt.Sscanf(lines[i], "PANE %d %d", &left, &top); n < 2 {
			i++
			continue
		}
		i++
		for row := top; i < len(lines) && lines[i] != "ENDPANE"; i, row = i+1, row+1 {
			if row < 0 || row >= h {
				continue
			}
			for k, r := range []rune(lines[i]) {
				col := left + k
				if col >= 0 && col < w {
					grid[row][col] = r
				}
			}
		}
		if i < len(lines) && lines[i] == "ENDPANE" {
			i++
		}
	}
	out := make([]string, h)
	for y := range grid {
		out[y] = string(grid[y])
	}
	return out
}

var diffAnsiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

// diffGutterStyle renders the line-number gutter faint/gray so it sits behind
// the code without competing with the diff's own green/red.
var diffGutterStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))

// Fixed semantic background tints for changed rows: a dark green band behind
// added lines, dark red behind removed. Truecolor (Ghostty); foreground syntax
// color shows on top. Emitted as raw SGR rather than a lipgloss style so they
// compose with the foreground-only syntax escapes without an intervening reset.
const (
	diffAddBgSeq = "\x1b[48;2;20;38;27m"
	diffDelBgSeq = "\x1b[48;2;46;20;24m"
)

// tintColumn paints a full-width background band behind fg-only content s: it
// fits/truncates s to width DISPLAY columns (copying ANSI escapes verbatim, not
// counting them toward width; counting each rune by its terminal cell width so a
// double-width glyph can't overrun the band), pads the remainder with spaces,
// and wraps the whole thing in bgSeq … reset so the band spans the row. s must
// carry only foreground SGR (no \x1b[0m), so the band isn't cleared mid-line.
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
		rw := runewidth.RuneWidth(rs[i])
		if vis+rw > width { // a wide rune that won't fit the last cell
			break
		}
		b.WriteRune(rs[i])
		vis += rw
		i++
	}
	if vis < width {
		b.WriteString(strings.Repeat(" ", width-vis))
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// diffGapRowStyle renders the "⋯ N unchanged lines" divider shown where the
// changes-only view has collapsed a run of context.
var diffGapRowStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))

// isRemovedLine reports whether a (possibly ANSI-colored) diff line is a removal
// (leading '-' once color is stripped). The +++/--- markers are stripped
// upstream, so a leading '-' here is a removed file line.
func isRemovedLine(line string) bool {
	s := diffAnsiSeq.ReplaceAllString(line, "")
	return s != "" && s[0] == '-'
}

// numberLines prefixes each diff line with a right-aligned NEW-file line-number
// gutter ("<n> │ "). Context and added (+) lines advance the counter and show
// their number; removed (-) lines aren't in the current file, so their gutter is
// blank. The gutter is dim; the line's own ANSI color (after it) is preserved. A
// trailing empty line (from a final newline) gets no gutter. Changed rows are
// tinted with a full-width background band out to width.
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
	blankGutter := diffGutterStyle.Render(strings.Repeat(" ", w) + " │ ")

	var b strings.Builder
	n := 0
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if cnt, ok := isGapLine(ln); ok {
			n += cnt
			b.WriteString(blankGutter)
			b.WriteString(diffGapRowStyle.Render("⋯ " + itoa(cnt) + " unchanged lines"))
			continue
		}
		if ln == "" {
			continue
		}
		kind, _ := classifyDiffLine(ln)
		var gutter string
		if kind == diffDel {
			gutter = blankGutter
		} else {
			n++
			gutter = diffGutterStyle.Render(fmt.Sprintf("%*d", w, n) + " │ ")
		}
		codeW := width - lipgloss.Width(gutter)
		// A line wider than codeW wraps onto continuation rows (blank gutter)
		// rather than getting cut off; most lines are one row.
		for j, row := range wrapColumns(ln, codeW) {
			if j > 0 {
				b.WriteByte('\n')
				b.WriteString(blankGutter)
			} else {
				b.WriteString(gutter)
			}
			switch kind {
			case diffAdd:
				b.WriteString(tintColumn(row, codeW, diffAddBgSeq))
			case diffDel:
				b.WriteString(tintColumn(row, codeW, diffDelBgSeq))
			default:
				b.WriteString(row)
			}
		}
	}
	return b.String()
}

// Diff line kinds, classified from the leading marker once color is stripped.
const (
	diffSkip = iota // empty trailing line; produces no row
	diffContext
	diffAdd
	diffDel
)

// diffMinColWidth is the minimum number of columns each side needs for the
// side-by-side view to be worthwhile; below it, the inline view is used.
const diffMinColWidth = 40

// classifyDiffLine reports a line's kind and its text with the leading marker
// (and the diff's context-space prefix) removed but its ANSI color preserved.
func classifyDiffLine(line string) (int, string) {
	vis := diffAnsiSeq.ReplaceAllString(line, "")
	if vis == "" {
		return diffSkip, ""
	}
	switch vis[0] {
	case '+':
		return diffAdd, dropMarker(line)
	case '-':
		return diffDel, dropMarker(line)
	default:
		return diffContext, dropMarker(line)
	}
}

// dropMarker removes the leading diff marker (the +/-/space prefix) from a
// possibly ANSI-colored line, preserving every escape sequence and the rest of
// the text (so a colored line keeps its color, minus the marker glyph).
func dropMarker(line string) string {
	rs := []rune(line)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == '\x1b' { // copy an ESC...m sequence verbatim
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
		// first visible rune: drop it, then copy the remainder verbatim
		b.WriteString(string(rs[i+1:]))
		break
	}
	return b.String()
}

// diffGapPrefix marks a synthetic "collapsed context" line. The integer that
// follows is how many unchanged lines were hidden; the renderers use it to draw
// a divider and to advance the line-number counters across the gap. The NUL
// prefix can't collide with a real diff line (those start with +/-/space).
const diffGapPrefix = "\x00GAP:"

// gapLine builds the sentinel for a collapsed run of n unchanged lines.
func gapLine(n int) string { return diffGapPrefix + itoa(n) }

// isGapLine reports whether s is a gap sentinel and, if so, how many lines it
// hides.
func isGapLine(s string) (int, bool) {
	if !strings.HasPrefix(s, diffGapPrefix) {
		return 0, false
	}
	digits := s[len(diffGapPrefix):]
	if digits == "" {
		return 0, false
	}
	n := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// diffKeepMask classifies each diff line and marks which lines the changes-only
// view keeps: every changed (+/-) line plus the `ctx` unchanged lines on either
// side of it. Returns the keep flags and the per-line kinds (so callers needn't
// re-classify).
func diffKeepMask(lines []string, ctx int) (keep []bool, kind []int) {
	n := len(lines)
	keep = make([]bool, n)
	kind = make([]int, n)
	for i, ln := range lines {
		kind[i], _ = classifyDiffLine(ln)
	}
	for i := range lines {
		if kind[i] != diffAdd && kind[i] != diffDel {
			continue
		}
		keep[i] = true
		for d := 1; d <= ctx; d++ {
			if i-d >= 0 {
				keep[i-d] = true
			}
			if i+d < n {
				keep[i+d] = true
			}
		}
	}
	return keep, kind
}

// hasCollapsibleContext reports whether the changes-only view would hide any
// line: there must be at least one change to anchor context around AND at least
// one unchanged line farther than ctx from every change. A change-less diff has
// nothing to collapse, so it's shown in full. Used to decide whether the
// changes/full switcher is worth showing.
func hasCollapsibleContext(content string, ctx int) bool {
	lines := strings.Split(content, "\n")
	keep, kind := diffKeepMask(lines, ctx)
	hasChange, hasHidden := false, false
	for i := range lines {
		switch {
		case kind[i] == diffAdd || kind[i] == diffDel:
			hasChange = true
		case kind[i] == diffContext && !keep[i]:
			hasHidden = true
		}
	}
	return hasChange && hasHidden
}

// collapseContext returns the diff body with every run of unchanged context
// farther than ctx lines from a change replaced by a single gap sentinel
// (gapLine) encoding how many lines it hid. Changed lines, nearby context, and
// the trailing blank line are kept verbatim.
func collapseContext(content string, ctx int) string {
	lines := strings.Split(content, "\n")
	keep, kind := diffKeepMask(lines, ctx)

	var b strings.Builder
	first := true
	hidden := 0
	write := func(s string) {
		if !first {
			b.WriteByte('\n')
		}
		b.WriteString(s)
		first = false
	}
	flushGap := func() {
		if hidden > 0 {
			write(gapLine(hidden))
			hidden = 0
		}
	}
	for i, ln := range lines {
		switch {
		case kind[i] == diffSkip: // trailing blank line: keep it, never collapse
			flushGap()
			write(ln)
		case keep[i]:
			flushGap()
			write(ln)
		default: // an unchanged line too far from any change: collapse it
			hidden++
		}
	}
	flushGap()
	return b.String()
}

// fitColumn truncates or space-pads a possibly ANSI-colored string to exactly
// width DISPLAY columns (escape sequences don't count toward width; each rune
// counts by its terminal cell width so a double-width glyph can't overrun),
// ending in a reset so color can't bleed into the next column.
func fitColumn(s string, width int) string {
	if width < 0 {
		width = 0
	}
	rs := []rune(s)
	var b strings.Builder
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
		rw := runewidth.RuneWidth(rs[i])
		if vis+rw > width { // a wide rune that won't fit the last cell
			break
		}
		b.WriteRune(rs[i])
		vis += rw
		i++
	}
	b.WriteString("\x1b[0m")
	if vis < width {
		b.WriteString(strings.Repeat(" ", width-vis))
	}
	return b.String()
}

// ansiIsReset reports whether seq (a full "\x1b[...m" escape) clears the
// active foreground color rather than setting one: an empty, "0", or "39"
// parameter. Those are the only codes this package ever emits or expects in a
// diff body — highlightSource closes every color it opens with "39m" (see
// highlight.go), and "0m"/empty are the general ANSI reset forms used in test
// fixtures — so this covers every "color ended here" case wrapColumns needs
// to track.
func ansiIsReset(seq string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
	return inner == "" || inner == "0" || inner == "39"
}

// wrapColumns splits a possibly ANSI-colored string into rows of at most
// width display columns each (greedy width-based wrap, no word-boundary
// smarts — a diff/code line wraps mid-token same as an editor would). This is
// the single place where a long diff line's overflow goes: every renderer
// that lays out a fixed-width column wraps through here instead of cutting
// the line short, so the rest of a long line shows up on a continuation row
// rather than vanishing past the edge. The active foreground color escape (if
// any) is tracked and re-opened at the start of each continuation row so a
// mid-token wrap doesn't lose syntax highlighting. Returns one (possibly
// empty) row for input that already fits, including the empty string.
func wrapColumns(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	rs := []rune(s)
	var rows []string
	var b strings.Builder
	vis := 0
	active := ""
	flush := func() {
		rows = append(rows, b.String())
		b.Reset()
		vis = 0
	}
	for i := 0; i < len(rs); {
		if rs[i] == '\x1b' {
			j := i
			for j < len(rs) && rs[j] != 'm' {
				j++
			}
			if j < len(rs) {
				j++
			}
			seq := string(rs[i:j])
			if ansiIsReset(seq) {
				active = ""
			} else {
				active = seq
			}
			b.WriteString(seq)
			i = j
			continue
		}
		rw := runewidth.RuneWidth(rs[i])
		if vis > 0 && vis+rw > width { // wrap before a rune that would overflow this row
			flush()
			if active != "" {
				b.WriteString(active)
			}
		}
		b.WriteRune(rs[i])
		vis += rw
		i++
	}
	rows = append(rows, b.String())
	return rows
}

// pickByWidth chooses the view that fits: side-by-side when each column would
// get at least diffMinColWidth columns, otherwise inline.
func pickByWidth(cw int) int {
	if (cw-3)/2 >= diffMinColWidth { // 3 = the " │ " divider
		return diffModeSideBySide
	}
	return diffModeInline
}

// renderBodyMode renders the unified diff <content> in the requested mode for a
// box interior <cw> columns wide.
func renderBodyMode(content string, cw, mode int) string {
	if mode == diffModeSideBySide {
		return renderSideBySide(content, cw)
	}
	return numberLines(content, cw)
}

// renderDiffBody renders in whichever mode the width suggests (used when the
// user hasn't explicitly chosen one).
func renderDiffBody(content string, cw int) string {
	return renderBodyMode(content, cw, pickByWidth(cw))
}

// tabRow renders the clickable "[ Inline ] [ Side-by-side ]" view switcher, the
// active mode shown as a filled chip. Its column layout matches tabAt.
func (m DiffViewModel) tabRow() string {
	inline, sxs := diffTabInactiveStyle, diffTabInactiveStyle
	if m.mode == diffModeSideBySide {
		sxs = diffTabActiveStyle
	} else {
		inline = diffTabActiveStyle
	}
	// Highlight a hovered tab (unless it's already the active one).
	if m.hoverMode == diffModeInline && m.mode != diffModeInline {
		inline = diffTabHoverStyle
	}
	if m.hoverMode == diffModeSideBySide && m.mode != diffModeSideBySide {
		sxs = diffTabHoverStyle
	}
	row := strings.Repeat(" ", diffTabIndent) +
		diffTabIconStyle.Render(diffLayoutIcon) + strings.Repeat(" ", diffTabIconGap) +
		inline.Render(diffTabInlineText) + " " + sxs.Render(diffTabSxsText)
	if !m.collapsible {
		return row
	}
	// The changes-only vs full switcher, its own icon-labelled group set apart
	// from the layout switcher by the gap.
	changes, full := diffTabInactiveStyle, diffTabInactiveStyle
	if m.compact {
		changes = diffTabActiveStyle
	} else {
		full = diffTabActiveStyle
	}
	if m.hoverCtx == ctxTabChanges && !m.compact {
		changes = diffTabHoverStyle
	}
	if m.hoverCtx == ctxTabFull && m.compact {
		full = diffTabHoverStyle
	}
	return row + strings.Repeat(" ", diffCtxTabGap) +
		diffTabIconStyle.Render(diffCtxIcon) + strings.Repeat(" ", diffTabIconGap) +
		changes.Render(diffTabChangesText) + " " + full.Render(diffTabFullText)
}

// diffTabLayout returns the content-column start of each clickable button,
// derived from the same icon/label widths tabRow renders with so the hit-tests
// can't drift from the drawing. Layout: indent, layout-icon + icon-gap, Inline,
// space, Side-by-side, gap, context-icon + icon-gap, Changes, space, Full.
func diffTabLayout() (inlineStart, sxsStart, changesStart, fullStart int) {
	inlineStart = diffTabIndent + lipgloss.Width(diffLayoutIcon) + diffTabIconGap
	sxsStart = inlineStart + len(diffTabInlineText) + 1
	layoutEnd := sxsStart + len(diffTabSxsText)
	changesStart = layoutEnd + diffCtxTabGap + lipgloss.Width(diffCtxIcon) + diffTabIconGap
	fullStart = changesStart + len(diffTabChangesText) + 1
	return inlineStart, sxsStart, changesStart, fullStart
}

// tabAt maps a click's column (relative to the box content's left edge) to the
// layout-switch button it lands on, or -1 if it misses both.
func tabAt(contentX int) int {
	inlineStart, sxsStart, _, _ := diffTabLayout()
	switch {
	case contentX >= inlineStart && contentX < inlineStart+len(diffTabInlineText):
		return diffModeInline
	case contentX >= sxsStart && contentX < sxsStart+len(diffTabSxsText):
		return diffModeSideBySide
	default:
		return -1
	}
}

// contextTabAt maps a click's content column to the context (changes/full)
// switcher button it lands on, or -1 if it misses both.
func contextTabAt(contentX int) int {
	_, _, changesStart, fullStart := diffTabLayout()
	switch {
	case contentX >= changesStart && contentX < changesStart+len(diffTabChangesText):
		return ctxTabChanges
	case contentX >= fullStart && contentX < fullStart+len(diffTabFullText):
		return ctxTabFull
	default:
		return -1
	}
}

// sbsCell is one side of a side-by-side row: a line number (0 = blank cell) and
// the colored text.
type sbsCell struct {
	no   int
	text string
}

// renderSideBySide lays the diff out in two columns. Context lines appear on
// both sides at their respective old/new line numbers; a change block pairs its
// removed lines (left) with its added lines (right), padding the shorter side
// with blank cells. Each column is line-numbered and truncated to fit.
func renderSideBySide(content string, cw int) string {
	lines := strings.Split(content, "\n")

	oldTotal, newTotal := 0, 0
	for _, ln := range lines {
		if cnt, ok := isGapLine(ln); ok {
			oldTotal += cnt
			newTotal += cnt
			continue
		}
		switch k, _ := classifyDiffLine(ln); k {
		case diffContext:
			oldTotal++
			newTotal++
		case diffAdd:
			newTotal++
		case diffDel:
			oldTotal++
		}
	}
	gw := len(itoa(maxInt(oldTotal, newTotal)))
	if gw < 1 {
		gw = 1
	}
	colW := (cw - 3) / 2
	textW := colW - gw - 1 // gutter digits + one space
	if textW < 1 {
		textW = 1
	}

	var rows []string
	// emit lays out one logical old/new pair as one or more screen rows: a
	// cell whose text is wider than textW wraps onto continuation rows
	// (blank gutter) instead of getting cut off. The two sides wrap
	// independently, so the shorter side is padded with blank rows to keep
	// both columns — and the " │ " divider between them — aligned.
	emit := func(l, r sbsCell, lbg, rbg string) {
		lRows := wrapColumns(l.text, textW)
		rRows := wrapColumns(r.text, textW)
		n := maxInt(len(lRows), len(rRows))
		for i := 0; i < n; i++ {
			var lc, rc sbsCell
			lBg, rBg := "", ""
			if i < len(lRows) {
				no := 0
				if i == 0 {
					no = l.no
				}
				lc, lBg = sbsCell{no, lRows[i]}, lbg
			}
			if i < len(rRows) {
				no := 0
				if i == 0 {
					no = r.no
				}
				rc, rBg = sbsCell{no, rRows[i]}, rbg
			}
			rows = append(rows, sbsCellStr(lc, gw, textW, lBg)+diffGutterStyle.Render(" │ ")+sbsCellStr(rc, gw, textW, rBg))
		}
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

	oldNo, newNo := 0, 0
	for _, ln := range lines {
		if cnt, ok := isGapLine(ln); ok {
			flush()
			oldNo += cnt // hidden lines are unchanged context: both sides advance
			newNo += cnt
			rows = append(rows, diffGapRowStyle.Render("⋯ "+itoa(cnt)+" unchanged lines"))
			continue
		}
		k, text := classifyDiffLine(ln)
		switch k {
		case diffSkip:
			continue
		case diffContext:
			flush()
			oldNo++
			newNo++
			emit(sbsCell{oldNo, text}, sbsCell{newNo, text}, "", "")
		case diffDel:
			oldNo++
			dels = append(dels, sbsCell{oldNo, text})
		case diffAdd:
			newNo++
			adds = append(adds, sbsCell{newNo, text})
		}
	}
	flush()
	return strings.Join(rows, "\n")
}

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

// countDiffLines tallies the added (+) and deleted (-) lines of the diff body.
// The body is the structural (uncolored) diff with the +++/--- file markers
// stripped upstream, so a leading +/- is an authoritative add/delete marker;
// context lines (leading space) are ignored. ANSI escapes are still dropped
// first so the count is correct even when called on a syntax-highlighted body.
func countDiffLines(content string) (added, deleted int) {
	for _, line := range strings.Split(content, "\n") {
		s := diffAnsiSeq.ReplaceAllString(line, "")
		if s == "" {
			continue
		}
		switch s[0] {
		case '+':
			added++
		case '-':
			deleted++
		}
	}
	return added, deleted
}

// isSingleSided reports whether the diff is a whole-file addition or deletion —
// every body line is the same kind (+ or -) with no context. Because the diff
// is produced with -U999999 (whole file as one hunk), a modified file always
// carries context lines, so a context-free, one-sided diff is exactly a git
// status A (added) or D (deleted) file. Such a diff has nothing to compare
// across two columns, so the pager locks to the inline view and hides the
// switcher.
func isSingleSided(content string) bool {
	added, deleted, context := 0, 0, 0
	for _, line := range strings.Split(content, "\n") {
		s := diffAnsiSeq.ReplaceAllString(line, "")
		if s == "" {
			continue
		}
		switch s[0] {
		case '+':
			added++
		case '-':
			deleted++
		default:
			context++
		}
	}
	if context > 0 {
		return false
	}
	return (added > 0 && deleted == 0) || (deleted > 0 && added == 0)
}

// diffStatus classifies the file's git status from its diff body: a one-sided,
// context-free diff is a whole-file addition ("added") or deletion ("deleted"),
// and anything else is a "modified" file.
func diffStatus(content string) string {
	if !isSingleSided(content) {
		return "modified"
	}
	if added, _ := countDiffLines(content); added > 0 {
		return "added"
	}
	return "deleted"
}

// NewDiffView builds the pager for the given title (the file path, shown in the
// header) and content (the structural, uncolored diff body, which is
// syntax-highlighted once here for display while m.content keeps the raw body).
// The added/deleted line counts and the file status shown in the header are
// derived from the raw content. A whole-file add/delete is shown in a single
// (inline) view with no view switcher.
func NewDiffView(title, content string) DiffViewModel {
	added, deleted := countDiffLines(content)
	single := isSingleSided(content)
	// Expand tabs before highlighting so no tab reaches the column layout; the
	// raw content is kept for the line counts/status (tabs don't affect those).
	expanded := expandTabs(content, diffTabWidth)
	return DiffViewModel{
		title:       title,
		content:     content,
		highlighted: highlightDiff(expanded, title),
		added:       added,
		deleted:     deleted,
		status:      diffStatus(content),
		singleView:  single,
		compact:     true,
		collapsible: !single && hasCollapsibleContext(content, diffContextLines),
		hoverMode:   -1,
		hoverCtx:    -1,
	}
}

// bodyContent is the diff body to render: collapsed (changes-only) when the view
// is compact and there's context worth hiding, otherwise the full file.
func (m DiffViewModel) bodyContent() string {
	if m.compact && m.collapsible {
		return collapseContext(m.highlighted, diffContextLines)
	}
	return m.highlighted
}

// WithBackdrop sets the dimmed screen snapshot shown behind the floating box
// (typically from ParseBackdrop). With no backdrop the margin is left blank.
func (m DiffViewModel) WithBackdrop(rows []string) DiffViewModel {
	m.backdrop = rows
	return m
}

func (m DiffViewModel) Init() tea.Cmd {
	return nil
}

// headerHeight and footerHeight are the chrome rows reserved above and below the
// scrolling viewport: the title block + a rule, and a single control bar.
const (
	diffHeaderHeight       = 4 // title, blank gap, view-switch tabs, rule
	diffSingleHeaderHeight = 2 // single-sided file: title, rule (no tabs, no gap)
	diffFooterHeight       = 1
)

// headerHeight is the number of chrome rows above the scrolling viewport. A
// single-sided file drops the view-switch tab row (and the gap before it), so
// its header is shorter.
func (m DiffViewModel) headerHeight() int {
	if m.singleView {
		return diffSingleHeaderHeight
	}
	return diffHeaderHeight
}

// layout derives the floating box geometry from the full popup size: mh/mv are
// the click-to-close margins on each side, and contentW/contentH are the box's
// interior (inside its border). The box border adds 1 row/col per side, so the
// box occupies columns [mh, width-mh) and rows [mv, height-mv) — used verbatim
// by View (lipgloss.Place centers it exactly there) and by the mouse hit-test.
func (m DiffViewModel) layout() (mh, mv, contentW, contentH int) {
	mh = m.width / 20 // ~5% margin
	if mh < 2 {
		mh = 2
	}
	mv = m.height / 20
	if mv < 1 {
		mv = 1
	}
	contentW = m.width - 2*mh - 2 // minus margins, minus the box border
	if contentW < 1 {
		contentW = 1
	}
	contentH = m.height - 2*mv - 2
	if contentH < 1 {
		contentH = 1
	}
	return mh, mv, contentW, contentH
}

func (m DiffViewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		_, _, cw, ch := m.layout()
		h := ch - m.headerHeight() - diffFooterHeight
		if h < 1 {
			h = 1
		}
		if !m.ready {
			m.viewport = viewport.New(cw, h)
			m.ready = true
		} else {
			m.viewport.Width = cw
			m.viewport.Height = h
		}
		// Until the user picks a view, the layout auto-adapts to width: side-by-side
		// when wide enough, inline otherwise. A single-sided file is always inline.
		if m.singleView {
			m.mode = diffModeInline
		} else if !m.modeForced {
			m.mode = pickByWidth(cw)
		}
		m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
		return m, nil

	case tea.MouseMsg:
		mh, mv, cw, _ := m.layout()
		// The view-switch tabs live on content row 2 (screen row mv+3) — below the
		// title and the blank gap row — their columns offset by the left border at
		// mh+1. Single-sided files have no switcher. Track which tab the pointer is
		// over so it can highlight.
		onTabRow := !m.singleView && msg.Y == mv+3
		hovered, hoveredCtx := -1, -1
		if onTabRow {
			hovered = tabAt(msg.X - (mh + 1))
			if m.collapsible {
				hoveredCtx = contextTabAt(msg.X - (mh + 1))
			}
		}

		if msg.Action == tea.MouseActionMotion {
			m.hoverMode = hovered
			m.hoverCtx = hoveredCtx
			return m, nil
		}

		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// The discard control lives on the title row (content row 0 -> screen
			// row mv+1). When armed it shows Yes/No; otherwise the Discard button.
			if msg.Y == mv+1 {
				cx := msg.X - (mh + 1)
				if m.discardArmed {
					ys, ye, ns, ne := discardConfirmSpans(cw)
					if cx >= ys && cx < ye {
						m.discardRequested = true
						m.quitting = true
						return m, tea.Quit
					}
					if cx >= ns && cx < ne {
						m.discardArmed = false
						return m, nil
					}
				} else {
					ds, de := discardButtonSpan(cw)
					if cx >= ds && cx < de {
						m.discardArmed = true
						return m, nil
					}
				}
			}
			// A click on a layout-switch tab switches the inline/side-by-side mode.
			if hovered != -1 {
				m.modeForced = true
				m.mode = hovered
				m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
				return m, nil
			}
			// A click on the context switcher toggles changes-only vs full file.
			if hoveredCtx != -1 {
				m.compact = hoveredCtx == ctxTabChanges
				m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
				return m, nil
			}
			// A click in the margin outside the floating box closes the popup;
			// other inside clicks fall through to the viewport (wheel scrolling).
			if msg.X < mh || msg.X >= m.width-mh || msg.Y < mv || msg.Y >= m.height-mv {
				m.quitting = true
				return m, tea.Quit
			}
		}

	case tea.KeyMsg:
		// While armed, the keyboard drives the confirm: Enter/y confirm, n cancels,
		// Esc cancels (Ctrl-C still hard-quits). All other keys are swallowed so the
		// confirm can't be scrolled or toggled out from under the user.
		if m.discardArmed {
			switch msg.Type {
			case tea.KeyCtrlC:
				m.quitting = true
				return m, tea.Quit
			case tea.KeyEscape:
				m.discardArmed = false
				return m, nil
			case tea.KeyEnter:
				m.discardRequested = true
				m.quitting = true
				return m, tea.Quit
			case tea.KeyRunes:
				if len(msg.Runes) == 1 {
					switch msg.Runes[0] {
					case 'y', 'Y':
						m.discardRequested = true
						m.quitting = true
						return m, tea.Quit
					case 'n', 'N':
						m.discardArmed = false
						return m, nil
					}
				}
			}
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEscape:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyTab:
			// Toggle inline <-> side-by-side from the keyboard. A single-sided file
			// has only the one view, so Tab is a no-op.
			if m.singleView {
				return m, nil
			}
			m.modeForced = true
			if m.mode == diffModeSideBySide {
				m.mode = diffModeInline
			} else {
				m.mode = diffModeSideBySide
			}
			_, _, cw, _ := m.layout()
			m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
			return m, nil
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				switch msg.Runes[0] {
				case 'q', 'Q':
					m.quitting = true
					return m, tea.Quit
				case 'd', 'D':
					// Arm the discard confirm (Yes/No). Nothing is discarded yet.
					m.discardArmed = true
					return m, nil
				case 'f', 'F':
					// Toggle changes-only <-> full file. A non-collapsible diff (a
					// whole-file add/delete, or a file with no far context) has only the
					// one view, so f is a no-op.
					if !m.collapsible {
						return m, nil
					}
					m.compact = !m.compact
					_, _, cw, _ := m.layout()
					m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
					return m, nil
				case 'g':
					m.viewport.GotoTop()
					return m, nil
				case 'G':
					m.viewport.GotoBottom()
					return m, nil
				}
			}
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// truncatePath shortens a file path to at most width display columns, keeping
// the tail (the basename is the most useful part) behind a leading ellipsis. A
// width below 1 yields a single ellipsis; a path that already fits is returned
// unchanged.
func truncatePath(p string, width int) string {
	rs := []rune(p)
	if len(rs) <= width {
		return p
	}
	if width <= 1 {
		return "…"
	}
	return "…" + string(rs[len(rs)-(width-1):])
}

// discardButtonSpan returns the [start, end) content columns the [ Discard ]
// button occupies, right-anchored to the content width cw. The button is the
// last bw columns of the row, so the hit-box is independent of the title length.
func discardButtonSpan(cw int) (start, end int) {
	bw := lipgloss.Width(diffDiscardLabel)
	return cw - bw, cw
}

// discardConfirmSpans returns the [start, end) content columns of the armed Yes
// and No confirm chips, right-anchored adjacent (Yes then a gap then No) so the
// rightmost chip ends at cw. Hit-boxes track the render exactly.
func discardConfirmSpans(cw int) (yesStart, yesEnd, noStart, noEnd int) {
	yw := lipgloss.Width(diffDiscardYes)
	nw := lipgloss.Width(diffDiscardNo)
	noEnd = cw
	noStart = noEnd - nw
	yesEnd = noStart - diffDiscardGap
	yesStart = yesEnd - yw
	return yesStart, yesEnd, noStart, noEnd
}

// discardControl renders the right-anchored discard region of the title row:
// the [ Discard ] button normally, or the [ Yes ] [ No ] confirm chips when
// armed. <avail> is the content width left of this region (it pads the controls
// flush to the right edge); a negative pad means the title already fills the row.
func (m DiffViewModel) discardControl(avail int) string {
	var ctrl string
	if m.discardArmed {
		ctrl = diffDiscardYesStyle.Render(diffDiscardYes) +
			strings.Repeat(" ", diffDiscardGap) + diffDiscardNoStyle.Render(diffDiscardNo)
	} else {
		ctrl = diffDiscardStyle.Render(diffDiscardLabel)
	}
	pad := avail
	if pad < 1 {
		pad = 1
	}
	return strings.Repeat(" ", pad) + ctrl
}

// statusBadge renders the file-status chip (e.g. " ADDED ") in the color that
// matches the kind of change; an unknown status falls back to the modified tint.
func (m DiffViewModel) statusBadge() string {
	color, ok := diffStatusColors[m.status]
	if !ok {
		color = diffStatusColors["modified"]
	}
	return diffStatusBadgeStyle.Background(color).Render(strings.ToUpper(m.status))
}

func (m DiffViewModel) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return ""
	}

	mh, mv, cw, ch := m.layout()
	// Top line: a status badge (added/deleted/modified), the file path, the
	// added/deleted line counts, and — right-anchored — the discard control. The
	// path is truncated (leading ellipsis, keeping the basename) so the row never
	// wraps: a wrapped title row would push the discard button onto a second
	// visual line, out of reach of its title-row click hit-box.
	badge := m.statusBadge()
	counts := diffAddStyle.Render("+"+itoa(m.added)) + " " + diffDelStyle.Render("−"+itoa(m.deleted))
	ctrlW := lipgloss.Width(diffDiscardLabel)
	if m.discardArmed {
		ctrlW = lipgloss.Width(diffDiscardYes) + diffDiscardGap + lipgloss.Width(diffDiscardNo)
	}
	// diffTitleStyle adds 1 column of padding each side (+2). Reserve a 1-column
	// gap before the right-anchored control too.
	pathBudget := cw - lipgloss.Width(badge) - lipgloss.Width(counts) - ctrlW - 2 - 1
	titleLeft := badge + diffTitleStyle.Render(truncatePath(m.title, pathBudget)) + counts
	title := titleLeft + m.discardControl(cw-lipgloss.Width(titleLeft)-ctrlW)
	rule := diffRuleStyle.Render(strings.Repeat("─", maxInt(cw, 0)))

	pct := int(m.viewport.ScrollPercent() * 100)
	// The bar carries Padding(0,1); the box wraps inner lines wider than cw, and a
	// wrapped bar would grow the box by a row and shift every row's screen
	// position (breaking the title/tab-row click math). Clip it to one line.
	barW := maxInt(cw-2, 0)
	var bar string
	if m.discardArmed {
		// Armed: the bar becomes the confirm prompt. Yes/No are clickable in the
		// title row; y/n/Esc drive it from the keyboard.
		bar = diffBarStyle.Render(fitColumn("Discard this file's changes? · y confirm · n/Esc cancel", barW))
	} else {
		hints := "↑↓/jk scroll · space/b page · g/G top·end"
		if !m.singleView { // modified file: the layout switcher is available
			hints += " · tab view"
		}
		if m.collapsible { // far context to collapse: the changes/full switcher is available
			if m.compact {
				hints += " · f full"
			} else {
				hints += " · f changes"
			}
		}
		hints += " · click-out/q/Esc close"
		bar = diffBarStyle.Render(fitColumn(hints+"    "+padPercent(pct), barW))
	}

	// A single-sided file (whole-file add/delete) shows no view switcher row. The
	// others put a blank row between the title and the controls so the header
	// reads as two distinct blocks, not one dense line.
	var rows []string
	if m.singleView {
		rows = []string{title, rule, m.viewport.View(), bar}
	} else {
		rows = []string{title, "", m.tabRow(), rule, m.viewport.View(), bar}
	}
	inner := strings.Join(rows, "\n")
	box := diffBoxStyle.Width(cw).Height(ch).Render(inner)

	// No backdrop: float the box on a blank surface.
	if len(m.backdrop) == 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	// Backdrop: composite the box over the dimmed snapshot. The box occupies
	// columns [mh, width-mh) and rows [mv, mv+boxRows); everything else shows the
	// dimmed screen behind. Slicing the PLAIN backdrop by rune index is safe; the
	// already-styled box lines are dropped in whole.
	return m.composite(box, mh, mv)
}

// composite overlays the rendered box onto the dimmed backdrop and returns the
// full-screen frame. The backdrop carries no ANSI, so its rows can be sliced by
// rune column; only the box (which spans the middle slot exactly) carries color.
func (m DiffViewModel) composite(box string, mh, mv int) string {
	boxLines := strings.Split(box, "\n")
	var b strings.Builder
	for y := 0; y < m.height; y++ {
		bg := m.bgRow(y)
		if y >= mv && y-mv < len(boxLines) {
			b.WriteString(diffDimStyle.Render(string(bg[:mh])))
			b.WriteString(boxLines[y-mv])
			b.WriteString(diffDimStyle.Render(string(bg[m.width-mh:])))
		} else {
			b.WriteString(diffDimStyle.Render(string(bg)))
		}
		if y < m.height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// bgRow returns backdrop row y as exactly m.width runes (space-padded / clipped),
// so it never depends on the captured width matching the live popup width.
func (m DiffViewModel) bgRow(y int) []rune {
	row := make([]rune, m.width)
	for i := range row {
		row[i] = ' '
	}
	if y < len(m.backdrop) {
		for i, r := range []rune(m.backdrop[y]) {
			if i >= m.width {
				break
			}
			row[i] = r
		}
	}
	return row
}

func padPercent(p int) string {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	s := "  "
	switch {
	case p >= 100:
		s = ""
	case p >= 10:
		s = " "
	}
	return s + itoa(p) + "%"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// itoa avoids pulling in strconv for a single small non-negative int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
