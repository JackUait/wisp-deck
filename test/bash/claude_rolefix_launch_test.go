package bash_test

import (
	"strings"
	"testing"
)

func featherlessLaunchEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	base := []string{
		"HOME=/home/tester",
		"WISP_DECK_CLAUDE_PROVIDER=featherless",
		"WISP_DECK_RESUME=0",
		"WISP_DECK_RESUME_SESSION=",
	}
	return buildEnv(t, nil, append(base, extra...)...)
}

// Featherless validates the Anthropic schema strictly and rejects the "system"
// role Claude Code puts in messages[], so every turn 400s without the repair
// proxy in front of it. The wrapper owns the whole raw command, exactly as the
// ChatGPT adapter does.
func TestFeatherlessLaunchWrapsTheRawClaudeCommand(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "/opt/Claude App/claude", `prompt; must-not-run`},
		featherlessLaunchEnv(t, "WISP_DECK_CLAUDE_SETTINGS=/cfg/featherless glm.json"))
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if strings.Count(got, "claude-rolefix") != 1 {
		t.Fatalf("rolefix wrapper count != 1: %q", got)
	}
	for _, want := range []string{
		`wisp-deck-tui claude-rolefix`,
		`--settings /cfg/featherless\ glm.json`,
		`-- bash -c`,
		`/opt/Claude\ App/claude`,
		`prompt\;\ must-not-run`,
	} {
		assertContains(t, got, want)
	}
}

// Every other provider must be untouched: the proxy exists for one endpoint's
// strictness, and a gateway that accepts the request needs no extra hop.
func TestFeatherlessLaunchLeavesOtherProvidersAlone(t *testing.T) {
	for _, provider := range []string{"zhipu", "moonshot", "mimo", ""} {
		out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
			[]string{"claude", "/opt/claude"},
			buildEnv(t, nil,
				"HOME=/home/tester",
				"WISP_DECK_CLAUDE_PROVIDER="+provider,
				"WISP_DECK_RESUME=0",
				"WISP_DECK_RESUME_SESSION=",
				"WISP_DECK_CLAUDE_SETTINGS=/cfg/x.json"))
		assertExitCode(t, code, 0)
		if strings.Contains(out, "claude-rolefix") {
			t.Errorf("provider %q was wrapped in the repair proxy: %s", provider, out)
		}
	}
}

// With no overlay to point at the proxy there is nothing to rewrite, so the
// launch must stay exactly as it was rather than wrap a command that cannot be
// redirected.
func TestFeatherlessLaunchSkipsTheWrapperWithoutAnOverlay(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "/opt/claude"},
		featherlessLaunchEnv(t, "WISP_DECK_CLAUDE_SETTINGS="))
	assertExitCode(t, code, 0)
	if strings.Contains(out, "claude-rolefix") {
		t.Errorf("wrapped a launch with no settings overlay: %s", out)
	}
}

// The marker is what the launch branches on, so it has to survive the trusted
// allowlist that reads it back off disk.
func TestGetClaudeConfigProviderReportsFeatherless(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "featherless.json", `{"env":{
		"WISP_DECK_SUBSCRIPTION_PROVIDER":"featherless",
		"ANTHROPIC_BASE_URL":"https://api.featherless.ai"
	}}`)
	out, code := runBashFunc(t, "lib/claude-configs.sh", "get_claude_config_provider",
		[]string{dir + "/featherless.json"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "featherless" {
		t.Errorf("provider = %q, want featherless", strings.TrimSpace(out))
	}
}
