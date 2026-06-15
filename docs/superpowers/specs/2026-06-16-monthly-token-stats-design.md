# Monthly Token Stats — Design

**Date:** 2026-06-16
**Status:** Approved (pending spec review)

## Summary

Add a Stats screen to the Ghost Tab TUI showing how many tokens were spent each
month, broken down by type. Reached from the main menu with the `T` key. Data
comes from Claude Code session transcripts. Parsing is cached and incremental so
launch stays fast.

## Goals

- Show per-month token usage: Input, Output, Cache-Write, Cache-Read, and Total.
- All-time history, scrollable.
- Table rows plus a horizontal bar chart scaled to each month's total.
- Fast: never re-parse unchanged transcript files.

## Non-Goals

- Estimated USD cost (no pricing table).
- Per-model split.
- Token data from other AI tools (Codex/Copilot/OpenCode store none).

## Data Source

Claude Code writes session transcripts to `~/.claude/projects/**/*.jsonl`
(one JSON object per line). On this machine: ~5,998 files, ~2.68 GB, ~92,786
records carrying token usage.

A usage-bearing record looks like:

```json
{
  "type": "assistant",
  "timestamp": "2026-06-15T19:43:05.932Z",
  "message": {
    "model": "claude-opus-4-7",
    "id": "msg_01RisWH7z1b1gAkMmKj4Dpq4",
    "usage": {
      "input_tokens": 6,
      "output_tokens": 331,
      "cache_creation_input_tokens": 40401,
      "cache_read_input_tokens": 0
    }
  }
}
```

Relevant fields:

| Field | Use |
|-------|-----|
| `type` | Keep only `"assistant"`. |
| `timestamp` | ISO 8601; first 7 chars `YYYY-MM` = the month bucket. |
| `message.id` | Dedup key (within a file). |
| `message.usage.input_tokens` | Input column. |
| `message.usage.output_tokens` | Output column. |
| `message.usage.cache_creation_input_tokens` | Cache-Write column. |
| `message.usage.cache_read_input_tokens` | Cache-Read column. |

### Counting rules

- Only the top-level `message.usage` object is summed (ignore the nested
  `iterations` breakdown — top level is the per-message total).
- Records with no `message.usage` or a non-`assistant` type are skipped.
- Malformed JSON lines are skipped, not fatal (a corrupt line must not break the
  whole report).
- Dedup by `message.id` **within a file**. Cross-file duplicate ids are negligible
  and intentionally not deduped — documented here, not silently dropped.
- `cache_read_input_tokens` is shown as its own column even though it is near-free,
  because the user asked for the per-type breakdown.

## Architecture

Two units plus wiring. Pure logic is separated from the TUI so it can be tested
without a terminal.

### 1. `internal/usage` — pure aggregation core (no TUI deps)

Types:

```go
type MonthlyUsage struct {
    Month      string // "YYYY-MM"
    Input      int64
    Output     int64
    CacheWrite int64
    CacheRead  int64
}
func (m MonthlyUsage) Total() int64 // Input+Output+CacheWrite+CacheRead
```

Functions:

- `ParseFile(path string) (months map[string]*MonthlyUsage, meta FileMeta, err error)`
  Reads the file line by line, applies the counting rules above, dedups by
  `message.id`, groups into month buckets. `FileMeta` holds `{ModTime, Size}`.

- `Aggregate(claudeDir string) ([]MonthlyUsage, error)`
  Walks `claudeDir/**/*.jsonl`. Loads the cache. For each file: if cached
  `{mtime,size}` matches the file on disk, reuse the cached per-file month map;
  otherwise re-parse and update the cache entry. Removes cache entries for files
  that no longer exist. Merges all per-file maps into a global map, saves the
  cache, and returns months sorted **descending** (newest first).

`claudeDir` is a parameter (not hardcoded) so tests pass a `t.TempDir()`. The
production caller resolves `~/.claude/projects` via `os.UserHomeDir`.

### 2. `internal/usage/cache.go` — incremental cache

- File: `~/.config/ghost-tab/usage-cache.json`.
- Shape:

```json
{
  "version": 1,
  "files": {
    "/abs/path/session.jsonl": {
      "mod_time": "2026-06-15T19:43:05Z",
      "size": 12345,
      "months": { "2026-06": { "input": 6, "output": 331,
                               "cache_write": 40401, "cache_read": 0 } }
    }
  }
}
```

- `LoadCache(path) (*Cache, error)` — missing or corrupt cache returns an empty
  cache (rebuild from scratch), never an error that blocks the screen.
- `SaveCache(path, *Cache) error` — atomic write (temp file + rename).
- Cache staleness check: a file is re-parsed when its on-disk `ModTime` or `Size`
  differs from the cached entry. Append-only growth changes size, so growing
  session files are caught.

### 3. `internal/tui/stats.go` — `StatsModel` (Bubbletea)

- Implements `tea.Model` (Init/Update/View), consistent with existing screens.
- `Init()` returns a `tea.Cmd` that runs `usage.Aggregate` off the UI thread and
  delivers a `statsLoadedMsg{months []MonthlyUsage}` (or `statsErrMsg`). A spinner
  (bubbles) shows while loading — important because the first run parses 2.68 GB.
- `View()` renders, inside the project's box style:
  - Header: `Token Usage by Month`.
  - One row per month: `YYYY-MM │ Input │ Output │ Cache-W │ Cache-R │ Total`,
    numbers humanized (`1.2M`, `847K`, `12.3B`).
  - A horizontal bar (`█`) under/beside each month scaled so the largest Total
    fills the bar width; other months scale proportionally.
  - Footer hint: `↑↓ scroll · esc back`.
- All-time scrollable: keep an offset into the sorted months; `↑/↓` move it,
  clamped to `[0, len-window]`. Only the visible window renders.
- `esc` → emit the existing `PopScreenMsg`.
- Empty data → friendly "No usage data found yet." message, not a blank table.

### 4. Wiring

- `internal/tui/mainmenu.go`:
  - Add a `T: Stats` action item to the action list and a footer hint.
  - Handle the `t`/`T` key → return `PushScreenMsg{ NewStatsModel(...) }`.
- `cmd/ghost-tab-tui/stats.go`:
  - A standalone `stats` Cobra subcommand that runs `StatsModel` directly. Used
    for manual inspection and easier testing. It is interactive (no JSON output
    to bash — unlike the selector subcommands — because Stats is a viewer).

## Data Flow

```
T pressed in MainMenu
  └─ PushScreenMsg{StatsModel}
       └─ StatsModel.Init() → tea.Cmd → usage.Aggregate(~/.claude/projects)
            ├─ LoadCache(~/.config/ghost-tab/usage-cache.json)
            ├─ walk *.jsonl: reuse cached or ParseFile changed
            ├─ SaveCache(...)
            └─ statsLoadedMsg{months}
       └─ View(): table + bars, scrollable
  └─ esc → PopScreenMsg → back to MainMenu
```

## Error Handling

- Missing `~/.claude/projects`: treat as empty → "No usage data found yet."
- Corrupt cache file: ignored, rebuilt.
- Malformed transcript line: skipped.
- Unreadable transcript file: skipped (logged to the error channel, not fatal);
  the rest still aggregate.
- Aggregate error surfaced as `statsErrMsg` → the screen shows the error and an
  `esc back` hint rather than crashing the TUI.

## Testing (TDD — test first, watch fail, then code)

`internal/usage` (Go unit tests, fixtures in `t.TempDir()`):

- `ParseFile`: month grouping; per-type sums; `Total()`; dedup by `message.id`;
  skip non-assistant; skip records without `usage`; skip malformed lines;
  multi-month file; empty file.
- `Aggregate`: empty dir → empty slice; two files merge; months sorted desc.
- Cache incremental: build cache, then assert an unchanged file is **not**
  re-parsed (spy by mutating cached month values and confirming the result keeps
  the spied values), a file whose size/mtime changed **is** re-parsed, a deleted
  file's entry is dropped, corrupt cache rebuilds.

`internal/tui/stats.go`:

- Build `StatsModel` seeded with fixed `[]MonthlyUsage` (bypass loading) and call
  `View()`: assert month labels present, numbers humanized, bar widths
  proportional to totals (largest total = widest bar), footer hint present.
- Scroll: `↑/↓` keys clamp offset at both ends; window size respected.
- Empty data renders the "no data" message.
- Number humanizer: table-driven (`999→999`, `1000→1.0K`, `1_500_000→1.5M`, ...).

Parallel subagents for implementation (independent files, no shared state):

- Agent A: `internal/usage/{usage.go,cache.go}` + tests.
- Agent B: `internal/tui/stats.go` + tests (depends on the `MonthlyUsage` type
  contract above; can stub it until A lands, then integrate).
- Agent C: wiring in `mainmenu.go` + `cmd/ghost-tab-tui/stats.go` (depends on
  `NewStatsModel`).

Run order: A and B in parallel against the agreed type contract; C after B.

## Completion Gates (from project CLAUDE.md)

- Tests written first, watched fail, then code.
- `shellcheck` on any modified `.sh` (likely none — this is Go-only).
- `./run-tests.sh` full suite green.
- `git push`, `git status` clean.
