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
// tokens, so Reason is never added to a total — it only sharpens the fingerprint
// that identifies a request when collapsing duplicate emissions.
type codexTokens struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
	Reason int64 `json:"reasoning_output_tokens"`
}

// codexReplayBurstPerSecond is the number of token_count events a rollout may write
// inside one wall-clock second before that second is read as a replayed history dump
// rather than live traffic.
//
// Codex replays an ancestor thread's ENTIRE token_count history into a forked,
// resumed, or compacted rollout, re-stamped with the load instant. The replayed
// records are byte-identical in shape to live ones, so density is the only signal
// that separates them: a replay writes hundreds of events inside one second, while a
// real request round-trip takes seconds. Without this filter, one production month
// summed to 466B tokens against 14.9B actually spent — a 31x over-count, because
// 1,684 rollout files were 43 session chains replaying each other (one chain spanned
// 448 files). Measured against that corpus, every threshold from 1 to 12 recovered
// the deduplicated truth to three decimal places while keeping 100% of the distinct
// requests, so the exact value is not delicate; 4 leaves generous headroom for a
// genuinely busy second.
const codexReplayBurstPerSecond = 4

// codexEvent is one buffered token_count record. Replay detection needs to know how
// many events share a second before any of them can be counted, so a rollout's
// events are collected first and folded afterwards.
type codexEvent struct {
	second string // timestamp truncated to the second; the replay-density bucket
	month  string
	model  string // "" until the file's first turn_context backfills it
	tokens codexTokens
}

// ParseCodexRollout reads a single Codex rollout .jsonl and aggregates its token
// usage by month and by model, matching ParseFile's contract so the Aggregate cache
// can treat Codex, Claude, and OpenCode sources uniformly. Non-token records and
// malformed lines are skipped. Each token_count event is attributed to the most
// recent turn_context model seen in the file; events preceding the first
// turn_context are backfilled to that first model ("unknown" only when the whole
// file has no turn_context). Codex reports no cache-write tokens, so CacheWrite
// stays 0.
//
// Replayed history is discarded, which is what makes the number believable. Unlike
// Claude, Codex does not confine a conversation to one file: forking a subagent,
// resuming a thread, and compacting all open a fresh rollout that first replays the
// ancestor's entire token_count history. Summing those replays counted a single
// month 31x over. They cannot be recognised per record — a replayed line is
// byte-identical to a live one — but they arrive in dense bursts, so any second in
// which the file wrote more than codexReplayBurstPerSecond events is treated as a
// dump and skipped, and a verbatim re-emission of the request just counted is
// collapsed into it. Together those two rules reproduced the cross-file
// deduplicated truth to three decimal places on a 1,684-file corpus while keeping
// 100% of its distinct requests, so dedup can stay per-file and the incremental
// Aggregate cache stays intact.
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

	var events []codexEvent
	perSecond := map[string]int{}
	currentModel := ""
	firstModel := ""

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		var rec codexRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type == "turn_context" && rec.Payload.Model != "" {
			currentModel = rec.Payload.Model
			if firstModel == "" {
				firstModel = currentModel
			}
			continue
		}
		if rec.Payload.Type != "token_count" || rec.Payload.Info == nil || rec.Payload.Info.Last == nil {
			continue
		}
		if len(rec.Timestamp) < 7 {
			continue
		}
		// Codex stamps UTC RFC3339, so the first 19 bytes ("2026-07-23T10:09:27")
		// are the whole-second bucket. A short timestamp carrying only a month
		// cannot be bucketed; it becomes its own bucket and is always counted.
		second := rec.Timestamp
		if len(second) > 19 {
			second = second[:19]
		}
		perSecond[second]++
		events = append(events, codexEvent{
			second: second,
			month:  rec.Timestamp[:7],
			model:  currentModel,
			tokens: *rec.Payload.Info.Last,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, meta, err
	}
	if firstModel == "" {
		firstModel = "unknown"
	}

	// month -> model -> accumulator
	acc := map[string]map[string]*ModelUsage{}
	var prev codexTokens
	havePrev := false
	for _, e := range events {
		// Drop replayed history: every event in an implausibly dense second.
		if perSecond[e.second] > codexReplayBurstPerSecond {
			havePrev = false // the run of live events is broken here
			continue
		}
		// Collapse a re-emission of the request just counted. Codex sometimes
		// repeats a token_count verbatim seconds later; a genuine follow-up request
		// always carries a larger input than its predecessor, so byte-identical
		// back-to-back deltas never describe two requests.
		if havePrev && prev == e.tokens {
			continue
		}
		prev, havePrev = e.tokens, true

		fresh := e.tokens.Input - e.tokens.Cached
		if fresh < 0 {
			fresh = 0
		}
		model := e.model
		if model == "" {
			// Replayed events preceding the file's first turn_context belong to that
			// model, not to "unknown".
			model = firstModel
		}
		byModel := acc[e.month]
		if byModel == nil {
			byModel = map[string]*ModelUsage{}
			acc[e.month] = byModel
		}
		addCounts(byModel, model, usageCounts{
			Input:     fresh,
			Output:    e.tokens.Output,
			CacheRead: e.tokens.Cached,
		})
	}

	months := make(map[string]*MonthlyUsage, len(acc))
	for month, byModel := range acc {
		if mu := buildMonthly(month, byModel); mu != nil {
			months[month] = mu
		}
	}
	return months, meta, nil
}
