package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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
