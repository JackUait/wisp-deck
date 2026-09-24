package bash_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const syncDevInstallScript = "scripts/sync-dev-install.sh"

// setupSyncRepo makes a repo holding a few distribution entries and an install
// dir that already has an older copy of them.
func setupSyncRepo(t *testing.T) (repoDir, installDir string) {
	t.Helper()
	dir := t.TempDir()
	repoDir = filepath.Join(dir, "repo")
	installDir = filepath.Join(dir, "install")

	writeTempFile(t, repoDir, "VERSION", "9.9.9\n")
	writeTempFile(t, repoDir, "wrapper.sh", "# wrapper v2\n")
	writeTempFile(t, repoDir, "lib/same.sh", "# unchanged\n")
	writeTempFile(t, repoDir, "lib/changed.sh", "# new body\n")
	writeTempFile(t, repoDir, "internal/not-shipped.go", "package x\n")
	gitIn(t, repoDir, "init", "-q", "-b", "main")
	gitIn(t, repoDir, "add", "-A")
	commitIn(t, repoDir, "init")

	writeTempFile(t, installDir, ".version", "1.0.0\n")
	writeTempFile(t, installDir, "VERSION", "1.0.0\n")
	writeTempFile(t, installDir, "wrapper.sh", "# wrapper v1\n")
	writeTempFile(t, installDir, "lib/same.sh", "# unchanged\n")
	writeTempFile(t, installDir, "lib/changed.sh", "# old body\n")
	writeTempFile(t, installDir, "lib/removed.sh", "# gone at HEAD\n")
	writeTempFile(t, installDir, "templates/gone.txt", "# whole entry gone at HEAD\n")
	writeTempFile(t, installDir, "update.log", "not a distribution file\n")
	return repoDir, installDir
}

func runDevInstallSync(t *testing.T, repoDir string, env []string) (string, int) {
	t.Helper()
	script := filepath.Join(projectRoot(t), syncDevInstallScript)
	return runBashSnippet(t, fmt.Sprintf("cd %q && bash %q", repoDir, script), env)
}

func fileInode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestSyncDevInstall_replaces_by_rename_and_writes_markers(t *testing.T) {
	repoDir, installDir := setupSyncRepo(t)
	changed := filepath.Join(installDir, "lib", "changed.sh")
	same := filepath.Join(installDir, "lib", "same.sh")
	changedIno, sameIno := fileInode(t, changed), fileInode(t, same)

	// A live bash reading this file must keep seeing the old bytes.
	reader, err := os.Open(changed)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	out, code := runDevInstallSync(t, repoDir, buildEnv(t, nil, "WISP_DECK_INSTALL_DIR="+installDir))
	assertExitCode(t, code, 0)

	if fileInode(t, changed) == changedIno {
		t.Errorf("changed file was overwritten in place, not renamed over; output:\n%s", out)
	}
	old, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "# old body\n" {
		t.Errorf("open reader saw %q, want the old body", old)
	}
	if got := readDevInstallFile(t, changed); got != "# new body\n" {
		t.Errorf("changed file = %q", got)
	}
	if fileInode(t, same) != sameIno {
		t.Error("unchanged file was rewritten")
	}
	if got := readDevInstallFile(t, filepath.Join(installDir, "wrapper.sh")); got != "# wrapper v2\n" {
		t.Errorf("wrapper.sh = %q", got)
	}
	for _, rel := range []string{"lib/removed.sh", "templates", "internal"} {
		if _, err := os.Stat(filepath.Join(installDir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist after sync, stat err: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(installDir, "update.log")); err != nil {
		t.Errorf("files outside the distribution must be left alone: %v", err)
	}
	if got := strings.TrimSpace(readDevInstallFile(t, filepath.Join(installDir, ".version"))); got != "9.9.9" {
		t.Errorf(".version = %q, want 9.9.9", got)
	}
	head, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(readDevInstallFile(t, filepath.Join(installDir, ".dev-install"))); got != strings.TrimSpace(string(head)) {
		t.Errorf(".dev-install = %q, want HEAD %q", got, head)
	}
	leftovers, _ := filepath.Glob(filepath.Join(installDir, "*", ".sync.*"))
	if len(leftovers) > 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestSyncDevInstall_refuses_the_default_install_under_test(t *testing.T) {
	repoDir, _ := setupSyncRepo(t)
	home := t.TempDir()
	defaultDir := filepath.Join(home, ".local", "share", "wisp-deck")
	writeTempFile(t, defaultDir, ".version", "1.0.0\n")

	out, code := runDevInstallSync(t, repoDir, buildEnv(t, nil, "HOME="+home))
	if code == 0 {
		t.Fatalf("sync must refuse under WISP_DECK_TESTING=1 without WISP_DECK_INSTALL_DIR; output:\n%s", out)
	}
	assertContains(t, out, "refusing")
	if _, err := os.Stat(filepath.Join(defaultDir, ".dev-install")); !os.IsNotExist(err) {
		t.Error("refused sync still wrote the marker")
	}
}

func readDevInstallFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
