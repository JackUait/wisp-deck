package gptbridge

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fuseClock struct{ at time.Time }

func (c *fuseClock) now() time.Time { return c.at }

func newTestFuse(clock *fuseClock) (*ColdStartFuse, *[]ColdStartWarning) {
	var warnings []ColdStartWarning
	fuse := NewColdStartFuse(func(w ColdStartWarning) { warnings = append(warnings, w) })
	fuse.now = clock.now
	fuse.async = false
	return fuse, &warnings
}

func TestColdStartFuseWarnsOnceWhenTheWindowPassesTheThreshold(t *testing.T) {
	clock := &fuseClock{at: time.Unix(1_000_000, 0)}
	fuse, warnings := newTestFuse(clock)
	for i := 0; i < 17; i++ {
		fuse.Record(125_000)
		clock.at = clock.at.Add(30 * time.Second)
	}
	if len(*warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one", *warnings)
	}
	w := (*warnings)[0]
	if w.Tokens <= 2_000_000 || w.Starts < 17 || w.Window != 10*time.Minute {
		t.Fatalf("warning = %+v", w)
	}
}

func TestColdStartFuseStaysQuietBelowTheThreshold(t *testing.T) {
	clock := &fuseClock{at: time.Unix(1_000_000, 0)}
	fuse, warnings := newTestFuse(clock)
	for i := 0; i < 100; i++ {
		fuse.Record(150_000)
		clock.at = clock.at.Add(time.Minute)
	}
	if len(*warnings) != 0 {
		t.Fatalf("1.5M per 10 min warned: %+v", *warnings)
	}
}

func TestColdStartFuseWarnsAgainOnlyAfterAnHour(t *testing.T) {
	clock := &fuseClock{at: time.Unix(1_000_000, 0)}
	fuse, warnings := newTestFuse(clock)
	burst := func() {
		for i := 0; i < 5; i++ {
			fuse.Record(1_000_000)
		}
	}
	burst()
	clock.at = clock.at.Add(30 * time.Minute)
	burst()
	if len(*warnings) != 1 {
		t.Fatalf("warned %d times within the hour", len(*warnings))
	}
	clock.at = clock.at.Add(31 * time.Minute)
	burst()
	if len(*warnings) != 2 {
		t.Fatalf("warned %d times after the hour, want 2", len(*warnings))
	}
}

func TestEngineRecordsAColdStartOnlyWhenItReplaysHistory(t *testing.T) {
	var mu sync.Mutex
	var recorded []int64
	fuse := NewColdStartFuse(func(ColdStartWarning) {})
	fuse.record = func(tokens int64) { mu.Lock(); recorded = append(recorded, tokens); mu.Unlock() }

	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) { completeTextTurn(rpc, threadID, turnID, "ok") }
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), Models: []string{"gpt-test"}, ColdStarts: fuse,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)

	fresh := testTranslation("hi")
	fresh.EstimatedInputTokens = 10
	if _, err := engine.Execute(context.Background(), fresh, nil); err != nil {
		t.Fatal(err)
	}
	replay := testTranslation("again")
	replay.EstimatedInputTokens = 90_000
	replay.History = []map[string]any{{"type": "message", "role": "user",
		"content": []map[string]any{{"type": "input_text", "text": "old"}}}}
	if _, err := engine.Execute(context.Background(), replay, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 || recorded[0] != 90_000 {
		t.Fatalf("recorded = %v, want one cold start of 90000", recorded)
	}
}
