package featherless

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleModels() []Model {
	return []Model{
		{ID: "moonshotai/Kimi-K3", Class: "kimi3-2780b", Context: 262144, InPerM: 3, OutPerM: 15, ImageInput: true, OnPlan: true, Created: 1785207017},
		{ID: "zai-org/GLM-5.2", Class: "glm52-753b", Context: 262144, InPerM: 0.75, OutPerM: 2.4, OnPlan: true, Created: 1780000000},
	}
}

func TestCache_round_trips_every_field_the_pick_writes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "featherless-models.json")
	if err := SaveCache(path, sampleModels()); err != nil {
		t.Fatal(err)
	}
	models, fetchedAt, ok := LoadCache(path)
	if !ok {
		t.Fatal("cache did not load back")
	}
	if len(models) != 2 || models[0] != sampleModels()[0] || models[1] != sampleModels()[1] {
		t.Errorf("round trip changed the models: %+v", models)
	}
	if time.Since(fetchedAt) > time.Minute {
		t.Errorf("fetchedAt = %v, want ~now", fetchedAt)
	}
}

func TestLoadCache_reports_a_missing_or_unreadable_cache(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := LoadCache(filepath.Join(dir, "absent.json")); ok {
		t.Error("a missing cache must not report ok")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := LoadCache(bad); ok {
		t.Error("an unparseable cache must not report ok")
	}
}

// A cache written by a build with a different Model shape must be refetched
// rather than decoded into zero values, which would show a picker full of
// nameless 0-token models.
func TestLoadCache_rejects_another_versions_cache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "featherless-models.json")
	if err := os.WriteFile(path, []byte(`{"version":999,"fetched_at":0,"models":[{"id":"a"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := LoadCache(path); ok {
		t.Error("a cache from another version must not load")
	}
}

func TestStale_expires_after_the_ttl(t *testing.T) {
	if Stale(time.Now()) {
		t.Error("a fresh cache is not stale")
	}
	if !Stale(time.Now().Add(-CacheTTL - time.Minute)) {
		t.Error("a cache older than the TTL is stale")
	}
}

// The picker resolves this once; the load/save functions themselves take an
// explicit path so they never read the environment.
func TestDefaultCachePath_sits_beside_the_other_wisp_deck_config(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	want := filepath.Join(dir, "wisp-deck", "featherless-models.json")
	if got := DefaultCachePath(); got != want {
		t.Errorf("DefaultCachePath() = %q, want %q", got, want)
	}
}
