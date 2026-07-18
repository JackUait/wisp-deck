package claudeconfig

import "strings"

// AuthKind describes how a subscription provider authenticates. API-key
// providers store ANTHROPIC_AUTH_TOKEN in their settings file; Codex ChatGPT
// providers delegate authentication to `codex login`.
type AuthKind string

const (
	AuthAPIKey       AuthKind = "api-key"
	AuthCodexChatGPT AuthKind = "codex-chatgpt"
)

// Model is one provider model with the metadata reused across the app: its id,
// API price (USD per 1,000,000 tokens), and OpenCode context / max-output token
// limits. Each model is defined exactly once here, so its id, price, and limits
// stay identical everywhere they appear (the mapping UI, the Stats cost calc, and
// the OpenCode mirror).
type Model struct {
	ID      string
	InPerM  float64 // input price, USD per 1M tokens
	OutPerM float64 // output price, USD per 1M tokens
	Context int     // max context tokens
	Output  int     // max output tokens
}

// Provider is one alternative-LLM gateway selectable as a subscription. Aliases
// are case-insensitive substrings of the config name that select this provider;
// the first provider in Providers is the default when a name matches none.
type Provider struct {
	Key            string
	Name           string
	Aliases        []string
	BaseURL        string
	Models         []Model
	DefaultModels  [4]string
	Auth           AuthKind
	MirrorOpenCode bool
}

// Providers is the single source of truth for subscription providers and their
// models. Prices are official vendor list prices (USD per 1M tokens); limits are
// context / max-output tokens from the vendors' docs and models.dev (June 2026).
// The first entry (zhipu) is the default for config names that match no provider,
// matching how unrecognized names already resolve to GLM models.
var Providers = []Provider{
	{
		Key:            "zhipu",
		Name:           "Zhipu / GLM",
		Aliases:        []string{"zhipu", "glm", "z.ai", "zai"},
		BaseURL:        "https://api.z.ai/api/anthropic",
		Auth:           AuthAPIKey,
		MirrorOpenCode: true,
		DefaultModels:  [4]string{"glm-4.7", "glm-4.7", "glm-4.5-air", "glm-4.5-air"},
		Models: []Model{
			{"glm-5.2", 1.40, 4.40, 1000000, 128000},
			{"glm-5.1", 1.40, 4.40, 202752, 128000},
			{"glm-5", 1.00, 3.20, 202752, 128000},
			{"glm-4.7", 0.60, 2.20, 200000, 128000},
			{"glm-4.6", 0.60, 2.20, 200000, 128000},
			{"glm-4.5-air", 0.20, 1.10, 131072, 96000},
		},
	},
	{
		Key:            "mimo",
		Name:           "Xiaomi MiMo",
		Aliases:        []string{"mimo", "xiaomi"},
		BaseURL:        "https://api.xiaomimimo.com/anthropic",
		Auth:           AuthAPIKey,
		MirrorOpenCode: true,
		DefaultModels:  [4]string{"mimo-v2.5-pro", "mimo-v2.5-pro", "mimo-v2.5", "mimo-v2.5"},
		Models: []Model{
			{"mimo-v2.5-pro", 0.435, 0.87, 1048576, 131072},
			{"mimo-v2.5", 0.14, 0.28, 1048576, 131072},
		},
	},
	{
		Key:            "openai-chatgpt",
		Name:           "OpenAI / ChatGPT",
		Aliases:        []string{"openai gpt", "chatgpt"},
		Auth:           AuthCodexChatGPT,
		MirrorOpenCode: false,
		DefaultModels:  [4]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-luna"},
		Models: []Model{
			{"gpt-5.6-sol", 0, 0, 272000, 0},
			{"gpt-5.6-terra", 0, 0, 272000, 0},
			{"gpt-5.6-luna", 0, 0, 272000, 0},
			{"gpt-5.5", 0, 0, 272000, 0},
			{"gpt-5.4", 0, 0, 272000, 0},
			{"gpt-5.4-mini", 0, 0, 272000, 0},
			{"gpt-5.3-codex-spark", 0, 0, 128000, 0},
		},
	},
}

// providerFor returns the provider whose alias appears in the config name, or the
// default (first) provider when none matches — so the model list, base URL, cost,
// and limits always resolve to the same provider. Providers and their aliases are
// checked in slice order, so resolution is deterministic.
func providerFor(configName string) Provider {
	lower := strings.ToLower(configName)
	for _, p := range Providers {
		for _, a := range p.Aliases {
			if strings.Contains(lower, a) {
				return p
			}
		}
	}
	return Providers[0]
}

// ProviderForName returns the provider selected by legacy display-name
// matching. Unknown names retain the historical Zhipu fallback.
func ProviderForName(configName string) Provider {
	return providerFor(configName)
}

func providerByKey(key string) (Provider, bool) {
	for _, provider := range Providers {
		if provider.Key == key {
			return provider, true
		}
	}
	return Provider{}, false
}

// ProviderByKey returns the provider with the exact catalog key.
func ProviderByKey(key string) (Provider, bool) {
	return providerByKey(key)
}

// modelByID returns the catalog Model with the given id, across all providers.
func modelByID(id string) (Model, bool) {
	for _, p := range Providers {
		for _, m := range p.Models {
			if m.ID == id {
				return m, true
			}
		}
	}
	return Model{}, false
}

// CatalogModels returns every model across all providers, in catalog order.
func CatalogModels() []Model {
	var out []Model
	for _, p := range Providers {
		out = append(out, p.Models...)
	}
	return out
}

// ModelCost returns the input/output price (USD per 1M tokens) for a catalog
// model id, and whether the id is in the catalog.
func ModelCost(id string) (inPerM, outPerM float64, ok bool) {
	m, found := modelByID(id)
	return m.InPerM, m.OutPerM, found
}

// ModelLimit returns the context and max-output token limits for a catalog model
// id, and whether the id is in the catalog with a non-zero limit.
func ModelLimit(id string) (context, output int, ok bool) {
	m, found := modelByID(id)
	if !found || (m.Context == 0 && m.Output == 0) {
		return 0, 0, false
	}
	return m.Context, m.Output, true
}
