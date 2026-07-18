# ChatGPT Inline Login — Design

**Date:** 2026-07-19
**Status:** Approved for implementation

## Problem

The OpenAI GPT subscription bridge checks `account/read` when it starts. If
Codex is signed out, it exits and tells the user to run `codex login`. Wisp Deck
never starts the login flow itself, so selecting OpenAI GPT is not sufficient
to make the subscription usable.

Codex app-server already exposes the managed ChatGPT browser flow through
`account/login/start`, `account/login/completed`, and `account/login/cancel`.
The bridge should use that supported protocol and continue the pending Claude
launch after authentication succeeds.

## Design

When the OpenAI GPT adapter starts:

1. Start and initialize Codex app-server as today.
2. Read the current account.
3. If it is already a ChatGPT account, continue without showing login UI.
4. If it is signed out, call `account/login/start` with the managed `chatgpt`
   browser flow and hosted success page.
5. Print the returned URL to the Claude pane and attempt to open it with the
   macOS `open` command. Failure to open the browser is non-fatal because the
   printed URL remains usable.
6. Wait for the matching `account/login/completed` notification. Ignore
   notifications for unrelated login IDs.
7. Re-read the account and model catalog, validate ChatGPT subscription auth,
   and continue launching Claude through the bridge.

An API-key account remains a hard error. Wisp Deck must not log it out or
silently replace it because that would mutate an explicit user choice and could
change billing behavior.

## Cancellation and Errors

Interactive login has a separate bounded timeout and is not charged against
the short app-server startup timeout. If the pane closes, the user interrupts
the launch, or the login times out, the adapter calls
`account/login/cancel` with the matching login ID before shutting down.

Failed login notifications surface the app-server error. Malformed responses,
missing login IDs or URLs, a post-login non-ChatGPT account, and an empty
post-login model catalog all fail explicitly. Browser-open failures are shown
as warnings with the copyable URL and do not abort the pending login.

## Boundaries

- Codex continues to own OAuth credentials, persistence, and token refresh.
- Wisp Deck never reads or stores Codex tokens.
- The existing loopback bridge authentication and API-key rejection remain
  unchanged.
- Standard Claude, Zhipu GLM, Xiaomi MiMo, and direct Codex launches are
  unchanged.
- Login is initiated only when a new OpenAI GPT-backed Claude launch actually
  needs it; rendering the Subscription modal remains side-effect free.

## Testing

Use the existing fake JSON-RPC transport and fake Codex subprocess to prove:

- signed-out startup requests the managed ChatGPT browser flow;
- the exact authentication URL is opened and printed;
- browser-open failure preserves the manual URL path;
- only the matching completion notification unblocks launch;
- successful login re-reads the account and model catalog before Claude starts;
- failure and cancellation never launch Claude and cancel the pending login;
- existing ChatGPT auth skips login;
- API-key auth remains rejected.

Update the gated live test preflight so signed-out state is no longer rejected
before the adapter can exercise the real login behavior. Ordinary automated
tests must never touch the developer's real Codex credentials or open a real
browser.
