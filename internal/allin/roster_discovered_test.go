package allin

import (
	"testing"
	"time"
)

func seedCache(t *testing.T, env Env, cache ModelCache) {
	t.Helper()
	if err := SaveModelCache(ModelCachePath(env), cache); err != nil {
		t.Fatal(err)
	}
}

func TestRoster_offers_the_cached_claude_models_on_every_login(t *testing.T) {
	env := rosterEnv(t)
	seedCache(t, env, ModelCache{anthropicCacheKey: {FetchedAt: time.Now(), Models: []Listed{
		{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
		{ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"},
	}}})
	rows := Roster(env)
	got := models(rows)
	for _, want := range []string{
		"wisp/acct.default/claude-opus-5-5[1m]",
		"wisp/acct.personal/claude-opus-5-5[1m]",
		"wisp/acct.personal/claude-haiku-4-5-20251001[1m]",
	} {
		if !has(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if has(got, "wisp/acct.personal/claude-opus-5[1m]") {
		t.Fatal("the pinned lineup must not be used when a list is cached")
	}
	if label := labelFor(rows, "wisp/acct.personal/claude-opus-5-5[1m]"); label != "Personal · Opus 5.5" {
		t.Fatalf("label = %q", label)
	}
}

func TestRoster_falls_back_to_the_pinned_lineup_without_a_cache(t *testing.T) {
	env := rosterEnv(t)
	if !has(models(Roster(env)), "wisp/acct.personal/claude-opus-5-5[1m]") {
		t.Fatal("no cache must keep the pinned lineup")
	}
}

func TestRoster_offers_the_cached_provider_models(t *testing.T) {
	env := rosterEnv(t)
	seedCache(t, env, ModelCache{configCacheKey("zhipu-glm.json"): {FetchedAt: time.Now(), Models: listed("glm-6", "glm-5.3-flash")}})
	got := models(Roster(env))
	if !has(got, "wisp/cfg.zhipu-glm/glm-6") || has(got, "wisp/cfg.zhipu-glm/glm-5.2") {
		t.Fatalf("rows = %v", got)
	}
}

func TestRoster_floors_a_discovered_model_by_its_catalog_window(t *testing.T) {
	env := rosterEnv(t)
	seedCache(t, env, ModelCache{configCacheKey("zhipu-glm.json"): {FetchedAt: time.Now(), Models: []Listed{
		{ID: "glm-4.5-air"},              // catalog: 131072
		{ID: "glm-tiny", Context: 32768}, // listing's own window
		{ID: "glm-unknown"},              // no window anywhere: admitted
	}}})
	got := models(Roster(env))
	if has(got, "wisp/cfg.zhipu-glm/glm-4.5-air") || has(got, "wisp/cfg.zhipu-glm/glm-tiny") {
		t.Fatalf("a model under the floor was offered: %v", got)
	}
	if !has(got, "wisp/cfg.zhipu-glm/glm-unknown") {
		t.Fatalf("an unknown window must be admitted: %v", got)
	}
}
