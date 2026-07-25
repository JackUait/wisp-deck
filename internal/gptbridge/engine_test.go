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
	callParams  []json.RawMessage
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
	raw, _ := json.Marshal(params)
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.callParams = append(f.callParams, raw)
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

func TestEngineRecoversExpiredContinuationFromValidatedHistory(t *testing.T) {
	rpc := newFakeEngineRPC()
	starts := 0
	rpc.onTurnStart = func(threadID, turnID string) {
		starts++
		if starts == 1 {
			rpc.requests <- ServerRequest{
				ID:     fakeRequestID("rpc-expired-recovery"),
				Method: "item/tool/call",
				Params: json.RawMessage(fmt.Sprintf(
					`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Echo","arguments":{}}`,
					threadID, turnID,
				)),
			}
			return
		}
		completeTextTurn(rpc, threadID, turnID, "recovered")
	}
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: time.Millisecond,
		PendingTTL: 15 * time.Millisecond, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	first, err := engine.Execute(context.Background(), testTranslation("run tool"), nil)
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

	continuation := testTranslation("")
	continuation.Input = nil
	continuation.History = []map[string]any{{
		"type": "function_call", "call_id": id,
		"name": "Echo", "arguments": `{}`,
	}}
	continuation.ToolResults = []TranslatedToolResult{{
		ToolUseID: id, Success: true,
		ContentItems: []ToolOutputItem{{Type: "inputText", Text: "tool finished"}},
	}}
	message, err := engine.Execute(context.Background(), continuation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "recovered" {
		t.Fatalf("recovered response = %+v", message)
	}

	rpc.mu.Lock()
	calls := append([]string(nil), rpc.calls...)
	params := append([]json.RawMessage(nil), rpc.callParams...)
	responses := append([]fakeResponse(nil), rpc.responses...)
	rpc.mu.Unlock()
	var injected string
	for index, method := range calls {
		if method == "thread/inject_items" {
			injected += string(params[index])
		}
	}
	if !strings.Contains(injected, `"type":"function_call_output"`) ||
		!strings.Contains(injected, `"call_id":"`+id+`"`) ||
		!strings.Contains(injected, "tool finished") {
		t.Fatalf("recovery history = %s", injected)
	}
	if len(responses) != 0 {
		t.Fatalf("expired app-server request was answered again: %+v", responses)
	}
	var turnInputs string
	for index, method := range calls {
		if method == "turn/start" {
			turnInputs += string(params[index])
		}
	}
	if !strings.Contains(turnInputs, "tool results above are complete") {
		t.Fatalf("recovery turn/start is missing the synthetic continuation input: %s", turnInputs)
	}
	// A recovered turn replays the whole conversation on a fresh thread; without
	// an explicit directive the model re-runs completed tools and re-asks
	// already-answered questions (observed live: duplicate Skill launches and a
	// repeated AskUserQuestion after its answer was already in history).
	if !strings.Contains(turnInputs, "Do not repeat tool calls that already have results above") {
		t.Fatalf("recovery turn/start is missing the no-repeat directive: %s", turnInputs)
	}
}

// Claude Code can request compaction in the same final user message as a
// pending tool result. If that continuation has expired, recovery must replay
// only the actual tool output and keep the compaction instruction as fresh
// model input; hiding it in function output makes GPT continue with tools and
// leaves Claude Code with an empty summary.
func TestEngineRecoveryPreservesCompactionInstructionAsFreshInput(t *testing.T) {
	rpc := newFakeEngineRPC()
	starts := 0
	rpc.onTurnStart = func(threadID, turnID string) {
		starts++
		if starts == 1 {
			rpc.requests <- ServerRequest{
				ID:     fakeRequestID("rpc-compaction-recovery"),
				Method: "item/tool/call",
				Params: json.RawMessage(fmt.Sprintf(
					`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Bash","arguments":{}}`,
					threadID, turnID,
				)),
			}
			return
		}
		completeTextTurn(rpc, threadID, turnID, "summary text")
	}
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: time.Millisecond,
		PendingTTL: 15 * time.Millisecond, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	first := testTranslation("Run lint")
	first.DynamicTools[0].Name = "Bash"
	message, err := engine.Execute(context.Background(), first, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := message.Content[0].ID
	deadline := time.Now().Add(time.Second)
	for engine.PendingTurns() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if engine.PendingTurns() != 0 {
		t.Fatal("expired pending turn remained in memory")
	}

	const compact = "Summarize the conversation. Do not call tools; respond with plain text only."
	continuation := testTranslation("")
	continuation.History = []map[string]any{{
		"type": "function_call", "call_id": id,
		"name": "Bash", "arguments": `{}`,
	}}
	continuation.Input = []UserInput{{Type: "text", Text: compact}}
	continuation.ToolResults = []TranslatedToolResult{{
		ToolUseID: id,
		Success:   true,
		ContentItems: []ToolOutputItem{
			{Type: "inputText", Text: "Exit code 1"},
			{Type: "inputText", Text: compact},
		},
		HistoryContentItems: []ToolOutputItem{{Type: "inputText", Text: "Exit code 1"}},
	}}
	message, err = engine.Execute(context.Background(), continuation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "summary text" {
		t.Fatalf("recovered response = %+v", message)
	}

	rpc.mu.Lock()
	calls := append([]string(nil), rpc.calls...)
	params := append([]json.RawMessage(nil), rpc.callParams...)
	rpc.mu.Unlock()
	var injected, turnInputs string
	for index, method := range calls {
		switch method {
		case "thread/inject_items":
			injected += string(params[index])
		case "turn/start":
			turnInputs += string(params[index])
		}
	}
	if !strings.Contains(injected, "Exit code 1") {
		t.Fatalf("recovery history is missing real tool output: %s", injected)
	}
	if strings.Contains(injected, compact) {
		t.Fatalf("supplemental instruction leaked into function output history: %s", injected)
	}
	if !strings.Contains(turnInputs, "tool results above are complete") ||
		!strings.Contains(turnInputs, compact) {
		t.Fatalf("recovery input = %s", turnInputs)
	}
}

func TestEngineRecoveryRefusedWithoutHistoryStaysInvalidContinuation(t *testing.T) {
	// The 400 mapping in writeExecutionError depends on the refused-recovery
	// error still being an invalidContinuationError; a bare unknown error would
	// silently become a retryable 502 again.
	rpc := newFakeEngineRPC()
	engine := newTestEngine(t, rpc)
	_, err := engine.Execute(context.Background(), Translation{
		Model: "gpt-test", MaxTokens: 100,
		ToolResults: []TranslatedToolResult{{ToolUseID: "toolu_wisp_gone", Success: true}},
	}, nil)
	var invalid invalidContinuationError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "unknown or expired") {
		t.Fatalf("refused recovery error = %v, want invalidContinuationError", err)
	}
}

func TestEngineRecoveryCleansUpLiveTurnFromMixedContinuation(t *testing.T) {
	// A replayed continuation can pair an expired bridge ID with one that still
	// owns a live suspended turn. Recovery must interrupt/delete that live turn
	// instead of leaking its Codex thread and unanswered request for the TTL.
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		if threadID != "thread-1" && threadID != "thread-2" {
			completeTextTurn(rpc, threadID, turnID, "recovered")
			return
		}
		rpc.requests <- ServerRequest{
			ID:     fakeRequestID("rpc-" + threadID),
			Method: "item/tool/call",
			Params: json.RawMessage(fmt.Sprintf(
				`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Echo","arguments":{}}`,
				threadID, turnID,
			)),
		}
	}
	// A long TTL keeps expiry timers out of the picture: only the recovery path
	// itself may clean up the orphaned live turn.
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL: time.Hour, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	first, err := engine.Execute(context.Background(), testTranslation("one"), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Execute(context.Background(), testTranslation("two"), nil)
	if err != nil {
		t.Fatal(err)
	}
	liveID := second.Content[0].ID
	_ = first

	staleID := "toolu_wisp_already_consumed"
	continuation := testTranslation("")
	continuation.Input = nil
	continuation.History = []map[string]any{
		{"type": "function_call", "call_id": staleID, "name": "Echo", "arguments": `{}`},
		{"type": "function_call", "call_id": liveID, "name": "Echo", "arguments": `{}`},
	}
	continuation.ToolResults = []TranslatedToolResult{
		{ToolUseID: staleID, Success: true},
		{ToolUseID: liveID, Success: true},
	}
	message, err := engine.Execute(context.Background(), continuation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "recovered" {
		t.Fatalf("recovered response = %+v", message)
	}

	deadline := time.Now().Add(time.Second)
	for engine.PendingTurns() > 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := engine.PendingTurns(); got != 1 {
		t.Fatalf("pending turns after recovery = %d, want only the untouched first turn", got)
	}
	rpc.mu.Lock()
	calls := strings.Join(rpc.calls, ",")
	rpc.mu.Unlock()
	if !strings.Contains(calls, "turn/interrupt") {
		t.Fatalf("live orphaned turn was not interrupted: %s", calls)
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

// contextCompaction is a thread-lifecycle item, not a Codex-owned tool: the
// app-server emits it when the turn's own context fills up and it compacts
// itself. It grants the model no host capability, so it must never abort the
// turn — long GPT sessions hit it routinely.
func TestEngineToleratesContextCompactionItem(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.notifications <- notification(
			"item/started", threadID, turnID,
			`"startedAtMs":1,"item":{"id":"compact","type":"contextCompaction"}`,
		)
		rpc.notifications <- notification(
			"item/completed", threadID, turnID,
			`"item":{"id":"compact","type":"contextCompaction"}`,
		)
		completeTextTurn(rpc, threadID, turnID, "survived")
	}
	engine := newTestEngine(t, rpc)
	message, err := engine.Execute(context.Background(), testTranslation("long"), nil)
	if err != nil {
		t.Fatalf("compaction aborted the turn: %v", err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "survived" {
		t.Fatalf("message = %+v", message)
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

func TestDefaultPendingTTLOutlivesLongClaudeToolRuns(t *testing.T) {
	// Regression: the 10-minute default expired a suspended turn while a
	// legitimate Claude-side bash tool (a ~12-minute `yarn lint` run) was still
	// executing, permanently killing the conversation. Claude tools routinely
	// run 10+ minutes (bash timeouts, subagents), so the default must sit far
	// above any real tool runtime.
	if defaultPendingTTL < 12*time.Hour {
		t.Fatalf("defaultPendingTTL = %s; a live turn must survive long Claude tool runs (want >= 12h)", defaultPendingTTL)
	}
}
