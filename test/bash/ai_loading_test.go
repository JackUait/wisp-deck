package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The AI pane loading indicator: claude 2.1.217's startup blocks for seconds
// (minutes under a wedged macOS Security subsystem) BEFORE it paints, leaving
// the pane black — which reads as "the agent fails to load". A one-line
// "Starting …" banner printed before the launch makes that window legible;
// claude's alt-screen cleanly replaces it once it paints. Zero added latency.

func TestAiPaneLoadingPrefix_names_tool_clears_and_themes(t *testing.T) {
	out, code := runBashFunc(t, "lib/ai-loading.sh", "ai_pane_loading_prefix",
		[]string{"claude", "209"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "printf")
	assertContains(t, out, "Starting")
	assertContains(t, out, "claude")
	// Clears the pane so no stale frame sits behind the message.
	assertContains(t, out, "[2J")
	// Themed with the caller's accent colour.
	assertContains(t, out, "38;5;209")
	// Ends as a prefix (trailing separator) so the AI command follows.
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), ";") {
		t.Fatalf("prefix must end with ';' so the launch command follows: %q", out)
	}
}

func TestAiPaneLoadingPrefix_default_accent_and_other_tools(t *testing.T) {
	// Missing accent falls back to a sane default (no empty colour code).
	out, code := runBashFunc(t, "lib/ai-loading.sh", "ai_pane_loading_prefix",
		[]string{"codex"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "codex")
	assertContains(t, out, "38;5;")
	assertNotContains(t, out, "38;5;m")
}

func TestAiPaneLoadingPrefix_rejects_empty_tool(t *testing.T) {
	_, code := runBashFunc(t, "lib/ai-loading.sh", "ai_pane_loading_prefix",
		[]string{""}, nil)
	if code == 0 {
		t.Fatalf("expected non-zero for an empty tool name")
	}
}

// The prefix, when run, actually emits the banner text to the terminal.
func TestAiPaneLoadingPrefix_emits_banner_when_run(t *testing.T) {
	prefix, code := runBashFunc(t, "lib/ai-loading.sh", "ai_pane_loading_prefix",
		[]string{"claude", "209"}, nil)
	assertExitCode(t, code, 0)
	prefix = strings.TrimSpace(prefix)
	out, rc := runBashSnippet(t, prefix+" true\n", nil)
	assertExitCode(t, rc, 0)
	assertContains(t, out, "Starting")
	assertContains(t, out, "claude")
}

// TestWrapper_ai_pane_shows_loading_indicator verifies wrapper.sh prepends the
// indicator to the claude AI launch, so the split-window command carries it.
func TestWrapper_ai_pane_shows_loading_indicator(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	recPath := filepath.Join(home, "rec")
	mocks := map[string]string{
		"tmux":          recordingTmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	seedRestoreQueue(t, home, projDir, "claude")
	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("tmux never invoked: %v", err)
	}
	got := string(data)
	// The AI split's command must carry the loading indicator, before claude.
	if !strings.Contains(got, "Starting") {
		t.Fatalf("claude AI launch has no loading indicator:\n%s", got)
	}
	assertContains(t, got, "split-window -h")
}
