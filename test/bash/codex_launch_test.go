package bash_test

import (
	"strings"
	"testing"
)

// Codex needs its own arm in build_ai_launch_cmd rather than the `default` one.
// The default arm is claude's: it prepends `env -u CLAUDE_CONFIG_DIR` (or a
// CLAUDE_CONFIG_DIR= assignment) and appends `--settings <path>` — neither of
// which `codex` accepts. Panes are created with `-c "$PROJECT_DIR"`, so codex
// also takes no positional path the way opencode does.

func codexLaunchCmd(t *testing.T, env []string, extra string) string {
	t.Helper()
	if env == nil {
		// buildEnv strips inherited WISP_DECK* vars; a bare nil would let the
		// surrounding wisp-deck pane leak WISP_DECK_RESUME etc. into the test.
		env = buildEnv(t, nil)
	}
	args := []string{"codex", "/usr/bin/codex"}
	if extra != "" {
		args = append(args, extra)
	}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd", args, env)
	assertExitCode(t, code, 0)
	return strings.TrimSpace(out)
}

func TestBuildAiLaunchCmd_codex_is_the_bare_command(t *testing.T) {
	if got := codexLaunchCmd(t, nil, ""); got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// A claude settings file is active for the session, but codex must not receive
// --settings: it would fail to parse the flag and drop the pane to a shell.
func TestBuildAiLaunchCmd_codex_ignores_claude_settings(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_SETTINGS=/tmp/settings.json")
	got := codexLaunchCmd(t, env, "")
	if strings.Contains(got, "--settings") {
		t.Errorf("codex launch %q must not carry --settings", got)
	}
	if got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// Likewise the CLAUDE_CONFIG_DIR env plumbing (both the assignment form and the
// `env -u` shedding form) is claude-only.
func TestBuildAiLaunchCmd_codex_carries_no_claude_config_dir_plumbing(t *testing.T) {
	for _, env := range [][]string{
		buildEnv(t, nil), // -> claude's default arm would emit `env -u CLAUDE_CONFIG_DIR `
		buildEnv(t, nil, "WISP_DECK_CLAUDE_ACCOUNT_DIR=/tmp/acct"),
	} {
		got := codexLaunchCmd(t, env, "")
		if strings.Contains(got, "CLAUDE_CONFIG_DIR") {
			t.Errorf("codex launch %q must not mention CLAUDE_CONFIG_DIR", got)
		}
	}
}

// The screenshot-drag filter wraps claude only.
func TestBuildAiLaunchCmd_codex_carries_no_screenshot_filter(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ")
	if got := codexLaunchCmd(t, env, ""); got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// opencode gets the project dir as a positional arg; codex must not, because the
// pane's cwd is already the project dir.
func TestBuildAiLaunchCmd_codex_takes_no_positional_project_dir(t *testing.T) {
	if got := codexLaunchCmd(t, nil, "/p/app"); got != "/usr/bin/codex" {
		t.Errorf("got %q, want %q", got, "/usr/bin/codex")
	}
}

// Resume mode with a captured session id: codex resumes ITS exact session via
// a guarded `codex resume <id>` that falls back to a plain launch when the
// resume fails at startup (mirrors claude's --resume → -c → plain chain).
func TestBuildAiLaunchCmd_codex_resume_with_sid_uses_guarded_resume_chain(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION=sid-42")
	got := codexLaunchCmd(t, env, "")
	assertContains(t, got, "/usr/bin/codex resume sid-42")
	assertContains(t, got, "_wd_rc")
	// The fallback step is the bare binary (a "; /usr/bin/codex;" segment).
	assertContains(t, got, "; /usr/bin/codex;")
}

// Resume mode WITHOUT a captured id stays a plain relaunch: `codex resume
// --last` is cwd-filtered but could still steal another pane's session.
func TestBuildAiLaunchCmd_codex_resume_without_sid_relaunches_fresh(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION=")
	got := codexLaunchCmd(t, env, "")
	if got != "/usr/bin/codex" {
		t.Errorf("got %q, want a plain relaunch %q", got, "/usr/bin/codex")
	}
	for _, flag := range []string{"--continue", "--resume", "-c"} {
		if strings.Contains(got, flag) {
			t.Errorf("codex resume launch %q must not carry %q", got, flag)
		}
	}
}

// The collapsed signature: opencode still receives the project dir positionally,
// now from the single <tool_cmd> slot.
func TestBuildAiLaunchCmd_opencode_keeps_positional_dir_under_single_slot(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "npx --prefer-offline opencode-ai@latest", "/p/app"}, buildEnv(t, nil))
	assertExitCode(t, code, 0)
	want := `npx --prefer-offline opencode-ai@latest "/p/app"`
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetToolAccent_codex_is_teal(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "get_tool_accent", []string{"codex"}, buildEnv(t, nil))
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "36" {
		t.Errorf("get_tool_accent codex = %q, want 36", got)
	}
}
