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
		"clean -fq -- missing.txt":                {err: wantErr},
	}}
	mutator := NewGitMutator(runner)

	err := mutator.Discard(context.Background(), "/repo", []string{"missing.txt"})

	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("discard error = %v, want failing path and wrapped cause", err)
	}
}
