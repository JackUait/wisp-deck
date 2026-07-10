package usage

import (
	"bufio"
	"encoding/json"
	"os"
)

// codexRecord is the subset of a Codex rollout line we read. Codex writes one JSON
// object per line under <CODEX_HOME>/sessions/YYYY/MM/DD/rollout-*.jsonl. Two record
// kinds matter: turn_context (top-level type) carries the model for the turn that
// follows, and event_msg records whose nested payload.type is "token_count" carry
// per-request token usage. The current model is tracked across lines because
// token_count events do not name a model themselves.
type codexRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type  string `json:"type"`  // event_msg sub-type, e.g. "token_count"
		Model string `json:"model"` // present on turn_context
		Info  *struct {
			// last_token_usage is the delta for the most recent request; summing it
			// across events yields the session's real billed consumption.
			// total_token_usage is cumulative (and resets on compaction) so it is
			// deliberately ignored.
			Last *codexTokens `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// codexTokens holds the token fields Codex reports per request. input_tokens is the
// full prompt including the cached portion, so fresh input = input - cached and
// cached is charged as a cache read. output_tokens already includes reasoning
// tokens, so reasoning_output_tokens is not read (it would double-count).
type codexTokens struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
}

// ParseCodexRollout reads a single Codex rollout .jsonl and aggregates its token
// usage by month and by model, matching ParseFile's contract so the Aggregate cache
// can treat Codex, Claude, and OpenCode sources uniformly. Non-token records and
// malformed lines are skipped. Each token_count event is attributed to the most
// recent turn_context model seen in the file (or "unknown" if a token_count precedes
// any turn_context). Codex reports no cache-write tokens, so CacheWrite stays 0.
//
// Like ParseFile, dedup is per-file only. Codex can replay early token_count events
// into a resumed session's new rollout file; those cross-file duplicates are
// tolerated the same way Claude's rare cross-file duplicate ids are (see Aggregate).
func ParseCodexRollout(path string) (map[string]*MonthlyUsage, FileMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, FileMeta{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, FileMeta{}, err
	}
	meta := FileMeta{ModTime: info.ModTime(), Size: info.Size()}

	// month -> model -> accumulator
	acc := map[string]map[string]*ModelUsage{}
	currentModel := ""

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		var rec codexRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type == "turn_context" && rec.Payload.Model != "" {
			currentModel = rec.Payload.Model
			continue
		}
		if rec.Payload.Type != "token_count" || rec.Payload.Info == nil || rec.Payload.Info.Last == nil {
			continue
		}
		if len(rec.Timestamp) < 7 {
			continue
		}
		lt := rec.Payload.Info.Last
		model := currentModel
		if model == "" {
			model = "unknown"
		}
		fresh := lt.Input - lt.Cached
		if fresh < 0 {
			fresh = 0
		}
		month := rec.Timestamp[:7]
		byModel := acc[month]
		if byModel == nil {
			byModel = map[string]*ModelUsage{}
			acc[month] = byModel
		}
		addCounts(byModel, model, usageCounts{
			Input:     fresh,
			Output:    lt.Output,
			CacheRead: lt.Cached,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, meta, err
	}

	months := make(map[string]*MonthlyUsage, len(acc))
	for month, byModel := range acc {
		if mu := buildMonthly(month, byModel); mu != nil {
			months[month] = mu
		}
	}
	return months, meta, nil
}
