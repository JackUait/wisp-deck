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
