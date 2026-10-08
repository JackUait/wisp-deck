package gptbridge

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/usage"
)

func TestEngineDelayedUsagePricesGPT61SolOnce(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.requests <- ServerRequest{
			ID:     fakeRequestID("rpc-usage"),
			Method: "item/tool/call",
			Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","callId":"call-usage","tool":"Echo","arguments":{}}`),
		}
	}
	rpc.onRespond = func(count int) {
		rpc.notifications <- notification("thread/tokenUsage/updated", "thread-1", "turn-1",
			`"tokenUsage":{"last":{"inputTokens":1000,"cachedInputTokens":900,"outputTokens":30,"reasoningOutputTokens":0,"totalTokens":1030},"total":{"inputTokens":1000,"cachedInputTokens":900,"outputTokens":30,"reasoningOutputTokens":0,"totalTokens":1030}}`)
		rpc.notifications <- notification("thread/tokenUsage/updated", "thread-1", "turn-1",
			`"tokenUsage":{"last":{"inputTokens":2000,"cachedInputTokens":1800,"outputTokens":60,"reasoningOutputTokens":0,"totalTokens":2060},"total":{"inputTokens":3000,"cachedInputTokens":2700,"outputTokens":90,"reasoningOutputTokens":0,"totalTokens":3090}}`)
		rpc.notifications <- notification("item/agentMessage/delta", "thread-1", "turn-1",
			`"itemId":"agent","delta":"done"`)
		rpc.notifications <- notification("turn/completed", "thread-1", "",
			`"turn":{"id":"turn-1","status":"completed","items":[]}`)
	}
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL: time.Second, Models: []string{"gpt-6.1-sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	translation := testTranslation("tools")
	translation.Model = "gpt-6.1-sol"
	translation.EstimatedInputTokens = 100000
	first, err := engine.Execute(ctx, translation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.StopReason != "tool_use" || len(first.Content) != 1 {
		t.Fatalf("first response = %+v, want one tool call", first)
	}
	if first.Usage.InputTokens != 0 || first.Usage.CacheReadInputTokens != 0 || first.Usage.OutputTokens != 0 {
		t.Errorf("unreported tool-boundary usage = %+v, want no fabricated tokens", first.Usage)
	}

	second, err := engine.Execute(ctx, Translation{
		Model: "gpt-6.1-sol", MaxTokens: 100, EstimatedInputTokens: 100000,
		ToolResults: []TranslatedToolResult{{
			ToolUseID: first.Content[0].ID, Success: true,
			ContentItems: []ToolOutputItem{{Type: "inputText", Text: "done"}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.StopReason != "end_turn" {
		t.Fatalf("final stop reason = %q, want end_turn", second.StopReason)
	}
	if second.Usage.InputTokens != 200 || second.Usage.CacheReadInputTokens != 1800 || second.Usage.OutputTokens != 60 {
		t.Errorf("latest request usage = %+v, want input=200 cache=1800 output=60", second.Usage)
	}

	path := filepath.Join(t.TempDir(), "usage.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, message := range []AnthropicMessage{first, second} {
		if err := encoder.Encode(struct {
			Type      string           `json:"type"`
			Timestamp string           `json:"timestamp"`
			Message   AnthropicMessage `json:"message"`
		}{"assistant", "2026-10-08T00:00:00Z", message}); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	months, _, err := usage.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	month := months["2026-10"]
	if month == nil || len(month.Models) != 1 || month.Models[0].Model != "gpt-6.1-sol" {
		t.Fatalf("parsed usage = %+v, want one GPT-6.1 Sol row", month)
	}
	got := month.Models[0]
	if got.Input != 300 || got.CacheRead != 2700 || got.Output != 90 || got.CacheWrite != 0 {
		t.Errorf("parsed tokens = %+v, want input=300 cache=2700 output=90 writes=0", got)
	}
	cost, priced := usage.ModelCostUSD(got)
	if !priced || math.Abs(cost-0.00177) > 1e-12 {
		t.Errorf("cost = %.12f, priced=%t, want $0.00177", cost, priced)
	}
}
