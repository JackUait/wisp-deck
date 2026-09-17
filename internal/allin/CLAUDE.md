# allin — gotchas

Gotchas for the All-In model router: one Claude settings profile whose `/model`
picker lists every configured subscription, routed through a loopback proxy
with no pane relaunch. Loaded when Claude opens a file in this package. Spec
and live measurements: `docs/all-in-model-router.md`.

### The id grammar splits on the first two segments only

A picker row's id is `wisp/<source>/<model>`, and `Route` (`route.go`) cuts it
on the first two `/`s, not all of them. A Featherless model id contains its own
slash (`TurboVadim/Qwen3.8-27B-OBLITERATED`), so cutting on every `/` would
misparse the model as another path segment. `<source>` is `acct.<dir>` (a
Claude login) or `cfg.<file-without-.json>` (a subscription profile). An id
this build cannot place — wrong prefix, empty source, empty model — returns
`KindSession` rather than an error: the turn must still run on the session's
own credential. Guarded by `TestRoute_keeps_slashes_inside_the_model_id` and
`TestRoute_treats_an_unparseable_prefix_as_the_session`.

### Every Claude row carries `[1m]`, and a provider row never may

Measured live through `/context`: a `wisp/…` row carrying `behavesAs:
claude-opus-5` gets the flat 200k window, so `behavesAs` does not carry a window
across the router. The literal `[1m]` suffix on the raw model string does,
because Claude Code reads that marker off the string itself. Decoded from
2.1.263, the marker branch is the FIRST thing the model-window resolver checks,
and the whole chain below it is unreachable once it hits.

`accountRows` appends `OneMillionMarker` to every Claude id, and `configRows`
appends it to none. A provider row must stay unmarked: no configured provider
serves 1M, and the marker would tell the session it has room the endpoint
refuses. `strip1M` (`route.go`) removes it before an id reaches `Resolve` or the
upstream, `Want1M` carries the fact forward, and `proxy.go` turns it into the
`context-1m-2025-08-07` beta header. Guarded by
`TestRoster_marks_every_claude_row_1m_and_no_provider_row` and
`TestRoster_never_marks_a_chatgpt_row_1m`.

**The trade this accepts.** The window is granted to the whole *session*, and
nothing narrows it again when the user picks a provider row later in the same
conversation: by then the transcript may be past that endpoint's cap, and
`/compact` cannot escape it — it sends the same oversized transcript plus a
summarization prompt, so it is larger than the turn that already failed. This is
the unrecoverable class the root `CLAUDE.md` documents, and
`_guard_subscription_context` — which catches it on a subscription *switch* —
does not run on a `/model` pick. The alternative was a uniform 200k across every
row, which no pick can wedge; 1M on the Claude rows was chosen over it
deliberately, with that failure understood.

The generated profile still declares its own window, because the rows are not
the only model string in play: the session's *starting* model comes from the
user's global settings. Every other profile gets its window from
`stampContextBudget`, which returns early here — All-In has no model mappings
for it to size a window from — so this is the one profile that must declare the
set itself.

At 1M that set is **one** key. `routerEnv` writes
`CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000` and maps
`CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_CODE_DISABLE_1M_CONTEXT` and
`CLAUDE_CODE_MAX_OUTPUT_TOKENS` to `""`, which `EnsureProfile` reads as
**delete**. Deleting is the load-bearing half: every profile written before this
change carries those three at their 200k values, and either of the first two
alone puts the session back at 200k. `DISABLE_1M_CONTEXT` makes the decoded
`tc(model)` return false whatever the id ends with, so the marker stops working
on every row at once, and `AUTO_COMPACT_WINDOW` caps the window directly. That
is exactly what `contextWindowEnv` computes for a 1000000 window, which matters
because `bin/wisp-deck` runs `ensure-budget` over this file on every install and
a set that disagrees is rewritten every time.
`TestEnsureProfile_survives_the_context_budget_sweep`,
`TestEnsureProfile_clears_a_sub_1m_windows_leftover_keys` and
`TestEnsureProfile_leaves_the_1m_model_marker_armed` hold the three facts apart.

The hidden-rows file predates the marker and stores ids **unmarked**, so every
lookup into it goes through `BareModel` and `ToggleHidden` strips before it
writes. Without that, marking the Claude rows would silently un-hide every
Claude row a user had already hidden, and the file would collect two spellings
of one row. Guarded by
`TestEnsureProfile_omits_a_marked_row_the_file_lists_unmarked` and
`TestToggleHidden_stores_a_marked_row_unmarked`.

### `Row` declares no `behavesAs`, and re-adding one costs the pane its effort control

Measured on a live pane: an unknown model id **without** `behavesAs` is given
`thinking: {"type":"adaptive"}` and keeps effort available; the same id **with**
`behavesAs: claude-sonnet-4-5` is given `thinking: {"type":"enabled", budget}`
and the pane reports "Effort not supported". Absent is the more capable default,
and (per the measurement above) it carries no context window across the router
either — so the field bought nothing and cost the pane a control. It was
declared on `Row` and never set by anything; it is now gone. Guarded by
`TestRoster_never_declares_behaves_as`, which reads the serialized rows so it
fails on a field that is re-declared *and* populated.

### The roster's exclusion is advice until `Resolve` enforces it too

`configRows` decides what the picker OFFERS. `Resolve` decides what the router
will actually address, and a model id arrives off the wire — hand-typed into
`/model`, or saved as a picker default by an older roster — so a row the roster
would never have written still reaches it. `routableProfile` (`credential.go`)
therefore re-applies the one rule left, through the `routableAuth` predicate
both sides share: the router profile itself is refused. It sits in the very
same configs list, so a row could name the router that is asking for it, and
`configRows`'s display-name skip does not survive a rename — the marker does.

`routableAuth` is an **allowlist** (`AuthAPIKey`, `AuthCodexChatGPT`), not a
"not `AuthWispRouter`" test, so a new `AuthKind` is refused until someone
decides how to serve it. That is the safe direction: an unserved row 400s a
turn, an unoffered one costs nothing.

`RemoteCatalog` (Featherless) is NOT refused here — see the delegation section
below — but `routableProfile` still hands its caller the resolved `Provider`,
because `Resolve` needs its `RemoteCatalog` bit for a different decision: it
must key on `RemoteCatalog`, never on `SuppliesOwnModel()` — that is true for a
self-hosted profile too, and a self-hosted endpoint speaks the Anthropic API
directly and needs no repair. Guarded by
`TestResolve_refuses_a_provider_the_roster_would_not_offer` (which asserts the
exact refusal, not just an error — without `routableAuth` the resolver falls
through to the key read and answers "is not ready", an accident of that profile
carrying no token that stops holding the moment a user adds one),
`TestResolve_refuses_the_router_profile_itself`,
`TestResolve_does_not_mark_an_ordinary_gateway_for_repair`, and the two
`TestResolve_still_serves_*` counterweights that stop the refusal widening.

### Every error carries the `allin:` prefix, never `wisp-deck:`

`writeRoutingError` renders `fmt.Sprintf("wisp-deck: %v", err)` itself, so an
error that already begins with that string reaches the user as
`wisp-deck: wisp-deck: …`. Every error raised in this package uses the package
prefix instead. Guarded by `TestHandler_never_doubles_the_wisp_deck_prefix`.

### The rewrite fails closed, because failing open sends a credential with a routing id

`rewriteModel` (`proxy.go`) refuses a nil payload and a body that will not
re-encode, and the handler answers 400 rather than forwarding. Both are
unreachable today, and only because a non-local invariant holds: `Route` answers
`KindSession` for the empty id a nil payload yields, and a payload decoded from
JSON always re-encodes. Failing open there is worse than it looks — the
credential is swapped **before** the body is rewritten, so a third-party
endpoint would receive an unresolvable `wisp/…` id while holding someone's real
token. Guarded by `TestRewriteModel_refuses_a_body_it_cannot_re_address`.

### A stale credential must surface as HTTP 400, never 401 or 5xx

Claude Code retries a 401, a 429, or a 5xx about eleven times before giving up.
Every routing failure in this package is deterministic — the same request
would fail again the same way — so `writeRoutingError` (`proxy.go`) always
answers 400, in Anthropic's own error envelope. A malformed upstream base URL
shipped as a 502 in an earlier draft (the raw `url.Parse` failure reached
`httputil.ReverseProxy`, which reports a dial failure as retryable) and was
caught in review; `validUpstream` now rejects a URL with no scheme or host
before the proxy ever runs. Guarded by
`TestHandler_reports_a_stale_account_as_400_not_401` and
`TestHandler_reports_a_malformed_upstream_as_400_not_502`.

### The body read goes one byte past the cap

`maxRouteBytes` bounds how large a request this handler will re-address.
`io.LimitReader(r.Body, maxRouteBytes)` alone can return an exactly-full,
silently truncated body with a nil error — there is no way to tell "the body
was exactly this size" from "the body was cut off" without seeing one more
byte arrive. The handler reads `maxRouteBytes+1` and rejects anything that
actually filled it, and it never consults `r.ContentLength`: a chunked request
reports `-1` there, so a cap enforced on that field passes the very body it
exists to stop. This matters more here than in an ordinary proxy: the
All-In profile sets `replaceBuiltInOptions: true` on its picker, so there is no
built-in row to fall back to — a truncated body's `model` field would parse to
some other row's id, or to none, and forward silently instead of failing
loudly. Same lesson `internal/rolefix` already records for its own repair
budget. Guarded by `TestHandler_rejects_a_body_over_the_routing_cap` and
`TestHandler_routes_correctly_when_content_length_is_unknown` (a chunked
request reports `ContentLength: -1`, so the cap can only be enforced by reading),
and by `TestHandler_rejects_an_over_cap_body_that_declares_no_length`, which is
the one that actually kills the `ContentLength`-plus-plain-`LimitReader` mutant:
the two tests before it both pass while a truncated body is forwarded with a
200.

### Both credential headers are deleted before the swap, unconditionally

`r.Header.Del("Authorization")` and `r.Header.Del("X-Api-Key")` both run before
`r.Header.Set(credential.Header, credential.Value)`, even though `Credential`
only ever names one of the two today. A conditional delete — clearing only the
header the swap is about to set — would let the session's own OAuth bearer (or
API key) survive on the other header and travel to the swapped-to endpoint
alongside the new credential, the day a provider using `x-api-key` is added.
Guarded by `TestHandler_clears_both_credential_headers_before_swapping_in_one`.

### `ErrorLog` is a discarding logger

`httputil.ReverseProxy`'s `ErrorLog` field defaults to nil, which falls back to
the standard `log` package writing to stderr. In this repo the wrapped
process's stderr is the terminal Claude Code paints on (see
`wrapper-stderr-is-the-ai-pane` in project memory), so an unset `ErrorLog` here
would print raw proxy errors into the middle of the agent's own screen.
`newReverseProxy` (`proxy.go`) sets it to a `log.New(io.Discard, ...)` logger.

### `FlushInterval: -1` is load-bearing

Buffering the response would swallow the `: keep-alive` bytes a slow upstream
sends before its first token. Claude Code arms a byte-stall watchdog on every
stream and replays the whole turn after 20s of silence on the wire; a
buffering proxy would sit on those bytes and manufacture exactly that silence,
even though the upstream was sending the whole time. `newReverseProxy`
forwards each write as it arrives instead.

### The launch gate is the settings file's own rows, never the profile's display name

`gt_claude_launch_wrapper` (`lib/tmux-session.sh`) decides whether to wrap a
Claude launch in this router by grepping the settings file itself for
`wisp/acct.` or `wisp/cfg.` — the exact prefixes `roster.go` writes — never by
checking the profile's display name (`allin.ProfileName`, `"All-In"`).
Renaming a profile is reachable from the UI in more than one place, and a
renamed-but-still-routed profile would silently stop being wrapped while its
picker kept offering rows nothing was resolving.

The match must stay anchored to those two literal prefixes. A bare `wisp/`
substring also matches a statusline path or a model id under an org literally
named `wisp`, and a false positive here silently steals the launch from the
**other** branch of the same function: a Featherless profile's launch is
gated on the same `if`/`elif` chain, so a false-positive All-In match would
strip the role-repair proxy that is the only reason a Featherless pane can
call a tool at all. This was mutation-proven in review.

### A RemoteCatalog target is served through `rolefix`'s own handler, not a second repair

Featherless 400s on Claude Code's `role:"system"` capability listings and
silently stops parsing tool calls once a request carries a `thinking` field;
both are things Claude Code sends on every turn. An earlier draft of this
router excluded Featherless from the roster entirely on the theory that
`rolefix`'s repair function was unexported and not reusable outside its own
package. **That theory was wrong.** `rolefix.NewHandler(upstream string)
http.Handler` is exported and self-contained: it builds its own reverse proxy
to `upstream` and wires both the request repair (role rewrite, `thinking`
strip) and the response repairs (`ModifyResponse`: structured-output
extraction, synthesized usage, mis-spelled tool names) — the same handler
`claude-rolefix` (`cmd/wisp-deck-tui/claude_rolefix.go`) already wraps a
dedicated Featherless pane in. There is nothing here for this router to
reimplement.

So `configRows` no longer skips `RemoteCatalog`, and `routableProfile`
(`credential.go`) no longer refuses it either — `Resolve`'s `KindConfig` branch
reads the resolved `Provider`'s `RemoteCatalog` bit and sets
`Credential.NeedsRepair`. `NewHandler` (`proxy.go`) checks that flag right
where it used to always call `newReverseProxy`: a `NeedsRepair` target is
served by `rolefix.NewHandler(base).ServeHTTP(w, r)` instead — everything the
plain path already did (credential headers swapped in, `model` rewritten from
the `wisp/…` id to the real one, body reassigned onto `r`) has already run by
that point, so `rolefix` only ever sees an ordinary, already-addressed
Featherless request.

- **The credential survives delegation because `rolefix`'s `Director` never
  touches it.** It sets only `req.URL.Scheme`, `req.URL.Host`, `req.URL.Path`
  and `req.Host` — `httputil.ReverseProxy` forwards every other header
  (Authorization included) unchanged, so the credential this router just swapped
  in reaches Featherless exactly as it would from a dedicated pane. Verified by
  `TestHandler_repairs_a_featherless_request_before_it_reaches_the_upstream`,
  which asserts the swapped `Authorization` header on the fake upstream.
- **`FlushInterval: -1` needs no second declaration.** `rolefix.NewHandler` sets
  it on its own `ReverseProxy`, so the keep-alive bytes Featherless sends before
  its first token still stream through unbuffered — this router's own
  `newReverseProxy` sets the same field for the plain path, but delegation does
  not run through it at all. Guarded by
  `TestHandler_streams_a_repaired_targets_first_chunk_before_the_second_is_sent`.
- **`validUpstream` must still run first.** It is what turns a malformed
  address into this package's deterministic 400 rather than a retryable 5xx;
  `rolefix.NewHandler`'s own bad-URL fallback answers 500. The delegation
  branch runs strictly after `validUpstream` succeeds, so it can never reach
  that fallback. Guarded by
  `TestHandler_reports_a_malformed_upstream_as_400_not_502_for_a_repaired_target`,
  which fails with a 500 if the two are ever reordered.
- **`rolefix.NewHandler` sets its OWN `ErrorLog` too, as of this change** —
  it previously had none, so a dial failure on either a dedicated Featherless
  pane or (now) a `NeedsRepair` All-In row printed
  `http: proxy error: dial tcp ...` straight into the agent's pane, the exact
  failure mode this package's own `discardLog` exists to prevent for the plain
  path. Fixed once in `rolefix` rather than re-declared here, so both callers
  get it. Guarded by `TestNewHandler_does_not_log_a_dial_failure_to_stderr`
  (`internal/rolefix/rolefix_test.go`).
- **The handler is built fresh per request, deliberately, not cached.**
  Measured: `rolefix.NewHandler(upstream)` costs 288ns and 4 allocations
  (`url.Parse` plus one `ReverseProxy` struct literal) — dwarfed by the
  milliseconds-to-seconds of the HTTP round trip it is about to make, and this
  router already builds a fresh `newReverseProxy` per request the same way. A
  cache would add a concurrency-safe map for a cost too small to be worth
  measuring against real network I/O — the "measure per-tick cost" discipline in
  the root `CLAUDE.md` is about a background loop running many times a second
  forever; this runs once per LLM turn.
- **`UserConfigured` (the self-hosted `custom` provider) is still never marked
  `NeedsRepair`.** It speaks the Anthropic API directly. The two flags look
  similar (`SuppliesOwnModel()` is true for both) but only `RemoteCatalog` names
  the one needing `rolefix` at all — keying off `SuppliesOwnModel()` instead
  would wrongly route a self-hosted profile's already-conforming requests
  through a rewrite they don't need. Guarded by
  `TestResolve_still_serves_a_self_hosted_profile`.
- **The context floor is unaffected and still applies to Featherless.**
  `providerModels` already built Featherless's single synthetic `Model` from the
  profile's own declared `CLAUDE_CODE_MAX_CONTEXT_TOKENS` (it ships no static
  `Models` list — `SuppliesOwnModel()` is true for it), and `configRows`'s
  `model.Context < minRosterContext` check runs on that `Model` exactly as it
  does for any other provider. Nothing about admitting the provider touches this
  check. Guarded by `TestRoster_omits_a_featherless_model_below_the_context_floor`.

Guarded end to end by `TestRoster_admits_a_ready_featherless_profile`,
`TestResolve_serves_a_featherless_target_and_marks_it_for_repair`,
`TestResolve_does_not_mark_an_ordinary_gateway_for_repair`,
`TestHandler_repairs_a_featherless_request_before_it_reaches_the_upstream`, and
`TestHandler_leaves_an_unrepaired_target_untouched` (the selectivity
counterweight — nothing routes through `rolefix` except a `NeedsRepair`
target).

### `Env` is built twice, from two different files, and both must agree

`Roster` and `Resolve` are never called from the same `Env`. `Roster` (via
`EnsureProfile`, through the shared `EnsureProfileIfEligible` gate) is reached
from the CLI's `ensure-allin`/`add`/`delete` (`cmd/wisp-deck-tui/claude_config.go`)
and from the TUI's own login and subscription add/delete/disable
(`internal/tui/subscription_modal*.go`, via `(*MainMenuModel).ensureAllIn`) —
and also from opening the Subscriptions modal itself
(`openSubscriptionModal`, `internal/tui/subscription_modal.go`), which is a
read rather than a mutation: it is the only call site that catches a machine
whose sources were all connected before this feature existed, or a session
that never mutates anything at all. Every one of those call sites builds its
`allin.Env` from
`${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck`, whether through `bin/wisp-deck`'s
`CONFIGS_DIR` (`bin/wisp-deck:194`) or the TUI's own `gt_config_dir`
(`lib/menu-tui.sh`). `Resolve` is reached only from the launch wrapper:
`gt_claude_launch_wrapper` (`lib/tmux-session.sh`) builds its own
`config_root` and passes it to `claude-allin`
(`cmd/wisp-deck-tui/claude_allin.go`), which never runs `Roster` or
`EnsureProfile`.

The two are **textually independent recomputations** of
`${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck` — `CONFIGS_DIR` is not exported,
and `lib/tmux-session.sh` never reads it. Editing one without the other does
not error: it makes the roster get built from one directory while the router
resolves credentials from another. A row's picker id would then name a
profile the router's `ConfigsDir` cannot find, and `Resolve` would return
`allin: profile "…" is not ready` for a profile that plainly is. Change the
config root in `bin/wisp-deck` and `lib/tmux-session.sh` together, or not at
all.

### A disabled subscription contributes no row, and `Resolve` does not need to know

`claudeconfig.LoadDisabled` (the same sidecar `internal/tui/subscription_modal.go:219`
already reads for the switcher popup) hides a config from `configRows` and from
`SourceCount`, which is derived from `Roster` itself. A machine measured with
two logins and four ready providers, all four disabled, produced 19 picker rows
before this — 11 of them for providers the user had explicitly turned off — and
would still read as eligible on a machine whose only non-login sources were all
disabled, when it had nothing left to route between.

This is `configRows` deciding what to OFFER, not `routableProfile`
(`credential.go`) deciding what the router can ADDRESS — and the two do not
need to agree here. `routableProfile`'s two rules exist because the router
structurally cannot serve that request (no key to swap in, no repair proxy);
disabling a profile changes nothing about whether it works, only whether the
picker offers it. `useSubscriptionProfile` already lets a user select a
disabled profile directly as their Standard-Claude-mode subscription, so a
stale All-In picker default saved before the user disabled that provider is
left to keep working rather than 400 with "not ready" — consistent with
disabled meaning hidden from discovery, never revoked.

Disabling or re-enabling a profile refreshes an existing All-In profile
immediately: `toggleSubscriptionProfileDisabled` calls `ensureAllIn` right
after `claudeconfig.ToggleDisabled`, the same way every other mutation site
does. A profile left with only one login's rows because its only other source
got disabled is not deleted — the same "never delete below two sources"
contract `TestEnsureProfileIfEligible_refreshes_an_existing_profile_below_two_sources`
already pins for a removed login. Guarded by
`TestRoster_omits_rows_for_a_disabled_config`,
`TestSourceCount_excludes_a_disabled_config`,
`TestEnsureProfileIfEligible_writes_nothing_when_the_only_second_source_is_disabled`,
`TestEnsureProfileIfEligible_refresh_drops_rows_for_a_source_disabled_after_creation`,
and `TestToggleSubscriptionProfileDisabled_refreshes_an_existing_allin_profile`.

### Models narrower than 200000 tokens are not offered

`minRosterContext` in `roster.go` is Claude Code's own floor: the tool
schemas, system prompt, and agent/skill rosters it sends before a conversation
starts already cost roughly this many tokens (see the "Claude Code's floor" and
"window holds the reply too" sections in the root `CLAUDE.md`), so a narrower
model cannot complete a single turn. `configRows`'s check is
`model.Context != 0 && model.Context < minRosterContext` — a `Context` of
exactly zero skips the floor entirely, because zero also means "the catalog
never populated this field." A `SuppliesOwnModel` provider (Featherless,
custom/self-hosted) ships no static `Models` list at all, so `providerModels`
must build the one synthetic `Model` for it by reading the profile's own
declared window (`claudeconfig.ReadContextWindow`) and rejecting it outright
when that value is missing or non-positive — returning a `Model` with
`Context` left at zero would read as "no floor" and offer a model narrower
than Claude Code can run a turn on. Guarded by
`TestRoster_omits_a_model_too_narrow_for_claude_code` and
`TestRoster_omits_a_self_hosted_model_too_narrow_for_claude_code`.

### A ChatGPT row is served by a Codex bridge this launch starts lazily

ChatGPT was the one subscription this router excluded: `configRows` skipped any
provider that was not `AuthAPIKey`, and `routableProfile` refused it, because a
ChatGPT profile stores **no endpoint and no key** — Codex authenticates it and a
bridge process serves it. It is admitted now, and the rule both sides share is
`routableAuth` (`credential.go`): `AuthAPIKey` and `AuthCodexChatGPT` are
routable, `AuthWispRouter` is not. One predicate, called from `configRows` and
from `routableProfile`, so the picker can never offer a row the router refuses.

The Codex knowledge lives entirely behind `gptbridge.ChatGPTBridge`, an
`Endpoint() (baseURL, key string, err error)` this package sees through its own
one-method `ChatGPTBridge` interface. `FileResolver.Bridge` holds it beside
`Token`, and `Resolve`'s ChatGPT branch just returns
`Credential{BaseURL: <loopback>, Header: "Authorization", Value: "Bearer <key>"}`.
`proxy.go` is untouched: a ChatGPT turn is an ordinary reverse-proxy forward to
a loopback address, so it already gets `FlushInterval: -1` (the bridge answers a
reasoning turn with `event: ping` alone for minutes — buffering those would
manufacture the very silence Claude Code's byte watchdog aborts on) and the
`discardLog` ErrorLog.

- **The branch sits BEFORE the key/endpoint read.** A ChatGPT profile fails
  `key == "" || base == ""` by design, so placing it after answers a working
  subscription with `profile "openai-chatgpt" is not ready`. Guarded by
  `TestResolve_serves_a_chatgpt_target_through_the_bridge`.
- **`NeedsRepair` stays false.** `rolefix` rewrites a request for Featherless's
  stricter *published* schema; the bridge reads the fields it strips. Only
  `RemoteCatalog` is repaired.
- **The id reaching the bridge is the bare Codex id.** `Route` strips
  `wisp/cfg.<profile>/` and `rewriteModel` puts the remainder back in the body,
  so `gpt-6-astra` is what `Engine.Execute` checks against its allowlist —
  which is whatever the **running** app-server reported from `model/list`, not
  the catalog. Guarded by
  `TestHandler_sends_a_chatgpt_turn_to_the_bridge_with_the_bare_codex_model_id`.
- **The catalog and that allowlist are two different lists, and All-In is what
  makes them matter.** `claudeconfig`'s `openai-chatgpt` `Models` are written by
  hand; the roster turns each into a picker row; the engine refuses any the
  running app-server does not report. Probed against a live 0.153.4 app-server
  on 2026-09-08, `includeHidden:false` (what `StartAppServer` asks for) returns
  seven: `gpt-6-astra, gpt-5.6-sol, gpt-5.6-terra, gpt-5.6-luna, gpt-5.5,
  gpt-5.4-mini, gpt-5.3-codex-spark`. `includeHidden:true` adds only
  `gpt-reserve` and `codex-auto-review`, both `hidden`, and neither reaches the
  allowlist. The catalog held an eighth, `gpt-5.4`, which is served under
  neither flag — a row that resolved and then 400d — so it was removed.
  `TestLiveChatGPTCatalogMatchesTheAppServer` (env-gated, one app-server start,
  no quota) is what catches the next drift, in both directions. Note the live
  list is per-subscription: a narrower ChatGPT plan legitimately reports fewer,
  so read a failure against the account before editing the catalog.
- **ChatGPT rows land at the flat 200k window like every other row.** The whole
  5.6/6 tier declares 272000 (over `minRosterContext`, so it is offered) and
  `gpt-5.3-codex-spark`'s 128000 is dropped. No row carries `[1m]`, for the
  reason the section above gives. Guarded by
  `TestRoster_never_offers_a_1m_chatgpt_row` and
  `TestRoster_omits_a_chatgpt_model_too_narrow_for_claude_code`.

### The bridge starts on the first GPT turn, is reused, and is shut down by hand

Three invariants, each with a guard, and each is about a 220MB process:

- **Lazy.** `newClaudeAllInCommandWithBridge` constructs the bridge for every
  launch and starts nothing; only `chatGPTCredential` calls `Endpoint`, and only
  a `AuthCodexChatGPT` target reaches it. A session that never picks a GPT row
  never execs Codex. Guarded by
  `TestHandler_never_starts_a_bridge_for_a_non_chatgpt_turn` and, in gptbridge,
  `TestChatGPTBridge_starts_nothing_until_a_turn_asks_for_an_endpoint` — which
  checks the struct as well as the seam, because an eager start in the
  constructor runs before a test can install the seam.
- **Reused.** The router asks once per ChatGPT turn; deduplication is the
  facade's job. `ChatGPTBridge` holds its mutex across the whole start, so eight
  concurrent first turns share one app-server. A **failed** start is never
  cached — the usual cause is a signed-out Codex, fixed between turns.
- **Shut down before the exit code.** `runLoopbackWrappedLaunch` takes a
  `cleanup func()` (nil for claude-rolefix) and runs it at the top of `finish`,
  on every route out. It cannot be a `defer`: `exit` is `os.Exit` in production
  and runs no deferred function, which is the route that actually happens when
  Claude exits. Guarded by
  `TestClaudeAllIn_closes_the_chatgpt_bridge_before_it_propagates_an_exit_code`.

The routes Go never gets to run anything on are covered by two measured facts.
`kill_tree` (`lib/process.sh`) is depth-first — children before the parent — so
a window close reaps the app-server while claude-allin is still its parent. And
a `respawn-pane -k` or a SIGKILL closes the pipe holding the app-server's stdin:
measured, a real `codex app-server` exits **7.6ms** after stdin EOF
(`TestLiveCodexAppServerExitsWhenItsParentPipeCloses`, env-gated). Between them
there is no orphan route, which is why claude-allin installs **no** signal
handler — `signal.NotifyContext` would suppress the default action and leave the
wrapper alive after a SIGTERM meant to kill it, while nothing in the blocking
`child.Run()` watches a context.

**The cold start is not hidden and does not need to be.** Measured on this
machine at load average 25: **2.15s** through the npm shim and **2.25s** for the
220MB native binary copied to a fresh inode (so the exec goes genuinely cold),
436ms warm. Claude Code paints its stall banner after **20s** of raw-byte
silence, so no keep-alive scaffolding is warranted — and the start finishes
before any response header is written, so there is no `text/event-stream` body
for that watchdog to be armed on yet. `StartupTimeout` is 60s (not
`RunAdapter`'s three minutes, which is paid at pane launch rather than inside a
turn), so the worst case is a deterministic 400 well inside the 180s abort
budget. `TestLiveChatGPTBridgeStartsAndServes` re-measures it and fails at 20s.

- **Codex is never signed in from here.** A dedicated GPT pane owns the terminal
  before Claude starts, so `RunAdapter` can open a browser login; here the only
  writable stream is the pane Claude Code is painting on (see
  `wrapper-stderr-is-the-ai-pane` in project memory), so `buildAppServer` reports
  a signed-out Codex as a turn error naming `codex login`.
- **Every bridge failure is a 400**, like every other routing failure — a
  signed-out Codex, an absent Codex, a bridge that will not start. Claude Code
  retries a 5xx about eleven times, and none of these get better on a retry
  inside one turn. Guarded by
  `TestHandler_reports_a_bridge_that_cannot_start_as_400_not_502`, whose
  fail-open mutant (dial a dead loopback port) really does answer 502.
- **`Close` waits for a start in flight.** It takes the same mutex `Endpoint`
  holds across the whole start, so a graceful `/exit` during the very first GPT
  turn's startup blocks until that start finishes — 2s normally, the 60s cap at
  worst. Left as is: a wedged start means the turn is hanging anyway, and every
  abrupt exit (Ctrl-C, window close, respawn) goes through `kill_tree` or the
  stdin-EOF path and never reaches `Close` at all.
- **An existing All-In profile gains ChatGPT rows on the next `ensure-allin`,**
  not on a `git pull`: `EnsureProfile` rewrites `modelPicker` only when a
  mutation site or `bin/wisp-deck` runs it. Opening the Subscriptions modal is
  the read that catches a machine which mutates nothing.
- **The Codex path comes from `WISP_DECK_CODEX_CMD`,** which `wrapper.sh` stamps
  into the tmux session env (and `lib/tab-view.sh` re-exports for a new tab), so
  every process in the pane inherits it however deep the launch chain nests. No
  change to `gt_claude_launch_wrapper` was needed. A **relative** value is
  dropped rather than resolved — it would exec against whatever directory the
  pane sits in — and reads as absent, which is the bridge's own 400 naming Codex.

### Known exposure: the loopback port mints turns on any credential, unauthenticated

The router will mint a turn on **any** login's OAuth token or **any** provider's
API key, chosen by a string in the request body, for anything that can reach its
loopback port. `claude-rolefix` does not do this — it forwards the credential the
client already had — so this is a real escalation over the proxy it is modelled
on, and it is recorded here rather than fixed.

It is **not** an escalation over what a local process on this machine can already
do. `keychainToken` shells out to `security find-generic-password -w`, which
returns without a prompt for the user's own login keychain, and every provider key
sits in a 0600 file the same process can read. Anything that can open the loopback
port can already read both sources directly.

The right end state is a shared secret: mint a random id at launch, stamp it into
the settings overlay as a header the client sends, and refuse a request without
it — the same shape `gptbridge` already uses for `randomBridgeID`. It was
deliberately **not** bundled with the profile-shape redesign this wave: an auth
scheme added alongside a Critical fix, with one review pass left, is how a worse
bug ships. Add it as its own change, with its own tests.

### The stream watchdog is disarmed inside `EnsureProfileIfEligible` itself, not left to a sweep

A profile can now be born on a keypress — the moment a second subscription
connects, from the TUI or the CLI's `add`/`delete` — never touching
`bin/wisp-deck`'s `ensure-watchdog` sweep at all. `EnsureProfileIfEligible`
therefore calls `claudeconfig.EnsureStreamWatchdog` itself, right after
`EnsureProfile` writes the file, rather than relying on that install-time
sweep to catch up on the next run. Without it, an All-In pane routed to a
gateway sits with the event-tier watchdog armed for its whole life: root
`CLAUDE.md`'s "keepalive buys 30 pings" section measures the consequence — a
stream carrying only keepalives is aborted and replayed at 610s, and the
replay repeats the same work.

**Never move this into `routerEnv`.** That block rewrites its keys on every
call, while a declared watchdog value is documented as the user's own and is
kept untouched by every other path (`stampStreamWatchdog` already no-ops on a
key that is already declared) — writing it there would blow away a value the
user set deliberately. `EnsureStreamWatchdog` needs the settings object to
already have an `env` key, which `routerEnv` guarantees the file always has by
the time this runs. Guarded by
`TestEnsureProfileIfEligible_disarms_the_stream_watchdog_on_a_freshly_created_profile`.

### An Explore subagent stays on the picked row only because `routerEnv` disarms the cap

Claude Code's built-in Explore agent is the one agent type that does not inherit
the session model. Decoded from 2.1.267: `LX(agent, mainLoopModel)` returns
`{inheritCap:"opus"}` whenever the mainLoopModel's id names no haiku / sonnet /
opus family member (`edo` → `!F7t(id, ["haiku","sonnet","opus"])`), and the cap
then resolves through the alias to the first-party `claude-opus-5`. Every
`wisp/…` row names a provider, so its id names no family either — the cap is
always armed in an All-In session.

`Route` places that bare id as `KindSession`, so the turn leaves on the
session's OWN login at api.anthropic.com, not the subscription the user picked.
Measured on a live All-In pane: three Explore subagents ran on claude-opus-5 and
died on the Claude subscription's weekly limit, while that same session's
general-purpose subagents ran on the picked row (the roster and inheritance were
never the problem — general-purpose inherits, and inheriting a `wisp/…` id
routes correctly).

`CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP=1` is the only switch: `LX` returns
plain `"inherit"` when it is set, and the subagent then carries the row id
through the router like every other turn. It lives in `routerEnv` because the
profile is what every session reads. Measured end to end with one Explore
subagent per run, key absent then present: without it the subagent's first
request carried `claude-opus-5`, with it `wisp/cfg.deepseek/deepseek-flash`.

Residual, not fixed by the cap alone: a family model named on the Agent call
itself, or in an agent's own frontmatter (`statusline-setup` ships
`model:"sonnet"`, plugins ship their own), outranks inheritance and resolves the
same way — measured: `model: sonnet` on the call carried `claude-sonnet-5` into
the router.

Guarded by `TestEnsureProfile_disarms_the_explore_inherit_cap`.

### …and every other subagent only because `routerEnv` forces inheritance

Inheritance is the weaker of the two sources. Decoded from 2.1.268, the
resolver `OH(agentSpec, mainLoopModel, toolModel, …)` takes its model from the
first of: the model named on the Agent call, the agent's own declared model,
the env default, and only then the parent. `CLAUDE_CODE_SUBAGENT_MODEL_FORCE`
drops the first two — `ozn` returns `undefined` for both when it is set — which
leaves the subagent inheriting the session model, exactly like Explore above.
Dropping them is the point, not a side effect: superpowers' own
`subagent-driven-development` skill tells the model to *always* name one
("An omitted model inherits your session's model — often the most capable and
[expensive]"), so a session that follows its own instructions names `sonnet` for
every implementer on essentially every dispatch.

This profile carries none of the four `ANTHROPIC_DEFAULT_*_MODEL` aliases, and
deliberately: the row is a per-session choice, so no static alias can name it.
`Mp()` / `PY()` therefore fall through to the first-party ids — `sonnet` to
`claude-sonnet-5`, `haiku` to `claude-haiku-4-5-20251001` — and `Route` reads
each as `KindSession`, billing the session's OWN Claude subscription.

Measured end to end on a real 2.1.268 pane driven by a local endpoint, one
`Agent(model:"sonnet")` call per run, key absent then present: without it the
subagent's first request carried `claude-sonnet-5`, with it
`wisp/cfg.deepseek/deepseek-flash`.

It pairs with `CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP` rather than replacing
it, and the order matters: the force keeps an object-shaped spec's `inheritCap`
(`ozn`'s `r && W5()==="inherit"` branch), so with the cap still armed Explore
resolves to opus anyway. Both keys are pinned by
`TestEnsureProfile_forces_subagents_onto_the_picked_row` and its neighbour.

Not measured, and left alone: the small/fast alias behind Claude Code's own
background calls (the title generator, the classifiers). `og()` returns the
main model whenever `ANTHROPIC_SMALL_FAST_MODEL` is unset and the provider is
not first-party — which the router's loopback endpoint guarantees — and the
one live run above showed the title request carrying the session's `wisp/…` row
rather than a haiku id. If a future build re-derives that alias from
`ANTHROPIC_DEFAULT_HAIKU_MODEL` regardless of provider, those calls would leak
to api.anthropic.com the same way; a `tcpdump`-free check is to add a `model`
log line to the router and read it for one session.

### `ensure-allin` and `claude-allin` name the same two files the same way

Both take `--configs-list` and `--configs-dir` (plus `--accounts-list` and
`--accounts-dir`). `ensure-allin`'s siblings under `claude-config` use bare
`--list`/`--dir`, but this command already takes an explicit `--accounts-*`
pair, so the bare names identified the configs pair only by omission — and the
two commands read the same files from the same roots. `bin/wisp-deck` passes the
long names; see also the `Env` section above, which is what those roots must
agree with.

### Known limitation: there is no token refresh

An account whose Keychain access token has expired resolves as
`ErrStaleAccount`, and the 400 response names the login by directory so the
user can fix it — but the fix is manual: open a wisp-deck tab on that login
once, which makes Claude Code itself refresh the token. Nothing in this
package attempts a refresh. This is a stated v1 decision (see "Открытые
риски" in the spec), not a bug to fix reflexively.

### `allin` is a third key in `get_claude_config_provider`'s allowlist, and it is admitted for a different reason than the other two

`ConfigReady` (`internal/claudeconfig/claudeconfig.go`) treats `AuthWispRouter`
as always ready — the profile's credentials are resolved per request from the
Keychain, so there is nothing local to check. That marks the switcher's
All-In row selectable on the Go side, but the shell side that actually
performs the switch (`lib/account-switch.sh`) never learned the same fact:

- `get_claude_config_provider` (`lib/claude-configs.sh`) only returns a marker
  it allowlists; `allin` was absent, so it read as no marker at all.
- With no marker, `_subscription_choice_ready`'s name-fallback `case`
  (`lib/account-switch.sh`) matches nothing and lands on `provider=zhipu`.
- Zhipu is an `AuthAPIKey` provider in spirit, so readiness fell through to
  `jq -er '.env.ANTHROPIC_AUTH_TOKEN | select(...)'` — a check the generated
  All-In profile can never pass, because it deliberately carries no
  `ANTHROPIC_AUTH_TOKEN` (`routerEnv` in `profile.go`).

So clicking All-In in the pill's menu did nothing: the row was offered
(`ConfigReady` said yes) and then silently refused (the shell said no).

`allin` joins the allowlist for a **different reason** than `featherless` (the
gateway needing the repair proxy) or `openai-chatgpt` (the GPT bridge): no
shell launch decision is keyed to it — `wrapper.sh`, `lib/tmux-session.sh` and
`lib/account-switch.sh` all branch only on `openai-chatgpt` or `featherless`
literal strings, never on `allin`. It exists purely so
`_subscription_choice_ready` can name the router profile and give it its own
branch — `return 0` before the token check — mirroring the honest parallel
already in that function: the ChatGPT branch, which likewise cannot be judged
by a token.

Guarded by `TestApplyAccountSwitchChoice_allin_relaunches_without_a_token`
(`test/bash/account_switch_subscription_test.go`), which fails two different
ways depending on which half of the fix is missing: no allowlist entry means
`get_claude_config_provider` reports no marker at all, while no `allin` branch
in `_subscription_choice_ready` means the marker resolves correctly but the
switch is still refused on the missing token.

### The implicit login's row is labeled by the user's tag, and `DefaultLabelFile` is optional on purpose

`accountRows` used to hardcode `{"Default", "default"}` for the implicit
login, so a user who tagged their Keychain login "Work" (the Subscriptions
modal's LOGINS section, persisted at
`claude-account-default-label`) still saw "Default · Opus 5" in Claude Code's
own status bar — the picker's `label` field is what that bar prints verbatim,
so the router disagreed with the rest of the product about the login's name.
`accountRows` now reads `claudeaccount.GetDefaultLabel(env.DefaultLabelFile)`
for that row's label instead.

- **The row id is untouched.** It still names the directory (`acct.default`),
  never the label — a saved picker default must survive a later relabel.
  Guarded by `TestRoster_row_ids_are_unchanged_by_the_default_label`.
- **`DefaultLabelFile` is deliberately NOT one of the four required `Env`
  paths `EnsureProfileIfEligible` refuses on.** `GetDefaultLabel` already
  degrades an empty path, a missing file, or an empty file to `"Default"`, so
  a construction site that never learned about the field keeps working
  exactly as before rather than silently failing every call. Making it
  required and forgetting to plumb even one of the (currently five) call
  sites would have turned that site's `EnsureProfileIfEligible` into a
  permanent no-op — see `EnsureProfileIfEligible`'s own four-path comment.
  Guarded by `TestEnsureProfileIfEligible_succeeds_with_no_default_label_file`.
- **Every construction site is plumbed anyway**, for consistency rather than
  necessity: `cmd/wisp-deck-tui/claude_config.go` (`ensureAllInFromCLI`, the
  standalone `ensure-allin` command, and the `add`/`delete` subcommands),
  `internal/tui/subscription_modal_allin.go`, `bin/wisp-deck`'s `ensure-allin`
  invocation, and `lib/config-tui.sh`'s `add`/`delete` calls (the legacy
  "Manage Claude configs" menu). `cmd/wisp-deck-tui/claude_allin.go` (the
  `claude-allin` launch wrapper) takes the flag too even though `Resolve`
  never reads it — that command never calls `EnsureProfileIfEligible` at all,
  only `NewResolver`, so the field is inert there; it is wired solely so this
  `Env` stays shaped the same as every other construction site.

### A hidden row is left out of the picker, and nowhere else

The Subscriptions modal's MODEL ROUTING block is a checklist for this profile:
one checkbox per picker row, toggled with Enter, Space or a click. The four
Opus/Sonnet/Haiku/Fable mappings every other provider shows are inert here —
`cycleSubscriptionMapping` returns early on the empty model list an All-In
profile has — so the pane offered four rows reading "(none)" that nothing could
change. A machine with several logins and providers produces well over a dozen
picker rows, and a user wants only some of them in `/model`.

State is a sidecar beside the configs list, `claude-allin.hidden`, one picker
model id per line — the same shape, and the same load/toggle pattern, as
`claude-configs.disabled`. `hidden.go` reads it as **whole lines**: a Featherless
model id carries its own slashes, for the same reason `Route` cuts on the first
two only.

- **The filter lives in `EnsureProfile`, at the one line that builds
  `options`.** Not in `Roster`: the modal's checklist renders from the roster,
  so a hidden row must still appear there to be un-hidden, and `SourceCount` is
  derived from `Roster` — filtering there would move the `< 2 sources` gate that
  decides whether the profile is created at all. Guarded by
  `TestRoster_still_reports_a_hidden_row` and `TestSourceCount_ignores_hidden_rows`.
- **`Resolve` never reads the file.** Hiding is a display preference, never a
  revocation: a session whose saved picker default was hidden after the fact
  must still finish its turn.
- **The empty picker is guarded twice.** `replaceBuiltInOptions` leaves no
  built-in row to fall back on, so a zero-option picker is a session with no way
  to change model at all and no escape from inside it. The modal refuses to
  uncheck the last visible row and says why; `EnsureProfile` independently
  writes the full roster when the file would leave nothing, which is what
  catches a hand-edited file.
- **A toggle writes immediately and refreshes the profile**, the way `x` already
  disables a subscription. It never goes through the draft or "Save changes":
  it is a property of the machine, not of the profile being edited.

On the TUI side (`internal/tui/subscription_modal_allin_models.go`) the checklist
is a **span** of cursor values, `subscriptionDetailAllInBase + index`, appended
past every fixed `subscriptionDetail*` constant. Two things fall out of that:
the mouse hit test must run the checklist branch first and skip the alias loop
entirely for All-In (a row label like "Default · Opus 5" contains "Opus", which
that loop searches for as a substring), and the renderer and the hit test must
shape a label through the one `subscriptionAllInRowText` — the hit test locates a
row by searching the rendered line for that exact string, so a label truncated
one way and searched for the other never matches.

### A row says what its subscription has left, and a spent one is left out

Every row's description ends with what the credential it spends has left
(`Zhipu / GLM · 75% left`), and by default a source with nothing left contributes
no row at all. Both are read from the usage caches the statusline already
maintains, so the picker never reaches for a network on a path that writes a
settings file. `usageFreshFor` is the same 2h `gt_sub_usage_fresh` uses
(`lib/statusline.sh`), so the two surfaces can never disagree about whether a
number is real.

**Claude Code snapshots `modelPicker` ONCE, at launch** — verified live: editing
a profile under a running pane left `/model` showing the old rows, and a row
added after launch never appeared. Everything else here follows from that:

- **Unknown is never exhausted.** `Quota.Known` is false for a source with no
  cache, an unreadable one, a stale one, or a provider reporting no windows, and
  `Exhausted()` requires it. A wrong hide cannot be undone until the next
  launch, so offering a spent row costs one failed turn while hiding a working
  subscription costs the whole session.
- **The filter never empties the picker.** `replaceBuiltInOptions` leaves no
  built-in row to fall back on, so `EnsureProfile` keeps the unfiltered options
  whenever the filter would leave nothing — a deck whose subscriptions are all
  spent still has rows. That is also why the TUI toggle needs no last-row guard,
  unlike `toggleAllInRow`.
- **`Resolve` never reads usage**, exactly like a hidden row: a session whose
  saved picker default ran out mid-conversation must still finish its turn.
  `Usage` is called from `profile.go` alone.
- **A quota belongs to a SOURCE, not a row.** `SourceKey` reduces a row to the
  credential it spends, so one login's rows all carry the same number and all
  vanish together.
- **Nothing refreshes these caches at launch.** Opening a pane used to arm a
  round that fetched every login AND every enabled profile, so a deck spent a
  request per source on subscriptions the session never routes to, and read
  every login's Keychain entry on the way — refreshing, and therefore
  ROTATING, the OAuth token of logins nobody had opened a pane on. It was
  removed: the picker reads whatever the caches already hold, and
  `account-usage` / `subscription-usage` are what write them, on demand.
  Guarded by `TestClaudeAllIn_fetches_no_usage_at_launch`
  (`cmd/wisp-deck-tui/claude_allin_test.go`), which pre-writes fresh account
  caches so the throttle keeps a failing run off api.anthropic.com and only the
  provider stub can be reached.
- **So a number goes unknown rather than stale.** Past `usageFreshFor`,
  `quotaFromSnapshot` returns an empty `Quota` and `Known` is false, which drops
  the `N% left` suffix and — since `Exhausted()` requires `Known` — makes the
  spent-row filter inert. That is the safe direction the section above already
  argues for: a wrong hide stands for a whole session, an offered spent row
  costs one turn.

The behavior is a checkbox in the modal's All-In block, stored beside the
configs list as `claude-allin.hide-exhausted` — the same sidecar shape as
`claude-allin.hidden`. **Absent means on**, and an unreadable file reads as on
too. Like a hidden row it writes immediately and refreshes the profile, never
going through the draft: it is a property of the machine, not of the profile
being edited.

`subscriptionDetailAllInHideSpent` (`internal/tui/subscription_modal.go`) had
exactly one legal slot — after `subscriptionDetailImages` and before
`subscriptionDetailAllInBase`, which is a span nothing may follow. It renders as
the first line of the block, so every checklist row moved down one line and
`subscriptionDetailCursorLine` moved with it.

Guarded by `internal/allin/usage_test.go`,
`internal/tui/subscription_modal_allin_hidespent_test.go`,
`cmd/wisp-deck-tui/account_usage_cmd_test.go` and
`cmd/wisp-deck-tui/claude_allin_test.go`.

### Two rows in `/model` belong to Claude Code, not to the roster

`replaceBuiltInOptions` curates the lineup; it does not own the list. Two rows
come back whatever `EnsureProfile` writes, and both have been read as bugs once.

- **`Default (recommended)` cannot be removed.** With `replaceBuiltInOptions`
  true, the curation returns `[...e.filter((C)=>C.value===null), ..._]`, and the
  built-in row it keeps is the one whose `value` is `null` — the Default row.
  No setting drops it; the schema text is explicit that the picker then "shows
  only the Default row and these options". It carries no `wisp/` id, so `Route`
  reads a turn picked there as `KindSession` and it spends the pane's own login
  rather than a chosen subscription.
- **The session's current model is re-added as its own row when it equals no
  option's `value`.** The comparison is verbatim, and the appended row is
  labelled from the model family, so it names no source and shows no quota.
  Neither the hidden set nor the spent filter reaches it: both shape the options
  list, and this row is pushed after that list is curated. It still routes,
  because `Route` accepts an id with no marker, but a Claude row saved before
  `OneMillionMarker` existed now reaches the upstream without the 1M beta
  header. Picking any row rewrites the stored value and the extra row goes.

### A borrowed login's OAuth token is refreshed here, because nothing else does

Claude Code refreshes the login of the config dir it is RUNNING under. All-In
borrows another login's credential without ever starting a process under that
dir, so a login nobody has opened a pane on simply ages out: its Keychain entry
keeps a valid `refreshToken` while its `accessToken` goes stale, and every turn
routed to that row gets `401 OAuth access token has expired` from Anthropic.
Measured on this machine: the `personal` login's entry was last touched
2026-09-14 11:02 and its token expired 2026-09-14 18:18, while the default
login — which had a live pane — was valid throughout.

The 401 is the expensive half. It comes from the upstream, not from
`writeRoutingError`, so it is outside the "every routing failure answers 400"
rule above and Claude Code retries it about eleven times before the user sees
anything.

`keychainToken` (`keychain_darwin.go`) therefore reads `expiresAt` and
`refreshToken` as well, and `freshToken` (`refresh.go`) refreshes through
`internal/proxy`'s `RefreshToken` when the token is inside `refreshSkew` of
expiring. Three things that look optional and are not:

- **The skew is wider than a turn.** A token minutes from expiring outlives the
  request that swaps it in but not the stream it starts, and that mid-turn 401
  lands on the same eleven-retry ladder.
- **The refresh is taken under a cross-process lock, and the entry is re-read
  inside it.** A refresh ROTATES the refresh token, so two panes refreshing one
  login at once means the loser spends a token the server has already retired —
  and is then permanently unable to refresh. The lock is keyed by the Keychain
  service name, so two logins never wait on each other.
- **A failed write-back does NOT fail the turn.** The rotation already happened
  upstream by then, so refusing would lose the turn without saving the login.

The write patches the three OAuth fields into the existing blob rather than
rebuilding it: `subscriptionType`, `scopes`, `rateLimitTier` and
`refreshTokenExpiresAt` sit beside them and Claude Code reads this same entry
when a pane does run under that dir. It also has to name the item's own `acct`
attribute, read back from `security`, because `-U` matches on the attributes it
is given — a guessed account creates a SECOND item under one service name and
leaves the read answering whichever the Keychain returns first.

The password reaches `security` as an argv value. There is no alternative:
`add-generic-password` ignores stdin (verified — it stores an empty password)
and prompts on a tty when `-w` is omitted. It costs nothing anyway, since a
process that can read the entry unprompted could read the token directly.

An entry recording `expiresAt: 0` is served as-is. Refreshing it would rotate
the refresh token on every single request.

Nothing sweeps the other logins for this. `Resolve`'s `KindAccount` branch is
the only automatic caller, so a borrowed login is refreshed by the first turn
that routes to it and not before — which is the whole point: a login the user
never picks is never touched. `wisp-deck-tui account-usage` does walk every
login on the machine, but **nothing in the tree invokes it** (`lib/`, `bin/`,
`templates/` and `scripts/` carry no reference); it is a by-hand command.
