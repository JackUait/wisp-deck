package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// parserGolden pins what the three parsers return for a fixed corpus, at the
// cacheVersion that produced it. The per-file cache and the journal only
// re-parse a file whose parser version went up, so a parser change shipped
// without a cacheVersion bump keeps serving the old numbers. When this fails,
// bump cacheVersion and re-pin both values.
const (
	parserGoldenVersion = 8
	parserGolden        = "985178e2fe5a57234c38efd467b7ddd6a0cdd6b6a80aaca5af305a1dd9ebbd0e"
)

// parserGoldenCorpus covers every shape a parser branches on: a Claude
// streaming snapshot before its final line, iterations with an advisor round,
// a record with no id, a Codex replay burst and a verbatim re-emission, and an
// OpenCode message.
func parserGoldenCorpus(t *testing.T) map[string]map[string]*MonthlyUsage {
	t.Helper()
	dir := t.TempDir()
	claude := writeFixture(t, dir, "claude.jsonl",
		`{"type":"assistant","timestamp":"2026-10-05T10:00:00Z","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":2,"output_tokens":3,"cache_creation_input_tokens":20,"cache_read_input_tokens":100}}}
{"type":"assistant","timestamp":"2026-10-05T10:00:01Z","message":{"id":"m1","model":"claude-opus-5-5","stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":90,"cache_creation_input_tokens":30,"cache_read_input_tokens":210,"iterations":[{"type":"message","input_tokens":2,"output_tokens":40,"cache_read_input_tokens":100,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":20}},{"type":"advisor_message","model":"claude-opus-5-5","input_tokens":500,"output_tokens":60,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},{"type":"message","input_tokens":2,"output_tokens":50,"cache_read_input_tokens":110,"cache_creation_input_tokens":10}]}}}
{"type":"assistant","timestamp":"2026-09-30T23:59:59Z","message":{"model":"gpt-6-sol","usage":{"input_tokens":7,"output_tokens":8,"cache_read_input_tokens":9}}}
{"type":"user","timestamp":"2026-10-05T10:00:02Z","message":{"id":"u"}}
`)
	tok := func(ts string, in, cached, out int) string {
		return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` +
			itoa(in) + `,"cached_input_tokens":` + itoa(cached) + `,"output_tokens":` + itoa(out) + `}}}}` + "\n"
	}
	rollout := `{"timestamp":"2026-10-01T00:00:00Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}` + "\n"
	for i := 0; i < 6; i++ {
		rollout += tok("2026-10-01T00:00:01Z", 1000+i, 900, 10)
	}
	rollout += tok("2026-10-01T00:00:05Z", 2000, 1800, 20)
	rollout += tok("2026-10-01T00:00:09Z", 2000, 1800, 20)
	rollout += tok("2026-10-01T00:00:15Z", 3000, 2900, 30)
	codex := writeFixture(t, dir, "rollout.jsonl", rollout)
	created := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).UnixMilli()
	opencode := writeFixture(t, dir, "msg.json", ocMsg("assistant", "claude-opus-4-8", created, 10, 20, 3, 1, 5))

	out := map[string]map[string]*MonthlyUsage{}
	for name, parse := range map[string]struct {
		path string
		fn   parseFunc
	}{
		"claude":   {claude, ParseFile},
		"codex":    {codex, ParseCodexRollout},
		"opencode": {opencode, ParseOpenCodeMessage},
	} {
		months, _, err := parse.fn(parse.path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = months
	}
	return out
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestParserOutputChangesBumpCacheVersion(t *testing.T) {
	data, err := json.Marshal(parserGoldenCorpus(t))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if cacheVersion != parserGoldenVersion {
		t.Fatalf("cacheVersion is %d but the golden was pinned at %d: set parserGoldenVersion = %d and parserGolden = %q",
			cacheVersion, parserGoldenVersion, cacheVersion, got)
	}
	if got != parserGolden {
		t.Fatalf("parser output changed (%s) without a cacheVersion bump: old cached and journaled months would keep the old numbers.\n"+
			"Bump cacheVersion, then set parserGoldenVersion to it and parserGolden = %q.\noutput: %s", got, got, data)
	}
}
