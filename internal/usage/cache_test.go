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
