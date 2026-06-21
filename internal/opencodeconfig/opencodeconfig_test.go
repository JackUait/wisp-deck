package opencodeconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func parse(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, b)
	}
	return m
}

func glmSub(active bool) Subscription {
	return Subscription{
		Name:      "Work GLM",
		File:      "work-glm.json",
		APIKey:    "sk-test-123",
		BaseURL:   "https://api.z.ai/api/anthropic",
		OpusModel: "glm-4.6",
		Models:    []string{"glm-4.6", "glm-4.5-air"},
		Active:    active,
	}
}

func TestMerge_creates_provider_from_empty(t *testing.T) {
	out, ok := MergeSubscriptions(nil, []Subscription{glmSub(true)})
	if !ok {
		t.Fatal("expected ok=true")
	}
	m := parse(t, out)
	if m["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("missing/wrong $schema: %v", m["$schema"])
	}
	if m["model"] != "ghost-tab-work-glm/glm-4.6" {
		t.Errorf("model = %v, want ghost-tab-work-glm/glm-4.6", m["model"])
	}
	prov := m["provider"].(map[string]any)["ghost-tab-work-glm"].(map[string]any)
	if prov["npm"] != "@ai-sdk/anthropic" {
		t.Errorf("npm = %v", prov["npm"])
	}
	if prov["name"] != "Work GLM" {
		t.Errorf("name = %v", prov["name"])
	}
	opts := prov["options"].(map[string]any)
	if opts["baseURL"] != "https://api.z.ai/api/anthropic" || opts["apiKey"] != "sk-test-123" {
		t.Errorf("options = %v", opts)
	}
	models := prov["models"].(map[string]any)
	if _, ok := models["glm-4.6"]; !ok {
		t.Errorf("models missing glm-4.6: %v", models)
	}
	if _, ok := models["glm-4.5-air"]; !ok {
		t.Errorf("models missing glm-4.5-air: %v", models)
	}
}

func TestMerge_preserves_user_keys_and_providers(t *testing.T) {
	existing := []byte(`{
  "theme": "tokyonight",
  "model": "anthropic/claude-sonnet-4-5",
  "provider": { "myown": { "name": "Mine" } }
}`)
	out, ok := MergeSubscriptions(existing, []Subscription{glmSub(false)})
	if !ok {
		t.Fatal("expected ok=true")
	}
	m := parse(t, out)
	if m["theme"] != "tokyonight" {
		t.Errorf("theme lost: %v", m["theme"])
	}
	// No active subscription -> top-level model must be left untouched.
	if m["model"] != "anthropic/claude-sonnet-4-5" {
		t.Errorf("model changed: %v", m["model"])
	}
	prov := m["provider"].(map[string]any)
	if _, ok := prov["myown"]; !ok {
		t.Errorf("user provider 'myown' was dropped: %v", prov)
	}
	if _, ok := prov["ghost-tab-work-glm"]; !ok {
		t.Errorf("ghost-tab provider not added: %v", prov)
	}
}

func TestMerge_rebuild_removes_stale_ghost_tab_providers(t *testing.T) {
	existing := []byte(`{"provider":{"ghost-tab-old":{"name":"Old"},"keep":{"name":"Keep"}}}`)
	out, ok := MergeSubscriptions(existing, []Subscription{glmSub(true)})
	if !ok {
		t.Fatal("expected ok=true")
	}
	prov := parse(t, out)["provider"].(map[string]any)
	if _, ok := prov["ghost-tab-old"]; ok {
		t.Errorf("stale ghost-tab-old not removed: %v", prov)
	}
	if _, ok := prov["keep"]; !ok {
		t.Errorf("user provider 'keep' was dropped: %v", prov)
	}
	if _, ok := prov["ghost-tab-work-glm"]; !ok {
		t.Errorf("current ghost-tab provider missing: %v", prov)
	}
}

func TestMerge_delete_removes_provider(t *testing.T) {
	existing := []byte(`{"provider":{"ghost-tab-work-glm":{"name":"Work GLM"}}}`)
	out, ok := MergeSubscriptions(existing, nil)
	if !ok {
		t.Fatal("expected ok=true")
	}
	m := parse(t, out)
	if p, ok := m["provider"]; ok {
		if pm, _ := p.(map[string]any); len(pm) != 0 {
			t.Errorf("provider should be empty/absent, got: %v", pm)
		}
	}
}

func TestMerge_skips_non_mirrorable(t *testing.T) {
	noKey := glmSub(true)
	noKey.APIKey = ""
	noURL := glmSub(true)
	noURL.BaseURL = ""
	noModels := glmSub(true)
	noModels.Models = nil
	out, ok := MergeSubscriptions(nil, []Subscription{noKey, noURL, noModels})
	if !ok {
		t.Fatal("expected ok=true")
	}
	m := parse(t, out)
	if p, ok := m["provider"]; ok {
		if pm, _ := p.(map[string]any); len(pm) != 0 {
			t.Errorf("no provider should be written, got: %v", pm)
		}
	}
	if _, ok := m["model"]; ok {
		t.Errorf("no default model should be set, got: %v", m["model"])
	}
}

func TestMerge_returns_false_on_jsonc_comments(t *testing.T) {
	existing := []byte("{\n  // a comment\n  \"theme\": \"x\"\n}")
	out, ok := MergeSubscriptions(existing, []Subscription{glmSub(true)})
	if ok || out != nil {
		t.Errorf("expected (nil,false) for JSONC input, got ok=%v out=%s", ok, out)
	}
}

func TestMerge_default_model_falls_back_to_first_model(t *testing.T) {
	s := glmSub(true)
	s.OpusModel = "" // no opus slot mapped
	out, _ := MergeSubscriptions(nil, []Subscription{s})
	if got := parse(t, out)["model"]; got != "ghost-tab-work-glm/glm-4.6" {
		t.Errorf("fallback model = %v, want ghost-tab-work-glm/glm-4.6", got)
	}
}

func TestProviderPrefixConst(t *testing.T) {
	if !strings.HasPrefix("ghost-tab-work-glm", ProviderPrefix) {
		t.Errorf("ProviderPrefix = %q", ProviderPrefix)
	}
}

// modelsOf returns the models map of the single ghost-tab provider in out.
func modelsOf(t *testing.T, out []byte) map[string]any {
	t.Helper()
	m := parse(t, out)
	prov := m["provider"].(map[string]any)["ghost-tab-work-glm"].(map[string]any)
	return prov["models"].(map[string]any)
}

func TestMerge_enriches_known_model_with_cost_and_limit(t *testing.T) {
	out, ok := MergeSubscriptions(nil, []Subscription{glmSub(true)})
	if !ok {
		t.Fatal("expected ok=true")
	}
	g46 := modelsOf(t, out)["glm-4.6"].(map[string]any)
	cost, ok := g46["cost"].(map[string]any)
	if !ok {
		t.Fatalf("glm-4.6 has no cost: %v", g46)
	}
	// JSON round-trip yields float64 for all numbers.
	if cost["input"].(float64) != 0.6 || cost["output"].(float64) != 2.2 {
		t.Errorf("glm-4.6 cost = %v, want input 0.6 / output 2.2", cost)
	}
	lim, ok := g46["limit"].(map[string]any)
	if !ok {
		t.Fatalf("glm-4.6 has no limit: %v", g46)
	}
	if lim["context"].(float64) != 200000 || lim["output"].(float64) != 128000 {
		t.Errorf("glm-4.6 limit = %v, want context 200000 / output 128000", lim)
	}
}

func TestMerge_unknown_model_has_name_only(t *testing.T) {
	s := glmSub(true)
	s.Models = []string{"mimo-v2-omni"} // valid provider model, but no cost/limit data
	s.OpusModel = "mimo-v2-omni"
	out, ok := MergeSubscriptions(nil, []Subscription{s})
	if !ok {
		t.Fatal("expected ok=true")
	}
	e := modelsOf(t, out)["mimo-v2-omni"].(map[string]any)
	if e["name"] != "mimo-v2-omni" {
		t.Errorf("name = %v", e["name"])
	}
	if _, ok := e["cost"]; ok {
		t.Errorf("unexpected cost for unpriced model: %v", e)
	}
	if _, ok := e["limit"]; ok {
		t.Errorf("unexpected limit for unknown-limit model: %v", e)
	}
}
