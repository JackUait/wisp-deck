# Proxy Soft-Quota Fallback

**Date:** 2026-07-03
**Goal:** auto-switch accounts when one is close to the 5h or weekly limit *without interrupting the current session*.

## Problem

The account-rotation proxy (see `2026-07-01-account-rotation-proxy-design.md`) treats an
account whose observed `anthropic-ratelimit-unified-{5h,7d}-utilization` is at/above the
switch threshold (default 0.98) as **unavailable**. That is correct for proactive
switching — but when *every* account in the pool crosses the threshold, `GetActiveAccount`
finds nothing available and the proxy answers `429 rate_limit_error "All N accounts
exhausted"`.

That interrupts the session prematurely: at 98% utilization each account still has real
headroom, and upstream would keep serving until an actual 429. Without the proxy the
session would have kept working; with it, it stops at the threshold. The threshold is an
early-switch heuristic, not a hard limit.

## Design

Add a **soft fallback** to account selection in `internal/proxy/manager.go`:

1. `GetActiveAccount` first behaves as today — keep the current account while it is below
   the threshold, otherwise pick the best available account (`pickBest`, soonest weekly
   reset first).
2. **New:** when no account is below the threshold, fall back to the least-utilized
   account that is only *soft*-blocked — i.e. near-quota by threshold but **not**:
   - `errored` (dead refresh token),
   - throttled (inside a real 429 retry-after window),
   - upstream-`rejected` (`anthropic-ratelimit-unified-status: rejected`).
3. "All accounts exhausted" is reported only when every account is hard-blocked.

`HasAvailable` keeps its strict (threshold-based) semantics: it decides whether a 429'd
request should *switch immediately* vs wait out retry-after, and a soft-blocked account is
not a good failover target — the wait path already covers it.

## Behavior matrix

| Pool state | Before | After |
| --- | --- | --- |
| Some account below threshold | serve it (switch if current is near-quota) | unchanged |
| All accounts ≥ threshold, none hard-blocked | 429 "exhausted" (session interrupted) | serve least-utilized account until upstream actually rejects |
| All accounts throttled / rejected / errored | 429 "exhausted" | unchanged |

## Testing

- Manager: all accounts near-quota → `GetActiveAccount` returns the least-utilized one.
- Manager: soft fallback skips throttled / rejected / errored accounts; when only those
  remain, selection still fails (exhausted).
- Server: request is forwarded (200 relayed) when every account is near-quota but upstream
  still accepts, instead of writing the exhausted error.
- Existing `TestGetActiveAccount_noneAvailable` updated to use a hard-blocked account
  (upstream-rejected) so it still exercises the exhausted path.
