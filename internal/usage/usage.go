package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"time"
)

// ModelUsage holds token counts for a single model within a month. CacheWrite is
// the total cache-creation tokens; CacheWrite1h is the 1-hour-TTL subset of it
// (priced at 2x input vs the 5-minute 1.25x), so it is NOT added again by Total.
type ModelUsage struct {
	Model        string `json:"model"`
	Input        int64  `json:"input"`
	Output       int64  `json:"output"`
	CacheWrite   int64  `json:"cache_write"`
	CacheWrite1h int64  `json:"cache_write_1h"`
	CacheRead    int64  `json:"cache_read"`
}

// Total returns the sum of all token columns.
func (m ModelUsage) Total() int64 {
	return m.Input + m.Output + m.CacheWrite + m.CacheRead
}

// MonthlyUsage holds token counts for a single YYYY-MM bucket. The flat fields are
// the sum across Models; Models is the per-model breakdown sorted by Total() desc.
type MonthlyUsage struct {
	Month      string       `json:"month"`
	Input      int64        `json:"input"`
	Output     int64        `json:"output"`
	CacheWrite int64        `json:"cache_write"`
	CacheRead  int64        `json:"cache_read"`
	Models     []ModelUsage `json:"models"`
}

// Total returns the sum of all token columns.
func (m MonthlyUsage) Total() int64 {
	return m.Input + m.Output + m.CacheWrite + m.CacheRead
}

// buildMonthly assembles a MonthlyUsage from per-model accumulators: it sums the
// flat fields and returns Models sorted by Total() desc (tie-break by model id).
// Models with zero total tokens (e.g. "<synthetic>" placeholder records) are
// dropped so they neither render nor flag the month as partially unpriced. Returns
// nil when no model has any tokens.
func buildMonthly(month string, models map[string]*ModelUsage) *MonthlyUsage {
	mu := &MonthlyUsage{Month: month}
	for _, m := range models {
		if m.Total() == 0 {
			continue
		}
		mu.Input += m.Input
		mu.Output += m.Output
		mu.CacheWrite += m.CacheWrite
		mu.CacheRead += m.CacheRead
		mu.Models = append(mu.Models, *m)
		// (CacheWrite1h is a subset of CacheWrite; tracked per-model for pricing,
		// not summed into the month flat fields.)
	}
	if len(mu.Models) == 0 {
		return nil
	}
	sort.Slice(mu.Models, func(i, j int) bool {
		if ti, tj := mu.Models[i].Total(), mu.Models[j].Total(); ti != tj {
			return ti > tj
		}
		return mu.Models[i].Model < mu.Models[j].Model
	})
	return mu
}

// FileMeta captures the on-disk identity used for incremental caching.
type FileMeta struct {
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
}

// maxLineBytes bounds a single transcript line (some carry large embedded content).
const maxLineBytes = 50 * 1024 * 1024

// usageCounts holds the raw token fields shared by a top-level usage object and
// each entry in its iterations array.
type usageCounts struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	// CacheCreation is the per-TTL breakdown of cache writes. When present it is
	// authoritative over the flat CacheWrite total above.
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type transcriptRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			usageCounts
			// Iterations holds each separately billed API round within a single
			// assistant turn (e.g. a model fallback: a refused fable-5 round followed
			// by an opus-4-8 retry). Each iteration may run on a DIFFERENT model and
			// is billed at that model's rate. When present, the top-level fields above
			// mirror only the LAST iteration, so iterations are authoritative and the
			// top-level totals must be ignored to avoid dropping earlier rounds.
			Iterations []struct {
				usageCounts
				Model string `json:"model"`
			} `json:"iterations"`
		} `json:"usage"`
	} `json:"message"`
}

// addCounts folds one billed call's token counts into the per-model accumulator
// for a month, creating the accumulator on first use. The per-TTL breakdown is
// preferred (authoritative); the flat CacheWrite is the fallback for older
// transcripts that lack it, charged entirely at the 5m rate.
func addCounts(byModel map[string]*ModelUsage, model string, c usageCounts) {
	mu := byModel[model]
	if mu == nil {
		mu = &ModelUsage{Model: model}
		byModel[model] = mu
	}
	mu.Input += c.Input
	mu.Output += c.Output
	mu.CacheRead += c.CacheRead
	if cc := c.CacheCreation; cc != nil {
		mu.CacheWrite += cc.Ephemeral5m + cc.Ephemeral1h
		mu.CacheWrite1h += cc.Ephemeral1h
	} else {
		mu.CacheWrite += c.CacheWrite
	}
}

// ParseFile reads a single .jsonl transcript and aggregates token usage by month
// and by model. Non-assistant records, records without usage, and malformed lines
// are skipped. Assistant records are deduped by message.id within this file, and
// the LAST line for an id wins: Claude Code writes a streaming snapshot first
// (output_tokens of a few tokens, no iterations), and only the final line holds
// the real output count and the advisor rounds in iterations. A record with no
// model id is attributed to "unknown".
func ParseFile(path string) (map[string]*MonthlyUsage, FileMeta, error) {
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

	// Kept in first-seen order; a later line for the same id replaces the record.
	var records []transcriptRecord
	index := map[string]int{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		var rec transcriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" || rec.Message.Usage == nil {
			continue
		}
		if len(rec.Timestamp) < 7 {
			continue
		}
		if id := rec.Message.ID; id != "" {
			if i, ok := index[id]; ok {
				records[i] = rec
				continue
			}
			index[id] = len(records)
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, meta, err
	}

	// month -> model -> accumulator
	acc := map[string]map[string]*ModelUsage{}
	for _, rec := range records {
		month := rec.Timestamp[:7]
		byModel := acc[month]
		if byModel == nil {
			byModel = map[string]*ModelUsage{}
			acc[month] = byModel
		}
		u := rec.Message.Usage
		// When the turn has per-iteration usage, each iteration is a separately
		// billed call (possibly on a different model), so attribute each to its own
		// model and ignore the top-level totals (which mirror only the last one).
		// Otherwise fall back to the single top-level usage.
		if len(u.Iterations) > 0 {
			for i := range u.Iterations {
				it := &u.Iterations[i]
				model := it.Model
				if model == "" {
					model = rec.Message.Model
				}
				if model == "" {
					model = "unknown"
				}
				addCounts(byModel, model, it.usageCounts)
			}
		} else {
			model := rec.Message.Model
			if model == "" {
				model = "unknown"
			}
			addCounts(byModel, model, u.usageCounts)
		}
	}

	months := make(map[string]*MonthlyUsage, len(acc))
	for month, byModel := range acc {
		if mu := buildMonthly(month, byModel); mu != nil {
			months[month] = mu
		}
	}
	return months, meta, nil
}
