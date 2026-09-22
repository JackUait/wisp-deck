# All-In Latest Models Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the All-In picker offer each source's newest model per family, discovered from live model lists, with no code change when a new model ships.

**Architecture:** A pure family rule (`Latest`) reduces a listing. Fetchers read Anthropic-shaped or OpenAI-shaped `/models` endpoints and Codex's local model cache. Results go into a cache file. `Roster` reads only that cache and falls back to today's static lists. The All-In router refreshes stale cache entries in the background, once per process, using the session's own Claude token, then rewrites the profile.

**Tech Stack:** Go 1.25, `net/http`, `net/http/httptest`, standard `testing`.

**Spec:** `docs/superpowers/specs/2026-09-22-allin-latest-models-design.md`

## Global Constraints

- `Roster` must perform no network I/O.
- The refresh must never block a request and must run at most once per router process.
- Staleness: an entry older than 12h (`12 * time.Hour`), or missing, is refreshed.
- A failed or empty fetch keeps that source's previous entry. It never empties the picker.
- The Claude listing uses the session's captured `Authorization` / `X-Api-Key`, and only when the session upstream host is `api.anthropic.com`. There is no Keychain read.
- The cache file is `allin-models.json` in `filepath.Dir(env.ConfigsList)`, written atomically (temp file + rename).
- Anthropic request header: `anthropic-version: 2023-06-01`.
- DeepSeek list URL: `https://api.deepseek.com/models`. Every other API-key provider: `<profile ANTHROPIC_BASE_URL>/v1/models`.
- ChatGPT: `~/.codex/models_cache.json`, rows with `visibility == "list"` only.
- `SuppliesOwnModel()` providers (custom, Featherless) are not discovered.
- The production wiring runs only when `currentHostEffectsDecision().Allowed`.
- Tests: run only the new/changed test files' tests (`go test ./internal/allin/ -run '<names>'`). Never a bare `go test ./...`.
- Lint only changed files: `gofmt -l <files>` and `go vet ./internal/allin/ ./internal/claudeconfig/ ./cmd/wisp-deck-tui/`.
- Comments: short, only for non-obvious constraints.
- Commit on `main`. Stage only your own files, because other sessions share this checkout. End each commit message with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.

## Review Focus

1. **A listing with ids the family rule has never seen** (e.g. `gpt-4o`, `o3`, `claude-3-7-sonnet-latest`). Each should form its own family and be kept, never dropped or panicked on. Test: Task 1, `TestLatest_keeps_unfamiliar_ids`.
2. **A provider that answers 200 with an empty `data`.** It must not replace a good cached list with nothing. Test: Task 5, `TestRefresh_keeps_the_old_entry_when_a_listing_is_empty`.
3. **A corrupt or half-written cache file.** The roster must fall back to static lists, not lose rows. Test: Task 3, `TestLoadModelCache_treats_garbage_as_empty`.
4. **A session whose upstream is not Anthropic.** Its credential must never be sent to `api.anthropic.com`. Test: Task 6, `TestHandler_passes_no_session_auth_for_a_non_anthropic_upstream`.
5. **A discovered model below the 200k floor, known only through the catalog.** It must still be filtered out. Test: Task 4, `TestRoster_floors_a_discovered_model_by_its_catalog_window`.

---

### Task 1: The family rule

**Files:**
- Create: `internal/allin/latest.go`
- Test: `internal/allin/latest_test.go`

**Interfaces:**
- Produces:
  ```go
  type Listed struct {
      ID      string    `json:"id"`
      Label   string    `json:"label,omitempty"`
      Created time.Time `json:"created,omitempty"`
      Context int       `json:"context,omitempty"`
  }
  func Latest(models []Listed) []Listed
  ```

- [ ] **Step 1: Write the failing test**

```go
package allin

import (
	"reflect"
	"testing"
	"time"
)

func ids(models []Listed) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func listed(idList ...string) []Listed {
	out := make([]Listed, 0, len(idList))
	for _, id := range idList {
		out = append(out, Listed{ID: id})
	}
	return out
}

func TestLatest_picks_the_newest_per_family(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"anthropic 2026-09-22", []string{
			"claude-opus-5-5", "claude-fable-5-1", "claude-opus-5", "claude-sonnet-5",
			"claude-fable-5", "claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4-6",
			"claude-opus-4-6", "claude-opus-4-5-20251101", "claude-haiku-4-5-20251001",
			"claude-sonnet-4-5-20250929",
		}, []string{"claude-opus-5-5", "claude-fable-5-1", "claude-sonnet-5", "claude-haiku-4-5-20251001"}},
		{"zhipu 2026-09-22", []string{
			"glm-4.5", "glm-4.5-air", "glm-4.6", "glm-4.7", "glm-5", "glm-5-turbo",
			"glm-5.1", "glm-5.2", "glm-5.3", "glm-5.3-flash", "glm-5.3-flashx",
		}, []string{"glm-5.3", "glm-4.5-air", "glm-5-turbo", "glm-5.3-flash", "glm-5.3-flashx"}},
		{"deepseek", []string{"deepseek-flash", "deepseek-v4-pro"},
			[]string{"deepseek-flash", "deepseek-v4-pro"}},
		{"kimi coding, equal dates", []string{"kimi-for-coding", "kimi-for-coding-highspeed", "k3", "k3-256k"},
			[]string{"kimi-for-coding", "kimi-for-coding-highspeed", "k3", "k3-256k"}},
		{"codex", []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"},
			[]string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ids(Latest(listed(c.in...))); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Latest = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLatest_breaks_a_version_tie_on_the_date_suffix_then_created(t *testing.T) {
	got := ids(Latest(listed("claude-haiku-4-5-20250101", "claude-haiku-4-5-20251001")))
	if !reflect.DeepEqual(got, []string{"claude-haiku-4-5-20251001"}) {
		t.Fatalf("date tiebreak: %v", got)
	}
	older := Listed{ID: "kimi-for-coding", Created: time.Unix(100, 0)}
	newer := Listed{ID: "kimi-for-coding", Label: "newer", Created: time.Unix(200, 0)}
	if got := Latest([]Listed{older, newer}); len(got) != 1 || got[0].Label != "newer" {
		t.Fatalf("created tiebreak: %+v", got)
	}
}

func TestLatest_keeps_unfamiliar_ids(t *testing.T) {
	in := []string{"gpt-4o", "o3", "claude-3-7-sonnet-latest", "", "weird--id"}
	got := ids(Latest(listed(in...)))
	want := []string{"gpt-4o", "o3", "claude-3-7-sonnet-latest", "weird--id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Latest = %v, want %v", got, want)
	}
}
```

Note on the Zhipu expectation: output keeps first-seen family order. `glm-4.5` opens family `glm` and `glm-4.5-air` opens `glm-air`, so `glm-air` sits second even though its only member is old. The 200k floor removes it later (Task 4).

Note on `claude-3-7-sonnet-latest`: its family is `claude-sonnet-latest` (versions 3,7), alone in this list, so it is kept. `o3` has a one-letter prefix, so its family is `o`. An empty id is dropped.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/allin/ -run 'TestLatest_' -v`
Expected: FAIL to compile, `undefined: Listed` / `undefined: Latest`.

- [ ] **Step 3: Write minimal implementation**

```go
package allin

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Listed is one model a source's live list reports.
type Listed struct {
	ID      string    `json:"id"`
	Label   string    `json:"label,omitempty"`
	Created time.Time `json:"created,omitempty"`
	Context int       `json:"context,omitempty"`
}

// A version segment is a number, optionally behind a one- or two-letter tag
// (v4, k3, k2.7). The tag stays in the family so kimi-k3 and k3 differ.
var versionSegment = regexp.MustCompile(`^([a-z]{0,2})(\d+(?:\.\d+)*)$`)

// An 8-digit segment is a snapshot date, not a version: haiku-4-5-20251001
// must share a family and a version with haiku-4-5.
var dateSegment = regexp.MustCompile(`^\d{8}$`)

func familyOf(id string) (family string, version []int, date string) {
	var parts []string
	for _, seg := range strings.Split(strings.ToLower(id), "-") {
		if dateSegment.MatchString(seg) {
			date = seg
			continue
		}
		if m := versionSegment.FindStringSubmatch(seg); m != nil {
			if m[1] != "" {
				parts = append(parts, m[1])
			}
			for _, n := range strings.Split(m[2], ".") {
				v, _ := strconv.Atoi(n)
				version = append(version, v)
			}
			continue
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, "-"), version, date
}

func compareVersions(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// newer reports whether a should replace b in their shared family. Dates are
// compared before Created because some sources (Kimi) give every model the
// same Created.
func newer(a, b Listed) bool {
	_, av, ad := familyOf(a.ID)
	_, bv, bd := familyOf(b.ID)
	if c := compareVersions(av, bv); c != 0 {
		return c > 0
	}
	if ad != bd {
		return ad > bd
	}
	return a.Created.After(b.Created)
}

// Latest keeps the newest model of each family, in the order each family
// first appears in the listing.
func Latest(models []Listed) []Listed {
	var order []string
	best := map[string]Listed{}
	for _, m := range models {
		if m.ID == "" {
			continue
		}
		family, _, _ := familyOf(m.ID)
		current, seen := best[family]
		if !seen {
			order = append(order, family)
			best[family] = m
			continue
		}
		if newer(m, current) {
			best[family] = m
		}
	}
	out := make([]Listed, 0, len(order))
	for _, family := range order {
		out = append(out, best[family])
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/allin/ -run 'TestLatest_' -v`
Expected: PASS. If a case fails, trace `familyOf` on the failing id before changing the expectation. The expectations are the spec's agreed results.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal/allin/latest.go internal/allin/latest_test.go
go vet ./internal/allin/
git add internal/allin/latest.go internal/allin/latest_test.go
git commit -m "feat(allin): pick the newest model per family from a listing

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Listing fetchers and DeepSeek's list URL

**Files:**
- Create: `internal/allin/listing.go`
- Test: `internal/allin/listing_test.go`
- Modify: `internal/claudeconfig/catalog.go`: add a `ModelsURL string` field to `Provider` (after `UnsupportedTools`), and set it on the `deepseek` entry.
- Test: `internal/claudeconfig/deepseek_catalog_test.go` (add one test)

**Interfaces:**
- Consumes: `Listed` (Task 1).
- Produces:
  ```go
  func FetchListing(client *http.Client, url string, auth http.Header) ([]Listed, error)
  func ReadCodexModels(path string) ([]Listed, error)
  // claudeconfig
  Provider.ModelsURL string
  ```

- [ ] **Step 1: Write the failing tests**

`internal/allin/listing_test.go`:

```go
package allin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFetchListing_reads_the_anthropic_shape_across_pages(t *testing.T) {
	var sawAuth, sawVersion, sawAfter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawVersion = r.Header.Get("anthropic-version")
		if after := r.URL.Query().Get("after_id"); after != "" {
			sawAfter = after
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5","created_at":"2025-10-15T00:00:00Z"}],"has_more":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","created_at":"2026-09-21T16:24:00Z"}],"has_more":true,"last_id":"claude-opus-5-5"}`))
	}))
	defer server.Close()

	got, err := FetchListing(server.Client(), server.URL+"/v1/models", http.Header{"Authorization": {"Bearer tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(got), []string{"claude-opus-5-5", "claude-haiku-4-5-20251001"}) {
		t.Fatalf("ids = %v", ids(got))
	}
	if got[0].Label != "Claude Opus 5.5" || got[0].Created.IsZero() {
		t.Fatalf("first = %+v", got[0])
	}
	if sawAuth != "Bearer tok" || sawVersion != "2023-06-01" || sawAfter != "claude-opus-5-5" {
		t.Fatalf("auth=%q version=%q after=%q", sawAuth, sawVersion, sawAfter)
	}
}

func TestFetchListing_reads_the_openai_shape_and_its_windows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"deepseek-flash","context_window":1048576},
			{"id":"k3","created":1761264000,"context_length":262144}]}`))
	}))
	defer server.Close()

	got, err := FetchListing(server.Client(), server.URL, http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Context != 1048576 || got[1].Context != 262144 || got[1].Created.Unix() != 1761264000 {
		t.Fatalf("got %+v", got)
	}
}

func TestFetchListing_fails_on_a_non_200_or_bad_json(t *testing.T) {
	for _, handler := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) },
		func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>`)) },
	} {
		server := httptest.NewServer(handler)
		if _, err := FetchListing(server.Client(), server.URL, http.Header{}); err == nil {
			t.Errorf("want an error")
		}
		server.Close()
	}
}

func TestReadCodexModels_keeps_only_listed_rows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models_cache.json")
	body := `{"fetched_at":"x","models":[
		{"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","context_window":272000},
		{"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","context_window":272000}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCodexModels(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "gpt-6-astra" || got[0].Label != "GPT-6-Astra" || got[0].Context != 272000 {
		t.Fatalf("got %+v", got)
	}
	if _, err := ReadCodexModels(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}
```

Append to `internal/claudeconfig/deepseek_catalog_test.go` (keep its existing imports; add any missing):

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/allin/ -run 'TestFetchListing_|TestReadCodexModels_' -v` and `go test ./internal/claudeconfig/ -run TestDeepSeekProvider_lists_models_off_its_openai_host -v`
Expected: compile failures, `undefined: FetchListing`, `ReadCodexModels`, `p.ModelsURL`.

- [ ] **Step 3: Implement**

`internal/allin/listing.go`:

```go
package allin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// maxListingPages bounds pagination so a server that always says has_more
// cannot keep a refresh running.
const maxListingPages = 10

type listingPage struct {
	Data []struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		CreatedAt     string `json:"created_at"`
		Created       int64  `json:"created"`
		ContextLength int    `json:"context_length"`
		ContextWindow int    `json:"context_window"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// FetchListing reads a /models list in either the Anthropic shape
// (display_name, created_at, has_more) or the OpenAI one (created in epoch
// seconds). auth is copied onto every request as-is.
func FetchListing(client *http.Client, listURL string, auth http.Header) ([]Listed, error) {
	var out []Listed
	after := ""
	for page := 0; page < maxListingPages; page++ {
		target := listURL
		if after != "" {
			parsed, err := url.Parse(listURL)
			if err != nil {
				return nil, err
			}
			query := parsed.Query()
			query.Set("after_id", after)
			parsed.RawQuery = query.Encode()
			target = parsed.String()
		}
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		for key, values := range auth {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("allin: model list %s answered %d", listURL, resp.StatusCode)
		}
		var parsed listingPage
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("allin: model list %s: %w", listURL, err)
		}
		for _, m := range parsed.Data {
			item := Listed{ID: m.ID, Label: m.DisplayName, Context: m.ContextWindow}
			if item.Context == 0 {
				item.Context = m.ContextLength
			}
			if created, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
				item.Created = created
			} else if m.Created > 0 {
				item.Created = time.Unix(m.Created, 0)
			}
			out = append(out, item)
		}
		if !parsed.HasMore || parsed.LastID == "" {
			break
		}
		after = parsed.LastID
	}
	return out, nil
}

// ReadCodexModels reads the model list Codex caches for itself. Only rows it
// shows in its own picker are kept; hidden ones are internal.
func ReadCodexModels(path string) ([]Listed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache struct {
		Models []struct {
			Slug          string `json:"slug"`
			DisplayName   string `json:"display_name"`
			Visibility    string `json:"visibility"`
			ContextWindow int    `json:"context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	var out []Listed
	for _, m := range cache.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		out = append(out, Listed{ID: m.Slug, Label: m.DisplayName, Context: m.ContextWindow})
	}
	return out, nil
}
```

`internal/claudeconfig/catalog.go`: in `type Provider struct`, after `UnsupportedTools []string`, add:

```go
	// ModelsURL is where this gateway lists its models, when that is not
	// <base URL>/v1/models. All-In reads it to offer the newest models.
	ModelsURL string
```

In the `deepseek` entry, after `UnsupportedTools: []string{"Artifact"},` add:

```go
		// Measured: <base>/v1/models answers 404; the OpenAI host lists them.
		ModelsURL: "https://api.deepseek.com/models",
```

- [ ] **Step 4: Run the tests to verify they pass**

Run the two commands from Step 2. Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal/allin/listing.go internal/allin/listing_test.go internal/claudeconfig/catalog.go internal/claudeconfig/deepseek_catalog_test.go
go vet ./internal/allin/ ./internal/claudeconfig/
git add internal/allin/listing.go internal/allin/listing_test.go internal/claudeconfig/catalog.go internal/claudeconfig/deepseek_catalog_test.go
git commit -m "feat(allin): read a source's live model list

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: The model cache file

**Files:**
- Create: `internal/allin/modelcache.go`
- Test: `internal/allin/modelcache_test.go`

**Interfaces:**
- Consumes: `Listed` (Task 1), `Env` (existing, `roster.go`).
- Produces:
  ```go
  const anthropicCacheKey = "anthropic"
  func configCacheKey(file string) string          // "cfg.<file without .json>"
  type CacheEntry struct { FetchedAt time.Time `json:"fetched_at"`; Models []Listed `json:"models"` }
  type ModelCache map[string]CacheEntry
  func ModelCachePath(env Env) string              // "" when env.ConfigsList is ""
  func LoadModelCache(path string) ModelCache      // never nil
  func SaveModelCache(path string, cache ModelCache) error
  func (c ModelCache) Fresh(key string, now time.Time, maxAge time.Duration) bool
  ```

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/allin/ -run 'TestModelCache_|TestLoadModelCache_|TestConfigCacheKey_' -v`
Expected: compile failure, undefined names.

- [ ] **Step 3: Implement**

```go
package allin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const anthropicCacheKey = "anthropic"

// configCacheKey matches the <source> of a wisp/cfg.<source>/… row id.
func configCacheKey(file string) string {
	return "cfg." + strings.TrimSuffix(file, ".json")
}

// CacheEntry is one source's reduced model list and when it was fetched.
type CacheEntry struct {
	FetchedAt time.Time `json:"fetched_at"`
	Models    []Listed  `json:"models"`
}

// ModelCache holds every source's last good list, keyed by source.
type ModelCache map[string]CacheEntry

// ModelCachePath sits beside the configs list, so every Env already locates it.
func ModelCachePath(env Env) string {
	if env.ConfigsList == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(env.ConfigsList), "allin-models.json")
}

// LoadModelCache never fails: an unreadable file means "nothing cached", and
// the roster falls back to its static lists.
func LoadModelCache(path string) ModelCache {
	cache := ModelCache{}
	if path == "" {
		return cache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return ModelCache{}
	}
	return cache
}

// SaveModelCache writes through a rename: several routers can refresh at once,
// and a reader must never see half a file.
func SaveModelCache(path string, cache ModelCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Fresh reports whether key holds a list younger than maxAge.
func (c ModelCache) Fresh(key string, now time.Time, maxAge time.Duration) bool {
	entry, ok := c[key]
	return ok && now.Sub(entry.FetchedAt) < maxAge
}
```

- [ ] **Step 4: Run to verify pass**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal/allin/modelcache.go internal/allin/modelcache_test.go
go vet ./internal/allin/
git add internal/allin/modelcache.go internal/allin/modelcache_test.go
git commit -m "feat(allin): cache each source's model list on disk

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Roster reads the cache

**Files:**
- Modify: `internal/allin/roster.go`: `accountRows`, `configRows`, and the `claudeLineup` comment. Extract `routableConfigs`.
- Test: `internal/allin/roster_discovered_test.go` (new file; uses `rosterEnv`, `models`, `has` and `labelFor` from `roster_test.go`)

**Interfaces:**
- Consumes: `LoadModelCache`, `ModelCachePath`, `anthropicCacheKey`, `configCacheKey`, `SaveModelCache` (Task 3), `claudeconfig.ModelLimit` (existing).
- Produces (used by Task 5):
  ```go
  type routableConfig struct { Config claudeconfig.Config; Provider claudeconfig.Provider }
  func routableConfigs(env Env) []routableConfig
  ```

- [ ] **Step 1: Write the failing tests**

```go
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
	if !has(models(Roster(env)), "wisp/acct.personal/claude-opus-5[1m]") {
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
		{ID: "glm-4.5-air"},                 // catalog: 131072
		{ID: "glm-tiny", Context: 32768},    // listing's own window
		{ID: "glm-unknown"},                 // no window anywhere: admitted
	}}})
	got := models(Roster(env))
	if has(got, "wisp/cfg.zhipu-glm/glm-4.5-air") || has(got, "wisp/cfg.zhipu-glm/glm-tiny") {
		t.Fatalf("a model under the floor was offered: %v", got)
	}
	if !has(got, "wisp/cfg.zhipu-glm/glm-unknown") {
		t.Fatalf("an unknown window must be admitted: %v", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/allin/ -run 'TestRoster_offers_the_cached|TestRoster_falls_back_to_the_pinned|TestRoster_floors_a_discovered' -v`
Expected: `TestRoster_offers_the_cached_*` and `TestRoster_floors_*` FAIL, because the roster ignores the cache. `TestRoster_falls_back_*` PASSES; it is a regression guard.

- [ ] **Step 3: Implement in `internal/allin/roster.go`**

Replace the `claudeLineup` comment with:

```go
// claudeLineup is the fallback until the router has cached Anthropic's own
// list (modelcache.go). An id it does not know still routes fine.
```

Add below it:

```go
// claudeModels is the cached Anthropic list, or the pinned lineup.
func claudeModels(cache ModelCache) []claudeModel {
	entry, ok := cache[anthropicCacheKey]
	if !ok || len(entry.Models) == 0 {
		return claudeLineup
	}
	out := make([]claudeModel, 0, len(entry.Models))
	for _, m := range entry.Models {
		label := strings.TrimPrefix(m.Label, "Claude ")
		if label == "" {
			label = m.ID
		}
		out = append(out, claudeModel{id: m.ID, label: label})
	}
	return out
}
```

In `accountRows`, before the `var rows []Row` loop add
`lineup := claudeModels(LoadModelCache(ModelCachePath(env)))`, and change `for _, model := range claudeLineup {` to `for _, model := range lineup {`.

Extract the filters from `configRows` into:

```go
type routableConfig struct {
	Config   claudeconfig.Config
	Provider claudeconfig.Provider
}

// routableConfigs is every profile the picker may offer. The refresher walks
// the same list, so it never fetches for a row the picker would not show.
func routableConfigs(env Env) []routableConfig {
	var out []routableConfig
	disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(env.ConfigsList))
	for _, config := range claudeconfig.Load(env.ConfigsList) {
		// ...move the existing disabled / ProfileName / ConfigReady /
		// routableAuth checks here unchanged, WITH their existing comments...
		out = append(out, routableConfig{Config: config, Provider: provider})
	}
	return out
}
```

Then rewrite `configRows` as:

```go
func configRows(env Env) []Row {
	var rows []Row
	cache := LoadModelCache(ModelCachePath(env))
	for _, rc := range routableConfigs(env) {
		config, provider := rc.Config, rc.Provider
		// Keep the existing RemoteCatalog comment block here.
		source := strings.TrimSuffix(config.File, ".json")
		for _, model := range offeredModels(env, config, provider, cache) {
			if model.Context != 0 && model.Context < minRosterContext {
				continue
			}
			rows = append(rows, Row{
				Model:       fmt.Sprintf("wisp/cfg.%s/%s", source, model.ID),
				Label:       config.Name + " · " + model.ID,
				Description: provider.Name,
			})
		}
	}
	return rows
}

// offeredModels prefers the cached live list. A model's window comes from the
// listing, then the catalog, else stays 0 (unknown, admitted by the floor).
func offeredModels(env Env, config claudeconfig.Config, provider claudeconfig.Provider, cache ModelCache) []claudeconfig.Model {
	entry, ok := cache[configCacheKey(config.File)]
	if provider.SuppliesOwnModel() || !ok || len(entry.Models) == 0 {
		return providerModels(env, config, provider)
	}
	out := make([]claudeconfig.Model, 0, len(entry.Models))
	for _, m := range entry.Models {
		window := m.Context
		if window == 0 {
			window, _, _ = claudeconfig.ModelLimit(m.ID)
		}
		out = append(out, claudeconfig.Model{ID: m.ID, Context: window})
	}
	return out
}
```

Note: `claudeconfig.ModelLimit` returns `(context, output int, ok bool)` and a zero context when the id is unknown. That is the "unknown, admit" value.

- [ ] **Step 4: Run the new tests, then the existing roster/profile tests to catch the refactor**

Run: `go test ./internal/allin/ -run 'TestRoster_|TestSourceCount|TestEnsureProfile' -v`
Expected: PASS, all of them. The existing roster tests have no cache file, so they must be unchanged.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal/allin/roster.go internal/allin/roster_discovered_test.go
go vet ./internal/allin/
git add internal/allin/roster.go internal/allin/roster_discovered_test.go
git commit -m "feat(allin): build the picker from the cached model lists

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: The refresher

**Files:**
- Create: `internal/allin/modelrefresh.go`
- Test: `internal/allin/modelrefresh_test.go`

**Interfaces:**
- Consumes: `FetchListing`, `ReadCodexModels` (Task 2); cache API (Task 3); `routableConfigs` (Task 4); `Latest` (Task 1); `anthropicUpstream` (existing, `credential.go`); `claudeconfig.ReadAPIKey` / `ReadBaseURL` (existing); `Provider.ModelsURL` (Task 2).
- Produces:
  ```go
  const modelListMaxAge = 12 * time.Hour
  type ModelRefresher struct {
      Env          Env
      Client       *http.Client
      CodexCache   string
      AnthropicURL string          // "" means anthropicUpstream+"/v1/models"
      Now          func() time.Time
      Ensure       func(Env) error
      // unexported: once sync.Once
  }
  func (r *ModelRefresher) Observe(sessionAuth http.Header) // once per process, async
  func (r *ModelRefresher) Refresh(sessionAuth http.Header) bool // sync; true if any list changed
  ```

- [ ] **Step 1: Write the failing tests**

```go
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

// pointZhipuAt rewrites the rosterEnv zhipu profile's base URL to a test server.
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
	if got := ids(LoadModelCache(ModelCachePath(env))[configCacheKey("zhipu-glm.json")].Models); len(got) != 1 || got[0] != "glm-5.3" {
		t.Fatalf("entry = %v", got)
	}
}

func TestRefresh_skips_anthropic_without_session_auth(t *testing.T) {
	env := rosterEnv(t)
	var hits int32
	anthropic := listServer(t, `{"data":[{"id":"claude-opus-5-5"}]}`, &hits, nil)
	var zhipuHits int32
	// rosterEnv points zhipu at the real z.ai; every refresh test must redirect it.
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
	listFile := env.ConfigsList
	if err := os.WriteFile(listFile, []byte("OpenAI / ChatGPT:openai-chatgpt.json\n"), 0o600); err != nil {
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
	defer close(release)
	pointZhipuAt(t, env, slow.URL)
	r := &ModelRefresher{Env: env, Client: http.DefaultClient, AnthropicURL: slow.URL,
		Now: time.Now, Ensure: func(Env) error { return nil }}

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
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hits = %d; one Observe'd refresh reaches its first fetch", hits)
	}
}
```

Before relying on `TestRefresh_reads_chatgpt_from_the_codex_cache`, check that `claudeconfig.ConfigReady` accepts a ChatGPT profile holding only the provider marker. Read `ConfigReady` in `internal/claudeconfig/claudeconfig.go:380`, and look at how `internal/allin/chatgpt_test.go` builds a ready ChatGPT profile. Copy that fixture exactly if it needs more keys.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/allin/ -run 'TestRefresh_|TestObserve_' -v`
Expected: compile failure, `undefined: ModelRefresher`.

- [ ] **Step 3: Implement**

```go
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
```

Note: `TestObserve_refreshes_once_and_never_blocks` points both the Anthropic URL and Zhipu at the same slow server. The anthropic fetch blocks until `release` closes, so exactly one hit arrives while the test watches. That is why it asserts `hits == 1`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/allin/ -run 'TestRefresh_|TestObserve_' -race -v`
Expected: PASS, with no race reports.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal/allin/modelrefresh.go internal/allin/modelrefresh_test.go
go vet ./internal/allin/
git add internal/allin/modelrefresh.go internal/allin/modelrefresh_test.go
git commit -m "feat(allin): refresh stale model lists in the background

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Wire the refresher into the router

**Files:**
- Modify: `internal/allin/proxy.go` (`NewHandler` → delegate to `NewObservingHandler`)
- Modify: `cmd/wisp-deck-tui/claude_allin.go` (build a `ModelRefresher`, pass `Observe`)
- Test: `internal/allin/proxy_observe_test.go` (new)
- Modify: `internal/allin/CLAUDE.md` (one gotcha section)

**Interfaces:**
- Consumes: `ModelRefresher.Observe` (Task 5), `allin.EnsureProfileIfEligible` (existing), `currentHostEffectsDecision()` (existing, `cmd/wisp-deck-tui/host_effects_policy.go:89`).
- Produces:
  ```go
  func NewObservingHandler(resolver Resolver, sessionUpstream string, observe func(http.Header)) http.Handler
  ```

- [ ] **Step 1: Write the failing tests**

`internal/allin/proxy_observe_test.go`. Read `internal/allin/chatgpt_test.go:170-260` first and reuse its fake-resolver type for a routed request. The code below uses a local minimal one:

```go
package allin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixedResolver struct{ credential Credential }

func (f fixedResolver) Resolve(Target) (Credential, error) { return f.credential, nil }

func observeOnce(t *testing.T, upstream, model string) http.Header {
	t.Helper()
	var seen http.Header
	called := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	resolver := fixedResolver{Credential{BaseURL: backend.URL, Header: "Authorization", Value: "Bearer swapped"}}
	handler := NewObservingHandler(resolver, upstream, func(h http.Header) { called = true; seen = h })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"`+model+`"}`))
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("X-Api-Key", "session-key")
	req.Header.Set("Anthropic-Beta", "something")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("observe was not called")
	}
	return seen
}

func TestHandler_hands_the_session_auth_to_the_observer_before_the_swap(t *testing.T) {
	seen := observeOnce(t, "https://api.anthropic.com", "wisp/acct.personal/claude-opus-5-5[1m]")
	if seen.Get("Authorization") != "Bearer session" || seen.Get("X-Api-Key") != "session-key" {
		t.Fatalf("seen = %v", seen)
	}
	if seen.Get("Anthropic-Beta") != "" {
		t.Fatal("only credential headers may be handed on")
	}
}

func TestHandler_passes_no_session_auth_for_a_non_anthropic_upstream(t *testing.T) {
	if seen := observeOnce(t, "https://api.z.ai/api/anthropic", "wisp/acct.personal/claude-opus-5[1m]"); len(seen) != 0 {
		t.Fatalf("a non-Anthropic session credential leaked: %v", seen)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/allin/ -run 'TestHandler_hands_the_session_auth|TestHandler_passes_no_session_auth' -v`
Expected: compile failure, `undefined: NewObservingHandler`.

- [ ] **Step 3: Implement in `internal/allin/proxy.go`**

Change the top of the file's handler constructor:

```go
func NewHandler(resolver Resolver, sessionUpstream string) http.Handler {
	return NewObservingHandler(resolver, sessionUpstream, nil)
}

// NewObservingHandler is NewHandler plus a hook that sees the session's own
// credential on every request. It gets nothing unless the session upstream
// is Anthropic, so a provider key never reaches api.anthropic.com.
func NewObservingHandler(resolver Resolver, sessionUpstream string, observe func(http.Header)) http.Handler {
	sessionIsAnthropic := false
	if parsed, err := url.Parse(sessionUpstream); err == nil && parsed.Hostname() == "api.anthropic.com" {
		sessionIsAnthropic = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if observe != nil {
			auth := http.Header{}
			if sessionIsAnthropic {
				for _, key := range []string{"Authorization", "X-Api-Key"} {
					if v := r.Header.Get(key); v != "" {
						auth.Set(key, v)
					}
				}
			}
			observe(auth)
		}
		// ...the existing handler body, unchanged...
	})
}
```

(`net/url` is already imported by `proxy.go` for `validUpstream`. Confirm this with `grep -n '"net/url"' internal/allin/proxy.go`.)

Then in `cmd/wisp-deck-tui/claude_allin.go`, inside `RunE`, replace the `newHandler` closure with:

```go
			observe := func(http.Header) {}
			if currentHostEffectsDecision().Allowed {
				refresher := &allin.ModelRefresher{
					Env:        env,
					Client:     &http.Client{Timeout: 15 * time.Second},
					CodexCache: codexModelsCache(),
					Now:        time.Now,
					Ensure:     allin.EnsureProfileIfEligible,
				}
				observe = refresher.Observe
			}
			newHandler := func(upstream string) http.Handler {
				return allin.NewObservingHandler(resolver, upstream, observe)
			}
```

and add at the bottom of the file:

```go
// codexModelsCache is the list Codex fetches and caches for itself. Reading it
// starts no app-server, which would cost ~7s cold.
func codexModelsCache() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "models_cache.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "models_cache.json")
}
```

Add `os`, `path/filepath` and `time` to the imports only if they are missing. Check the file's import block first.

`EnsureProfileIfEligible`'s signature must be `func(Env) error`. Verify at `internal/allin/profile.go:135` before relying on it.

Before adding the `CODEX_HOME` branch, check with `grep -rn 'CODEX_HOME' internal cmd lib | head` whether the repo already resolves the Codex home somewhere. If it does, reuse that helper instead of this function.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/allin/ -run 'TestHandler_|TestNewHandler|Chat' -v` and `go test ./cmd/wisp-deck-tui/ -run 'ClaudeAllIn' -v`
Expected: PASS. The cmd tests run as a go test binary, so `currentHostEffectsDecision().Allowed` is false and no refresher is built.

- [ ] **Step 5: Document the gotcha**

Append to `internal/allin/CLAUDE.md`. That file is audited production prose: do not use the bare word that means "speak aloud", and no audio-tool names.

```markdown
### The picker's models come from a cache the router fills

`Roster` never touches the network: it reads `allin-models.json` (beside
`claude-configs.list`) and falls back to `claudeLineup` / the catalog when a
source has no entry. The router fills that file once per process, in the
background, for entries older than 12h, and then re-runs
`EnsureProfileIfEligible` — so a new model reaches the picker on the NEXT
All-In launch after a refresh.

- The Claude list is fetched with the credential the session itself sent, and
  only when the session upstream is `api.anthropic.com`. Reading a login from
  the Keychain instead would refresh — and rotate — the token of a login nobody
  is using (see `8711da5`).
- `Latest` keeps the highest VERSION per family, not the newest date: Kimi gives
  every model the same date. An 8-digit segment is a snapshot date and only
  breaks ties.
- A failed or empty listing keeps the old entry. Never let a refresh empty a
  source.
- DeepSeek's `/anthropic` base has no model list; `Provider.ModelsURL` names the
  one it does have.
```

- [ ] **Step 6: Lint, commit, push**

```bash
gofmt -l internal/allin/proxy.go internal/allin/proxy_observe_test.go cmd/wisp-deck-tui/claude_allin.go
go vet ./internal/allin/ ./cmd/wisp-deck-tui/
git add internal/allin/proxy.go internal/allin/proxy_observe_test.go cmd/wisp-deck-tui/claude_allin.go internal/allin/CLAUDE.md
git commit -m "feat(allin): refresh the picker's models from inside the router

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
git push
```

---

### Task 7: Whole-package check and live verification

**Files:** none (verification only)

- [ ] **Step 1: Run the changed packages through the project's test runner**

Read `./run-tests.sh` to see how it scopes packages. Then run it for `./internal/allin/ ./internal/claudeconfig/ ./cmd/wisp-deck-tui/` if it accepts package arguments. If it does not, run with `WISP_DECK_TESTING=1 go test ./internal/allin/ ./internal/claudeconfig/ ./cmd/wisp-deck-tui/ -json | tee "$SCRATCH/out.json" >/dev/null; go run ./cmd/ci-report --title allin "$SCRATCH/out.json"`, where `$SCRATCH` is the session scratchpad.
Expected: all pass. If an existing test hard-codes the catalog's `Provider` field count or order (e.g. a struct-literal comparison), fix it there.

- [ ] **Step 2: Live check against the real endpoints (costs no quota)**

Build a throwaway program in the scratchpad (not in the repo). It should:
- read the default login's token the way the Task-0 probe did;
- call `allin.FetchListing` on `https://api.anthropic.com/v1/models`, `https://api.z.ai/api/anthropic/v1/models` and `https://api.deepseek.com/models`, plus `allin.ReadCodexModels` on the real Codex cache;
- print `allin.Latest` of each.

Expected, compared against the spec's list:
- Claude: opus-5-5, fable-5-1, sonnet-5, haiku-4-5-20251001;
- Zhipu: the 5 families;
- DeepSeek: 2;
- ChatGPT: 5.

Report any difference as a finding. Do not change the expectation to match.

- [ ] **Step 3: Local install**

Follow the "Build local binary from HEAD" memory: `git archive HEAD` → build in the scratchpad → install, sign and warm `~/.local/bin/wisp-deck-tui`. Tell the user a new All-In launch is needed for the refresh to run, and the one after it shows the new rows. Do not respawn any live pane.
