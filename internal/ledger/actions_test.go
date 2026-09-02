package ledger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDiscardTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTestRun(t, repo, "init", "-q")
	gitTestRun(t, repo, "config", "user.email", "ledger@example.test")
	gitTestRun(t, repo, "config", "user.name", "Ledger Test")
	writeGitTestFile(t, repo, "tracked.txt", []byte("committed\n"))
	gitTestRun(t, repo, "add", "tracked.txt")
	gitTestRun(t, repo, "commit", "-qm", "initial")
	return repo
}

func TestDiscardRestoresTrackedFileFromIndex(t *testing.T) {
	repo := newDiscardTestRepo(t)
	writeGitTestFile(t, repo, "tracked.txt", []byte("working tree edit\n"))
	mutator := NewGitMutator(ExecRunner{})

	if err := mutator.Discard(context.Background(), repo, []string{"tracked.txt"}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "committed\n" {
		t.Fatalf("tracked content = %q, want committed index content", got)
	}
}

func TestDiscardKeepsStagedNewFileAndDropsWorkingTreeEdit(t *testing.T) {
	repo := newDiscardTestRepo(t)
	writeGitTestFile(t, repo, "staged-new.txt", []byte("staged content\n"))
	gitTestRun(t, repo, "add", "staged-new.txt")
	writeGitTestFile(t, repo, "staged-new.txt", []byte("unstaged edit\n"))
	mutator := NewGitMutator(ExecRunner{})

	if err := mutator.Discard(context.Background(), repo, []string{"staged-new.txt"}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "staged-new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "staged content\n" {
		t.Fatalf("working content = %q, want staged content", got)
	}
	if out := string(gitTestOutput(t, repo, "diff", "--cached", "--name-only")); !strings.Contains(out, "staged-new.txt") {
		t.Fatalf("staged-new file left the index: %q", out)
	}
}

func TestDiscardCleansUntrackedFile(t *testing.T) {
	repo := newDiscardTestRepo(t)
	writeGitTestFile(t, repo, "untracked.txt", []byte("temporary\n"))
	mutator := NewGitMutator(ExecRunner{})

	if err := mutator.Discard(context.Background(), repo, []string{"untracked.txt"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(repo, "untracked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untracked file still exists or stat failed unexpectedly: %v", err)
	}
}

func TestDiscardReportsFailingPath(t *testing.T) {
	wantErr := errors.New("clean denied")
	runner := &fakeRunner{results: map[string]fakeRunResult{
		"ls-files --error-unmatch -- missing.txt": {err: errors.New("not tracked")},
		"clean -ffdq -- missing.txt":              {err: wantErr},
	}}
	mutator := NewGitMutator(runner)

	err := mutator.Discard(context.Background(), "/repo", []string{"missing.txt"})

	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("discard error = %v, want failing path and wrapped cause", err)
	}
}

// `git ls-files --others` does not descend into a nested repository, so a
// subagent's `.claude/worktrees/<name>` checkout reaches the ledger as ONE row
// whose path is "<dir>/". `git clean -f` never removes a directory and still
// exits 0, so discarding those rows reported success and left every one of them
// exactly where it was.
func TestDiscardRemovesAnUntrackedNestedWorktree(t *testing.T) {
	repo := newDiscardTestRepo(t)
	worktree := filepath.Join(".claude", "worktrees", "agent-a02ee641e06fabfe7")
	gitTestRun(t, repo, "worktree", "add", "-q", worktree, "-b", "agent-work")

	snapshot, err := NewSource(ExecRunner{}).Load(context.Background(), repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, row := range snapshot.Rows {
		if row.Kind == RowFile {
			paths = append(paths, row.Path)
		}
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/") {
		t.Fatalf(`ledger rows = %v, want the nested worktree as one "<dir>/" entry`, paths)
	}

	if err := NewGitMutator(ExecRunner{}).Discard(context.Background(), repo, paths); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(repo, worktree)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested worktree still on disk after discard: %v", err)
	}
	if out := gitTestOutput(t, repo, "ls-files", "--others", "--exclude-standard"); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the discarded worktree is still a ledger row: %q", out)
	}
}

// Removing the checkout leaves its registration behind, and the project menu
// polls `git worktree list` every couple of seconds — a leftover registration
// is a menu row that launches into a path that no longer exists.
func TestDiscardUnregistersTheWorktreeItRemoved(t *testing.T) {
	repo := newDiscardTestRepo(t)
	worktree := filepath.Join(".claude", "worktrees", "agent-a02ee641e06fabfe7")
	gitTestRun(t, repo, "worktree", "add", "-q", worktree, "-b", "agent-work")

	if err := NewGitMutator(ExecRunner{}).Discard(context.Background(), repo, []string{filepath.ToSlash(worktree) + "/"}); err != nil {
		t.Fatal(err)
	}

	if out := gitTestOutput(t, repo, "worktree", "list", "--porcelain"); strings.Contains(string(out), "agent-a02ee641e06fabfe7") {
		t.Fatalf("discarded worktree is still registered: %q", out)
	}
}
