package allin

import (
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

const modelListMaxAge = 12 * time.Hour

// ModelRefresher keeps allin-models.json current from inside the router.
type ModelRefresher struct {
	Env          Env
	Client       *http.Client
	CodexCache   string
	AnthropicURL string
	Now          func() time.Time
	Ensure       func(Env) error

	once sync.Once
}

// Observe starts one background refresh per process and returns at once. The
// header is the session's own, taken before the router swaps any credential.
func (r *ModelRefresher) Observe(sessionAuth http.Header) {
	r.once.Do(func() {
		auth := sessionAuth.Clone()
		go r.Refresh(auth)
	})
}

// Refresh fetches every stale source. A failed or empty fetch keeps the old
// entry. It rewrites the profile only when some list actually changed.
func (r *ModelRefresher) Refresh(sessionAuth http.Header) bool {
	path := ModelCachePath(r.Env)
	if path == "" {
		return false
	}
	now := r.Now()
	cache := LoadModelCache(path)
	fetched, changed := false, false
	store := func(key string, models []Listed, err error) {
		if err != nil || len(models) == 0 {
			return
		}
		reduced := Latest(models)
		if !reflect.DeepEqual(cache[key].Models, reduced) {
			changed = true
		}
		cache[key] = CacheEntry{FetchedAt: now, Models: reduced}
		fetched = true
	}

	if len(sessionAuth) > 0 && !cache.Fresh(anthropicCacheKey, now, modelListMaxAge) {
		listURL := r.AnthropicURL
		if listURL == "" {
			listURL = anthropicUpstream + "/v1/models"
		}
		models, err := FetchListing(r.Client, listURL, sessionAuth)
		store(anthropicCacheKey, models, err)
	}

	for _, rc := range routableConfigs(r.Env) {
		key := configCacheKey(rc.Config.File)
		if rc.Provider.SuppliesOwnModel() || cache.Fresh(key, now, modelListMaxAge) {
			continue
		}
		if rc.Provider.Auth == claudeconfig.AuthCodexChatGPT {
			models, err := ReadCodexModels(r.CodexCache)
			store(key, models, err)
			continue
		}
		listURL := rc.Provider.ModelsURL
		if listURL == "" {
			listURL = strings.TrimRight(claudeconfig.ReadBaseURL(r.Env.ConfigsDir, rc.Config.File), "/") + "/v1/models"
		}
		apiKey := claudeconfig.ReadAPIKey(r.Env.ConfigsDir, rc.Config.File)
		// Both headers, as the 2026-09-22 probe sent them: gateways differ in
		// which one they read.
		auth := http.Header{"Authorization": {"Bearer " + apiKey}, "X-Api-Key": {apiKey}}
		models, err := FetchListing(r.Client, listURL, auth)
		store(key, models, err)
	}

	if fetched {
		_ = SaveModelCache(path, cache)
	}
	if changed && r.Ensure != nil {
		_ = r.Ensure(r.Env)
	}
	return changed
}
