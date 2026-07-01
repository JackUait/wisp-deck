package bash_test

import (
	"testing"
)

// When the account-rotation proxy is active, wrapper.sh exports the proxy port +
// key so build_ai_launch_cmd prefixes ANTHROPIC_BASE_URL/ANTHROPIC_API_KEY onto
// the claude launch — routing claude through the proxy, which injects the active
// account's token. No per-account CLAUDE_CONFIG_DIR is set (rotation is upstream).

func TestBuildAILaunchCmd_mitm_uses_https_proxy_and_ca(t *testing.T) {
	// MITM mode (teamclaude default): route via HTTPS_PROXY + NODE_EXTRA_CA_CERTS,
	// and NOT ANTHROPIC_BASE_URL/API_KEY (claude keeps its own token; the proxy
	// injects the account token upstream).
	env := []string{"WISP_DECK_PROXY_PORT=54321", "WISP_DECK_PROXY_KEY=wd-abc", "WISP_DECK_PROXY_CA=/cfg/ca.pem"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	// The key is embedded in the proxy URL so claude sends Proxy-Authorization on
	// CONNECT (loopback is not trusted; the proxy authenticates the tunnel).
	assertContains(t, out, `HTTPS_PROXY="http://wisp-deck:wd-abc@127.0.0.1:54321"`)
	assertContains(t, out, `NODE_EXTRA_CA_CERTS="/cfg/ca.pem"`)
	assertNotContains(t, out, "ANTHROPIC_BASE_URL")
	assertContains(t, out, "claude /proj")
}

func TestBuildAILaunchCmd_baseurl_mode_when_no_ca(t *testing.T) {
	// Without a CA (--mitm=false), fall back to base-URL mode.
	env := []string{"WISP_DECK_PROXY_PORT=54321", "WISP_DECK_PROXY_KEY=wd-abc"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `ANTHROPIC_BASE_URL="http://127.0.0.1:54321"`)
	assertContains(t, out, `ANTHROPIC_API_KEY="wd-abc"`)
	assertNotContains(t, out, "HTTPS_PROXY")
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
