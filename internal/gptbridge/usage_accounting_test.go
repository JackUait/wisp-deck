package gptbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestResponseReducerDoesNotBillPromptEstimates(t *testing.T) {
	for _, stop := range []string{"tool_use", "end_turn"} {
		t.Run(stop, func(t *testing.T) {
			reducer := NewResponseReducer(ResponseOptions{
				MessageID: "msg_estimate", Model: "gpt-6.1-sol", EstimatedInputTokens: 100000,
			})
			var start struct {
				Message struct {
					Usage Usage `json:"usage"`
				} `json:"message"`
			}
			decodeEventData(t, reducer.Start()[0].Data, &start)
			if start.Message.Usage.InputTokens != 0 {
				t.Fatalf("message_start bills %d estimated input tokens", start.Message.Usage.InputTokens)
			}
			if _, err := reducer.Apply(notification("item/agentMessage/delta", "thread-1", "turn-1",
				`"itemId":"text","delta":"hello"`)); err != nil {
				t.Fatal(err)
			}
			events, err := reducer.Finish(stop)
			if err != nil {
				t.Fatal(err)
			}
			got := messageDeltaUsage(t, events)
			if got.InputTokens != 0 || got.CacheReadInputTokens != 0 || got.OutputTokens != 0 {
				t.Fatalf("unreported usage = %+v, want zero billed tokens", got)
			}
		})
	}
}

func accountingUsage(thread, turn string, input, cached, output, totalInput, totalCached, totalOutput int64) Notification {
	return notification("thread/tokenUsage/updated", thread, turn, fmt.Sprintf(
		`"tokenUsage":{"last":{"inputTokens":%d,"cachedInputTokens":%d,"outputTokens":%d},"total":{"inputTokens":%d,"cachedInputTokens":%d,"outputTokens":%d}}`,
		input, cached, output, totalInput, totalCached, totalOutput))
}

func billedUsage(t *testing.T, message AnthropicMessage) Usage {
	t.Helper()
	data, err := json.Marshal(message.Usage)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Iterations []struct {
			Model  string `json:"model"`
			Input  int64  `json:"input_tokens"`
			Cache  int64  `json:"cache_read_input_tokens"`
			Output int64  `json:"output_tokens"`
		} `json:"iterations"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Iterations) == 0 {
		return message.Usage
	}
	var usage Usage
	for _, iteration := range wire.Iterations {
		if iteration.Model != message.Model {
			t.Fatalf("iteration model = %q, want %q", iteration.Model, message.Model)
		}
		usage.InputTokens += iteration.Input
		usage.CacheReadInputTokens += iteration.Cache
		usage.OutputTokens += iteration.Output
	}
	return usage
}

func TestResponseReducerKeepsContextSeparateFromCumulativeBilling(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{MessageID: "msg_totals", Model: "gpt-6.1-sol"})
	for _, n := range []Notification{
		accountingUsage("thread-1", "turn-1", 1000, 900, 30, 1000, 900, 30),
		accountingUsage("thread-1", "turn-1", 2000, 1800, 60, 3000, 2700, 90),
		accountingUsage("thread-1", "turn-1", 2000, 1800, 60, 3000, 2700, 90),
	} {
		if _, err := reducer.Apply(n); err != nil {
			t.Fatal(err)
		}
	}
	events, err := reducer.Finish("end_turn")
	if err != nil {
		t.Fatal(err)
	}
	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	if message.Usage.InputTokens != 200 || message.Usage.CacheReadInputTokens != 1800 || message.Usage.OutputTokens != 60 {
		t.Fatalf("latest context usage = %+v, want 200/1800/60", message.Usage)
	}
	got := billedUsage(t, message)
	if got.InputTokens != 300 || got.CacheReadInputTokens != 2700 || got.OutputTokens != 90 {
		t.Fatalf("billed usage = %+v, want 300/2700/90", got)
	}
	streamed := messageDeltaUsage(t, events)
	wire, _ := json.Marshal(streamed)
	messageWire, _ := json.Marshal(message.Usage)
	if string(wire) != string(messageWire) {
		t.Fatalf("streamed usage %s differs from message %s", wire, messageWire)
	}
}

func TestEngineCountsLateUsageOnce(t *testing.T) {
	rpc := newFakeEngineRPC()
	tool := func(callID string) {
		rpc.requests <- ServerRequest{
			ID: fakeRequestID("rpc-" + callID), Method: "item/tool/call",
			Params: json.RawMessage(fmt.Sprintf(
				`{"threadId":"thread-1","turnId":"turn-1","callId":%q,"tool":"Echo","arguments":{}}`, callID)),
		}
	}
	rpc.onTurnStart = func(_, _ string) { tool("first") }
	rpc.onRespond = func(count int) {
		if count == 1 {
			tool("second")
			return
		}
		rpc.notifications <- accountingUsage("thread-1", "turn-1", 1000, 900, 30, 1000, 900, 30)
		rpc.notifications <- notification("turn/completed", "thread-1", "turn-1",
			`"turn":{"id":"turn-1","status":"completed"}`)
	}
	engine := newTestEngine(t, rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	translation := testTranslation("tools")
	translation.EstimatedInputTokens = 100000
	first, err := engine.Execute(ctx, translation, nil)
	if err != nil {
		t.Fatal(err)
	}
	rpc.notifications <- accountingUsage("thread-1", "turn-1", 1000, 900, 30, 1000, 900, 30)
	resume := func(message AnthropicMessage) AnthropicMessage {
		t.Helper()
		next, err := engine.Execute(ctx, Translation{
			Model: "gpt-test", MaxTokens: 100, EstimatedInputTokens: 100100,
			ToolResults: []TranslatedToolResult{{
				ToolUseID: message.Content[0].ID, Success: true,
				ContentItems: []ToolOutputItem{{Type: "inputText", Text: "ok"}},
			}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	second := resume(first)
	third := resume(second)
	var total Usage
	for _, message := range []AnthropicMessage{first, second, third} {
		u := billedUsage(t, message)
		total.InputTokens += u.InputTokens
		total.CacheReadInputTokens += u.CacheReadInputTokens
		total.OutputTokens += u.OutputTokens
	}
	if total.InputTokens != 100 || total.CacheReadInputTokens != 900 || total.OutputTokens != 30 {
		t.Fatalf("total billed usage = %+v, want one request: 100/900/30", total)
	}
}

func TestEngineBillingBaselineSurvivesParkedReuse(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := newTestEngine(t, rpc)
	starts := 0
	rpc.onTurnStart = func(thread, turn string) {
		starts++
		if starts == 1 {
			rpc.notifications <- accountingUsage(thread, turn, 1000, 900, 30, 1000, 900, 30)
		} else {
			rpc.notifications <- accountingUsage(thread, turn, 2000, 1800, 60, 3000, 2700, 90)
		}
		rpc.notifications <- notification("item/agentMessage/delta", thread, turn, `"itemId":"agent","delta":"ok"`)
		rpc.notifications <- notification("turn/completed", thread, turn,
			fmt.Sprintf(`"turn":{"id":%q,"status":"completed"}`, turn))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := engine.Execute(ctx, testTranslation("first"), nil); err != nil {
		t.Fatal(err)
	}
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	second, err := engine.Execute(ctx, next, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls := parkCalls(rpc, "thread/start"); calls != 1 {
		t.Fatalf("thread starts = %d, want one reused thread", calls)
	}
	got := billedUsage(t, second)
	if got.InputTokens != 200 || got.CacheReadInputTokens != 1800 || got.OutputTokens != 60 {
		t.Fatalf("reused thread billed usage = %+v, want only new request: 200/1800/60", got)
	}
}

func TestResponseReducerKeepsBilledUsageAcrossCounterReset(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{MessageID: "msg_reset", Model: "gpt-6.1-sol"})
	for _, n := range []Notification{
		accountingUsage("thread-1", "turn-1", 1000, 900, 30, 1000, 900, 30),
		accountingUsage("thread-1", "turn-1", 0, 0, 0, 0, 0, 0),
		accountingUsage("thread-1", "turn-1", 2000, 1800, 60, 2000, 1800, 60),
	} {
		if _, err := reducer.Apply(n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reducer.Finish("end_turn"); err != nil {
		t.Fatal(err)
	}
	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	got := billedUsage(t, message)
	if got.InputTokens != 300 || got.CacheReadInputTokens != 2700 || got.OutputTokens != 90 {
		t.Fatalf("billed usage after reset = %+v, want 300/2700/90", got)
	}
}
