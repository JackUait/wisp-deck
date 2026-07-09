package tui

import "testing"

// The installer names each tool's publishing organization in parentheses, so a
// user selecting from the list knows whose CLI they are about to install.
func TestInstallerToolDisplayName(t *testing.T) {
	cases := map[string]string{
		"claude":   "Claude Code",
		"opencode": "OpenCode (anomalyco)",
		"codex":    "Codex (openai)",
	}
	for tool, want := range cases {
		if got := installerToolDisplayName(tool); got != want {
			t.Errorf("installerToolDisplayName(%q) = %q, want %q", tool, got, want)
		}
	}
}
