package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/tui"
)

// applyCmd executes a tea.Cmd and feeds its message(s) back into the model,
// unwrapping one level of tea.Batch (submit now batches the clone cmd with
// its spinner ticker).
func applyCmd(t *testing.T, m *tui.MainMenuModel, cmd tea.Cmd) *tui.MainMenuModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		res, _ := m.Update(msg)
		return res.(*tui.MainMenuModel)
	}
	for _, c := range batch {
		if c == nil {
			continue
		}
		res, _ := m.Update(c())
		m = res.(*tui.MainMenuModel)
	}
	return m
}

// newGitHubAddMenu builds a menu in add-project mode with the path field
// holding a GitHub URL, a projects file, and a projects root pointing at dir.
func newGitHubAddMenu(t *testing.T, url string) (*tui.MainMenuModel, string, string) {
	t.Helper()
	dir := t.TempDir()
	projFile := filepath.Join(dir, "projects")
	os.WriteFile(projFile, []byte(""), 0644)
	rootFile := filepath.Join(dir, "projects-root")
	os.WriteFile(rootFile, []byte(dir+"\n"), 0644)

	m := tui.NewMainMenu(nil, testAITools(), "claude", "animated")
	m.SetProjectsFile(projFile)
	m.SetProjectsRootFile(rootFile)
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue(url)
	return m, projFile, dir
}

func TestMainMenu_AddProject_GitHubURL_AdvancesWithoutDirValidation(t *testing.T) {
	m, _, _ := newGitHubAddMenu(t, "https://github.com/owner/my-repo")

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := result.(*tui.MainMenuModel)

	if mm.InputFocusPath() {
		t.Fatal("Expected focus to advance to name field for a GitHub URL (no dir validation)")
	}
	if got := mm.NameInputValue(); got != "my-repo" {
		t.Errorf("Name should auto-derive from repo name, got %q", got)
	}
}

func TestMainMenu_AddProject_GitHubURL_ClonesIntoRootAndAddsProject(t *testing.T) {
	m, projFile, dir := newGitHubAddMenu(t, "https://github.com/owner/my-repo")

	var gotURL, gotDest string
	m.SetGitCloneForTest(func(url, dest string) error {
		gotURL, gotDest = url, dest
		return os.MkdirAll(dest, 0755)
	})

	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // path -> name
	mm1 := result1.(*tui.MainMenuModel)
	result2, cmd := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter}) // submit
	mm2 := result2.(*tui.MainMenuModel)

	if cmd == nil {
		t.Fatal("Submit with GitHub URL should dispatch a clone tea.Cmd")
	}
	if !mm2.Cloning() {
		t.Error("Model should be in cloning state after submit")
	}
	if mm2.InInputMode() != true {
		t.Error("Form should stay open while cloning")
	}

	mm3 := applyCmd(t, mm2, cmd) // run the clone (and its spinner ticker)

	wantDest := filepath.Join(dir, "my-repo")
	if gotURL != "https://github.com/owner/my-repo.git" {
		t.Errorf("clone URL = %q, want normalized https URL", gotURL)
	}
	if gotDest != wantDest {
		t.Errorf("clone dest = %q, want %q", gotDest, wantDest)
	}
	if mm3.Cloning() {
		t.Error("Cloning state should clear after clone completes")
	}
	if mm3.InInputMode() {
		t.Error("Should exit input mode after successful clone")
	}
	if mm3.FeedbackMsg() == "" {
		t.Error("Expected success feedback after clone")
	}
	data, _ := os.ReadFile(projFile)
	if !strings.Contains(string(data), "my-repo:"+wantDest) {
		t.Errorf("Projects file should contain cloned project, got: %q", string(data))
	}
}

func TestMainMenu_AddProject_GitHubURL_CloneFailureShowsError(t *testing.T) {
	m, projFile, _ := newGitHubAddMenu(t, "https://github.com/owner/my-repo")
	m.SetGitCloneForTest(func(url, dest string) error {
		return fmt.Errorf("repository not found")
	})

	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)
	result2, cmd := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)
	if cmd == nil {
		t.Fatal("Submit with GitHub URL should dispatch a clone tea.Cmd")
	}

	mm3 := applyCmd(t, mm2, cmd)

	if !mm3.InInputMode() {
		t.Error("Should stay in input mode after clone failure")
	}
	if mm3.Cloning() {
		t.Error("Cloning state should clear after failure")
	}
	if mm3.NameErr() == nil || !strings.Contains(mm3.NameErr().Error(), "repository not found") {
		t.Errorf("Expected clone error surfaced, got %v", mm3.NameErr())
	}
	data, _ := os.ReadFile(projFile)
	if strings.Contains(string(data), "my-repo") {
		t.Errorf("Failed clone must not be added to projects file, got: %q", string(data))
	}
}

func TestMainMenu_AddProject_GitHubURL_ExistingDestBlocksClone(t *testing.T) {
	m, _, dir := newGitHubAddMenu(t, "https://github.com/owner/my-repo")
	os.MkdirAll(filepath.Join(dir, "my-repo"), 0755)

	cloneCalled := false
	m.SetGitCloneForTest(func(url, dest string) error {
		cloneCalled = true
		return nil
	})

	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)
	result2, cmd := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)

	mm2 = applyCmd(t, mm2, cmd)
	if cloneCalled {
		t.Error("Clone must not run when destination directory already exists")
	}
	if !mm2.InInputMode() {
		t.Error("Should stay in input mode when destination exists")
	}
	if mm2.NameErr() == nil {
		t.Error("Expected an error about existing destination")
	}
}

func TestMainMenu_AddProject_GitHubURL_EnterWhileCloningIsNoop(t *testing.T) {
	m, _, _ := newGitHubAddMenu(t, "https://github.com/owner/my-repo")
	m.SetGitCloneForTest(func(url, dest string) error { return os.MkdirAll(dest, 0755) })

	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)
	result2, cmd1 := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)
	if cmd1 == nil {
		t.Fatal("First submit should dispatch a clone tea.Cmd")
	}

	result3, cmd2 := mm2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm3 := result3.(*tui.MainMenuModel)
	if cmd2 != nil {
		t.Error("Enter while cloning should not dispatch another cmd")
	}
	if !mm3.Cloning() || !mm3.InInputMode() {
		t.Error("Enter while cloning should leave state unchanged")
	}
}

func TestMainMenu_AddProject_GitHubURL_NoRootClonesIntoHome(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0755)
	t.Setenv("HOME", home)

	projFile := filepath.Join(dir, "projects")
	os.WriteFile(projFile, []byte(""), 0644)

	m := tui.NewMainMenu(nil, testAITools(), "claude", "animated")
	m.SetProjectsFile(projFile)
	m.SetProjectsRootFile(filepath.Join(dir, "missing-root-file"))
	m.EnterInputModeForTest("add-project")
	m.SetPathInputValue("git@github.com:owner/ssh-repo.git")

	var gotURL, gotDest string
	m.SetGitCloneForTest(func(url, dest string) error {
		gotURL, gotDest = url, dest
		return os.MkdirAll(dest, 0755)
	})

	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)
	result2, cmd := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)
	if cmd == nil {
		t.Fatal("Submit with SSH GitHub URL should dispatch a clone tea.Cmd")
	}
	applyCmd(t, mm2, cmd)

	if gotURL != "git@github.com:owner/ssh-repo.git" {
		t.Errorf("SSH input should keep SSH clone URL, got %q", gotURL)
	}
	if gotDest != filepath.Join(home, "ssh-repo") {
		t.Errorf("Without projects root, dest should be ~/<repo>, got %q", gotDest)
	}
}

func TestMainMenu_AddProject_GitHubURL_ViewShowsCloningState(t *testing.T) {
	m, _, _ := newGitHubAddMenu(t, "https://github.com/owner/my-repo")
	m.SetGitCloneForTest(func(url, dest string) error { return os.MkdirAll(dest, 0755) })

	result1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm1 := result1.(*tui.MainMenuModel)
	result2, _ := mm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm2 := result2.(*tui.MainMenuModel)

	if !mm2.Cloning() {
		t.Fatal("Expected cloning state after submit")
	}
	if !strings.Contains(mm2.View(), "Cloning") {
		t.Error("View should show a cloning indicator while the clone is in flight")
	}
}

func TestMainMenu_AddProject_ViewMentionsGitHubURL(t *testing.T) {
	m, _, _ := newGitHubAddMenu(t, "")

	if !strings.Contains(m.View(), "GitHub") {
		t.Error("Add-project form should tell the user a GitHub URL can be pasted")
	}
}
