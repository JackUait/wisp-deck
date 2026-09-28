# All-In Fast Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Claude Code's background calls in an All-In session run on the fast model of the source the session is using, instead of the session's main model.

**Architecture:** `routerEnv` sets `ANTHROPIC_SMALL_FAST_MODEL=wisp/fast`. `Route` parses that marker as `KindFast`. The per-pane router remembers the last row it routed (seeded from the session's settings file) and turns a fast request into a normal target on that row's source, with the source's fast model.

**Tech Stack:** Go, `net/http`, package `internal/allin`, `internal/claudeconfig`, `cmd/wisp-deck-tui`.

**Spec:** `docs/superpowers/specs/2026-09-28-allin-fast-model-design.md`

## Global Constraints

- Marker value: `wisp/fast`, exactly.
- A Claude login's fast model: `claude-haiku-4-5-20251001`.
- A profile's fast model: its `ANTHROPIC_DEFAULT_HAIKU_MODEL`; if empty, the last row's own model.
- A fast target never carries `Want1M`.
- A fast call never moves to another source than the last row's.
- Starting row: `model` from `$CLAUDE_CONFIG_DIR/settings.json`, else `~/.claude/settings.json`. Never parse `--model`.
- Routing errors stay 400 (`writeRoutingError`), never 401/5xx.
- Run tests scoped to `./internal/allin/` and `./internal/claudeconfig/` with `WISP_DECK_TESTING=1`. Never a bare `go test ./...`.
- Comments: short, only what silently breaks if changed. `internal/**/CLAUDE.md` is audited prose: never write a bare `say`, a BEL escape, or an audio marker in it.
- Work on `main`. Stage only your own files (other sessions commit to this checkout).
- Every commit message ends with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.

## Review Focus

1. A fast request arrives before any main request (the title call) → it goes to the starting row's source, not the session login.
2. The user switches rows with `/model` mid-session → the next fast call follows the new source.
3. A fast request must not overwrite the remembered row → two fast calls in a row still go to the main row's source.
4. The last row is a 1M Claude row → the fast call carries no `context-1m` beta.
5. A fast call on the session's own login → the body's `wisp/fast` is rewritten to Haiku (the upstream must never see `wisp/fast`).

Each of these has a test in Task 3.

---

### Task 1: Parse the fast marker

**Files:**
- Modify: `internal/allin/route.go`
- Test: `internal/allin/route_test.go`

**Interfaces:**
- Produces: `const FastModel = "wisp/fast"`, `KindFast Kind` (after `KindConfig`), `Route("wisp/fast")` → `Target{Kind: KindFast}`.

- [ ] **Step 1: Write the failing test** (append to `route_test.go`)

```go
func TestRoute_reads_the_fast_marker(t *testing.T) {
	for _, model := range []string{FastModel, FastModel + OneMillionMarker} {
		if got := Route(model); got != (Target{Kind: KindFast}) {
			t.Errorf("Route(%q) = %+v, want the bare KindFast target", model, got)
		}
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run TestRoute_reads_the_fast_marker`
Expected: build failure, `undefined: FastModel` / `undefined: KindFast`.

- [ ] **Step 3: Implement**

In `route.go`, add `KindFast` after `KindConfig`:

```go
	KindConfig
	// KindFast is Claude Code's background call. It names no source: the
	// router sends it to the source of the session's last row.
	KindFast
```

Add to the const block beside `rowPrefix`:

```go
	// FastModel is what routerEnv sets ANTHROPIC_SMALL_FAST_MODEL to.
	FastModel = "wisp/fast"
```

In `Route`, right after `trimmed, want1m := strip1M(model)`:

```go
	if trimmed == FastModel {
		return Target{Kind: KindFast}
	}
```

- [ ] **Step 4: Run it and see it pass**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run 'TestRoute_'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/allin/route.go internal/allin/route_test.go
git commit -m "feat(allin): parse wisp/fast as a background-call marker"
```

---

### Task 2: Pick the fast target for a row

**Files:**
- Create: `internal/allin/fast.go`
- Test: `internal/allin/fast_test.go`
- Modify: `internal/claudeconfig/claudeconfig.go` (add `ReadFastModel` beside `ReadBaseURL`)

**Interfaces:**
- Consumes: `Target`, `KindFast`, `Route`, `validSource` (credential.go), `rosterEnv`/`writeProfile` test helpers.
- Produces:
  - `func fastTarget(row Target, configFast func(source string) string) Target`
  - `func (e Env) FastModelFor(source string) string`
  - `func StartingRow(settingsPath string) Target`
  - `func UserSettingsPath() string`
  - `func claudeconfig.ReadFastModel(configsDir, file string) string`

- [ ] **Step 1: Write the failing tests** (`fast_test.go`)

```go
package allin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFastTarget_keeps_an_account_row_on_its_login(t *testing.T) {
	row := Target{Kind: KindAccount, Source: "work", Model: "claude-opus-5-5", Want1M: true}
	want := Target{Kind: KindAccount, Source: "work", Model: "claude-haiku-4-5-20251001"}
	if got := fastTarget(row, nil); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFastTarget_uses_a_profiles_own_fast_model(t *testing.T) {
	row := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-pro"}
	got := fastTarget(row, func(source string) string {
		if source != "deepseek" {
			t.Fatalf("asked for %q", source)
		}
		return "deepseek-flash"
	})
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFastTarget_keeps_a_profile_without_a_fast_model_on_its_row(t *testing.T) {
	row := Target{Kind: KindConfig, Source: "qwen", Model: "qwen-big"}
	got := fastTarget(row, func(string) string { return "" })
	want := Target{Kind: KindConfig, Source: "qwen", Model: "qwen-big"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFastTarget_sends_a_session_row_to_haiku_on_the_session_login(t *testing.T) {
	for _, row := range []Target{{}, {Kind: KindSession, Model: "claude-opus-5-5", Want1M: true}} {
		want := Target{Kind: KindSession, Model: "claude-haiku-4-5-20251001"}
		if got := fastTarget(row, nil); got != want {
			t.Errorf("fastTarget(%+v) = %+v, want %+v", row, got, want)
		}
	}
}

func TestEnvFastModelFor_reads_the_profiles_haiku_mapping(t *testing.T) {
	env := rosterEnv(t)
	writeProfile(t, env, "DeepSeek", "deepseek.json",
		`{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"deepseek-flash"}}`)
	if got := env.FastModelFor("deepseek"); got != "deepseek-flash" {
		t.Fatalf("got %q", got)
	}
	if got := env.FastModelFor("zhipu-glm"); got != "" {
		t.Fatalf("a profile with no mapping gave %q", got)
	}
}

func TestEnvFastModelFor_refuses_a_path_shaped_source(t *testing.T) {
	env := rosterEnv(t)
	outside := filepath.Join(filepath.Dir(env.ConfigsDir), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"leak"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := env.FastModelFor("../outside"); got != "" {
		t.Fatalf("read a file outside the configs dir: %q", got)
	}
}

func TestStartingRow_reads_the_settings_model(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"model":"wisp/cfg.deepseek/deepseek-pro"}`)
	if got := StartingRow(path); got != (Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-pro"}) {
		t.Fatalf("got %+v", got)
	}
	write(`{"model":"wisp/fast"}`)
	if got := StartingRow(path); got != (Target{}) {
		t.Fatalf("a fast marker as the start row gave %+v", got)
	}
	write(`not json`)
	if got := StartingRow(path); got != (Target{}) {
		t.Fatalf("broken settings gave %+v", got)
	}
	if got := StartingRow(filepath.Join(t.TempDir(), "missing.json")); got != (Target{}) {
		t.Fatalf("a missing file gave %+v", got)
	}
}

func TestUserSettingsPath_follows_CLAUDE_CONFIG_DIR(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/acct")
	if got := UserSettingsPath(); got != "/tmp/acct/settings.json" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := UserSettingsPath(); got != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run 'TestFastTarget_|TestEnvFastModelFor_|TestStartingRow_|TestUserSettingsPath_'`
Expected: build failure, `undefined: fastTarget` etc.

- [ ] **Step 3: Implement**

In `internal/claudeconfig/claudeconfig.go`, after `ReadBaseURL`:

```go
// ReadFastModel returns a profile's haiku mapping, the model Claude Code
// sends its background calls to.
func ReadFastModel(configsDir, file string) string {
	return readEnvValue(configsDir, file, "ANTHROPIC_DEFAULT_HAIKU_MODEL")
}
```

Create `internal/allin/fast.go`:

```go
package allin

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// fastClaudeModel is the model Claude Code picks for background calls when
// it talks to Anthropic directly.
const fastClaudeModel = "claude-haiku-4-5-20251001"

// fastTarget keeps a background call on row's source. It must never pick
// another source: that would bill a subscription the session did not choose.
func fastTarget(row Target, configFast func(source string) string) Target {
	switch row.Kind {
	case KindAccount:
		return Target{Kind: KindAccount, Source: row.Source, Model: fastClaudeModel}
	case KindConfig:
		model := ""
		if configFast != nil {
			model = configFast(row.Source)
		}
		if model == "" {
			model = row.Model
		}
		return Target{Kind: KindConfig, Source: row.Source, Model: model}
	}
	return Target{Kind: KindSession, Model: fastClaudeModel}
}

// FastModelFor returns a profile's own fast model, or "" when it has none.
// The source comes off the wire, so it is checked before it names a file.
func (e Env) FastModelFor(source string) string {
	if validSource(source) != nil {
		return ""
	}
	return claudeconfig.ReadFastModel(e.ConfigsDir, source+".json")
}

// StartingRow is the row a session opens on. Claude Code asks for the title
// before the first turn, so the router has no row of its own yet. Anything
// unreadable gives the zero Target: the session's own login.
func StartingRow(settingsPath string) Target {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return Target{}
	}
	var settings struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return Target{}
	}
	row := Route(settings.Model)
	if row.Kind == KindFast {
		return Target{}
	}
	return row
}

// UserSettingsPath is the settings file the session reads its model from.
// Every account dir links its settings.json to ~/.claude/settings.json.
func UserSettingsPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}
```

- [ ] **Step 4: Run them and see them pass**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run 'TestFastTarget_|TestEnvFastModelFor_|TestStartingRow_|TestUserSettingsPath_'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/allin/fast.go internal/allin/fast_test.go internal/claudeconfig/claudeconfig.go
git commit -m "feat(allin): pick each source's fast model for a background call"
```

---

### Task 3: The router routes fast calls to the last row's source

**Files:**
- Modify: `internal/allin/proxy.go` (`NewObservingHandler` → delegates; new `FastRoute`, `NewRoutingHandler`)
- Test: `internal/allin/proxy_fast_test.go`

**Interfaces:**
- Consumes: `fastTarget`, `KindFast`, `rewriteModel`.
- Produces:
  - `type FastRoute struct { Start Target; ConfigFast func(source string) string }`
  - `func NewRoutingHandler(resolver Resolver, sessionUpstream string, observe func(http.Header), fast FastRoute) http.Handler`
  - `NewObservingHandler(r, u, o)` == `NewRoutingHandler(r, u, o, FastRoute{})`.

- [ ] **Step 1: Write the failing tests** (`proxy_fast_test.go`)

```go
package allin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recordingResolver struct {
	mu       sync.Mutex
	targets  []Target
	upstream string
}

func (r *recordingResolver) Resolve(target Target) (Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, target)
	return Credential{BaseURL: r.upstream, Header: "Authorization", Value: "Bearer routed"}, nil
}

func (r *recordingResolver) last() Target {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.targets) == 0 {
		return Target{}
	}
	return r.targets[len(r.targets)-1]
}

type seenRequest struct {
	model, auth, beta string
}

func fastFixture(t *testing.T, fast FastRoute) (http.Handler, *recordingResolver, func() seenRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen seenRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sent map[string]any
		_ = json.Unmarshal(body, &sent)
		mu.Lock()
		seen = seenRequest{model: sent["model"].(string), auth: r.Header.Get("Authorization"), beta: r.Header.Get("Anthropic-Beta")}
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)
	resolver := &recordingResolver{upstream: upstream.URL}
	handler := NewRoutingHandler(resolver, upstream.URL, nil, fast)
	return handler, resolver, func() seenRequest { mu.Lock(); defer mu.Unlock(); return seen }
}

func postModel(t *testing.T, handler http.Handler, model string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"`+model+`"}`))
	request.Header.Set("Authorization", "Bearer session")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", model, recorder.Code, recorder.Body.String())
	}
}

func deepseekFast(source string) string {
	if source == "deepseek" {
		return "deepseek-flash"
	}
	return ""
}

func TestFastCall_goes_to_haiku_on_the_last_account_row(t *testing.T) {
	handler, resolver, seen := fastFixture(t, FastRoute{})
	postModel(t, handler, "wisp/acct.work/claude-opus-5-5[1m]")
	postModel(t, handler, FastModel)
	want := Target{Kind: KindAccount, Source: "work", Model: "claude-haiku-4-5-20251001"}
	if got := resolver.last(); got != want {
		t.Fatalf("resolved %+v, want %+v", got, want)
	}
	if got := seen(); got.model != "claude-haiku-4-5-20251001" || strings.Contains(got.beta, "context-1m") {
		t.Fatalf("upstream saw %+v", got)
	}
}

func TestFastCall_follows_a_switch_to_another_row(t *testing.T) {
	handler, resolver, seen := fastFixture(t, FastRoute{ConfigFast: deepseekFast})
	postModel(t, handler, "wisp/acct.work/claude-opus-5-5[1m]")
	postModel(t, handler, "wisp/cfg.deepseek/deepseek-pro")
	postModel(t, handler, FastModel)
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got := resolver.last(); got != want {
		t.Fatalf("resolved %+v, want %+v", got, want)
	}
	if got := seen().model; got != "deepseek-flash" {
		t.Fatalf("upstream model %q", got)
	}
}

func TestFastCall_before_any_turn_uses_the_starting_row(t *testing.T) {
	start := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-pro"}
	handler, resolver, _ := fastFixture(t, FastRoute{Start: start, ConfigFast: deepseekFast})
	postModel(t, handler, FastModel)
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got := resolver.last(); got != want {
		t.Fatalf("resolved %+v, want %+v", got, want)
	}
}

func TestFastCall_does_not_replace_the_remembered_row(t *testing.T) {
	handler, resolver, _ := fastFixture(t, FastRoute{})
	postModel(t, handler, "wisp/acct.work/claude-opus-5-5")
	postModel(t, handler, FastModel)
	postModel(t, handler, FastModel)
	want := Target{Kind: KindAccount, Source: "work", Model: "claude-haiku-4-5-20251001"}
	if got := resolver.last(); got != want {
		t.Fatalf("second fast call resolved %+v, want %+v", got, want)
	}
}

func TestFastCall_on_the_session_login_never_sends_the_marker_upstream(t *testing.T) {
	handler, resolver, seen := fastFixture(t, FastRoute{})
	postModel(t, handler, FastModel)
	got := seen()
	if got.model != "claude-haiku-4-5-20251001" {
		t.Fatalf("upstream model %q, want haiku", got.model)
	}
	if got.auth != "Bearer session" {
		t.Fatalf("auth %q, want the session's own", got.auth)
	}
	if len(resolver.targets) != 0 {
		t.Fatalf("a session fast call resolved %+v", resolver.targets)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run 'TestFastCall_'`
Expected: build failure, `undefined: NewRoutingHandler` / `undefined: FastRoute`.

- [ ] **Step 3: Implement** in `proxy.go`

Add `"sync"` to the imports. Replace the `NewObservingHandler` header so it delegates, and rename its body to `NewRoutingHandler`:

```go
// FastRoute is what a wisp/fast call needs: the row the session opens on,
// and each profile's own fast model.
type FastRoute struct {
	Start      Target
	ConfigFast func(source string) string
}

func NewObservingHandler(resolver Resolver, sessionUpstream string, observe func(http.Header)) http.Handler {
	return NewRoutingHandler(resolver, sessionUpstream, observe, FastRoute{})
}

// NewRoutingHandler is NewObservingHandler plus fast-call routing. One handler
// serves one pane, so the row it remembers is that session's row.
func NewRoutingHandler(resolver Resolver, sessionUpstream string, observe func(http.Header), fast FastRoute) http.Handler {
	sessionIsAnthropic := false
	// ... existing body unchanged up to the handler func ...
	var lastMu sync.Mutex
	last := fast.Start
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

Keep the existing doc comment of `NewObservingHandler` on it. Then replace:

```go
		model, _ := payload["model"].(string)
		target := Route(model)
```

with:

```go
		model, _ := payload["model"].(string)
		target := Route(model)
		fastCall := target.Kind == KindFast
		lastMu.Lock()
		if fastCall {
			target = fastTarget(last, fast.ConfigFast)
		} else if model != "" {
			last = target
		}
		lastMu.Unlock()
		// A session target skips the rewrite below, and the upstream must
		// never see the marker.
		if fastCall && target.Kind == KindSession {
			rewritten, err := rewriteModel(payload, target.Model, nil)
			if err != nil {
				writeRoutingError(w, target, err)
				return
			}
			body = rewritten
		}
```

The existing `if target.Kind != KindSession { … }` block then handles account and config fast targets unchanged; `Want1M` is false on every fast target, so no 1M beta is added.

- [ ] **Step 4: Run the new tests and the existing handler tests**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run 'TestFastCall_|TestHandler_|TestRewriteModel_'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/allin/proxy.go internal/allin/proxy_fast_test.go
git commit -m "feat(allin): route background calls to the fast model of the session's source"
```

---

### Task 4: Turn it on — profile key, launch wiring, package notes

**Files:**
- Modify: `internal/allin/profile.go` (`routerEnv`, its doc comment)
- Test: `internal/allin/profile_test.go`
- Modify: `cmd/wisp-deck-tui/claude_allin.go:89-91`
- Modify: `internal/allin/CLAUDE.md` (the "Not measured, and left alone" paragraph, around lines 627-635)

**Interfaces:**
- Consumes: `FastModel`, `NewRoutingHandler`, `FastRoute`, `StartingRow`, `UserSettingsPath`, `Env.FastModelFor`.

- [ ] **Step 1: Write the failing test** (after `TestEnsureProfile_forces_subagents_onto_the_picked_row` in `profile_test.go`)

```go
// Without the key, Claude Code sends the title and quota calls to the session
// model, because the router's loopback endpoint is not first-party. Measured
// on 2.1.283: both went to the Opus row.
func TestEnsureProfile_sends_background_calls_to_the_fast_marker(t *testing.T) {
	_, _, path := generatedProfile(t)
	if got := readEnv(t, path)["ANTHROPIC_SMALL_FAST_MODEL"]; got != FastModel {
		t.Fatalf("ANTHROPIC_SMALL_FAST_MODEL = %q, want %q", got, FastModel)
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/allin/ -run TestEnsureProfile_sends_background_calls_to_the_fast_marker`
Expected: FAIL, `ANTHROPIC_SMALL_FAST_MODEL = "", want "wisp/fast"`.

- [ ] **Step 3: Implement**

In `routerEnv` add:

```go
		"ANTHROPIC_SMALL_FAST_MODEL":              FastModel,
```

and add to its doc comment, before the "A 1M window is ONE key" paragraph:

```go
// ANTHROPIC_SMALL_FAST_MODEL: without it Claude Code sends its background
// calls (title, quota check) to the session model, because the loopback
// endpoint is not first-party. The router turns the marker into the fast
// model of the session's current source.
```

In `cmd/wisp-deck-tui/claude_allin.go`, replace the `newHandler` closure:

```go
			fast := allin.FastRoute{
				Start:      allin.StartingRow(allin.UserSettingsPath()),
				ConfigFast: env.FastModelFor,
			}
			newHandler := func(upstream string) http.Handler {
				return allin.NewRoutingHandler(resolver, upstream, observe, fast)
			}
```

In `internal/allin/CLAUDE.md`, replace the paragraph that starts "Not measured, and left alone: the small/fast alias" with:

```markdown
### Background calls run on the fast model of the session's source

Claude Code sends its background calls (the session title, the startup quota
check) to its small/fast model. With the loopback endpoint and no
`ANTHROPIC_SMALL_FAST_MODEL`, it uses the session model instead. Measured on
2.1.283 against a capture server: without the key both calls carried the Opus
row; with it both carried the key's value, while the main turns and the
prompt-suggestion call stayed on the session model.

`routerEnv` sets the key to `wisp/fast`. `Route` reads it as `KindFast`, and
the handler sends it to the source of the last row it routed: a login gets
Haiku 4.5 on that login, a profile gets its `ANTHROPIC_DEFAULT_HAIKU_MODEL`
(or the row's own model when it has none), and the session's own login gets
Haiku. Before the first turn the handler has no row, so it starts from the
`model` in the session's settings file. A fast call never moves to another
source. Guarded by `proxy_fast_test.go` and
`TestEnsureProfile_sends_background_calls_to_the_fast_marker`.
```

- [ ] **Step 4: Run the package tests, build, and lint the changed files**

Run:
```bash
WISP_DECK_TESTING=1 go test ./internal/allin/ ./internal/claudeconfig/
go build ./cmd/wisp-deck-tui/
gofmt -l internal/allin/route.go internal/allin/fast.go internal/allin/fast_test.go internal/allin/proxy.go internal/allin/proxy_fast_test.go internal/allin/profile.go internal/allin/profile_test.go internal/claudeconfig/claudeconfig.go cmd/wisp-deck-tui/claude_allin.go
go vet ./internal/allin/ ./cmd/wisp-deck-tui/
WISP_DECK_TESTING=1 go test ./cmd/wisp-deck-tui/ -run 'ClaudeAllIn|ClaudeConfigAllIn'
WISP_DECK_TESTING=1 go test ./test/bash/ -timeout 20m -run 'HostEffectOwnership'
```
Expected: all PASS, `gofmt -l` prints nothing. The last line checks the audited CLAUDE.md prose.

- [ ] **Step 5: Commit**

```bash
git add internal/allin/profile.go internal/allin/profile_test.go cmd/wisp-deck-tui/claude_allin.go internal/allin/CLAUDE.md
git commit -m "feat(allin): send background calls to wisp/fast in every All-In session"
```

---

### Task 5: Live check and push

**Files:** none in the repo. Throwaway files go to the session scratchpad.

- [ ] **Step 1: Confirm Claude Code sends background calls to `wisp/fast`**

Repeat the capture run from the research: the scratchpad `capture.py` fake endpoint on a free port, a copy of the live overlay with `ANTHROPIC_BASE_URL` pointed at it and `ANTHROPIC_SMALL_FAST_MODEL=wisp/fast`, `claude --settings <copy>` on the isolated `tmux -L wdprobe` socket, one prompt.
Expected in the capture log: the quota request and the title request (system text "naming a coding session") carry `"model": "wisp/fast"`; the main turns carry the session row.

- [ ] **Step 2: Clean up**

```bash
tmux -L wdprobe kill-server; pkill -f "scratchpad/capture.py"
```

- [ ] **Step 3: Push**

```bash
git pull --rebase && git push
```
Expected: push succeeds.
