package bash_test

import (
	"testing"
)

// When the account-rotation proxy is active, wrapper.sh exports the proxy port +
// key so build_ai_launch_cmd prefixes ANTHROPIC_BASE_URL/ANTHROPIC_API_KEY onto
// the claude launch — routing claude through the proxy, which injects the active
// account's token. No per-account CLAUDE_CONFIG_DIR is set (rotation is upstream).

func TestBuildAILaunchCmd_prefixes_proxy_env_for_claude(t *testing.T) {
	env := []string{"WISP_DECK_PROXY_PORT=54321", "WISP_DECK_PROXY_KEY=wd-abc"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `ANTHROPIC_BASE_URL="http://127.0.0.1:54321"`)
	assertContains(t, out, `ANTHROPIC_API_KEY="wd-abc"`)
	assertContains(t, out, "claude /proj")
}

func TestBuildAILaunchCmd_prefixes_proxy_env_on_resume(t *testing.T) {
	env := []string{"WISP_DECK_RESUME=1", "WISP_DECK_PROXY_PORT=54321", "WISP_DECK_PROXY_KEY=wd-abc"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "opencode"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `ANTHROPIC_BASE_URL="http://127.0.0.1:54321"`)
	assertContains(t, out, "claude -c")
}

func TestBuildAILaunchCmd_no_proxy_env_when_unset(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "opencode", "/proj"}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "ANTHROPIC_BASE_URL")
}

func TestBuildAILaunchCmd_proxy_env_ignored_for_opencode(t *testing.T) {
	env := []string{"WISP_DECK_PROXY_PORT=54321", "WISP_DECK_PROXY_KEY=wd-abc"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "claude", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "ANTHROPIC_BASE_URL")
}
