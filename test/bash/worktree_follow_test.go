package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// followTmuxLog runs follow_agent_checkout against the three-pane mock session
// and returns (relaunch context, tmux call log, exit code).
func followTmuxLog(t *testing.T, dir, relaunch, target string, panes string) (string, string, int) {
	t.Helper()
	rec := filepath.Join(dir, "tmux.log")
	var bin string
	if panes == "" {
		bin = worktreeSwitchMockTmux(t, dir, "", rec)
	} else {
		bin = worktreeFollowMockTmux(t, dir, rec, panes)
	}
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("follow_agent_checkout tmux %q %q", relaunch, target)), env)
	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null", rec), nil)
	return string(ctx), logOut, code
}

// worktreeFollowMockTmux is worktreeSwitchMockTmux with a caller-chosen pane
// table, so a session missing its ledger can be described.
func worktreeFollowMockTmux(t *testing.T, dir, rec, panes string) string {
	t.Helper()
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_LIB_DIR" ]; then
  printf 'WISP_DECK_LIB_DIR=%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-message" ]; then printf 'wisp-session\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' %q; exit 0; fi
if [ "$1" = "capture-pane" ]; then printf '❯\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, filepath.Join(projectRoot(t), "lib"), panes, rec))
}

// The whole point of following the agent: the session's side panes and its
// durable context move to the checkout the agent moved into.
func TestFollowAgentCheckout_retargets_the_session_at_the_new_checkout(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, wt, "")
	assertExitCode(t, code, 0)

	if !strings.Contains(ctx, "project_dir="+wt+"\n") {
		t.Fatalf("relaunch context not retargeted:\n%s", ctx)
	}
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+wt)
	for _, pane := range []string{"%2", "%3"} {
		if !strings.Contains(logOut, "respawn-pane -k -t "+pane) {
			t.Fatalf("pane %s did not respawn:\n%s", pane, logOut)
		}
	}
	for _, line := range strings.Split(logOut, "\n") {
		if strings.HasPrefix(line, "respawn-pane") && !strings.Contains(line, "-c "+wt) {
			t.Fatalf("respawn not rooted at the new checkout: %q", line)
		}
	}
}

// The defining difference from the pill's manual switch. The agent pane holds
// the live conversation that just created the worktree — respawning it would
// throw that conversation away as a side effect of the agent doing its job.
func TestFollowAgentCheckout_never_respawns_the_agent_pane(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)

	_, logOut, code := followTmuxLog(t, dir, relaunch, wt, "")
	assertExitCode(t, code, 0)

	assertNotContains(t, logOut, "respawn-pane -k -t %1")
	// A fresh launch would also re-stamp the session identity or send keys into
	// the agent; neither belongs to a follow.
	assertNotContains(t, logOut, "send-keys")
}

// The agent can cd anywhere. Only a checkout git itself reports for this project
// may move the session — anything else would respawn panes into an arbitrary
// directory.
func TestFollowAgentCheckout_ignores_a_directory_that_is_not_a_checkout(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	stray := filepath.Join(dir, "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := worktreeSwitchCtx(t, dir, repo)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, stray, "")
	if code == 0 {
		t.Fatal("follow_agent_checkout accepted a directory that is not a checkout")
	}
	if !strings.Contains(ctx, "project_dir="+repo+"\n") {
		t.Fatalf("relaunch context was retargeted anyway:\n%s", ctx)
	}
	assertNotContains(t, logOut, "respawn-pane")
	assertNotContains(t, logOut, "set-environment WISP_DECK_PATH")
}

// Leaving a worktree is the same signal in reverse, so the main checkout has to
// be a valid target too.
func TestFollowAgentCheckout_follows_back_to_the_main_checkout(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, wt)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, repo, "")
	assertExitCode(t, code, 0)
	if !strings.Contains(ctx, "project_dir="+repo+"\n") {
		t.Fatalf("relaunch context not retargeted home:\n%s", ctx)
	}
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+repo)
}

// The watcher compares paths it reads from two different sources; the checkout
// already running must cost nothing rather than churn three panes.
func TestFollowAgentCheckout_current_checkout_is_a_noop(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)

	_, logOut, code := followTmuxLog(t, dir, relaunch, repo, "")
	assertExitCode(t, code, 0)
	assertNotContains(t, logOut, "respawn-pane")
}

// _session_side_panes prints "<ledger> <spare>" and the ledger field is EMPTY
// when the session has no ledger pane, so `read -r ledger spare` collapses the
// fields and hands the SPARE's id over as the ledger — respawning the user's
// terminal as a changeset ledger.
func TestFollowAgentCheckout_never_respawns_the_spare_as_a_ledger(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, wt)

	_, logOut, code := followTmuxLog(t, dir, relaunch, repo,
		"%1|1|claude\n%3||env -u TMUX tmux -L wdspare-x")
	assertExitCode(t, code, 0)

	for _, line := range strings.Split(logOut, "\n") {
		if strings.HasPrefix(line, "respawn-pane") && strings.Contains(line, "compact_view") {
			t.Fatalf("a session with no ledger respawned one anyway: %q", line)
		}
	}
}

// worktreeFollowNestedRepo builds the layout EnterWorktree actually produces:
// the worktree lives at <main>/.claude/worktrees/<name>. Returns (main, worktree).
func worktreeFollowNestedRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("checkout", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(repo, ".claude", "worktrees", "feature")
	git("worktree", "add", "-q", "-b", "feature", wt)
	return resolved(t, repo), resolved(t, wt)
}

// ExitWorktree's documented clean exit removes the worktree as it leaves, so the
// tab is asked to follow home FROM a checkout that no longer exists. Validating
// against a deleted anchor reports no checkouts at all, which would refuse the
// snap-back and strand the tab on a dead directory forever: a ledger diffing
// nothing, and a crash-restore that reopens a path that is gone.
func TestFollowAgentCheckout_follows_home_after_the_worktree_is_removed(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeFollowNestedRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, wt)

	out, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", wt).CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, repo, "")
	assertExitCode(t, code, 0)
	if !strings.Contains(ctx, "project_dir="+repo+"\n") {
		t.Fatalf("relaunch context still points at the removed worktree:\n%s", ctx)
	}
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+repo)
	assertContains(t, logOut, "respawn-pane -k -t %2")
}

// The dead anchor must not become a way in. Walking up from a removed checkout
// only ever re-roots the question at the repository that owned it; a directory
// belonging to some other repository is still refused.
func TestFollowAgentCheckout_removed_worktree_does_not_admit_a_foreign_repo(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeFollowNestedRepo(t, dir)
	other, _ := worktreeSwitchRepo(t, t.TempDir())
	relaunch := worktreeSwitchCtx(t, dir, wt)

	out, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", wt).CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}

	_, logOut, code := followTmuxLog(t, dir, relaunch, other, "")
	if code == 0 {
		t.Fatal("follow_agent_checkout accepted a checkout of a different repository")
	}
	assertNotContains(t, logOut, "respawn-pane")
	assertNotContains(t, logOut, "set-environment WISP_DECK_PATH")
}
