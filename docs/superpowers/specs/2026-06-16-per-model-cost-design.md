# Per-Model Token Breakdown + Cost Estimate — Design

**Date:** 2026-06-16
**Status:** Approved (pending spec review)

## Summary

Extend the Stats screen to show, per month, which model spent how many tokens, and
an estimated API cost in USD that accounts for the different token types (input,
output, cache-write, cache-read). Adds a grand-total cost.

## Goals

- Per (month, model) token breakdown shown under each month.
- USD cost estimate per model, per month total, and grand total.
- Cost accounts for token type: input, output, cache-write, cache-read priced
  differently.
- Pure, testable pricing logic. Unknown models are flagged, not silently priced.

## Non-Goals

- Live pricing fetch from the API (table is maintained in code).
- Exact 5-minute vs 1-hour cache-write split (data only has the combined total).
- Cost for non-Claude tools (no usage data exists).

## Data Source (unchanged)

`~/.claude/projects/**/*.jsonl`. Each usage-bearing assistant record carries
`message.model` (e.g. `claude-opus-4-7`) alongside `message.usage`. The current
parser ignores `message.model`; this change captures it.

## Architecture

### 1. `internal/usage` — per-model aggregation

New type and an extended `MonthlyUsage`:

```go
type ModelUsage struct {
    Model      string
    Input      int64
    Output     int64
    CacheWrite int64
    CacheRead  int64
}
func (m ModelUsage) Total() int64 // Input+Output+CacheWrite+CacheRead

type MonthlyUsage struct {
    Month      string
    Input      int64        // = sum over Models
    Output     int64
    CacheWrite int64
    CacheRead  int64
    Models     []ModelUsage // sorted descending by Total()
}
```

`ParseFile` groups by `(month, model)`. Within a file it still dedups by
`message.id`. Each month's `Models` slice is built from the per-model sub-totals
and sorted by `Total()` descending; the month's flat fields remain the sum across
its models (so existing month-level rendering is unchanged).

`Aggregate` merges per-model sub-totals across files the same way it merges month
totals: for each month, merge model rows by model id.

### 2. Cache schema v2

`cacheVersion` bumps `1 → 2`. `fileCacheEntry.Months` now serialises the
`Models` slice too. `LoadCache` already returns an empty cache when
`c.Version != cacheVersion`, so existing v1 caches rebuild automatically on first
run after upgrade — no migration code needed.

### 3. `internal/usage/pricing.go` — cost estimate (pure)

Pricing table, per 1,000,000 tokens, matched by model-id prefix (so
`claude-haiku-4-5-20251001` matches `claude-haiku-4-5`):

| model id prefix     | input $/MTok | output $/MTok |
|---------------------|-------------:|--------------:|
| `claude-opus-4-5`   | 5            | 25            |
| `claude-opus-4-6`   | 5            | 25            |
| `claude-opus-4-7`   | 5            | 25            |
| `claude-opus-4-8`   | 5            | 25            |
| `claude-sonnet-4-5` | 3            | 15            |
| `claude-sonnet-4-6` | 3            | 15            |
| `claude-haiku-4-5`  | 1            | 5             |
| `claude-fable-5`    | 10           | 50            |
| `claude-mythos-5`   | 10           | 50            |

Cache multipliers (relative to the model's input rate):
- cache-write: **1.25×** input (5-minute-TTL rate; the combined
  `cache_creation_input_tokens` is priced at this rate — documented approximation).
- cache-read: **0.1×** input.

Cost formula (rates are per-token = `perMTok / 1_000_000`):

```
usd = Input*inRate + Output*outRate
    + CacheWrite*(1.25*inRate) + CacheRead*(0.1*inRate)
```

API:

```go
// ModelCostUSD returns the estimated cost and whether the model was priced.
func ModelCostUSD(m ModelUsage) (usd float64, priced bool)

// CostUSD sums the cost of every priced model in the month; allPriced is false
// if any model in the month had no pricing entry.
func (mu MonthlyUsage) CostUSD() (usd float64, allPriced bool)
```

Unknown model → `priced=false`, `usd=0`. The view renders its cost as `—` and the
month/total marks itself with a `~` (or note) when any contributing model is
unpriced, so a missing price is visible rather than silently dropped.

### 4. `internal/tui/stats.go` — rendering

Per month block becomes:

```
2026-06    26.8M    22.7M   204.1M     4.6B      4.9B
████████████████████████  $312 · 70% of all
  opus-4-7    4.8B   $300
  sonnet-4-6  0.1B   $12
```

- The bar line trades the bare `70% of all` for `<cost> · <pct>% of all`.
- Under each month, one indented row per model: `  <model>  <total tokens>  <cost>`.
  `<model>` is the id with the `claude-` prefix stripped for width.
- Footer total row gains a trailing total-cost figure.
- Cost formatting (`dollarFmt`): `< $1` → `$0.00`; `< $10000` → `$1,234`
  (comma-grouped whole dollars); otherwise `$12.3K` / `$4.6M`.
- Unpriced model row shows `—` for cost; a month or grand total that includes an
  unpriced model is prefixed `~` to signal "estimate excludes some models".

Box, scrolling, and centering are unchanged. Note: each month now renders more
rows (1 totals + 1 bar + N model rows), so the fixed `statsWindow` budget is
interpreted as "month blocks", not raw lines — `statsWindow` continues to mean
months, and a tall month simply renders all its model rows (acceptable; months are
few).

## Data Flow (unchanged shape)

`T` → push StatsModel → `usage.Aggregate` (now per-model, cache v2) → render table
with per-model rows + cost.

## Error Handling

- Missing model on a record: treated as model id `"unknown"`; it aggregates and
  renders as an unpriced row rather than being dropped.
- Unknown/unpriced model: cost `—`, excluded from totals, total flagged `~`.
- All prior error handling (missing dir, corrupt cache, malformed line) unchanged.

## Testing (TDD — test first, fail, then code)

`internal/usage`:
- `ParseFile`: two models in one month produce two `ModelUsage` rows with correct
  per-type sums; month flat totals equal the sum; models sorted desc; dedup still
  per `message.id`; record missing `model` → `"unknown"` row.
- `Aggregate`: same model across two files merges into one row; models sorted desc;
  month totals correct.
- Cache v2: a v1 cache on disk (or `version:1`) is ignored and rebuilt; round-trip
  of a v2 cache preserves `Models`.

`internal/usage/pricing.go`:
- Table-driven `ModelCostUSD`: each tier (opus/sonnet/haiku/fable) with a known
  token mix yields the hand-computed dollar figure; cache-write at 1.25×,
  cache-read at 0.1×; date-suffixed id matches by prefix; unknown id →
  `priced=false`.
- `MonthlyUsage.CostUSD`: sums priced models; `allPriced=false` when one model is
  unpriced.

`internal/tui/stats.go`:
- View with two models in a month renders both model rows with humanized tokens and
  `$` costs; bar line shows the month `$`; footer shows grand-total `$`.
- `dollarFmt` table-driven (`0.42→$0.42`, `312→$312`, `1234→$1,234`,
  `12345→$12.3K`, `4_600_000→$4.6M`).
- A month containing an unpriced model renders `—` for that row and `~` on the
  month/total.

## Completion Gates (project CLAUDE.md)

Test-first → fail → code → pass; full `./run-tests.sh`; `git push`; rebuild local
binary so the change is visible.
