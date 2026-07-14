package ledger

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

func TestParseNumstatZOrdinaryPath(t *testing.T) {
	raw := []byte("4\t2\tsrc/a.go\x00")

	got, err := parseNumstatZ(raw, GroupModified)

	if err != nil {
		t.Fatal(err)
	}
	want := Change{Group: GroupModified, Path: "src/a.go", Added: 4, Deleted: 2}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("changes = %#v, want %#v", got, []Change{want})
	}
}

func TestParseNumstatZRenameUsesCurrentPath(t *testing.T) {
	raw := []byte("4\t2\t\x00old name.go\x00new name.go\x00")

	got, err := parseNumstatZ(raw, GroupStaged)

	if err != nil {
		t.Fatal(err)
	}
	want := Change{
		Group: GroupStaged,
		Path:  "new name.go", OldPath: "old name.go",
		Added: 4, Deleted: 2,
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("changes = %#v, want %#v", got, []Change{want})
	}
}

func TestParseNumstatZPreservesTabsAndNewlinesInPath(t *testing.T) {
	path := "src/a\tstrange\nname.go"
	raw := append([]byte("1\t0\t"+path), 0)

	got, err := parseNumstatZ(raw, GroupNew)

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != path {
		t.Fatalf("path = %q, want %q", got[0].Path, path)
	}
}

func TestParseNumstatZMarksBinaryCounts(t *testing.T) {
	got, err := parseNumstatZ([]byte("-\t-\timage.png\x00"), GroupModified)

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Binary || got[0].Added != 0 || got[0].Deleted != 0 {
		t.Fatalf("binary change = %#v", got)
	}
}

func TestParseNumstatZRejectsTruncatedRecords(t *testing.T) {
	tests := [][]byte{
		[]byte("1\t2\tpath-without-nul"),
		[]byte("1\t2\t\x00old-only\x00"),
		[]byte("x\t2\tbad-count\x00"),
	}
	for _, raw := range tests {
		if _, err := parseNumstatZ(raw, GroupModified); err == nil {
			t.Fatalf("parseNumstatZ(%q) succeeded; want error", raw)
		}
	}
}

func TestParsePathListZPreservesUnusualPaths(t *testing.T) {
	raw := []byte("plain.go\x00with space.go\x00with\ttab.go\x00with\nnewline.go\x00")
	want := []string{"plain.go", "with space.go", "with\ttab.go", "with\nnewline.go"}

	got, err := parsePathListZ(raw)

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestParsePathListZRejectsMissingTerminator(t *testing.T) {
	if _, err := parsePathListZ([]byte("unterminated")); err == nil {
		t.Fatal("missing NUL terminator should fail")
	}
}

func TestGitNumstatFormatMatchesParser(t *testing.T) {
	repo := t.TempDir()
	gitTestRun(t, repo, "init", "-q")
	gitTestRun(t, repo, "config", "user.email", "ledger@example.test")
	gitTestRun(t, repo, "config", "user.name", "Ledger Test")
	writeGitTestFile(t, repo, "ordinary.txt", []byte("before\n"))
	writeGitTestFile(t, repo, "old name.go", []byte("package old\n"))
	writeGitTestFile(t, repo, "deleted.txt", []byte("delete me\n"))
	writeGitTestFile(t, repo, "binary.bin", []byte{0, 1, 2})
	gitTestRun(t, repo, "add", "-A")
	gitTestRun(t, repo, "commit", "-qm", "base")

	writeGitTestFile(t, repo, "ordinary.txt", []byte("before\nafter\n"))
	if err := os.Rename(filepath.Join(repo, "old name.go"), filepath.Join(repo, "new name.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, repo, "binary.bin", []byte{0, 1, 2, 3, 4})
	gitTestRun(t, repo, "add", "-A")

	raw := gitTestOutput(t, repo, "diff", "--cached", "--numstat", "-z", "--find-renames")
	changes, err := parseNumstatZ(raw, GroupStaged)
	if err != nil {
		t.Fatalf("parse real git output: %v\nraw: %q", err, raw)
	}

	paths := make([]string, 0, len(changes))
	var rename Change
	for _, change := range changes {
		paths = append(paths, change.Path)
		if change.Path == "new name.go" {
			rename = change
		}
	}
	sort.Strings(paths)
	wantPaths := []string{"binary.bin", "deleted.txt", "new name.go", "ordinary.txt"}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	if rename.OldPath != "old name.go" {
		t.Fatalf("rename old path = %q, want old name.go", rename.OldPath)
	}
}

func gitTestRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	_ = gitTestOutput(t, repo, args...)
}

func gitTestOutput(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func writeGitTestFile(t *testing.T, repo, name string, data []byte) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
