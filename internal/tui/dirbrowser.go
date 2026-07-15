package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/util"
)

// ChooseThisFolderRow is the pinned first row of the browser list: selecting
// it picks the current directory itself as the project folder.
const ChooseThisFolderRow = "⏎ choose this folder"

// DirBrowserModel is the state of the add-project folder browser: a current
// directory, its subdirectory listing, a typed filter, and a highlight over
// the visible rows (row 0 is always ChooseThisFolderRow).
type DirBrowserModel struct {
	cwd      string
	entries  []string // all subdir names of cwd (dotdirs included), sorted
	filter   string
	selected int
	errMsg   string
}

// NewDirBrowser creates a browser rooted at startDir (~ expanded, cleaned).
func NewDirBrowser(startDir string) DirBrowserModel {
	b := DirBrowserModel{cwd: filepath.Clean(util.ExpandPath(startDir))}
	b.entries, b.errMsg = readSubdirs(b.cwd)
	return b
}

// readSubdirs lists the subdirectory names of dir, sorted. Dotdirs are kept in
// the pool; visibility filtering happens in VisibleRows.
func readSubdirs(dir string) ([]string, string) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err.Error()
	}
	var names []string
	for _, e := range dirents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, ""
}

// Cwd returns the current directory.
func (b *DirBrowserModel) Cwd() string { return b.cwd }

// Filter returns the typed filter text.
func (b *DirBrowserModel) Filter() string { return b.filter }

// Err returns the last navigation error, or "".
func (b *DirBrowserModel) Err() string { return b.errMsg }

// Selected returns the highlight index into VisibleRows.
func (b *DirBrowserModel) Selected() int { return b.selected }

// VisibleRows returns the pinned choose-this-folder row followed by the
// subdirectories matching the filter. Dotdirs are hidden unless the filter
// itself starts with ".".
func (b *DirBrowserModel) VisibleRows() []string {
	rows := []string{ChooseThisFolderRow}
	showDot := strings.HasPrefix(b.filter, ".")
	needle := strings.ToLower(b.filter)
	for _, name := range b.entries {
		if strings.HasPrefix(name, ".") && !showDot {
			continue
		}
		if needle == "" || strings.Contains(strings.ToLower(name), needle) {
			rows = append(rows, name)
		}
	}
	return rows
}

// MoveUp moves the highlight up, clamping at the top.
func (b *DirBrowserModel) MoveUp() {
	if b.selected > 0 {
		b.selected--
	}
}

// MoveDown moves the highlight down, clamping at the last visible row.
func (b *DirBrowserModel) MoveDown() {
	if b.selected < len(b.VisibleRows())-1 {
		b.selected++
	}
}

// Descend enters the highlighted subdirectory, resetting the filter and
// highlight. Returns false on the choose-this-folder row. An unreadable
// target sets Err and stays put.
func (b *DirBrowserModel) Descend() bool {
	rows := b.VisibleRows()
	if b.selected <= 0 || b.selected >= len(rows) {
		return false
	}
	target := filepath.Join(b.cwd, rows[b.selected])
	entries, errMsg := readSubdirs(target)
	if errMsg != "" {
		b.errMsg = errMsg
		return false
	}
	b.cwd = target
	b.entries = entries
	b.filter = ""
	b.selected = 0
	b.errMsg = ""
	return true
}

// GoUp moves to the parent directory (stopping at the filesystem root),
// resetting the filter and highlight.
func (b *DirBrowserModel) GoUp() {
	parent := filepath.Dir(b.cwd)
	if parent == b.cwd {
		return
	}
	entries, errMsg := readSubdirs(parent)
	if errMsg != "" {
		b.errMsg = errMsg
		return
	}
	b.cwd = parent
	b.entries = entries
	b.filter = ""
	b.selected = 0
	b.errMsg = ""
}

// ChooseHighlighted returns the highlighted folder's absolute path: the cwd
// for the pinned row, otherwise the highlighted subdirectory.
func (b *DirBrowserModel) ChooseHighlighted() (string, bool) {
	rows := b.VisibleRows()
	if b.selected < 0 || b.selected >= len(rows) {
		return "", false
	}
	if b.selected == 0 {
		return b.cwd, true
	}
	return filepath.Join(b.cwd, rows[b.selected]), true
}

// TypeRune appends to the filter and highlights the first match (row 1) when
// one exists, else the pinned row.
func (b *DirBrowserModel) TypeRune(r rune) {
	b.filter += string(r)
	b.resetHighlight()
}

// BackspaceFilter deletes the last filter rune; returns false when the filter
// is already empty (the caller then treats Backspace as "go up").
func (b *DirBrowserModel) BackspaceFilter() bool {
	if b.filter == "" {
		return false
	}
	runes := []rune(b.filter)
	b.filter = string(runes[:len(runes)-1])
	b.resetHighlight()
	return true
}

// ClearFilter empties the filter, keeping the highlight in range.
func (b *DirBrowserModel) ClearFilter() {
	b.filter = ""
	b.resetHighlight()
}

func (b *DirBrowserModel) resetHighlight() {
	b.errMsg = ""
	if len(b.VisibleRows()) > 1 {
		b.selected = 1
	} else {
		b.selected = 0
	}
}

// GitHubURL reports whether the filter text parses as a GitHub repo URL,
// returning the normalized clone URL.
func (b *DirBrowserModel) GitHubURL() (string, bool) {
	cloneURL, _, ok := util.ParseGitHubRepo(strings.TrimSpace(b.filter))
	return cloneURL, ok
}

// Browser card geometry. The list window is fixed-height so the card never
// jumps while navigating or filtering.
const (
	browserMaxVisible     = 10
	browserCardInnerWidth = 46
)

// browserInnerLines is the card's content block: cwd, filter line, the
// fixed-height folder list (or the GitHub clone hint), a status slot, and a
// dim help footer hugging the bottom border — mirroring the About card shape.
// Styles are built per render, never in package vars (pre-tty renderer trap).
func (m *MainMenuModel) browserInnerLines() []string {
	b := m.browser
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	primaryStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	accentStyle := lipgloss.NewStyle().Foreground(m.theme.Accent)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	lines := []string{
		primaryStyle.Render(TruncateMiddle(abbreviateHome(b.Cwd()), browserCardInnerWidth)),
	}
	if b.Filter() == "" {
		lines = append(lines, dimStyle.Render("Filter: type to filter · paste a GitHub URL"))
	} else {
		lines = append(lines, dimStyle.Render("Filter: ")+TruncateMiddle(b.Filter(), browserCardInnerWidth-9)+"▌")
	}
	lines = append(lines, "")

	if cloneURL, ok := b.GitHubURL(); ok {
		list := make([]string, browserMaxVisible)
		list[0] = accentStyle.Render(TruncateMiddle("⬡ "+repoSlug(cloneURL)+" — press ⏎ to clone", browserCardInnerWidth))
		lines = append(lines, list...)
	} else {
		lines = append(lines, m.browserListLines()...)
	}

	// Status slot: navigation error or blank, always one line.
	if b.Err() != "" {
		lines = append(lines, errorStyle.Render(TruncateMiddle("✗ "+b.Err(), browserCardInnerWidth)))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, "", dimStyle.Render("⏎ open · tab choose · ← up · esc cancel"))
	return lines
}

// browserListLines renders the fixed-height scroll window over VisibleRows,
// keeping the highlighted row inside the window.
func (m *MainMenuModel) browserListLines() []string {
	b := m.browser
	rows := b.VisibleRows()
	start := 0
	if b.Selected() >= browserMaxVisible {
		start = b.Selected() - browserMaxVisible + 1
	}
	selectedStyle := lipgloss.NewStyle().Reverse(true)
	list := make([]string, browserMaxVisible)
	for i := 0; i < browserMaxVisible; i++ {
		idx := start + i
		if idx >= len(rows) {
			continue
		}
		label := rows[idx]
		if idx > 0 {
			label += "/"
		}
		label = TruncateMiddle(label, browserCardInnerWidth-2)
		if idx == b.Selected() {
			list[i] = selectedStyle.Render("▸ " + label)
		} else {
			list[i] = "  " + label
		}
	}
	return list
}

// renderBrowserCard draws the floating folder-browser card with the same
// chrome as the About / account-switch modals.
func (m *MainMenuModel) renderBrowserCard() string {
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(aboutCardPadY, aboutCardPadX, 0, aboutCardPadX).
		Width(browserCardInnerWidth + 2*aboutCardPadX).
		Render(strings.Join(m.browserInnerLines(), "\n"))
	return embedAboutBorderTitle(card, "Add Project — choose folder", browserCardInnerWidth+2*aboutCardPadX)
}

// browserCardLayout returns the centered card's absolute screen geometry.
func (m *MainMenuModel) browserCardLayout() (left, top, w, h int) {
	w = browserCardInnerWidth + 2*aboutCardPadX + 2
	h = len(m.browserInnerLines()) + aboutCardPadY + 2
	left = (m.width - w) / 2
	if left < 0 {
		left = 0
	}
	top = (m.height - h) / 2
	if top < 0 {
		top = 0
	}
	return left, top, w, h
}

// overlayBrowser composites the folder-browser card over the placed screen.
func (m *MainMenuModel) overlayBrowser(placed string) string {
	left, top, w, _ := m.browserCardLayout()
	return m.overlayCard(placed, strings.Split(m.renderBrowserCard(), "\n"), left, top, w)
}

// handleBrowserMouse owns all mouse input while the browser overlay is open:
// a left click outside the card dismisses it (mirroring the About card);
// everything else is swallowed so nothing falls through to the menu beneath.
func (m *MainMenuModel) handleBrowserMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		left, top, w, h := m.browserCardLayout()
		onCard := msg.X >= left && msg.X < left+w && msg.Y >= top && msg.Y < top+h
		if !onCard {
			m.exitInputMode()
		}
	}
	return m, nil
}

// updateBrowser owns all key input while the folder browser is open.
func (m *MainMenuModel) updateBrowser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := m.browser
	switch msg.Type {
	case tea.KeyCtrlC:
		m.exitInputMode()
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyEsc:
		if b.Filter() != "" {
			b.ClearFilter()
			return m, nil
		}
		m.exitInputMode()
		return m, nil
	case tea.KeyUp:
		b.MoveUp()
		return m, nil
	case tea.KeyDown:
		b.MoveDown()
		return m, nil
	case tea.KeyRight:
		b.Descend()
		return m, nil
	case tea.KeyLeft:
		b.GoUp()
		return m, nil
	case tea.KeyBackspace:
		if !b.BackspaceFilter() {
			b.GoUp()
		}
		return m, nil
	case tea.KeyTab:
		if path, ok := b.ChooseHighlighted(); ok {
			return m.chooseBrowserFolder(path)
		}
		return m, nil
	case tea.KeyEnter:
		if _, ok := b.GitHubURL(); ok {
			return m.chooseBrowserGitHubURL(strings.TrimSpace(b.Filter()))
		}
		if b.Selected() == 0 {
			if path, ok := b.ChooseHighlighted(); ok {
				return m.chooseBrowserFolder(path)
			}
			return m, nil
		}
		b.Descend()
		return m, nil
	case tea.KeySpace:
		b.TypeRune(' ')
		return m, nil
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			b.TypeRune(r)
		}
		return m, nil
	}
	return m, nil
}

// chooseBrowserFolder closes the browser and hands the chosen folder to the
// existing name stage (auto-derive, duplicate checks, persistence).
func (m *MainMenuModel) chooseBrowserFolder(path string) (tea.Model, tea.Cmd) {
	m.browser = nil
	m.pathInput.SetValue(path)
	return m.advanceToNameField()
}

// chooseBrowserGitHubURL closes the browser with the filter's GitHub URL as
// the path, entering the name stage that leads to the clone flow.
func (m *MainMenuModel) chooseBrowserGitHubURL(url string) (tea.Model, tea.Cmd) {
	m.browser = nil
	m.pathInput.SetValue(url)
	return m.advanceToNameField()
}

// reopenBrowserFromName returns from the name stage to the browser: at the
// chosen folder's parent, or with a GitHub URL back in the filter.
func (m *MainMenuModel) reopenBrowserFromName() {
	val := strings.TrimSpace(m.pathInput.Value())
	var b DirBrowserModel
	if _, _, isGitHub := util.ParseGitHubRepo(val); isGitHub || val == "" {
		b = NewDirBrowser(m.browserStartDir())
		for _, r := range val {
			b.TypeRune(r)
		}
	} else {
		b = NewDirBrowser(filepath.Dir(filepath.Clean(util.ExpandPath(val))))
	}
	m.browser = &b
	m.nameInput.Blur()
	m.inputFocusPath = true
}
