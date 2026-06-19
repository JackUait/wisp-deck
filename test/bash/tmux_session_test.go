package bash_test

import (
	"strings"
	"testing"
)

func aiCmd(t *testing.T, tool string, resume bool) string {
	t.Helper()
	var env []string
	if resume {
		env = buildEnv(t, nil, "GHOST_TAB_RESUME=1")
	} else {
		// Stay hermetic when the test itself runs inside a restored
		// Ghost Tab session (which exports GHOST_TAB_RESUME=1).
		env = buildEnv(t, nil, "GHOST_TAB_RESUME=0")
	}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{tool, "claude", "codex", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	return strings.TrimSpace(out)
}

func TestBuildAiLaunchCmd_resume_flags(t *testing.T) {
	cases := []struct {
		tool string
		want string
	}{
		{"claude", "claude -c"},
		{"codex", "codex resume --last"},
		{"opencode", "npx opencode-ai@latest --continue"},
	}
	for _, c := range cases {
		if got := aiCmd(t, c.tool, true); got != c.want {
			t.Errorf("resume %s: got %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestBuildAiLaunchCmd_normal_unaffected(t *testing.T) {
	if got := aiCmd(t, "codex", false); got != `codex --cd "/p/app"` {
		t.Errorf("normal codex: got %q", got)
	}
}

// When GHOST_TAB_CLAUDE_FILTER is set (wrapper sets it after confirming the TUI
// binary supports the screenshot-drag filter), the Claude launch is prefixed
// with it so a dropped screenshot's temp path is rewritten to a stable copy
// before Claude reads it. Codex/OpenCode are never wrapped.
func TestBuildAiLaunchCmd_wraps_claude_with_filter(t *testing.T) {
	env := buildEnv(t, nil, "GHOST_TAB_RESUME=0",
		"GHOST_TAB_CLAUDE_FILTER=ghost-tab-tui screenshot-filter -- ")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "codex", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != `ghost-tab-tui screenshot-filter -- claude /p/app` {
		t.Errorf("claude wrap: got %q", got)
	}
	out, _ = runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"codex", "claude", "codex", "npx opencode-ai@latest", "/p/app"}, env)
	if strings.Contains(out, "screenshot-filter") {
		t.Errorf("codex must not be wrapped: %q", strings.TrimSpace(out))
	}
}

func TestBuildAiLaunchCmd_wraps_claude_resume_with_filter(t *testing.T) {
	env := buildEnv(t, nil, "GHOST_TAB_RESUME=1",
		"GHOST_TAB_CLAUDE_FILTER=ghost-tab-tui screenshot-filter -- ")
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "codex", "npx opencode-ai@latest", "/p/app"}, env)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != `ghost-tab-tui screenshot-filter -- claude -c` {
		t.Errorf("claude resume wrap: got %q", got)
	}
}
