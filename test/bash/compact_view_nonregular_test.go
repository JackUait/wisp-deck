package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// git lists an untracked symlink as its own entry and never follows it, so a
// link to a directory reaches untracked_numstat as an ordinary path. Reading
// through it counts the TARGET, not the link: `git add <symlink>` stages the
// target path as a one-line blob, so numstat scores every symlink "1".
func TestUntrackedNumstat_counts_a_symlink_as_one_line(t *testing.T) {
	dir := t.TempDir()
	git := discardGitRepo(t, dir)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")

	target := t.TempDir()
	writeTempFile(t, target, "inside.txt", "one\ntwo\nthree\n")
	mustSymlink(t, target, filepath.Join(dir, "dirlink"))
	mustSymlink(t, filepath.Join(target, "inside.txt"), filepath.Join(dir, "filelink"))
	mustSymlink(t, filepath.Join(target, "gone.txt"), filepath.Join(dir, "danglink"))

	out, code := cvFuncArgv(t, "untracked_numstat", dir)
	assertExitCode(t, code, 0)
	for _, name := range []string{"dirlink", "filelink", "danglink"} {
		if !strings.Contains(out, "1\t0\t"+name) {
			t.Errorf("symlink %s must count as 1 added line like git numstat, got:\n%q", name, out)
		}
	}
}

// git does not descend into a nested repository; it reports the directory
// itself, with a trailing slash. Its line count is unknowable, not zero-by-read.
func TestUntrackedNumstat_reports_a_nested_repository_without_reading_it(t *testing.T) {
	dir := t.TempDir()
	git := discardGitRepo(t, dir)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "seed.txt", "x\n")
	git("add", "seed.txt")
	git("commit", "-q", "-m", "init")

	nested := filepath.Join(dir, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	nestedGit := discardGitRepo(t, nested)
	nestedGit("init", "-q")
	writeTempFile(t, nested, "inner.txt", "inner\n")

	out, code := cvFuncArgv(t, "untracked_numstat", dir)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "0\t0\tsub/") {
		t.Errorf("nested repository must be reported uncounted, got:\n%q", out)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
