package gptbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func userItem(text string) map[string]any {
	return inputHistoryItem([]UserInput{{Type: "text", Text: text}})
}

func assistantItem(text string) map[string]any {
	return map[string]any{"type": "message", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text}}}
}

func parkingEngine(t *testing.T, rpc *fakeEngineRPC, limit int, idle time.Duration) *Engine {
	t.Helper()
	rpc.onTurnStart = func(threadID, turnID string) { completeTextTurn(rpc, threadID, turnID, "ok") }
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), Models: []string{"gpt-test"},
		ToolBatchWindow: 5 * time.Millisecond, ParkLimit: limit, ParkIdle: idle,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	return engine
}

func parkCalls(rpc *fakeEngineRPC, method string) int {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	n := 0
	for _, call := range rpc.calls {
		if call == method {
			n++
		}
	}
	return n
}

func parkRun(t *testing.T, engine *Engine, translation Translation) {
	t.Helper()
	if _, err := engine.Execute(context.Background(), translation, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEngineReusesAParkedThreadForTheNextMessage(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, time.Hour)
	parkRun(t, engine, testTranslation("first"))
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	parkRun(t, engine, next)
	if got := parkCalls(rpc, "thread/start"); got != 1 {
		t.Fatalf("thread/start = %d, want the thread reused", got)
	}
	if got := parkCalls(rpc, "thread/inject_items"); got != 0 {
		t.Fatalf("a reused thread replayed history %d times", got)
	}
	if got := parkCalls(rpc, "thread/delete"); got != 0 {
		t.Fatalf("thread/delete = %d, want the thread kept", got)
	}
}

func TestEngineStartsANewThreadWhenAnythingDiffers(t *testing.T) {
	cases := map[string]func(next *Translation){
		"model":        func(n *Translation) { n.Model = "gpt-other" },
		"system":       func(n *Translation) { n.System = "changed" },
		"tools":        func(n *Translation) { n.DynamicTools = nil },
		"edited input": func(n *Translation) { n.History[0] = userItem("rewritten") },
		"extra user":   func(n *Translation) { n.History = append(n.History, userItem("queued")) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rpc := newFakeEngineRPC()
			engine := parkingEngine(t, rpc, 16, time.Hour)
			engine.models["gpt-other"] = true
			parkRun(t, engine, testTranslation("first"))
			next := testTranslation("second")
			next.History = []map[string]any{userItem("first"), assistantItem("ok")}
			mutate(&next)
			parkRun(t, engine, next)
			if got := parkCalls(rpc, "thread/start"); got != 2 {
				t.Fatalf("thread/start = %d, want a new thread", got)
			}
		})
	}
}

func TestEngineDeletesParkedThreadsPastTheLimit(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 2, time.Hour)
	for _, text := range []string{"a", "b", "c"} {
		parkRun(t, engine, testTranslation(text))
	}
	if got := parkCalls(rpc, "thread/delete"); got != 1 {
		t.Fatalf("thread/delete = %d, want the oldest of three dropped", got)
	}
}

func TestEngineDeletesAnIdleParkedThread(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, 20*time.Millisecond)
	parkRun(t, engine, testTranslation("a"))
	deadline := time.Now().Add(2 * time.Second)
	for parkCalls(rpc, "thread/delete") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("an idle parked thread was never deleted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEngineHandsAParkedThreadToOneRequestOnly(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, time.Hour)
	parkRun(t, engine, testTranslation("first"))
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	a := engine.takeParked(next)
	b := engine.takeParked(next)
	if a == nil || b != nil {
		t.Fatalf("takeParked twice gave %v and %v, want one thread then none", a, b)
	}
	engine.cleanupTurn(a, false)
}

func TestEngineDropsAStrayEventForAParkedThread(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, time.Hour)
	parkRun(t, engine, testTranslation("first"))
	rpc.notifications <- notification("item/agentMessage/delta", "thread-1", "turn-stale",
		`"itemId":"late","delta":"stale"`)
	time.Sleep(20 * time.Millisecond)
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	message, err := engine.Execute(context.Background(), next, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(message.Content)
	if string(raw) != `[{"type":"text","text":"ok"}]` {
		t.Fatalf("content = %s, want only the new turn's text", raw)
	}
}

func TestEngineNeverParksAFailedTurn(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.notifications <- Notification{Method: "turn/completed", Params: json.RawMessage(
			`{"threadId":"` + threadID + `","turn":{"id":"` + turnID + `","status":"failed","error":{"message":"boom"},"items":[]}}`)}
	}
	engine, err := NewEngine(rpc, EngineOptions{PrivateCWD: t.TempDir(), Models: []string{"gpt-test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	_, _ = engine.Execute(context.Background(), testTranslation("a"), nil)
	engine.mu.Lock()
	parked := len(engine.parked)
	engine.mu.Unlock()
	if parked != 0 {
		t.Fatalf("a failed turn was parked")
	}
}
