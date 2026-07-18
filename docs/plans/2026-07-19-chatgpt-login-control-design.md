# ChatGPT Login Control Design

## Problem

The OpenAI / ChatGPT subscription details currently describe authentication as
`codex login`, but provide no visible authentication control or account state.
The bridge starts Codex's managed browser login only when a new Claude launch
finds Codex signed out. This makes authentication undiscoverable, gives an
already-authenticated user no way to switch accounts from Wisp Deck, and makes
deleting and recreating a Wisp Deck profile appear related to Codex login even
though the two states are independent.

## User Experience

Every OpenAI / ChatGPT profile shows a connection state and a persistent
`Sign in / switch account` action in the subscription detail pane.

The connection state is one of:

- `Checking…` while Wisp Deck asks Codex for the current account;
- `Signed in` when Codex reports a ChatGPT account;
- `Signed out` when Codex reports no account; or
- `Unavailable` when Codex cannot be queried.

Activating an OpenAI / ChatGPT profile while signed out starts Codex's managed
ChatGPT browser login automatically. Selecting the visible action starts the
same flow even when an account is already present, allowing the user to sign in
again or switch accounts. Wisp Deck never asks for, reads, or stores a password
or authentication token.

Deleting a Wisp Deck profile continues to delete routing configuration only.
It does not log Codex out.

## Architecture

The TUI runs authentication work asynchronously through Bubble Tea commands so
rendering and keyboard input never block. A small authentication state is kept
in the subscription modal. Opening the modal on an OpenAI profile schedules an
account check. Moving onto an OpenAI profile schedules a check when the modal
does not already have current state for that profile.

The account check and login command use the existing Codex app-server client.
The login command calls the existing managed `account/login/start` transaction,
opens the returned HTTPS URL with the platform browser opener, waits for the
matching completion notification, and refreshes the account. Results return to
the modal as Bubble Tea messages.

The existing launch-time login gate remains as a recovery path for launches
that bypass the modal or whose authentication expires later.

## Interaction

The ChatGPT connection section contains:

```text
Authentication  Signed in
[ Sign in / switch account ]
```

When signed out, activating the profile starts login automatically. The action
remains available so users can retry a failed flow. While login is pending, its
label becomes `Waiting for browser…` and repeated activation is ignored.

The fallback authentication URL and actionable failures appear inside the
modal. A browser-open failure is non-fatal: the URL remains visible so the user
can open it manually.

## Error Handling

- Missing or incompatible Codex reports `Unavailable` with a concise error.
- Login cancellation returns to `Signed out`.
- A rejected or timed-out login leaves the action available for retry.
- Stale asynchronous results are ignored if the user has moved to another
  profile or closed and reopened the modal.
- Only HTTPS authentication URLs are presented or opened.

## Testing

Tests cover:

- rendering each account state and the persistent login action;
- starting an asynchronous account check when the ChatGPT profile is shown;
- starting login from the action while signed in or signed out;
- automatic login when a signed-out ChatGPT profile becomes active;
- preventing duplicate login commands;
- success, cancellation, browser-open failure, and app-server failure;
- ignoring stale results;
- keyboard and mouse activation; and
- preserving API-key and Standard Claude behavior.

Focused TUI, bridge, shell-launch, race, and live bridge tests verify the
integration. The repository test suite is run before installation.
