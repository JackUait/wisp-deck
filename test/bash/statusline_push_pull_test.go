package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The upstream divergence (↑N commits to push, ↓M to pull) rides the Claude
// statusline, immediately to the RIGHT of the branch name — it moved off the
// ledger's bottom bar, where it competed with the account pill for the row.

// statuslinePushPullRepo builds a repo on branch `main` tracking a bare remote,
// left `ahead` commits ahead of and `behind` commits behind its upstream.
func statuslinePushPullRepo(t *testing.T, ahead, behind int) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	git := func(wd string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", wd}, args...)...)
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git(dir, "init", "-q")
	git(dir, "checkout", "-q", "-b", "main")
	writeTempFile(t, dir, "a.txt", "base\n")
	git(dir, "add", "a.txt")
	git(dir, "commit", "-q", "-m", "init")

	bare := filepath.Join(t.TempDir(), "r.git")
	c := exec.Command("git", "init", "--bare", "-q", bare)
	c.Env = env
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git(dir, "remote", "add", "origin", bare)
	git(dir, "push", "-q", "origin", "main")
	git(dir, "branch", "--set-upstream-to=origin/main", "main")

	if behind > 0 {
		clone := filepath.Join(t.TempDir(), "clone")
		c := exec.Command("git", "clone", "-q", bare, clone)
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		git(clone, "checkout", "-q", "-B", "main", "origin/main")
		for i := 0; i < behind; i++ {
			git(clone, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("remote%d", i))
		}
		git(clone, "push", "-q", "origin", "HEAD:main")
		git(dir, "fetch", "-q", "origin")
	}
	for i := 0; i < ahead; i++ {
		git(dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("local%d", i))
	}
	return dir
}

func runStatuslineCommand(t *testing.T, repoDir string) string {
	t.Helper()
	cmdPath := filepath.Join(projectRoot(t), "templates", "statusline-command.sh")
	script := fmt.Sprintf(`echo '{"current_dir":"%s"}' | bash '%s'`, repoDir, cmdPath)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	return out
}

func TestStatusline_names_push_and_pull_counts_after_the_branch(t *testing.T) {
	out := runStatuslineCommand(t, statuslinePushPullRepo(t, 2, 1))

	plain := stripStatuslineANSI(out)
	if !strings.Contains(plain, "↑2") {
		t.Errorf("statusline lost the push count (↑2): %q", plain)
	}
	if !strings.Contains(plain, "↓1") {
		t.Errorf("statusline lost the pull count (↓1): %q", plain)
	}
	branchAt := strings.Index(plain, "main")
	pushAt := strings.Index(plain, "↑2")
	if branchAt < 0 || pushAt < branchAt {
		t.Errorf("push count must sit to the RIGHT of the branch name: %q", plain)
	}
	if pullAt := strings.Index(plain, "↓1"); pullAt < pushAt {
		t.Errorf("pull count must follow the push count: %q", plain)
	}
}

// The two counts mean opposite things (my commits to send vs. someone else's to
// take), so they must be told apart at a glance: each carries its own SGR color,
// and neither may borrow the branch label's color — a push count painted in the
// branch's cyan reads as part of the branch name rather than as a count.
func TestStatusline_colors_push_and_pull_differently(t *testing.T) {
	out := runStatuslineCommand(t, statuslinePushPullRepo(t, 2, 1))

	push := statuslineColorBefore(t, out, "↑2")
	pull := statuslineColorBefore(t, out, "↓1")
	branch := statuslineColorBefore(t, out, "󰙅")

	if push == pull {
		t.Errorf("push and pull counts share color %q: %q", push, out)
	}
	if push == branch {
		t.Errorf("push count wears the branch color %q: %q", push, out)
	}
	if pull == branch {
		t.Errorf("pull count wears the branch color %q: %q", pull, out)
	}
}

// statuslineColorBefore returns the SGR parameters of the last color escape
// preceding needle (e.g. "01;36"), failing if the text is uncolored.
func statuslineColorBefore(t *testing.T, value, needle string) string {
	t.Helper()
	at := strings.Index(value, needle)
	if at < 0 {
		t.Fatalf("statusline lacks %q: %q", needle, value)
	}
	head := value[:at]
	start := strings.LastIndex(head, "\x1b[")
	if start < 0 {
		t.Fatalf("%q carries no color: %q", needle, value)
	}
	end := strings.Index(head[start:], "m")
	if end < 0 {
		t.Fatalf("unterminated escape before %q: %q", needle, value)
	}
	code := head[start+2 : start+end]
	if code == "00" || code == "0" {
		t.Fatalf("%q is painted with a reset, not a color: %q", needle, value)
	}
	// Compare the HUE, not the raw escape: "36" and "01;36" are different
	// strings but the same cyan on screen, so bolding alone cannot be what
	// distinguishes two counts.
	for _, param := range strings.Split(code, ";") {
		switch param {
		case "", "0", "00", "01", "1", "02", "2", "03", "3", "04", "4":
			continue // attribute (bold/dim/italic/underline), not a color
		default:
			return param
		}
	}
	t.Fatalf("%q carries only attributes, no color: %q", needle, value)
	return ""
}

func TestStatusline_omits_push_pull_when_in_sync(t *testing.T) {
	plain := stripStatuslineANSI(runStatuslineCommand(t, statuslinePushPullRepo(t, 0, 0)))
	if !strings.Contains(plain, "main") {
		t.Fatalf("statusline lost the branch name: %q", plain)
	}
	if strings.ContainsAny(plain, "↑↓") {
		t.Errorf("an in-sync branch must carry no divergence marker: %q", plain)
	}
}

func TestStatusline_omits_push_pull_without_an_upstream(t *testing.T) {
	dir := statuslineCmdSetupGitRepo(t) // no remote, no upstream
	plain := stripStatuslineANSI(runStatuslineCommand(t, dir))
	if !strings.Contains(plain, statuslineCmdBranch) {
		t.Fatalf("statusline lost the branch name: %q", plain)
	}
	if strings.ContainsAny(plain, "↑↓") {
		t.Errorf("a branch with no upstream must carry no divergence marker: %q", plain)
	}
}

func stripStatuslineANSI(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == 0x1b {
			for i < len(value) && value[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(value[i])
	}
	return b.String()
}
