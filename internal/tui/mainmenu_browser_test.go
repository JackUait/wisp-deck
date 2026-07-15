package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newBrowserAddMenu builds a menu that just entered add-project mode with a
// projects root containing the given subdirs. Returns the model and the root.
func newBrowserAddMenu(t *testing.T, subdirs ...string) (*MainMenuModel, string) {
	t.Helper()
	m := newTestMenu()
	m.width, m.height = 100, 40
	dir := t.TempDir()
	for _, d := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m.projectsFile = filepath.Join(dir, "projects")
	m.projectsRootFile = filepath.Join(dir, "projects-root")
	if err := os.WriteFile(m.projectsRootFile, []byte(dir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.enterInputMode("add-project")
	return m, dir
}

func bkey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func runes(s string) []tea.KeyMsg {
	var msgs []tea.KeyMsg
	for _, r := range s {
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return msgs
}

func send(t *testing.T, m *MainMenuModel, msgs ...tea.KeyMsg) *MainMenuModel {
	t.Helper()
	for _, msg := range msgs {
		res, _ := m.Update(msg)
		m = res.(*MainMenuModel)
	}
	return m
}

func TestBrowser_AddProjectOpensBrowserAtRoot(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	if m.browser == nil {
		t.Fatal("entering add-project should open the folder browser")
	}
	if m.browser.Cwd() != dir {
		t.Errorf("browser should start at the projects root %q, got %q", dir, m.browser.Cwd())
	}
}

func TestBrowser_OpenOnceKeepsTextField(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	m.exitInputMode()
	m.browser = nil
	m.enterInputMode("open-once")
	if m.browser != nil {
		t.Error("open-once must keep the text field, not open the browser")
	}
}

func TestBrowser_EscClosesBackToMenu(t *testing.T) {
	m, _ := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyEsc))
	if m.browser != nil || m.inputMode != "" {
		t.Errorf("Esc should close browser and input mode, browser=%v mode=%q", m.browser, m.inputMode)
	}
}

func TestBrowser_EscClearsFilterFirst(t *testing.T) {
	m, _ := newBrowserAddMenu(t, "alpha")
	m = send(t, m, runes("al")...)
	m = send(t, m, bkey(tea.KeyEsc))
	if m.browser == nil {
		t.Fatal("Esc with a filter should only clear the filter")
	}
	if m.browser.Filter() != "" {
		t.Errorf("filter should be cleared, got %q", m.browser.Filter())
	}
}

func TestBrowser_EnterDescendsIntoHighlighted(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha/inner")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyEnter))
	if m.browser == nil {
		t.Fatal("browser should stay open after descending")
	}
	if m.browser.Cwd() != filepath.Join(dir, "alpha") {
		t.Errorf("Cwd = %q, want %q", m.browser.Cwd(), filepath.Join(dir, "alpha"))
	}
}

func TestBrowser_ArrowsNavigateUpAndDown(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha/inner")
	m = send(t, m, bkey(tea.KeyRight))
	if m.browser.Cwd() != dir {
		t.Errorf("→ on choose-row should stay, got %q", m.browser.Cwd())
	}
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyRight))
	if m.browser.Cwd() != filepath.Join(dir, "alpha") {
		t.Errorf("→ should descend, got %q", m.browser.Cwd())
	}
	m = send(t, m, bkey(tea.KeyLeft))
	if m.browser.Cwd() != dir {
		t.Errorf("← should go up, got %q", m.browser.Cwd())
	}
}

func TestBrowser_ChooseCwdAdvancesToNameStage(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyEnter)) // row 0 = choose this folder
	if m.browser != nil {
		t.Fatal("choosing a folder should close the browser")
	}
	if m.inputMode != "add-project" {
		t.Fatalf("should remain in add-project mode, got %q", m.inputMode)
	}
	if m.inputFocusPath {
		t.Error("focus should advance to the name field")
	}
	if got := m.pathInput.Value(); got != dir {
		t.Errorf("path = %q, want %q", got, dir)
	}
	if got := m.nameInput.Value(); got != filepath.Base(dir) {
		t.Errorf("name should auto-derive %q, got %q", filepath.Base(dir), got)
	}
}

func TestBrowser_TabChoosesHighlightedSubdir(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab))
	if m.browser != nil {
		t.Fatal("Tab should choose the highlighted folder and close the browser")
	}
	want := filepath.Join(dir, "alpha")
	if got := m.pathInput.Value(); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got := m.nameInput.Value(); got != "alpha" {
		t.Errorf("name = %q, want alpha", got)
	}
}

func TestBrowser_NameStagePathRowIsStatic(t *testing.T) {
	m, _ := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab))
	raw := stripAnsi(m.renderInputBox())
	pathLine := ""
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "Path:") {
			pathLine = l
			break
		}
	}
	if pathLine == "" {
		t.Fatalf("no Path row rendered:\n%s", raw)
	}
	if strings.Contains(pathLine, ">") {
		t.Errorf("path row should be static text without an input prompt, got %q", pathLine)
	}
	// Long paths are middle-truncated, so assert on the basename.
	if !strings.Contains(pathLine, "alpha") {
		t.Errorf("path row should show the chosen folder, got %q", pathLine)
	}
}

func TestBrowser_SubmitFromNameStageAppendsProject(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab), bkey(tea.KeyEnter))
	data, err := os.ReadFile(m.projectsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha:" + filepath.Join(dir, "alpha")
	if !strings.Contains(string(data), want) {
		t.Errorf("projects file should contain %q, got %q", want, string(data))
	}
	if m.inputMode != "" {
		t.Error("input mode should close after submit")
	}
}

func TestBrowser_EscFromNameStageReopensBrowser(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab)) // name stage, path = dir/alpha
	m = send(t, m, bkey(tea.KeyEsc))
	if m.browser == nil {
		t.Fatal("Esc from the name stage should reopen the browser")
	}
	if m.browser.Cwd() != dir {
		t.Errorf("browser should reopen at the chosen folder's parent %q, got %q", dir, m.browser.Cwd())
	}
	if m.inputMode != "add-project" {
		t.Errorf("still in add-project mode, got %q", m.inputMode)
	}
}

func TestBrowser_GitHubURLEnterAdvancesToNameStage(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	m = send(t, m, bkey(tea.KeyEnter))
	if m.browser != nil {
		t.Fatal("Enter on a GitHub URL should close the browser")
	}
	if m.inputFocusPath {
		t.Error("focus should be on the name field")
	}
	if got := m.pathInput.Value(); got != "https://github.com/owner/my-repo" {
		t.Errorf("path should hold the URL, got %q", got)
	}
	if got := m.nameInput.Value(); got != "my-repo" {
		t.Errorf("name should derive from the repo, got %q", got)
	}
}

func TestBrowser_GitHubURLSubmitDispatchesClone(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	cloned := false
	m.gitClone = func(url, dest string) error {
		cloned = true
		return os.MkdirAll(dest, 0o755)
	}
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	m = send(t, m, bkey(tea.KeyEnter)) // → name stage
	res, cmd := m.Update(bkey(tea.KeyEnter))
	m = res.(*MainMenuModel)
	if cmd == nil {
		t.Fatal("submit should dispatch the clone command")
	}
	if !m.cloning {
		t.Error("model should enter cloning state")
	}
	_ = cloned // executed asynchronously via the returned cmd
}

func TestBrowser_CtrlCQuits(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	res, cmd := m.Update(bkey(tea.KeyCtrlC))
	m = res.(*MainMenuModel)
	if cmd == nil {
		t.Fatal("Ctrl+C should quit")
	}
	if m.result == nil || m.result.Action != "quit" {
		t.Error("Ctrl+C should set the quit action result")
	}
}

func TestBrowser_BackspaceGoesUpWhenFilterEmpty(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha/inner")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyEnter)) // into alpha
	m = send(t, m, bkey(tea.KeyBackspace))
	if m.browser.Cwd() != dir {
		t.Errorf("backspace on empty filter should go up to %q, got %q", dir, m.browser.Cwd())
	}
}
