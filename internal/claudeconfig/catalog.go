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
	// UserConfigured marks a provider that ships no endpoint and no model
	// catalog: the user supplies both. The modal offers text fields for them
	// instead of the model cycler, which is inert with an empty model list.
	UserConfigured bool
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
		DefaultModels:  [4]string{"gpt-5.6-terra", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-sol"},
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
	{
		// Moonshot's flat-rate Kimi For Coding subscription is a different
		// service from the open platform below: a different host, a different
		// model namespace, and credentials (sk-kimi-…) each gateway rejects
		// from the other with a bare 401. It is listed before "moonshot" so the
		// bare "kimi" alias cannot claim a coding profile that has no explicit
		// marker and strand it on the gateway that cannot authenticate it.
		Key:            "moonshot-coding",
		Name:           "Kimi For Coding",
		Aliases:        []string{"kimi for coding", "kimi coding", "kimi-coding"},
		BaseURL:        "https://api.kimi.com/coding",
		Auth:           AuthAPIKey,
		MirrorOpenCode: true,
		DefaultModels:  [4]string{"k3", "kimi-for-coding", "kimi-for-coding", "k3"},
		// Ids and context windows are the gateway's own /v1/models listing; all
		// four report a 262144 context. Prices are zero because the plan is a
		// flat-rate subscription rather than metered per token — pricing.go's
		// catalog fold skips zero-priced models instead of publishing a false
		// $0 rate. Max output is the documented default of each model's
		// underlying family (k3: 131072, k2.7 coding: 32768) and must stay
		// non-zero: it reaches OpenCode's limit.output verbatim.
		Models: []Model{
			{"k3", 0, 0, 262144, 131072},
			{"k3-256k", 0, 0, 262144, 131072},
			{"kimi-for-coding", 0, 0, 262144, 32768},
			{"kimi-for-coding-highspeed", 0, 0, 262144, 32768},
		},
	},
	{
		Key:            "moonshot",
		Name:           "Moonshot Kimi",
		Aliases:        []string{"moonshot", "kimi"},
		BaseURL:        "https://api.moonshot.ai/anthropic",
		Auth:           AuthAPIKey,
		MirrorOpenCode: true,
		// Haiku maps to the sonnet workhorse rather than -highspeed: Claude Code
		// routes background work to haiku, and highspeed costs exactly 2x per
		// token, which would invert the cost gradient every other provider keeps.
		DefaultModels: [4]string{"kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code", "kimi-k3"},
		// Prices are Moonshot's official cache-miss input / output list prices
		// (platform.kimi.ai/docs/pricing). k2.6 and k2.7-code match the rates
		// already hand-maintained in usage/pricing.go, so init()'s catalog fold
		// is a no-op for them rather than a silent repricing of past usage.
		// Max-output must stay non-zero: these mirror into OpenCode's
		// limit.output verbatim (ModelLimit only gates on Context).
		Models: []Model{
			// k3's max output is the documented 131072 default. The docs also
			// mention 1048576 as a settable maximum, but that is single-sourced
			// and equal to the whole context window, so the default is used.
			{"kimi-k3", 3, 15, 1048576, 131072},
			// The k2.x quickstarts document max_tokens "Default to be 32k aka
			// 32768" and a shared 256K context for all three.
			{"kimi-k2.7-code", 0.95, 4, 262144, 32768},
			{"kimi-k2.7-code-highspeed", 1.9, 8, 262144, 32768},
			{"kimi-k2.6", 0.95, 4, 262144, 32768},
		},
	},
	{
		// Any endpoint speaking the Anthropic Messages API: a self-hosted
		// vLLM/LiteLLM stack, a company gateway, an SSH tunnel. It ships no
		// BaseURL and no Models because both are the user's to supply, and it
		// is LAST so it can never become the unknown-name fallback that
		// Providers[0] serves. MirrorOpenCode stays off: OpenCode's catalog
		// cannot size a model nobody has published.
		Key:            "custom",
		Name:           "Custom / self-hosted",
		Aliases:        []string{"custom", "self-hosted", "selfhosted"},
		Auth:           AuthAPIKey,
		MirrorOpenCode: false,
		UserConfigured: true,
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
