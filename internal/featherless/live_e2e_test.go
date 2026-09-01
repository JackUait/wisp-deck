package featherless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// watchdogTrigger is the byte silence at which Claude Code paints
// "Waiting for API response · check your network" and starts counting down to
// aborting and replaying the turn. It is a hardcoded interval in Claude Code
// with no env override.
const watchdogTrigger = 20 * time.Second

// liveGapCeiling is the margin this guard holds Featherless to. A measured gap
// anywhere near the trigger means the keep-alive mechanism has weakened, and a
// slower model or a busier day would cross it.
const liveGapCeiling = watchdogTrigger * 3 / 4

// This is the only check that can see Featherless changing its own side. Two
// things this integration rests on are undocumented and could be withdrawn
// without warning:
//
//   - the Anthropic Messages route at /v1/messages, which is what lets Claude
//     Code talk to Featherless with no translating proxy at all, and
//   - the ": keep-alive (awaiting first token)" SSE comments that fill the wait
//     before the first token, which are the only reason a slow prefill or a cold
//     model load does not trip Claude Code's byte-stall watchdog and get the
//     turn aborted and replayed.
//
// The prompt is deliberately large: keep-alives are emitted only while there is
// a wait to fill, so a small prompt to a hot model proves nothing about them.
//
// Run after a Featherless-side change is suspected:
//
//	WISP_DECK_LIVE_FEATHERLESS_E2E=1 FEATHERLESS_API_KEY=... \
//	  go test ./internal/featherless/ -run TestLiveFeatherless -v
func TestLiveFeatherlessCatalogAndAnthropicRoute(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_FEATHERLESS_E2E") != "1" {
		t.Skip("set WISP_DECK_LIVE_FEATHERLESS_E2E=1 to run")
	}
	key := os.Getenv("FEATHERLESS_API_KEY")
	if key == "" {
		t.Skip("set FEATHERLESS_API_KEY to run")
	}

	models, err := Fetch(context.Background(), key)
	if err != nil {
		t.Fatalf("fetch catalog: %v", err)
	}
	if len(models) < 1000 {
		t.Errorf("catalog returned %d tool-calling models, want thousands", len(models))
	}
	if models[0].Context < 100000 {
		t.Errorf("widest model has a %d context; the frontier tier is missing", models[0].Context)
	}
	t.Logf("catalog: %d usable models, widest %s at %d tokens",
		len(models), models[0].ID, models[0].Context)

	body, _ := json.Marshal(map[string]any{
		"model":      models[0].ID,
		"max_tokens": 64,
		"stream":     true,
		"system":     "You are a coding agent. " + strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2000),
		"messages": []any{
			map[string]string{"role": "user", "content": "What is the weather in Paris? Use the tool."},
		},
		"tools": []any{map[string]any{
			"name":        "get_weather",
			"description": "Get the current weather in a city",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]string{"type": "string"}},
				"required":   []string{"city"},
			},
		}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "https://api.featherless.ai/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/messages: %s", resp.Status)
	}

	var (
		sawToolUse bool
		keepAlives int
		maxGap     time.Duration
		last       = started
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if gap := time.Since(last); gap > maxGap {
			maxGap = gap
		}
		last = time.Now()
		line := scanner.Text()
		if strings.HasPrefix(line, ":") {
			keepAlives++
		}
		if strings.Contains(line, `"tool_use"`) {
			sawToolUse = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	t.Logf("stream: %d keep-alive comments, worst byte silence %v (watchdog trips at %v)",
		keepAlives, maxGap.Round(time.Millisecond), watchdogTrigger)

	if !sawToolUse {
		t.Error("no tool_use block: Claude Code cannot read or edit a file without one")
	}
	// The property the profile depends on: whatever fills the wait, nothing
	// leaves the socket silent long enough for Claude Code to call the turn a
	// dead connection. If this fails, the profile needs the byte watchdog
	// disarmed the way a self-hosted one does.
	if maxGap >= liveGapCeiling {
		t.Errorf("byte silence reached %v, within reach of the %v watchdog trigger",
			maxGap.Round(time.Millisecond), watchdogTrigger)
	}
}
