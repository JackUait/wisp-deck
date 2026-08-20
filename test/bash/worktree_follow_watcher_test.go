package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// followWatcherGeneration builds an attention generation directory holding the
// working-directory sidecar the Claude attention runtime publishes, and returns
// its state-file path (what the watcher already holds).
func followWatcherGeneration(t *testing.T, dir, cwd string) string {
	t.Helper()
	generation := filepath.Join(dir, "attention", "generation.abc123")
	if err := os.MkdirAll(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	if cwd != "" {
		if err := os.WriteFile(filepath.Join(generation, "cwd"), []byte(cwd+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(generation, "state")
}

// followWatcherRun drives one watcher observation and returns the tmux call log.
func followWatcherRun(t *testing.T, dir, stateFile, relaunch string, calls int) string {
	t.Helper()
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	lib := filepath.Join(projectRoot(t), "lib")

	body := strings.Repeat(fmt.Sprintf(
		"attention_watcher_follow_agent tmux %q %q %q\n", stateFile, relaunch, lib), calls)
	_, code := runBashSnippet(t, fmt.Sprintf(
		"source %q && source %q && attention_watcher_reset && %s",
		filepath.Join(lib, "tui.sh"), filepath.Join(lib, "tab-title-watcher.sh"), body), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null", rec), nil)
	return logOut
}

// The goal end to end: the agent moved into a worktree, so the tab moves with it.
func TestAttentionWatcherFollowAgent_moves_the_tab_into_the_agents_worktree(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	stateFile := followWatcherGeneration(t, dir, wt)

	logOut := followWatcherRun(t, dir, stateFile, relaunch, 1)
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+wt)
	assertContains(t, logOut, "respawn-pane -k -t %2")

	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "project_dir="+wt+"\n") {
		t.Fatalf("relaunch context not retargeted:\n%s", ctx)
	}
}

// The agent pane is where the conversation that made the worktree is running.
func TestAttentionWatcherFollowAgent_never_respawns_the_agent_pane(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	stateFile := followWatcherGeneration(t, dir, wt)

	logOut := followWatcherRun(t, dir, stateFile, relaunch, 1)
	assertNotContains(t, logOut, "respawn-pane -k -t %1")
}

// This runs on the attention tick, twice a second for the life of a session.
// An agent sitting in the checkout the tab already shows must cost nothing.
func TestAttentionWatcherFollowAgent_is_silent_while_the_agent_stays_put(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	stateFile := followWatcherGeneration(t, dir, repo)

	if logOut := followWatcherRun(t, dir, stateFile, relaunch, 5); strings.TrimSpace(logOut) != "" {
		t.Fatalf("a stationary agent produced tmux calls:\n%s", logOut)
	}
}

// A directory the follow refuses (the agent cd'd out of the project) must be
// attempted ONCE. Retrying it every tick would spawn a shell twice a second for
// as long as the agent stayed there.
func TestAttentionWatcherFollowAgent_attempts_each_directory_once(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	stray := filepath.Join(dir, "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := worktreeSwitchCtx(t, dir, repo)
	stateFile := followWatcherGeneration(t, dir, stray)

	logOut := followWatcherRun(t, dir, stateFile, relaunch, 6)
	// A refused follow reaches tmux only through the library probes it makes
	// before validating; it must never respawn a pane or move the session env.
	assertNotContains(t, logOut, "respawn-pane")
	assertNotContains(t, logOut, "set-environment WISP_DECK_PATH")

	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "project_dir="+repo+"\n") {
		t.Fatalf("relaunch context was retargeted anyway:\n%s", ctx)
	}
}

// An absent sidecar is what every non-Claude agent looks like, and what a Claude
// session looks like before its first poll. Neither has moved anywhere.
func TestAttentionWatcherFollowAgent_ignores_a_missing_sidecar(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	stateFile := followWatcherGeneration(t, dir, "")

	if logOut := followWatcherRun(t, dir, stateFile, relaunch, 3); strings.TrimSpace(logOut) != "" {
		t.Fatalf("a session with no sidecar produced tmux calls:\n%s", logOut)
	}
}

// The sidecar is a file on disk; a truncated or junk read must not be handed to
// a shell that respawns panes into it.
func TestAttentionWatcherFollowAgent_ignores_a_sidecar_that_is_not_an_absolute_path(t *testing.T) {
	dir := t.TempDir()
	repo, _ := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)

	for _, junk := range []string{"", "repo--feature", "  ", "; rm -rf /"} {
		stateFile := followWatcherGeneration(t, t.TempDir(), "placeholder")
		if err := os.WriteFile(filepath.Join(filepath.Dir(stateFile), "cwd"),
			[]byte(junk+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if logOut := followWatcherRun(t, t.TempDir(), stateFile, relaunch, 1); strings.TrimSpace(logOut) != "" {
			t.Fatalf("sidecar %q produced tmux calls:\n%s", junk, logOut)
		}
	}
}

// Leaving the worktree is the same signal in reverse.
func TestAttentionWatcherFollowAgent_follows_the_agent_back_out(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, wt)
	stateFile := followWatcherGeneration(t, dir, repo)

	logOut := followWatcherRun(t, dir, stateFile, relaunch, 1)
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+repo)
}

// The watcher is started before the session has a relaunch context to follow,
// and older sessions have no library path stamped at all.
func TestAttentionWatcherFollowAgent_is_disabled_without_its_inputs(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	stateFile := followWatcherGeneration(t, dir, wt)
	lib := filepath.Join(projectRoot(t), "lib")
	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	env := buildEnv(t, []string{bin}, "HOME="+dir)

	for name, call := range map[string]string{
		"no relaunch context": fmt.Sprintf("attention_watcher_follow_agent tmux %q '' %q", stateFile, lib),
		"no library":          fmt.Sprintf("attention_watcher_follow_agent tmux %q %q ''", stateFile, relaunch),
		"no state file":       fmt.Sprintf("attention_watcher_follow_agent tmux '' %q %q", relaunch, lib),
		"no tmux":             fmt.Sprintf("attention_watcher_follow_agent '' %q %q %q", stateFile, relaunch, lib),
	} {
		_, code := runBashSnippet(t, fmt.Sprintf(
			"source %q && source %q && attention_watcher_reset && %s",
			filepath.Join(lib, "tui.sh"), filepath.Join(lib, "tab-title-watcher.sh"), call), env)
		if code != 0 {
			t.Fatalf("%s: exit code %d, want a quiet no-op", name, code)
		}
		logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null", rec), nil)
		if strings.TrimSpace(logOut) != "" {
			t.Fatalf("%s produced tmux calls:\n%s", name, logOut)
		}
	}
}

// The chain end to end, through the real tick: an attention observation whose
// published working directory is a worktree moves the tab there. Every other
// test in this file calls attention_watcher_follow_agent directly, so without
// this one the tick could stop calling it and nothing would notice.
func TestAttentionWatcherTick_follows_the_agent_into_a_worktree(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)

	root := filepath.Join(dir, "attention")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	generation := "generation.follow1"
	state := writeAttentionState(t, root, generation, "1", "working", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	if err := os.WriteFile(filepath.Join(root, generation, "cwd"), []byte(wt+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := filepath.Join(dir, "tmux.log")
	bin := worktreeSwitchMockTmux(t, dir, "", rec)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	lib := filepath.Join(projectRoot(t), "lib")

	_, code := runBashSnippet(t, fmt.Sprintf(`
source %q
source %q
apply_tab_title() { :; }
keep_awake_tick() { :; }
attention_watcher_reset
attention_watcher_tick session project full tmux %q %q %q %q
wait
`,
		filepath.Join(lib, "tui.sh"), filepath.Join(lib, "tab-title-watcher.sh"),
		descriptor, filepath.Join(dir, "config"), relaunch, lib), env)
	assertExitCode(t, code, 0)

	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q 2>/dev/null", rec), nil)
	assertContains(t, logOut, "set-environment WISP_DECK_PATH "+wt)
	assertNotContains(t, logOut, "respawn-pane -k -t %1")

	ctx, err := os.ReadFile(relaunch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), "project_dir="+wt+"\n") {
		t.Fatalf("the tick never retargeted the session:\n%s", ctx)
	}
}
