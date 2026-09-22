# All-In always offers the latest models

## Goal

The All-In picker should offer each source's newest models without a code
change. When Anthropic ships Opus 5.5, or a provider ships a new version, the
picker should pick it up by itself.

Today the Claude rows come from `claudeLineup` (`internal/allin/roster.go`), a
pinned list. Provider rows come from the static `Models` in
`internal/claudeconfig/catalog.go`. On 2026-09-22 the pinned list already
missed `claude-opus-5-5`, which Anthropic listed on 2026-09-21.

## Decisions (agreed with the user)

- Scope: every provider, not only Claude.
- Rows: the newest model **per family**, not every model.
- Refresh runs in the router, off the launch path.

## Evidence gathered (2026-09-22)

| Source | List endpoint | Dates | Window in listing |
|---|---|---|---|
| Anthropic, OAuth login token | `GET https://api.anthropic.com/v1/models` → 200, no beta header needed | `created_at` | no |
| Zhipu | `GET <base>/v1/models` (`https://api.z.ai/api/anthropic/v1/models`) → 200, Anthropic shape | `created_at` | no |
| DeepSeek | `GET https://api.deepseek.com/models` → 200. `<base>/v1/models` under `/anthropic` → 404 | none | `context_window` |
| Kimi For Coding | `GET <base>/v1/models` (`https://api.kimi.com/coding/v1/models`) → 200 | all four share one date | `context_length` |
| ChatGPT | `~/.codex/models_cache.json`, written by Codex itself | none | `context_window`, `visibility` |
| MiMo | `<base>/v1/models` → 404, `/v1/models` on host → 401 (key likely dead) | unverified | unverified |
| Moonshot open platform | no key to probe | unverified | unverified |

Anthropic's list covers the ids in `claudeLineup` and adds `claude-opus-5-5`.
The Kimi listing has drifted from the catalog: `kimi-for-coding` is now
"K2.8 Preview" at 1048576 tokens, while the catalog still says 262144.

## Design

### 1. Family rule (pure function)

`Latest(models []Listed) []Listed`, where `Listed` is `{ID, Label, Created,
Context}`.

- Split the id on `-`. A segment is a version if it is numeric (`5`, `5.3`), or
  a short letter prefix followed by a number (`v4`, `k3`, `k2.7`). That
  prefix stays in the family key and the number becomes the version.
- An 8-digit segment (`20251001`) is a date. It is dropped from both the family
  key and the version, and is used only as a tiebreak.
- Family key = the non-version segments, joined. The version is the numbers in
  order, compared as a tuple (`5-5` → (5,5) beats `5` → (5)).
- Keep the highest version in each family. Break a tie on `Created`, then keep
  the first one in the listing.
- Output keeps the listing's first-seen family order.

Expected results on the 2026-09-22 listings:

- Claude: `claude-opus-5-5`, `claude-fable-5-1`, `claude-sonnet-5`,
  `claude-haiku-4-5-20251001`.
- Zhipu: `glm-5.3`, `glm-5.3-flash`, `glm-5.3-flashx`, `glm-5-turbo`. Also
  `glm-4.5-air`, which the 200k floor then removes, because the catalog knows
  it is 131072.
- DeepSeek: `deepseek-flash`, `deepseek-v4-pro`.
- Kimi For Coding: `k3`, `k3-256k`, `kimi-for-coding`,
  `kimi-for-coding-highspeed`.
- ChatGPT: `gpt-6-astra`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`,
  `gpt-5.5`, from `visibility: "list"` rows only.

### 2. Fetchers

Each fetcher returns `[]Listed` or an error. It never panics, and it uses a
short HTTP timeout.

- **Anthropic shape:** `GET <url>` with the source's auth header and
  `anthropic-version: 2023-06-01`. It reads `data[].id`, `display_name` and
  `created_at`, and reads `context_length` / `context_window` when present. It
  follows `has_more` / `last_id` pagination.
  - Claude uses `https://api.anthropic.com/v1/models`.
  - An API-key provider uses `<profile ANTHROPIC_BASE_URL>/v1/models`, or the
    provider's new `ModelsURL` when set.
- **OpenAI shape:** the same `data[]`, with `created` in epoch seconds. One
  parser accepts both shapes.
- **Codex cache:** reads `~/.codex/models_cache.json`. It keeps
  `visibility == "list"` rows and takes the window from `context_window`. No
  process is started.

New catalog field: `Provider.ModelsURL`, set only for DeepSeek
(`https://api.deepseek.com/models`).

### 3. Cache

- The file `allin-models.json` sits in the configs root (beside
  `claude-configs.list`). Its path is derived from `Env.ConfigsList`, so no new
  flag is plumbed.
- It is keyed by source: `anthropic` (one entry for every login), and
  `cfg.<file>` for each provider profile.
- Each entry holds `fetched_at` and `models: []Listed`, already reduced by
  `Latest`.
- It is written atomically (temp file + rename). Several routers may refresh
  at once.
- A missing, unreadable or unparseable cache counts as no entry.

### 4. Roster reads the cache only

- `accountRows` uses the `anthropic` entry. If there is no entry, it uses
  `claudeLineup`. `claudeLineup` stays as the fallback and is no longer the
  source of truth.
  - The label is the login label + " · " + `display_name` with the leading
    `Claude ` removed.
  - The id stays `wisp/acct.<dir>/<id>[1m]`.
- `configRows` uses the `cfg.<file>` entry when present, otherwise
  `providerModels` as today. `SuppliesOwnModel` providers (custom,
  Featherless) are unchanged: the user picks their model.
- Window for the 200k floor: the listing's window if present, otherwise the
  catalog `Model.Context` for that id, otherwise 0. 0 keeps today's meaning,
  "unknown, admit".
- `Roster` performs no network I/O. This is guarded by a test.

### 5. Refresh in the router

- `NewHandler` gets an optional refresher hook.
- On each request, before the credential swap, the handler sees the session's
  own `Authorization` / `X-Api-Key`. When the session upstream is
  `api.anthropic.com`, it hands a copy to the refresher. This way the Claude
  list uses the token Claude Code already sends, with no Keychain read and no
  token refresh or rotation for an idle login.
- The refresher runs at most once per router process, in a goroutine that never
  blocks the request. For each stale entry (older than 12h, or missing), it:
  - fetches Anthropic with the captured header,
  - fetches each enabled, routable API-key provider with its profile key,
  - reads the Codex cache.
- A failed fetch keeps that source's old entry, so a network blip never empties
  the picker.
- If any entry changed, it calls `EnsureProfileIfEligible(env)`, so the next
  All-In session's picker holds the new rows.
- Unverified: whether a session that is already running re-reads its picker. Only
  the next launch is promised.

### 6. Out of scope

- Non-All-In profiles keep their `ANTHROPIC_DEFAULT_*` mappings.
- Prices: a newly discovered id has no catalog price, so Stats shows no cost
  for it.
- Featherless and custom stay user-picked.

## Testing (TDD)

- `Latest`: table test over the real 2026-09-22 id lists above, plus the date
  tiebreak and the equal-date Kimi list.
- Fetchers: `httptest` servers for the Anthropic shape (with pagination), the
  OpenAI shape, a 404, a 401 and malformed JSON. Plus a Codex cache fixture.
- Cache: round-trip, stale vs. fresh, a corrupt file, and an atomic write.
- Roster:
  - cached Claude entry → an Opus 5.5 row per login;
  - no cache → `claudeLineup`;
  - listing window vs. catalog window vs. unknown, against the floor.
- Router:
  - the refresher gets the session's header, never a swapped one;
  - it runs once per process;
  - a request never waits on it.
- Run only the new tests, scoped to their files. Lint only the changed files.
