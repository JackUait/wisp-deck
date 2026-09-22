package allin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModelCache_round_trips_and_ages(t *testing.T) {
	dir := t.TempDir()
	env := Env{ConfigsList: filepath.Join(dir, "claude-configs.list")}
	path := ModelCachePath(env)
	if path != filepath.Join(dir, "allin-models.json") {
		t.Fatalf("path = %q", path)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cache := ModelCache{anthropicCacheKey: {FetchedAt: now, Models: listed("claude-opus-5-5")}}
	if err := SaveModelCache(path, cache); err != nil {
		t.Fatal(err)
	}
	loaded := LoadModelCache(path)
	if got := loaded[anthropicCacheKey].Models; len(got) != 1 || got[0].ID != "claude-opus-5-5" {
		t.Fatalf("loaded %+v", loaded)
	}
	if !loaded.Fresh(anthropicCacheKey, now.Add(11*time.Hour), 12*time.Hour) {
		t.Fatal("11h old entry should be fresh")
	}
	if loaded.Fresh(anthropicCacheKey, now.Add(13*time.Hour), 12*time.Hour) {
		t.Fatal("13h old entry should be stale")
	}
	if loaded.Fresh("cfg.missing", now, 12*time.Hour) {
		t.Fatal("a missing entry is never fresh")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp*"))
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

func TestLoadModelCache_treats_garbage_as_empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allin-models.json")
	if err := os.WriteFile(path, []byte(`{"anthropic":{"models":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadModelCache(path); got == nil || len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	if got := LoadModelCache(""); got == nil || len(got) != 0 {
		t.Fatalf("empty path: %+v", got)
	}
}

func TestConfigCacheKey_matches_the_row_source(t *testing.T) {
	if got := configCacheKey("zhipu-glm.json"); got != "cfg.zhipu-glm" {
		t.Fatalf("got %q", got)
	}
}
