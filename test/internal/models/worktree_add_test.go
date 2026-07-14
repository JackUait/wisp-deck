package models_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// initGitRepo creates a git repository with one commit in a fresh temp dir
// and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
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

func TestAddWorktree_CreatesWorktreeOnNewBranch(t *testing.T) {
	repo := initGitRepo(t)
	wtPath := filepath.Join(filepath.Dir(repo), "repo--foxtrot-people-stop-sexy")

	if err := models.AddWorktree(repo, wtPath, "foxtrot-people-stop-sexy"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree dir not created: %v", err)
	}
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "refs/heads/foxtrot-people-stop-sexy") {
		t.Errorf("worktree list missing new branch:\n%s", out)
	}
}

func TestAddWorktree_NonGitDir_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	wtPath := filepath.Join(dir, "wt")

	err := models.AddWorktree(dir, wtPath, "some-branch")
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
}

func TestAddWorktree_ExistingBranch_ReturnsError(t *testing.T) {
	repo := initGitRepo(t)
	wtPath := filepath.Join(filepath.Dir(repo), "repo--dup")

	if err := models.AddWorktree(repo, wtPath, "dup"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}
	err := models.AddWorktree(repo, filepath.Join(filepath.Dir(repo), "repo--dup2"), "dup")
	if err == nil {
		t.Fatal("expected error when branch already exists, got nil")
	}
}
