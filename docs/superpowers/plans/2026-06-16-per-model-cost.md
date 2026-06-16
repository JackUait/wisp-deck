# Per-Model Tokens + Cost Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:test-driven-development for each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Show per-(month, model) token breakdown and an estimated USD cost (accounting for input/output/cache-write/cache-read) on the Stats screen, with a grand total.

**Architecture:** `internal/usage` gains a `ModelUsage` type and per-model aggregation (cache schema v2); a new `pricing.go` computes cost from a per-MTok table with cache multipliers; `internal/tui/stats.go` renders per-model rows + `$` figures.

**Tech Stack:** Go 1.25, Bubbletea/Lipgloss (existing).

**Reference spec:** `docs/superpowers/specs/2026-06-16-per-model-cost-design.md`

---

## Task 1: Per-model aggregation in `internal/usage`

**Files:** Modify `internal/usage/usage.go`, `internal/usage/aggregate.go`, `internal/usage/cache.go`; tests in `internal/usage/usage_test.go`, `aggregate_test.go`.

- [ ] **Step 1: Failing tests**

```go
// usage_test.go
func TestParseFile_groupsByModel(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","model":"claude-sonnet-4-6","usage":{"input_tokens":5,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-03T10:00:00Z","message":{"id":"c","model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, err := ParseFile(p)
	if err != nil { t.Fatal(err) }
	may := months["2026-05"]
	if may == nil { t.Fatal("no May") }
	if len(may.Models) != 2 { t.Fatalf("models = %d, want 2", len(may.Models)) }
	// sorted desc by total: opus (10+1+1+1=13) before sonnet (7)
	if may.Models[0].Model != "claude-opus-4-7" || may.Models[0].Input != 11 {
		t.Errorf("models[0] = %+v, want opus input 11", may.Models[0])
	}
	if may.Models[1].Model != "claude-sonnet-4-6" {
		t.Errorf("models[1] = %+v, want sonnet", may.Models[1])
	}
	if may.Input != 16 { t.Errorf("month input = %d, want 16 (sum)", may.Input) }
}

func TestParseFile_missingModelIsUnknown(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, _ := ParseFile(p)
	if months["2026-05"].Models[0].Model != "unknown" {
		t.Errorf("missing model = %q, want unknown", months["2026-05"].Models[0].Model)
	}
}
```

```go
// aggregate_test.go
func TestAggregate_mergesModelsAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	writeFixture(t, dir, "a.jsonl", `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, dir, "b.jsonl", `{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","model":"claude-opus-4-7","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	out, err := Aggregate(dir, cachePath)
	if err != nil { t.Fatal(err) }
	if len(out) != 1 || len(out[0].Models) != 1 || out[0].Models[0].Input != 15 {
		t.Errorf("merged models = %+v, want one opus row input 15", out)
	}
}
```

- [ ] **Step 2: Run → fail** `go test ./internal/usage/ -run "groupsByModel|missingModelIsUnknown|mergesModelsAcrossFiles" -v` → FAIL (`Models` undefined etc.)

- [ ] **Step 3: Implement**

In `usage.go`: add `ModelUsage` + `Total()`; add `Models []ModelUsage` to `MonthlyUsage` (with `json:"models"`); capture `Message.Model string json:"model"` in `transcriptRecord`. In `ParseFile`, key the per-month accumulation by model: keep `map[month]map[model]*ModelUsage`; an empty model id becomes `"unknown"`. After scanning, for each month build `MonthlyUsage` with summed flat fields and a `Models` slice sorted by `Total()` desc (tie-break by model id for determinism).

```go
type ModelUsage struct {
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheWrite int64  `json:"cache_write"`
	CacheRead  int64  `json:"cache_read"`
}
func (m ModelUsage) Total() int64 { return m.Input + m.Output + m.CacheWrite + m.CacheRead }
```

Add `Models []ModelUsage `json:"models"`` to `MonthlyUsage`. Add `Model string `json:"model"`` inside `transcriptRecord.Message`.

Rewrite the accumulation in `ParseFile` to track per-model, then assemble. Add a helper `buildMonthly(month string, models map[string]*ModelUsage) *MonthlyUsage` that sums and sorts.

In `aggregate.go` `Aggregate`: when merging `entry.Months`, also merge `Models` — keep `map[month]map[model]*ModelUsage` alongside the flat merge, then assemble each `MonthlyUsage` with sorted `Models`. Reuse the same `buildMonthly` helper.

In `cache.go`: bump `cacheVersion` to `2`. No other change — `MonthlyUsage` now carries `Models`, which serialises automatically.

- [ ] **Step 4: Run → pass** `go test ./internal/usage/ -count=1` (all existing + new pass; existing month-level tests still pass because flat fields unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/usage/usage.go internal/usage/aggregate.go internal/usage/cache.go internal/usage/usage_test.go internal/usage/aggregate_test.go
git commit -m "feat(usage): aggregate token usage per model within each month"
```

---

## Task 2: Cost estimate in `internal/usage/pricing.go`

**Files:** Create `internal/usage/pricing.go`, `internal/usage/pricing_test.go`.

- [ ] **Step 1: Failing tests**

```go
package usage

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestModelCostUSD_tiers(t *testing.T) {
	// 1M input + 1M output on opus = $5 + $25 = $30
	usd, priced := ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", Input: 1_000_000, Output: 1_000_000})
	if !priced || !approx(usd, 30) { t.Errorf("opus = %v priced=%v, want 30", usd, priced) }
	// sonnet 1M in + 1M out = $3 + $15 = $18
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-sonnet-4-6", Input: 1_000_000, Output: 1_000_000})
	if !approx(usd, 18) { t.Errorf("sonnet = %v, want 18", usd) }
	// haiku 1M in = $1
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-haiku-4-5", Input: 1_000_000})
	if !approx(usd, 1) { t.Errorf("haiku = %v, want 1", usd) }
}

func TestModelCostUSD_cacheMultipliers(t *testing.T) {
	// opus input rate $5/MTok. cache-write 1.25x = $6.25/MTok; cache-read 0.1x = $0.50/MTok
	usd, _ := ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", CacheWrite: 1_000_000})
	if !approx(usd, 6.25) { t.Errorf("cacheWrite = %v, want 6.25", usd) }
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", CacheRead: 1_000_000})
	if !approx(usd, 0.5) { t.Errorf("cacheRead = %v, want 0.5", usd) }
}

func TestModelCostUSD_prefixMatchAndUnknown(t *testing.T) {
	usd, priced := ModelCostUSD(ModelUsage{Model: "claude-haiku-4-5-20251001", Input: 1_000_000})
	if !priced || !approx(usd, 1) { t.Errorf("suffixed haiku = %v priced=%v, want 1/true", usd, priced) }
	_, priced = ModelCostUSD(ModelUsage{Model: "gpt-4o", Input: 1_000_000})
	if priced { t.Errorf("unknown model should be unpriced") }
}

func TestMonthlyCostUSD_sumAndFlag(t *testing.T) {
	mu := MonthlyUsage{Models: []ModelUsage{
		{Model: "claude-opus-4-7", Input: 1_000_000},   // $5
		{Model: "mystery", Input: 1_000_000},           // unpriced
	}}
	usd, allPriced := mu.CostUSD()
	if !approx(usd, 5) { t.Errorf("month cost = %v, want 5 (priced only)", usd) }
	if allPriced { t.Errorf("allPriced should be false when a model is unpriced") }
}
```

- [ ] **Step 2: Run → fail** `go test ./internal/usage/ -run Cost -v` → FAIL (undefined).

- [ ] **Step 3: Implement**

```go
package usage

import "strings"

type modelRate struct{ inPerMTok, outPerMTok float64 }

// modelRates is matched by id prefix (so date-suffixed ids resolve). Order longest
// prefixes first if any prefix is a prefix of another (none currently overlap).
var modelRates = map[string]modelRate{
	"claude-opus-4-5":   {5, 25},
	"claude-opus-4-6":   {5, 25},
	"claude-opus-4-7":   {5, 25},
	"claude-opus-4-8":   {5, 25},
	"claude-sonnet-4-5": {3, 15},
	"claude-sonnet-4-6": {3, 15},
	"claude-haiku-4-5":  {1, 5},
	"claude-fable-5":    {10, 50},
	"claude-mythos-5":   {10, 50},
}

const (
	cacheWriteMult = 1.25 // 5-minute-TTL cache write, relative to input rate
	cacheReadMult  = 0.10 // cache read, relative to input rate
)

func rateFor(model string) (modelRate, bool) {
	for prefix, r := range modelRates {
		if strings.HasPrefix(model, prefix) {
			return r, true
		}
	}
	return modelRate{}, false
}

// ModelCostUSD returns the estimated USD cost for a model's usage and whether the
// model had a pricing entry. Cache-write is priced at 1.25x input (5-min TTL) and
// cache-read at 0.1x input.
func ModelCostUSD(m ModelUsage) (float64, bool) {
	r, ok := rateFor(m.Model)
	if !ok {
		return 0, false
	}
	inRate := r.inPerMTok / 1_000_000
	outRate := r.outPerMTok / 1_000_000
	usd := float64(m.Input)*inRate +
		float64(m.Output)*outRate +
		float64(m.CacheWrite)*cacheWriteMult*inRate +
		float64(m.CacheRead)*cacheReadMult*inRate
	return usd, true
}

// CostUSD sums the cost of every priced model in the month. allPriced is false if
// any model lacked a pricing entry.
func (mu MonthlyUsage) CostUSD() (float64, bool) {
	var total float64
	allPriced := true
	for _, m := range mu.Models {
		usd, priced := ModelCostUSD(m)
		if !priced {
			allPriced = false
			continue
		}
		total += usd
	}
	return total, allPriced
}
```

- [ ] **Step 4: Run → pass** `go test ./internal/usage/ -count=1`.

- [ ] **Step 5: Commit**

```bash
git add internal/usage/pricing.go internal/usage/pricing_test.go
git commit -m "feat(usage): estimate USD cost per model with cache multipliers"
```

---

## Task 3: Render per-model rows + cost in `internal/tui/stats.go`

**Files:** Modify `internal/tui/stats.go`, tests in `internal/tui/stats_test.go`.

- [ ] **Step 1: Failing tests**

```go
func TestDollarFmt(t *testing.T) {
	cases := []struct{ in float64; want string }{
		{0.42, "$0.42"}, {312, "$312"}, {1234, "$1,234"},
		{12345, "$12.3K"}, {4_600_000, "$4.6M"},
	}
	for _, c := range cases {
		if got := dollarFmt(c.in); got != c.want {
			t.Errorf("dollarFmt(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatsView_showsModelRowsAndCost(t *testing.T) {
	months := []usage.MonthlyUsage{{
		Month: "2026-06", Input: 2_000_000, Output: 1_000_000,
		Models: []usage.ModelUsage{
			{Model: "claude-opus-4-7", Input: 2_000_000, Output: 1_000_000}, // $10+$25 = $35
		},
	}}
	view := NewStatsModelWithData(months).View()
	if !strings.Contains(view, "opus-4-7") {
		t.Errorf("missing model row:\n%s", view)
	}
	if !strings.Contains(view, "$35") {
		t.Errorf("missing model/grand cost $35:\n%s", view)
	}
}
```

- [ ] **Step 2: Run → fail** `go test ./internal/tui/ -run "DollarFmt|showsModelRowsAndCost" -v` → FAIL.

- [ ] **Step 3: Implement**

Add `dollarFmt(usd float64) string`:

```go
func dollarFmt(usd float64) string {
	switch {
	case usd < 1:
		return fmt.Sprintf("$%.2f", usd)
	case usd < 10000:
		// comma-grouped whole dollars
		n := int64(usd + 0.5)
		s := strconv.FormatInt(n, 10)
		var out []byte
		for i, c := range []byte(s) {
			if i > 0 && (len(s)-i)%3 == 0 {
				out = append(out, ',')
			}
			out = append(out, c)
		}
		return "$" + string(out)
	case usd < 1_000_000:
		return fmt.Sprintf("$%.1fK", usd/1000)
	default:
		return fmt.Sprintf("$%.1fM", usd/1_000_000)
	}
}
```

In `View`, for each visible month after the data row + bar row, append one row per
model: `  <model-without-claude-prefix>  <humanizeTokens(total)>  <cost>`. Cost per
model via `usage.ModelCostUSD`; unpriced → `—`. Bar line: change the trailing
label from `fmt.Sprintf("%d%% of all", pct)` to include the month cost, e.g.
`dollarFmt(monthCost) + " · " + fmt.Sprintf("%d%% of all", pct)`; if the month is
not all-priced, prefix the cost with `~`. Footer total row: append the grand-total
cost (sum of every month's `CostUSD`), prefixed `~` if any month is not all-priced.

Strip the `claude-` prefix for the model label: `strings.TrimPrefix(m.Model, "claude-")`.

Keep all rows inside `statsBoxLine`. The model rows are plain `muted`-styled; the
cost on the bar line and footer uses the accent/primary style for emphasis.

- [ ] **Step 4: Run → pass** `go test ./internal/tui/ -count=1` (all tui tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/stats.go internal/tui/stats_test.go
git commit -m "feat(tui): show per-model tokens and USD cost on stats screen"
```

---

## Task 4: Integration, real-data check, push, local rebuild

- [ ] `go build ./...`; `./run-tests.sh` (full suite green).
- [ ] Ephemeral real-data preview: aggregate `~/.claude/projects`, print the rendered View, eyeball per-model rows + costs; delete the throwaway test.
- [ ] `git pull --rebase && git push`; `git status` clean.
- [ ] Rebuild local binary: `go build -ldflags "-X main.Version=$(cat VERSION)" -o "$HOME/.local/bin/ghost-tab-tui" ./cmd/ghost-tab-tui`.

---

## Self-Review Notes

- Spec coverage: per-model aggregation (T1) · cache v2 (T1) · pricing + cache mult + unknown flag (T2) · per-model rows + month/grand cost + dollarFmt (T3) · real-data + push + rebuild (T4).
- Type consistency: `ModelUsage{Model,Input,Output,CacheWrite,CacheRead}` + `Total()`; `MonthlyUsage.Models`; `ModelCostUSD`, `MonthlyUsage.CostUSD`, `dollarFmt` used identically across tasks.
