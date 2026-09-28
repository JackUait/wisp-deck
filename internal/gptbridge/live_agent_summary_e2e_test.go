package gptbridge

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// After a claude upgrade: the local progress-summary answer only works while
// Claude Code's AgentSummary prompt still starts with agentSummaryPrompt. If
// it changes, every summary goes back to replaying the whole conversation.
func TestLiveClaudeStillSendsTheAgentSummaryPrompt(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_AGENT_SUMMARY_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_AGENT_SUMMARY_E2E=1 to check the installed Claude Code")
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(binary, []byte(agentSummaryPrompt)) {
		t.Fatalf("%s no longer contains %q: update agentSummaryPrompt, or summaries replay whole conversations again", resolved, agentSummaryPrompt)
	}
}
