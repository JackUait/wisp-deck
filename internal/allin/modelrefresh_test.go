package allin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func listServer(t *testing.T, body string, hits *int32, sawAuth *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if sawAuth != nil {
			*sawAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// pointZhipuAt redirects rosterEnv's zhipu profile, which names the real z.ai.
func pointZhipuAt(t *testing.T, env Env, base string) {
	t.Helper()
	body := `{"env":{"ANTHROPIC_BASE_URL":"` + base + `","ANTHROPIC_AUTH_TOKEN":"zkey"}}`
	if err := os.WriteFile(filepath.Join(env.ConfigsDir, "zhipu-glm.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRefresh_fetches_stale_sources_and_rewrites_the_profile(t *testing.T) {
	env := rosterEnv(t)
	var anthropicHits, zhipuHits, ensures int32
	var sawAnthropicAuth, sawZhipuAuth string
	anthropic := listServer(t, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"},{"id":"claude-opus-5"}]}`, &anthropicHits, &sawAnthropicAuth)
	zhipu := listServer(t, `{"data":[{"id":"glm-5.3"},{"id":"glm-6"}]}`, &zhipuHits, &sawZhipuAuth)
	pointZhipuAt(t, env, zhipu.URL)

	r := &ModelRefresher{
		Env: env, Client: http.DefaultClient, AnthropicURL: anthropic.URL,
		Now:    func() time.Time { return time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC) },
		Ensure: func(Env) error { atomic.AddInt32(&ensures, 1); return nil },
	}
	if !r.Refresh(http.Header{"Authorization": {"Bearer session"}}) {
		t.Fatal("want changed")
	}
	cache := LoadModelCache(ModelCachePath(env))
	if got := ids(cache[anthropicCacheKey].Models); len(got) != 1 || got[0] != "claude-opus-5-5" {
		t.Fatalf("anthropic entry = %v", got)
	}
	if got := ids(cache[configCacheKey("zhipu-glm.json")].Models); len(got) != 1 || got[0] != "glm-6" {
		t.Fatalf("zhipu entry = %v", got)
	}
	if sawAnthropicAuth != "Bearer session" || sawZhipuAuth != "Bearer zkey" || ensures != 1 {
		t.Fatalf("anthropic auth=%q zhipu auth=%q ensures=%d", sawAnthropicAuth, sawZhipuAuth, ensures)
	}

	// Fresh now: a second refresh fetches nothing and rewrites nothing.
	if r.Refresh(http.Header{"Authorization": {"Bearer session"}}) {
		t.Fatal("a fresh cache must not change")
	}
	if anthropicHits != 1 || zhipuHits != 1 || ensures != 1 {
		t.Fatalf("hits anthropic=%d zhipu=%d ensures=%d", anthropicHits, zhipuHits, ensures)
	}
}

func TestRefresh_keeps_the_old_entry_when_a_listing_is_empty(t *testing.T) {
	env := rosterEnv(t)
	var hits int32
	empty := listServer(t, `{"data":[]}`, &hits, nil)
	pointZhipuAt(t, env, empty.URL)
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seedCache(t, env, ModelCache{configCacheKey("zhipu-glm.json"): {FetchedAt: old, Models: listed("glm-5.3")}})

	r := &ModelRefresher{Env: env, Client: http.DefaultClient, AnthropicURL: empty.URL,
		Now: func() time.Time { return old.Add(48 * time.Hour) }, Ensure: func(Env) error { return nil }}
	r.Refresh(nil)
	if hits == 0 {
		t.Fatal("the stale entry was never re-fetched")
	}
	if got := ids(LoadModelCache(ModelCachePath(env))[configCacheKey("zhipu-glm.json")].Models); len(got) != 1 || got[0] != "glm-5.3" {
		t.Fatalf("entry = %v", got)
	}
}

func TestRefresh_skips_anthropic_without_session_auth(t *testing.T) {
	env := rosterEnv(t)
	var hits, zhipuHits int32
	anthropic := listServer(t, `{"data":[{"id":"claude-opus-5-5"}]}`, &hits, nil)
	pointZhipuAt(t, env, listServer(t, `{"data":[]}`, &zhipuHits, nil).URL)
	r := &ModelRefresher{Env: env, Client: http.DefaultClient, AnthropicURL: anthropic.URL,
		Now: time.Now, Ensure: func(Env) error { return nil }}
	r.Refresh(nil)
	if hits != 0 {
		t.Fatalf("anthropic fetched %d times with no session credential", hits)
	}
}

func TestRefresh_reads_chatgpt_from_the_codex_cache(t *testing.T) {
	env := rosterEnv(t)
	if err := os.WriteFile(env.ConfigsList, []byte("OpenAI / ChatGPT:openai-chatgpt.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`
	if err := os.WriteFile(filepath.Join(env.ConfigsDir, "openai-chatgpt.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(t.TempDir(), "models_cache.json")
	if err := os.WriteFile(codex, []byte(`{"models":[{"slug":"gpt-7","visibility":"list","context_window":272000}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &ModelRefresher{Env: env, Client: http.DefaultClient, CodexCache: codex,
		Now: time.Now, Ensure: func(Env) error { return nil }}
	r.Refresh(nil)
	if got := ids(LoadModelCache(ModelCachePath(env))[configCacheKey("openai-chatgpt.json")].Models); len(got) != 1 || got[0] != "gpt-7" {
		t.Fatalf("chatgpt entry = %v", got)
	}
}

func TestObserve_refreshes_once_and_never_blocks(t *testing.T) {
	env := rosterEnv(t)
	release := make(chan struct{})
	var hits int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-5-5"}]}`))
	}))
	defer slow.Close()
	pointZhipuAt(t, env, slow.URL)
	finished := make(chan struct{})
	r := &ModelRefresher{Env: env, Client: http.DefaultClient, AnthropicURL: slow.URL,
		Now: time.Now, Ensure: func(Env) error { close(finished); return nil }}

	done := make(chan struct{})
	go func() {
		r.Observe(http.Header{"Authorization": {"Bearer s"}})
		r.Observe(http.Header{"Authorization": {"Bearer s"}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Observe blocked on the fetch")
	}
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&hits) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("hits = %d; one refresh reaches its first fetch", got)
	}
	// Let the refresh finish before TempDir cleanup removes its files.
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the background refresh never finished")
	}
}

// A cached time comes back from JSON in a different Location than a freshly
// parsed one, so an unchanged list must not read as changed.
func TestRefresh_does_not_rewrite_the_profile_for_an_unchanged_list(t *testing.T) {
	env := rosterEnv(t)
	var hits, ensures int32
	zhipu := listServer(t, `{"data":[{"id":"glm-6","created":1786636800,"context_length":1000000}]}`, &hits, nil)
	pointZhipuAt(t, env, zhipu.URL)
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seedCache(t, env, ModelCache{configCacheKey("zhipu-glm.json"): {FetchedAt: old, Models: []Listed{
		{ID: "glm-6", Created: time.Unix(1786636800, 0).UTC(), Context: 1000000},
	}}})

	r := &ModelRefresher{Env: env, Client: http.DefaultClient,
		Now:    func() time.Time { return old.Add(48 * time.Hour) },
		Ensure: func(Env) error { atomic.AddInt32(&ensures, 1); return nil }}
	if r.Refresh(nil) || ensures != 0 {
		t.Fatalf("an unchanged list rewrote the profile (ensures=%d)", ensures)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
}
