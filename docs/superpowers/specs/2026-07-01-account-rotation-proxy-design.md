# Automatic Claude Account Switching — In-House Rotation Proxy

Date: 2026-07-01

## Goal

Add a Wisp Deck setting that **automatically switches between multiple Claude
accounts** to pool their quota, so a long session keeps working past a single
account's 5-hour / weekly limit — with the switch feeling seamless (same session,
work continues).

Inspired by [teamclaude](https://github.com/KarpelesLab/teamclaude), reproduced
in Wisp Deck's own stack (Go) rather than depending on the npm package.

## How teamclaude works (research)

teamclaude is a transparent HTTPS proxy between Claude Code and
`api.anthropic.com`. It:

- Stores a list of accounts, each an OAuth `accessToken`/`refreshToken`/`expiresAt`
  (imported from Claude Code's `~/.claude/.credentials.json`, nested under
  `claudeAiOauth`).
- Per request: validates the client's local `x-api-key`, strips it, and injects
  `Authorization: Bearer <active account token>`, then forwards upstream.
- Learns each account's utilization passively from `anthropic-ratelimit-unified-*`
  response headers (and optionally the `/api/oauth/usage` endpoint).
- Rotates to the least-utilized available account when the active one crosses a
  threshold (default 98%) of its 5h or weekly quota, or on a persistent `429`
  (honoring `retry-after`).
- Refreshes OAuth tokens nearing expiry via `platform.claude.com/v1/oauth/token`.

Client is pointed at the proxy with `ANTHROPIC_BASE_URL` + `ANTHROPIC_API_KEY`
(the local proxy key).

## Key architectural payoff

With the proxy approach, `claude` runs under a **single** `CLAUDE_CONFIG_DIR` and
never knows accounts are rotating — the swap happens entirely upstream in the
proxy. So conversation continuity is automatic: no pane restart, no sharing of
`projects/` history across account dirs. That is what makes it seamless.

## Architecture

New proxy implemented as a subcommand of the existing Go binary:
`wisp-deck-tui proxy` (source in `internal/proxy/`, wired via
`cmd/wisp-deck-tui/`). Using the existing binary avoids changing the
release/installer pipeline (no new asset to download).

Components (each independently testable):

1. **`internal/proxy/accounts.go`** — load the account pool. Reuses the existing
   `claude-accounts` layout: `<accounts_dir>/<dir>/.credentials.json` →
   `{accessToken, refreshToken, expiresAt}` per account. Pure functions over a
   directory; no network.

2. **`internal/proxy/manager.go`** — the `Manager`: holds accounts + observed
   quota, picks the active account, applies rotation policy. Pure/deterministic
   (time injected), mirrors teamclaude's `AccountManager` but trimmed:
   - `Active()` returns the current account.
   - `UpdateQuota(idx, headers)` parses `anthropic-ratelimit-unified-*` and stores
     utilization + reset windows.
   - `RotateIfNeeded()` / `MarkThrottled(idx, until)` switch to the least-utilized
     available account when the active crosses `threshold` or is throttled.
   - Clears expired windows by injected `now`.

3. **`internal/proxy/oauth.go`** — `RefreshToken(refreshToken)` against the token
   endpoint; `ParseCredentials`/`WriteCredentials` for a `.credentials.json`
   file (writes refreshed tokens back atomically, mode 0600).

4. **`internal/proxy/server.go`** — `net/http` server. Validates the client
   `x-api-key`, strips hop-by-hop + client auth headers, injects the active
   account's bearer token, forwards to `https://api.anthropic.com` (streaming
   the response body through), feeds response headers to `UpdateQuota`, and on
   `429` honors `retry-after` then re-dispatches on the next account. Refreshes a
   token before use when `expiresAt` is near.

5. **CLI glue** — `wisp-deck-tui proxy --accounts-dir DIR --list FILE
   --threshold F` prints the chosen `port` + generated proxy key as JSON on
   startup (so bash can read them), then serves until killed.

## Bash + setting integration

- **Setting storage:** a flag file `~/.config/wisp-deck/auto-switch-accounts`
  (`on`/`off`), read/written by a new `lib/auto-switch.sh` helper (pure
  functions, matching the existing module style).
- **Settings menu:** add an "Auto-switch Claude accounts" toggle row in the Go
  settings menu, persisting the flag. Disabled/no-op unless ≥2 accounts exist.
- **Launch wiring (`wrapper.sh` + `lib/tmux-session.sh`):** when the flag is
  `on` and ≥2 accounts are configured, before building the claude launch command:
  1. Start `wisp-deck-tui proxy ...` in the background, capture its `port`+`key`.
  2. Export `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>` and
     `ANTHROPIC_API_KEY=<key>` for the claude pane; do **not** set a per-account
     `CLAUDE_CONFIG_DIR` (claude uses one config dir; rotation is upstream).
  3. The existing process-tree cleanup already kills the child on window close.
- When off (or <2 accounts), behavior is exactly as today (manual account
  selection via `CLAUDE_CONFIG_DIR`).

## Rotation policy (v1, matching teamclaude's patterns)

- Utilization = max of `unified5h`, `unified7d` from response headers (0–1).
- An account is *available* when it is not errored, not throttled, and under the
  `threshold` (default 0.98; a `unified-status: rejected` header also counts as
  over).
- `GetActiveAccount(tried)` keeps the current account while it is available and
  not already tried this request; otherwise it selects the best available
  account. Selection order mirrors teamclaude's `_pickBestAvailable`: an account
  with **no known weekly reset sorts first** (so its quota gets discovered), then
  the account whose **weekly window resets soonest** (that quota is closest to
  refreshing, so spending it first preserves later-resetting accounts). Ties
  break by index.
- On `429`: parse `retry-after`, clamp to `[1, 300]s` (default 60). While retries
  remain (`retryCount < poolSize`), **wait and retry the same account** (a
  transient limit). Once retries are exhausted, mark the account throttled until
  its reset and re-dispatch, which picks another account. When every account is
  exhausted, return a structured `rate_limit_error` (429) with a `retry-after`.
- Header hygiene mirrors teamclaude: strip `accept-encoding` from the forwarded
  request and `content-encoding`/`content-length` from the relayed response
  (the transport may auto-decompress), and do not follow upstream redirects
  (`redirect: manual`).

## Error handling

- Missing/invalid `.credentials.json` for an account → skip it (log to stderr),
  don't crash; if fewer than 1 usable account remains, the proxy exits non-zero
  and bash falls back to the normal (no-proxy) launch.
- Token refresh failure that is a hard `401`/`400` → mark account errored and
  rotate; transient `5xx`/network → retry with backoff (like teamclaude).
- Proxy fails to start → bash logs a warning and launches claude normally.

## Testing

- **Go unit tests** (`internal/proxy/*_test.go`): account loading from a temp
  dir; header parsing → utilization; rotation policy (threshold, throttle,
  all-exhausted fallback) with injected `now`; credentials read/write; server
  request handling against an `httptest.Server` standing in for upstream
  (asserts token injection, `x-api-key` stripping, streaming, `429`
  re-dispatch, quota update).
- **Bash integration tests** (`test/bash/`): `lib/auto-switch.sh` get/set/toggle;
  wrapper wiring builds the proxy launch + env only when flag on and ≥2 accounts,
  and is a no-op otherwise.
- Run `shellcheck` on all modified scripts and the full `./run-tests.sh` suite.

## Out of scope for v1 (future extensions)

sx.org residential proxy, MITM/CA mode, the interactive TUI dashboard, request
logging, priority ordering CLI, per-account disable/enable, persisted-quota state
file, and the passive `/api/oauth/usage` probe. All present in teamclaude; none
needed for the core "keep working past one account's limit" behavior.
