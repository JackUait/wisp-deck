package ledger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRealRepositoryBuildsCurrentSnapshot(t *testing.T) {
	repo := t.TempDir()
	gitTestRun(t, repo, "init", "-q")
	gitTestRun(t, repo, "config", "user.email", "ledger@example.test")
	gitTestRun(t, repo, "config", "user.name", "Ledger Test")
	writeGitTestFile(t, repo, "unstaged.txt", []byte("before\n"))
	writeGitTestFile(t, repo, "image.png", []byte{0, 1, 2})
	gitTestRun(t, repo, "add", "-A")
	gitTestRun(t, repo, "commit", "-qm", "base")

	writeGitTestFile(t, repo, "unstaged.txt", []byte("before\nafter\n"))
	writeGitTestFile(t, repo, "staged.txt", []byte("staged\n"))
	gitTestRun(t, repo, "add", "staged.txt")
	writeGitTestFile(t, repo, "new.txt", []byte("new\nfile"))
	writeGitTestFile(t, repo, "image.png", []byte{0, 1, 2, 3, 4})
	gitTestRun(t, repo, "add", "image.png")

	source := NewSource(ExecRunner{}, WithWorkers(2))
	snapshot, err := source.Load(context.Background(), repo, 8)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != 8 || snapshot.Metadata.TotalFiles != 4 {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	wantGroups := map[string]Group{
		"staged.txt":   GroupStaged,
		"image.png":    GroupStaged,
		"unstaged.txt": GroupModified,
		"new.txt":      GroupNew,
	}
	imageIndex, ok := snapshot.Index(RowID{Group: GroupStaged, Path: "image.png"})
	if !ok {
		t.Fatal("missing staged image")
	}
	image := snapshot.Rows[imageIndex]
	if image.OldBytes != 3 || image.NewBytes != 5 {
		t.Fatalf("image sizes = %d -> %d, want 3 -> 5", image.OldBytes, image.NewBytes)
	}
	for path, group := range wantGroups {
		index, ok := snapshot.Index(RowID{Group: group, Path: path})
		if !ok {
			t.Errorf("missing %s in group %d", path, group)
			continue
		}
		if snapshot.Rows[index].Path != path {
			t.Errorf("indexed row = %#v", snapshot.Rows[index])
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatal(err)
	}
}
