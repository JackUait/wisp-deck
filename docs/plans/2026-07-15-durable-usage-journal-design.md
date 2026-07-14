# Durable Usage Journal — Design

**Date:** 2026-07-15
**Status:** Approved

## Problem

The Stats tab derives token usage from Claude Code, OpenCode, and Codex session
files. Its only persisted state is `usage-cache.json`. That file mixes two
responsibilities: disposable parse acceleration and the only copy of usage from
source files that have been pruned. Cache-version changes, corruption, deletion,
and concurrent last-writer-wins saves can therefore erase history.

Claude's one-hour cache-write count has a separate correctness problem.
`ParseFile` records `CacheWrite1h`, but `addRow` omits it while combining files,
so the Stats cost calculation treats every cache write as a cheaper five-minute
write.

## Durability boundary

Once Wisp Deck has observed a source file, its usage must survive:

- source transcript pruning;
- Wisp Deck and parser/cache schema upgrades;
- process interruption and a truncated final write;
- concurrent Wisp Deck processes;
- loss or corruption of either one of the two local journal copies; and
- deletion or corruption of the disposable parse cache.

No local-only design can survive physical loss of the disk or deliberate
deletion of every local copy. Usage pruned before Wisp Deck ever observes it is
also unrecoverable because no source data exists to ingest.

## Chosen approach

Keep `usage-cache.json` only as an optimization. Make two append-only journals
the authoritative history:

- `~/.config/wisp-deck/usage-history.jsonl`
- `~/.config/wisp-deck/usage-history.backup.jsonl`

Each journal line is a complete, checksummed transaction containing a monotonic
sequence number and zero or more full per-source usage snapshots. A snapshot is
keyed by the source's absolute path and contains its file metadata, parser
version, and month/model totals. A later snapshot for the same path replaces the
earlier snapshot during replay; an absent source never deletes its last known
snapshot.

This structure is append-only: no successful update rewrites or removes an
older committed record. A partial final line is ignored, leaving all earlier
records usable. Both journals are replayed together, records are deduplicated,
and a valid record missing from either copy is appended to repair that copy.
Conflicting valid records at the same sequence are reported rather than guessed.

The alternatives rejected were:

- strengthening the existing JSON cache with migrations and rotating backups,
  because every full-file replacement would remain a destructive operation;
- SQLite, because its transactions are attractive but adding a large database
  dependency is unnecessary for an append-and-replay workload; and
- retaining only aggregate month totals, because corrected parsers cannot safely
  replace an individual source without knowing that source's previous totals.

## Journal data and replay

A journal transaction contains:

- a journal schema version that is decoded per record rather than used to reject
  the whole file;
- a sequence number allocated while holding the journal lock;
- a checksum over the record payload;
- complete source snapshots to upsert;
- optional legacy aggregate totals and sealed paths imported from the v6 cache;
  and
- a marker proving that the legacy cache import has occurred.

Replay validates every checksum, unions valid records from both copies, sorts by
sequence, rejects conflicting records, and applies source updates in order.
Malformed lines are skipped when a valid counterpart or earlier source snapshot
exists. Unknown future record schemas are reported without destroying readable
history.

The first run imports all v6 `Files` entries, including entries whose source has
already disappeared, plus the existing `Archive` and `Sealed` state. The import
and current live-source updates commit in the journal before the disposable
cache is saved. Future cache-version changes may discard and rebuild the cache,
but never affect journal replay.

## Write protocol and concurrency

Source walking and parsing may happen without the journal lock. Before commit,
the writer acquires an advisory lock beside the journals, reloads both copies,
repairs either copy, allocates the next sequence, and removes source updates that
are already current. It then appends the same encoded transaction to each file
and calls `fsync` before releasing the lock.

If only one copy accepts a transaction, that copy remains authoritative and the
operation returns an error instead of silently claiming durability. The next
writer repairs the missing transaction before adding another. The Stats UI must
surface journal persistence errors; unlike the old cache save, they are not
best-effort.

The cache is saved only after the journal commit. A cache-save failure does not
lose usage because the journals already contain the authoritative snapshots.

## Aggregation flow

1. Load the disposable cache and replay the journals.
2. Walk every configured Claude, OpenCode, and Codex source root.
3. Reuse a compatible unchanged cache entry or parse the source.
4. Under the journal lock, import the legacy cache when necessary and append
   changed source snapshots to both journal copies.
5. Build Stats output from journal source snapshots plus the one-time legacy
   aggregate import, never from cache ownership or file presence.
6. Save the live-file parse cache as a best-effort optimization.

The main menu starts this ingestion in the background at launch instead of
waiting until the user first opens Stats. This reduces the interval in which a
new transcript exists but has never been observed.

## CacheWrite1h correctness

`CacheWrite1h` is a subset of `CacheWrite`, not an additional token category.
Every model-row merge must add both fields independently. Full source snapshots,
legacy migration, cache hits, journal replay, and deleted-source history all
preserve `CacheWrite1h`. `ModelCostUSD` continues to price the subset at 2× input
and the remainder at 1.25× input.

Existing archived totals that already discarded the one-hour subset cannot be
reconstructed after their source transcripts are gone. Live and per-file cached
records retain the field and become correct during migration.

## Error handling

- Missing journals start empty and are created with user-only permissions.
- One missing, truncated, or partially corrupt copy is repaired from the other.
- A valid conflict, unsupported schema, lock failure, or failure to synchronize
  both copies is returned to the Stats loader.
- Malformed source records retain the parsers' existing skip behavior.
- A corrupt or incompatible parse cache is ignored and rebuilt from live files;
  journal history remains authoritative.

## Testing

Tests will prove:

- `CacheWrite1h` survives model merging, cache hits, source deletion, journal
  replay, and legacy-cache migration, and produces the two-rate cost;
- later source snapshots replace earlier snapshots without double-counting;
- a missing source retains its last snapshot indefinitely;
- cache corruption and cache-version rejection do not remove journal history;
- primary and backup loss, truncation, and malformed lines recover from the
  valid counterpart;
- concurrent writers preserve the union of their source updates;
- partial or failed dual writes are surfaced and repaired on the next open;
- the existing v6 cache imports exactly once; and
- main-menu initialization starts background ingestion.

The complete `internal/usage` suite and repository-wide Go tests must pass. The
final binary is installed and verified according to `AGENTS.md`.
