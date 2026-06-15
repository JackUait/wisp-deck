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

	// Dedup is per-file only (ParseFile dedups by message.id within a file). We do
	// NOT dedup across files: a global dedup would require parsing every file
	// together, which defeats the incremental cache. Cross-file duplicate ids are
	// rare (~0.02% in practice) and intentionally tolerated. Do not "fix" this.
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
