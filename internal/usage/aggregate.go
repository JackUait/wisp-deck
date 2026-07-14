package usage

import (
	"io/fs"
	"os"
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
	a.CacheWrite1h += m.CacheWrite1h
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

func historySourceFromCacheEntry(entry fileCacheEntry) historySource {
	return historySource{
		ParserVersion: cacheVersion,
		Meta:          entry.Meta,
		Months:        entry.Months,
	}
}

func historySourcesFromCacheEntries(entries map[string]fileCacheEntry) map[string]historySource {
	sources := make(map[string]historySource, len(entries))
	for path, entry := range entries {
		sources[path] = historySourceFromCacheEntry(entry)
	}
	return sources
}

func foldHistorySources(acc map[string]map[string]*ModelUsage, sources map[string]historySource) {
	for _, source := range sources {
		foldMonths(acc, source.Months)
	}
}

// parseFunc reads one source file into per-month usage; ParseFile (Claude) and
// ParseOpenCodeMessage (OpenCode) both satisfy it so the cache treats them alike.
type parseFunc func(path string) (map[string]*MonthlyUsage, FileMeta, error)

// Aggregate is AggregateAll for a single Claude transcript root. Most callers
// that track usage across multiple native accounts should use AggregateAll with
// ClaudeAccountProjectDirs instead.
func Aggregate(claudeDir, opencodeDir, cachePath string) ([]MonthlyUsage, error) {
	return AggregateAll([]string{claudeDir}, opencodeDir, "", cachePath)
}

// AggregateAll walks each dir in claudeDirs for *.jsonl transcripts and
// opencodeDir for *.json OpenCode message files, merging all of them into
// per-month token usage sorted newest-first. Multiple Claude roots let usage be
// counted across every native account (the Default ~/.claude plus each extra
// account's config dir); transcript paths are absolute and disjoint across roots,
// so they share one cache without colliding. Same-named models from any source
// fold into a single row. It reuses cachePath entries for files whose size and
// mtime are unchanged and re-parses changed files. Complete per-source snapshots
// are committed to mirrored append-only journals before output is returned, so
// observed history survives source pruning and cache loss/version changes. The
// cache's Archive/Sealed state remains as redundant backward-compatible data and
// is imported into the journal exactly once. A missing/empty opencodeDir (e.g.
// OpenCode not installed) is simply skipped. codexDir is walked for *.jsonl Codex
// rollout files (recursing its YYYY/MM/DD layout) parsed by ParseCodexRollout; an
// empty codexDir is likewise skipped. Best-effort saves the updated cache only
// after the journal commit succeeds.
func AggregateAll(claudeDirs []string, opencodeDir, codexDir, cachePath string) ([]MonthlyUsage, error) {
	cache := LoadCache(cachePath)
	historyPaths := historyPathsForCache(cachePath)
	history, err := commitHistory(historyPaths, historyUpdate{})
	if err != nil {
		return nil, err
	}
	next := &Cache{
		Version: cacheVersion,
		Files:   map[string]fileCacheEntry{},
		Archive: cloneArchive(cache.Archive),
		Sealed:  map[string]bool{},
	}
	for p := range cache.Sealed {
		next.Sealed[p] = true
	}
	for p := range history.Sealed {
		next.Sealed[p] = true
	}

	seen := map[string]bool{}
	consider := func(path string, info fs.FileInfo, parse parseFunc) {
		seen[path] = true
		if next.Sealed[path] {
			// Already folded into Archive; never re-count. A brand-new file that
			// reuses a sealed path would be ignored, but transcript/message paths
			// embed a session id so reuse does not happen in practice.
			return
		}
		if prev, ok := cache.Files[path]; ok &&
			prev.Meta.Size == info.Size() && prev.Meta.ModTime.Equal(info.ModTime()) {
			next.Files[path] = prev
			return
		}
		months, meta, parseErr := parse(path)
		if parseErr != nil {
			return // skip unreadable file
		}
		next.Files[path] = fileCacheEntry{Meta: meta, Months: months}
	}

	walk := func(root, suffix string, parse parseFunc) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable dirs/files, keep going
			}
			if d.IsDir() || !strings.HasSuffix(path, suffix) {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			consider(path, info, parse)
			return nil
		})
	}

	for _, claudeDir := range claudeDirs {
		if err := walk(claudeDir, ".jsonl", ParseFile); err != nil {
			return nil, err
		}
	}
	if err := walk(opencodeDir, ".json", ParseOpenCodeMessage); err != nil {
		return nil, err
	}
	if err := walk(codexDir, ".jsonl", ParseCodexRollout); err != nil {
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

	// On the first journal commit, preserve every old per-file cache entry,
	// including entries whose source vanished before this run. The pre-run archive
	// is imported alongside them; newly sealed cache entries are the same per-file
	// snapshots and must not also be imported as aggregate totals.
	sources := historySourcesFromCacheEntries(next.Files)
	if !history.ImportedLegacy {
		for path, entry := range cache.Files {
			if _, current := sources[path]; !current {
				sources[path] = historySourceFromCacheEntry(entry)
			}
		}
	}
	history, err = commitHistory(historyPaths, historyUpdate{
		Sources:       sources,
		LegacyArchive: cache.Archive,
		LegacySealed:  cache.Sealed,
		ImportLegacy:  true,
	})
	if err != nil {
		return nil, err
	}

	// Dedup is per-file only (ParseFile dedups by message.id within a file). We do
	// NOT dedup across files: a global dedup would require parsing every file
	// together, which defeats the incremental cache. Cross-file duplicate ids are
	// rare (~0.02% in practice) and intentionally tolerated. Do not "fix" this.
	// The journal, rather than the disposable cache, is authoritative for output.
	acc := map[string]map[string]*ModelUsage{}
	foldHistorySources(acc, history.Sources)
	foldArchive(acc, history.Archive)

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

// DefaultPaths returns the production Claude transcript dir, the OpenCode message
// storage dir, the Codex sessions dir, and the cache file path. The OpenCode storage
// root honors OPENCODE_DATA_DIR (which may be a comma-separated list — the first entry
// wins), falling back to ~/.local/share/opencode; messages live under
// <root>/storage/message. The Codex sessions dir honors CODEX_HOME (see
// CodexSessionsDir).
func DefaultPaths(home string) (claudeDir, opencodeDir, codexDir, cachePath string) {
	dataDir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR"))
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share", "opencode")
	} else if i := strings.IndexByte(dataDir, ','); i >= 0 {
		dataDir = strings.TrimSpace(dataDir[:i])
	}
	return filepath.Join(home, ".claude", "projects"),
		filepath.Join(dataDir, "storage", "message"),
		CodexSessionsDir(home),
		filepath.Join(home, ".config", "wisp-deck", "usage-cache.json")
}

// CodexSessionsDir returns the Codex rollout sessions root. Codex stores per-session
// rollout-*.jsonl files under <CODEX_HOME>/sessions/YYYY/MM/DD/. CODEX_HOME defaults
// to ~/.codex, matching the Codex CLI's own resolution.
func CodexSessionsDir(home string) string {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "sessions")
}

// ClaudeAccountProjectDirs returns every Claude transcript root whose usage must
// be counted: the Default login's ~/.claude/projects first, followed by the
// projects/ dir of each additional native account registered under the wisp-deck
// accounts dir (${XDG_CONFIG_HOME:-~/.config}/wisp-deck/claude-accounts/<dir>).
// Each native account is isolated by its own CLAUDE_CONFIG_DIR (see
// internal/claudeaccount), so its transcripts live outside ~/.claude and would
// otherwise be invisible to the stats screen. Account dirs lacking a projects/
// subdir, and the accounts dir being absent entirely, are tolerated.
func ClaudeAccountProjectDirs(home string) []string {
	dirs := []string{filepath.Join(home, ".claude", "projects")}

	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	accountsDir := filepath.Join(configHome, "wisp-deck", "claude-accounts")

	entries, err := os.ReadDir(accountsDir)
	if err != nil {
		return dirs // accounts dir absent/unreadable: Default only
	}
	for _, e := range entries {
		projects := filepath.Join(accountsDir, e.Name(), "projects")
		if info, err := os.Stat(projects); err == nil && info.IsDir() {
			dirs = append(dirs, projects)
		}
	}
	return dirs
}
