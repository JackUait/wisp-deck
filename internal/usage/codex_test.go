package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeCodexRollout writes lines to a temp .jsonl and returns its path.
func writeCodexRollout(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// modelIn returns the accumulator for a model within a month's parsed result.
func modelIn(mu *MonthlyUsage, model string) *ModelUsage {
	if mu == nil {
		return nil
	}
	for i := range mu.Models {
		if mu.Models[i].Model == model {
			return &mu.Models[i]
		}
	}
	return nil
}

func TestParseCodexRollout_attributesTokensToCurrentModel(t *testing.T) {
	turn := `{"timestamp":"2026-07-10T00:35:10.535Z","type":"turn_context","payload":{"turn_id":"t1","model":"gpt-5.6-sol"}}`
	tok := `{"timestamp":"2026-07-10T00:35:23.275Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":18177,"cached_input_tokens":11008,"output_tokens":461,"reasoning_output_tokens":136,"total_tokens":18638},"last_token_usage":{"input_tokens":18177,"cached_input_tokens":11008,"output_tokens":461,"reasoning_output_tokens":136,"total_tokens":18638}}}}`
	path := writeCodexRollout(t, turn, tok)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	mu := months["2026-07"]
	m := modelIn(mu, "gpt-5.6-sol")
	if m == nil {
		t.Fatalf("no usage for gpt-5.6-sol; got %+v", months)
	}
	// Fresh input = input_tokens - cached_input_tokens = 18177 - 11008 = 7169.
	if m.Input != 7169 {
		t.Errorf("Input = %d, want 7169", m.Input)
	}
	// Cache read = cached_input_tokens = 11008.
	if m.CacheRead != 11008 {
		t.Errorf("CacheRead = %d, want 11008", m.CacheRead)
	}
	// Output = output_tokens (already includes reasoning).
	if m.Output != 461 {
		t.Errorf("Output = %d, want 461", m.Output)
	}
	// Codex reports no cache-write.
	if m.CacheWrite != 0 {
		t.Errorf("CacheWrite = %d, want 0", m.CacheWrite)
	}
}

func TestParseCodexRollout_sumsLastTokenUsageDeltas(t *testing.T) {
	turn := `{"timestamp":"2026-07-10T00:35:10Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	// Two separate requests; each last_token_usage is that request's delta and both
	// must be summed (total_token_usage is cumulative and must be ignored).
	tok1 := `{"timestamp":"2026-07-10T00:35:23Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10,"total_tokens":110},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10,"total_tokens":110}}}}`
	tok2 := `{"timestamp":"2026-07-10T00:36:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":300,"cached_input_tokens":0,"output_tokens":25,"total_tokens":325},"last_token_usage":{"input_tokens":200,"cached_input_tokens":0,"output_tokens":15,"total_tokens":215}}}}`
	path := writeCodexRollout(t, turn, tok1, tok2)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	m := modelIn(months["2026-07"], "gpt-5.6-sol")
	if m == nil {
		t.Fatalf("no usage; got %+v", months)
	}
	if m.Input != 300 { // 100 + 200
		t.Errorf("Input = %d, want 300", m.Input)
	}
	if m.Output != 25 { // 10 + 15
		t.Errorf("Output = %d, want 25", m.Output)
	}
}

func TestParseCodexRollout_modelSwitchMidSession(t *testing.T) {
	turnA := `{"timestamp":"2026-07-10T00:35:10Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	tokA := `{"timestamp":"2026-07-10T00:35:23Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":5,"total_tokens":55}}}}`
	turnB := `{"timestamp":"2026-07-10T00:40:00Z","type":"turn_context","payload":{"model":"gpt-5-mini"}}`
	tokB := `{"timestamp":"2026-07-10T00:40:10Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":80,"cached_input_tokens":0,"output_tokens":8,"total_tokens":88}}}}`
	path := writeCodexRollout(t, turnA, tokA, turnB, tokB)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	if m := modelIn(months["2026-07"], "gpt-5.6-sol"); m == nil || m.Input != 50 {
		t.Errorf("gpt-5.6-sol Input: got %+v, want 50", m)
	}
	if m := modelIn(months["2026-07"], "gpt-5-mini"); m == nil || m.Input != 80 {
		t.Errorf("gpt-5-mini Input: got %+v, want 80", m)
	}
}

func TestParseCodexRollout_tokenCountBeforeModelIsUnknown(t *testing.T) {
	// A file with no turn_context at all has nothing to backfill from.
	tok := `{"timestamp":"2026-07-10T00:35:23Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":2,"total_tokens":12}}}}`
	path := writeCodexRollout(t, tok)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	if m := modelIn(months["2026-07"], "unknown"); m == nil || m.Input != 10 {
		t.Errorf("unknown model Input: got %+v, want 10", m)
	}
}

func TestParseCodexRollout_backfillsReplayedTokenCountsToFirstModel(t *testing.T) {
	// Forked/resumed sessions replay the parent's token_count events at the top of
	// the new rollout, before any turn_context. Those must be attributed to the
	// file's first turn_context model, not "unknown".
	tokReplayed := `{"timestamp":"2026-07-10T00:35:23Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":7,"total_tokens":107}}}}`
	turn := `{"timestamp":"2026-07-10T00:36:00Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	tokLive := `{"timestamp":"2026-07-10T00:36:10Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":5,"total_tokens":55}}}}`
	path := writeCodexRollout(t, tokReplayed, turn, tokLive)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	m := modelIn(months["2026-07"], "gpt-5.6-sol")
	if m == nil {
		t.Fatalf("no usage for gpt-5.6-sol; got %+v", months)
	}
	if m.Input != 90 { // (100-60) replayed + 50 live
		t.Errorf("Input = %d, want 90", m.Input)
	}
	if m.CacheRead != 60 {
		t.Errorf("CacheRead = %d, want 60", m.CacheRead)
	}
	if u := modelIn(months["2026-07"], "unknown"); u != nil {
		t.Errorf("unknown bucket should be empty, got %+v", u)
	}
}

func TestParseCodexRollout_dropsReplayedHistoryBurst(t *testing.T) {
	// A forked/resumed rollout replays its ancestor's ENTIRE token_count history at
	// load time, re-stamped with the load instant. Observed in production: 18,899 of
	// one 19,019-event rollout's events landed in a 2-second burst, and summing them
	// over-counted July's Codex usage by 31x. A replay burst writes hundreds of
	// events inside one second; a real request round-trip takes seconds. Only the
	// live events that follow may be counted.
	turn := `{"timestamp":"2026-07-10T00:35:00Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	lines := []string{turn}
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"timestamp":"2026-07-10T00:35:10.%03dZ","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":100,"total_tokens":%d}}}}`,
			i, 1000+i, 1100+i))
	}
	// Two genuine requests, seconds apart, each alone in its second.
	lines = append(lines,
		`{"timestamp":"2026-07-10T00:36:20Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":7,"cached_input_tokens":2,"output_tokens":3,"total_tokens":10}}}}`,
		`{"timestamp":"2026-07-10T00:36:41Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":9,"cached_input_tokens":4,"output_tokens":6,"total_tokens":15}}}}`)
	path := writeCodexRollout(t, lines...)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	m := modelIn(months["2026-07"], "gpt-5.6-sol")
	if m == nil {
		t.Fatalf("no usage for gpt-5.6-sol; got %+v", months)
	}
	if m.Input != 10 { // (7-2) + (9-4); the 10-event replay burst contributes nothing
		t.Errorf("Input = %d, want 10 (replay burst must not be counted)", m.Input)
	}
	if m.CacheRead != 6 { // 2 + 4
		t.Errorf("CacheRead = %d, want 6", m.CacheRead)
	}
	if m.Output != 9 { // 3 + 6
		t.Errorf("Output = %d, want 9", m.Output)
	}
}

func TestParseCodexRollout_collapsesConsecutiveDuplicateTokenCounts(t *testing.T) {
	// Codex re-emits the same token_count for a single request (observed in a real
	// rollout: an identical last_token_usage 11 seconds after the first emission).
	// Counting it twice inflates the request. Identical back-to-back deltas are one
	// request: a genuine follow-up request always carries a larger input than its
	// predecessor, so byte-identical consecutive deltas never describe two requests.
	turn := `{"timestamp":"2026-07-10T00:35:00Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	tok := `{"timestamp":"2026-07-10T00:35:20Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10,"total_tokens":540}}}}`
	dup := `{"timestamp":"2026-07-10T00:35:31Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10,"total_tokens":540}}}}`
	next := `{"timestamp":"2026-07-10T00:35:55Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":700,"cached_input_tokens":300,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":720}}}}`
	path := writeCodexRollout(t, turn, tok, dup, next)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	m := modelIn(months["2026-07"], "gpt-5.6-sol")
	if m == nil {
		t.Fatalf("no usage; got %+v", months)
	}
	if m.Input != 600 { // (500-300) counted once + (700-300)
		t.Errorf("Input = %d, want 600 (duplicate emission counted once)", m.Input)
	}
	if m.CacheRead != 600 { // 300 + 300
		t.Errorf("CacheRead = %d, want 600", m.CacheRead)
	}
	if m.Output != 60 { // 40 + 20
		t.Errorf("Output = %d, want 60", m.Output)
	}
}

func TestParseCodexRollout_keepsBurstFreeRapidRequests(t *testing.T) {
	// The dense-second rule must not punish a genuinely busy session: a handful of
	// requests inside one second stays under the replay threshold and is counted.
	turn := `{"timestamp":"2026-07-10T00:35:00Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	a := `{"timestamp":"2026-07-10T00:35:20.100Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":1,"total_tokens":11}}}}`
	b := `{"timestamp":"2026-07-10T00:35:20.500Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"cached_input_tokens":0,"output_tokens":2,"total_tokens":22}}}}`
	c := `{"timestamp":"2026-07-10T00:35:20.900Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":30,"cached_input_tokens":0,"output_tokens":3,"total_tokens":33}}}}`
	path := writeCodexRollout(t, turn, a, b, c)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	m := modelIn(months["2026-07"], "gpt-5.6-sol")
	if m == nil || m.Input != 60 {
		t.Fatalf("Input: got %+v, want 60", m)
	}
}

func TestParseCodexRollout_skipsNonTokenAndMalformed(t *testing.T) {
	turn := `{"timestamp":"2026-07-10T00:35:10Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	garbage := `not json at all`
	other := `{"timestamp":"2026-07-10T00:35:11Z","type":"event_msg","payload":{"type":"agent_message","message":"hi"}}`
	tok := `{"timestamp":"2026-07-10T00:35:23Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":40,"cached_input_tokens":0,"output_tokens":4,"total_tokens":44}}}}`
	path := writeCodexRollout(t, turn, garbage, other, tok)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	if m := modelIn(months["2026-07"], "gpt-5.6-sol"); m == nil || m.Input != 40 {
		t.Errorf("Input: got %+v, want 40", m)
	}
}

func TestParseCodexRollout_negativeFreshInputClampedToZero(t *testing.T) {
	// Defensive: if cached exceeds input, fresh input must not go negative.
	turn := `{"timestamp":"2026-07-10T00:35:10Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}`
	tok := `{"timestamp":"2026-07-10T00:35:23Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":150,"output_tokens":5,"total_tokens":105}}}}`
	path := writeCodexRollout(t, turn, tok)

	months, _, err := ParseCodexRollout(path)
	if err != nil {
		t.Fatalf("ParseCodexRollout: %v", err)
	}
	m := modelIn(months["2026-07"], "gpt-5.6-sol")
	if m == nil {
		t.Fatalf("no usage; got %+v", months)
	}
	if m.Input != 0 {
		t.Errorf("Input = %d, want 0 (clamped)", m.Input)
	}
	if m.CacheRead != 150 {
		t.Errorf("CacheRead = %d, want 150", m.CacheRead)
	}
}

func TestParseCodexRollout_missingFileErrors(t *testing.T) {
	if _, _, err := ParseCodexRollout(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Error("expected error for missing file")
	}
}
