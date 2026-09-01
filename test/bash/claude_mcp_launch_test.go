package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSettingsWindow writes a profile declaring one context window and returns
// its path, standing in for the settings file a subscription writes.
func writeSettingsWindow(t *testing.T, window string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	body := `{"env":{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"` + window + `"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// MCP tool schemas cost ~18k tokens on top of a ~23k base prompt, so a profile
// under the floor overflows its window before the first turn can finish. The
// launch drops the servers rather than letting every request 400.
func TestClaudeLaunch_drops_MCP_servers_when_the_window_cannot_hold_them(t *testing.T) {
	settings := writeSettingsWindow(t, "32768")
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_SETTINGS="+settings)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd_raw",
		[]string{"claude", "/usr/bin/claude"}, env)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "--strict-mcp-config") {
		t.Errorf("a 32768 profile launches without --strict-mcp-config: %q", out)
	}
}

// A window with room to spare keeps the servers: they are why the tools are
// there, and dropping them everywhere would be a silent capability loss.
func TestClaudeLaunch_keeps_MCP_servers_when_the_window_has_room(t *testing.T) {
	settings := writeSettingsWindow(t, "262144")
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_SETTINGS="+settings)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd_raw",
		[]string{"claude", "/usr/bin/claude"}, env)
	assertExitCode(t, code, 0)
	if strings.Contains(out, "--strict-mcp-config") {
		t.Errorf("a 262144 profile should keep its MCP servers: %q", out)
	}
}

// A profile that declares no window says nothing about whether MCP fits, and
// guessing would strip working servers from every uncatalogued endpoint.
func TestClaudeLaunch_keeps_MCP_servers_when_no_window_is_declared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"env":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := buildEnv(t, nil, "WISP_DECK_CLAUDE_SETTINGS="+path)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd_raw",
		[]string{"claude", "/usr/bin/claude"}, env)
	assertExitCode(t, code, 0)
	if strings.Contains(out, "--strict-mcp-config") {
		t.Errorf("an undeclared window should not drop MCP servers: %q", out)
	}
}
