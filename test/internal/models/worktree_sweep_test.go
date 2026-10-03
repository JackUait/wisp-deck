package models_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/models"
)

func sweepGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// sweepRepo makes a repo with one commit on main.
func sweepRepo(t *testing.T) string {
	t.Helper()
	// git reports resolved paths (/private/var, not /var).
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	sweepGit(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sweepGit(t, repo, "add", "a.txt")
	sweepGit(t, repo, "commit", "-q", "-m", "init")
	return repo
}

// addDetached adds a detached worktree at HEAD under the repo's
// .claude/worktrees, a throwaway root.
func addDetached(t *testing.T, repo, name string) string {
	t.Helper()
	wt := filepath.Join(repo, ".claude", "worktrees", name)
	sweepGit(t, repo, "worktree", "add", "-q", "--detach", wt)
	return wt
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestSweepDetachedWorktrees_removesACleanReachableIdleOne(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "done")

	removed := models.SweepDetachedWorktrees(repo, 0)

	if exists(wt) {
		t.Fatalf("worktree still on disk: %s", wt)
	}
	if len(removed) != 1 || removed[0] != wt {
		t.Errorf("removed = %v, want [%s]", removed, wt)
	}
	if list := sweepGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, wt) {
		t.Errorf("worktree still registered:\n%s", list)
	}
}

func TestSweepDetachedWorktrees_keepsOneWithUncommittedChanges(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "dirty")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a worktree with uncommitted changes was removed")
	}
}

func TestSweepDetachedWorktrees_keepsOneWithUntrackedFiles(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "untracked")
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a worktree with untracked files was removed")
	}
}

func TestSweepDetachedWorktrees_keepsOneWhoseCommitIsOnNoRef(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "orphan")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("only here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sweepGit(t, wt, "commit", "-q", "-am", "detached work")

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a worktree whose commit no ref reaches was removed")
	}
}

func TestSweepDetachedWorktrees_keepsOneOutsideAThrowawayRoot(t *testing.T) {
	repo := sweepRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "kept-on-purpose")
	sweepGit(t, repo, "worktree", "add", "-q", "--detach", wt)

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a detached worktree outside the throwaway roots was removed")
	}
}

func TestSweepDetachedWorktrees_keepsABranchWorktree(t *testing.T) {
	repo := sweepRepo(t)
	wt := filepath.Join(repo, ".claude", "worktrees", "feature")
	sweepGit(t, repo, "worktree", "add", "-q", "-b", "feature", wt)

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a worktree on a branch was removed")
	}
}

func TestSweepDetachedWorktrees_keepsALockedOne(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "locked")
	sweepGit(t, repo, "worktree", "lock", wt)

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a locked worktree was removed")
	}
}

func TestSweepDetachedWorktrees_keepsOneTouchedRecently(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "fresh")

	models.SweepDetachedWorktrees(repo, time.Hour)

	if !exists(wt) {
		t.Fatal("a worktree created moments ago was removed")
	}
}

func TestSweepDetachedWorktrees_removesOneIdlePastTheThreshold(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "stale")
	old := time.Now().Add(-2 * time.Hour)
	admin := strings.TrimPrefix(readFile(t, filepath.Join(wt, ".git")), "gitdir: ")
	for _, p := range []string{wt, filepath.Join(admin, "HEAD"), filepath.Join(admin, "index"), filepath.Join(admin, "logs", "HEAD")} {
		if err := os.Chtimes(p, old, old); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	models.SweepDetachedWorktrees(repo, time.Hour)

	if exists(wt) {
		t.Fatal("a worktree idle for two hours was kept")
	}
}

func TestSweepDetachedWorktrees_keepsOneAProcessIsRunningIn(t *testing.T) {
	repo := sweepRepo(t)
	wt := addDetached(t, repo, "busy")
	sub := filepath.Join(wt, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty directory is not untracked content to git, so only the
	// process keeps this worktree.
	cmd := exec.Command("sleep", "30")
	cmd.Dir = sub
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	models.SweepDetachedWorktrees(repo, 0)

	if !exists(wt) {
		t.Fatal("a worktree a process is running in was removed")
	}
}

func TestSweepDetachedWorktrees_prunesARegistrationWhoseFolderIsGone(t *testing.T) {
	repo := sweepRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "gone")
	sweepGit(t, repo, "worktree", "add", "-q", "-b", "gone", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	models.SweepDetachedWorktrees(repo, 0)

	if list := sweepGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, wt) {
		t.Errorf("registration for a deleted folder survived:\n%s", list)
	}
}

func TestIsThrowawayWorktreePath(t *testing.T) {
	cases := map[string]bool{
		"/private/tmp/claude-501/-Users-x-repo/sess/scratchpad/wt-a": true,
		"/tmp/claude-501/-Users-x-repo/sess/scratchpad/wt-a":         true,
		"/Users/x/repo/.claude/worktrees/agent-1":                    true,
		"/Users/x/Packages/.blok-undo/wt/tab-sync-base":              false,
		"/private/tmp/other/wt":                                      false,
		"/Users/x/repo/.claude/worktrees":                            false,
	}
	for path, want := range cases {
		if got := models.IsThrowawayWorktreePath(path); got != want {
			t.Errorf("IsThrowawayWorktreePath(%q) = %v, want %v", path, got, want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
