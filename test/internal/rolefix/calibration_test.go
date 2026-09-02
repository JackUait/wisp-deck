// Package rolefix_test pins the proxy's token estimate to what Featherless
// actually charged for a real Claude Code request. The recording lives here
// rather than beside the package because the repository's host-effect audit
// reads every tracked text file under internal/ as production source, and this
// one is a verbatim capture of Claude Code's tool descriptions.
package rolefix_test

import (
	"os"
	"testing"

	"github.com/jackuait/wisp-deck/internal/featherless"
	"github.com/jackuait/wisp-deck/internal/rolefix"
)

// firstTurn is the verbatim first request a headless Claude Code pane sent on
// 2026-09-02 — 26 tool schemas, the system prompt, and the agent and skill
// rosters, with an empty config directory, no MCP servers and a two-file
// project. Featherless priced that exact conversation at 19,838 prompt tokens
// on its own tokenizer, measured by posting it to /v1/chat/completions, which
// reports the usage /v1/messages returns as zero.
const charged = 19838

func firstTurn(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/claude-code-first-turn.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// The direction matters more than the margin. Reading low lets a conversation
// grow past a window the endpoint then rejects, and there is no way back from
// that: /compact must itself send the oversized transcript. Reading high only
// compacts a little early.
func TestEstimateInputTokens_matches_what_the_endpoint_charged(t *testing.T) {
	got := rolefix.EstimateInputTokens(firstTurn(t))
	if got < charged {
		t.Errorf("estimate = %d for a request charged %d; an estimate that reads low is the one that kills a session",
			got, charged)
	}
	if got > charged*5/4 {
		t.Errorf("estimate = %d for a request charged %d; over a quarter high compacts away a quarter of the window",
			got, charged)
	}
}

// Claude Code's floor is what makes a narrow model unusable, and the featherless
// picker's MinContext is derived from it. Both numbers come from this one
// request, so a change to either must be a deliberate one.
func TestEstimateInputTokens_prices_claude_codes_floor_near_the_pickers_assumption(t *testing.T) {
	if got := rolefix.EstimateInputTokens(firstTurn(t)); got < featherless.ClaudeCodeFloorTokens {
		t.Errorf("a bare Claude Code turn estimates %d tokens, under the %d the picker assumes",
			got, featherless.ClaudeCodeFloorTokens)
	}
}
