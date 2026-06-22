package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DiffViewModel is a scrollable pager for a (pre-colored) git diff, shown inside
// the click-to-open popup. Unlike less, it closes on a single Esc press because
// Bubbletea's input parser emits a distinct KeyEscape for a lone Esc and parses
// arrow-key escape sequences separately. q and ctrl+c also quit. The viewport
// bubble handles scrolling (↑↓/j/k, space/b page, u/d half-page, mouse wheel);
// g/G jump to the top/bottom. ANSI color in the content is preserved.
type DiffViewModel struct {
	title    string
	content  string
	added    int
	deleted  int
	width    int // full popup (window) size; the box floats centered within it
	height   int
	backdrop []string // dimmed screen snapshot shown behind the box (may be nil)
	viewport viewport.Model
	ready    bool
	quitting bool
}

var (
	diffTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("208")). // orange, matching the popup border
			Padding(0, 1)

	diffAddStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	diffDelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red

	diffRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))

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
// trailing empty line (from a final newline) gets no gutter.
func numberLines(content string) string {
	lines := strings.Split(content, "\n")
	maxNo := 0
	for _, ln := range lines {
		if ln != "" && !isRemovedLine(ln) {
			maxNo++
		}
	}
	width := len(itoa(maxNo))
	if width < 1 {
		width = 1
	}
	var b strings.Builder
	n := 0
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if ln == "" {
			continue
		}
		var num string
		if isRemovedLine(ln) {
			num = strings.Repeat(" ", width)
		} else {
			n++
			num = fmt.Sprintf("%*d", width, n)
		}
		b.WriteString(diffGutterStyle.Render(num + " │ "))
		b.WriteString(ln)
	}
	return b.String()
}

// countDiffLines tallies the added (+) and deleted (-) lines of the diff body.
// The body is pre-colored (git --color=always) and the +++/--- file markers are
// stripped upstream, so after dropping the ANSI escapes a leading +/- is an
// authoritative add/delete marker; context lines (leading space) are ignored.
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

// NewDiffView builds the pager for the given title (the file path, shown in the
// header) and content (the colored diff body). The added/deleted line counts
// shown in the header are derived from the content.
func NewDiffView(title, content string) DiffViewModel {
	added, deleted := countDiffLines(content)
	return DiffViewModel{title: title, content: content, added: added, deleted: deleted}
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
// scrolling viewport: a title line + a rule, and a single control bar.
const (
	diffHeaderHeight = 2
	diffFooterHeight = 1
)

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
		h := ch - diffHeaderHeight - diffFooterHeight
		if h < 1 {
			h = 1
		}
		if !m.ready {
			m.viewport = viewport.New(cw, h)
			m.viewport.SetContent(numberLines(m.content))
			m.ready = true
		} else {
			m.viewport.Width = cw
			m.viewport.Height = h
		}
		return m, nil

	case tea.MouseMsg:
		// Click outside the floating box (in the margin) closes the popup. Inside
		// clicks fall through to the viewport (mouse-wheel scrolling).
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			mh, mv, _, _ := m.layout()
			if msg.X < mh || msg.X >= m.width-mh || msg.Y < mv || msg.Y >= m.height-mv {
				m.quitting = true
				return m, tea.Quit
			}
		}

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEscape:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				switch msg.Runes[0] {
				case 'q', 'Q':
					m.quitting = true
					return m, tea.Quit
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

func (m DiffViewModel) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return ""
	}

	mh, mv, cw, ch := m.layout()
	// Top line: ONLY the file path and the added/deleted line counts.
	title := diffTitleStyle.Render(m.title) +
		diffAddStyle.Render("+"+itoa(m.added)) + " " +
		diffDelStyle.Render("−"+itoa(m.deleted))
	rule := diffRuleStyle.Render(strings.Repeat("─", maxInt(cw, 0)))

	pct := int(m.viewport.ScrollPercent() * 100)
	hints := "↑↓/jk scroll · space/b page · g/G top·end · click-out/q/Esc close"
	bar := diffBarStyle.Render(hints + "    " + padPercent(pct))

	inner := strings.Join([]string{title, rule, m.viewport.View(), bar}, "\n")
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
