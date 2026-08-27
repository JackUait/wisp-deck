package ledger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// git lists an untracked symlink as one entry and never follows it, so opening
// the entry reaches the TARGET. A link to a directory therefore fails the read
// with EISDIR and used to abort the whole snapshot with
// "inspect untracked files: read <path>: is a directory".
func TestLoadCountsAnUntrackedSymlinkWithoutFollowingIt(t *testing.T) {
	repo := newLedgerTestRepo(t)
	target := t.TempDir()
	writeGitTestFile(t, target, "inside.txt", []byte("one\ntwo\nthree\n"))
	symlinkForTest(t, target, filepath.Join(repo, "dirlink"))
	symlinkForTest(t, filepath.Join(target, "inside.txt"), filepath.Join(repo, "filelink"))
	symlinkForTest(t, filepath.Join(target, "gone.txt"), filepath.Join(repo, "danglink"))

	snapshot, err := NewSource(ExecRunner{}, WithWorkers(2)).Load(context.Background(), repo, 1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// `git add <symlink>` stages the target path as a one-line blob, so numstat
	// reports "1 0" for every symlink whatever it points at.
	for _, name := range []string{"dirlink", "filelink", "danglink"} {
		row := requireNewRow(t, snapshot, name)
		if row.Added != 1 || row.Binary || row.NewBytes != 0 {
			t.Errorf("%s = added %d, binary %v, bytes %d; want added 1, text, no size",
				name, row.Added, row.Binary, row.NewBytes)
		}
	}
}

// git does not descend into a nested repository; it reports the directory
// itself, with a trailing slash.
func TestLoadReportsAnUntrackedNestedRepositoryWithoutReadingIt(t *testing.T) {
	repo := newLedgerTestRepo(t)
	nested := filepath.Join(repo, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, nested, "init", "-q")
	writeGitTestFile(t, nested, "inner.txt", []byte("inner\n"))

	snapshot, err := NewSource(ExecRunner{}, WithWorkers(2)).Load(context.Background(), repo, 1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	row := requireNewRow(t, snapshot, "sub/")
	if row.Added != 0 || row.Binary || row.NewBytes != 0 {
		t.Errorf("sub/ = added %d, binary %v, bytes %d; want an uncounted text row",
			row.Added, row.Binary, row.NewBytes)
	}
}

func newLedgerTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTestRun(t, repo, "init", "-q")
	gitTestRun(t, repo, "config", "user.email", "ledger@example.test")
	gitTestRun(t, repo, "config", "user.name", "Ledger Test")
	writeGitTestFile(t, repo, "base.txt", []byte("base\n"))
	gitTestRun(t, repo, "add", "-A")
	gitTestRun(t, repo, "commit", "-qm", "base")
	return repo
}

func symlinkForTest(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func requireNewRow(t *testing.T, snapshot Snapshot, path string) Row {
	t.Helper()
	index, ok := snapshot.Index(RowID{Group: GroupNew, Path: path})
	if !ok {
		t.Fatalf("missing new row %q in %#v", path, snapshot.Rows)
	}
	return snapshot.Rows[index]
}
