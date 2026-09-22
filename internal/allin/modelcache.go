package allin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const anthropicCacheKey = "anthropic"

// configCacheKey matches the <source> of a wisp/cfg.<source>/… row id.
func configCacheKey(file string) string {
	return "cfg." + strings.TrimSuffix(file, ".json")
}

// CacheEntry is one source's reduced model list and when it was fetched.
type CacheEntry struct {
	FetchedAt time.Time `json:"fetched_at"`
	Models    []Listed  `json:"models"`
}

// ModelCache holds every source's last good list, keyed by source.
type ModelCache map[string]CacheEntry

// ModelCachePath sits beside the configs list, so every Env already locates it.
func ModelCachePath(env Env) string {
	if env.ConfigsList == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(env.ConfigsList), "allin-models.json")
}

// LoadModelCache never fails: an unreadable file means "nothing cached", and
// the roster falls back to its static lists.
func LoadModelCache(path string) ModelCache {
	cache := ModelCache{}
	if path == "" {
		return cache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return ModelCache{}
	}
	return cache
}

// SaveModelCache writes through a rename: several routers can refresh at once,
// and a reader must never see half a file.
func SaveModelCache(path string, cache ModelCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Fresh reports whether key holds a list younger than maxAge.
func (c ModelCache) Fresh(key string, now time.Time, maxAge time.Duration) bool {
	entry, ok := c[key]
	return ok && now.Sub(entry.FetchedAt) < maxAge
}
