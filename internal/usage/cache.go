package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cacheVersion is 3 because the cache now carries a durable per-month Archive of
// totals sealed from deleted transcripts plus the Sealed set of folded paths.
// LoadCache rejects a mismatched version, so older caches rebuild on upgrade.
const cacheVersion = 3

// fileCacheEntry stores one transcript file's identity and its parsed months.
type fileCacheEntry struct {
	Meta   FileMeta                 `json:"meta"`
	Months map[string]*MonthlyUsage `json:"months"`
}

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
