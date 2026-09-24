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
	return followTmuxLogWith(t, dir, relaunch, target, followMock{panes: panes})
}

// followMock describes the mocked tab. owner is the WISP_DECK_RELAUNCH_FILE the
// session carries ("" = the relaunch file under test); sessionPath is tmux's
// launch-time #{session_path}.
type followMock struct {
	panes, owner, sessionPath string
}

func followTmuxLogWith(t *testing.T, dir, relaunch, target string, m followMock) (string, string, int) {
	t.Helper()
	rec := filepath.Join(dir, "tmux.log")
	if m.panes == "" {
		m.panes = "%1|1|claude\n%2||source /lib/compact-view.sh && compact_view /old\n%3||env -u TMUX tmux -L wdspare-x"
	}
	if m.owner == "" {
		m.owner = relaunch
	}
	if m.sessionPath == "" {
		m.sessionPath = "wisp-session"
	}
	bin := worktreeFollowMockTmux(t, dir, rec, m)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("follow_agent_checkout tmux %q %q wisp-session", relaunch, target)), env)
	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null", rec), nil)
	return string(ctx), logOut, code
}

// worktreeFollowMockTmux mocks one tab with a caller-chosen pane table, so a
// session missing its ledger can be described.
func worktreeFollowMockTmux(t *testing.T, dir, rec string, m followMock) string {
	t.Helper()
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
orig="$*"
if [ "$1" = "has-session" ]; then exit 0; fi
# Drop a leading "-t <target>" so the matches below see the verb's arguments;
# the log keeps the target so tests can assert the call named its session.
verb="$1"; shift
[ "$1" = "-t" ] && shift 2
set -- "$verb" "$@"
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_LIB_DIR" ]; then
  printf 'WISP_DECK_LIB_DIR=%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_RELAUNCH_FILE" ]; then
  printf 'WISP_DECK_RELAUNCH_FILE=%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-message" ]; then
  case "$*" in
    *session_path*) printf '%%s\n' %q ;;
    *) printf 'wisp-session\n' ;;
  esac
  exit 0
fi
if [ "$1" = "list-panes" ]; then printf '%%b\n' %q; exit 0; fi
if [ "$1" = "capture-pane" ]; then printf '❯\n'; exit 0; fi
printf '%%s\n' "$orig" >> %q`, filepath.Join(projectRoot(t), "lib"), m.owner, m.sessionPath, m.panes, rec))
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
	assertContains(t, logOut, "set-environment -t =wisp-session WISP_DECK_PATH "+wt)
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
	assertNotContains(t, logOut, "set-environment")
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
	assertContains(t, logOut, "set-environment -t =wisp-session WISP_DECK_PATH "+repo)
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
	assertContains(t, logOut, "set-environment -t =wisp-session WISP_DECK_PATH "+repo)
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
	assertNotContains(t, logOut, "set-environment")
}

// followGit runs git in root with a fixed identity.
func followGit(t *testing.T, root string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// followInitRepo makes a one-commit repository at root and returns it resolved.
func followInitRepo(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	followGit(t, root, "init", "-q")
	followGit(t, root, "checkout", "-q", "-b", "main")
	followGit(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	return resolved(t, root)
}

// followCtxPinned is a relaunch context that records the repository the tab
// was launched in, as wrapper.sh writes it.
func followCtxPinned(t *testing.T, dir, projectDir, repo string) string {
	t.Helper()
	relaunch := worktreeSwitchCtx(t, dir, projectDir)
	f, err := os.OpenFile(relaunch, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "project_repo=%s\n", filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	return relaunch
}

func assertFollowRefused(t *testing.T, ctx, logOut string, code int, projectDir string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("follow_agent_checkout accepted the move:\n%s", logOut)
	}
	if !strings.Contains(ctx, "project_dir="+projectDir+"\n") {
		t.Fatalf("relaunch context was retargeted anyway:\n%s", ctx)
	}
	assertNotContains(t, logOut, "respawn-pane")
	assertNotContains(t, logOut, "set-environment")
}

// followNestedForeignRepo builds repo Y holding an ignored, nested repo X at
// y/vendor/x whose worktree the tab was in, then deletes X entirely. Walking up
// from the dead worktree lands in y/vendor, which is inside Y. Returns (tab's
// dead checkout, X's main checkout, a worktree of Y).
func followNestedForeignRepo(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	y := followInitRepo(t, filepath.Join(dir, "y"))
	if err := os.WriteFile(filepath.Join(y, ".git", "info", "exclude"), []byte("vendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	x := followInitRepo(t, filepath.Join(y, "vendor", "x"))
	xwt := filepath.Join(x, ".claude", "worktrees", "feature")
	followGit(t, x, "worktree", "add", "-q", "-b", "feature", xwt)
	ywt := filepath.Join(dir, "y--other")
	followGit(t, y, "worktree", "add", "-q", "-b", "other", ywt)
	if err := os.RemoveAll(x); err != nil {
		t.Fatal(err)
	}
	return xwt, x, resolved(t, ywt)
}

// Walking up from a removed checkout can reach an ENCLOSING repository. The tab
// belongs to X; a worktree of Y must never admit it.
func TestFollowAgentCheckout_refuses_an_enclosing_repo_after_its_own_is_removed(t *testing.T) {
	dir := t.TempDir()
	xwt, x, ywt := followNestedForeignRepo(t, dir)
	relaunch := followCtxPinned(t, dir, xwt, x)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, ywt, "")
	assertFollowRefused(t, ctx, logOut, code, xwt)
}

// The same layout for a context written before the tab recorded its repository.
func TestFollowAgentCheckout_unpinned_context_refuses_an_enclosing_repo(t *testing.T) {
	dir := t.TempDir()
	xwt, _, ywt := followNestedForeignRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, xwt)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, ywt, "")
	assertFollowRefused(t, ctx, logOut, code, xwt)
}

// Two sibling repositories, like two projects under ~/Packages. The tab of A
// was already dragged into a worktree of B (by old code); the record still says
// A, so B can take it no further.
func TestFollowAgentCheckout_refuses_a_sibling_repos_worktree(t *testing.T) {
	dir := t.TempDir()
	a := followInitRepo(t, filepath.Join(dir, "a"))
	b := followInitRepo(t, filepath.Join(dir, "b"))
	bwt := filepath.Join(dir, "b-wt")
	followGit(t, b, "worktree", "add", "-q", "-b", "feature", bwt)
	bwt = resolved(t, bwt)

	for name, projectDir := range map[string]string{"from its own checkout": a, "from the foreign worktree": bwt} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			relaunch := followCtxPinned(t, d, projectDir, a)
			target := bwt
			if projectDir == bwt {
				target = b
			}
			ctx, logOut, code := followTmuxLog(t, d, relaunch, target, "")
			assertFollowRefused(t, ctx, logOut, code, projectDir)
		})
	}
}

// Worktrees kept outside the main checkout (<parent>/.x-undo/wt/<name>): once
// ExitWorktree removes one, nothing above it is a repository, so only the
// recorded identity can admit the way home.
func TestFollowAgentCheckout_follows_home_from_a_removed_out_of_tree_worktree(t *testing.T) {
	dir := t.TempDir()
	x := followInitRepo(t, filepath.Join(dir, "x"))
	wt := filepath.Join(dir, ".x-undo", "wt", "integration")
	followGit(t, x, "worktree", "add", "-q", "-b", "integration", wt)
	wt = resolved(t, wt)
	relaunch := followCtxPinned(t, dir, wt, x)
	followGit(t, x, "worktree", "remove", "--force", wt)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, x, "")
	assertExitCode(t, code, 0)
	if !strings.Contains(ctx, "project_dir="+x+"\n") {
		t.Fatalf("relaunch context still points at the removed worktree:\n%s", ctx)
	}
	assertContains(t, logOut, "set-environment -t =wisp-session WISP_DECK_PATH "+x)
}

// The record is the fixed point every later follow is checked against, so a
// follow must never move it.
func TestFollowAgentCheckout_never_rewrites_the_recorded_repo(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := followCtxPinned(t, dir, repo, repo)

	ctx, _, code := followTmuxLog(t, dir, relaunch, wt, "")
	assertExitCode(t, code, 0)
	assertContains(t, ctx, "project_dir="+wt+"\n")
	if n := strings.Count(ctx, "project_repo="); n != 1 || !strings.Contains(ctx, "project_repo="+repo+"/.git\n") {
		t.Fatalf("recorded repo changed by a follow:\n%s", ctx)
	}
}

// A context written by older code has no record. It still follows its own
// worktree, with tmux's launch-time session path standing in for the record.
func TestFollowAgentCheckout_unpinned_context_follows_its_own_worktree(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)

	ctx, _, code := followTmuxLogWith(t, dir, relaunch, wt, followMock{sessionPath: repo})
	assertExitCode(t, code, 0)
	assertContains(t, ctx, "project_dir="+wt+"\n")
}

// The incident shape for a live, unpinned tab: old code already moved its
// project_dir into another repository. The session path tmux got at launch
// still names the tab's own, so the foreign repository is refused.
func TestFollowAgentCheckout_unpinned_context_trusts_the_launch_path_over_project_dir(t *testing.T) {
	dir := t.TempDir()
	a := followInitRepo(t, filepath.Join(dir, "a"))
	b := followInitRepo(t, filepath.Join(dir, "b"))
	bwt := filepath.Join(dir, "b-wt")
	followGit(t, b, "worktree", "add", "-q", "-b", "feature", bwt)
	bwt = resolved(t, bwt)
	relaunch := worktreeSwitchCtx(t, dir, bwt)

	ctx, logOut, code := followTmuxLogWith(t, dir, relaunch, b, followMock{sessionPath: a})
	assertFollowRefused(t, ctx, logOut, code, bwt)
}

// Defense in depth: the session named must be the one this relaunch file
// belongs to, or a mis-targeted call would move another tab.
func TestFollowAgentCheckout_refuses_a_session_that_owns_another_relaunch_file(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := followCtxPinned(t, dir, repo, repo)

	ctx, logOut, code := followTmuxLogWith(t, dir, relaunch, wt,
		followMock{owner: filepath.Join(dir, "relaunch-other")})
	assertFollowRefused(t, ctx, logOut, code, repo)
}

// wrapper.sh records the repository with the launch context.
func TestWriteRelaunchContext_records_the_project_repo(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	out := filepath.Join(dir, "relaunch")

	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude "" "" %q %q "" "" "" "" "" "" "" "$(_repo_identity %q)"`,
		out, wt, dir, wt)), nil)
	assertExitCode(t, code, 0)
	ctx, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// A worktree and its main checkout share one identity.
	assertContains(t, string(ctx), "project_repo="+repo+"/.git\n")
}

// A mid-session tool switch rewrites the whole file without the record; it
// must carry the record over, or the first switch would unpin the tab.
func TestWriteRelaunchContext_rewrite_keeps_the_recorded_repo(t *testing.T) {
	dir := t.TempDir()
	out := writeTempFile(t, dir, "relaunch", "project_dir=/p\nproject_repo=/p/.git\n")

	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q codex codex "" "" /p %q`, out, dir)), nil)
	assertExitCode(t, code, 0)
	ctx, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(ctx), "tool=codex\n")
	assertContains(t, string(ctx), "project_repo=/p/.git\n")
}

// A non-git project records an empty identity, so it can never follow.
func TestWriteRelaunchContext_non_git_project_records_no_repo(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "relaunch")
	plain := t.TempDir()

	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude "" "" %q %q "" "" "" "" "" "" "" "$(_repo_identity %q)"`,
		out, plain, dir, plain)), nil)
	assertExitCode(t, code, 0)
	ctx, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(ctx), "\nproject_repo=\n")
}

// The launch is the one place the record is written.
func TestWrapper_records_the_project_repo_at_launch(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, `WISP_DECK_RELAUNCH_FILE="$SHARE_DIR/relaunch-`)
	end := strings.Index(src, "export WISP_DECK_RELAUNCH_FILE")
	if start < 0 || end < start {
		t.Fatal("wrapper.sh relaunch-context block not found")
	}
	assertContains(t, src[start:end], `_repo_identity "$PROJECT_DIR"`)
}

// A subdirectory shares its repository's identity, but only a checkout root may
// move the tab.
func TestFollowAgentCheckout_refuses_a_subdirectory_of_its_own_repo(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := followCtxPinned(t, dir, repo, repo)

	ctx, logOut, code := followTmuxLog(t, dir, relaunch, sub, "")
	assertFollowRefused(t, ctx, logOut, code, repo)
}
