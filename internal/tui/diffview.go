package tui

import (
	"fmt"
	"image"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
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
	added       int
	deleted     int
	loading     bool
	loadErr     error
	diffReader  io.Reader
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
	// Image mode (see NewImageView): the body is a half-block truecolor preview
	// of img instead of a diff; imgErr holds the decode failure when img is nil.
	isImage bool
	img     image.Image
	imgErr  string
	imgPath string // on-disk location for "Open in Preview"; empty = hide the control
	// Kitty-graphics hi-res overlay (see WithKittyHires): the PNG is written to
	// kittyOut over the half-block cells so supported terminals show real
	// pixels. imgSrc keeps the original file bytes so an already-PNG source can
	// be transmitted verbatim without a re-encode (see kittyPayload).
	imgSrc      []byte
	kittyOut    io.Writer
	kittyTmux   bool
	kittySent   bool
	kittyArming bool         // transmit command in flight
	kittyShared *kittyShared // state shared with the transmit goroutine
}

type diffLoadedMsg struct {
	content string
	err     error
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
	// The open button names its destination app so it's obvious where the
	// image will appear (macOS Preview, launched explicitly with `open -a`).
	diffOpenLabel = "[ Open in Preview ]"
	diffOpenGap   = 1 // columns between the open button and the discard control
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
	forEachCell(code, func(text string, w int, isEsc bool) bool {
		if text == "\t" {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			return true
		}
		b.WriteString(text)
		col += w
		return true
	})
	return marker + b.String()
}

// forEachCell walks a possibly ANSI-colored string as the terminal lays it
// out: one callback per zero-width escape sequence, and one per GRAPHEME
// CLUSTER — not per rune — with the cells that cluster occupies. Return false
// to stop.
//
// Measuring per rune is the bug this exists to prevent. In Bengali (and Hindi,
// Thai, Arabic, …) a syllable is a base letter plus combining vowel signs and
// viramas: "ভি" is two codepoints painted in ONE cell. ZWJ emoji are the same
// shape — 👨‍👩‍👦 is five codepoints in two cells. Summing runewidth.RuneWidth
// over the runes counted those marks as cells of their own, so every column in
// the diff pager thought a Bengali line was nearly twice as wide as the
// terminal drew it: lines wrapped and truncated early, background bands stopped
// short of the column edge, and the side-by-side divider drifted off its
// column. Slicing by rune also let a truncation orphan a base letter from its
// vowel sign.
//
// Per-cluster widths come from clusterWidth, which is validated against a live
// tmux across the scripts of the world by TestCellWidth_matches_tmux.
// A segment's text may CONTAIN escape sequences, because color changes do not
// interrupt how the terminal composes characters: the syntax highlighter wraps
// every rune in its own SGR pair, so a Bengali letter and its vowel sign, or the
// halves of a ZWJ emoji, routinely arrive with an escape between them and are
// still painted in one cell. Segmenting each colored run separately counted
// those pieces twice over and left highlighted non-Latin rows short.
func forEachCell(s string, fn func(text string, w int, isEsc bool) bool) {
	if isASCII(s) {
		// Nothing in ASCII combines, so each byte is its own cell and none of
		// the segmenting below can change the answer. Most lines of most diffs
		// take this path; skipping the machinery keeps them as cheap as they
		// were before any of this existed.
		for i := 0; i < len(s); {
			if s[i] == '\x1b' {
				j := i
				for j < len(s) && s[j] != 'm' {
					j++
				}
				if j < len(s) {
					j++
				}
				if !fn(s[i:j], 0, true) {
					return
				}
				i = j
				continue
			}
			w := 0
			if s[i] >= 0x20 && s[i] != 0x7f {
				w = 1
			}
			if !fn(s[i:i+1], w, false) {
				return
			}
			i++
		}
		return
	}
	if strings.IndexByte(s, '\x1b') < 0 {
		segmentText(s, func(lo, hi, w int) bool { return fn(s[lo:hi], w, false) })
		return
	}
	plain := stripEscapes(s)
	if len(plain) == 0 { // nothing but escapes
		fn(s, 0, true)
		return
	}
	// Walk s and plain in lockstep: for every segment found in the stripped
	// text, hand back the original bytes it spans, escapes and all, so the
	// caller can copy them through unchanged.
	cur := 0
	skipEscapes := func() {
		for cur < len(s) && s[cur] == '\x1b' {
			for cur < len(s) && s[cur] != 'm' {
				cur++
			}
			if cur < len(s) {
				cur++
			}
		}
	}
	skipEscapes()
	if cur > 0 && !fn(s[:cur], 0, true) { // escapes before the first character
		return
	}
	segmentText(plain, func(lo, hi, w int) bool {
		start := cur
		for n := hi - lo; n > 0; n-- {
			skipEscapes()
			cur++
		}
		skipEscapes() // trailing escapes belong to the segment they follow
		return fn(s[start:cur], w, false)
	})
}

// segmentText walks escape-free text, reporting each segment as a byte range
// and the cells it occupies. Stops early if emit returns false.
func segmentText(plain string, emit func(lo, hi, w int) bool) {
	state := -1
	for i := 0; i < len(plain); {
		// A run of regional indicators (flag emoji) is one segment, because
		// that is how tmux stores it — see regionalIndicatorRun.
		if n, count := regionalIndicatorRun(plain[i:]); count > 0 {
			w := 2
			if count == 1 { // a lone indicator, not half of a flag
				w = 1
			}
			if !emit(i, i+n, w) {
				return
			}
			i += n
			state = -1
			continue
		}
		seg, next := nextSegment(plain[i:], state)
		if seg == "" {
			return
		}
		state = next
		if !emit(i, i+len(seg), clusterWidth(seg)) {
			return
		}
		i += len(seg)
	}
}

// stripEscapes returns s with its ESC…m sequences removed.
func stripEscapes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

const (
	zeroWidthJoiner   = '‍'
	softHyphen        = '­'
	variationSelect16 = '️'
)

// nextSegment returns the leading run of s that the terminal keeps in one cell,
// plus the segmenter state to continue with.
//
// It is a Unicode grapheme cluster, with one deliberate extension: tmux glues
// whatever follows a zero-width joiner onto the same cell — not just emoji, the
// way Unicode's GB11 says, but anything non-ASCII. That is how the Indic
// conjuncts written with ZWJ behave on its grid: Sinhala "ප්‍ර", Tamil "ஜ்‍ஞ"
// and Devanagari "क्‍ष" are one cell each, where the segmenter alone would say
// two. The non-ASCII condition is not arbitrary — tmux has an ASCII fast path
// that skips grapheme combining entirely, so "a‍b" really is two cells.
func nextSegment(s string, state int) (string, int) {
	seg, _, _, next := uniseg.FirstGraphemeClusterInString(s, state)
	if seg == "" {
		return "", state
	}
	for strings.HasSuffix(seg, string(zeroWidthJoiner)) && len(seg) < len(s) && s[len(seg)] >= utf8.RuneSelf {
		more, _, _, after := uniseg.FirstGraphemeClusterInString(s[len(seg):], next)
		if more == "" {
			break
		}
		seg, next = seg+more, after
	}
	return seg, next
}

// clusterWidth reports the cells one segment occupies, matching how tmux — which
// owns the cell grid the popup is painted into — accounts for it.
//
// The rule is "every mark hanging off a base character is free, and anything
// that still claims its own space is charged for". Marks, format controls and
// emoji skin-tone modifiers ride along for nothing, which is what makes
// Bengali "লো", Thai "ดี", Hebrew "שָׁ", a skin-toned 👍🏽 and a ZWJ 👨‍👩‍👦 each cost
// exactly what their base costs. Two cases still get charged even though the
// segmenter folded them into the cluster, because tmux gives them a cell of
// their own: Thai's spacing vowel SARA AM ("กำ" is two cells) and the halfwidth
// katakana voiced marks ("ｶﾞ" is two).
//
// Note this is NOT runewidth.StringWidth over the cluster, which bills every
// codepoint — that is how Myanmar came out three columns too wide. Nor is it
// the width of the base alone, which loses the two cases above.
//
// Variation selector 16 is the documented exception: it promotes a text-style
// glyph to emoji presentation and the terminal reserves two cells for it (❤ is
// one cell, ❤️ is two). VS15 does the reverse and is left to the base.
func clusterWidth(cluster string) int {
	if strings.ContainsRune(cluster, variationSelect16) {
		return 2
	}
	w := 0
	glued, prev := false, rune(-1)
	for _, r := range cluster {
		switch {
		case r == zeroWidthJoiner:
			glued = true // everything past here shares the cell it joined
		case glued:
		case unicode.Is(tmuxWide, r):
			w += 2
		case r == softHyphen:
			w++ // formally a format control, but the terminal prints it
		case prev >= 0 && isConjoiningJamo(r):
			// Free only when something precedes it in the same segment — i.e. it
			// is part of a written-out syllable. An orphan jamo is charged a
			// cell: tmux composes it onto whatever cell came before, which we
			// cannot see from here, so we round the safe way and reserve one.
		case isInvisible(r):
		case unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me):
			// Marks are free wherever they land — including as the FIRST rune of
			// a segment, which happens when the segmenter refuses to attach one
			// to its base: Unicode keeps Myanmar's ာ/ါ out of the SpacingMark
			// class, so they segment alone even though they are painted on the
			// preceding letter. tmux hangs them off it anyway.
		case r >= 0x1F3FB && r <= 0x1F3FF && prev >= 0x1F000 && prev <= 0x1FFFF:
			// A skin-tone modifier recolors the emoji in front of it, for free.
			// Only an emoji from the pictographic planes absorbs one, though:
			// tmux charges the modifier two cells of its own after a BMP glyph
			// like ☝ or ✌, after a plane-2 ideograph, and when it stands alone.
		default:
			if rw := runewidth.RuneWidth(r); rw > 0 {
				w += rw
			}
		}
		prev = r
	}
	return w
}

// isConjoiningJamo reports whether r is a Hangul medial vowel, final consonant
// or filler. Written out, "한" is a lead consonant plus these, and the terminal
// composes the whole syllable into the lead's two cells, so they cost nothing.
func isConjoiningJamo(r rune) bool {
	return (r >= 0x1160 && r <= 0x11FF) || // Hangul Jamo: medials and finals
		(r >= 0xD7B0 && r <= 0xD7C6) || // Extended-B medials
		(r >= 0xD7CB && r <= 0xD7FB) || // Extended-B finals
		r == 0x3164 // Hangul filler
}

// tmuxWide holds the codepoints tmux paints two cells wide where runewidth
// calls them narrow — mostly emoji whose East Asian Width is still "ambiguous"
// (☝ ✌ 🏋 🕵 🖐) and the divination-symbol blocks (☰ ䷀ 𝌆), plus a few marks it
// reserves space for. Both tables are defensible readings of Unicode; they just
// disagree, and tmux owns the grid.
//
// This list is not guesswork: it is every disagreement found by pushing all
// 154,996 assigned codepoints through a live tmux and diffing against
// cellWidth. Only the ones where tmux is WIDER are corrected here, because that
// is the direction that breaks a layout — writing a glyph the column cannot
// hold shoves the divider sideways. Where we read wider than tmux (a mark from
// a script newer than Go's Unicode tables), the column merely under-fills by a
// cell, so those are left alone rather than pinned to one tmux build.
var tmuxWide = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x261D, Hi: 0x261D, Stride: 1},
		{Lo: 0x2630, Hi: 0x2637, Stride: 1}, // trigrams
		{Lo: 0x268A, Hi: 0x268F, Stride: 1}, // monograms and digrams
		{Lo: 0x26F9, Hi: 0x26F9, Stride: 1},
		{Lo: 0x270C, Hi: 0x270D, Stride: 1},
		{Lo: 0x302E, Hi: 0x302F, Stride: 1}, // Hangul tone marks
		{Lo: 0x31E4, Hi: 0x31E5, Stride: 1}, // CJK strokes
		{Lo: 0x4DC0, Hi: 0x4DFF, Stride: 1}, // Yijing hexagrams
	},
	R32: []unicode.Range32{
		{Lo: 0x16FF0, Hi: 0x16FF1, Stride: 1}, // Vietnamese reading marks
		{Lo: 0x18CFF, Hi: 0x18CFF, Stride: 1},
		{Lo: 0x1D300, Hi: 0x1D356, Stride: 1}, // Tai Xuan Jing symbols
		{Lo: 0x1D360, Hi: 0x1D376, Stride: 1}, // counting rod numerals
		{Lo: 0x1F3CB, Hi: 0x1F3CC, Stride: 1},
		{Lo: 0x1F574, Hi: 0x1F575, Stride: 1},
		{Lo: 0x1F590, Hi: 0x1F590, Stride: 1},
		{Lo: 0x1FA89, Hi: 0x1FA89, Stride: 1},
		{Lo: 0x1FA8F, Hi: 0x1FA8F, Stride: 1},
		{Lo: 0x1FABE, Hi: 0x1FABE, Stride: 1},
		{Lo: 0x1FAC6, Hi: 0x1FAC6, Stride: 1},
		{Lo: 0x1FADC, Hi: 0x1FADC, Stride: 1},
		{Lo: 0x1FADF, Hi: 0x1FADF, Stride: 1},
		{Lo: 0x1FAE9, Hi: 0x1FAE9, Stride: 1},
	},
}

// isInvisible reports whether r takes no space of its own: the format controls
// — bidi isolates and overrides, joiners, tags — which steer how the text around
// them is laid out without occupying a cell themselves. The soft hyphen is a
// format control the terminal does print, and the caller charges for it before
// reaching here.
func isInvisible(r rune) bool {
	return unicode.In(r, unicode.Cf)
}

// regionalIndicatorRun returns the byte length and letter count of the run of
// regional-indicator letters at the start of s, or 0, 0 if there is none.
//
// Flags get their own segment because tmux does not pair the indicators up the
// way Unicode's GB12/GB13 say to: it packs a whole adjacent run into a single
// two-cell slot, so "🇺🇸🇯🇵🇩🇪" is two cells on its grid, not six. Standard
// pairing would leave the layout four cells adrift on a language-picker diff.
// Indicators separated by anything else are ordinary two-cell flags.
func regionalIndicatorRun(s string) (bytes, count int) {
	for bytes < len(s) {
		r, size := utf8.DecodeRuneInString(s[bytes:])
		if r < 0x1F1E6 || r > 0x1F1FF {
			break
		}
		bytes += size
		count++
	}
	return bytes, count
}

// cellWidth reports how many terminal cells s occupies, ANSI escapes excluded.
// It is the sum of forEachCell's per-cluster widths, and deliberately NOT
// runewidth.StringWidth(s) over the whole string: this repo's runewidth ships
// newer Unicode tables than tmux does, so for an Indic conjunct like "ক্ষ" it
// answers 1 where tmux draws 2. tmux owns the cell grid the popup is painted
// into, so per-cluster is the measurement that keeps the columns aligned.
// diffview_grapheme_test.go pins this against widths captured from a live tmux.
func cellWidth(s string) int {
	n := 0
	forEachCell(s, func(_ string, w int, _ bool) bool {
		n += w
		return true
	})
	return n
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
	// Open-in-Preview: a blue chip — actionable but safe, unlike the red Discard.
	diffOpenStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("4"))

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
	var b strings.Builder
	b.WriteString(bgSeq)
	vis := 0
	forEachCell(s, func(text string, w int, isEsc bool) bool {
		if isEsc {
			b.WriteString(text)
			return true
		}
		if vis+w > width { // a glyph that won't fit the remaining cells
			return false
		}
		b.WriteString(text)
		vis += w
		return vis < width
	})
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
		wrapped, wrappedW := wrapColumnsWidths(ln, codeW)
		for j, row := range wrapped {
			if j > 0 {
				b.WriteByte('\n')
				b.WriteString(blankGutter)
			} else {
				b.WriteString(gutter)
			}
			switch kind {
			case diffAdd:
				b.WriteString(tintTo(row, wrappedW[j], codeW, diffAddBgSeq))
			case diffDel:
				b.WriteString(tintTo(row, wrappedW[j], codeW, diffDelBgSeq))
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
	var b strings.Builder
	vis := 0
	forEachCell(s, func(text string, w int, isEsc bool) bool {
		if isEsc {
			b.WriteString(text)
			return true
		}
		if vis+w > width { // a glyph that won't fit the remaining cells
			return false
		}
		b.WriteString(text)
		vis += w
		return vis < width
	})
	b.WriteString("\x1b[0m")
	if vis < width {
		b.WriteString(strings.Repeat(" ", width-vis))
	}
	return b.String()
}

// padTo pads s — whose cell width is already known to be w — out to width
// cells, ending in a reset so color cannot bleed into the next column. It is
// fitColumn without the measuring pass; the caller guarantees w <= width.
func padTo(s string, w, width int) string {
	if w > width {
		return fitColumn(s, width)
	}
	return s + "\x1b[0m" + strings.Repeat(" ", width-w)
}

// tintTo is tintColumn for a string whose cell width is already known.
func tintTo(s string, w, width int, bgSeq string) string {
	if w > width {
		return tintColumn(s, width, bgSeq)
	}
	return bgSeq + s + strings.Repeat(" ", width-w) + "\x1b[0m"
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
	rows, _ := wrapColumnsWidths(s, width)
	return rows
}

// wrapColumnsWidths is wrapColumns plus the cell width of each row it produced.
// Handing those widths on saves the caller from segmenting the same text a
// second time just to pad it, which on a file written in a non-Latin script is
// the difference between measuring every line once and measuring it twice.
func wrapColumnsWidths(s string, width int) ([]string, []int) {
	if width < 1 {
		width = 1
	}
	var rows []string
	var widths []int
	var b strings.Builder
	vis := 0
	active := ""
	flush := func() {
		rows = append(rows, b.String())
		widths = append(widths, vis)
		b.Reset()
		vis = 0
	}
	// Track the last color opened, wherever it turns up: escapes arrive on their
	// own, and also embedded in a segment, since the highlighter colors each
	// rune separately and a letter can be joined to its accent across one.
	track := func(text string) {
		if strings.IndexByte(text, '\x1b') < 0 {
			return
		}
		for _, seq := range diffAnsiSeq.FindAllString(text, -1) {
			if ansiIsReset(seq) {
				active = ""
			} else {
				active = seq
			}
		}
	}
	forEachCell(s, func(text string, w int, isEsc bool) bool {
		if isEsc {
			track(text)
			b.WriteString(text)
			return true
		}
		if vis > 0 && vis+w > width { // wrap before a glyph that would overflow this row
			flush()
			if active != "" {
				b.WriteString(active)
			}
		}
		track(text)
		if w > width {
			// Wider than the whole column, so no amount of wrapping will seat
			// it — a double-width CJK glyph or an emoji in a one-column view.
			// Stand an ellipsis in for it rather than let it overhang the
			// column and shove everything to its right out of alignment.
			b.WriteString("…")
			vis += 1
			return true
		}
		b.WriteString(text)
		vis += w
		return true
	})
	rows = append(rows, b.String())
	widths = append(widths, vis)
	return rows, widths
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
	no    int
	text  string
	width int // cells text occupies, carried from wrapColumnsWidths
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
		lRows, lWidths := wrapColumnsWidths(l.text, textW)
		rRows, rWidths := wrapColumnsWidths(r.text, textW)
		n := maxInt(len(lRows), len(rRows))
		for i := 0; i < n; i++ {
			var lc, rc sbsCell
			lBg, rBg := "", ""
			if i < len(lRows) {
				no := 0
				if i == 0 {
					no = l.no
				}
				lc, lBg = sbsCell{no, lRows[i], lWidths[i]}, lbg
			}
			if i < len(rRows) {
				no := 0
				if i == 0 {
					no = r.no
				}
				rc, rBg = sbsCell{no, rRows[i], rWidths[i]}, rbg
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
			emit(sbsCell{no: oldNo, text: text}, sbsCell{no: newNo, text: text}, "", "")
		case diffDel:
			oldNo++
			dels = append(dels, sbsCell{no: oldNo, text: text})
		case diffAdd:
			newNo++
			adds = append(adds, sbsCell{no: newNo, text: text})
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
		return gutter + padTo(c.text, c.width, textW)
	}
	return gutter + tintTo(c.text, c.width, textW, bgSeq)
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
	m := NewLoadingDiffView(title)
	m.installDiff(content)
	return m
}

// NewLoadingDiffView creates a text-diff popup that can paint its chrome before
// the piped diff has reached EOF.
func NewLoadingDiffView(title string) DiffViewModel {
	return DiffViewModel{
		title:      title,
		status:     "modified",
		singleView: true,
		compact:    true,
		hoverMode:  -1,
		hoverCtx:   -1,
		loading:    true,
	}
}

// WithDiffReader attaches the text stream that Init will load asynchronously.
func (m DiffViewModel) WithDiffReader(reader io.Reader) DiffViewModel {
	m.diffReader = reader
	return m
}

// LoadDiff returns a Tea command that owns the blocking stream read.
func (m DiffViewModel) LoadDiff(reader io.Reader) tea.Cmd {
	return func() tea.Msg {
		content, err := io.ReadAll(reader)
		return diffLoadedMsg{content: string(content), err: err}
	}
}

func (m *DiffViewModel) installDiff(content string) {
	added, deleted := countDiffLines(content)
	single := isSingleSided(content)
	// Expand tabs before highlighting so no tab reaches the column layout; the
	// raw content is kept for the line counts/status (tabs don't affect those).
	expanded := expandTabs(content, diffTabWidth)
	m.content = content
	m.highlighted = highlightDiff(expanded, m.title)
	m.added = added
	m.deleted = deleted
	m.status = diffStatus(content)
	m.singleView = single
	m.compact = true
	m.collapsible = !single && hasCollapsibleContext(content, diffContextLines)
	m.hoverMode = -1
	m.hoverCtx = -1
	m.loading = false
	m.loadErr = nil
	m.diffReader = nil
}

// bodyContent is the diff body to render: collapsed (changes-only) when the view
// is compact and there's context worth hiding, otherwise the full file.
func (m DiffViewModel) bodyContent() string {
	if m.loading {
		return diffGutterStyle.Render(" loading diff…")
	}
	if m.loadErr != nil {
		return diffDelStyle.Render(" could not load diff · " + m.loadErr.Error())
	}
	// An empty text diff (binary change with no textual hunks, or an upstream
	// pipeline that produced nothing) used to render as a silent blank box —
	// indistinguishable from the popup not opening. Say what happened instead.
	if !m.isImage && strings.TrimSpace(m.content) == "" {
		return diffGutterStyle.Render(" no textual changes to show — binary file or empty diff")
	}
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
	if m.loading && m.diffReader != nil {
		return m.LoadDiff(m.diffReader)
	}
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

// safeRender runs a render function and converts a panic into the fallback
// screen. The pager is the last stop before the user's eyes: a panic here used
// to kill the process before first paint, so the popup silently never opened
// (a fixed-width itoa buffer did exactly that for every lockfile-sized diff).
// Degrading to an error screen keeps the popup visible and closable no matter
// what a future rendering bug does.
func safeRender(render func() string, fallback func(any) string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fallback(r)
		}
	}()
	return render()
}

// safeUpdate runs an update step and converts a panic into "nothing happened":
// the prior model is returned unchanged, so the popup stays open and quit keys
// keep working instead of the whole pager dying mid-session.
func safeUpdate(update func() (tea.Model, tea.Cmd), prior tea.Model) (model tea.Model, cmd tea.Cmd) {
	defer func() {
		if r := recover(); r != nil {
			model, cmd = prior, nil
		}
	}()
	return update()
}

// renderPanicScreen is the render-panic fallback: plain unstyled text (nothing
// here may panic again) naming the file, the failure, and the way out.
func (m DiffViewModel) renderPanicScreen(r any) string {
	return fmt.Sprintf("%s\n\npreview failed to render: %v\n\npress q or Esc to close", m.title, r)
}

// Update guards the real update step: a panic leaves the model unchanged
// instead of killing the popup.
func (m DiffViewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return safeUpdate(func() (tea.Model, tea.Cmd) { return m.update(msg) }, m)
}

func (m DiffViewModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case diffLoadedMsg:
		if msg.err != nil {
			m.loading = false
			m.loadErr = msg.err
			m.diffReader = nil
		} else {
			m.installDiff(msg.content)
		}
		if m.ready {
			_, _, cw, ch := m.layout()
			h := ch - m.headerHeight() - diffFooterHeight
			if h < 1 {
				h = 1
			}
			m.viewport.Width = cw
			m.viewport.Height = h
			if m.singleView {
				m.mode = diffModeInline
			} else if !m.modeForced {
				m.mode = pickByWidth(cw)
			}
			m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
		}
		return m, nil

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
		if m.isImage {
			m.viewport.SetContent(m.renderImageBody(cw, h))
			// Hi-res overlay: the first size kicks the async encode+transmit (off
			// the event loop, so this paint isn't blocked; and inside the alt
			// screen, where the placement must live). Later sizes re-place the
			// already-transmitted image; a resize while the transmit is in flight
			// is reconciled by the kittySentMsg handler.
			if m.kittyOut != nil && m.img != nil && !m.kittySent && !m.kittyArming {
				if cmd := m.kittyTransmitCmd(); cmd != nil {
					m.kittyArming = true
					return m, cmd
				}
			}
			if m.kittySent {
				m.rePlaceKitty()
			}
		} else {
			m.viewport.SetContent(renderBodyMode(m.bodyContent(), cw, m.mode))
		}
		return m, nil

	case kittySentMsg:
		// The async transmit finished. If the encode failed, stand down; if the
		// popup resized while the transmit was in flight, the image sits at the
		// stale rectangle — move it to the current one.
		m.kittyArming = false
		if !msg.ok {
			m.kittyOut = nil
			return m, nil
		}
		m.kittySent = true
		if msg.w != m.width || msg.h != m.height {
			m.rePlaceKitty()
		}
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
					if m.showOpenButton() {
						os, oe := openButtonSpan(cw)
						if cx >= os && cx < oe {
							_ = openInPreview(m.imgPath)
							return m, nil
						}
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
				case 'o', 'O':
					// Open the image in the macOS Preview app; the popup stays up.
					if m.isImage && m.imgPath != "" {
						_ = openInPreview(m.imgPath)
					}
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
	if cellWidth(p) <= width {
		return p
	}
	if width <= 1 {
		return "…"
	}
	// Keep the widest tail that fits beside the ellipsis, walking whole
	// grapheme clusters so a directory name in a combining script never loses
	// its vowel signs (see forEachCell).
	type cell struct {
		off, w int
	}
	var cells []cell
	off := 0
	forEachCell(p, func(text string, w int, isEsc bool) bool {
		cells = append(cells, cell{off, w})
		off += len(text)
		return true
	})
	budget := width - 1 // the ellipsis takes one cell
	start := len(p)
	for i := len(cells) - 1; i >= 0; i-- {
		if budget-cells[i].w < 0 {
			break
		}
		budget -= cells[i].w
		start = cells[i].off
	}
	return "…" + p[start:]
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

// showOpenButton reports whether the title row carries the [ Open in Preview ]
// button: image mode with a known on-disk path, and the discard confirm not
// armed (while armed the title row belongs to the Yes/No chips).
func (m DiffViewModel) showOpenButton() bool {
	return m.isImage && m.imgPath != "" && !m.discardArmed
}

// openButtonSpan returns the [start, end) content columns of the
// [ Open in Preview ] button, right-anchored just left of the discard button.
func openButtonSpan(cw int) (start, end int) {
	ds, _ := discardButtonSpan(cw)
	end = ds - diffOpenGap
	return end - lipgloss.Width(diffOpenLabel), end
}

// discardControl renders the right-anchored control region of the title row:
// the [ Open in Preview ] (image mode) and [ Discard ] buttons normally, or
// the [ Yes ] [ No ] confirm chips when armed. <avail> is the content width
// left of this region (it pads the controls flush to the right edge); a
// negative pad means the title already fills the row.
func (m DiffViewModel) discardControl(avail int) string {
	var ctrl string
	if m.discardArmed {
		ctrl = diffDiscardYesStyle.Render(diffDiscardYes) +
			strings.Repeat(" ", diffDiscardGap) + diffDiscardNoStyle.Render(diffDiscardNo)
	} else {
		if m.showOpenButton() {
			ctrl = diffOpenStyle.Render(diffOpenLabel) + strings.Repeat(" ", diffOpenGap)
		}
		ctrl += diffDiscardStyle.Render(diffDiscardLabel)
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

// View guards the real renderer: a panic degrades to renderPanicScreen so the
// popup always paints something closable.
func (m DiffViewModel) View() string {
	return safeRender(m.render, m.renderPanicScreen)
}

func (m DiffViewModel) render() string {
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
	// An image has no line counts; its header caption is the pixel size instead.
	counts := diffAddStyle.Render("+"+itoa(m.added)) + " " + diffDelStyle.Render("−"+itoa(m.deleted))
	if m.isImage {
		counts = diffGutterStyle.Render(m.imageDims())
	}
	ctrlW := lipgloss.Width(diffDiscardLabel)
	if m.discardArmed {
		ctrlW = lipgloss.Width(diffDiscardYes) + diffDiscardGap + lipgloss.Width(diffDiscardNo)
	} else if m.showOpenButton() {
		ctrlW += lipgloss.Width(diffOpenLabel) + diffOpenGap
	}
	// diffTitleStyle adds 1 column of padding each side (+2). Reserve a 1-column
	// gap before the right-anchored control too.
	pathBudget := cw - lipgloss.Width(badge) - lipgloss.Width(counts) - ctrlW - 2 - 1
	titleLeft := badge + diffTitleStyle.Render(truncatePath(m.title, pathBudget)) + counts
	// cellWidth, not lipgloss.Width: titleLeft carries the file path, and a path
	// with a non-Latin directory in it measures differently under the two (see
	// forEachCell). Getting this wrong pushes the discard button off its row.
	title := titleLeft + m.discardControl(cw-cellWidth(titleLeft)-ctrlW)
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
		if m.showOpenButton() { // image with a known path: o opens it in Preview
			hints += " · o Preview"
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
	box := framePopup(inner, cw, ch)

	// No backdrop: float the box on a blank surface.
	if len(m.backdrop) == 0 {
		return placeBox(box, m.width, m.height)
	}
	// Backdrop: composite the box over the dimmed snapshot. The box occupies
	// columns [mh, width-mh) and rows [mv, mv+boxRows); everything else shows the
	// dimmed screen behind. Slicing the PLAIN backdrop by rune index is safe; the
	// already-styled box lines are dropped in whole.
	return m.composite(box, mh, mv)
}

// framePopup draws the popup's rounded border around inner: every content row
// fitted to exactly cw cells, padded out to ch rows.
//
// This is deliberately not lipgloss's Width()+Border(), which is what it
// replaced. lipgloss pads each line to width using its OWN width table, and
// that table disagrees with the terminal's for the same scripts everything else
// here had to be taught about — it reads the Bengali "র্ত" one cell narrower
// than tmux paints it. So it padded rows that were already exactly cw, and a
// Bengali diff came out with 122- and 124-cell rows inside a 120-cell box: the
// right border and the side-by-side divider walked off their columns, row by
// row. Measuring the rows ourselves with fitColumn is the only way to keep them
// honest, because no amount of pre-padding survives a second, wrong pass.
func framePopup(inner string, cw, ch int) string {
	border := lipgloss.RoundedBorder()
	edge := lipgloss.NewStyle().Foreground(diffBoxStyle.GetBorderTopForeground())
	side := edge.Render(border.Left)
	rule := strings.Repeat(border.Top, maxInt(cw, 0))

	lines := strings.Split(inner, "\n")
	for len(lines) < ch {
		lines = append(lines, "")
	}
	var b strings.Builder
	b.WriteString(edge.Render(border.TopLeft + rule + border.TopRight))
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(side)
		b.WriteString(fitColumn(line, cw))
		b.WriteString(side)
	}
	b.WriteByte('\n')
	b.WriteString(edge.Render(border.BottomLeft + rule + border.BottomRight))
	return b.String()
}

// placeBox centers box on a blank width×height frame. lipgloss.Place would do
// this, but it pads each line according to the same width table that made
// framePopup necessary — so a box row carrying non-Latin text got a cell too
// much padding and the popup's right edge came out ragged.
func placeBox(box string, width, height int) string {
	lines := strings.Split(box, "\n")
	boxW := 0
	for _, line := range lines {
		if w := cellWidth(line); w > boxW {
			boxW = w
		}
	}
	left := maxInt((width-boxW)/2, 0)
	top := maxInt((height-len(lines))/2, 0)

	var b strings.Builder
	for y := 0; y < height; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		if y < top || y-top >= len(lines) {
			b.WriteString(strings.Repeat(" ", maxInt(width, 0)))
			continue
		}
		line := lines[y-top]
		b.WriteString(strings.Repeat(" ", left))
		b.WriteString(line)
		b.WriteString(strings.Repeat(" ", maxInt(width-left-cellWidth(line), 0)))
	}
	return b.String()
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

// itoa formats a count for the pager's chrome. Counts can be huge — a gap
// sentinel in a lockfile-sized diff hides tens of thousands of lines — so this
// must never assume a digit budget (a fixed 4-byte buffer here once panicked
// the pager on any count ≥ 10000, killing the popup before it painted).
func itoa(n int) string { return strconv.Itoa(n) }

// isASCII reports whether s is entirely single-byte characters.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
