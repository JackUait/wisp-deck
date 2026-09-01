package featherless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CacheTTL is how long a stored catalog is served without refetching. The list
// grows as Featherless adds models, but never fast enough to be worth a 7MB
// download every time the picker opens.
const CacheTTL = 24 * time.Hour

// cacheVersion guards the stored Model shape. Bump it whenever Model's fields
// change, or an older cache decodes into zero values and the picker fills with
// nameless 0-token rows.
const cacheVersion = 1

type cacheFile struct {
	Version   int     `json:"version"`
	FetchedAt int64   `json:"fetched_at"`
	Models    []Model `json:"models"`
}

// LoadCache reads a stored catalog, reporting when it was fetched. A missing,
// unreadable, unparseable, or differently versioned cache reports ok=false.
func LoadCache(path string) ([]Model, time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	var stored cacheFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, time.Time{}, false
	}
	if stored.Version != cacheVersion || len(stored.Models) == 0 {
		return nil, time.Time{}, false
	}
	return stored.Models, time.Unix(stored.FetchedAt, 0), true
}

// SaveCache stores a catalog, creating the directory if needed. The catalog is
// public data, so it carries no credential and needs no restricted mode.
func SaveCache(path string, models []Model) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{
		Version:   cacheVersion,
		FetchedAt: time.Now().Unix(),
		Models:    models,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Stale reports whether a cache fetched at that instant should be refreshed.
func Stale(fetchedAt time.Time) bool { return time.Since(fetchedAt) > CacheTTL }

// DefaultCachePath is where the picker stores the catalog, beside wisp-deck's
// other configuration. LoadCache and SaveCache take an explicit path so they
// never read the environment themselves.
func DefaultCachePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "wisp-deck", "featherless-models.json")
}
