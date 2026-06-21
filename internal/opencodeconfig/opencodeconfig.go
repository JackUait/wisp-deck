// Package opencodeconfig mirrors Ghost Tab subscriptions (custom Claude configs)
// into OpenCode's global config as custom providers. MergeSubscriptions is the
// pure core; Sync (sync.go) wires it to disk.
package opencodeconfig

import (
	"encoding/json"
	"strings"

	"github.com/jackuait/ghost-tab/internal/claudeconfig"
)

// ProviderPrefix namespaces every provider Ghost Tab owns in opencode.json, so a
// rebuild can remove and re-add only these without touching user-authored ones.
const ProviderPrefix = "ghost-tab-"

const schemaURL = "https://opencode.ai/config.json"

// modelEntry builds the OpenCode model object for a provider model id, enriching
// it with cost and context/output limits from the shared claudeconfig catalog —
// the single source for these values. Custom-provider models aren't in models.dev,
// so without this OpenCode shows $0 cost and an unknown context size. Both fields
// are best-effort: a model not in the catalog gets name only.
func modelEntry(id string) map[string]any {
	entry := map[string]any{"name": id}
	if in, out, ok := claudeconfig.ModelCost(id); ok {
		entry["cost"] = map[string]any{"input": in, "output": out}
	}
	if ctx, out, ok := claudeconfig.ModelLimit(id); ok {
		entry["limit"] = map[string]any{"context": ctx, "output": out}
	}
	return entry
}

// Subscription is the resolved view of a Ghost Tab subscription that OpenCode
// needs. BaseURL is pre-resolved by the caller (empty -> not mirrorable).
type Subscription struct {
	Name      string
	File      string
	APIKey    string
	BaseURL   string
	OpusModel string
	Models    []string
	Active    bool
}

// providerID returns the namespaced OpenCode provider id for a subscription.
func (s Subscription) providerID() string {
	return ProviderPrefix + claudeconfig.Slugify(strings.TrimSuffix(s.File, ".json"))
}

// mirrorable reports whether the subscription has everything a working OpenCode
// provider needs.
func (s Subscription) mirrorable() bool {
	return s.APIKey != "" && s.BaseURL != "" && len(s.Models) > 0
}

// defaultModel returns the "<providerID>/<model>" string for the active provider.
func (s Subscription) defaultModel() string {
	model := s.OpusModel
	if model == "" {
		model = s.Models[0]
	}
	return s.providerID() + "/" + model
}

// MergeSubscriptions rebuilds the ghost-tab-* providers in existing opencode.json
// bytes from subs, preserving every other key. Returns (nil, false) when existing
// is non-empty but not valid JSON (e.g. JSONC), meaning the caller must not write.
func MergeSubscriptions(existing []byte, subs []Subscription) ([]byte, bool) {
	m := map[string]any{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &m); err != nil {
			return nil, false
		}
	}

	m["$schema"] = schemaURL

	provider, _ := m["provider"].(map[string]any)
	if provider == nil {
		provider = map[string]any{}
	}
	// Drop every provider we own; user-authored ones stay.
	for id := range provider {
		if strings.HasPrefix(id, ProviderPrefix) {
			delete(provider, id)
		}
	}

	// Clear the model key if it currently points to a ghost-tab provider; it
	// will be re-set below if an active subscription is still mirrorable.
	if current, ok := m["model"].(string); ok && strings.HasPrefix(current, ProviderPrefix) {
		delete(m, "model")
	}

	for _, s := range subs {
		if !s.mirrorable() {
			continue
		}
		models := map[string]any{}
		for _, id := range s.Models {
			models[id] = modelEntry(id)
		}
		provider[s.providerID()] = map[string]any{
			"npm":  "@ai-sdk/anthropic",
			"name": s.Name,
			"options": map[string]any{
				"baseURL": s.BaseURL,
				"apiKey":  s.APIKey,
			},
			"models": models,
		}
		if s.Active {
			m["model"] = s.defaultModel()
		}
	}

	if len(provider) == 0 {
		delete(m, "provider")
	} else {
		m["provider"] = provider
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, false
	}
	return append(out, '\n'), true
}
