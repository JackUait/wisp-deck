# Monthly Token Stats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `T`-key Stats screen to the Ghost Tab TUI showing per-month token usage (input/output/cache-write/cache-read + total) parsed from Claude Code transcripts, with a table + bar chart, incremental caching, all-time scroll.

**Architecture:** A pure `internal/usage` package parses `~/.claude/projects/**/*.jsonl`, aggregates by month, and caches per-file results to `~/.config/ghost-tab/usage-cache.json` so only changed files are re-parsed. A Bubbletea `StatsModel` (`internal/tui/stats.go`) loads it async with a spinner and renders a scrollable table + bars. `mainmenu.go` pushes the screen on `T`.

**Tech Stack:** Go 1.25, Bubbletea v1.3.10, Bubbles v1.0.0 (spinner), Lipgloss v1.1.0, Cobra v1.10.2.

**Parallelization:** Tasks 1–3 (`internal/usage`) and Tasks 4–6 (`internal/tui/stats.go`) are independent and can run as two parallel subagents against the `usage.MonthlyUsage` contract defined in Task 1. Tasks 7–8 (wiring) depend on both and run after. Task 9 is final integration.

**Reference spec:** `docs/superpowers/specs/2026-06-16-monthly-token-stats-design.md`

---

## Task 1: Usage core — `MonthlyUsage` type + `ParseFile`

**Files:**
- Create: `internal/usage/usage.go`
- Test: `internal/usage/usage_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package usage

import (
	"path/filepath"
	"os"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTotal_sumsAllColumns(t *testing.T) {
	m := MonthlyUsage{Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4}
	if got := m.Total(); got != 10 {
		t.Fatalf("Total() = %d, want 10", got)
	}
}

func TestParseFile_groupsByMonthAndSumsTypes(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":1}}}
{"type":"assistant","timestamp":"2026-05-02T10:00:00.000Z","message":{"id":"b","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":4}}}
{"type":"assistant","timestamp":"2026-06-01T10:00:00.000Z","message":{"id":"c","usage":{"input_tokens":100,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, meta, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size == 0 {
		t.Errorf("meta.Size = 0, want non-zero")
	}
	may := months["2026-05"]
	if may == nil || may.Input != 11 || may.Output != 22 || may.CacheWrite != 8 || may.CacheRead != 5 {
		t.Errorf("May = %+v, want input 11 output 22 cacheW 8 cacheR 5", may)
	}
	if jun := months["2026-06"]; jun == nil || jun.Input != 100 {
		t.Errorf("Jun = %+v, want input 100", jun)
	}
}

func TestParseFile_dedupsByMessageID(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"dup","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-05-01T10:00:01.000Z","message":{"id":"dup","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := months["2026-05"].Input; got != 10 {
		t.Errorf("Input = %d, want 10 (dedup by id)", got)
	}
}

func TestParseFile_skipsNonAssistantNoUsageAndMalformed(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"user","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"u"}}
{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"nousage"}}
this is not json
{"type":"assistant","timestamp":"2026-05-01T10:00:00.000Z","message":{"id":"ok","usage":{"input_tokens":7,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`
	p := writeFixture(t, dir, "s.jsonl", content)
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 1 || months["2026-05"].Input != 7 {
		t.Errorf("months = %+v, want only the one valid assistant record (input 7)", months)
	}
}

func TestParseFile_emptyFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFixture(t, dir, "empty.jsonl", "")
	months, _, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 0 {
		t.Errorf("months = %+v, want empty", months)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/usage/ -run TestParseFile -v`
Expected: FAIL — `undefined: ParseFile`, `undefined: MonthlyUsage`.

- [ ] **Step 3: Write minimal implementation**

```go
package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// MonthlyUsage holds token counts for a single YYYY-MM bucket.
type MonthlyUsage struct {
	Month      string `json:"month"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheWrite int64  `json:"cache_write"`
	CacheRead  int64  `json:"cache_read"`
}

// Total returns the sum of all token columns.
func (m MonthlyUsage) Total() int64 {
	return m.Input + m.Output + m.CacheWrite + m.CacheRead
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
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseFile reads a single .jsonl transcript and aggregates token usage by month.
// Non-assistant records, records without usage, and malformed lines are skipped.
// Assistant records are deduped by message.id within this file.
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

	months := map[string]*MonthlyUsage{}
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
		mu := months[month]
		if mu == nil {
			mu = &MonthlyUsage{Month: month}
			months[month] = mu
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
	return months, meta, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/usage/ -v`
Expected: PASS (all Task 1 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/usage/usage.go internal/usage/usage_test.go
git commit -m "feat(usage): parse Claude transcripts into monthly token totals"
```

---

## Task 2: Usage cache — load/save with atomic write

**Files:**
- Create: `internal/usage/cache.go`
- Test: `internal/usage/cache_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCache_missingFileReturnsEmpty(t *testing.T) {
	c := LoadCache(filepath.Join(t.TempDir(), "nope.json"))
	if c == nil || c.Files == nil {
		t.Fatalf("LoadCache = %+v, want non-nil cache with initialized Files", c)
	}
	if len(c.Files) != 0 {
		t.Errorf("Files = %v, want empty", c.Files)
	}
}

func TestLoadCache_corruptFileReturnsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	writeFixture(t, filepath.Dir(p), "cache.json", "{not valid json")
	c := LoadCache(p)
	if c == nil || len(c.Files) != 0 {
		t.Fatalf("corrupt cache should rebuild empty, got %+v", c)
	}
}

func TestCache_saveThenLoadRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	c := &Cache{Version: cacheVersion, Files: map[string]fileCacheEntry{
		"/a.jsonl": {
			Meta:   FileMeta{ModTime: time.Unix(1000, 0).UTC(), Size: 42},
			Months: map[string]*MonthlyUsage{"2026-05": {Month: "2026-05", Input: 5}},
		},
	}}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got := LoadCache(p)
	entry, ok := got.Files["/a.jsonl"]
	if !ok || entry.Meta.Size != 42 || entry.Months["2026-05"].Input != 5 {
		t.Errorf("round-trip mismatch: %+v", got.Files)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/usage/ -run Cache -v`
Expected: FAIL — `undefined: LoadCache`, `undefined: Cache`, `undefined: cacheVersion`.

- [ ] **Step 3: Write minimal implementation**

```go
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const cacheVersion = 1

// fileCacheEntry stores one transcript file's identity and its parsed months.
type fileCacheEntry struct {
	Meta   FileMeta                 `json:"meta"`
	Months map[string]*MonthlyUsage `json:"months"`
}

// Cache is the persisted incremental-parse state keyed by absolute file path.
type Cache struct {
	Version int                       `json:"version"`
	Files   map[string]fileCacheEntry `json:"files"`
}

// LoadCache reads the cache file. A missing or corrupt cache returns an empty,
// usable cache (so the stats screen can always rebuild from scratch) — never nil.
func LoadCache(path string) *Cache {
	empty := &Cache{Version: cacheVersion, Files: map[string]fileCacheEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil || c.Version != cacheVersion || c.Files == nil {
		return empty
	}
	return &c
}

// Save writes the cache atomically (temp file + rename).
func (c *Cache) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/usage/ -v`
Expected: PASS (Task 1 + Task 2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/usage/cache.go internal/usage/cache_test.go
git commit -m "feat(usage): incremental parse cache with atomic save"
```

---

## Task 3: Usage aggregation — incremental walk + merge + sort

**Files:**
- Create: `internal/usage/aggregate.go`
- Test: `internal/usage/aggregate_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAggregate_emptyDir(t *testing.T) {
	out, err := Aggregate(t.TempDir(), filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("out = %+v, want empty", out)
	}
}

func TestAggregate_mergesFilesAndSortsDescending(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "proj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, sub, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	writeFixture(t, sub, "b.jsonl",
		`{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-06-01T10:00:00Z","message":{"id":"c","usage":{"input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	out, err := Aggregate(dir, filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Month != "2026-06" || out[1].Month != "2026-05" {
		t.Errorf("order = [%s,%s], want descending [2026-06,2026-05]", out[0].Month, out[1].Month)
	}
	if out[1].Input != 15 {
		t.Errorf("May Input = %d, want 15 (merged across files)", out[1].Input)
	}
}

func TestAggregate_reusesCacheForUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")

	// First pass builds the cache.
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	// Tamper the cached value WITHOUT touching the file on disk. If Aggregate
	// reuses the cache (file unchanged), the tampered value must survive.
	c := LoadCache(cachePath)
	info, _ := os.Stat(p)
	c.Files[p] = fileCacheEntry{
		Meta:   FileMeta{ModTime: info.ModTime(), Size: info.Size()},
		Months: map[string]*MonthlyUsage{"2026-05": {Month: "2026-05", Input: 999}},
	}
	if err := c.Save(cachePath); err != nil {
		t.Fatal(err)
	}
	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Input != 999 {
		t.Errorf("Input = %+v, want 999 (cache reused for unchanged file)", out)
	}
}

func TestAggregate_reparsesChangedFile(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	// Append a new record and bump mtime so size/mtime differ from cache.
	more := `{"type":"assistant","timestamp":"2026-05-02T10:00:00Z","message":{"id":"b","usage":{"input_tokens":5,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(more)
	f.Close()
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(p, future, future)

	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Input != 15 {
		t.Errorf("Input = %+v, want 15 (file re-parsed after change)", out)
	}
}

func TestAggregate_dropsDeletedFileFromCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	p := writeFixture(t, dir, "a.jsonl",
		`{"type":"assistant","timestamp":"2026-05-01T10:00:00Z","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n")
	if _, err := Aggregate(dir, cachePath); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	out, err := Aggregate(dir, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("out = %+v, want empty after file deleted", out)
	}
	if _, ok := LoadCache(cachePath).Files[p]; ok {
		t.Errorf("deleted file still present in cache")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/usage/ -run Aggregate -v`
Expected: FAIL — `undefined: Aggregate`.

- [ ] **Step 3: Write minimal implementation**

```go
package usage

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Aggregate walks claudeDir for *.jsonl transcripts and returns per-month token
// usage sorted newest-first. It reuses cachePath entries for files whose size and
// mtime are unchanged, re-parses changed files, drops entries for deleted files,
// and best-effort saves the updated cache.
func Aggregate(claudeDir, cachePath string) ([]MonthlyUsage, error) {
	cache := LoadCache(cachePath)
	next := &Cache{Version: cacheVersion, Files: map[string]fileCacheEntry{}}

	err := filepath.WalkDir(claudeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs/files, keep going
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
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

	merged := map[string]*MonthlyUsage{}
	for _, entry := range next.Files {
		for month, mu := range entry.Months {
			agg := merged[month]
			if agg == nil {
				agg = &MonthlyUsage{Month: month}
				merged[month] = agg
			}
			agg.Input += mu.Input
			agg.Output += mu.Output
			agg.CacheWrite += mu.CacheWrite
			agg.CacheRead += mu.CacheRead
		}
	}

	_ = next.Save(cachePath) // best-effort; a save failure must not break the view

	out := make([]MonthlyUsage, 0, len(merged))
	for _, mu := range merged {
		out = append(out, *mu)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out, nil
}

// DefaultPaths returns the production transcript dir and cache file path.
func DefaultPaths(home string) (claudeDir, cachePath string) {
	return filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".config", "ghost-tab", "usage-cache.json")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/usage/ -v`
Expected: PASS (all usage tests).

- [ ] **Step 5: Commit**

```bash
git add internal/usage/aggregate.go internal/usage/aggregate_test.go
git commit -m "feat(usage): incremental monthly aggregation across transcripts"
```

---

## Task 4: Number humanizer for the stats view

**Files:**
- Create: `internal/tui/stats.go`
- Test: `internal/tui/stats_test.go`

> This task creates `stats.go` with only the humanizer so the file compiles. Tasks 5–6 extend the same file.

- [ ] **Step 1: Write the failing test**

```go
package tui

import "testing"

func TestHumanizeTokens(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{12345, "12.3K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{2000000000, "2.0B"},
	}
	for _, tt := range tests {
		if got := humanizeTokens(tt.in); got != tt.want {
			t.Errorf("humanizeTokens(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestHumanizeTokens -v`
Expected: FAIL — `undefined: humanizeTokens`.

- [ ] **Step 3: Write minimal implementation**

```go
package tui

import (
	"fmt"
	"strconv"
)

// humanizeTokens renders a token count as a compact string (999, 1.5K, 2.0M, 3.1B).
func humanizeTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestHumanizeTokens -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/stats.go internal/tui/stats_test.go
git commit -m "feat(tui): add token count humanizer"
```

---

## Task 5: `StatsModel` rendering — table, bars, empty, error

**Files:**
- Modify: `internal/tui/stats.go`
- Test: `internal/tui/stats_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// add to internal/tui/stats_test.go

import (
	"strings"

	"github.com/jackuait/ghost-tab/internal/usage"
)

func TestStatsView_rendersMonthRowsHumanizedAndBars(t *testing.T) {
	months := []usage.MonthlyUsage{
		{Month: "2026-06", Input: 2_000_000, Output: 0, CacheWrite: 0, CacheRead: 0}, // total 2M (max)
		{Month: "2026-05", Input: 1_000_000, Output: 0, CacheWrite: 0, CacheRead: 0}, // total 1M (half)
	}
	m := NewStatsModelWithData(months)
	view := m.View()

	if !strings.Contains(view, "2026-06") || !strings.Contains(view, "2026-05") {
		t.Errorf("view missing month labels:\n%s", view)
	}
	if !strings.Contains(view, "2.0M") || !strings.Contains(view, "1.0M") {
		t.Errorf("view missing humanized totals:\n%s", view)
	}
	bar6 := countBarRunes(view, "2026-06")
	bar5 := countBarRunes(view, "2026-05")
	if bar6 <= bar5 || bar5 == 0 {
		t.Errorf("bar widths not proportional: jun=%d may=%d", bar6, bar5)
	}
}

func TestStatsView_emptyShowsFriendlyMessage(t *testing.T) {
	m := NewStatsModelWithData([]usage.MonthlyUsage{})
	if !strings.Contains(m.View(), "No usage data") {
		t.Errorf("empty view should show 'No usage data', got:\n%s", m.View())
	}
}

func TestStatsView_errorShown(t *testing.T) {
	m := NewStatsModelWithData(nil)
	m.err = errTestStats
	if !strings.Contains(m.View(), "Failed") {
		t.Errorf("error view should mention failure, got:\n%s", m.View())
	}
}

// countBarRunes returns how many '█' runes appear on the line containing label.
func countBarRunes(view, label string) int {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, label) {
			return strings.Count(line, "█")
		}
	}
	return 0
}

var errTestStats = stubErr("boom")

type stubErr string

func (e stubErr) Error() string { return string(e) }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestStatsView -v`
Expected: FAIL — `undefined: NewStatsModelWithData`, `StatsModel` has no `err` field, etc.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/tui/stats.go` (keep `humanizeTokens` from Task 4):

```go
import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/usage"
)

const (
	statsBarWidth = 24
	statsWindow   = 8 // months visible at once before scrolling
)

// StatsModel renders monthly token usage as a scrollable table + bar chart.
type StatsModel struct {
	months    []usage.MonthlyUsage
	loading   bool
	err       error
	offset    int
	claudeDir string
	cachePath string
}

// NewStatsModelWithData builds a ready-to-render model (no async load). For tests
// and any caller that already has aggregated data.
func NewStatsModelWithData(months []usage.MonthlyUsage) StatsModel {
	return StatsModel{months: months, loading: false}
}

// maxTotal returns the largest month Total, used to scale bars. Minimum 1.
func (m StatsModel) maxTotal() int64 {
	var max int64 = 1
	for _, mu := range m.months {
		if t := mu.Total(); t > max {
			max = t
		}
	}
	return max
}

func (m StatsModel) renderRow(mu usage.MonthlyUsage, max int64) string {
	barLen := int(mu.Total() * int64(statsBarWidth) / max)
	if barLen == 0 && mu.Total() > 0 {
		barLen = 1
	}
	bar := strings.Repeat("█", barLen)
	return fmt.Sprintf("%s  in %-7s out %-7s cw %-7s cr %-7s = %-7s %s",
		mu.Month,
		humanizeTokens(mu.Input),
		humanizeTokens(mu.Output),
		humanizeTokens(mu.CacheWrite),
		humanizeTokens(mu.CacheRead),
		humanizeTokens(mu.Total()),
		bar,
	)
}

func (m StatsModel) View() string {
	title := titleStyle.Render("Token Usage by Month")
	hint := lipgloss.NewStyle().Faint(true).Render("↑↓ scroll · esc back")

	if m.err != nil {
		return title + "\n\n" + "Failed to load usage: " + m.err.Error() + "\n\n" + hint
	}
	if m.loading {
		return title + "\n\n" + "Crunching token usage…" + "\n\n" + hint
	}
	if len(m.months) == 0 {
		return title + "\n\n" + "No usage data found yet." + "\n\n" + hint
	}

	max := m.maxTotal()
	end := m.offset + statsWindow
	if end > len(m.months) {
		end = len(m.months)
	}
	var b strings.Builder
	b.WriteString(title + "\n\n")
	for _, mu := range m.months[m.offset:end] {
		b.WriteString(m.renderRow(mu, max) + "\n")
	}
	b.WriteString("\n" + hint)
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run "TestStatsView|TestHumanizeTokens" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/stats.go internal/tui/stats_test.go
git commit -m "feat(tui): render monthly token table with bar chart"
```

---

## Task 6: `StatsModel` behavior — Init load, scroll, esc, messages

**Files:**
- Modify: `internal/tui/stats.go`
- Test: `internal/tui/stats_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// add to internal/tui/stats_test.go

import (
	tea "github.com/charmbracelet/bubbletea"
)

func threeMonths() []usage.MonthlyUsage {
	return []usage.MonthlyUsage{
		{Month: "2026-06", Input: 3}, {Month: "2026-05", Input: 2}, {Month: "2026-04", Input: 1},
	}
}

func TestStatsUpdate_scrollClampsAtBothEnds(t *testing.T) {
	m := NewStatsModelWithData(threeMonths())
	// Up at top stays at 0.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.(StatsModel).offset != 0 {
		t.Errorf("offset after up-at-top = %d, want 0", updated.(StatsModel).offset)
	}
	// Down advances by one.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.(StatsModel).offset != 1 {
		t.Errorf("offset after down = %d, want 1", updated.(StatsModel).offset)
	}
	// Down repeatedly clamps at len-1 (max scroll keeps last row visible).
	m2 := updated.(StatsModel)
	for i := 0; i < 10; i++ {
		u, _ := m2.Update(tea.KeyMsg{Type: tea.KeyDown})
		m2 = u.(StatsModel)
	}
	if m2.offset > len(m2.months)-1 {
		t.Errorf("offset = %d, want clamped <= %d", m2.offset, len(m2.months)-1)
	}
}

func TestStatsUpdate_escEmitsPopScreen(t *testing.T) {
	m := NewStatsModelWithData(threeMonths())
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc returned nil cmd, want PopScreenMsg cmd")
	}
	if _, ok := cmd().(PopScreenMsg); !ok {
		t.Errorf("esc cmd = %T, want PopScreenMsg", cmd())
	}
}

func TestStatsUpdate_loadedMsgPopulatesAndStopsLoading(t *testing.T) {
	m := StatsModel{loading: true}
	updated, _ := m.Update(statsLoadedMsg{months: threeMonths()})
	sm := updated.(StatsModel)
	if sm.loading || len(sm.months) != 3 {
		t.Errorf("after load: loading=%v months=%d, want false/3", sm.loading, len(sm.months))
	}
}

func TestStatsUpdate_errMsgSetsError(t *testing.T) {
	m := StatsModel{loading: true}
	updated, _ := m.Update(statsErrMsg{err: errTestStats})
	sm := updated.(StatsModel)
	if sm.err == nil || sm.loading {
		t.Errorf("after err: err=%v loading=%v, want set/false", sm.err, sm.loading)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestStatsUpdate -v`
Expected: FAIL — `StatsModel` has no `Update`, `Init`; `statsLoadedMsg`/`statsErrMsg` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/tui/stats.go`:

```go
import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

type statsLoadedMsg struct{ months []usage.MonthlyUsage }
type statsErrMsg struct{ err error }

// NewStatsModel builds a model that loads usage asynchronously on Init.
func NewStatsModel() StatsModel {
	home, _ := os.UserHomeDir()
	claudeDir, cachePath := usage.DefaultPaths(home)
	return StatsModel{loading: true, claudeDir: claudeDir, cachePath: cachePath}
}

func (m StatsModel) Init() tea.Cmd {
	if !m.loading {
		return nil
	}
	claudeDir, cachePath := m.claudeDir, m.cachePath
	return func() tea.Msg {
		months, err := usage.Aggregate(claudeDir, cachePath)
		if err != nil {
			return statsErrMsg{err: err}
		}
		return statsLoadedMsg{months: months}
	}
}

func (m StatsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statsLoadedMsg:
		m.months = msg.months
		m.loading = false
		return m, nil
	case statsErrMsg:
		m.err = msg.err
		m.loading = false
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			return m, func() tea.Msg { return PopScreenMsg{} }
		case tea.KeyUp:
			if m.offset > 0 {
				m.offset--
			}
			return m, nil
		case tea.KeyDown:
			if m.offset < len(m.months)-1 {
				m.offset++
			}
			return m, nil
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				switch msg.Runes[0] {
				case 'k':
					if m.offset > 0 {
						m.offset--
					}
				case 'j':
					if m.offset < len(m.months)-1 {
						m.offset++
					}
				}
			}
			return m, nil
		}
	}
	return m, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS (all tui tests, including pre-existing).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/stats.go internal/tui/stats_test.go
git commit -m "feat(tui): StatsModel async load, scroll, esc handling"
```

---

## Task 7: `stats` standalone subcommand

**Files:**
- Create: `cmd/ghost-tab-tui/stats.go`
- Test: none (thin Cobra wrapper; covered by manual run in Task 9). Verify it builds.

- [ ] **Step 1: Write the implementation**

```go
package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/jackuait/ghost-tab/internal/tui"
	"github.com/jackuait/ghost-tab/internal/util"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show monthly token usage",
	Long:  "Displays Claude Code token usage aggregated by month.",
	RunE:  runStats,
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

func runStats(cmd *cobra.Command, args []string) error {
	tui.ApplyTheme(tui.ThemeForTool(aiToolFlag))

	model := tui.NewStatsModel()

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	defer cleanup()

	opts := append([]tea.ProgramOption{tea.WithAltScreen()}, ttyOpts...)
	p := tea.NewProgram(model, opts...)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}
	return nil
}
```

> Note: `aiToolFlag` is the existing global persistent flag used by the other subcommands (see `select_project.go:30`). If the build reports it undefined, confirm its declaration in `root.go` — do not redeclare it.

- [ ] **Step 2: Verify it builds**

Run: `go build ./cmd/ghost-tab-tui/`
Expected: builds with no error.

- [ ] **Step 3: Commit**

```bash
git add cmd/ghost-tab-tui/stats.go
git commit -m "feat(cmd): add stats subcommand"
```

---

## Task 8: Wire Stats into the main menu (`T` key)

**Files:**
- Modify: `internal/tui/mainmenu.go` (action labels `~line 104-113`, `handleRune` `~line 1390-1433`)
- Test: `internal/tui/mainmenu_stats_test.go`

- [ ] **Step 1: Write the failing test**

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMainMenu_TKeyPushesStatsScreen(t *testing.T) {
	m := NewMainMenuModel(nil) // adjust constructor call to match existing signature
	updated, cmd := m.handleRune('t')
	_ = updated
	if cmd == nil {
		t.Fatal("'t' returned nil cmd, want PushScreenMsg{StatsModel}")
	}
	push, ok := cmd().(PushScreenMsg)
	if !ok {
		t.Fatalf("'t' cmd = %T, want PushScreenMsg", cmd())
	}
	if _, ok := push.Model.(StatsModel); !ok {
		t.Errorf("pushed model = %T, want StatsModel", push.Model)
	}
}

func TestMainMenu_hasStatsActionLabel(t *testing.T) {
	found := false
	for _, a := range actionLabels {
		if a.shortcut == "T" && a.label == "Stats" {
			found = true
		}
	}
	if !found {
		t.Errorf("actionLabels missing {T, Stats}: %+v", actionLabels)
	}
}
```

> Before running, open `mainmenu.go` and confirm the exact `NewMainMenuModel` constructor signature; adjust the test's construction call to match (the assertion logic stays).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestMainMenu_TKey -v` and `-run TestMainMenu_hasStatsActionLabel -v`
Expected: FAIL — no `T` rune case; `actionLabels` has no `{T, Stats}`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/mainmenu.go`, add the Stats action label (after the Settings entry, `~line 112`):

```go
	{"S", "Settings"},
	{"T", "Stats"},
```

In `handleRune` (after the `case 's', 'S':` block, `~line 1420`), add:

```go
	case 't', 'T':
		return m, func() tea.Msg { return PushScreenMsg{Model: NewStatsModel()} }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestMainMenu -v`
Expected: PASS.

- [ ] **Step 5: Fix any layout/count assertions broken by the new action item**

Run: `go test ./internal/tui/ -v`
The render test in `mainmenu_render_test.go` may assert a fixed action-item count or menu height. If it fails, update its expected values to include the new `T: Stats` row (this is a legitimate expectation change, not a workaround). Re-run until green.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/mainmenu.go internal/tui/mainmenu_stats_test.go internal/tui/mainmenu_render_test.go
git commit -m "feat(tui): add T:Stats action to main menu"
```

---

## Task 9: Integration, full suite, manual verification, push

**Files:** none (verification only)

- [ ] **Step 1: Build the binary**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 2: Run the full Go test suite**

Run: `./run-tests.sh`
Expected: all tests pass.

- [ ] **Step 3: shellcheck (only if any .sh changed)**

This feature is Go-only. If `git diff --name-only origin/main` shows no `.sh` files, skip. Otherwise run:
Run: `shellcheck lib/*.sh lib/terminals/*.sh bin/ghost-tab wrapper.sh`
Expected: clean.

- [ ] **Step 4: Manual smoke test**

Run: `go run ./cmd/ghost-tab-tui/ stats`
Expected: spinner ("Crunching token usage…") on first run while it parses, then a table of months with bars. Press `↑`/`↓` to scroll, `esc` to exit. Run a second time — it should appear near-instantly (cache hit). Confirm `~/.config/ghost-tab/usage-cache.json` was created.

- [ ] **Step 5: Push**

```bash
git pull --rebase
git push
git status   # MUST show "up to date with origin"
```

---

## Self-Review Notes

- **Spec coverage:** T-key nav (Task 8) · per-type+total table (Task 5) · cache+incremental (Tasks 2–3) · table+bars (Task 5) · all-time scroll (Tasks 5–6) · async spinner/error/empty (Tasks 5–6) · dedup + skip-malformed + month grouping (Task 1) · standalone subcommand (Task 7). All covered.
- **Type consistency:** `MonthlyUsage{Month,Input,Output,CacheWrite,CacheRead}` + `Total()`, `FileMeta{ModTime,Size}`, `fileCacheEntry{Meta,Months}`, `Cache{Version,Files}`, `Aggregate(claudeDir,cachePath)`, `DefaultPaths(home)`, `NewStatsModel()`/`NewStatsModelWithData()`, `statsLoadedMsg`/`statsErrMsg` — used identically across tasks.
- **Open verification points flagged inline:** `NewMainMenuModel` constructor signature (Task 8) and `aiToolFlag` declaration (Task 7) must be confirmed against existing code during implementation.
