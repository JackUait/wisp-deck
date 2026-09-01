# Featherless Subscriptions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user back a Claude Code pane with any of Featherless's ~15,571 tool-calling models, chosen from a searchable picker in the Subscription modal.

**Architecture:** Featherless serves the Anthropic Messages API natively at `https://api.featherless.ai`, so it is an ordinary API-key gateway — no proxy or bridge. What it lacks is a *static* model catalog, so a new `RemoteCatalog` provider trait routes its model/context/images rows through the same writers the self-hosted provider already uses, and a new `internal/featherless` package fetches, caches, filters and searches the remote list.

**Tech Stack:** Go 1.x, Bubbletea (`tea.Model` / `tea.Cmd`), `net/http`, `encoding/json`, existing `internal/claudeconfig` writers.

**Spec:** `docs/superpowers/specs/2026-09-01-featherless-subscription-design.md` — read it first; it carries the live-probe evidence every design decision here rests on.

## Global Constraints

- **TDD is mandatory.** Write the test, run it, watch it FAIL, then write code. Code written before its test must be deleted and redone. (`CLAUDE.md`, IRON RULE.)
- **Run only the tests you added**, scoped to the file: `go test ./internal/featherless/ -run TestName -v`. Never the full suite mid-task.
- **Full suite + `shellcheck` on modified scripts only at the end** (Task 10), then `git push`.
- Base URL is exactly `https://api.featherless.ai` (Claude Code appends `/v1/messages`).
- Credential env var is `ANTHROPIC_AUTH_TOKEN` (`Authorization: Bearer`). `ANTHROPIC_API_KEY` / `x-api-key` is rejected by Featherless with a 401.
- Provider key is `"featherless"`; sole alias is `"featherless"`.
- The byte watchdog **stays armed** for Featherless. Never add it to `stampByteWatchdog`'s disarm path.
- Never commit a Featherless API key. Live tests read `FEATHERLESS_API_KEY` from the environment. The repo has a guard test for leaked keys (see `kimi-key-leaked-publicly`).
- Work directly on `main`; do not create branches.
- Comments follow `CLAUDE.md`: record only what silently breaks if changed. No comment that restates the code.

---

### Task 1: The `RemoteCatalog` trait and the Featherless catalog entry

**Files:**
- Modify: `internal/claudeconfig/catalog.go` (the `Provider` struct ~line 30, and the `Providers` slice)
- Test: `internal/claudeconfig/featherless_provider_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `Provider.RemoteCatalog bool`; `func (p Provider) SuppliesOwnModel() bool`; a provider with `Key: "featherless"` reachable via `ProviderByKey("featherless")`.

- [ ] **Step 1: Write the failing test**

Create `internal/claudeconfig/featherless_provider_test.go`:

```go
package claudeconfig

import "testing"

// Featherless ships an endpoint and an auth kind but no model list: its catalog
// is ~15,571 models fetched at runtime, so the modal must offer a picker rather
// than the alias cycler, which is inert on an empty model list.
func TestFeatherlessProvider_declares_a_remote_catalog(t *testing.T) {
	provider, ok := ProviderByKey("featherless")
	if !ok {
		t.Fatal("catalog has no featherless provider")
	}
	if !provider.RemoteCatalog {
		t.Error("featherless must set RemoteCatalog")
	}
	if provider.UserConfigured {
		t.Error("featherless ships its own endpoint, so it is not UserConfigured")
	}
	if provider.BaseURL != "https://api.featherless.ai" {
		t.Errorf("base URL = %q, want https://api.featherless.ai", provider.BaseURL)
	}
	if len(provider.Models) != 0 {
		t.Errorf("featherless must ship no static models, got %d", len(provider.Models))
	}
	if provider.Auth != AuthAPIKey {
		t.Errorf("auth = %q, want %q", provider.Auth, AuthAPIKey)
	}
	if provider.MirrorOpenCode {
		t.Error("OpenCode's catalog cannot size a Featherless-only model")
	}
	if !provider.SuppliesOwnModel() {
		t.Error("a remote-catalog provider supplies its own model")
	}
}

// Profiles are auto-named after the picked model, so "Featherless Kimi-K3" is a
// name this feature produces routinely. It contains "kimi", which is moonshot's
// alias, and alias matching is substring in slice order — so a featherless entry
// placed after moonshot resolves those profiles to the wrong gateway.
func TestFeatherlessAliasBeatsAModelNameFromAnotherProvider(t *testing.T) {
	for _, name := range []string{
		"Featherless Kimi-K3",
		"Featherless GLM-5.2",
		"featherless moonshotai/Kimi-K2.7-Code",
	} {
		if got := ProviderForName(name).Key; got != "featherless" {
			t.Errorf("ProviderForName(%q) = %q, want featherless", name, got)
		}
	}
}

// Providers[0] is the fallback for every name matching no alias. A provider with
// no models there would claim every stray config on the machine. The custom
// provider must stay last for the same reason.
func TestFeatherlessIsNeitherTheFallbackNorLast(t *testing.T) {
	if Providers[0].Key == "featherless" {
		t.Fatal("featherless must not be the unknown-name fallback")
	}
	if Providers[len(Providers)-1].Key != "custom" {
		t.Fatalf("custom must stay last, got %q", Providers[len(Providers)-1].Key)
	}
}

// The self-hosted provider must keep answering the same question the same way:
// the trait is an addition, not a redefinition.
func TestCustomProviderStillSuppliesItsOwnModel(t *testing.T) {
	provider, ok := ProviderByKey("custom")
	if !ok {
		t.Fatal("catalog has no custom provider")
	}
	if !provider.SuppliesOwnModel() {
		t.Error("custom must still report SuppliesOwnModel")
	}
	if zhipu, _ := ProviderByKey("zhipu"); zhipu.SuppliesOwnModel() {
		t.Error("a static-catalog gateway must not report SuppliesOwnModel")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claudeconfig/ -run 'TestFeatherless|TestCustomProviderStillSupplies' -v`
Expected: FAIL to compile — `provider.RemoteCatalog undefined`, `provider.SuppliesOwnModel undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/claudeconfig/catalog.go`, add the field to `Provider` immediately after `UserConfigured`:

```go
	// RemoteCatalog marks a provider that ships an endpoint but fetches its
	// model list at runtime instead of declaring one here. The modal offers a
	// searchable picker rather than the alias cycler, which is inert on an
	// empty model list.
	RemoteCatalog bool
```

Add the method below the `Provider` type:

```go
// SuppliesOwnModel reports whether the profile's model id, context window, and
// image policy come from the user or the picker rather than from this catalog.
// True for a self-hosted endpoint and for a remote-catalog gateway — both must
// skip WriteModelMappings, which would otherwise delete the stored model on
// every save because it writes the four aliases from an empty model list.
func (p Provider) SuppliesOwnModel() bool { return p.UserConfigured || p.RemoteCatalog }
```

Insert this entry into `Providers` **immediately before the `moonshot-coding` entry** (after `openai-chatgpt`):

```go
	{
		// Featherless hosts ~22,000 HuggingFace models behind one key and —
		// undocumented, verified live on 2026-09-01 — serves the Anthropic
		// Messages API natively at /v1/messages, so no translating proxy is
		// involved. Its catalog is fetched at runtime by internal/featherless.
		//
		// It sits ahead of the moonshot entries because profiles are named
		// after the picked model: "Featherless Kimi-K3" contains moonshot's
		// "kimi" alias, and alias matching is substring in slice order.
		Key:            "featherless",
		Name:           "Featherless",
		Aliases:        []string{"featherless"},
		BaseURL:        "https://api.featherless.ai",
		Auth:           AuthAPIKey,
		MirrorOpenCode: false,
		RemoteCatalog:  true,
	},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claudeconfig/ -run 'TestFeatherless|TestCustomProviderStillSupplies' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/claudeconfig/catalog.go internal/claudeconfig/featherless_provider_test.go
git commit -m "feat(subscriptions): add Featherless as a remote-catalog provider"
```

---

### Task 2: Readiness, window ownership, and the armed-watchdog guard

**Files:**
- Modify: `internal/claudeconfig/claudeconfig.go` (`ConfigReady`, ~line 377)
- Modify: `internal/claudeconfig/contextbudget.go` (`stampContextBudget`, ~line 60)
- Test: `internal/claudeconfig/featherless_provider_test.go` (append)

**Interfaces:**
- Consumes: `Provider.SuppliesOwnModel()` from Task 1.
- Produces: a Featherless profile that is `ConfigReady` only once key + model + window are stored, and whose declared window survives `EnsureContextBudget`.

- [ ] **Step 1: Write the failing test**

Append to `internal/claudeconfig/featherless_provider_test.go`:

```go
func featherlessConfig(t *testing.T) (dir, list, file string) {
	t.Helper()
	dir = t.TempDir()
	list = filepath.Join(dir, "claude-configs.list")
	file, err := AddForProvider(list, dir, "Featherless Kimi-K3", "featherless")
	if err != nil {
		t.Fatalf("AddForProvider: %v", err)
	}
	return dir, list, file
}

// A profile with no picked model would launch a pane with no model at all, so it
// must not be selectable until the pick and the key are both stored.
func TestFeatherlessConfigReady_requires_a_picked_model_and_a_key(t *testing.T) {
	dir, _, file := featherlessConfig(t)
	config := Config{Name: "Featherless Kimi-K3", File: file}

	if ConfigReady(dir, config) {
		t.Fatal("a fresh featherless profile has no model or key and must not be ready")
	}
	if err := WriteAPIKey(dir, file, "rc_test"); err != nil {
		t.Fatal(err)
	}
	if ConfigReady(dir, config) {
		t.Error("a key alone is not enough: the model is still unpicked")
	}
	if err := WriteCustomModel(dir, file, "moonshotai/Kimi-K3"); err != nil {
		t.Fatal(err)
	}
	if ConfigReady(dir, config) {
		t.Error("a model with no declared window strands the session on the flat 200000 default")
	}
	if err := WriteCustomContextWindow(dir, file, "262144"); err != nil {
		t.Fatal(err)
	}
	if !ConfigReady(dir, config) {
		t.Error("key + model + window must be ready")
	}
}

// Featherless keeps the socket warm with ": keep-alive" SSE comments through a
// cold model load (measured: comments every ~1.2s across a 12s load, worst byte
// silence 4.8s against the watchdog's 20s trigger). Disarming the watchdog here
// would trade a real dead-connection signal for nothing.
func TestFeatherlessKeepsTheByteWatchdogArmed(t *testing.T) {
	dir, _, file := featherlessConfig(t)
	if _, ok := readEnv(t, dir, file)[ByteWatchdogKey]; ok {
		t.Fatal("featherless must not disarm the byte watchdog at creation")
	}
	changed, err := EnsureByteWatchdog(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("the watchdog sweep must leave a featherless profile untouched")
	}
	if _, ok := readEnv(t, dir, file)[ByteWatchdogKey]; ok {
		t.Error("featherless must not disarm the byte watchdog on sweep")
	}
}

// The static catalog cannot size a Featherless-only model, so the window written
// at pick time is the only figure available and every sweep must preserve it.
func TestFeatherlessDeclaredWindowSurvivesTheBudgetSweep(t *testing.T) {
	dir, _, file := featherlessConfig(t)
	if err := WriteCustomModel(dir, file, "moonshotai/Kimi-K3"); err != nil {
		t.Fatal(err)
	}
	if err := WriteCustomContextWindow(dir, file, "262144"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureContextBudget(dir, file); err != nil {
		t.Fatal(err)
	}
	if got, _ := readEnv(t, dir, file)[ContextBudgetKey].(string); got != "262144" {
		t.Errorf("declared window = %q, want 262144 preserved", got)
	}
}
```

Add `"path/filepath"` to that file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/claudeconfig/ -run 'TestFeatherlessConfigReady|TestFeatherlessKeeps|TestFeatherlessDeclared' -v`
Expected: FAIL — `TestFeatherlessConfigReady_requires_a_picked_model_and_a_key` reports ready after only the key, because `ConfigReady` short-circuits on `!provider.UserConfigured`.

- [ ] **Step 3: Write minimal implementation**

In `internal/claudeconfig/claudeconfig.go`, inside `ConfigReady`'s `AuthAPIKey` branch, change the short-circuit:

```go
		if !provider.SuppliesOwnModel() {
			return true
		}
```

The endpoint check below it needs no change: `AddForProvider` writes `ANTHROPIC_BASE_URL` for Featherless because the provider declares one, and `https://api.featherless.ai` passes `ValidateCustomEndpoint`.

In `internal/claudeconfig/contextbudget.go`, inside `stampContextBudget`, replace the two `UserConfigured` reads that decide window ownership:

```go
	marker, _ := env["WISP_DECK_SUBSCRIPTION_PROVIDER"].(string)
	provider, marked := providerByKey(marker)
	userConfigured := marked && provider.SuppliesOwnModel()
	if !marked {
		provider = providerFor(configName)
		userConfigured = provider.SuppliesOwnModel()
```

Leave the rest of the unmarked-legacy branch untouched.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/claudeconfig/ -run 'TestFeatherless' -v`
Expected: PASS.

Then confirm nothing regressed in the package:

Run: `go test ./internal/claudeconfig/`
Expected: PASS (ok).

- [ ] **Step 5: Commit**

```bash
git add internal/claudeconfig/
git commit -m "feat(subscriptions): gate Featherless readiness on a picked model and window"
```

---

### Task 3: Parse the Featherless catalog

**Files:**
- Create: `internal/featherless/catalog.go`
- Create: `internal/featherless/catalog_test.go`
- Create: `internal/featherless/testdata/models.json`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Model struct{ ID, Class string; Context int; InPerM, OutPerM float64; ImageInput, OnPlan bool; Created int64 }` and `func Parse(data []byte) ([]Model, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/featherless/testdata/models.json` — the real response shape, trimmed:

```json
{"data":[
 {"id":"recursal/EagleX_1-7T","is_gated":false,"created":1719250778,"model_class":"rwkv5-7b","context_length":16384,"pricing":{"input":0.1,"output":0.2}},
 {"id":"moonshotai/Kimi-K3","is_gated":false,"created":1785207017,"model_class":"kimi3-2780b","context_length":262144,"pricing":{"input":3,"output":15},"features":{"tool_use":true,"image_input":true}},
 {"id":"zai-org/GLM-5.2","is_gated":false,"created":1780000000,"model_class":"glm52-753b","context_length":262144,"pricing":{"input":0.75,"output":2.4},"features":{"tool_use":true}},
 {"id":"meta-llama/Llama-3.3-70B-Instruct","is_gated":true,"created":1733521405,"model_class":"llama33-70b","context_length":32768,"pricing":{"input":0.65,"output":0.75},"features":{"tool_use":true},"available_on_current_plan":false},
 {"id":"unsloth/Llama-3.3-70B-Instruct","is_gated":false,"created":1733616012,"model_class":"llama33-70b","context_length":32768,"pricing":{"input":2.6,"output":3},"features":{"tool_use":true}},
 {"id":"broken/no-context","is_gated":false,"created":1730000000,"model_class":"weird-1b","pricing":{"input":1,"output":1},"features":{"tool_use":true}}
]}
```

Create `internal/featherless/catalog_test.go`:

```go
package featherless

import (
	"os"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func ids(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

// Claude Code cannot run without tool calling: a model lacking it produces a
// pane that cannot read or edit a single file, so those models are never
// offered. A model with no declared context length cannot be sized, and an
// undeclared window strands the session on the flat 200000 default.
func TestParse_keeps_only_sizable_tool_calling_models(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ID == "recursal/EagleX_1-7T" {
			t.Error("a model without features.tool_use must be dropped")
		}
		if m.ID == "broken/no-context" {
			t.Error("a model without context_length must be dropped")
		}
		if m.Context <= 0 {
			t.Errorf("%s kept with context %d", m.ID, m.Context)
		}
	}
	if len(models) != 4 {
		t.Fatalf("kept %d models (%v), want 4", len(models), ids(models))
	}
}

// Someone opening the picker is nearly always after the frontier tier, so the
// widest window comes first and ties break to the newest model.
func TestParse_orders_by_context_then_newest(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"moonshotai/Kimi-K3",
		"zai-org/GLM-5.2",
		"unsloth/Llama-3.3-70B-Instruct",
		"meta-llama/Llama-3.3-70B-Instruct",
	}
	got := ids(models)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestParse_carries_the_fields_the_picker_and_the_pick_need(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	kimi := models[0]
	if kimi.Context != 262144 {
		t.Errorf("context = %d, want 262144", kimi.Context)
	}
	if kimi.InPerM != 3 || kimi.OutPerM != 15 {
		t.Errorf("price = %v/%v, want 3/15", kimi.InPerM, kimi.OutPerM)
	}
	if !kimi.ImageInput {
		t.Error("Kimi-K3 reports image_input and must carry it: the pick defaults the images toggle from it")
	}
	if models[1].ImageInput {
		t.Error("GLM-5.2 declares no image_input and must be text-only")
	}
}

// available_on_current_plan is absent on an unauthenticated listing, and absent
// must not read as "unavailable" — that would empty the picker for a user who
// has not typed a key yet.
func TestParse_treats_an_absent_plan_flag_as_available(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ID == "meta-llama/Llama-3.3-70B-Instruct" {
			if m.OnPlan {
				t.Error("an explicit available_on_current_plan:false must be preserved")
			}
			continue
		}
		if !m.OnPlan {
			t.Errorf("%s has no plan flag and must default to available", m.ID)
		}
	}
}

func TestParse_rejects_a_body_that_is_not_the_catalog(t *testing.T) {
	for name, body := range map[string]string{
		"truncated": `{"data":[{"id":"a"`,
		"html":      `<!doctype html><title>502</title>`,
		"error":     `{"error":{"message":"nope"}}`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s body accepted, want an error", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/featherless/ -v`
Expected: FAIL to build — `undefined: Parse`, `undefined: Model`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/featherless/catalog.go`:

```go
// Package featherless reads Featherless's remote model catalog: ~22,000
// HuggingFace models behind one API key, of which the ~15,500 reporting
// tool_use are the ones a Claude Code pane can actually use.
package featherless

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Model is one Featherless model, reduced to what the picker renders and what
// picking one writes into a profile.
type Model struct {
	ID         string  `json:"id"`
	Class      string  `json:"class"`
	Context    int     `json:"context"`
	InPerM     float64 `json:"in_per_m"`
	OutPerM    float64 `json:"out_per_m"`
	ImageInput bool    `json:"image_input"`
	OnPlan     bool    `json:"on_plan"`
	Created    int64   `json:"created"`
}

// wireModel is the catalog's own shape. It is separate from Model because the
// cache stores Model, and a Featherless field rename must not silently reshape
// what a cache written by an older build decodes into.
type wireModel struct {
	ID       string `json:"id"`
	Class    string `json:"model_class"`
	Context  int    `json:"context_length"`
	Created  int64  `json:"created"`
	Features struct {
		ToolUse    bool `json:"tool_use"`
		ImageInput bool `json:"image_input"`
	} `json:"features"`
	Pricing struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"pricing"`
	// A pointer because the field is absent on an unauthenticated listing, and
	// absent must read as available rather than as "not on your plan" — which
	// would empty the picker before the user has entered a key.
	OnPlan *bool `json:"available_on_current_plan"`
}

// Parse decodes a /v1/models body, keeping only models a Claude Code pane can
// use: tool calling is what lets it read and edit files, and a declared context
// length is what keeps the session off the flat 200000 default that strands a
// small model permanently.
func Parse(data []byte) ([]Model, error) {
	var body struct {
		Data []wireModel `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("featherless: parse catalog: %w", err)
	}
	if len(body.Data) == 0 {
		return nil, fmt.Errorf("featherless: catalog has no models")
	}
	models := make([]Model, 0, len(body.Data))
	for _, w := range body.Data {
		if !w.Features.ToolUse || w.Context <= 0 || w.ID == "" {
			continue
		}
		onPlan := true
		if w.OnPlan != nil {
			onPlan = *w.OnPlan
		}
		models = append(models, Model{
			ID:         w.ID,
			Class:      w.Class,
			Context:    w.Context,
			InPerM:     w.Pricing.Input,
			OutPerM:    w.Pricing.Output,
			ImageInput: w.Features.ImageInput,
			OnPlan:     onPlan,
			Created:    w.Created,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("featherless: catalog has no tool-calling models")
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Context != models[j].Context {
			return models[i].Context > models[j].Context
		}
		if models[i].Created != models[j].Created {
			return models[i].Created > models[j].Created
		}
		return models[i].ID < models[j].ID
	})
	return models, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/featherless/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/featherless/
git commit -m "feat(featherless): parse the remote model catalog"
```

---

### Task 4: Fetch the catalog over HTTP

**Files:**
- Create: `internal/featherless/fetch.go`
- Create: `internal/featherless/fetch_test.go`

**Interfaces:**
- Consumes: `Parse` and `Model` from Task 3.
- Produces: `const CatalogURL = "https://api.featherless.ai/v1/models"`, `func Fetch(ctx context.Context, key string) ([]Model, error)`, and `func fetchFrom(ctx context.Context, url, key string) ([]Model, error)` (unexported, used by tests to point at an httptest server).

- [ ] **Step 1: Write the failing test**

Create `internal/featherless/fetch_test.go`:

```go
package featherless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Featherless rejects x-api-key with a 401; the credential travels as a bearer
// token, which is exactly what the profile's ANTHROPIC_AUTH_TOKEN produces.
func TestFetch_sends_the_key_as_a_bearer_token(t *testing.T) {
	var gotAuth, gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		_, _ = w.Write(fixture(t))
	}))
	defer server.Close()

	models, err := fetchFrom(context.Background(), server.URL, "rc_secret")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer rc_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer rc_secret")
	}
	if gotAPIKey != "" {
		t.Errorf("x-api-key sent (%q); Featherless 401s on it", gotAPIKey)
	}
	if len(models) != 4 {
		t.Errorf("got %d models, want the 4 usable ones", len(models))
	}
}

// The catalog is public. A user who has not entered a key yet must still be able
// to browse and pick, or the flow deadlocks on itself.
func TestFetch_works_without_a_key(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("no key means no Authorization header at all")
		}
		_, _ = w.Write(fixture(t))
	}))
	defer server.Close()

	if _, err := fetchFrom(context.Background(), server.URL, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFetch_reports_a_failing_status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream is down"))
	}))
	defer server.Close()

	if _, err := fetchFrom(context.Background(), server.URL, ""); err == nil {
		t.Error("a 502 must be an error, not an empty catalog")
	}
}

func TestFetch_honours_a_cancelled_context(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchFrom(ctx, server.URL, ""); err == nil {
		t.Error("a cancelled context must fail the fetch")
	}
}

func TestCatalogURL_points_at_the_documented_listing(t *testing.T) {
	if CatalogURL != "https://api.featherless.ai/v1/models" {
		t.Errorf("CatalogURL = %q", CatalogURL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/featherless/ -run TestFetch -v`
Expected: FAIL to build — `undefined: fetchFrom`, `undefined: CatalogURL`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/featherless/fetch.go`:

```go
package featherless

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CatalogURL is Featherless's model listing. It answers unauthenticated; a key
// only adds available_on_current_plan.
const CatalogURL = "https://api.featherless.ai/v1/models"

// fetchTimeout covers a ~7MB body over a slow link.
const fetchTimeout = 60 * time.Second

// maxCatalogBytes caps what a wrong URL or a hijacked response can make this
// read into memory. The real catalog is ~7MB.
const maxCatalogBytes = 64 << 20

// Fetch downloads the catalog. The key is optional and travels as a bearer
// token: Featherless answers x-api-key with a 401.
func Fetch(ctx context.Context, key string) ([]Model, error) {
	return fetchFrom(ctx, CatalogURL, key)
}

func fetchFrom(ctx context.Context, url, key string) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("featherless: fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("featherless: catalog returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	if err != nil {
		return nil, fmt.Errorf("featherless: read catalog: %w", err)
	}
	return Parse(data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/featherless/ -v`
Expected: PASS (10 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/featherless/
git commit -m "feat(featherless): fetch the catalog with a bearer credential"
```

---

### Task 5: Cache the catalog

**Files:**
- Create: `internal/featherless/cache.go`
- Create: `internal/featherless/cache_test.go`

**Interfaces:**
- Consumes: `Model` from Task 3.
- Produces: `const CacheTTL = 24 * time.Hour`, `func LoadCache(path string) ([]Model, time.Time, bool)`, `func SaveCache(path string, models []Model) error`, `func Stale(fetchedAt time.Time) bool`, `func DefaultCachePath() string`.

- [ ] **Step 1: Write the failing test**

Create `internal/featherless/cache_test.go`:

```go
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
	if err := SaveCache(path, sampleModels()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bumped := []byte(string(data[:0]) + `{"version":999,"fetched_at":0,"models":[]}`)
	if err := os.WriteFile(path, bumped, 0o644); err != nil {
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/featherless/ -run 'TestCache|TestLoadCache|TestStale|TestDefaultCachePath' -v`
Expected: FAIL to build — `undefined: SaveCache`, `undefined: LoadCache`, `undefined: Stale`, `undefined: CacheTTL`, `undefined: DefaultCachePath`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/featherless/cache.go`:

```go
package featherless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CacheTTL is how long a stored catalog is served without refetching. The list
// changes as Featherless adds models, but never fast enough to be worth a 7MB
// download per picker open.
const CacheTTL = 24 * time.Hour

// cacheVersion guards the stored Model shape. Bump it whenever Model's fields
// change, or an older cache decodes into zero values and the picker fills with
// nameless 0-token rows.
const cacheVersion = 1

type cacheFile struct {
	Version   int     `json:"version"`
	FetchedAt int64   `json:"fetched_at"`
	Models    []Model `json:"models"`
}

// LoadCache reads a stored catalog, reporting when it was fetched. A missing,
// unreadable, unparseable, or differently versioned cache reports ok=false.
func LoadCache(path string) ([]Model, time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	var stored cacheFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, time.Time{}, false
	}
	if stored.Version != cacheVersion || len(stored.Models) == 0 {
		return nil, time.Time{}, false
	}
	return stored.Models, time.Unix(stored.FetchedAt, 0), true
}

// SaveCache stores a catalog, creating the directory if needed. The catalog is
// public data, so it carries no credential and needs no restricted mode.
func SaveCache(path string, models []Model) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{
		Version:   cacheVersion,
		FetchedAt: time.Now().Unix(),
		Models:    models,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Stale reports whether a cache fetched at that instant should be refreshed.
func Stale(fetchedAt time.Time) bool { return time.Since(fetchedAt) > CacheTTL }

// DefaultCachePath is where the picker stores the catalog, beside wisp-deck's
// other configuration. LoadCache and SaveCache take an explicit path so they
// never read the environment themselves.
func DefaultCachePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "wisp-deck", "featherless-models.json")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/featherless/ -v`
Expected: PASS (15 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/featherless/
git commit -m "feat(featherless): cache the catalog with a TTL"
```

---

### Task 6: Search the catalog

**Files:**
- Create: `internal/featherless/search.go`
- Create: `internal/featherless/search_test.go`

**Interfaces:**
- Consumes: `Model` from Task 3.
- Produces: `func Search(models []Model, query string) []Model`.

- [ ] **Step 1: Write the failing test**

Create `internal/featherless/search_test.go`:

```go
package featherless

import "testing"

func searchCorpus() []Model {
	return []Model{
		{ID: "moonshotai/Kimi-K3", Class: "kimi3-2780b", Context: 262144},
		{ID: "zai-org/GLM-5.2", Class: "glm52-753b", Context: 262144},
		{ID: "deepseek-ai/DeepSeek-V4-Flash", Class: "deepseek4-284b", Context: 262144},
		{ID: "unsloth/Llama-3.3-70B-Instruct", Class: "llama33-70b", Context: 32768},
		{ID: "Sao10K/L3.3-70B-Euryale-v2.3", Class: "llama33-70b", Context: 32768},
	}
}

func TestSearch_empty_query_keeps_the_catalog_order(t *testing.T) {
	got := Search(searchCorpus(), "  ")
	if len(got) != 5 || got[0].ID != "moonshotai/Kimi-K3" {
		t.Errorf("empty query changed the list: %v", ids(got))
	}
}

func TestSearch_matches_the_id_case_insensitively(t *testing.T) {
	got := Search(searchCorpus(), "KIMI")
	if len(got) != 1 || got[0].ID != "moonshotai/Kimi-K3" {
		t.Errorf("got %v, want just Kimi-K3", ids(got))
	}
}

// Model ids are namespaced, so someone typing a bare model name is typing the
// part after the slash. That must rank above an incidental match elsewhere.
func TestSearch_ranks_a_name_match_above_a_namespace_match(t *testing.T) {
	corpus := append(searchCorpus(), Model{ID: "llama-org/Some-Other-Model", Class: "x-1b", Context: 8192})
	got := Search(corpus, "llama")
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 matches", ids(got))
	}
	if got[0].ID != "unsloth/Llama-3.3-70B-Instruct" {
		t.Errorf("first = %q, want the model whose name starts with llama", got[0].ID)
	}
}

// A model family is how someone thinks about these ("give me a llama33"), and
// the class is the only place that family name appears for a fine-tune whose id
// does not carry it.
func TestSearch_matches_the_model_class(t *testing.T) {
	got := Search(searchCorpus(), "llama33")
	if len(got) != 2 {
		t.Fatalf("got %v, want both llama33 models", ids(got))
	}
}

func TestSearch_reports_no_match_as_an_empty_list(t *testing.T) {
	if got := Search(searchCorpus(), "nothing-matches-this"); len(got) != 0 {
		t.Errorf("got %v, want none", ids(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/featherless/ -run TestSearch -v`
Expected: FAIL to build — `undefined: Search`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/featherless/search.go`:

```go
package featherless

import (
	"sort"
	"strings"
)

// Search narrows the catalog to models matching the query, preserving the
// catalog's own order within each rank. Ids are namespaced (owner/name), so a
// bare query is nearly always the name half — a match there outranks one in the
// owner or the class.
func Search(models []Model, query string) []Model {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return models
	}
	type ranked struct {
		model Model
		rank  int
		order int
	}
	var hits []ranked
	for i, model := range models {
		id := strings.ToLower(model.ID)
		name := id
		if slash := strings.LastIndex(id, "/"); slash >= 0 {
			name = id[slash+1:]
		}
		rank := -1
		switch {
		case strings.HasPrefix(name, query):
			rank = 0
		case strings.Contains(id, query):
			rank = 1
		case strings.Contains(strings.ToLower(model.Class), query):
			rank = 2
		}
		if rank >= 0 {
			hits = append(hits, ranked{model: model, rank: rank, order: i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return hits[i].order < hits[j].order
	})
	out := make([]Model, len(hits))
	for i, hit := range hits {
		out[i] = hit.model
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/featherless/ -v`
Expected: PASS (20 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/featherless/
git commit -m "feat(featherless): rank catalog search by model name"
```

---

### Task 7: Show and save a Featherless profile's model rows

**Files:**
- Modify: `internal/tui/subscription_modal.go` (`subscriptionDetailRows` ~line 815, `loadSubscriptionDraft` ~line 926, `saveSubscriptionDraft` ~line 1018, `writeSubscriptionCustomFields` ~line 1069, `toggleSubscriptionImages` ~line 1108, `useSubscriptionProfile` ~line 956, `subscriptionDetailLines` ~lines 2195–2245)
- Test: `internal/tui/subscription_modal_featherless_test.go` (create)

**Interfaces:**
- Consumes: `Provider.SuppliesOwnModel()` (Task 1).
- Produces: a Featherless profile whose detail pane shows Model / Context / Images (no Endpoint field) and whose save writes the model to all four aliases.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/subscription_modal_featherless_test.go`:

```go
package tui

import (
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// addFeatherlessProfile appends a Featherless profile and focuses it.
func addFeatherlessProfile(t *testing.T, m *MainMenuModel, name string) string {
	t.Helper()
	file, err := claudeconfig.AddForProvider(m.claudeConfigsList, m.claudeConfigsDir, name, "featherless")
	if err != nil {
		t.Fatal(err)
	}
	m.SetClaudeConfigs(LoadClaudeConfigsList(m.claudeConfigsList))
	m.openSubscriptionModal()
	for steps := 0; m.subscriptionModalProfile().File != file; steps++ {
		if steps > len(m.subscriptionProfiles()) {
			t.Fatalf("profile %q not reachable in the modal", file)
		}
		m.moveSubscriptionProfile(1)
	}
	return file
}

// The endpoint ships with the provider, so offering a text field for it invites
// someone to break a working profile. The other three rows are the pick's.
func TestFeatherlessProfile_offers_model_context_and_images_but_no_endpoint(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless Kimi-K3")

	rows := m.subscriptionDetailRows()
	want := map[int]bool{
		subscriptionDetailModel:   false,
		subscriptionDetailContext: false,
		subscriptionDetailImages:  false,
	}
	for _, row := range rows {
		if row == subscriptionDetailEndpoint {
			t.Error("featherless must not offer an endpoint field: the provider supplies it")
		}
		if _, ok := want[row]; ok {
			want[row] = true
		}
	}
	for row, seen := range want {
		if !seen {
			t.Errorf("detail row %d missing for a featherless profile", row)
		}
	}
}

// WriteModelMappings writes the four aliases from the draft's model list, which
// is empty for a remote-catalog provider — so running it deletes the picked
// model on every save. This is the same defect the custom provider was fixed for.
func TestFeatherlessSave_keeps_the_picked_model_on_all_four_aliases(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addFeatherlessProfile(t, m, "Featherless Kimi-K3")

	draft := &m.subscriptionModal.draft
	draft.model = "moonshotai/Kimi-K3"
	draft.window = "262144"
	draft.customEdited = true
	draft.dirty = true
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}

	env := readConfigEnv(t, m.claudeConfigsDir, file)
	for _, key := range []string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
	} {
		if got, _ := env[key].(string); got != "moonshotai/Kimi-K3" {
			t.Errorf("%s = %q, want the picked model", key, got)
		}
	}
	if got, _ := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"].(string); got != "262144" {
		t.Errorf("declared window = %q, want 262144", got)
	}
	if got, _ := env["ANTHROPIC_BASE_URL"].(string); got != "https://api.featherless.ai" {
		t.Errorf("base URL = %q, want the provider's own", got)
	}
}

// Most Featherless models are text-only, and an image sent to one fails the
// turn, so the toggle must be reachable on this provider too.
func TestFeatherlessProfile_can_block_image_reads(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	file := addFeatherlessProfile(t, m, "Featherless GLM")

	m.subscriptionModal.draft.model = "zai-org/GLM-5.2"
	m.subscriptionModal.draft.window = "262144"
	m.subscriptionModal.detailCursor = subscriptionDetailImages
	m.toggleSubscriptionImages()
	if !m.subscriptionModal.draft.imagesBlocked {
		t.Fatal("toggling images on a featherless profile did nothing")
	}
	m.saveSubscriptionDraft()
	if m.subscriptionModal.err != nil {
		t.Fatalf("save: %v", m.subscriptionModal.err)
	}
	if !claudeconfig.ReadImagesBlocked(m.claudeConfigsDir, file) {
		t.Error("the deny rules were not written")
	}
}

// The unready message names what to do next, and for featherless that is never
// an endpoint.
func TestFeatherlessUnreadyMessage_does_not_ask_for_an_endpoint(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless Kimi-K3")
	m.useSubscriptionProfile()
	if m.subscriptionModal.err == nil {
		t.Fatal("an unconfigured featherless profile must refuse to be used")
	}
	if got := m.subscriptionModal.err.Error(); strings.Contains(got, "endpoint") {
		t.Errorf("message asks for an endpoint the provider supplies: %q", got)
	}
}
```

Add a small helper at the bottom of the same file (the custom-provider test file has an equivalent; keep this one local to avoid touching it):

```go
func readConfigEnv(t *testing.T, dir, file string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		t.Fatalf("config %s has no env section", file)
	}
	return env
}
```

Imports for this file: `encoding/json`, `os`, `path/filepath`, `strings`, `testing`, and `github.com/jackuait/wisp-deck/internal/claudeconfig`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestFeatherless -v`
Expected: FAIL — the detail rows come back as the four alias-cycler rows, and the save writes the (empty) mappings instead of the model.

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/subscription_modal.go`:

`subscriptionDetailRows` — replace the `if profile.Provider.UserConfigured` block:

```go
	if profile.Provider.SuppliesOwnModel() {
		rows = nil
		if profile.Provider.UserConfigured {
			rows = append(rows, subscriptionDetailEndpoint)
		}
		rows = append(rows,
			subscriptionDetailModel,
			subscriptionDetailContext,
			subscriptionDetailImages,
		)
	}
```

`loadSubscriptionDraft` — widen the read gate:

```go
		if profile.Provider.SuppliesOwnModel() {
			draft.endpoint = claudeconfig.ReadBaseURL(m.claudeConfigsDir, profile.File)
			draft.model = claudeconfig.ReadCustomModel(m.claudeConfigsDir, profile.File)
			draft.window = claudeconfig.ReadContextWindow(m.claudeConfigsDir, profile.File)
			draft.imagesBlocked = claudeconfig.ReadImagesBlocked(m.claudeConfigsDir, profile.File)
		}
```

`saveSubscriptionDraft` — widen the branch that skips `WriteModelMappings`:

```go
	if profile.Provider.SuppliesOwnModel() {
		if err := m.writeSubscriptionCustomFields(draft, profile.Provider); err != nil {
```

`writeSubscriptionCustomFields` — take the provider and skip the endpoint write when the provider supplies it:

```go
func (m *MainMenuModel) writeSubscriptionCustomFields(
	draft *subscriptionDraft, provider claudeconfig.Provider,
) error {
	if !draft.customEdited {
		return nil
	}
	if provider.UserConfigured {
		if err := claudeconfig.ValidateCustomEndpoint(draft.endpoint); err != nil {
			return err
		}
	}
	if err := claudeconfig.ValidateCustomModel(draft.model); err != nil {
		return err
	}
	if err := claudeconfig.ValidateCustomContextWindow(draft.window); err != nil {
		return err
	}
	if provider.UserConfigured {
		if err := claudeconfig.WriteCustomEndpoint(m.claudeConfigsDir, draft.file, draft.endpoint); err != nil {
			return err
		}
	}
	if err := claudeconfig.WriteCustomModel(m.claudeConfigsDir, draft.file, draft.model); err != nil {
		return err
	}
	if err := claudeconfig.WriteCustomContextWindow(m.claudeConfigsDir, draft.file, draft.window); err != nil {
		return err
	}
	if err := claudeconfig.WriteImagesBlocked(
		m.claudeConfigsDir, draft.file, draft.imagesBlocked); err != nil {
		return err
	}
	// Self-heals a profile created before the watchdog was disarmed, without
	// waiting for the next installer sweep. A no-op for featherless, which
	// keeps the watchdog armed.
	if _, err := claudeconfig.EnsureByteWatchdog(m.claudeConfigsDir, draft.file); err != nil {
		return err
	}
	draft.customEdited = false
	return nil
}
```

`toggleSubscriptionImages` — widen its guard:

```go
	if profile.Standard || !profile.Provider.SuppliesOwnModel() {
		return
	}
```

`useSubscriptionProfile` — split the unready message so it never names an endpoint the provider supplies:

```go
		switch {
		case profile.Provider.RemoteCatalog:
			m.subscriptionModal.err = fmt.Errorf(
				"%s needs a model and an API key before it can be used", profile.Name)
		case profile.Provider.UserConfigured:
			m.subscriptionModal.err = fmt.Errorf(
				"%s needs an endpoint, model, context window, and API key before it can be used",
				profile.Name,
			)
		default:
			m.subscriptionModal.err = fmt.Errorf("%s needs an API key before it can be used", profile.Name)
		}
```

`subscriptionDetailLines` — the endpoint row stays read-only unless the provider is user-configured (no change needed there, it already gates on `UserConfigured`), but the MODEL ROUTING block must render for both. Change:

```go
	if profile.Provider.SuppliesOwnModel() {
```

for the block that renders `subscriptionDetailModel` / `subscriptionDetailContext` / images.

Finally, the mouse hit-test (~line 2480) gates the same rows. Replace:

```go
	if m.subscriptionModalProfile().Provider.UserConfigured {
		for _, row := range []int{
			subscriptionDetailEndpoint, subscriptionDetailModel, subscriptionDetailContext,
		} {
```

with:

```go
	if m.subscriptionModalProfile().Provider.SuppliesOwnModel() {
		for _, row := range []int{
			subscriptionDetailEndpoint, subscriptionDetailModel, subscriptionDetailContext,
		} {
```

The row list itself needs no change: `subscriptionFieldSpec` is consulted per row and the endpoint row simply is not rendered for a remote-catalog provider, so a click can never land on it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestFeatherless -v`
Expected: PASS (4 tests).

Then confirm the custom provider did not regress:

Run: `go test ./internal/tui/ -run 'TestSubscriptionModal' `
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(subscriptions): route Featherless through the own-model save path"
```

---

### Task 8: The model picker

**Files:**
- Create: `internal/tui/subscription_model_picker.go`
- Create: `internal/tui/subscription_model_picker_test.go`
- Modify: `internal/tui/subscription_modal.go` (mode constants ~line 62, `subscriptionModalState` ~line 172, `updateSubscriptionModal` ~line 531, `activateSubscriptionDetail` ~line 1267)
- Modify: `internal/tui/mainmenu.go` (add the `featherlessCachePath` field to `MainMenuModel`)

**Interfaces:**
- Consumes: `featherless.Model`, `featherless.Search`, `featherless.Fetch`, `featherless.LoadCache`, `featherless.SaveCache`, `featherless.Stale`, `featherless.DefaultCachePath` (Tasks 3–6).
- Produces: mode `subscriptionPickModel`; `func (m *MainMenuModel) openSubscriptionModelPicker() tea.Cmd`; `func (m *MainMenuModel) updateSubscriptionModelPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd)`; `type featherlessCatalogMsg struct{ models []featherless.Model; err error }`; `func (m *MainMenuModel) applyFeatherlessPick(model featherless.Model)`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/subscription_model_picker_test.go`:

```go
package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/featherless"
)

func pickerCorpus() []featherless.Model {
	return []featherless.Model{
		{ID: "moonshotai/Kimi-K3", Class: "kimi3-2780b", Context: 262144, InPerM: 3, OutPerM: 15, ImageInput: true, OnPlan: true},
		{ID: "zai-org/GLM-5.2", Class: "glm52-753b", Context: 262144, InPerM: 0.75, OutPerM: 2.4, OnPlan: true},
		{ID: "unsloth/Llama-3.3-70B-Instruct", Class: "llama33-70b", Context: 32768, InPerM: 2.6, OutPerM: 3, OnPlan: true},
		{ID: "gated/Off-Plan-Model", Class: "x-70b", Context: 32768, InPerM: 1, OutPerM: 1, OnPlan: false},
	}
}

// The catalog is a 7MB HTTP round trip: fetching it inside Update would freeze
// the menu, so the picker opens in a loading state and is filled by a message.
func TestModelPicker_opens_loading_and_fills_from_the_catalog_message(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")

	cmd := m.openSubscriptionModelPicker()
	if cmd == nil {
		t.Fatal("opening the picker must return a fetch command")
	}
	if m.subscriptionModal.mode != subscriptionPickModel {
		t.Fatal("the picker did not become the active mode")
	}
	if !m.subscriptionModal.picker.loading {
		t.Error("the picker must show a loading state until the catalog arrives")
	}

	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	if m.subscriptionModal.picker.loading {
		t.Error("the catalog arrived; loading must clear")
	}
	if len(m.subscriptionModal.picker.filtered) != 4 {
		t.Errorf("filtered = %d, want the whole corpus", len(m.subscriptionModal.picker.filtered))
	}
}

func TestModelPicker_typing_narrows_the_list(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})

	for _, r := range "kimi" {
		m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.subscriptionModal.picker.filtered; len(got) != 1 || got[0].ID != "moonshotai/Kimi-K3" {
		t.Fatalf("filtered = %v, want just Kimi-K3", got)
	}
	if m.subscriptionModal.picker.cursor != 0 {
		t.Error("narrowing must reset the cursor into the visible list")
	}
}

// The pick is what declares the context window; without it the session falls
// back to a flat 200000 that strands a 32768 model permanently.
func TestModelPicker_enter_stamps_model_window_and_images(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})

	// Second row: GLM-5.2, which declares no image_input.
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyDown})
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})

	draft := m.subscriptionModal.draft
	if draft.model != "zai-org/GLM-5.2" {
		t.Errorf("model = %q, want the highlighted row", draft.model)
	}
	if draft.window != "262144" {
		t.Errorf("window = %q, want the model's context_length", draft.window)
	}
	if !draft.imagesBlocked {
		t.Error("a model with no image_input must default to blocking image reads")
	}
	if !draft.customEdited || !draft.dirty {
		t.Error("a pick must mark the draft dirty so it can be saved")
	}
	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Error("the picker must close on Enter")
	}
}

func TestModelPicker_a_vision_model_leaves_images_enabled(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.draft.imagesBlocked {
		t.Error("Kimi-K3 accepts images; blocking reads would remove a working capability")
	}
}

// A plan that will not run the model produces a turn that fails on every send,
// so it is visible but unpickable.
func TestModelPicker_refuses_a_model_that_is_off_the_plan(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})

	for i := 0; i < 3; i++ {
		m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEnter})

	if m.subscriptionModal.draft.model == "gated/Off-Plan-Model" {
		t.Error("an off-plan model must not be pickable")
	}
	if m.subscriptionModal.picker.err == nil {
		t.Error("refusing a pick must say why")
	}
}

func TestModelPicker_esc_closes_without_picking(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{models: pickerCorpus()})
	m.updateSubscriptionModelPicker(tea.KeyMsg{Type: tea.KeyEsc})

	if m.subscriptionModal.mode != subscriptionBrowse {
		t.Error("Esc must return to browsing")
	}
	if m.subscriptionModal.draft.model != "" {
		t.Error("Esc must not pick anything")
	}
}

// A catalog that cannot be fetched and has no cache must say so and stay
// dismissable, not sit on a spinner forever.
func TestModelPicker_reports_a_fetch_failure(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	addFeatherlessProfile(t, m, "Featherless")
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModelPicker()
	m.Update(featherlessCatalogMsg{err: errFetchFailed})

	if m.subscriptionModal.picker.loading {
		t.Error("a failed fetch must clear the loading state")
	}
	if m.subscriptionModal.picker.err == nil {
		t.Error("a failed fetch must be reported")
	}
}
```

Add at the top of that file, after the imports:

```go
var errFetchFailed = errors.New("featherless: catalog returned 502 Bad Gateway")
```

with `"errors"` imported.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestModelPicker -v`
Expected: FAIL to build — `undefined: subscriptionPickModel`, `openSubscriptionModelPicker`, `featherlessCatalogMsg`, `m.featherlessCachePath`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/subscription_modal.go`, append to the mode constants (order does not matter; they are not persisted):

```go
	subscriptionPickModel
```

Add to `subscriptionModalState`:

```go
	picker subscriptionModelPickerState
```

In `internal/tui/mainmenu.go`, add to `MainMenuModel`:

```go
	// Overridden in tests; empty means featherless.DefaultCachePath().
	featherlessCachePath string
```

Create `internal/tui/subscription_model_picker.go`:

```go
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/featherless"
)

type subscriptionModelPickerState struct {
	models   []featherless.Model
	filtered []featherless.Model
	query    textinput.Model
	cursor   int
	offset   int
	loading  bool
	err      error
}

// featherlessCatalogMsg delivers the catalog fetched off the Update loop.
type featherlessCatalogMsg struct {
	models []featherless.Model
	err    error
}

func (m *MainMenuModel) featherlessCache() string {
	if m.featherlessCachePath != "" {
		return m.featherlessCachePath
	}
	return featherless.DefaultCachePath()
}

// featherlessKey returns any stored Featherless credential, so the catalog is
// fetched authenticated when possible and reports available_on_current_plan.
func (m *MainMenuModel) featherlessKey() string {
	if key := strings.TrimSpace(m.subscriptionModal.draft.apiKey); key != "" {
		return key
	}
	for _, config := range m.claudeConfigs {
		provider := m.claudeConfigProvider(config)
		if !provider.RemoteCatalog {
			continue
		}
		if key := strings.TrimSpace(claudeconfig.ReadAPIKey(m.claudeConfigsDir, config.File)); key != "" {
			return key
		}
	}
	return ""
}

// openSubscriptionModelPicker enters the picker and starts the fetch. The
// catalog is a ~7MB round trip, so it never runs inside Update.
func (m *MainMenuModel) openSubscriptionModelPicker() tea.Cmd {
	query := textinput.New()
	query.Placeholder = "search 15,000+ tool-calling models"
	query.Width = 36
	query.Focus()

	m.subscriptionModal.mode = subscriptionPickModel
	m.subscriptionModal.picker = subscriptionModelPickerState{query: query, loading: true}
	m.subscriptionModal.err = nil

	path := m.featherlessCache()
	key := m.featherlessKey()
	return func() tea.Msg {
		if models, fetchedAt, ok := featherless.LoadCache(path); ok && !featherless.Stale(fetchedAt) {
			return featherlessCatalogMsg{models: models}
		}
		models, err := featherless.Fetch(context.Background(), key)
		if err != nil {
			// A stale list beats no list: the picker stays usable offline.
			if cached, _, ok := featherless.LoadCache(path); ok {
				return featherlessCatalogMsg{models: cached}
			}
			return featherlessCatalogMsg{err: err}
		}
		_ = featherless.SaveCache(path, models)
		return featherlessCatalogMsg{models: models}
	}
}

// applyFeatherlessCatalog fills the picker from a delivered catalog.
func (m *MainMenuModel) applyFeatherlessCatalog(msg featherlessCatalogMsg) {
	picker := &m.subscriptionModal.picker
	picker.loading = false
	if msg.err != nil {
		picker.err = msg.err
		return
	}
	picker.err = nil
	picker.models = msg.models
	picker.filtered = featherless.Search(msg.models, picker.query.Value())
	picker.cursor = 0
	picker.offset = 0
}

func (m *MainMenuModel) updateSubscriptionModelPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := &m.subscriptionModal.picker
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.subscriptionModal.mode = subscriptionBrowse
		picker.query.Blur()
		return m, nil
	case tea.KeyUp:
		if picker.cursor > 0 {
			picker.cursor--
		}
		return m, nil
	case tea.KeyDown:
		if picker.cursor < len(picker.filtered)-1 {
			picker.cursor++
		}
		return m, nil
	case tea.KeyEnter:
		if picker.cursor < 0 || picker.cursor >= len(picker.filtered) {
			return m, nil
		}
		model := picker.filtered[picker.cursor]
		if !model.OnPlan {
			picker.err = fmt.Errorf("%s is not available on this Featherless plan", model.ID)
			return m, nil
		}
		m.applyFeatherlessPick(model)
		m.subscriptionModal.mode = subscriptionBrowse
		picker.query.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	picker.query, cmd = picker.query.Update(msg)
	picker.filtered = featherless.Search(picker.models, picker.query.Value())
	// The list shrank under the cursor; leaving it where it was would highlight
	// a row that is no longer there.
	picker.cursor = 0
	picker.offset = 0
	return m, cmd
}

// applyFeatherlessPick writes the chosen model onto the draft. The window comes
// from the catalog because an undeclared one falls back to a flat 200000, which
// strands a smaller model with no way to compact out of it. The images default
// follows the model's own image_input: sending an image to a text-only endpoint
// fails the turn.
func (m *MainMenuModel) applyFeatherlessPick(model featherless.Model) {
	draft := &m.subscriptionModal.draft
	draft.model = model.ID
	draft.window = fmt.Sprintf("%d", model.Context)
	draft.imagesBlocked = !model.ImageInput
	draft.customEdited = true
	draft.dirty = true
	m.subscriptionModal.err = nil
}
```

In `updateSubscriptionModal`, route the new mode before the existing mode checks (beside the `subscriptionAddProvider` branch at ~line 538):

```go
	if m.subscriptionModal.mode == subscriptionPickModel {
		return m.updateSubscriptionModelPicker(msg)
	}
```

In the menu's `Update`, handle the message (add a case beside the other custom message types):

```go
	case featherlessCatalogMsg:
		m.applyFeatherlessCatalog(msg)
		return m, nil
```

In `activateSubscriptionDetail`, send the Model row to the picker for a remote-catalog provider:

```go
	case subscriptionDetailModel:
		if m.subscriptionModalProfile().Provider.RemoteCatalog {
			return m, m.openSubscriptionModelPicker()
		}
		return m, m.beginSubscriptionFieldEdit(m.subscriptionModal.detailCursor)
	case subscriptionDetailEndpoint, subscriptionDetailContext:
		return m, m.beginSubscriptionFieldEdit(m.subscriptionModal.detailCursor)
```

(replacing the combined `case subscriptionDetailEndpoint, subscriptionDetailModel, subscriptionDetailContext:` line).

Render the picker. In `subscriptionDetailLines`, immediately after the existing
mode `switch` that delegates to `subscriptionLifecycleLines`, add:

```go
	if m.subscriptionModal.mode == subscriptionPickModel {
		return m.subscriptionModelPickerLines(width, height)
	}
```

and add the renderer to `internal/tui/subscription_model_picker.go`:

```go
// subscriptionModelPickerLines renders the picker: a query field over the
// matching models. Styles are built here rather than held in package vars — a
// style built at init time binds the pre-tty Ascii renderer and comes out
// colorless.
func (m *MainMenuModel) subscriptionModelPickerLines(width, height int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	accent := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	picker := &m.subscriptionModal.picker

	lines := []string{
		accent.Render("Pick a Featherless model"),
		"",
		modalTruncate(picker.query.View(), width),
		"",
	}
	switch {
	case picker.loading:
		return append(lines, dim.Render("Loading catalog…"))
	case picker.err != nil:
		return append(lines,
			amber.Render(modalTruncate(picker.err.Error(), width)),
			"",
			dim.Render("Esc to go back"))
	case len(picker.filtered) == 0:
		return append(lines, dim.Render("No tool-calling model matches that."))
	}

	rows := make([]string, 0, len(picker.filtered))
	for i, model := range picker.filtered {
		row := fmt.Sprintf("%s  %dK  $%g/$%g per 1M",
			model.ID, model.Context/1024, model.InPerM, model.OutPerM)
		if !model.OnPlan {
			row += "  (not on plan)"
		}
		row = modalTruncate(row, width-2)
		switch {
		case i == picker.cursor:
			rows = append(rows, accent.Render("> "+row))
		case !model.OnPlan:
			rows = append(rows, dim.Render("  "+row))
		default:
			rows = append(rows, "  "+row)
		}
	}
	// Keep the cursor on screen: the list is thousands of rows long.
	visible := height - len(lines)
	if visible < 1 {
		visible = 1
	}
	if picker.cursor < picker.offset {
		picker.offset = picker.cursor
	}
	if picker.cursor >= picker.offset+visible {
		picker.offset = picker.cursor - visible + 1
	}
	return append(lines, modalWindow(rows, picker.offset, visible, width)...)
}
```

Add `"github.com/charmbracelet/lipgloss"` to that file's imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestModelPicker -v`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(subscriptions): add a searchable Featherless model picker"
```

---

### Task 9: Make it a few clicks — picker on add, name prefill, key reuse

**Files:**
- Modify: `internal/tui/subscription_modal.go` (`beginSubscriptionAddName` ~line 1355, `addSubscriptionProfile` ~line 1444)
- Modify: `internal/tui/subscription_model_picker.go` (pending-pick handling)
- Test: `internal/tui/subscription_model_picker_test.go` (append)

**Interfaces:**
- Consumes: everything from Tasks 7 and 8.
- Produces: `subscriptionModalState.pendingModel *featherless.Model`; `func featherlessProfileName(modelID string) string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/subscription_model_picker_test.go`:

```go
// Choosing Featherless with nothing else configured should land on the picker,
// not on a name prompt for a profile with no model.
func TestAddFeatherless_opens_the_picker_before_naming(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	m.openSubscriptionModal()
	m.startSubscriptionAdd()
	for i, provider := range claudeconfig.Providers {
		if provider.Key == "featherless" {
			m.subscriptionModal.providerCursor = i
		}
	}
	m.beginSubscriptionAddName()

	if m.subscriptionModal.mode != subscriptionPickModel {
		t.Fatal("picking Featherless must open the model picker first")
	}
}

// The name is derived from the model so a user can add several Featherless
// profiles without inventing names for them.
func TestFeatherlessProfileName_is_derived_from_the_model(t *testing.T) {
	for model, want := range map[string]string{
		"moonshotai/Kimi-K3":            "Featherless Kimi-K3",
		"zai-org/GLM-5.2":               "Featherless GLM-5.2",
		"unsloth/Llama-3.3-70B-Instruct": "Featherless Llama-3.3-70B-Instruct",
	} {
		if got := featherlessProfileName(model); got != want {
			t.Errorf("featherlessProfileName(%q) = %q, want %q", model, got, want)
		}
	}
}

// Adding a second model should not mean typing the key again.
func TestAddFeatherless_reuses_a_sibling_profiles_key(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.featherlessCachePath = filepath.Join(t.TempDir(), "featherless-models.json")
	existing := addFeatherlessProfile(t, m, "Featherless Kimi-K3")
	if err := claudeconfig.WriteAPIKey(m.claudeConfigsDir, existing, "rc_shared"); err != nil {
		t.Fatal(err)
	}
	m.SetClaudeConfigs(LoadClaudeConfigsList(m.claudeConfigsList))

	m.subscriptionModal.providerKey = "featherless"
	m.subscriptionModal.pendingModel = &featherless.Model{
		ID: "zai-org/GLM-5.2", Context: 262144, OnPlan: true,
	}
	m.addSubscriptionProfile("Featherless GLM-5.2")
	if m.subscriptionModal.err != nil {
		t.Fatalf("add: %v", m.subscriptionModal.err)
	}

	file := m.subscriptionModalProfile().File
	if got := claudeconfig.ReadAPIKey(m.claudeConfigsDir, file); got != "rc_shared" {
		t.Errorf("key = %q, want the sibling profile's key reused", got)
	}
	if got := claudeconfig.ReadCustomModel(m.claudeConfigsDir, file); got != "zai-org/GLM-5.2" {
		t.Errorf("model = %q, want the pending pick applied", got)
	}
	if got := claudeconfig.ReadContextWindow(m.claudeConfigsDir, file); got != "262144" {
		t.Errorf("window = %q, want the pick's context length", got)
	}
	if !m.subscriptionModalProfile().Ready {
		t.Error("model + window + reused key must make the profile ready immediately")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestAddFeatherless|TestFeatherlessProfileName' -v`
Expected: FAIL to build — `undefined: featherlessProfileName`, `m.subscriptionModal.pendingModel`.

- [ ] **Step 3: Write minimal implementation**

Add to `subscriptionModalState`:

```go
	// A model picked before the profile exists, applied once it does.
	pendingModel *featherless.Model
```

In `internal/tui/subscription_model_picker.go`, add:

```go
// featherlessProfileName derives a profile name from a model id. Ids are
// namespaced (owner/name) and the owner is noise in a profile list.
func featherlessProfileName(modelID string) string {
	name := modelID
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return "Featherless " + name
}
```

In `applyFeatherlessPick`, when the modal is mid-add (no draft file yet), record the pick and move on to naming instead of writing a draft:

```go
func (m *MainMenuModel) applyFeatherlessPick(model featherless.Model) {
	if m.subscriptionModal.draft.file == "" {
		m.subscriptionModal.pendingModel = &model
		m.subscriptionModal.input = m.newSubscriptionNameInput(featherlessProfileName(model.ID))
		m.enterSubscriptionLifecycle(subscriptionAddName)
		m.subscriptionModal.err = nil
		return
	}
	draft := &m.subscriptionModal.draft
	draft.model = model.ID
	draft.window = fmt.Sprintf("%d", model.Context)
	draft.imagesBlocked = !model.ImageInput
	draft.customEdited = true
	draft.dirty = true
	m.subscriptionModal.err = nil
}
```

Because that path sets the mode itself, remove the unconditional `m.subscriptionModal.mode = subscriptionBrowse` from the picker's Enter branch and let `applyFeatherlessPick` decide:

```go
	case tea.KeyEnter:
		...
		picker.query.Blur()
		wasAdding := m.subscriptionModal.draft.file == ""
		m.applyFeatherlessPick(model)
		if !wasAdding {
			m.subscriptionModal.mode = subscriptionBrowse
		}
		return m, nil
```

In `beginSubscriptionAddName`, divert a remote-catalog provider to the picker:

```go
	provider := claudeconfig.Providers[m.subscriptionModal.providerCursor]
	m.subscriptionModal.providerKey = provider.Key
	if provider.RemoteCatalog {
		m.subscriptionModal.draft = subscriptionDraft{}
		m.subscriptionModal.pendingModel = nil
		return m.openSubscriptionModelPicker()
	}
```

In `addSubscriptionProfile`, after `claudeconfig.AddForProvider` succeeds and before `m.reloadSubscriptionConfigs()`, apply the pending pick and reuse a sibling key:

```go
	if pick := m.subscriptionModal.pendingModel; pick != nil {
		if err := claudeconfig.WriteCustomModel(m.claudeConfigsDir, file, pick.ID); err != nil {
			m.subscriptionModal.err = err
			return
		}
		if err := claudeconfig.WriteCustomContextWindow(
			m.claudeConfigsDir, file, fmt.Sprintf("%d", pick.Context)); err != nil {
			m.subscriptionModal.err = err
			return
		}
		if err := claudeconfig.WriteImagesBlocked(
			m.claudeConfigsDir, file, !pick.ImageInput); err != nil {
			m.subscriptionModal.err = err
			return
		}
		// A second model on the same account should not mean typing the key
		// again; featherlessKey reads it from a sibling profile.
		if key := m.featherlessKey(); key != "" {
			if err := claudeconfig.WriteAPIKey(m.claudeConfigsDir, file, key); err != nil {
				m.subscriptionModal.err = err
				return
			}
		}
		m.subscriptionModal.pendingModel = nil
	}
```

Note `featherlessKey()` reads `m.subscriptionModal.draft.apiKey` first; the add flow resets the draft, so it falls through to the sibling scan. Add `"fmt"` to the file's imports if it is not already there.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestAddFeatherless|TestFeatherlessProfileName|TestModelPicker|TestFeatherless' -v`
Expected: PASS.

Then the whole package:

Run: `go test ./internal/tui/`
Expected: PASS (ok).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(subscriptions): pick a Featherless model during add and reuse the key"
```

---

### Task 10: Live end-to-end guard, docs, and full verification

**Files:**
- Create: `internal/featherless/live_e2e_test.go`
- Modify: `CLAUDE.md` (the Commands block, and a new architecture section)

**Interfaces:**
- Consumes: `Fetch` (Task 4).
- Produces: `WISP_DECK_LIVE_FEATHERLESS_E2E=1` gated test.

- [ ] **Step 1: Write the failing test**

Create `internal/featherless/live_e2e_test.go`:

```go
package featherless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// This is the only check that can see Featherless changing its own side. Two
// things this integration rests on are undocumented and could be withdrawn
// without warning:
//
//   - the Anthropic Messages route at /v1/messages, which is what lets Claude
//     Code talk to Featherless with no translating proxy at all, and
//   - the ": keep-alive" SSE comments emitted while awaiting the first token,
//     which are the only reason a cold model load does not trip Claude Code's
//     20s byte-stall watchdog and get the turn aborted and replayed.
//
// Run after a Featherless-side change is suspected:
//
//	WISP_DECK_LIVE_FEATHERLESS_E2E=1 FEATHERLESS_API_KEY=... \
//	  go test ./internal/featherless/ -run TestLiveFeatherless -v
func TestLiveFeatherlessCatalogAndAnthropicRoute(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_FEATHERLESS_E2E") != "1" {
		t.Skip("set WISP_DECK_LIVE_FEATHERLESS_E2E=1 to run")
	}
	key := os.Getenv("FEATHERLESS_API_KEY")
	if key == "" {
		t.Skip("set FEATHERLESS_API_KEY to run")
	}

	models, err := Fetch(context.Background(), key)
	if err != nil {
		t.Fatalf("fetch catalog: %v", err)
	}
	if len(models) < 1000 {
		t.Errorf("catalog returned %d tool-calling models, want thousands", len(models))
	}
	if models[0].Context < 100000 {
		t.Errorf("widest model has a %d context; the frontier tier is missing", models[0].Context)
	}

	body, _ := json.Marshal(map[string]any{
		"model":      models[0].ID,
		"max_tokens": 64,
		"stream":     true,
		"messages":   []any{map[string]string{"role": "user", "content": "What is the weather in Paris? Use the tool."}},
		"tools": []any{map[string]any{
			"name":        "get_weather",
			"description": "Get the current weather in a city",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]string{"type": "string"}},
				"required":   []string{"city"},
			},
		}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "https://api.featherless.ai/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/messages: %s", resp.Status)
	}

	var sawToolUse, sawKeepAlive bool
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ":") {
			sawKeepAlive = true
		}
		if strings.Contains(line, `"tool_use"`) {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Error("no tool_use block: Claude Code cannot read or edit a file without one")
	}
	if !sawKeepAlive {
		t.Error("no keep-alive comments: a cold model load will now trip Claude Code's 20s byte watchdog")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/featherless/ -run TestLiveFeatherless -v`
Expected: SKIP (`set WISP_DECK_LIVE_FEATHERLESS_E2E=1 to run`) — this confirms the gate. Then run it for real with the env vars set and confirm PASS.

- [ ] **Step 3: Document it**

Add to `CLAUDE.md`'s Commands block:

```
WISP_DECK_LIVE_FEATHERLESS_E2E=1 FEATHERLESS_API_KEY=... go test ./internal/featherless/ -run TestLiveFeatherless -v  # After a Featherless-side change is suspected: verify /v1/messages still speaks Anthropic and still emits ": keep-alive" comments (costs one short turn)
```

Add a new architecture section to `CLAUDE.md`, after the self-hosted sections, in the house style — the finding, then the rules that fell out of it:

```markdown
### Featherless speaks Anthropic natively, and keeps its own socket warm

Featherless is documented as an OpenAI-compatible provider, which by the rule
above would need a translating proxy. It does not: `POST /v1/messages` is a real
Anthropic Messages route, verified live — it answers unauth with Anthropic's
error envelope where `/v1/chat/completions` answers with OpenAI's and an unknown
path answers with a fastify 404, and with a key it streams
`content_block_start{tool_use}` → `input_json_delta` → `stop_reason:"tool_use"`.
So the provider is an ordinary API-key gateway at `https://api.featherless.ai`.

- **The credential is `ANTHROPIC_AUTH_TOKEN`, never `ANTHROPIC_API_KEY`.**
  Featherless answers `x-api-key` with a 401 and `Authorization: Bearer` with a
  200.
- **The byte watchdog stays ARMED here.** Featherless sends
  `: keep-alive (awaiting first token)` SSE comments — measured every ~1.2s
  across a 12s cold model load, worst byte silence 4.8s against the watchdog's
  20s trigger — so the stream is never silent long enough to trip it, and
  disarming would trade a real dead-connection signal for nothing. It sends no
  `event: ping` at all, so those comments are the whole mechanism:
  `TestLiveFeatherlessCatalogAndAnthropicRoute` is what notices if they stop.
- **Only tool-calling models are offered.** 15,571 of the 21,908 report
  `features.tool_use`; the rest produce a pane that cannot read or edit a single
  file, so `Parse` drops them, along with any model declaring no
  `context_length` — an undeclared window falls back to the flat 200000 that
  strands a 32768 model permanently.
- **`available_on_current_plan` is absent on an unauthenticated listing**, and
  absent must read as available, or the picker is empty until a key is typed.
- **`is_gated` is about HuggingFace, not Featherless.**
  `meta-llama/Llama-3.3-70B-Instruct` is gated and serves normally, so it never
  blocks a pick.
- **`RemoteCatalog` shares `UserConfigured`'s save path** via
  `Provider.SuppliesOwnModel()`: both must skip `WriteModelMappings`, which
  writes the four aliases from an empty model list and so deletes the picked
  model on every save.
- **Featherless sits ahead of the moonshot entries in `Providers`.** Profiles are
  named after the picked model, so "Featherless Kimi-K3" is routine — and it
  contains moonshot's `kimi` alias, which substring matching in slice order
  would otherwise win.

Guarded by `internal/claudeconfig/featherless_provider_test.go`,
`internal/featherless/*_test.go`, and
`internal/tui/subscription_model_picker_test.go`.
```

- [ ] **Step 4: Full verification**

Run the whole suite the way CI reads it:

```bash
go test -json ./... 2>&1 | tee /tmp/out.json >/dev/null
go run ./cmd/ci-report --title "Featherless subscriptions" /tmp/out.json
```

Expected: no failures. If `./test/bash/...` is included, give it room: `go test -timeout 20m ./test/bash/...` (the default 10m kills the package and reads as a hang).

No shell scripts are modified by this plan. If any were touched, run:

```bash
shellcheck <only the modified .sh files>
```

- [ ] **Step 5: Commit and push**

```bash
git add internal/featherless/live_e2e_test.go CLAUDE.md
git commit -m "docs: record how Featherless speaks Anthropic and keeps its socket warm"
git pull --rebase
git push
git status   # MUST show "up to date with origin"
```

---

## Notes for the executor

- **The 32768 majority is not a bug.** 13,710 of the 15,571 tool-calling models
  have a 32768 window. The default ordering (context desc) is what keeps the
  frontier tier on screen; do not "fix" the ordering to alphabetical.
- **Do not add a `/v1/models/{id}` detail fetch.** Everything the picker needs —
  context, price, `tool_use`, `image_input`, plan availability — is on the list
  endpoint. The detail endpoint is one request per model.
- **Do not use `?capabilities=tool_use`.** The documented filter returns an empty
  set; filtering happens in `Parse`.
- **If a step's code does not compile against the real file**, the surrounding
  code has moved — re-read the function and adapt, keeping the behavior the test
  demands. Do not weaken a test to match the code.
