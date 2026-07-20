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

// The branch name moved out of the ledger entirely — it lives in the Claude
// statusline now. The pinned heading keeps only the right-aligned changed-file
// stamp, and the bottom bar keeps the push/pull commit counts (↑N/↓M) without
// naming the branch either. Runs the shell renderer piped (deterministic
// fixture path) against a repo whose branch is ahead of upstream by 2, and
// asserts the branch name appears NOWHERE in the frame while the stamp and ↑2
// survive.
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
	headingLine, barLine := "", ""
	for _, line := range strings.Split(clean, "\n") {
		if strings.Contains(line, "1 file") && headingLine == "" {
			headingLine = line
		}
		if strings.Contains(line, "↑2") && barLine == "" {
			barLine = line
		}
	}
	if headingLine == "" {
		t.Fatalf("no heading line (\"1 file\" stamp) rendered:\n%q", clean)
	}
	if !strings.HasSuffix(strings.TrimRight(headingLine, " "), "1 file  +1 −0") {
		t.Errorf("stamp must be right-aligned, owning the heading alone; got %q", headingLine)
	}
	if barLine == "" {
		t.Fatalf("bottom bar lost the push count (\"↑2\"):\n%q", clean)
	}
}
