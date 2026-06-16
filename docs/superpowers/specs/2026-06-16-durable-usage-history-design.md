# Durable Usage History — Design

**Date:** 2026-06-16
**Status:** Approved, pending implementation

## Problem

The stats screen ("Token Usage by Month") only shows two months because Claude
Code prunes transcript files older than `cleanupPeriodDays` (default 30). Once a
`*.jsonl` transcript is deleted from `~/.claude/projects`, `usage.Aggregate`
stops re-adding it to the cache, so that month's tokens disappear from the view.

We want monthly usage history to persist for as long as the user keeps using
ghost-tab, surviving Claude Code's transcript pruning.

## Scope

- History accumulates **from now forward**. Months already pruned from disk
  before this ships are unrecoverable — there is no source left to read them
  from. This is stated, not a feature to build.
- We do **not** modify the user's Claude config (`cleanupPeriodDays`). Raising it
  only delays loss and bloats `~/.claude`; out of scope.
- The stats screen view is unchanged: it shows 8 months at a time and scrolls
  (↑↓) through older ones. No layout work.

## Approach

When a transcript file disappears from disk, **seal** its parsed month→model
totals into a durable archive inside the existing usage cache, then drop its
per-file entry. The archive is keyed by month (not by file), so it stays tiny
(~6 model rows per month, dozens per year) regardless of how many transcripts
come and go.

Rejected alternatives:

- **Retain every per-file entry forever.** Smallest change but the `Files` map
  grows unbounded (~4k transcripts/month → multi-MB JSON parsed each time the
  stats screen opens). Too slow over time.
- **Raise `cleanupPeriodDays`.** Touches the user's Claude config, only delays
  the loss, and bloats `~/.claude`. Out of scope.

## Data model (`internal/usage/cache.go`)

Bump `cacheVersion` 2 → 3. `LoadCache` already rejects a version mismatch, so
existing v2 caches rebuild cleanly from current disk files on upgrade (no
history existed to lose).

```go
type Cache struct {
    Version int                              `json:"version"`
    Files   map[string]fileCacheEntry        `json:"files"`   // live transcripts, per-file
    Archive map[string]map[string]*ModelUsage `json:"archive"` // month -> model -> sealed totals
    Sealed  map[string]bool                  `json:"sealed"`  // file paths already folded into Archive
}
```

- `Files` — unchanged: live transcripts kept per-file so a growing in-progress
  session re-parses incrementally on size/mtime change.
- `Archive` — month → model → accumulated `ModelUsage` from deleted files. The
  same shape the aggregator already accumulates into.
- `Sealed` — set of file paths already folded into `Archive`, so a path is never
  counted twice.

`LoadCache` returns an empty-but-usable cache (with non-nil `Files`, `Archive`,
`Sealed`) on missing/corrupt/version-mismatch, exactly as today.

## Aggregate flow (`internal/usage/aggregate.go`)

1. Load cache. Seed `next.Archive` and `next.Sealed` from the loaded cache
   (carry forward prior history).
2. Walk `claudeDir` for `*.jsonl`, collecting the set of paths seen on disk:
   - If a path is in `Sealed`, **skip** it — already counted in `Archive`;
     re-parsing it live would double-count. (Guards the rare case of a sealed
     transcript reappearing.)
   - Otherwise: cache-hit (unchanged size+mtime) or re-parse, into `next.Files`
     — unchanged logic.
3. After the walk, for each path in the **old** `cache.Files` that was not seen
   on disk and is not already in `Sealed` (deleted transcript): fold its
   `Months` into `next.Archive` (month→model accumulation) and set
   `next.Sealed[path] = true`. Do **not** carry it in `next.Files`.
4. Build the output accumulator by merging **both** `next.Files` entries and
   `next.Archive` into the existing month→model `acc` map, then `buildMonthly`
   per month as today.
5. `next.Save(cachePath)` (best-effort, as today).

### Invariant

Every transcript path is counted in exactly one place after each run: live in
`Files`, or once-sealed in `Archive`. The walk skips `Sealed` paths; deleted
`Files` paths get sealed. No path contributes to both.

Per-file message-id dedup (in `ParseFile`) is preserved. Cross-file duplicate
ids remain intentionally tolerated (~0.02%), unchanged.

## Testing (TDD — test first, watch fail, implement)

In `internal/usage/aggregate_test.go` / `cache_test.go`:

1. **Seal on delete:** run `Aggregate`, then remove a transcript file, run again
   — that file's month/model totals still appear in the output and in
   `cache.Archive`.
2. **No double-count on reappear:** seal a file (delete + aggregate), then
   recreate the same path, aggregate again — totals are unchanged (file is
   skipped, not re-added on top of the archive).
3. **Live re-parse still works:** an existing (not deleted) file that grows
   (size/mtime change) re-parses and updates `Files`, not `Archive`.
4. **Archive + live merge:** a month with both a live file and a sealed file
   sums both into one month row.
5. **Version upgrade:** a v2 cache file on disk is rejected and rebuilt; output
   matches a from-scratch parse of current disk files.

## Out of scope / non-goals

- Recovering pre-existing deleted months.
- Changing the stats view layout or the 8-month window.
- Modifying Claude Code settings.
- Archive pruning/caps — the archive is inherently tiny (per-month, not
  per-file), so no cap is needed.
