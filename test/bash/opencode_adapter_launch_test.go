package bash_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAiLaunchCmdRoutesOpenCodeThroughStrictAdapter(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "npx", "exit 0")
	state := filepath.Join(dir, "generation.OpenCode", "state")
	env := buildEnv(t, []string{binDir},
		"WISP_DECK_ATTENTION_FILE="+state,
		"WISP_DECK_ATTENTION_GENERATION=generation.OpenCode",
		"WISP_DECK_RESUME=0",
	)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "npx --no-install opencode-ai", "/workspace/project"}, env)
	assertExitCode(t, code, 0)
	want := "wisp-deck-tui opencode-adapter --state-file " + state +
		" --generation generation.OpenCode -- " + filepath.Join(binDir, "npx") + " --no-install opencode-ai"
	if strings.TrimSpace(out) != want {
		t.Fatalf("OpenCode adapter launch = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestBuildAiLaunchCmdOpenCodeResumeAndHandoffStayInsideAdapterFlags(t *testing.T) {
	dir := t.TempDir()
	opencode := filepath.Join(dir, "OpenCode Binary")
	state := filepath.Join(dir, "generation.Resume", "state")
	env := buildEnv(t, nil,
		"WISP_DECK_ATTENTION_FILE="+state,
		"WISP_DECK_ATTENTION_GENERATION=generation.Resume",
		"WISP_DECK_RESUME=1",
		"WISP_DECK_OPENCODE_HANDOFF_PROMPT=--hostile prompt; $(must-not-run)",
	)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", opencode, "/workspace/project"}, env)
	assertExitCode(t, code, 0)
	launch := strings.TrimSpace(out)
	for _, fragment := range []string{
		"opencode-adapter", "--continue", "--prompt --hostile\\ prompt\\;\\ \\$\\(must-not-run\\)", "-- " + strings.ReplaceAll(opencode, " ", "\\ "),
	} {
		if !strings.Contains(launch, fragment) {
			t.Fatalf("OpenCode resume launch %q missing %q", launch, fragment)
		}
	}
}

func TestBuildAiLaunchCmdRejectsUnsupportedOpenCodePrefixWithoutRawFallback(t *testing.T) {
	dir := t.TempDir()
	env := buildEnv(t, nil,
		"WISP_DECK_ATTENTION_FILE="+filepath.Join(dir, "generation.Strict", "state"),
		"WISP_DECK_ATTENTION_GENERATION=generation.Strict",
	)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"opencode", "env EVIL=1 opencode", "/workspace/project"}, env)
	if code == 0 {
		t.Fatalf("unsupported OpenCode command escaped strict adapter: %q", out)
	}
	if strings.Contains(out, "env EVIL") {
		t.Fatalf("unsafe OpenCode command was emitted: %q", out)
	}
}
