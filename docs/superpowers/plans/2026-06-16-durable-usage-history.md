# Durable Usage History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist monthly token-usage history so it survives Claude Code's transcript pruning, by sealing a transcript's totals into a durable month-keyed archive the moment its `.jsonl` file disappears.

**Architecture:** Extend the existing usage cache (`internal/usage/cache.go`) with an `Archive` (month → model → totals) and a `Sealed` set of folded file paths. `Aggregate` (`internal/usage/aggregate.go`) seals vanished files into the archive, skips already-sealed paths on the walk, and merges live files + archive into the output. Each transcript path is counted in exactly one place (live `Files` or sealed `Archive`).

**Tech Stack:** Go, standard library only (`encoding/json`, `path/filepath`, `io/fs`). Tests via `go test`.

**Spec:** `docs/superpowers/specs/2026-06-16-durable-usage-history-design.md`

---

### Task 1: Extend the Cache struct with Archive + Sealed

**Files:**
- Modify: `internal/usage/cache.go` (const `cacheVersion`, type `Cache`, func `LoadCache`)
- Test: `internal/usage/cache_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/usage/cache_test.go`:

```go
func TestLoadCache_initializesArchiveAndSealed(t *testing.T) {
	c := LoadCache(filepath.Join(t.TempDir(), "nope.json"))
	if c.Archive == nil {
		t.Errorf("Archive = nil, want initialized map")
	}
	if c.Sealed == nil {
		t.Errorf("Sealed = nil, want initialized map")
	}
}

func TestCache_archiveAndSealedRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	c := &Cache{
		Version: cacheVersion,
		Files:   map[string]fileCacheEntry{},
		Archive: map[string]map[string]*ModelUsage{
			"2026-05": {"claude-opus-4-7": {Model: "claude-opus-4-7", Input: 7}},
		},
		Sealed: map[string]bool{"/gone.jsonl": true},
	}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got := LoadCache(p)
	if got.Archive["2026-05"]["claude-opus-4-7"].Input != 7 {
		t.Errorf("archive round-trip mismatch: %+v", got.Archive)
	}
	if !got.Sealed["/gone.jsonl"] {
		t.Errorf("sealed round-trip mismatch: %+v", got.Sealed)
	}
}

func TestLoadCache_rejectsOlderVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	// A v2-shaped cache on disk must be rejected and rebuilt empty on upgrade.
	writeFixture(t, filepath.Dir(p), "cache.json",
		`{"version":2,"files":{"/a.jsonl":{"meta":{"mod_time":"2026-05-01T00:00:00Z","size":1},"months":{}}}}`)
	c := LoadCache(p)
	if len(c.Files) != 0 || c.Archive == nil || c.Sealed == nil {
		t.Errorf("v2 cache should rebuild empty v3, got %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/usage/ -run 'TestLoadCache_initializesArchiveAndSealed|TestCache_archiveAndSealedRoundTrip|TestLoadCache_rejectsOlderVersion' -v`
Expected: FAIL — compile error (`c.Archive`/`c.Sealed` undefined).

- [ ] **Step 3: Write minimal implementation**

In `internal/usage/cache.go`, bump the version and update the comment:

```go
// cacheVersion is 3 because the cache now carries a durable per-month Archive of
// totals sealed from deleted transcripts plus the Sealed set of folded paths.
// LoadCache rejects a mismatched version, so older caches rebuild on upgrade.
const cacheVersion = 3
```

Replace the `Cache` type:

```go
// Cache is the persisted incremental-parse state. Files holds one entry per live
// transcript (keyed by absolute path) for incremental re-parsing. Archive holds
// durable month -> model -> totals folded from transcripts that have since been
// deleted from disk, and Sealed records which paths have already been folded so
// nothing is counted twice.
type Cache struct {
	Version int                               `json:"version"`
	Files   map[string]fileCacheEntry         `json:"files"`
	Archive map[string]map[string]*ModelUsage `json:"archive"`
	Sealed  map[string]bool                   `json:"sealed"`
}
```

Replace `LoadCache` so the empty cache and a loaded (possibly older-shaped) cache always have non-nil `Archive`/`Sealed`:

```go
// LoadCache reads the cache file. A missing or corrupt cache returns an empty,
// usable cache (so the stats screen can always rebuild from scratch) — never nil.
// Archive and Sealed are always non-nil so callers can write to them directly.
func LoadCache(path string) *Cache {
	empty := &Cache{
		Version: cacheVersion,
		Files:   map[string]fileCacheEntry{},
		Archive: map[string]map[string]*ModelUsage{},
		Sealed:  map[string]bool{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil || c.Version != cacheVersion || c.Files == nil {
		return empty
	}
	if c.Archive == nil {
		c.Archive = map[string]map[string]*ModelUsage{}
	}
	if c.Sealed == nil {
		c.Sealed = map[string]bool{}
	}
	return &c
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/usage/ -run 'TestLoadCache|TestCache_' -v`
Expected: PASS (new tests including `TestLoadCache_rejectsOlderVersion` pass; existing `TestLoadCache_*` and `TestCache_saveThenLoadRoundTrips` still pass — they only touch `Files`).

- [ ] **Step 5: Commit**

```bash
git add internal/usage/cache.go internal/usage/cache_test.go
git commit -m "feat(usage): add durable Archive + Sealed to cache (v3)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Add merge helpers for per-model accumulation

**Files:**
- Modify: `internal/usage/aggregate.go` (new unexported helpers)
- Test: `internal/usage/aggregate_test.go`

These helpers replace the inline merge loop and are reused by sealing and output
building (DRY). Adding them first keeps Task 3 focused on flow.

- [ ] **Step 1: Write the failing test**

Add to `internal/usage/aggregate_test.go`:

```go
func TestAddModelRows_accumulatesByModel(t *testing.T) {
	dst := map[string]*ModelUsage{}
	addModelRows(dst, []ModelUsage{
		{Model: "claude-opus-4-7", Input: 10, Output: 1},
		{Model: "claude-opus-4-7", Input: 5, CacheRead: 2},
		{Model: "claude-fable-5", Input: 3},
	})
	if dst["claude-opus-4-7"].Input != 15 || dst["claude-opus-4-7"].Output != 1 ||
		dst["claude-opus-4-7"].CacheRead != 2 {
		t.Errorf("opus row = %+v, want input 15 output 1 cacheRead 2", dst["claude-opus-4-7"])
	}
	if dst["claude-fable-5"].Input != 3 {
		t.Errorf("fable row = %+v, want input 3", dst["claude-fable-5"])
	}
}

func TestFoldMonths_accumulatesByMonthAndModel(t *testing.T) {
	acc := map[string]map[string]*ModelUsage{}
	foldMonths(acc, map[string]*MonthlyUsage{
		"2026-05": {Month: "2026-05", Models: []ModelUsage{{Model: "m", Input: 4}}},
	})
	foldMonths(acc, map[string]*MonthlyUsage{
		"2026-05": {Month: "2026-05", Models: []ModelUsage{{Model: "m", Input: 6}}},
	})
	if acc["2026-05"]["m"].Input != 10 {
		t.Errorf("acc = %+v, want 2026-05/m input 10", acc)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/usage/ -run 'TestAddModelRows_accumulatesByModel|TestFoldMonths_accumulatesByMonthAndModel' -v`
Expected: FAIL — compile error (`addModelRows`/`foldMonths` undefined).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/usage/aggregate.go` (above `Aggregate`):

```go
// addModelRows accumulates per-model rows into dst (model id -> running total).
func addModelRows(dst map[string]*ModelUsage, rows []ModelUsage) {
	for _, m := range rows {
		a := dst[m.Model]
		if a == nil {
			a = &ModelUsage{Model: m.Model}
			dst[m.Model] = a
		}
		a.Input += m.Input
		a.Output += m.Output
		a.CacheWrite += m.CacheWrite
		a.CacheRead += m.CacheRead
	}
}

// foldMonths accumulates a file's per-month, per-model usage into acc
// (month -> model -> running total).
func foldMonths(acc map[string]map[string]*ModelUsage, months map[string]*MonthlyUsage) {
	for month, mu := range months {
		bucket := acc[month]
		if bucket == nil {
			bucket = map[string]*ModelUsage{}
			acc[month] = bucket
		}
		addModelRows(bucket, mu.Models)
	}
}

// foldArchive accumulates an archive (month -> model -> total) into acc.
func foldArchive(acc map[string]map[string]*ModelUsage, archive map[string]map[string]*ModelUsage) {
	for month, byModel := range archive {
		bucket := acc[month]
		if bucket == nil {
			bucket = map[string]*ModelUsage{}
			acc[month] = bucket
		}
		for _, m := range byModel {
			addModelRows(bucket, []ModelUsage{*m})
		}
	}
}

// cloneArchive deep-copies an archive so callers can fold into the copy without
// mutating the loaded cache's pointers.
func cloneArchive(src map[string]map[string]*ModelUsage) map[string]map[string]*ModelUsage {
	dst := map[string]map[string]*ModelUsage{}
	for month, byModel := range src {
		bucket := map[string]*ModelUsage{}
		for id, mu := range byModel {
			cp := *mu
			bucket[id] = &cp
		}
		dst[month] = bucket
	}
	return dst
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/usage/ -run 'TestAddModelRows|TestFoldMonths' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usage/aggregate.go internal/usage/aggregate_test.go
git commit -m "refactor(usage): extract per-model merge helpers

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Seal deleted transcripts into the archive in Aggregate

**Files:**
- Modify: `internal/usage/aggregate.go` (func `Aggregate`)
- Test: `internal/usage/aggregate_test.go` (update one existing test, add three new)

- [ ] **Step 1: Update the existing deletion test to expect sealing**

In `internal/usage/aggregate_test.go`, replace `TestAggregate_dropsDeletedFileFromCache` (the whole function) with:

```go
func TestAggregate_sealsDeletedFileIntoArchive(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// The month survives even though its transcript is gone.
	if len(out) != 1 || out[0].Month != "2026-05" || out[0].Input != 10 {
		t.Fatalf("out = %+v, want one 2026-05 row with input 10", out)
	}
	c := LoadCache(cachePath)
	if _, ok := c.Files[p]; ok {
		t.Errorf("deleted file should not remain in Files")
	}
	if !c.Sealed[p] {
		t.Errorf("deleted file should be recorded in Sealed")
	}
	if c.Archive["2026-05"]["claude-opus-4-7"].Input != 10 {
		t.Errorf("archive = %+v, want 2026-05/opus input 10", c.Archive)
	}
}

func TestAggregate_sealedFileNotDoubleCountedOnReappear(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	record := `{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	p := writeFixture(t, dir, "a.jsonl", record)
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	if _, err := Aggregate(dir, cachePath); err != nil { // seals into archive
		t.Fatal(err)
	}
	writeFixture(t, dir, "a.jsonl", record) // same path reappears
	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// Counted once (from the archive), NOT 20.
	if len(out) != 1 || out[0].Input != 10 {
		t.Errorf("out = %+v, want input 10 (sealed path skipped, not re-added)", out)
	}
}

func TestAggregate_mergesArchiveWithLiveMonth(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	// Two May files; delete one so it seals, keep one live. Both feed 2026-05.
	gone := writeFixture(t, dir, "gone.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"g","model":"claude-opus-4-7","usage":{"input_tokens":4,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, dir, "live.jsonl",
		`{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"l","model":"claude-opus-4-7","usage":{"input_tokens":6,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(gone)
	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// Archived 4 (gone) + live 6 = 10 for 2026-05.
	if len(out) != 1 || out[0].Input != 10 {
		t.Errorf("out = %+v, want 2026-05 input 10 (archive 4 + live 6)", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/usage/ -run 'TestAggregate_sealsDeletedFileIntoArchive|TestAggregate_sealedFileNotDoubleCountedOnReappear|TestAggregate_mergesArchiveWithLiveMonth' -v`
Expected: FAIL — current `Aggregate` drops deleted files, so `out` is empty / `Sealed` and `Archive` are empty.

- [ ] **Step 3: Rewrite Aggregate to seal and merge**

In `internal/usage/aggregate.go`, replace the body of `Aggregate` (keep the doc comment, updating it as shown) with:

```go
// Aggregate walks claudeDir for *.jsonl transcripts and returns per-month token
// usage sorted newest-first. It reuses cachePath entries for files whose size and
// mtime are unchanged and re-parses changed files. When a previously-cached file
// vanishes from disk, its totals are sealed into a durable per-month Archive so
// the history survives Claude Code's transcript pruning; sealed paths are never
// re-counted. Best-effort saves the updated cache.
func Aggregate(claudeDir, cachePath string) ([]MonthlyUsage, error) {
	cache := LoadCache(cachePath)
	next := &Cache{
		Version: cacheVersion,
		Files:   map[string]fileCacheEntry{},
		Archive: cloneArchive(cache.Archive),
		Sealed:  map[string]bool{},
	}
	for p := range cache.Sealed {
		next.Sealed[p] = true
	}

	seen := map[string]bool{}
	err := filepath.WalkDir(claudeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs/files, keep going
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		seen[path] = true
		if next.Sealed[path] {
			return nil // already folded into Archive; never re-count
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		if prev, ok := cache.Files[path]; ok &&
			prev.Meta.Size == info.Size() && prev.Meta.ModTime.Equal(info.ModTime()) {
			next.Files[path] = prev
			return nil
		}
		months, meta, parseErr := ParseFile(path)
		if parseErr != nil {
			return nil // skip unreadable file
		}
		next.Files[path] = fileCacheEntry{Meta: meta, Months: months}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Seal transcripts that were cached but have vanished from disk: fold their
	// months into the durable archive so the history outlives the source file.
	for path, entry := range cache.Files {
		if seen[path] || next.Sealed[path] {
			continue
		}
		foldMonths(next.Archive, entry.Months)
		next.Sealed[path] = true
	}

	// Dedup is per-file only (ParseFile dedups by message.id within a file). We do
	// NOT dedup across files: a global dedup would require parsing every file
	// together, which defeats the incremental cache. Cross-file duplicate ids are
	// rare (~0.02% in practice) and intentionally tolerated. Do not "fix" this.
	// Accumulate live files plus the sealed archive into month -> model totals.
	acc := map[string]map[string]*ModelUsage{}
	for _, entry := range next.Files {
		foldMonths(acc, entry.Months)
	}
	foldArchive(acc, next.Archive)

	_ = next.Save(cachePath) // best-effort; a save failure must not break the view

	out := make([]MonthlyUsage, 0, len(acc))
	for month, byModel := range acc {
		if mu := buildMonthly(month, byModel); mu != nil {
			out = append(out, *mu)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out, nil
}
```

- [ ] **Step 4: Run the usage suite to verify all pass**

Run: `go test ./internal/usage/ -v`
Expected: PASS — new sealing tests pass; `TestAggregate_mergesFilesAndSortsDescending`, `TestAggregate_reusesCacheForUnchangedFiles`, `TestAggregate_reparsesChangedFile`, `TestAggregate_mergesModelsAcrossFiles`, and Task 1/2 tests still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/usage/aggregate.go internal/usage/aggregate_test.go
git commit -m "feat(usage): seal deleted transcripts into durable archive

History now survives Claude Code's transcript pruning: a vanished
file's monthly totals are folded into a month-keyed archive and its
path recorded so it is never double-counted.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Full verification, lint, install, push

**Files:** none (verification only)

- [ ] **Step 1: Run the full Go test suite**

Run: `go test ./... -count=1`
Expected: every package `ok` (notably `internal/usage`, `internal/tui`, `test/bash`).

- [ ] **Step 2: Lint shell scripts (unchanged, but checklist-mandated)**

Run: `shellcheck lib/*.sh lib/terminals/*.sh bin/ghost-tab wrapper.sh`
Expected: no output (no scripts changed; confirms nothing regressed).

- [ ] **Step 3: Rebuild and install the local binary**

Run: `make install`
Expected: `✓ Installed ghost-tab-tui`. The change is parsing/aggregation only; the stats screen will now accumulate history going forward as transcripts get pruned.

- [ ] **Step 4: Push the branch**

```bash
git push -u origin feat/durable-usage-history
git status
```
Expected: branch pushed; `git status` shows it tracks `origin/feat/durable-usage-history`.

- [ ] **Step 5: Open a PR (optional, if requested)**

```bash
gh pr create --fill
```

---

## Notes for the implementer

- **No view changes.** The stats screen already scrolls 8 months at a time; older months simply appear as the archive grows. Do not touch `internal/tui/stats.go`.
- **History is forward-only.** Months pruned from disk before this ships cannot be recovered — there is no source to read. This is expected.
- **Version bump is intentional.** A v2 cache on disk is rejected by `LoadCache` and rebuilt from current files. No history is lost because none was being kept before.
- **Do not add cross-file dedup.** It is deliberately omitted (see the comment in `Aggregate`).
