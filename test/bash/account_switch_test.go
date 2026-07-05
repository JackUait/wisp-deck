package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompactViewLoadsAccountDeps_underZsh reproduces the pane's real runtime: the
// compact-view ledger is launched via `zsh -c 'source compact-view.sh && ...'`
// (the user's $SHELL), where BASH_SOURCE is empty. compact-view.sh must still load
// its account-switch deps so the switcher pill can render. wrapper.sh exports
// WISP_DECK_LIB_DIR for the pane; the script uses it to locate its siblings.
func TestCompactViewLoadsAccountDeps_underZsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	script := fmt.Sprintf(
		`source %q; type account_pill_enabled >/dev/null 2>&1 && echo DEFINED || echo MISSING`,
		filepath.Join(lib, "compact-view.sh"))
	cmd := exec.Command("zsh", "-c", script)
	cmd.Env = append(os.Environ(), "WISP_DECK_LIB_DIR="+lib)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh source failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "DEFINED") {
		t.Fatalf("compact-view.sh did not load account-switch deps under zsh (pill helpers missing): %s", out)
	}
}

// sourceAccountSwitch builds a bash snippet that sources account-switch.sh and
// its sibling deps from the repo's lib/ before running body. account-switch.sh
// leans on statusline.sh (gt_* helpers), claude-accounts.sh (pointer helpers),
// and tmux-session.sh (build_ai_launch_cmd).
func accountSwitchSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	return fmt.Sprintf(
		"source %q && source %q && source %q && source %q && %s",
		filepath.Join(lib, "statusline.sh"),
		filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"),
		filepath.Join(lib, "account-switch.sh"),
		body,
	)
}

func TestAccountPillEnabled_shows_when_relaunch_file_and_two_accounts(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code != 0 {
		t.Fatalf("expected pill enabled (exit 0), got %d: %s", code, out)
	}
}

func TestAccountPillEnabled_hidden_without_relaunch_file(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", "", list)), nil)
	if code == 0 {
		t.Fatalf("expected pill hidden (nonzero exit) with empty relaunch file: %s", out)
	}
}

func TestAccountPillEnabled_hidden_with_single_account(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "# only comments\n")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code == 0 {
		t.Fatalf("expected pill hidden with no managed accounts: %s", out)
	}
}

func TestAccountPill_renders_label_color_and_width(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`account_pill "Personal" "208"`), nil)
	assertExitCode(t, code, 0)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (pill + width), got %d: %q", len(lines), out)
	}
	assertContains(t, lines[0], "Personal")
	assertContains(t, lines[0], "38;5;208") // account color escape
	assertContains(t, lines[0], "󰀄")        // account glyph 󰀄
	// visible width = leading space + glyph + space + len("Personal") = 3 + 8 = 11
	if strings.TrimSpace(lines[1]) != "11" {
		t.Fatalf("expected width 11, got %q", lines[1])
	}
}

func TestAccountCurrent_active_managed_account(t *testing.T) {
	dir := t.TempDir()
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:170\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_current %q %q %q %q", pointer, list, defLabel, colors)), nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "Work\t170" {
		t.Fatalf("expected 'Work\\t170', got %q", got)
	}
}

func TestAccountCurrent_default_when_no_pointer(t *testing.T) {
	dir := t.TempDir()
	pointer := filepath.Join(dir, "claude-account") // absent = Default
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "default:39\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_current %q %q %q %q", pointer, list, defLabel, colors)), nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "Default\t39" {
		t.Fatalf("expected 'Default\\t39', got %q", got)
	}
}

func TestFindAIPane_picks_marked_pane(t *testing.T) {
	dir := t.TempDir()
	// Mock tmux: list-panes emits "pane_id gt_ai" lines; the AI pane carries 1.
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "list-panes" ]; then
  printf '%s\n' "%0 0" "%1 1" "%2 0"
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "find_ai_pane tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "%1" {
		t.Fatalf("expected AI pane '%%1', got %q", out)
	}
}

func TestBuildSwitchLaunchCmd_managed_account_sets_config_dir_and_continues(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude opencode "/cfg/settings.json" "" "/proj" "/cfg/claude-accounts/work"`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, `CLAUDE_CONFIG_DIR="/cfg/claude-accounts/work"`)
	assertContains(t, out, "claude -c")
	assertContains(t, out, `--settings "/cfg/settings.json"`)
}

func TestBuildSwitchLaunchCmd_default_account_leaves_config_dir_unset(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude opencode "/cfg/settings.json" "" "/proj" ""`), nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "CLAUDE_CONFIG_DIR=")
	assertContains(t, out, "claude -c")
}

func TestBuildSwitchLaunchCmd_resumes_exact_session_when_stamped(t *testing.T) {
	// A stamped session id must produce `--resume <id>` (the exact conversation),
	// with `-c` only as the fallback step — not the other way round. This keeps
	// the switched pane on ITS conversation in a multi-tab/window project, where
	// bare `-c` (most-recent-in-cwd) could grab a sibling tab's conversation.
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`build_switch_launch_cmd claude claude opencode "/cfg/settings.json" "" "/proj" "/cfg/claude-accounts/work" "sess-xyz-1"`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "--resume sess-xyz-1")
}

func TestCurrentAISession_reads_stamped_env(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_SESSION=abc-42\n'; exit 0; fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "abc-42" {
		t.Fatalf("expected stamped session 'abc-42', got %q", out)
	}
}

func TestCurrentAISession_empty_when_unset(t *testing.T) {
	dir := t.TempDir()
	// tmux prints `-NAME` (with a leading dash) for an unset variable.
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashSnippet(t, accountSwitchSnippet(t, "current_ai_session tmux"), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty session when unset, got %q", out)
	}
}

func TestRelaunchAIPane_resumes_exact_stamped_conversation(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-account", "work\n")
	acctDir := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acctDir, ".keep", "")
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude",
		"claude_cmd=claude",
		"opencode_cmd=opencode",
		"settings=/cfg/settings.json",
		"filter=",
		"project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_SESSION=sess-abc-123\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--resume sess-abc-123")
}

func TestWriteRelaunchContext_writes_all_keys(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "relaunch")
	cfg := filepath.Join(dir, "cfg")
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude opencode "/cfg/settings.json" "flt -- " "/proj" %q`,
		out, cfg)), nil)
	assertExitCode(t, code, 0)
	body, _ := runBashSnippet(t, fmt.Sprintf("cat %q", out), nil)
	assertContains(t, body, "tool=claude")
	assertContains(t, body, "claude_cmd=claude")
	assertContains(t, body, "opencode_cmd=opencode")
	assertContains(t, body, "settings=/cfg/settings.json")
	assertContains(t, body, "filter=flt -- ") // trailing space preserved
	assertContains(t, body, "project_dir=/proj")
	assertContains(t, body, "accounts_dir="+filepath.Join(cfg, "claude-accounts"))
	assertContains(t, body, "pointer="+filepath.Join(cfg, "claude-account"))
	assertContains(t, body, "list="+filepath.Join(cfg, "claude-accounts.list"))
	assertContains(t, body, "colors="+filepath.Join(cfg, "claude-account-colors"))
	assertContains(t, body, "default_label="+filepath.Join(cfg, "claude-account-default-label"))
}

// A round-trip: write the context, then relaunch reads it back and respawns the
// AI pane under the account the pointer names.
func TestWriteRelaunchContext_round_trips_through_relaunch(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg")
	writeTempFile(t, cfg, "claude-account", "work\n")
	writeTempFile(t, filepath.Join(cfg, "claude-accounts", "work"), ".keep", "")
	out := filepath.Join(dir, "relaunch")
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude opencode "/cfg/settings.json" "" "/proj" %q && relaunch_ai_pane tmux %q`,
		out, cfg, out)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+filepath.Join(cfg, "claude-accounts", "work")+`"`)
}

func TestRelaunchAIPane_respawns_with_new_account(t *testing.T) {
	dir := t.TempDir()
	// The switcher already wrote the pointer to "work"; its config dir exists.
	writeTempFile(t, dir, "claude-account", "work\n")
	acctDir := filepath.Join(dir, "claude-accounts", "work")
	writeTempFile(t, acctDir, ".keep", "")
	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude",
		"claude_cmd=claude",
		"opencode_cmd=opencode",
		"settings=/cfg/settings.json",
		"filter=",
		"project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then
  printf '%%s\n' "%%0 0" "%%1 1"
  exit 0
fi
printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("relaunch_ai_pane tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, "-k")
	assertContains(t, logOut, "%1")
	assertContains(t, logOut, `CLAUDE_CONFIG_DIR="`+filepath.Join(dir, "claude-accounts", "work")+`"`)
	assertContains(t, logOut, "claude -c")
	_ = out
}

// Top-level regression guard for the mid-session account switch: relaunch_ai_pane
// runs FROM the compact-view pane, whose shell is ZSH (the user's $SHELL). The
// original bug was a bash-glob idiom in a helper it calls (sync_claude_shared_state's
// `"$dest".migrating.*`) that, under zsh's default `nomatch`, aborted the whole pane
// — so the file-list view was killed instead of restoring, and the AI pane never
// even respawned. This drives the ENTIRE relaunch path under zsh with a real (empty)
// HOME/.claude and account dir, so ANY fatal unmatched glob anywhere in the flow
// (now or added later) fails this test. Success = the pane survived to run the tmux
// respawn.
func TestRelaunchAIPane_survives_full_path_under_zsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	root := projectRoot(t)
	lib := filepath.Join(root, "lib")
	dir := t.TempDir()

	// The switcher already wrote the pointer to "work"; its config dir exists.
	writeTempFile(t, dir, "claude-account", "work\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "work"), ".keep", "")
	// A real HOME/.claude so the shared-state/settings sync actually runs its
	// glob loops (the abort site) rather than short-circuiting on a missing dir.
	home := filepath.Join(dir, "home")
	writeTempFile(t, filepath.Join(home, ".claude"), ".keep", "")

	relaunch := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "claude_cmd=claude", "opencode_cmd=opencode",
		"settings=/cfg/settings.json", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"",
	}, "\n"))
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_SESSION=sess-1\n'; exit 0; fi
printf '%%s\n' "$*" >> %q`, rec))

	// Mirror the pane's real runtime: zsh sourcing the deps (account-switch.sh
	// last — it defines relaunch_ai_pane and leans on the others), then the switch.
	script := fmt.Sprintf(
		"source %q && source %q && source %q && source %q && source %q && "+
			"relaunch_ai_pane tmux %q && print DONE-RELAUNCH-SURVIVED",
		filepath.Join(lib, "statusline.sh"),
		filepath.Join(lib, "claude-accounts.sh"),
		filepath.Join(lib, "tmux-session.sh"),
		filepath.Join(lib, "claude-shared-settings.sh"),
		filepath.Join(lib, "account-switch.sh"),
		relaunch)
	cmd := exec.Command("zsh", "-c", script)
	cmd.Env = append(buildEnv(t, []string{bin}), "HOME="+home)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	assertExitCode(t, code, 0)
	if !strings.Contains(string(out), "DONE-RELAUNCH-SURVIVED") {
		t.Fatalf("the mid-session switch aborted the zsh pane (file-list view would vanish); output:\n%s", out)
	}
	// And the pane actually got respawned under the new account — the switch's point.
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	assertContains(t, logOut, filepath.Join(dir, "claude-accounts", "work"))
}
