package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Clicking an image in the file ledger must open a PREVIEW popup (wisp-deck-tui
// diff-view --image, raw bytes on stdin) instead of the useless "Binary files
// differ" diff. is_image_file gates the branch by extension — only the formats
// the Go stdlib decodes (png/jpg/jpeg/gif).

func TestIsImageFile_matches_stdlib_decodable_extensions(t *testing.T) {
	yes := []string{"a.png", "b.jpg", "c.jpeg", "d.gif", "UP.PNG", "x/y/z.JpG"}
	for _, p := range yes {
		if _, code := cvFuncArgv(t, "is_image_file", p); code != 0 {
			t.Errorf("is_image_file %q = %d, want 0", p, code)
		}
	}
	no := []string{"a.txt", "b.sh", "png", "a.png.bak", "c.webp", "d.svg", ""}
	for _, p := range no {
		if _, code := cvFuncArgv(t, "is_image_file", p); code == 0 {
			t.Errorf("is_image_file %q = 0, want non-zero", p)
		}
	}
}

// imagePopupCmd runs open_diff_popup against a mocked tmux (which echoes its
// argv) and returns the echoed popup command line.
func imagePopupCmd(t *testing.T, repo, file string) string {
	t.Helper()
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	env := buildEnv(t, []string{binDir})
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")
	script := "source " + module + " && open_diff_popup " + repo + " '" + file + "'"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup: %v\n%s", err, out)
	}
	return string(out)
}

// The ledger loop runs under zsh, where `status` is a READ-ONLY special
// parameter (an alias for $?): a `local status=...` fatals and kills the whole
// file list. The image branch must work under zsh, not just bash.
func TestOpenDiffPopup_image_branch_survives_zsh(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	writeTempFile(t, repo, "shot.png", "fakepngbytes")

	dir := t.TempDir()
	binDir := mockCommand(t, dir, "tmux", `echo "$@"`)
	env := buildEnv(t, []string{binDir})
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")
	script := "source " + module + " && open_diff_popup " + repo + " shot.png"
	cmd := exec.Command("zsh", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("open_diff_popup under zsh: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "read-only variable") {
		t.Fatalf("zsh read-only variable clash:\n%q", got)
	}
	assertContains(t, got, "--image")
	assertContains(t, got, "--status added")
}

func TestOpenDiffPopup_untracked_image_opens_preview_with_added_status(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	writeTempFile(t, repo, "shot.png", "fakepngbytes")

	got := imagePopupCmd(t, repo, "shot.png")
	assertContains(t, got, "--image")
	assertContains(t, got, "--status added")
	assertContains(t, got, "--title shot.png")
	// Raw bytes go straight to the pager: no git diff, no awk header strip.
	if strings.Contains(got, "awk") || strings.Contains(got, "diff HEAD") {
		t.Errorf("image popup must not run the diff pipeline:\n%q", got)
	}
}

func TestOpenDiffPopup_tracked_image_opens_preview_with_modified_status(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, repo, "logo.png", "v1")
	git("add", "logo.png")
	git("commit", "-q", "-m", "init")
	writeTempFile(t, repo, "logo.png", "v2")

	got := imagePopupCmd(t, repo, "logo.png")
	assertContains(t, got, "--image")
	assertContains(t, got, "--status modified")
}

// A deleted image has no bytes to preview; the popup falls back to the diff
// pipeline rather than cat-ing a missing file.
func TestOpenDiffPopup_missing_image_falls_back_to_diff(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	writeTempFile(t, repo, "gone.png", "v1")
	git("add", "gone.png")
	git("commit", "-q", "-m", "init")
	if err := os.Remove(filepath.Join(repo, "gone.png")); err != nil {
		t.Fatalf("rm: %v", err)
	}

	got := imagePopupCmd(t, repo, "gone.png")
	if strings.Contains(got, "--image") {
		t.Errorf("missing image must fall back to the diff pipeline:\n%q", got)
	}
	assertContains(t, got, "diff HEAD")
}

// The image branch keeps the shared popup plumbing: theme forwarding, the
// dimmed backdrop, and the discard decision file.
func TestOpenDiffPopup_image_keeps_theme_backdrop_and_discard(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	writeTempFile(t, repo, "pic.jpeg", "bytes")

	got := imagePopupCmd(t, repo, "pic.jpeg")
	assertContains(t, got, "--ai-tool claude")
	assertContains(t, got, "--backdrop-file")
	assertContains(t, got, "--discard-file")
	assertContains(t, got, "-B")
}
