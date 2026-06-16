package usage

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// addRow accumulates one model's tokens into dst (model id -> running total).
func addRow(dst map[string]*ModelUsage, m ModelUsage) {
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

// addModelRows accumulates per-model rows into dst (model id -> running total).
func addModelRows(dst map[string]*ModelUsage, rows []ModelUsage) {
	for _, m := range rows {
		addRow(dst, m)
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
			addRow(bucket, *m)
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
			// Already folded into Archive; never re-count. A brand-new file that
			// reuses a sealed path would be ignored, but transcript paths embed a
			// session UUID so reuse does not happen in practice.
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

// DefaultPaths returns the production transcript dir and cache file path.
func DefaultPaths(home string) (claudeDir, cachePath string) {
	return filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".config", "ghost-tab", "usage-cache.json")
}
