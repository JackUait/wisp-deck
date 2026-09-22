package claudeconfig

import "testing"

// Figures are DeepSeek's published ones (api-docs.deepseek.com, 2026-09-10),
// not measured: a 1M window, "384K" max output, and the PEAK cache-miss input
// and output list prices. Off-peak is documented as exactly half of these.
func TestDeepSeekCatalog_pointsAtTheAnthropicRoute(t *testing.T) {
	provider, ok := ProviderByKey("deepseek")
	if !ok {
		t.Fatal("catalog is missing the deepseek provider")
	}
	if provider.BaseURL != "https://api.deepseek.com/anthropic" {
		t.Errorf("BaseURL = %q", provider.BaseURL)
	}
	if provider.Auth != AuthAPIKey || !provider.MirrorOpenCode {
		t.Errorf("Auth = %q, MirrorOpenCode = %v", provider.Auth, provider.MirrorOpenCode)
	}

	byID := make(map[string]Model, len(provider.Models))
	for _, m := range provider.Models {
		byID[m.ID] = m
	}
	for _, want := range []Model{
		{"deepseek-flash", 0.30, 1.20, 1000000, 384000},
		{"deepseek-v4-pro", 1.32, 3.96, 1000000, 384000},
	} {
		if got := byID[want.ID]; got != want {
			t.Errorf("deepseek model %q = %+v, want %+v", want.ID, got, want)
		}
	}

	// V4 Pro has no vision and is routed to Flash from 2026-09-14, so no alias
	// may default to it.
	want := [4]string{"deepseek-flash", "deepseek-flash", "deepseek-flash", "deepseek-flash"}
	if provider.DefaultModels != want {
		t.Errorf("deepseek defaults = %v, want %v", provider.DefaultModels, want)
	}
}

func TestDeepSeekCatalog_nameResolution(t *testing.T) {
	for name, want := range map[string]string{
		"DeepSeek": "deepseek",
		// Featherless profiles are named after the model they run.
		"Featherless DeepSeek-V4": "featherless",
	} {
		if got := ProviderForName(name).Key; got != want {
			t.Errorf("ProviderForName(%q) = %q, want %q", name, got, want)
		}
	}
}

// Measured against the live endpoint on 2026-09-10, not published: DeepSeek
// validates every tool's `pattern` and 400s the whole turn on the Artifact
// tool's, naming it ("is not a \"regex\""). The construct it refuses is a bare
// `[` inside a character class — `^[^[]$` alone reproduces it, and the same
// Artifact pattern with that one bracket escaped answers 200. That is a
// DIFFERENT construct from the one z.ai refuses: the `\p{...}` escapes and the
// negative lookahead both pass here.
func TestDeepSeekProvider_declaresTheToolItsEndpointRejects(t *testing.T) {
	provider, ok := ProviderByKey("deepseek")
	if !ok {
		t.Fatal("catalog is missing the deepseek provider")
	}
	if got := provider.UnsupportedTools; len(got) != 1 || got[0] != "Artifact" {
		t.Errorf("UnsupportedTools = %v, want [Artifact]", got)
	}
}

func TestDeepSeekProvider_lists_models_off_its_openai_host(t *testing.T) {
	p, ok := ProviderByKey("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	// Its /anthropic base answers /v1/models with 404; the list lives here.
	if p.ModelsURL != "https://api.deepseek.com/models" {
		t.Fatalf("ModelsURL = %q", p.ModelsURL)
	}
}
