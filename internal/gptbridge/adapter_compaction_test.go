package gptbridge

import (
	"strings"
	"testing"
)

func TestBuildClaudeEnvironmentDisablesUnknownModelWindowEnforcement(t *testing.T) {
	got := BuildClaudeEnvironment(
		[]string{"CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=0"},
		"http://127.0.0.1:4321",
		"bridge-secret",
	)
	joined := "\n" + strings.Join(got, "\n") + "\n"
	const want = "\nCLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1\n"
	if !strings.Contains(joined, want) {
		t.Fatalf("Claude environment missing %q:\n%s", want, joined)
	}
	if countEnv(got, "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT") != 1 {
		t.Fatalf("window enforcement override occurs more than once: %q", got)
	}
}
