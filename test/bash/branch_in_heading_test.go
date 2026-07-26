package bash_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The branch name moved out of the ledger entirely — and so did the push/pull
// commit counts (↑N/↓M), which now sit right of the branch in the Claude
// statusline. With the top header gone, the changed-file stamp rides the bottom
// bar, right-aligned. Runs the shell renderer piped (deterministic fixture path)
// against a repo whose branch is ahead of upstream by 2, and asserts neither the
// branch name nor the divergence appears anywhere, while the stamp survives.
func TestCompactView_heading_omits_branch_keeps_stamp(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")

	dir := t.TempDir()
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-q")
	git("checkout", "-q", "-b", "hdrbranch")
	writeTempFile(t, dir, "a.txt", "base\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "init")
	bare := filepath.Join(t.TempDir(), "r.git")
	c := exec.Command("git", "init", "--bare", "-q", bare)
	c.Env = gitEnv
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git("remote", "add", "origin", bare)
	git("push", "-q", "origin", "hdrbranch")
	git("branch", "--set-upstream-to=origin/hdrbranch", "hdrbranch")
	git("commit", "-q", "--allow-empty", "-m", "ahead1")
	git("commit", "-q", "--allow-empty", "-m", "ahead2")
	// Dirty a.txt so the ledger has a file row and a "1 file" stamp.
	writeTempFile(t, dir, "a.txt", "base\nDIRTY\n")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-c", "source "+module+" && compact_view "+dir)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "COMPACT_VIEW_INTERVAL=0.1", "TERM=xterm")
	out, _ := cmd.CombinedOutput()

	clean := ansiRE.ReplaceAllString(string(out), "")
	if strings.Contains(clean, "hdrbranch") {
		t.Errorf("ledger still names the branch somewhere:\n%q", clean)
	}
	if strings.Contains(clean, "↑2") {
		t.Errorf("ledger still shows the push count — it rides the statusline now:\n%q", clean)
	}
	// The changed-file stamp still rides the bottom bar, right-aligned.
	barLine := ""
	for _, line := range strings.Split(clean, "\n") {
		if strings.Contains(line, "1 file  +1 −0") {
			barLine = line
			break
		}
	}
	if barLine == "" {
		t.Fatalf("bottom bar lost the changed-file stamp:\n%q", clean)
	}
	// Right-aligned: padding separates it from the pane's left edge (the piped run
	// has no tty, so a "/dev/tty" error may trail the bar line — assert the gap,
	// not a strict prefix).
	stamp := strings.Index(barLine, "1 file  +1 −0")
	if !strings.Contains(barLine[:stamp], "     ") {
		t.Errorf("stamp should be right-aligned (padded away from the left edge); got %q", barLine)
	}
}
