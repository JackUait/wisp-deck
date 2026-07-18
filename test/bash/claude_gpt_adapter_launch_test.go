package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chatGPTLaunchEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	base := []string{
		"HOME=/home/tester",
		"WISP_DECK_CLAUDE_PROVIDER=openai-chatgpt",
		"WISP_DECK_CODEX_CMD=/opt/Codex App/codex",
		"WISP_DECK_RESUME=0",
		"WISP_DECK_RESUME_SESSION=",
	}
	return buildEnv(t, nil, append(base, extra...)...)
}

func TestClaudeGPTLaunchWrapsTheCompleteRawClaudeCommand(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "/opt/Claude App/claude", `prompt; must-not-run`},
		chatGPTLaunchEnv(t, "WISP_DECK_CLAUDE_SETTINGS=/cfg/openai gpt.json"))
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if strings.Count(got, "claude-gpt-adapter") != 1 {
		t.Fatalf("GPT adapter count != 1: %q", got)
	}
	for _, want := range []string{
		`wisp-deck-tui claude-gpt-adapter`,
		`--codex /opt/Codex\ App/codex`,
		`-- bash -c`,
		`/opt/Claude\ App/claude`,
		`prompt\;\ must-not-run`,
		`--settings\ \"/cfg/openai\ gpt.json\"`,
	} {
		assertContains(t, got, want)
	}
}

func TestClaudeGPTLaunchKeepsAttentionOutermostAndScreenshotInside(t *testing.T) {
	env := chatGPTLaunchEnv(t,
		"WISP_DECK_ATTENTION_FILE=/private/runtime/generation.Abc/state",
		"WISP_DECK_ATTENTION_GENERATION=generation.Abc",
		"WISP_DECK_CLAUDE_FILTER=wisp-deck-tui screenshot-filter -- ",
	)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	for _, marker := range []string{"claude-attention", "claude-gpt-adapter", "screenshot-filter"} {
		if strings.Count(got, marker) != 1 {
			t.Fatalf("%s count != 1: %q", marker, got)
		}
	}
	attention := strings.Index(got, "claude-attention")
	adapter := strings.Index(got, "claude-gpt-adapter")
	filter := strings.Index(got, "screenshot-filter")
	if !(attention < adapter && adapter < filter) {
		t.Fatalf("wrapper order must be attention → GPT adapter → screenshot filter: %q", got)
	}
}

func TestClaudeGPTLaunchResumeFallbackRunsInsideOneAdapter(t *testing.T) {
	dir := t.TempDir()
	claudeLog := filepath.Join(dir, "claude.log")
	adapterLog := filepath.Join(dir, "adapter.log")
	binDir := mockCommand(t, dir, "claude", `
printf '%s\n' "$*" >> "$CLAUDE_LOG"
[ "$1" = "--resume" ] && exit 1
exit 0
`)
	mockCommand(t, dir, "wisp-deck-tui", `
printf '%s\n' "$@" >> "$ADAPTER_LOG"
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "$1" = "--" ] && shift
exec "$@"
`)
	env := chatGPTLaunchEnv(t,
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"CLAUDE_LOG="+claudeLog,
		"ADAPTER_LOG="+adapterLog,
		"WISP_DECK_RESUME=1",
		"WISP_DECK_RESUME_SESSION=sid-42",
	)
	launch, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude"}, env)
	assertExitCode(t, code, 0)
	_, code = runBashSnippet(t, strings.TrimSpace(launch), env)
	assertExitCode(t, code, 0)
	calls, err := os.ReadFile(claudeLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(calls); got != "--resume sid-42\n-c\n" {
		t.Fatalf("Claude fallback calls = %q", got)
	}
	adapterArgs, err := os.ReadFile(adapterLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(adapterArgs), "claude-gpt-adapter\n") != 1 {
		t.Fatalf("adapter launch count != 1:\n%s", adapterArgs)
	}
}

func TestClaudeGPTLaunchDoesNotChangeOtherProviders(t *testing.T) {
	baseline, baselineCode := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "/project"},
		buildEnv(t, nil, "WISP_DECK_RESUME=0"))
	for _, provider := range []string{"", "zhipu", "mimo", "untrusted"} {
		t.Run(provider, func(t *testing.T) {
			out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
				[]string{"claude", "claude", "/project"},
				buildEnv(t, nil,
					"WISP_DECK_RESUME=0",
					"WISP_DECK_CLAUDE_PROVIDER="+provider,
					"WISP_DECK_CODEX_CMD=/opt/codex",
				))
			if code != baselineCode || out != baseline {
				t.Fatalf("provider %q changed launch:\n got %q\nwant %q", provider, out, baseline)
			}
		})
	}
}

func TestClaudeGPTLaunchRequiresAbsoluteCodexPath(t *testing.T) {
	for _, codex := range []string{"", "codex"} {
		t.Run(codex, func(t *testing.T) {
			out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
				[]string{"claude", "claude"}, chatGPTLaunchEnv(t, "WISP_DECK_CODEX_CMD="+codex))
			if code == 0 {
				t.Fatalf("invalid Codex path accepted: %q", out)
			}
			assertContains(t, out, "Codex")
			assertContains(t, out, "relaunch")
			assertNotContains(t, out, "codex login")
		})
	}
}
