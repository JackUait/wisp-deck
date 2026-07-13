package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeAttentionEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	base := []string{
		"HOME=/home/tester",
		"WISP_DECK_RESUME=0",
		"WISP_DECK_RESUME_SESSION=",
		"WISP_DECK_ATTENTION_FILE=/private/runtime/generation.Abc123/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc123",
	}
	return buildEnv(t, nil, append(base, extra...)...)
}

func TestClaudeAttentionLaunch_wraps_complete_claude_chain_once(t *testing.T) {
	env := claudeAttentionEnv(t)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/project"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if strings.Count(got, "wisp-deck-tui claude-attention") != 1 {
		t.Fatalf("attention wrapper count != 1: %q", got)
	}
	for _, want := range []string{
		`--state-file /private/runtime/generation.Abc123/state`,
		`--generation generation.Abc123`,
		`--config-dir /home/tester/.claude`,
		`-- bash -c`,
		`env\ -u\ CLAUDE_CONFIG_DIR\ claude\ /project`,
	} {
		assertContains(t, got, want)
	}
}

func TestClaudeAttentionLaunch_uses_exact_account_config_root(t *testing.T) {
	env := claudeAttentionEnv(t,
		"WISP_DECK_CLAUDE_ACCOUNT_DIR=/cfg/accounts/work account")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/project"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	assertContains(t, got, `--config-dir /cfg/accounts/work\ account`)
	assertContains(t, got, `CLAUDE_CONFIG_DIR=\"/cfg/accounts/work\ account\"`)
}

func TestClaudeAttentionLaunch_preserves_resume_fallback_execution(t *testing.T) {
	dir := t.TempDir()
	claudeLog := filepath.Join(dir, "claude.log")
	attentionLog := filepath.Join(dir, "attention.log")
	binDir := mockCommand(t, dir, "claude", `
printf '%s\n' "$*" >> "$CLAUDE_LOG"
[ "$1" = "--resume" ] && exit 1
exit 0
`)
	mockCommand(t, dir, "wisp-deck-tui", `
printf '%s\n' "$@" > "$ATTENTION_LOG"
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "$1" = "--" ] && shift
exec "$@"
`)
	env := buildEnv(t, []string{binDir},
		"HOME=/home/tester",
		"CLAUDE_LOG="+claudeLog,
		"ATTENTION_LOG="+attentionLog,
		"WISP_DECK_RESUME=1",
		"WISP_DECK_RESUME_SESSION=sid-42",
		"WISP_DECK_ATTENTION_FILE=/private/runtime/generation.Abc123/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc123",
	)
	launch, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/project"}, env)
	assertExitCode(t, code, 0)
	_, code = runBashSnippet(t, strings.TrimSpace(launch), env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(claudeLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "--resume sid-42\n-c\n" {
		t.Fatalf("fallback calls = %q", got)
	}
	args, err := os.ReadFile(attentionLog)
	if err != nil {
		t.Fatal(err)
	}
	argText := string(args)
	for _, want := range []string{
		"claude-attention\n", "--state-file\n", "--generation\n",
		"--config-dir\n", "--\n", "bash\n", "-c\n",
	} {
		assertContains(t, argText, want)
	}
}

func TestClaudeAttentionLaunch_preserves_late_resume_failure_exit(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "claude", `exit 7`)
	mockCommand(t, dir, "wisp-deck-tui", `
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "$1" = "--" ] && shift
exec "$@"
`)
	env := buildEnv(t, []string{binDir},
		"HOME=/home/tester",
		"WISP_DECK_RESUME=1",
		"WISP_DECK_RESUME_SESSION=sid-42",
		"WISP_DECK_RESUME_FALLBACK_WINDOW=0",
		"WISP_DECK_ATTENTION_FILE=/private/runtime/generation.Abc123/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc123",
	)
	launch, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/project"}, env)
	assertExitCode(t, code, 0)
	_, code = runBashSnippet(t, strings.TrimSpace(launch), env)
	assertExitCode(t, code, 7)
}

func TestClaudeAttentionLaunch_keeps_switch_handoff_inside_wrapper(t *testing.T) {
	dir := t.TempDir()
	claudeLog := filepath.Join(dir, "claude.log")
	binDir := mockCommand(t, dir, "claude", `printf '<%s>\n' "$@" > "$CLAUDE_LOG"`)
	mockCommand(t, dir, "wisp-deck-tui", `
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "$1" = "--" ] && shift
exec "$@"
`)
	env := claudeAttentionEnv(t,
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"CLAUDE_LOG="+claudeLog,
	)
	root := projectRoot(t)
	snippet := fmt.Sprintf(`
source %q
source %q
handoff=" $(printf '%%q' 'taking over from codex via /tmp/handoff.md')"
cmd="$(build_switch_launch_cmd claude claude '' '' /project '' '' "$handoff")"
eval "$cmd"
`, filepath.Join(root, "lib", "tmux-session.sh"), filepath.Join(root, "lib", "account-switch.sh"))
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(claudeLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "<taking over from codex via /tmp/handoff.md>\n" {
		t.Fatalf("Claude handoff argv = %q", got)
	}
}

func TestClaudeAttentionLaunch_keeps_screenshot_filter_inside_single_wrapper(t *testing.T) {
	env := claudeAttentionEnv(t,
		"WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/project"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if strings.Count(got, "claude-attention") != 1 {
		t.Fatalf("outer wrapper count != 1: %q", got)
	}
	if strings.Count(got, "screenshot-filter") != 1 {
		t.Fatalf("screenshot filter count != 1: %q", got)
	}
	if strings.Index(got, "claude-attention") > strings.Index(got, "screenshot-filter") {
		t.Fatalf("screenshot filter escaped outer supervisor: %q", got)
	}
}

func TestClaudeAttentionLaunch_does_not_wrap_other_tools(t *testing.T) {
	for _, tc := range []struct {
		tool string
		cmd  string
	}{
		{"codex", "/usr/bin/codex"},
		{"opencode", "opencode"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
				[]string{tc.tool, tc.cmd, "/project"}, claudeAttentionEnv(t))
			assertExitCode(t, code, 0)
			assertNotContains(t, out, "claude-attention")
		})
	}
}

func TestClaudeAttentionLaunch_requires_both_runtime_fields(t *testing.T) {
	for _, missing := range []string{"state", "generation"} {
		t.Run(missing, func(t *testing.T) {
			extra := "WISP_DECK_ATTENTION_FILE="
			if missing == "generation" {
				extra = "WISP_DECK_ATTENTION_GENERATION="
			}
			out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
				[]string{"claude", "claude", "/project"}, claudeAttentionEnv(t, extra))
			assertExitCode(t, code, 0)
			assertNotContains(t, out, "claude-attention")
		})
	}
}
