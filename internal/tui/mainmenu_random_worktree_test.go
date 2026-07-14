package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// initTestGitRepo creates a git repository named "proj" with one commit inside
// a fresh temp dir and returns its path.
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"git", "-C", dir, "init", "-q"},
		{"git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// newRandomWorktreeMenu builds a menu whose first project points at a real
// git repository, so the dispatched git command can actually run.
func newRandomWorktreeMenu(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	repo := initTestGitRepo(t)
	projects := []models.Project{{Name: "proj", Path: repo}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.width = 100
	m.height = 40
	return m, repo
}

// randomWorktreeBasename matches "proj--" plus a four-word random name.
var randomWorktreeBasename = regexp.MustCompile(`^proj--[a-z]+(-[a-z]+){3}$`)

// TestNKey_CreatesRandomWorktreeForCursorProject verifies the whole flow:
// pressing 'n' on a project row returns a tea.Cmd that creates a git worktree
// with a random four-word name next to the project, and feeding the resulting
// message back into Update shows success feedback.
func TestNKey_CreatesRandomWorktreeForCursorProject(t *testing.T) {
	m, repo := newRandomWorktreeMenu(t)
	m.selectedItem = 0 // project "proj"

	_, cmd := m.handleRune('n')
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from pressing 'n' on a project row, got nil")
	}

	msg := cmd()
	done, ok := msg.(worktreeDoneMsg)
	if !ok {
		t.Fatalf("expected worktreeDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("worktree creation failed: %v", done.err)
	}

	base := filepath.Base(done.path)
	if !randomWorktreeBasename.MatchString(base) {
		t.Errorf("worktree basename %q does not match proj--<four-random-words>", base)
	}
	if filepath.Dir(done.path) != filepath.Dir(repo) {
		t.Errorf("worktree created at %q, want sibling of %q", done.path, repo)
	}
	if _, err := os.Stat(done.path); err != nil {
		t.Errorf("worktree dir not created: %v", err)
	}

	_, _ = m.Update(msg)
	if m.feedbackStyle != "success" {
		t.Errorf("expected success feedback after worktreeDoneMsg, got style=%q msg=%q", m.feedbackStyle, m.feedbackMsg)
	}
	if !strings.Contains(m.feedbackMsg, done.path) {
		t.Errorf("feedback %q should mention the new worktree path %q", m.feedbackMsg, done.path)
	}
}

// TestNKey_OnWorktreeRow_UsesParentProject verifies that pressing 'n' while
// the cursor is on a worktree row creates a new worktree for that row's
// parent project.
func TestNKey_OnWorktreeRow_UsesParentProject(t *testing.T) {
	m, repo := newRandomWorktreeMenu(t)

	// Give the project one existing worktree and expand it so the cursor can
	// sit on a worktree row.
	m.projects[0].Worktrees = []models.Worktree{{Path: repo + "--existing", Branch: "existing"}}
	m.expandedWorktrees[0] = true
	m.selectedItem = 1 // the worktree row
	if itemType, _, _ := m.ResolveItem(1); itemType != "worktree" {
		t.Fatalf("setup: expected worktree row at flat index 1, got %q", itemType)
	}

	_, cmd := m.handleRune('n')
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from pressing 'n' on a worktree row, got nil")
	}
	msg := cmd()
	done, ok := msg.(worktreeDoneMsg)
	if !ok {
		t.Fatalf("expected worktreeDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("worktree creation failed: %v", done.err)
	}
	if !strings.HasPrefix(filepath.Base(done.path), "proj--") {
		t.Errorf("worktree %q not created for parent project proj", done.path)
	}
}

// TestNKey_OnAddProjectRow_IsNoOp verifies that pressing 'n' on the
// add-project row does nothing.
func TestNKey_OnAddProjectRow_IsNoOp(t *testing.T) {
	m, _ := newRandomWorktreeMenu(t)
	m.selectedItem = 1 // add-project row (single project at 0)
	if itemType, _, _ := m.ResolveItem(1); itemType != "add-project" {
		t.Fatalf("setup: expected add-project row at flat index 1, got %q", itemType)
	}

	_, cmd := m.handleRune('n')
	if cmd != nil {
		t.Error("expected no tea.Cmd from pressing 'n' on the add-project row")
	}
}

// TestNKey_OutsideProjectsTab_IsNoOp verifies that 'n' is inert on other tabs.
func TestNKey_OutsideProjectsTab_IsNoOp(t *testing.T) {
	m, _ := newRandomWorktreeMenu(t)
	m.SetActiveTab(TabSettings)

	_, cmd := m.handleRune('n')
	if cmd != nil {
		t.Error("expected no tea.Cmd from pressing 'n' on the Settings tab")
	}
}

// TestNKey_GitFailure_ShowsErrorFeedback verifies that a failing git command
// surfaces error feedback instead of success.
func TestNKey_GitFailure_ShowsErrorFeedback(t *testing.T) {
	nonRepo := t.TempDir()
	projects := []models.Project{{Name: "proj", Path: nonRepo}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.selectedItem = 0

	_, cmd := m.handleRune('n')
	if cmd == nil {
		t.Fatal("expected a tea.Cmd, got nil")
	}
	msg := cmd()
	done, ok := msg.(worktreeDoneMsg)
	if !ok {
		t.Fatalf("expected worktreeDoneMsg, got %T", msg)
	}
	if done.err == nil {
		t.Fatal("expected error creating worktree in a non-git directory")
	}

	_, _ = m.Update(msg)
	if m.feedbackStyle != "error" {
		t.Errorf("expected error feedback, got style=%q msg=%q", m.feedbackStyle, m.feedbackMsg)
	}
}

// TestProjectsFooterHint_MentionsNewWorktreeKey keeps the new key discoverable.
func TestProjectsFooterHint_MentionsNewWorktreeKey(t *testing.T) {
	m, _ := newRandomWorktreeMenu(t)
	hint := m.focusHint()
	if !strings.Contains(hint, "N ") {
		t.Errorf("projects footer hint %q should mention the N (new worktree) key", hint)
	}
}
