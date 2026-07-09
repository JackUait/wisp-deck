package bash_test

import (
	"strings"
	"testing"
)

// resolve_ai_tool_cmd maps a tool identifier onto the binary that launches it.
// It exists so callers pass build_ai_launch_cmd a single <tool_cmd> slot rather
// than one positional slot per known tool — a shape that has no room for a
// third tool and degrades with every addition.

func TestResolveAiToolCmd_maps_each_tool_to_its_binary(t *testing.T) {
	cases := []struct{ tool, want string }{
		{"claude", "/usr/bin/claude"},
		{"opencode", "npx --prefer-offline opencode-ai@latest"},
		{"codex", "/usr/bin/codex"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_ai_tool_cmd",
				[]string{tc.tool, "/usr/bin/claude", "npx --prefer-offline opencode-ai@latest", "/usr/bin/codex"}, nil)
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Errorf("resolve_ai_tool_cmd %s = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

// An unknown identifier resolves to the claude command, matching the `default`
// arm every other per-tool switch in the codebase uses.
func TestResolveAiToolCmd_unknown_tool_falls_back_to_claude(t *testing.T) {
	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_ai_tool_cmd",
		[]string{"bogus", "/usr/bin/claude", "oc", "cx"}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "/usr/bin/claude" {
		t.Errorf("got %q, want %q", got, "/usr/bin/claude")
	}
}

// A tool that is not installed contributes an empty slot; resolving it must
// yield empty rather than silently substituting another tool's binary.
func TestResolveAiToolCmd_empty_slot_stays_empty(t *testing.T) {
	out, code := runBashFunc(t, "lib/ai-tools.sh", "resolve_ai_tool_cmd",
		[]string{"codex", "/usr/bin/claude", "oc", ""}, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
