# Featherless subscriptions: connect any of 15,571 models in a few clicks

**Status:** approved design, 2026-09-01

Featherless hosts ~22,000 HuggingFace models behind one API key. This adds it to
the Subscription modal so any of them can back a Claude Code pane, chosen from a
searchable picker rather than typed by hand.

## The finding that makes this cheap

Featherless is documented as an OpenAI-compatible provider, which would put it on
the wrong side of the rule in CLAUDE.md: *"The endpoint has to speak the Anthropic
Messages API … an OpenAI-compatible server needs a translating proxy in front of
it."* A translating proxy is a subsystem on the scale of `internal/gptbridge`.

It does not need one. Featherless serves the **Anthropic Messages API natively**,
undocumented as far as their public docs go. Probed live on 2026-09-01:

| Probe | Result |
| --- | --- |
| `POST /v1/messages`, no auth | `{"type":"error","error":{"type":"authentication_error",…}}` — Anthropic's error envelope |
| `POST /v1/chat/completions`, no auth | `{"error":{"message":…,"param":null,"code":"unauthorized"}}` — OpenAI's envelope |
| `POST /v1/bogusroute` | fastify `{"statusCode":404}` |

Three distinct shapes on one host is what makes this a real route rather than
middleware that happens to answer everything. With a key:

- `Authorization: Bearer <key>` → **200**, with `content`, `stop_reason`, `usage`.
- `x-api-key: <key>` → **401**. So `ANTHROPIC_AUTH_TOKEN` (what the modal already
  writes) is the working credential and `ANTHROPIC_API_KEY` is not.
- `"stream": true` with a `tools` array → the full correct sequence:
  `content_block_start{type:"tool_use"}` → `input_json_delta` →
  `message_delta{stop_reason:"tool_use"}`. Tool calling round-trips, which is the
  capability Claude Code cannot run without.
- A model id that does not exist → Anthropic-shaped `not_found_error`, so Claude
  Code renders the failure instead of choking on it.

**The base URL is therefore `https://api.featherless.ai`** and no proxy, bridge,
or translation layer is part of this work.

### The byte watchdog stays armed

`stampByteWatchdog` disarms Claude Code's raw-byte stall watchdog for
user-configured profiles, because a self-hosted model prefilling a large prompt
sends no bytes until its first output token, and 20s of byte silence paints
`Waiting for API response · check your network` before aborting the turn at 180s.

Featherless does not have that problem. A cold 14B model given a ~22k-token
prompt was measured emitting:

```
event: message_start
: keep-alive (awaiting first token)
: keep-alive (awaiting first token)
…
event: content_block_start
```

SSE comments roughly every 1.2s across a 12s model-load gap; the largest byte
silence measured was 4.8s. The watchdog counts bytes, not events, so the stream
is never silent long enough to trip it.

So a Featherless profile **keeps the watchdog armed**. It is not merely
unnecessary to disarm it — disarming trades a real dead-connection signal for
nothing. This falls out for free (`stampByteWatchdog` gates on `UserConfigured`,
which Featherless does not set), so the guard is a test that pins the behavior
against a future refactor, not code.

Note that Featherless sends no `event: ping` frames at all. The keep-alive
comments are what keeps the socket warm, so a future Featherless change that
drops them would silently reintroduce the whole self-hosted failure mode. That is
what the live e2e test below exists to catch.

## The catalog

`GET https://api.featherless.ai/v1/models` is public (a key adds
`available_on_current_plan`) and returns ~7MB:

- **21,908** models, of which **15,571** report `features.tool_use`
- `context_length` on all but 9 — 13,710 of the tool-calling models are 32768,
  and the frontier tier (Kimi K3, GLM-5.2, DeepSeek V4, MiniMax M3, Qwen 3.5) is
  at 262144
- `features.image_input` on 631 models
- `pricing.input` / `pricing.output` in USD per 1M tokens
- `max_completion_tokens` on only 5,870, so it cannot be relied on

The list carries everything the picker needs; the per-model detail endpoint
(`/v1/models/{id}`, which adds `vision_supported`, `availability.tier`,
`downloads`) is **not** needed and is not fetched.

`?capabilities=tool_use` returns an empty set — the documented filter name does
not work as described. `?q=` search plus pagination does work, but is not used:
the design fetches once and filters locally.

## Design

### 1. Catalog entry

A `featherless` provider in `Providers` (`internal/claudeconfig/catalog.go`):

```go
Key:            "featherless",
Name:           "Featherless",
Aliases:        []string{"featherless"},
BaseURL:        "https://api.featherless.ai",
Auth:           AuthAPIKey,
MirrorOpenCode: false,
RemoteCatalog:  true,
// no Models, no DefaultModels
```

Two placement constraints, both load-bearing:

- **Never index 0.** `Providers[0]` is the fallback for every profile name
  matching no alias; a provider with no models there would claim every stray
  config on the machine.
- **Before the moonshot entries.** Profiles are auto-named after the model, so
  "Featherless Kimi-K3" is a name this feature produces routinely — and it
  contains `kimi`, which is `moonshot`'s alias. Alias matching is substring, in
  slice order, so a later placement resolves the profile to the wrong gateway.
  `ProviderForConfig` prefers the explicit `WISP_DECK_SUBSCRIPTION_PROVIDER`
  marker and would be right anyway; the ordering fixes the legacy name-matching
  path that `ProviderForName` still serves.

The single alias is `featherless`, not also a bare `feather`, to keep the
substring surface small.

`MirrorOpenCode` stays off: OpenCode's catalog cannot size a model that only
exists on Featherless, and `Sync` skips non-mirrored providers outright.

### 2. `RemoteCatalog`, a new Provider trait

Featherless is neither a static-catalog gateway nor `UserConfigured`, and
overloading `UserConfigured` would be wrong in both directions. Each behavior
that flag currently gates, and where Featherless falls:

| Behavior gated by `UserConfigured` | Featherless |
| --- | --- |
| Endpoint text field | **No** — the base URL ships with the provider |
| Model text field | **No** — a picker replaces it |
| Alias cycler (`cycleSubscriptionMapping`) | **No** — inert on an empty model list |
| Context text field | **Yes** — auto-filled at pick time, hand-editable after |
| Images toggle | **Yes** — auto-defaulted at pick time |
| `WriteModelMappings` skipped on save | **Yes** — see below |
| Byte watchdog disarmed | **No** — measured above |
| Declared context window is owned, not catalog-derived | **Yes** |

`WriteModelMappings` writes the four aliases from the draft's *model list*, which
is empty for a remote-catalog provider. Running it deletes the model the user
just picked, on every save. This is the exact bug
`TestSubscriptionModal_savingACustomProfileKeepsItsModelMapping` was written for,
and the remote-catalog save path must skip it the same way.

`stampContextBudget` already lands correctly by accident — a Featherless model id
(`moonshotai/Kimi-K3`) is in no static catalog, so `ContextBudget` misses and the
declared window is preserved. The trait makes that explicit
(`ownsWindow := UserConfigured || RemoteCatalog`) rather than leaving it to a
namespace collision never happening.

`ConfigReady` gains a remote-catalog branch: key, model, window, and all four
alias keys agreeing — the same checks as `UserConfigured` minus the endpoint,
which the provider supplies.

### 3. `internal/featherless`

A new package owning the model list and nothing else.

```go
type Model struct {
    ID          string
    Class       string
    Context     int
    InPerM      float64
    OutPerM     float64
    ImageInput  bool
    OnPlan      bool   // available_on_current_plan; true when unauthenticated
    Created     int64
}

func Fetch(ctx context.Context, key string) ([]Model, error)
func LoadCache(path string) ([]Model, time.Time, bool)
func SaveCache(path string, models []Model) error
func Search(models []Model, query string) []Model
```

- `Fetch` GETs `/v1/models`, sends `Authorization: Bearer` when a key is
  available, and **keeps only `features.tool_use` models**. The other 6,337 are
  silently unusable in Claude Code — a pane that cannot call a tool cannot read
  or edit a file — so they are never offered.
- Models with no `context_length` are dropped: the window must be declared, and a
  model that cannot declare one cannot be picked safely.
- Cache path is passed in by the caller, matching `usage.LoadCache(path)`, so the
  package never reads `HOME` and stays testable. The caller derives it from
  `settingsFilePath()`'s directory — `<XDG_CONFIG_HOME|~/.config>/wisp-deck/`,
  giving `featherless-models.json`. A TTL of 24h; a stale cache is still served
  when the network fails, because a stale list beats no list.
- Default order: context length descending, then `created` descending. That puts
  the frontier 262144 tier at the top, which is what someone opening the picker
  is nearly always after.
- `Search` is a case-insensitive substring over id and class, preserving that
  order, with exact and prefix matches first.

### 4. The picker

A new modal mode beside `subscriptionAddProvider`, following
`updateSubscriptionProviderPicker`'s shape: a `textinput` for the query, a
windowed list, arrows/`j`/`k`, Enter to select, Esc to cancel.

- The fetch runs in a `tea.Cmd` and never in `Update` — it is a 7MB HTTP round
  trip.
- States: loading (cache miss), ready, and error-with-retry (no cache and no
  network).
- Each row shows the model id, its context window, and its price per 1M tokens.
  A model reporting `available_on_current_plan: false` is marked and cannot be
  selected — the plan will not run it. `is_gated` describes the HuggingFace repo,
  **not** Featherless access (`meta-llama/Llama-3.3-70B-Instruct` is gated and
  served normally), so it is not shown and never blocks a pick.

### 5. The flow

1. Subscriptions → `+ Add subscription` → **Featherless**.
2. The picker opens immediately, ordered so the frontier models are already on
   screen. Type to narrow.
3. Enter picks. The name prompt is pre-filled from the model
   (`Featherless Kimi-K3`), still editable.
4. If another Featherless profile already holds a key, it is reused, so the
   profile is Ready with nothing further to do. Otherwise the API key row is the
   one remaining step.

Selecting a model writes, through the existing writers:

- `WriteCustomModel(id)` — which already fills all four
  `ANTHROPIC_DEFAULT_*_MODEL` aliases.
- `WriteCustomContextWindow(context_length)` — the pick is **not complete**
  without it. An undeclared window falls back to a flat 200000, which strands a
  32768 model permanently: `/compact` must itself send the oversized transcript,
  so it fails with the same 400 as the turn that provoked it.
- `WriteImagesBlocked(!image_input)` — 631 of the 15,571 models accept images;
  the rest fail the turn on one, so the deny rules default on and stay toggleable.

Per the approved scope, one model fills all four aliases. The picker is attached
to the **Model row**, so per-alias picking (a cheap model on haiku, which carries
Claude Code's background work) is a later addition to the same seam rather than a
rewrite. Should it land, the declared window becomes the minimum across the four.

## Testing

TDD throughout: test first, watched failing, then code.

**`internal/featherless`** — parse a fixture captured from the real response;
`tool_use` filtering; dropping models with no context length; ordering; search
ranking; cache round-trip, TTL expiry, and stale-cache-on-network-failure;
malformed JSON and a truncated body.

**`internal/claudeconfig`** — the catalog entry's invariants (not index 0, ahead
of moonshot, base URL, no models, `MirrorOpenCode` off); that
`"Featherless Kimi-K3"` resolves to featherless and not moonshot by name;
`ConfigReady`'s remote-catalog branch; that a save writes all four aliases and
the window; and that the byte watchdog is **not** disarmed, with the keep-alive
measurement as the comment.

**`internal/tui`** — the picker opens on provider selection; search filters;
Enter stamps model, window and images onto the draft; save keeps the mapping
(the `WriteModelMappings` regression); a sibling profile's key is reused.

**Live e2e**, env-gated like the repo's other live tests
(`WISP_DECK_LIVE_FEATHERLESS_E2E=1`): fetch the catalog and assert the tool-use
tier is present, then run one small streaming tool turn asserting
`stop_reason:"tool_use"` and the presence of keep-alive comments. This is the
only check that can see Featherless changing its side — dropping the Anthropic
route, or dropping the keep-alive that keeps the byte watchdog quiet.

**`shellcheck`** on any modified script, and the full Go suite, before the work
is called done.

## Out of scope

- Folding Featherless prices into `usage/pricing.go`. The catalog carries real
  per-1M rates and the Stats tab could use them, but that is a separate change to
  a cost model with its own cache version.
- The OpenCode mirror, for the reason above.
- Per-alias model mapping, deliberately deferred to the seam described above.
- `availability.tier` / cold-start hints in the picker; the detail endpoint is
  one request per model and the keep-alive makes cold starts a non-event.
