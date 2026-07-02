package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildAndRunLaunchCmd builds the resume launch command and then executes it
// with `claude` mocked (mockBody), recording every claude invocation (one
// line of args per call) into the returned rec file's content.
func buildAndRunLaunchCmd(t *testing.T, sid, mockBody string, extraEnv ...string) string {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "claude",
		"echo \"$@\" >> "+rec+"\n"+mockBody)
	env := buildEnv(t, []string{binDir},
		append([]string{"WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION=" + sid}, extraEnv...)...)
	cmd, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	_, _ = runBashSnippet(t, strings.TrimSpace(cmd), env)
	data, _ := os.ReadFile(rec)
	return string(data)
}

func TestBuildAiLaunchCmd_resume_falls_back_to_c_on_instant_failure(t *testing.T) {
	// `claude --resume <id>` can fail at startup even for a validated id
	// (e.g. the >15KB-first-message resume bug). The launch must fall back to
	// `claude -c` instead of dumping the restored tab to a bare shell.
	calls := buildAndRunLaunchCmd(t, "sid-42", `case "$1" in --resume) exit 1 ;; *) exit 0 ;; esac`)
	want := "--resume sid-42\n-c\n"
	if calls != want {
		t.Errorf("claude calls:\n got %q\nwant %q", calls, want)
	}
}

func TestBuildAiLaunchCmd_resume_success_does_not_fall_back(t *testing.T) {
	calls := buildAndRunLaunchCmd(t, "sid-42", `exit 0`)
	want := "--resume sid-42\n"
	if calls != want {
		t.Errorf("claude calls:\n got %q\nwant %q", calls, want)
	}
}

func TestBuildAiLaunchCmd_c_falls_back_to_plain_on_instant_failure(t *testing.T) {
	// A restored tab whose project has NO conversations yet: `claude -c`
	// itself fails ("No conversation found to continue") — fall back to a
	// plain `claude` so the tab still comes back usable.
	calls := buildAndRunLaunchCmd(t, "", `case "$1" in -c) exit 1 ;; *) exit 0 ;; esac`)
	want := "-c\n\n"
	if calls != want {
		t.Errorf("claude calls:\n got %q\nwant %q", calls, want)
	}
}

func TestBuildAiLaunchCmd_no_fallback_after_long_run(t *testing.T) {
	// A non-zero exit AFTER the startup window is a crash or user action, not
	// a resume failure — relaunching claude then would be wrong. The window is
	// env-tunable so the test doesn't wait the real 10s.
	calls := buildAndRunLaunchCmd(t, "sid-42", "sleep 1.2\nexit 1",
		"WISP_DECK_RESUME_FALLBACK_WINDOW=1")
	want := "--resume sid-42\n"
	if calls != want {
		t.Errorf("claude calls:\n got %q\nwant %q", calls, want)
	}
}

func aiCmd(t *testing.T, tool string, resume bool) string {
	t.Helper()
	var env []string
	// WISP_DECK_RESUME_SESSION is force-cleared to stay hermetic when the test
	// itself runs inside a restored Wisp Deck session (which exports both).
	if resume {
		env = buildEnv(t, nil, "WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION=")
	} else {
		env = buildEnv(t, nil, "WISP_DECK_RESUME=0", "WISP_DECK_RESUME_SESSION=")
	}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{tool, "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	return strings.TrimSpace(out)
}

func TestBuildAiLaunchCmd_resume_flags(t *testing.T) {
	// Claude resumes via a guarded chain: `-c` first, plain `claude` as the
	// startup-failure fallback (project with no conversations yet).
	got := aiCmd(t, "claude", true)
	if !strings.Contains(got, "claude -c;") {
		t.Errorf("resume claude: %q must launch `claude -c` first", got)
	}
	if !strings.Contains(got, "then _wd_t0=$(date +%s); claude; _wd_rc=$?; fi") {
		t.Errorf("resume claude: %q must fall back to plain claude", got)
	}
	if got := aiCmd(t, "opencode", true); got != "npx opencode-ai@latest --continue" {
		t.Errorf("resume opencode: got %q", got)
	}
}

func TestBuildAiLaunchCmd_resumes_specific_claude_session(t *testing.T) {
	// Each restored tab must reopen ITS conversation: two tabs of the same
	// project resumed with plain `claude -c` would both open the project's
	// most recent conversation.
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1", "WISP_DECK_RESUME_SESSION=sid-42")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if !strings.Contains(got, "claude --resume sid-42;") {
		t.Errorf("%q must launch `claude --resume sid-42` first", got)
	}
	// Guarded fallbacks: -c if the specific resume fails at startup, then
	// plain claude if -c fails too.
	if !strings.Contains(got, "then _wd_t0=$(date +%s); claude -c; _wd_rc=$?; fi") {
		t.Errorf("%q must fall back to `claude -c`", got)
	}
	if !strings.Contains(got, "then _wd_t0=$(date +%s); claude; _wd_rc=$?; fi") {
		t.Errorf("%q must fall back to plain claude last", got)
	}

	// OpenCode has its own continue semantics; the Claude session id must
	// not leak into its command.
	out, code = runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "npx opencode-ai@latest --continue" {
		t.Errorf("opencode got %q, want %q", got, "npx opencode-ai@latest --continue")
	}
}

func TestBuildAiLaunchCmd_normal_unaffected(t *testing.T) {
	if got := aiCmd(t, "opencode", false); got != `npx opencode-ai@latest "/p/app"` {
		t.Errorf("normal opencode: got %q", got)
	}
}

// When WISP_DECK_CLAUDE_FILTER is set (wrapper sets it after confirming the TUI
// binary supports the screenshot-drag filter), the Claude launch is prefixed
// with it so a dropped screenshot's temp path is rewritten to a stable copy
// before Claude reads it. OpenCode is never wrapped.
func TestBuildAiLaunchCmd_wraps_claude_with_filter(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=0",
		"WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != `wisp-deck-tui screenshot-filter -- claude /p/app` {
		t.Errorf("claude wrap: got %q", got)
	}
	out, _ = runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	if strings.Contains(out, "screenshot-filter") {
		t.Errorf("opencode must not be wrapped: %q", strings.TrimSpace(out))
	}
}

func TestBuildAiLaunchCmd_wraps_claude_resume_with_filter(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1",
		"WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	// Every step of the fallback chain must be wrapped with the filter.
	if !strings.Contains(got, `wisp-deck-tui screenshot-filter -- claude -c;`) ||
		!strings.Contains(got, `then _wd_t0=$(date +%s); wisp-deck-tui screenshot-filter -- claude; _wd_rc=$?; fi`) {
		t.Errorf("claude resume wrap: got %q", got)
	}
}

// When a non-Default native account is active, wrapper.sh exports
// WISP_DECK_CLAUDE_ACCOUNT_DIR and the Claude launch is prefixed with
// CLAUDE_CONFIG_DIR=<dir> so `claude` runs under that account's isolated login.
// The Default account leaves the env var unset (Keychain login, unchanged).
func TestBuildAiLaunchCmd_prefixes_claude_config_dir(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=0",
		"WISP_DECK_CLAUDE_ACCOUNT_DIR=/cfg/claude-accounts/work")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != `CLAUDE_CONFIG_DIR="/cfg/claude-accounts/work" claude /p/app` {
		t.Errorf("claude account prefix: got %q", got)
	}
}

func TestBuildAiLaunchCmd_account_dir_not_applied_to_opencode(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=0",
		"WISP_DECK_CLAUDE_ACCOUNT_DIR=/cfg/claude-accounts/work")
	out, _ := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	if strings.Contains(out, "CLAUDE_CONFIG_DIR") {
		t.Errorf("opencode must not get CLAUDE_CONFIG_DIR: %q", strings.TrimSpace(out))
	}
}

// The account prefix composes ahead of the screenshot filter (env is inherited
// by the child claude) and survives resume mode.
func TestBuildAiLaunchCmd_account_dir_composes_with_filter_and_resume(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_RESUME=1",
		"WISP_DECK_CLAUDE_ACCOUNT_DIR=/cfg/claude-accounts/work",
		"WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	// Every step of the fallback chain carries the account prefix + filter.
	if !strings.Contains(got, `CLAUDE_CONFIG_DIR="/cfg/claude-accounts/work" wisp-deck-tui screenshot-filter -- claude -c;`) ||
		!strings.Contains(got, `then _wd_t0=$(date +%s); CLAUDE_CONFIG_DIR="/cfg/claude-accounts/work" wisp-deck-tui screenshot-filter -- claude; _wd_rc=$?; fi`) {
		t.Errorf("claude account+filter+resume: got %q", got)
	}
}
