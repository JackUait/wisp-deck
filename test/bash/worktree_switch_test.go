package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeSwitchRepo builds a real repo with one extra checkout and returns
// (main, worktree). git's own porcelain output is the contract under test, so
// these tests drive real git rather than a mock of it.
func worktreeSwitchRepo(t *testing.T, dir string) (string, string) {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(root string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(repo, "init", "-q")
	git(repo, "checkout", "-q", "-b", "main")
	git(repo, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(dir, "repo--feature")
	git(repo, "worktree", "add", "-q", "-b", "feature", wt)
	// git reports resolved paths, and so does the switch path (see _resolve_dir).
	// macOS puts t.TempDir() behind /var → /private/var, so an unresolved
	// expectation here would describe a session the code deliberately normalizes.
	return resolved(t, repo), resolved(t, wt)
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// worktreeSwitchCtx is a relaunch context rooted at projectDir, with the
// account/tool plumbing the switch path reads.
func worktreeSwitchCtx(t *testing.T, dir, projectDir string) string {
	t.Helper()
	claudeCmd := filepath.Join(mockCommand(t, dir, "claude", "exit 0"), "claude")
	return writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=" + claudeCmd,
		"settings=", "filter=", "project_dir=" + projectDir,
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"tools=claude",
		"claude_cmd=" + claudeCmd,
		"tool_pref=" + filepath.Join(dir, "ai-tool"),
		"",
	}, "\n"))
}

// worktreeSwitchMockTmux mocks a three-pane session: %1 is the agent (@gt_ai),
// %2 the ledger (its start command carries compact_view), %3 the spare. Every
// call is logged. sid is the stamped conversation id, "" for none.
func worktreeSwitchMockTmux(t *testing.T, dir, sid, rec string) string {
	t.Helper()
	sessionLine := "-WISP_DECK_CLAUDE_SESSION"
	if sid != "" {
		sessionLine = "WISP_DECK_CLAUDE_SESSION=" + sid
	}
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_SESSION" ]; then
  printf '%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_LIB_DIR" ]; then
  printf 'WISP_DECK_LIB_DIR=%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-message" ]; then printf 'wisp-session\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then
  case "$*" in
    *pane_start_command*)
      printf '%%s\n' '%%1|1|claude'
      printf '%%s\n' '%%2||source /lib/compact-view.sh && compact_view /old'
      printf '%%s\n' '%%3||env -u TMUX tmux -L wdspare-x'
      ;;
    *) printf '%%s\n' '%%1 1' '%%2 ' '%%3 ' ;;
  esac
  exit 0
fi
if [ "$1" = "capture-pane" ]; then printf '❯\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, sessionLine, filepath.Join(projectRoot(t), "lib"), rec))
}

// _session_worktrees turns git's porcelain listing into the switcher's
// "branch:path" rows — main checkout first, exactly as git reports it.
func TestSessionWorktrees_lists_the_main_checkout_first(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)

	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("_session_worktrees %q", repo)), nil)
	assertExitCode(t, code, 0)

	want := fmt.Sprintf("main:%s\nfeature:%s", repo, wt)
	if strings.TrimSpace(out) != want {
		t.Fatalf("worktrees =\n%s\nwant\n%s", strings.TrimSpace(out), want)
	}
}

// A directory that is not a git repository yields nothing rather than failing —
// the switcher simply shows no checkout group.
func TestSessionWorktrees_silent_outside_a_repo(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("_session_worktrees %q", dir)), nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("non-repo produced worktree rows: %q", out)
	}
}

// Picking a checkout rebuilds the tab at it: the relaunch context is retargeted
// (every later switch and the pane-heal watcher read it), the session's path env
// follows (or a crash-restore reopens the OLD checkout), and all three panes
// respawn there.
func TestApplyAccountSwitchChoice_worktree_rebuilds_the_tab_at_the_checkout(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("apply_account_switch_choice tmux %q worktree %q", relaunch, wt)), env)
	assertExitCode(t, code, 0)

	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "project_dir="+wt+"\n") {
		t.Fatalf("relaunch context not retargeted:\n%s", ctx)
	}

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+wt)
	for _, pane := range []string{"%1", "%2", "%3"} {
		want := "respawn-pane -k -t " + pane
		if !strings.Contains(logOut, want) {
			t.Fatalf("pane %s did not respawn:\n%s", pane, logOut)
		}
	}
	// Every respawn must be rooted at the new checkout.
	for _, line := range strings.Split(logOut, "\n") {
		if !strings.HasPrefix(line, "respawn-pane") {
			continue
		}
		if !strings.Contains(line, "-c "+wt) {
			t.Fatalf("respawn not rooted at the new checkout: %q", line)
		}
	}
}

// The agent relaunches FRESH. The stamped conversation belongs to the checkout
// being left, so resuming it would reopen the old tree's transcript in the new
// one.
func TestApplyAccountSwitchChoice_worktree_never_resumes_the_old_conversation(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "11111111-2222-3333-4444-555555555555", rec)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("apply_account_switch_choice tmux %q worktree %q", relaunch, wt)), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertNotContains(t, logOut, "11111111-2222-3333-4444-555555555555")
	assertNotContains(t, logOut, "--resume")
}

// A popup can sit open while worktrees are removed elsewhere. An unvalidated
// path would respawn three panes into an arbitrary directory.
func TestApplyAccountSwitchChoice_worktree_rejects_a_path_that_is_not_a_checkout(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	stray := filepath.Join(dir, "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("apply_account_switch_choice tmux %q worktree %q", relaunch, stray)), env)
	if code == 0 {
		t.Fatal("a path outside the project's worktrees was accepted")
	}

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null || true", rec), nil)
	assertNotContains(t, logOut, "respawn-pane")
	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "project_dir="+repo+"\n") {
		t.Fatalf("rejected choice still retargeted the context:\n%s", ctx)
	}
}

// Choosing the checkout already running is a no-op: rebuilding the tab would
// throw away a live conversation for nothing.
func TestApplyAccountSwitchChoice_worktree_current_checkout_is_a_noop(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("apply_account_switch_choice tmux %q worktree %q", relaunch, repo)), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null || true", rec), nil)
	assertNotContains(t, logOut, "respawn-pane")
}

// The spare pane's inner tmux reads its config at every (re)start, so the config
// must be regenerated at the new checkout — otherwise its + button and prefix+t
// keep opening tabs in the tree the session just left. The outer prefix+t bind
// follows for the same reason.
func TestApplyAccountSwitchChoice_worktree_retargets_the_spare_tabs(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	share := filepath.Dir(relaunch)
	conf := filepath.Join(share, "spare-wisp-session.conf")
	if err := os.WriteFile(conf, []byte("set -g @gt_dir \""+repo+"\"\nbind t new-window -c \""+repo+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("apply_account_switch_choice tmux %q worktree %q", relaunch, wt)), env)
	assertExitCode(t, code, 0)

	body, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `@gt_dir "`+wt+`"`) {
		t.Fatalf("spare config still points at the old checkout:\n%s", body)
	}
	if !strings.Contains(string(body), `bind t new-window -c "`+wt+`"`) {
		t.Fatalf("spare prefix+t still opens the old checkout:\n%s", body)
	}
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "bind-key t run-shell")
	if !strings.Contains(logOut, wt) {
		t.Fatalf("outer prefix+t not rebound to the new checkout:\n%s", logOut)
	}
}

// The popup can only offer checkouts it is told about.
func TestOpenAccountSwitcher_passes_worktrees_and_active_worktree(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "popup.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then printf '%%s\n' "$*" >> %q; exit 0; fi
exit 0`, rec))
	mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n --result-file\n --tools\n --active-worktree\n'`)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--worktrees ")
	assertContains(t, logOut, "--active-worktree "+repo)

	// The list handed to the popup must be the real one.
	listPath := ""
	for _, field := range strings.Fields(logOut) {
		if listPath == "pending" {
			listPath = field
			break
		}
		if field == "--worktrees" {
			listPath = "pending"
		}
	}
	if listPath == "" || listPath == "pending" {
		t.Fatalf("no --worktrees value in:\n%s", logOut)
	}
}

// A binary too old for the flags must simply get no checkout group, not a
// cobra error that kills the whole switcher.
func TestOpenAccountSwitcher_omits_worktree_flags_for_a_legacy_binary(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "popup.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then printf '%%s\n' "$*" >> %q; exit 0; fi
exit 0`, rec))
	mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n --result-file\n --tools\n'`)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertNotContains(t, logOut, "--worktrees")
	assertNotContains(t, logOut, "--active-worktree")
}

// A project with only its main checkout has nothing to switch to, so the flags
// are left off entirely and the popup renders no group.
func TestOpenAccountSwitcher_omits_worktree_flags_for_a_single_checkout(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "solo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"checkout", "-q", "-b", "main"},
		{"commit", "-q", "--allow-empty", "-m", "init"}} {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	rec := filepath.Join(dir, "popup.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then printf '%%s\n' "$*" >> %q; exit 0; fi
exit 0`, rec))
	mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n --result-file\n --tools\n --active-worktree\n'`)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertNotContains(t, logOut, "--worktrees")
}

// The popup reports a checkout as "worktree:<path>"; open_account_switcher must
// route it to the worktree apply path rather than reading it as an account dir.
func TestOpenAccountSwitcher_routes_a_worktree_result(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_LIB_DIR" ]; then
  printf 'WISP_DECK_LIB_DIR=%%s\n' %q; exit 0
fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
if [ "$1" = "display-message" ]; then printf 'wisp-session\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then
  case "$*" in
    *pane_start_command*)
      printf '%%s\n' '%%1|1|claude'
      printf '%%s\n' '%%2||source /lib/compact-view.sh && compact_view /old'
      printf '%%s\n' '%%3||env -u TMUX tmux -L wdspare-x'
      ;;
    *) printf '%%s\n' '%%1 1' '%%2 ' '%%3 ' ;;
  esac
  exit 0
fi
if [ "$1" = "capture-pane" ]; then printf '❯\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then
  rf=$(printf '%%s' "$*" | sed -n 's/.*--result-file \([^ ]*\).*/\1/p')
  [ -n "$rf" ] && printf 'worktree:%%s\n' %q > "$rf"
  exit 0
fi
printf '%%s\n' "$*" >> %q`, filepath.Join(projectRoot(t), "lib"), wt, rec))
	mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n --result-file\n --tools\n --active-worktree\n'`)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)

	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "project_dir="+wt+"\n") {
		t.Fatalf("worktree result did not retarget the session:\n%s", ctx)
	}
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
}

// The pill is the only way into the switch modal, and it was eligible only on
// another login, agent or subscription. A session with one login, one agent and
// no subscriptions still has its project's other checkouts to switch between.
func TestAccountPillEnabled_shows_for_a_project_with_worktrees(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git", "worktrees", "feature"), 0o755); err != nil {
		t.Fatal(err)
	}
	list := writeTempFile(t, dir, "claude-accounts.list", "")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\ntools=claude\nproject_dir="+project+"\n")

	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code != 0 {
		t.Fatalf("a project with worktrees must show the pill (exit 0), got %d: %s", code, out)
	}
}

// A linked worktree is itself a checkout with the main tree to go back to: its
// .git is a FILE, not a directory of its own.
func TestAccountPillEnabled_shows_inside_a_linked_worktree(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "repo--feature")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: /repo/.git/worktrees/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := writeTempFile(t, dir, "claude-accounts.list", "")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\ntools=claude\nproject_dir="+project+"\n")

	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code != 0 {
		t.Fatalf("a linked worktree must show the pill (exit 0), got %d: %s", code, out)
	}
}

// The gate stays closed for an ordinary single-checkout project: a pill there
// opens a modal whose only choice is the one already running.
func TestAccountPillEnabled_hidden_for_a_single_checkout(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	list := writeTempFile(t, dir, "claude-accounts.list", "")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\ntools=claude\nproject_dir="+project+"\n")

	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code == 0 {
		t.Fatalf("single-checkout project showed the pill: %s", out)
	}
}
