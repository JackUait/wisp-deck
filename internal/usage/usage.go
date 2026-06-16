package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"time"
)

// ModelUsage holds token counts for a single model within a month.
type ModelUsage struct {
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheWrite int64  `json:"cache_write"`
	CacheRead  int64  `json:"cache_read"`
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

type transcriptRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseFile reads a single .jsonl transcript and aggregates token usage by month
// and by model. Non-assistant records, records without usage, and malformed lines
// are skipped. Assistant records are deduped by message.id within this file. A
// record with no model id is attributed to "unknown".
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

	// month -> model -> accumulator
	acc := map[string]map[string]*ModelUsage{}
	seen := map[string]bool{}

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
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		month := rec.Timestamp[:7]
		model := rec.Message.Model
		if model == "" {
			model = "unknown"
		}
		byModel := acc[month]
		if byModel == nil {
			byModel = map[string]*ModelUsage{}
			acc[month] = byModel
		}
		mu := byModel[model]
		if mu == nil {
			mu = &ModelUsage{Model: model}
			byModel[model] = mu
		}
		u := rec.Message.Usage
		mu.Input += u.Input
		mu.Output += u.Output
		mu.CacheWrite += u.CacheWrite
		mu.CacheRead += u.CacheRead
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
