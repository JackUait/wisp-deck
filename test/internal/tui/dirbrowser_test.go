package tui_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackuait/wisp-deck/internal/tui"
)

// newBrowserDir builds a directory tree for browsing tests:
// alpha/, beta/ (with nested/), .hidden/, and a plain file.
func newBrowserDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"beta/nested", "alpha", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDirBrowser_ListsSortedSubdirsHidingDotdirsAndFiles(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	want := []string{tui.ChooseThisFolderRow, "alpha", "beta"}
	if got := b.VisibleRows(); !reflect.DeepEqual(got, want) {
		t.Errorf("VisibleRows() = %v, want %v", got, want)
	}
	if b.Cwd() != dir {
		t.Errorf("Cwd() = %q, want %q", b.Cwd(), dir)
	}
}

func TestDirBrowser_FilterNarrowsCaseInsensitively(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	b.TypeRune('A')
	b.TypeRune('L')
	want := []string{tui.ChooseThisFolderRow, "alpha"}
	if got := b.VisibleRows(); !reflect.DeepEqual(got, want) {
		t.Errorf("VisibleRows() with filter AL = %v, want %v", got, want)
	}
	if b.Filter() != "AL" {
		t.Errorf("Filter() = %q, want AL", b.Filter())
	}
}

func TestDirBrowser_DotFilterRevealsDotdirs(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	b.TypeRune('.')
	want := []string{tui.ChooseThisFolderRow, ".hidden"}
	if got := b.VisibleRows(); !reflect.DeepEqual(got, want) {
		t.Errorf("VisibleRows() with filter '.' = %v, want %v", got, want)
	}
}

func TestDirBrowser_TypingSelectsFirstMatch(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	b.TypeRune('b')
	if b.Selected() != 1 {
		t.Errorf("typing should highlight the first match (row 1), got %d", b.Selected())
	}
}

func TestDirBrowser_DescendEntersHighlightedAndResetsFilter(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	b.TypeRune('b') // highlight beta
	if !b.Descend() {
		t.Fatal("Descend() on a folder row should return true")
	}
	if b.Cwd() != filepath.Join(dir, "beta") {
		t.Errorf("Cwd() = %q, want %q", b.Cwd(), filepath.Join(dir, "beta"))
	}
	if b.Filter() != "" {
		t.Errorf("filter should reset on descend, got %q", b.Filter())
	}
	want := []string{tui.ChooseThisFolderRow, "nested"}
	if got := b.VisibleRows(); !reflect.DeepEqual(got, want) {
		t.Errorf("VisibleRows() after descend = %v, want %v", got, want)
	}
}

func TestDirBrowser_DescendOnChooseRowIsNoop(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	if b.Descend() {
		t.Error("Descend() on the choose-this-folder row should return false")
	}
	if b.Cwd() != dir {
		t.Errorf("Cwd() should not change, got %q", b.Cwd())
	}
}

func TestDirBrowser_GoUpToParentAndStopAtRoot(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(filepath.Join(dir, "beta", "nested"))

	b.GoUp()
	if b.Cwd() != filepath.Join(dir, "beta") {
		t.Errorf("Cwd() = %q, want %q", b.Cwd(), filepath.Join(dir, "beta"))
	}

	r := tui.NewDirBrowser("/")
	r.GoUp()
	if r.Cwd() != "/" {
		t.Errorf("GoUp at / should stay at /, got %q", r.Cwd())
	}
}

func TestDirBrowser_ChooseHighlighted(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	if got, ok := b.ChooseHighlighted(); !ok || got != dir {
		t.Errorf("choose on row 0 = %q,%v, want cwd %q,true", got, ok, dir)
	}

	b.MoveDown() // row 1: alpha
	if got, ok := b.ChooseHighlighted(); !ok || got != filepath.Join(dir, "alpha") {
		t.Errorf("choose on row 1 = %q,%v, want %q,true", got, ok, filepath.Join(dir, "alpha"))
	}
}

func TestDirBrowser_MoveClampsAtEnds(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	b.MoveUp()
	if b.Selected() != 0 {
		t.Errorf("MoveUp at top should clamp to 0, got %d", b.Selected())
	}
	b.MoveDown()
	b.MoveDown()
	b.MoveDown() // past end (rows: choose, alpha, beta)
	if b.Selected() != 2 {
		t.Errorf("MoveDown should clamp to last row 2, got %d", b.Selected())
	}
}

func TestDirBrowser_SelectionClampsWhenFilterShrinksList(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	b.MoveDown()
	b.MoveDown() // beta (row 2)
	b.TypeRune('a')
	b.TypeRune('l') // only alpha matches → rows: choose, alpha
	if b.Selected() >= len(b.VisibleRows()) {
		t.Errorf("selection %d out of range for %d rows", b.Selected(), len(b.VisibleRows()))
	}
}

func TestDirBrowser_BackspaceEditsFilterOrReportsEmpty(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	if b.BackspaceFilter() {
		t.Error("BackspaceFilter on empty filter should return false")
	}
	b.TypeRune('a')
	b.TypeRune('l')
	if !b.BackspaceFilter() {
		t.Error("BackspaceFilter with text should return true")
	}
	if b.Filter() != "a" {
		t.Errorf("Filter() = %q, want a", b.Filter())
	}
}

func TestDirBrowser_UnreadableDirDescendStaysAndSetsErr(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	dir := newBrowserDir(t)
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	b := tui.NewDirBrowser(dir)
	b.TypeRune('l') // highlight locked (alpha excluded? "l" matches alpha too)
	b.ClearFilter()
	b.TypeRune('c') // "c" matches only "locked"
	if !b.Descend() {
		// descend attempted; ok either way — the invariant is below
		t.Log("Descend returned false")
	}
	if b.Cwd() != dir {
		t.Errorf("unreadable descend should stay in %q, got %q", dir, b.Cwd())
	}
	if b.Err() == "" {
		t.Error("unreadable descend should set Err")
	}
}

func TestDirBrowser_GitHubURLDetection(t *testing.T) {
	dir := newBrowserDir(t)
	b := tui.NewDirBrowser(dir)

	if _, ok := b.GitHubURL(); ok {
		t.Error("empty filter should not be a GitHub URL")
	}
	for _, r := range "https://github.com/owner/repo" {
		b.TypeRune(r)
	}
	url, ok := b.GitHubURL()
	if !ok {
		t.Fatal("filter holding a GitHub URL should be detected")
	}
	if url == "" {
		t.Error("GitHubURL should return the clone URL")
	}
}
