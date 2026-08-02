package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/tui"
)

// Clicking an image in the file ledger must open a PREVIEW popup (wisp-deck-tui
// diff-view --image, raw bytes on stdin) instead of the useless "Binary files
// differ" diff. is_image_file gates the branch by extension — every popular
// image format, since the pager decodes each of them (pure Go where a decoder
// exists, macOS ImageIO via sips for the rest).

func TestIsImageFile_matches_every_popular_image_extension(t *testing.T) {
	yes := []string{
		"a.png", "a.apng", "b.jpg", "c.jpeg", "c.jpe", "c.jfif", "d.gif",
		"e.webp", "f.bmp", "f.dib", "g.tif", "g.tiff", "h.ico", "h.icns",
		"i.heic", "i.heif", "j.avif", "k.svg",
		"UP.PNG", "x/y/z.JpG", "deep/dir/Shot.HEIC", "logo.SVG",
	}
	for _, p := range yes {
		if _, code := cvFuncArgv(t, "is_image_file", p); code != 0 {
			t.Errorf("is_image_file %q = %d, want 0", p, code)
		}
	}
	no := []string{"a.txt", "b.sh", "png", "a.png.bak", "c.mp4", "d.pdf", ""}
	for _, p := range no {
		if _, code := cvFuncArgv(t, "is_image_file", p); code == 0 {
			t.Errorf("is_image_file %q = 0, want non-zero", p)
		}
	}
}

// The shell and Go renderers each own a copy of the extension list (one is a
// case glob, the other a map), and a pane picks between them by binary
// capability — so a format added to one and not the other previews for half the
// users. This pins them to each other.
func TestIsImageFile_matches_the_Go_renderers_list(t *testing.T) {
	for _, ext := range tui.PreviewableImageExtensions() {
		if _, code := cvFuncArgv(t, "is_image_file", "sample"+ext); code != 0 {
			t.Errorf("Go previews %q but is_image_file rejects it", ext)
		}
	}
}

// A byte-size delta is the right row for a raster image (its line count is
// meaningless) but the WRONG one for an SVG: git tracks it as text, reports
// real line counts, and never hydrates a byte size — so sizing it would print
// "±0" on every edit. The preview gate and the row-format gate are therefore
// different sets.
func TestIsBinaryImageFile_excludes_text_vector_formats(t *testing.T) {
	yes := []string{"a.png", "e.webp", "i.heic", "j.avif", "h.ico", "g.tiff"}
	for _, p := range yes {
		if _, code := cvFuncArgv(t, "is_binary_image_file", p); code != 0 {
			t.Errorf("is_binary_image_file %q = %d, want 0", p, code)
		}
	}
	no := []string{"k.svg", "logo.SVG", "a.txt", ""}
	for _, p := range no {
		if _, code := cvFuncArgv(t, "is_binary_image_file", p); code == 0 {
			t.Errorf("is_binary_image_file %q = 0, want non-zero", p)
		}
	}
}

// Every popular format reaches the preview branch of the popup, not just PNG.
func TestOpenDiffPopup_previews_every_popular_format(t *testing.T) {
	for _, name := range []string{
		"a.webp", "b.avif", "c.heic", "d.bmp", "e.tiff", "f.ico", "g.svg", "h.gif",
	} {
		repo := t.TempDir()
		git := discardGitRepo(t, repo)
		git("init", "-q")
		writeTempFile(t, repo, name, "bytes")

		got := imagePopupCmd(t, repo, name)
		if !strings.Contains(got, "--image") {
			t.Errorf("%s did not open the preview popup:\n%q", name, got)
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

// The pager only sees bytes on stdin; opening the image in the macOS Preview
// app needs the on-disk location, so the popup passes --path <dir>/<file>.
func TestOpenDiffPopup_image_passes_path_for_preview_app(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	writeTempFile(t, repo, "shot.png", "fakepngbytes")

	got := imagePopupCmd(t, repo, "shot.png")
	assertContains(t, got, "--path "+repo+"/shot.png")
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

// The hi-res overlay reaches the terminal as kitty-graphics APC escapes, which
// tmux eats unless allow-passthrough is enabled; the image branch must switch
// it on before opening the popup.
func TestOpenDiffPopup_image_enables_tmux_passthrough(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	writeTempFile(t, repo, "pic.png", "bytes")

	got := imagePopupCmd(t, repo, "pic.png")
	assertContains(t, got, "allow-passthrough")
}

// tmux 3.6 popups swallow DCS passthrough (panes forward it; popups don't —
// verified empirically via OSC52), so the pager must write kitty graphics
// straight to the tmux CLIENT tty. The image branch resolves it and passes it
// via --gfx-tty.
func TestOpenDiffPopup_image_passes_client_tty_for_graphics(t *testing.T) {
	repo := t.TempDir()
	git := discardGitRepo(t, repo)
	git("init", "-q")
	writeTempFile(t, repo, "pic.png", "bytes")

	got := imagePopupCmd(t, repo, "pic.png")
	assertContains(t, got, "--gfx-tty")
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
