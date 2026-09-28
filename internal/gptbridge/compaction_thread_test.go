package gptbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// countEngineCalls reports how many times the engine invoked one RPC method.
func countEngineCalls(rpc *fakeEngineRPC, method string) int {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	count := 0
	for _, call := range rpc.calls {
		if call == method {
			count++
		}
	}
	return count
}

// injectedItemsFor concatenates the params of every thread/inject_items call
// aimed at one thread. It has to be per-thread: the whole point of the rebuild
// is that the pre-compaction history was injected into the thread being
// abandoned, so a whole-run concatenation always contains it.
func injectedItemsFor(rpc *fakeEngineRPC, threadID string) string {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	var injected strings.Builder
	for index, call := range rpc.calls {
		if call != "thread/inject_items" {
			continue
		}
		var target struct {
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(rpc.callParams[index], &target); err != nil ||
			target.ThreadID != threadID {
			continue
		}
		injected.Write(rpc.callParams[index])
	}
	return injected.String()
}

// suspendOnFirstTurn makes the first turn call one dynamic tool, then answers
// whichever way the engine continues: a fresh thread replies with rebuiltReply,
// and a resume of the suspended thread replies with resumedReply. Both paths
// have to terminate, or a test that takes the wrong one hangs instead of
// reporting which path it took.
func suspendOnFirstTurn(rpc *fakeEngineRPC, requestID, rebuiltReply, resumedReply string) {
	starts := 0
	var suspendedThread, suspendedTurn string
	rpc.onTurnStart = func(threadID, turnID string) {
		starts++
		if starts == 1 {
			suspendedThread, suspendedTurn = threadID, turnID
			rpc.requests <- ServerRequest{
				ID:     fakeRequestID(requestID),
				Method: "item/tool/call",
				Params: json.RawMessage(fmt.Sprintf(
					`{"threadId":%q,"turnId":%q,"callId":"call","tool":"Echo","arguments":{}}`,
					threadID, turnID,
				)),
			}
			return
		}
		completeTextTurn(rpc, threadID, turnID, rebuiltReply)
	}
	rpc.onRespond = func(int) {
		completeTextTurn(rpc, suspendedThread, suspendedTurn, resumedReply)
	}
}

// preCompactionHistory is the conversation a turn is started with.
func preCompactionHistory() []map[string]any {
	return []map[string]any{
		{"type": "message", "role": "user", "content": "the whole earlier conversation"},
		{"type": "message", "role": "assistant", "content": "a long earlier reply"},
	}
}

// Claude Code's reactive autocompact preserves the trailing group verbatim, so
// a compaction that lands mid-tool-loop leaves the pending tool_result at the
// tail and the next request is a CONTINUATION. The Codex thread that owns that
// tool call still holds the whole pre-compaction conversation: resuming it
// hands the model exactly the context Claude Code just dropped, and Codex then
// reports that thread's size back through thread/tokenUsage/updated, so Claude
// Code sees a full window one turn after compacting and compacts again. Three
// of those in a row is the "Autocompact is thrashing" breaker, which aborts the
// turn. Observed live on 2026-09-04/06/07: postTokens 41,194 followed by a
// reported context of 243,069.
func TestEngineRebuildsTheThreadWhenClaudeCompactsMidToolCall(t *testing.T) {
	rpc := newFakeEngineRPC()
	suspendOnFirstTurn(rpc, "rpc-precompaction", "answered after compaction", "resumed on the stale thread")
	engine := newTestEngine(t, rpc)

	opening := testTranslation("work on the feature")
	opening.History = preCompactionHistory()
	started, err := engine.Execute(context.Background(), opening, nil)
	if err != nil {
		t.Fatal(err)
	}
	if started.StopReason != "tool_use" || len(started.Content) != 1 {
		t.Fatalf("opening response = %+v", started)
	}
	id := started.Content[0].ID

	// Claude Code compacted: the earlier conversation is gone, replaced by a
	// summary, and only the pending tool call survives.
	continuation := testTranslation("")
	continuation.Input = nil
	continuation.History = []map[string]any{
		{"type": "message", "role": "user", "content": "This session is being continued from a previous conversation that ran out of context."},
		{"type": "function_call", "call_id": id, "name": "Echo", "arguments": `{}`},
	}
	continuation.ToolResults = []TranslatedToolResult{{
		ToolUseID: id, Success: true,
		ContentItems: []ToolOutputItem{{Type: "inputText", Text: "tool finished"}},
	}}
	message, err := engine.Execute(context.Background(), continuation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "answered after compaction" {
		t.Fatalf("post-compaction response = %+v", message)
	}

	if starts := countEngineCalls(rpc, "thread/start"); starts != 2 {
		t.Fatalf("thread/start calls = %d, want 2: the compacted conversation must run on a "+
			"fresh Codex thread, not on the one still holding the dropped history", starts)
	}
	// thread-2 is the rebuild; it must carry the compacted conversation and
	// none of what the compaction dropped.
	injected := injectedItemsFor(rpc, "thread-2")
	if strings.Contains(injected, "the whole earlier conversation") {
		t.Fatalf("the rebuilt thread was given the dropped pre-compaction history: %s", injected)
	}
	if !strings.Contains(injected, "This session is being continued") ||
		!strings.Contains(injected, `"type":"function_call_output"`) ||
		!strings.Contains(injected, "tool finished") {
		t.Fatalf("the rebuilt thread is missing the compacted history or the tool result: %s", injected)
	}
	// The stale thread must be interrupted and deleted, not left holding an
	// unanswered app-server request and a quarter-million tokens of context.
	// The rebuilt thread-2 ends its turn normally and is parked for reuse.
	rpc.mu.Lock()
	staleDeleted := false
	for index, call := range rpc.calls {
		if call == "thread/delete" && strings.Contains(string(rpc.callParams[index]), `"thread-1"`) {
			staleDeleted = true
		}
	}
	rpc.mu.Unlock()
	if !staleDeleted {
		t.Fatal("the stale thread-1 was not deleted")
	}
	rpc.mu.Lock()
	responses := len(rpc.responses)
	rpc.mu.Unlock()
	if responses != 0 {
		t.Fatalf("the stale thread's pending tool request was answered %d times", responses)
	}
}

// The counterweight: an ordinary tool continuation extends the history the
// thread was built from, and must keep resuming that thread. Re-injecting the
// whole conversation on every tool round would make the check cost more than
// the bug it fixes.
func TestEngineResumesWhenTheHistoryStillExtendsTheThread(t *testing.T) {
	rpc := newFakeEngineRPC()
	suspendOnFirstTurn(rpc, "rpc-extending", "started a needless fresh thread", "resumed normally")
	engine := newTestEngine(t, rpc)

	opening := testTranslation("work on the feature")
	opening.History = preCompactionHistory()
	started, err := engine.Execute(context.Background(), opening, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := started.Content[0].ID

	continuation := testTranslation("")
	continuation.Input = nil
	continuation.History = append(preCompactionHistory(),
		map[string]any{"type": "message", "role": "user", "content": "work on the feature"},
		map[string]any{"type": "function_call", "call_id": id, "name": "Echo", "arguments": `{}`},
	)
	continuation.ToolResults = []TranslatedToolResult{{
		ToolUseID: id, Success: true,
		ContentItems: []ToolOutputItem{{Type: "inputText", Text: "tool finished"}},
	}}
	message, err := engine.Execute(context.Background(), continuation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "resumed normally" {
		t.Fatalf("continuation response = %+v", message)
	}
	if starts := countEngineCalls(rpc, "thread/start"); starts != 1 {
		t.Fatalf("thread/start calls = %d, want 1: an extending history must resume", starts)
	}
	rpc.mu.Lock()
	responses := len(rpc.responses)
	rpc.mu.Unlock()
	if responses != 1 {
		t.Fatalf("pending tool responses = %d, want 1 (the turn was not resumed)", responses)
	}
}

// A rewritten history the bridge cannot replay is the one case where neither
// path is safe, and resuming the stale thread is the wrong half of that choice:
// it would silently answer from a conversation the client no longer has. Fail
// the continuation instead, in the class the client already knows not to retry.
func TestEngineRefusesAStaleThreadItCannotRebuild(t *testing.T) {
	rpc := newFakeEngineRPC()
	suspendOnFirstTurn(rpc, "rpc-unrebuildable", "must not be reached", "resumed the stale thread")
	engine := newTestEngine(t, rpc)

	opening := testTranslation("work on the feature")
	opening.History = preCompactionHistory()
	started, err := engine.Execute(context.Background(), opening, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := started.Content[0].ID

	// Compacted, and the compaction did not preserve the pending tool call, so
	// the tool result has no function_call to anchor it to.
	continuation := testTranslation("")
	continuation.Input = nil
	continuation.History = []map[string]any{
		{"type": "message", "role": "user", "content": "This session is being continued from a previous conversation that ran out of context."},
	}
	continuation.ToolResults = []TranslatedToolResult{{
		ToolUseID: id, Success: true,
		ContentItems: []ToolOutputItem{{Type: "inputText", Text: "tool finished"}},
	}}
	if _, err := engine.Execute(context.Background(), continuation, nil); err == nil {
		t.Fatal("a stale thread that cannot be rebuilt was resumed instead of refused")
	} else {
		var invalid invalidContinuationError
		if !errors.As(err, &invalid) {
			t.Fatalf("refusal error = %v, want invalidContinuationError", err)
		}
	}
	if starts := countEngineCalls(rpc, "thread/start"); starts != 1 {
		t.Fatalf("thread/start calls = %d, want 1: nothing may run on the stale thread", starts)
	}
	rpc.mu.Lock()
	responses := len(rpc.responses)
	rpc.mu.Unlock()
	if responses != 0 {
		t.Fatalf("the stale thread's pending tool request was answered %d times", responses)
	}
}
