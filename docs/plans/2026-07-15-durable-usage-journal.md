# Durable Usage Journal Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Preserve `CacheWrite1h` accurately and make every locally observed usage snapshot survive transcript pruning, cache resets, upgrades, crashes, concurrent writers, and loss of either local history copy.

**Architecture:** Keep `usage-cache.json` as a disposable parsing accelerator and add two checksummed append-only JSONL journals as the authoritative history. Replay unions and repairs both journals under an advisory lock; aggregation upserts complete per-source snapshots before saving the cache and renders from journal state.

**Tech Stack:** Go 1.25, Go standard library (`bufio`, `crypto/sha256`, `encoding/json`, `syscall`), Bubble Tea, `go test`.

**Design:** `docs/plans/2026-07-15-durable-usage-journal-design.md`

**Repository constraint:** Work directly on the existing `main` branch. Never create a branch or worktree.

---

### Task 1: Preserve one-hour cache writes through aggregation

**Files:**
- Modify: `internal/usage/aggregate.go:13-22`
- Test: `internal/usage/aggregate_test.go`

**Step 1: Write the failing merge test**

Extend `TestAddModelRows_accumulatesByModel` with one-hour cache-write values and
assert the combined subset:

```go
addModelRows(dst, []ModelUsage{
    {Model: "claude-opus-4-7", CacheWrite: 10, CacheWrite1h: 4},
    {Model: "claude-opus-4-7", CacheWrite: 5, CacheWrite1h: 2},
})
if got := dst["claude-opus-4-7"].CacheWrite1h; got != 6 {
    t.Fatalf("CacheWrite1h = %d, want 6", got)
}
```

Add an aggregate-level regression using a Claude transcript whose
`cache_creation` has both TTL buckets. Run `Aggregate` twice so the second call
is a cache hit, then assert `CacheWrite == 10`, `CacheWrite1h == 6`, and
`ModelCostUSD` equals the hand-computed 5-minute/1-hour split.

**Step 2: Verify RED**

Run:

```bash
go test ./internal/usage -run 'TestAddModelRows_accumulatesByModel|TestAggregate_preservesCacheWrite1hOnCacheHit' -count=1 -v
```

Expected: FAIL because `addRow` leaves `CacheWrite1h` at zero.

**Step 3: Implement the minimal fix**

In `addRow`, add:

```go
a.CacheWrite1h += m.CacheWrite1h
```

Do not add `CacheWrite1h` to `Total`; it remains a subset of `CacheWrite`.

**Step 4: Verify GREEN**

Run the focused tests, then `go test ./internal/usage -count=1`.

**Step 5: Commit**

```bash
git add internal/usage/aggregate.go internal/usage/aggregate_test.go
git commit -m "fix(usage): preserve one-hour cache writes"
```

---

### Task 2: Implement checksummed journal replay

**Files:**
- Create: `internal/usage/history.go`
- Create: `internal/usage/history_test.go`

**Step 1: Write failing record/replay tests**

Define tests against the wished-for internal API:

```go
func TestHistoryReplay_latestSourceSnapshotWins(t *testing.T) {
    paths := testHistoryPaths(t)
    first := historySource{ParserVersion: 6, Months: testMonths(10)}
    second := historySource{ParserVersion: 6, Months: testMonths(25)}
    appendTestRecord(t, paths.Primary, historyRecord{Sequence: 1, Sources: map[string]historySource{"/a": first}})
    appendTestRecord(t, paths.Primary, historyRecord{Sequence: 2, Sources: map[string]historySource{"/a": second}})

    state, _, err := readHistoryCopies(paths)
    if err != nil { t.Fatal(err) }
    if got := state.Sources["/a"].Months["2026-07"].Input; got != 25 {
        t.Fatalf("input = %d, want 25", got)
    }
}
```

Also cover:

- checksum mismatch is not applied;
- a valid record in the other copy recovers a corrupt counterpart;
- two valid records with the same sequence and different checksums fail;
- legacy archive folding preserves `CacheWrite1h`;
- a later snapshot for one source does not affect another source; and
- unknown schemas return an error without deleting readable records.

**Step 2: Verify RED**

Run `go test ./internal/usage -run '^TestHistoryReplay' -count=1 -v`.
Expected: compile failure because journal types/functions do not exist.

**Step 3: Implement record types and pure replay**

Create these core shapes in `history.go`:

```go
const historySchemaVersion = 1

type historyPaths struct { Primary, Backup, Lock string }

type historySource struct {
    ParserVersion int                      `json:"parser_version"`
    Meta          FileMeta                 `json:"meta"`
    Months        map[string]*MonthlyUsage `json:"months"`
}

type historyRecord struct {
    Schema        int                               `json:"schema"`
    Sequence      uint64                            `json:"sequence"`
    Sources       map[string]historySource          `json:"sources,omitempty"`
    Archive       map[string]map[string]*ModelUsage `json:"archive,omitempty"`
    Sealed        []string                          `json:"sealed,omitempty"`
    ImportsLegacy bool                              `json:"imports_legacy,omitempty"`
    Checksum      string                            `json:"checksum"`
}

type historyState struct {
    Sources        map[string]historySource
    Archive        map[string]map[string]*ModelUsage
    Sealed         map[string]bool
    ImportedLegacy bool
    LastSequence   uint64
    Records        map[uint64]historyRecord
}
```

Implement checksum generation by marshaling a copy with an empty checksum and
hashing it with SHA-256. Read JSONL with a 50 MiB scanner buffer, validate each
known-schema record, union records from both copies, sort by sequence, reject
valid conflicts, and apply source replacement/archive folding in order.

`historyPathsForCache` must produce the production names for
`usage-cache.json`; arbitrary test cache names receive adjacent unique history
names so tests do not collide.

**Step 4: Verify GREEN and refactor**

Run the focused tests and `go test ./internal/usage -count=1`.

**Step 5: Commit**

```bash
git add internal/usage/history.go internal/usage/history_test.go
git commit -m "feat(usage): add checksummed usage history replay"
```

---

### Task 3: Add dual-copy locking, commit, and repair

**Files:**
- Modify: `internal/usage/history.go`
- Modify: `internal/usage/history_test.go`

**Step 1: Write failing durability tests**

Add real-filesystem tests for:

- a commit exists with identical checksum and sequence in both files;
- files are mode `0600`;
- deleting primary repairs it from backup;
- deleting backup repairs it from primary;
- a truncated final primary line is skipped and repaired from backup;
- a stale source snapshot cannot overwrite a newer snapshot committed by
  another writer;
- two goroutines committing disjoint sources preserve their union; and
- failure to open/sync either copy is returned, not swallowed.

Use a test hook around the low-level append function only for the otherwise
unreproducible partial dual-write case; all other tests use real files and locks.

**Step 2: Verify RED**

Run:

```bash
go test ./internal/usage -run 'TestHistoryCommit|TestHistoryRepair|TestHistoryConcurrent' -count=1 -v
```

Expected: compile failures for the commit API.

**Step 3: Implement the write protocol**

Implement:

```go
type historyUpdate struct {
    Sources        map[string]historySource
    LegacyArchive  map[string]map[string]*ModelUsage
    LegacySealed   map[string]bool
    ImportLegacy   bool
}

func commitHistory(paths historyPaths, update historyUpdate) (historyState, error)
```

The function must:

1. create the parent directory and lock file with user-only permissions;
2. hold `syscall.Flock(..., LOCK_EX)` across reload, repair, sequence allocation,
   and both appends;
3. reload both copies after acquiring the lock;
4. append valid records missing from either copy in sequence order;
5. ignore already-current source snapshots;
6. reject an older parser version or older `ModTime` for an existing source;
7. append a newline boundary after a truncated tail;
8. encode one complete record line in memory;
9. append and `Sync` primary, then append and `Sync` backup; and
10. return any durability error, even if one copy already committed.

On the next call, union replay repairs a one-sided successful append before
allocating a new sequence.

**Step 4: Verify GREEN**

Run focused tests, the race detector for the concurrency test, and the package:

```bash
go test -race ./internal/usage -run 'TestHistoryCommit|TestHistoryRepair|TestHistoryConcurrent' -count=1
go test ./internal/usage -count=1
```

**Step 5: Commit**

```bash
git add internal/usage/history.go internal/usage/history_test.go
git commit -m "feat(usage): persist mirrored append-only history"
```

---

### Task 4: Make journal history authoritative in aggregation

**Files:**
- Modify: `internal/usage/aggregate.go`
- Modify: `internal/usage/aggregate_test.go`
- Modify: `internal/usage/cache_test.go`

**Step 1: Write failing integration tests**

Add aggregate-level tests proving:

1. deleting a source and deleting/corrupting the cache still returns its journal
   usage;
2. writing an incompatible cache version does not remove journal history;
3. a v6 cache with live `Files`, missing-file entries, `Archive`, and `Sealed`
   imports exactly once without double-counting;
4. `CacheWrite1h` survives deletion and legacy migration;
5. a growing source replaces its previous journal snapshot rather than adding a
   second copy;
6. two concurrent aggregates over disjoint roots preserve both sources in the
   shared history; and
7. a history commit error is returned from `AggregateAll`.

**Step 2: Verify RED**

Run:

```bash
go test ./internal/usage -run 'TestAggregate.*(History|Journal|Legacy|CacheWrite1h)' -count=1 -v
```

Expected: tests fail because output still depends on `Cache.Files` and
`Cache.Archive`.

**Step 3: Integrate the journal**

Refactor `AggregateAll` to:

- load/repair initial history before the source walk;
- keep legacy sealed paths ignored;
- build current live cache entries as today;
- construct a `historyUpdate` from all current live entries;
- on first migration, also include every old cache `Files` entry, old `Archive`,
  and old `Sealed` path;
- commit history before saving the cache;
- return a journal error instead of silently showing unpersisted data;
- fold output from `historyState.Sources` and `historyState.Archive`; and
- retain existing cache sealing only as redundant backward-compatible state,
  with cache save remaining best-effort after journal durability succeeds.

Extract small helpers for cache-entry-to-history-source conversion and history
state folding. Preserve the public `Aggregate`/`AggregateAll` signatures.

**Step 4: Verify GREEN**

Run focused tests, then:

```bash
go test -race ./internal/usage -count=1
```

**Step 5: Commit**

```bash
git add internal/usage/aggregate.go internal/usage/aggregate_test.go internal/usage/cache_test.go
git commit -m "feat(usage): make local journal history authoritative"
```

---

### Task 5: Ingest usage when the main menu starts

**Files:**
- Modify: `internal/tui/mainmenu.go:2176-2185`
- Test: `internal/tui/mainmenu_stats_test.go`

**Step 1: Write the failing initialization test**

Create a main-menu model with animation disabled, call `Init`, and assert that:

- the returned command is non-nil; and
- `statsLoading` becomes true before Stats is opened.

Do not execute the command in this unit test because it would inspect the real
home directory.

**Step 2: Verify RED**

Run:

```bash
go test ./internal/tui -run TestMainMenuInit_startsUsageIngestion -count=1 -v
```

Expected: FAIL because `Init` only starts animation timers.

**Step 3: Implement background ingestion**

Append `m.ensureStatsLoad()` to the `Init` command batch. Preserve animation
commands and the existing `statsLoaded`/`statsLoading` guard so Stats navigation
does not start a duplicate load.

**Step 4: Verify GREEN**

Run the focused test and all TUI tests:

```bash
go test ./internal/tui -count=1
```

**Step 5: Commit**

```bash
git add internal/tui/mainmenu.go internal/tui/mainmenu_stats_test.go
git commit -m "feat(tui): ingest usage history at startup"
```

---

### Task 6: Full verification, migration smoke test, and local installation

**Files:**
- Modify if needed: `README.md`
- Verify: `bin/wisp-deck-tui`, `~/.local/bin/wisp-deck-tui`

**Step 1: Run formatting and static checks**

```bash
gofmt -w internal/usage/history.go internal/usage/history_test.go internal/usage/aggregate.go internal/usage/aggregate_test.go internal/tui/mainmenu.go internal/tui/mainmenu_stats_test.go
git diff --check
go vet ./...
```

**Step 2: Run all tests with race coverage on the changed packages**

```bash
go test -race ./internal/usage ./internal/tui -count=1
go test ./... -count=1
```

**Step 3: Perform an isolated migration smoke test**

Use temporary HOME/source/cache paths through a focused Go test or existing test
binary. Prove that first aggregation creates both journals, second aggregation
does not append a duplicate legacy import, deleting the source and cache retains
the same monthly totals, and each copy can restore the other.

Never experiment against or delete the user's real `~/.config/wisp-deck` data.

**Step 4: Update documentation if the user-visible storage behavior is not clear**

Document the local journal paths and durability boundary without claiming
survival of whole-disk loss.

**Step 5: Install and verify according to `AGENTS.md`**

```bash
make install
command -v wisp-deck-tui
shasum -a 256 bin/wisp-deck-tui ~/.local/bin/wisp-deck-tui
codesign --verify --verbose ~/.local/bin/wisp-deck-tui
```

Expected:

- command resolves to `~/.local/bin/wisp-deck-tui`;
- hashes match; and
- code-signature verification succeeds.

**Step 6: Review the final diff and commits**

```bash
git status --short
git diff HEAD~5 --stat
git log -6 --oneline
```

Confirm no unrelated user changes were modified. A running selector must be
restarted to begin background ingestion with the newly installed binary.
