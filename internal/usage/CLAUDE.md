# usage — gotchas

Gotchas for usage accounting. Loaded when Claude opens a file in this package.

### A Codex conversation is spread over many rollout files, and they replay each other

Claude keeps a conversation in one transcript, so per-file dedup is enough for it.
Codex does not. Forking a subagent, resuming a thread, and compacting each open a
**fresh** rollout file that begins by replaying the ancestor's entire
`token_count` history, re-stamped with the load instant. One real month held 1,684
rollout files that were only **43 session chains** — a single chain spanned 448
files — and summing every `last_token_usage` reported **466B tokens / $309K**
against **14.9B / ~$10.5K** actually spent. A **31x** over-count, shipped and
visible in the Stats tab.

A replayed record is byte-identical to a live one, so nothing about a single line
identifies it. What separates them is **density**: a replay dumps hundreds of
events inside one second, while a real request round-trip takes seconds. So
`ParseCodexRollout` drops every event in a second holding more than
`codexReplayBurstPerSecond` events, and collapses a verbatim re-emission of the
request it just counted (Codex repeats one). Measured against the full corpus,
those two per-file rules reproduce the cross-file **deduplicated** truth to three
decimal places while keeping **100%** of distinct requests — which is what lets
dedup stay per-file and keeps the incremental `Aggregate` cache intact.

Consequences to respect:

- **Never restore a plain "sum every `last_token_usage`" reading**, and never
  reach for `total_token_usage`: it is cumulative *including* the replays, so a
  forked rollout's final value hit 2.42B for one thread.
- **Changing this parser requires bumping `cacheVersion`** (`internal/usage/cache.go`).
  The per-file cache *and* the append-only journal both store parsed months
  tagged with a parser version, and only a higher version supersedes them —
  without the bump the old inflated numbers are simply reloaded.
- Guarded by the `TestParseCodexRollout_dropsReplayedHistoryBurst`,
  `_collapsesConsecutiveDuplicateTokenCounts`, and `_keepsBurstFreeRapidRequests`
  tests in `internal/usage/codex_test.go` — the last one is the counterweight, so
  a future tightening cannot start eating genuinely busy seconds.

### A Claude message is counted from its LAST line, never its first

Claude Code writes several lines per `message.id`. The first is a streaming
snapshot: `output_tokens` of a few tokens and no `iterations`. Only the final
line (the one with `stop_reason`) has the real output count and the
`iterations` array, which is the only place an `advisor_message` round is
billed. Keeping the first line counted Oct 2026 output as **41.1M against
73.9M** and Opus as **$3.9K against $5.1K**. Guarded by
`TestParseFile_countsAMessageFromItsFinalLine`.

### Any change to parser output must bump `cacheVersion`

`TestParserOutputChangesBumpCacheVersion` (`parser_golden_test.go`) hashes what
all three parsers return for a fixed corpus and pins it to `cacheVersion`. If
the output changes, the test fails until `cacheVersion` is bumped and the hash
is re-pinned. Without the bump, cached and journaled months keep the old
numbers. Even with it, months whose source files are gone stay as they were.
When a new transcript shape matters, add it to the corpus.

### Every model the catalog offers needs its own price entry

`rateFor` matches the longest prefix, so a new sibling quietly takes its
family's rate: `gpt-6-sol` was billed through `"gpt-6"` at Astra's $10/$50,
5x its own $2/$10. `TestEveryCatalogModelHasItsOwnPrice` fails on any
`claudeconfig` catalog id without an exact `modelRates` key. An id may inherit
on purpose only from `inheritsFamilyRate`, with the reason written next to it.
Prices come from a source, never from a sibling's rate.

### Known, measured, not fixed

- **Subagent transcripts copy their parent's messages** (same uuid and id), so
  a message is counted once per copy. Measured at ~0.4% of GPT-6 Sol in Oct
  2026. Dedup is per file on purpose (see the aggregate cache), so this is
  tolerated.
- **Historical bridged GPT rows can carry estimates instead of real usage**:
  old responses recorded bytes/4 input without a cache split. New responses
  defer billing until Codex reports real usage and use usage.iterations when
  delayed billing differs from the latest context. Old estimates cannot be
  corrected without their original Codex accounting.
