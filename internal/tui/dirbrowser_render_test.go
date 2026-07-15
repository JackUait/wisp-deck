package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newBrowserMenu builds a menu whose folder browser is open on a temp tree
// with subdirs alpha..delta.
func newBrowserMenu(t *testing.T, subdirs ...string) (*MainMenuModel, string) {
	t.Helper()
	m := newTestMenu()
	m.width, m.height = 100, 40
	dir := t.TempDir()
	if len(subdirs) == 0 {
		subdirs = []string{"alpha", "beta"}
	}
	for _, d := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b := NewDirBrowser(dir)
	m.browser = &b
	return m, dir
}

func TestBrowserCard_ShowsTitleCwdRowsAndFooter(t *testing.T) {
	m, dir := newBrowserMenu(t)
	raw := stripAnsi(m.renderBrowserCard())

	for _, want := range []string{
		"Add Project — choose folder",
		filepath.Base(dir), // cwd line mentions the directory
		ChooseThisFolderRow,
		"alpha/",
		"beta/",
		"esc cancel",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("card should contain %q, got:\n%s", want, raw)
		}
	}
}

func TestBrowserCard_HighlightMarkerFollowsSelection(t *testing.T) {
	m, _ := newBrowserMenu(t)
	raw := stripAnsi(m.renderBrowserCard())
	if !strings.Contains(raw, "▸ "+ChooseThisFolderRow) {
		t.Errorf("row 0 should carry the highlight marker, got:\n%s", raw)
	}

	m.browser.MoveDown()
	raw = stripAnsi(m.renderBrowserCard())
	if !strings.Contains(raw, "▸ alpha/") {
		t.Errorf("alpha should carry the highlight marker after MoveDown, got:\n%s", raw)
	}
	if strings.Contains(raw, "▸ "+ChooseThisFolderRow) {
		t.Errorf("row 0 must lose the marker, got:\n%s", raw)
	}
}

func TestBrowserCard_HeightStableAcrossStates(t *testing.T) {
	m, _ := newBrowserMenu(t)
	base := strings.Count(m.renderBrowserCard(), "\n")

	m.browser.TypeRune('z') // no matches
	if got := strings.Count(m.renderBrowserCard(), "\n"); got != base {
		t.Errorf("card height changed with empty match list: %d vs %d", got, base)
	}

	m.browser.ClearFilter()
	for _, r := range "https://github.com/owner/repo" {
		m.browser.TypeRune(r)
	}
	if got := strings.Count(m.renderBrowserCard(), "\n"); got != base {
		t.Errorf("card height changed in GitHub-URL state: %d vs %d", got, base)
	}
}

func TestBrowserCard_ScrollWindowFollowsSelection(t *testing.T) {
	var many []string
	for i := 0; i < 15; i++ {
		many = append(many, fmt.Sprintf("dir%02d", i))
	}
	m, _ := newBrowserMenu(t, many...)
	for i := 0; i < 15; i++ {
		m.browser.MoveDown()
	}
	raw := stripAnsi(m.renderBrowserCard())
	if !strings.Contains(raw, "▸ dir14/") {
		t.Errorf("last row should be visible and highlighted after scrolling, got:\n%s", raw)
	}
	if strings.Contains(raw, "dir00/") {
		t.Errorf("scrolled-out top rows should not render, got:\n%s", raw)
	}
}

func TestBrowserCard_GitHubURLShowsCloneHint(t *testing.T) {
	m, _ := newBrowserMenu(t)
	for _, r := range "https://github.com/owner/repo" {
		m.browser.TypeRune(r)
	}
	raw := stripAnsi(m.renderBrowserCard())
	if !strings.Contains(raw, "owner/repo") {
		t.Errorf("GitHub-URL state should show the repo slug, got:\n%s", raw)
	}
	if strings.Contains(raw, "alpha/") {
		t.Errorf("GitHub-URL state should replace the folder list, got:\n%s", raw)
	}
}

func TestView_CompositesBrowserOverlay(t *testing.T) {
	m, _ := newBrowserMenu(t)
	m.inputMode = "add-project"
	raw := stripAnsi(m.View())
	if !strings.Contains(raw, "Add Project — choose folder") {
		t.Errorf("View should composite the browser card, got:\n%s", raw)
	}
}
