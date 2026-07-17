package gptbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeEngineRPC struct {
	notifications chan Notification
	requests      chan ServerRequest
	done          chan struct{}

	mu          sync.Mutex
	nextThread  int
	calls       []string
	responses   []fakeResponse
	onTurnStart func(threadID, turnID string)
	onRespond   func(count int)
}

type fakeResponse struct {
	ID     string
	Result json.RawMessage
}

func newFakeEngineRPC() *fakeEngineRPC {
	return &fakeEngineRPC{
		notifications: make(chan Notification, 64),
		requests:      make(chan ServerRequest, 64),
		done:          make(chan struct{}),
	}
}

func (f *fakeEngineRPC) Call(_ context.Context, method string, params, target any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.mu.Unlock()
	switch method {
	case "thread/start":
		f.mu.Lock()
		f.nextThread++
		threadID := fmt.Sprintf("thread-%d", f.nextThread)
		f.mu.Unlock()
		return remarshal(map[string]any{
			"thread": map[string]any{"id": threadID},
			"model":  "gpt-test",
		}, target)
	case "thread/inject_items", "turn/interrupt", "thread/delete":
		return remarshal(map[string]any{}, target)
	case "turn/start":
		raw, _ := json.Marshal(params)
		var value struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(raw, &value)
		turnID := "turn-" + strings.TrimPrefix(value.ThreadID, "thread-")
		if err := remarshal(map[string]any{
			"turn": map[string]any{"id": turnID, "status": "inProgress", "items": []any{}},
		}, target); err != nil {
			return err
		}
		if f.onTurnStart != nil {
			f.onTurnStart(value.ThreadID, turnID)
		}
		return nil
	default:
		return fmt.Errorf("unexpected call %s", method)
	}
}

func (f *fakeEngineRPC) Respond(id RequestID, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.responses = append(f.responses, fakeResponse{ID: id.String(), Result: raw})
	count := len(f.responses)
	onRespond := f.onRespond
	f.mu.Unlock()
	if onRespond != nil {
		onRespond(count)
	}
	return nil
}

func (f *fakeEngineRPC) RespondError(RequestID, int64, string, any) error { return nil }
func (f *fakeEngineRPC) Notifications() <-chan Notification               { return f.notifications }
func (f *fakeEngineRPC) ServerRequests() <-chan ServerRequest             { return f.requests }
func (f *fakeEngineRPC) Done() <-chan struct{}                            { return f.done }
func (f *fakeEngineRPC) Err() error                                       { return errors.New("fake closed") }

func remarshal(value, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	return json.Unmarshal(data, target)
}

func fakeRequestID(value string) RequestID {
	raw, _ := json.Marshal(value)
	id, _ := decodeRequestID(raw)
	return id
}

func testTranslation(text string) Translation {
	return Translation{
		Model: "gpt-test", MaxTokens: 100, Input: []UserInput{{Type: "text", Text: text}},
		DynamicTools: []DynamicTool{{
			Type: "function", Name: "Echo", Description: "echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	}
}

func notification(method, threadID, turnID, extra string) Notification {
	if extra != "" {
		extra = "," + extra
	}
	return Notification{
		Method: method,
		Params: json.RawMessage(fmt.Sprintf(
			`{"threadId":%q,"turnId":%q%s}`, threadID, turnID, extra,
		)),
	}
}

func completeTextTurn(rpc *fakeEngineRPC, threadID, turnID, text string) {
	rpc.notifications <- notification(
		"item/agentMessage/delta", threadID, turnID,
		fmt.Sprintf(`"itemId":"agent","delta":%q`, text),
	)
	rpc.notifications <- Notification{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(fmt.Sprintf(`{
			"threadId":%q,"turnId":%q,
			"tokenUsage":{"last":{"inputTokens":10,"cachedInputTokens":2,"outputTokens":3,"reasoningOutputTokens":0,"totalTokens":13}}
		}`, threadID, turnID)),
	}
	rpc.notifications <- Notification{
		Method: "turn/completed",
		Params: json.RawMessage(fmt.Sprintf(
			`{"threadId":%q,"turn":{"id":%q,"status":"completed","items":[]}}`,
			threadID, turnID,
		)),
	}
}

func newTestEngine(t *testing.T, rpc *fakeEngineRPC) *Engine {
	t.Helper()
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD:      t.TempDir(),
		ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL:      time.Second,
		Models:          []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	return engine
}

func TestEngineCompletesTextTurnAndDeletesThread(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		completeTextTurn(rpc, threadID, turnID, "hello")
	}
	engine := newTestEngine(t, rpc)

	var events []StreamEvent
	message, err := engine.Execute(context.Background(), testTranslation("hi"), func(got []StreamEvent) error {
		events = append(events, got...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "hello" {
		t.Fatalf("message = %+v", message)
	}
	if message.StopReason != "end_turn" || len(events) == 0 {
		t.Fatalf("response = %+v, events = %+v", message, events)
	}
	rpc.mu.Lock()
	calls := append([]string(nil), rpc.calls...)
	rpc.mu.Unlock()
	if calls[len(calls)-1] != "thread/delete" {
		t.Fatalf("calls = %v, want final thread/delete", calls)
	}
	if engine.PendingTurns() != 0 {
		t.Fatalf("pending turns = %d", engine.PendingTurns())
	}
}

func TestEngineSuspendsAndResumesParallelDynamicTools(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		for index, name := range []string{"Echo", "Echo"} {
			rpc.requests <- ServerRequest{
				ID:     fakeRequestID(fmt.Sprintf("rpc-%d", index)),
				Method: "item/tool/call",
				Params: json.RawMessage(fmt.Sprintf(
					`{"threadId":%q,"turnId":%q,"callId":"call-%d","tool":%q,"arguments":{"value":%d}}`,
					threadID, turnID, index, name, index,
				)),
			}
		}
	}
	rpc.onRespond = func(count int) {
		if count == 2 {
			completeTextTurn(rpc, "thread-1", "turn-1", "both complete")
		}
	}
	engine := newTestEngine(t, rpc)

	first, err := engine.Execute(context.Background(), testTranslation("tools"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.StopReason != "tool_use" || len(first.Content) != 2 {
		t.Fatalf("first response = %+v", first)
	}
	if engine.PendingTurns() != 1 {
		t.Fatalf("pending turns = %d, want 1", engine.PendingTurns())
	}

	continuation := Translation{
		Model: "gpt-test", MaxTokens: 100,
		ToolResults: []TranslatedToolResult{
			{
				ToolUseID: first.Content[1].ID, Success: true,
				ContentItems: []ToolOutputItem{{Type: "inputText", Text: "second"}},
			},
			{
				ToolUseID: first.Content[0].ID, Success: false,
				ContentItems: []ToolOutputItem{{Type: "inputText", Text: "first"}},
			},
		},
	}
	second, err := engine.Execute(context.Background(), continuation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Content) != 1 || second.Content[0].Text != "both complete" {
		t.Fatalf("second response = %+v", second)
	}
	rpc.mu.Lock()
	responses := append([]fakeResponse(nil), rpc.responses...)
	rpc.mu.Unlock()
	if len(responses) != 2 || responses[0].ID != "rpc-1" || responses[1].ID != "rpc-0" {
		t.Fatalf("responses = %+v", responses)
	}
	if !strings.Contains(string(responses[1].Result), `"success":false`) {
		t.Fatalf("error response = %s", responses[1].Result)
	}
}

func TestEngineRejectsMissingDuplicateAndCrossTurnResults(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.requests <- ServerRequest{
			ID:     fakeRequestID("rpc"),
			Method: "item/tool/call",
			Params: json.RawMessage(fmt.Sprintf(
				`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Echo","arguments":{}}`,
				threadID, turnID,
			)),
		}
	}
	engine := newTestEngine(t, rpc)
	first, err := engine.Execute(context.Background(), testTranslation("one"), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := first.Content[0].ID

	tests := []struct {
		name    string
		results []TranslatedToolResult
	}{
		{"missing", nil},
		{"unknown", []TranslatedToolResult{{ToolUseID: "toolu_unknown", Success: true}}},
		{"duplicate", []TranslatedToolResult{{ToolUseID: id, Success: true}, {ToolUseID: id, Success: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Execute(context.Background(), Translation{
				Model: "gpt-test", MaxTokens: 100, ToolResults: tt.results,
			}, nil)
			if err == nil {
				t.Fatal("expected tool-result validation error")
			}
		})
	}
}

func TestEngineConcurrentTurnsAreIsolated(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.requests <- ServerRequest{
			ID:     fakeRequestID("rpc-" + threadID),
			Method: "item/tool/call",
			Params: json.RawMessage(fmt.Sprintf(
				`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Echo","arguments":{"thread":%q}}`,
				threadID, turnID, threadID,
			)),
		}
	}
	engine := newTestEngine(t, rpc)
	results := make(chan AnthropicMessage, 2)
	errs := make(chan error, 2)
	for _, text := range []string{"one", "two"} {
		go func() {
			message, err := engine.Execute(context.Background(), testTranslation(text), nil)
			results <- message
			errs <- err
		}()
	}
	first, second := <-results, <-results
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if first.Content[0].ID == second.Content[0].ID {
		t.Fatal("concurrent turns received the same bridge tool ID")
	}
	mixed := Translation{
		Model: "gpt-test", MaxTokens: 100,
		ToolResults: []TranslatedToolResult{
			{ToolUseID: first.Content[0].ID, Success: true},
			{ToolUseID: second.Content[0].ID, Success: true},
		},
	}
	if _, err := engine.Execute(context.Background(), mixed, nil); err == nil ||
		!strings.Contains(err.Error(), "different turns") {
		t.Fatalf("cross-turn result error = %v", err)
	}
}

func TestEngineCancellationInterruptsAndDeletesTurn(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := newTestEngine(t, rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := engine.Execute(ctx, testTranslation("wait"), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute error = %v", err)
	}
	rpc.mu.Lock()
	calls := strings.Join(rpc.calls, ",")
	rpc.mu.Unlock()
	if !strings.Contains(calls, "turn/interrupt") || !strings.Contains(calls, "thread/delete") {
		t.Fatalf("cleanup calls = %s", calls)
	}
}

func TestEngineRejectsCodexOwnedTools(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.notifications <- notification(
			"item/started", threadID, turnID,
			`"startedAtMs":1,"item":{"id":"bad","type":"commandExecution"}`,
		)
	}
	engine := newTestEngine(t, rpc)
	if _, err := engine.Execute(context.Background(), testTranslation("unsafe"), nil); err == nil ||
		!strings.Contains(err.Error(), "forbidden Codex-owned tool") {
		t.Fatalf("Execute error = %v", err)
	}
}

func TestEnginePendingToolTurnExpires(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.requests <- ServerRequest{
			ID:     fakeRequestID("rpc-expire"),
			Method: "item/tool/call",
			Params: json.RawMessage(fmt.Sprintf(
				`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Echo","arguments":{}}`,
				threadID, turnID,
			)),
		}
	}
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: time.Millisecond,
		PendingTTL: 15 * time.Millisecond, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	first, err := engine.Execute(context.Background(), testTranslation("expire"), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := first.Content[0].ID
	deadline := time.Now().Add(time.Second)
	for engine.PendingTurns() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if engine.PendingTurns() != 0 {
		t.Fatal("expired pending turn remained in memory")
	}
	_, err = engine.Execute(context.Background(), Translation{
		Model: "gpt-test", MaxTokens: 100,
		ToolResults: []TranslatedToolResult{{ToolUseID: id, Success: true}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown or expired") {
		t.Fatalf("stale result error = %v", err)
	}
}
