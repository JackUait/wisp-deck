package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
)

// reloadProjects re-reads the projects file into the model, mirroring what
// enterInputMode sees after external writes in a test.
func reloadProjects(t *testing.T, m *MainMenuModel) {
	t.Helper()
	projects, err := models.LoadProjects(m.projectsFile)
	if err != nil {
		t.Fatal(err)
	}
	m.projects = projects
}

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

func TestBrowser_ChooseCwdAddsProjectImmediately(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyEnter)) // row 0 = choose this folder
	if m.browser != nil {
		t.Fatal("choosing a folder should close the browser")
	}
	if m.inputMode != "" {
		t.Fatalf("choosing should add the project and leave input mode, got %q", m.inputMode)
	}
	data, err := os.ReadFile(m.projectsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Base(dir) + ":" + dir
	if !strings.Contains(string(data), want) {
		t.Errorf("projects file should contain %q, got %q", want, string(data))
	}
	if !strings.Contains(m.feedbackMsg, "Added "+filepath.Base(dir)) {
		t.Errorf("feedback should announce the add, got %q", m.feedbackMsg)
	}
}

func TestBrowser_TabAddsHighlightedSubdirImmediately(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab))
	if m.browser != nil {
		t.Fatal("Tab should choose the highlighted folder and close the browser")
	}
	if m.inputMode != "" {
		t.Fatalf("Tab choose should add the project and leave input mode, got %q", m.inputMode)
	}
	data, err := os.ReadFile(m.projectsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha:" + filepath.Join(dir, "alpha")
	if !strings.Contains(string(data), want) {
		t.Errorf("projects file should contain %q, got %q", want, string(data))
	}
}

func TestBrowser_DuplicatePathShowsErrorInBrowser(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	if err := os.WriteFile(m.projectsFile, []byte("alpha:"+filepath.Join(dir, "alpha")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloadProjects(t, m)
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab))
	if m.browser == nil {
		t.Fatal("duplicate path should keep the browser open")
	}
	if !strings.Contains(m.browser.Err(), "already exists") {
		t.Errorf("browser should show the duplicate error, got %q", m.browser.Err())
	}
}

func TestBrowser_DuplicateNameWarnsThenAddsOnSecondChoose(t *testing.T) {
	m, dir := newBrowserAddMenu(t, "alpha")
	if err := os.WriteFile(m.projectsFile, []byte("alpha:/somewhere/else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloadProjects(t, m)
	m = send(t, m, bkey(tea.KeyDown), bkey(tea.KeyTab))
	if m.browser == nil {
		t.Fatal("duplicate name should keep the browser open with a warning")
	}
	if !strings.Contains(m.browser.Err(), "already exists") {
		t.Errorf("browser should warn about the duplicate name, got %q", m.browser.Err())
	}
	m = send(t, m, bkey(tea.KeyTab)) // confirm
	if m.browser != nil || m.inputMode != "" {
		t.Fatal("second choose should add anyway and close")
	}
	data, err := os.ReadFile(m.projectsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alpha:"+filepath.Join(dir, "alpha")) {
		t.Errorf("second choose should append the project, got %q", string(data))
	}
}

func TestBrowser_GitHubURLEnterStartsCloneInBrowser(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	m.gitClone = func(url, dest string) error { return os.MkdirAll(dest, 0o755) }
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	res, cmd := m.Update(bkey(tea.KeyEnter))
	m = res.(*MainMenuModel)
	if cmd == nil {
		t.Fatal("Enter on a GitHub URL should dispatch the clone command")
	}
	if !m.cloning {
		t.Error("model should enter cloning state")
	}
	if m.browser == nil {
		t.Error("browser should stay open showing the clone spinner")
	}
}

func TestBrowser_CloneDoneAddsProjectAndCloses(t *testing.T) {
	m, dir := newBrowserAddMenu(t)
	m.gitClone = func(url, dest string) error { return os.MkdirAll(dest, 0o755) }
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	res, _ := m.Update(bkey(tea.KeyEnter))
	m = res.(*MainMenuModel)
	dest := filepath.Join(dir, "my-repo")
	res, _ = m.Update(githubCloneDoneMsg{name: "my-repo", dest: dest, err: nil})
	m = res.(*MainMenuModel)
	if m.browser != nil || m.inputMode != "" {
		t.Error("clone success should close the browser and input mode")
	}
	data, err := os.ReadFile(m.projectsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "my-repo:"+dest) {
		t.Errorf("projects file should contain the cloned repo, got %q", string(data))
	}
}

func TestBrowser_CloneFailureShowsErrorInBrowser(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	m.gitClone = func(url, dest string) error { return nil }
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	res, _ := m.Update(bkey(tea.KeyEnter))
	m = res.(*MainMenuModel)
	res, _ = m.Update(githubCloneDoneMsg{name: "my-repo", dest: "/nope", err: fmt.Errorf("boom")})
	m = res.(*MainMenuModel)
	if m.browser == nil {
		t.Fatal("clone failure should keep the browser open")
	}
	if !strings.Contains(m.browser.Err(), "clone failed") {
		t.Errorf("browser should show the clone error, got %q", m.browser.Err())
	}
}

func TestBrowser_KeysFrozenWhileCloning(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	m.gitClone = func(url, dest string) error { return os.MkdirAll(dest, 0o755) }
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	res, _ := m.Update(bkey(tea.KeyEnter))
	m = res.(*MainMenuModel)
	m = send(t, m, bkey(tea.KeyEsc))
	if m.browser == nil || m.inputMode != "add-project" {
		t.Error("Esc must not close the browser while a clone is in flight")
	}
}

func TestBrowser_CardShowsCloneSpinner(t *testing.T) {
	m, _ := newBrowserAddMenu(t)
	m.gitClone = func(url, dest string) error { return os.MkdirAll(dest, 0o755) }
	m = send(t, m, runes("https://github.com/owner/my-repo")...)
	res, _ := m.Update(bkey(tea.KeyEnter))
	m = res.(*MainMenuModel)
	// Long destinations middle-truncate the status line, so assert the prefix.
	raw := stripAnsi(strings.Join(m.browserInnerLines(), "\n"))
	if !strings.Contains(raw, "⠋ Cloning owner/my-re") {
		t.Errorf("browser card should show the clone spinner, got:\n%s", raw)
	}
	if !strings.Contains(raw, "Ctrl+C quit") {
		t.Errorf("footer while cloning should advertise only Ctrl+C, got:\n%s", raw)
	}
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
