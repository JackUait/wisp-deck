# Durable Codex Crash Restore Design

**Date:** 2026-07-18
**Status:** Approved

## Problem

Wisp Deck's live session snapshot records one conversation field, but always
fills it from `WISP_DECK_CLAUDE_SESSION`. Native Codex panes do not publish
their thread UUID there, so their snapshot entries have an empty conversation
field. After a macOS crash, restore passes that empty value through the queue
and intentionally launches plain Codex. The old rollout remains on disk while
the restored tab opens a new, empty thread.

The same schema can misidentify a pane that switched from Claude to Codex:
the snapshot may attach the stale Claude UUID to a Codex entry. Exact Codex
resume then fails and the current fallback silently launches a new thread.

## Goals

- Persist the exact Codex thread UUID as soon as the adapter correlates it.
- Survive loss of tmux and a crash between heartbeat ticks.
- Snapshot the identity belonging to the active tool, never another tool's ID.
- Resume every identified Codex conversation by exact UUID.
- Never silently replace a missing or failed Codex resume with an empty chat.
- Give legacy snapshots without a Codex UUID a safe, user-directed recovery
  path.

## Architecture

### Durable per-session identity

Each Wisp session gets a persistent identity file under:

```text
~/.config/wisp-deck/session-identities/<session-name>.codex
```

`wrapper.sh` creates the path before building the AI command, stamps it into
the tmux session environment as `WISP_DECK_CODEX_SESSION_FILE`, and passes it
to `wisp-deck-tui codex-adapter`.

The adapter correlates the exact initial root and separately tracks every new
top-level thread created by its private Codex TUI. It writes the current root
to the identity file atomically and durably. A resumed UUID is published
before the TUI starts. A fresh UUID and later `/new` transitions are published
on their observer events; a coherent reconnect snapshot recovers one
transition missed during an outage. Observer loss before the first identity
has a bounded recovery window, after which the adapter cancels the TUI and
reports the failure. Once a thread exists, failure to persist its identity is
fatal and visible; Wisp Deck must not allow an unrestoreable chat to continue
silently.

The identity file is deliberately not deleted from the wrapper's shutdown
trap. A graceful tab close and a machine crash deliver indistinguishable
signals. Stale identity files are harmless because restore reads only safe
keys rooted in `session-identities`. Opportunistic pruning removes files older
than 30 days only when no live tmux session, snapshot, snapshot backup, or
restore queue references them.

### Tool-aware snapshot

The snapshot gains a ninth field containing the session identity key:

```text
boot|project|path|tool|terminal|conversation|layout|account|identity_key
```

The `conversation` value is selected by active tool:

- Claude: `WISP_DECK_CLAUDE_SESSION`
- Codex: valid durable identity file contents, otherwise a valid
  `WISP_DECK_CODEX_SESSION` compatibility stamp
- OpenCode: empty, because its current contract is project-scoped continuation

For Codex, a Claude UUID is never consulted. This prevents both empty native
Codex restores and stale cross-tool identity reuse.

The queue carries the identity key with the existing fields. On restore,
Codex resolution prefers the current durable sidecar, then the valid embedded
UUID. UUIDs are validated before launch.

### Fail-closed recovery

Normal, non-restored Codex launches remain fresh.

A restored Codex entry follows this matrix:

1. Valid exact UUID: run `codex resume <uuid>`.
2. Missing or invalid UUID, including a legacy snapshot: run `codex resume`
   and show Codex's conversation selector.
3. Exact resume exits quickly with failure: fall back to the conversation
   selector, never plain Codex.

The selector may require a user choice, but it cannot silently impersonate a
successful restore with an empty conversation.

## Data Flow

```text
wrapper creates persistent identity path
  -> codex-adapter starts private app-server observer
  -> reducer correlates exact root UUID
  -> supervisor durably writes UUID sidecar
  -> heartbeat snapshots active-tool UUID + sidecar key
  -> macOS crash kills tmux; snapshot and sidecar survive
  -> queue carries exact UUID/key
  -> restored adapter runs codex resume <exact UUID>
```

If the frozen snapshot predates this format or the identity is unavailable,
the final step becomes the Codex resume selector rather than a fresh launch.

## Error Handling and Invariants

- Identity files accept only canonical lowercase Codex UUIDs.
- Writes use a private temporary file, file sync, atomic rename, and directory
  sync where supported.
- Snapshot and queue parsing remain backward compatible with shorter lines.
- An invalid sidecar is treated as unavailable and cannot become a command
  argument.
- Two tabs in the same project remain distinct because identity is keyed by
  Wisp session, not cwd or recency.
- Restore never falls back from exact Codex resume to plain Codex.

## Testing

- Supervisor tests prove resumed and freshly correlated roots are persisted,
  and persistence failure aborts rather than continuing.
- Command tests prove exact restore uses `--resume-session`, while missing or
  failed exact restore uses the selector and never a fresh launch.
- Snapshot tests prove Codex reads only Codex identity, ignores stale Claude
  identity, accepts the durable sidecar, and rejects malformed UUIDs.
- Queue tests prove the identity key survives snapshot-to-pop roundtrips and
  old eight-field snapshots remain readable.
- Wrapper tests prove the persistent path is passed to the adapter and stamped
  into tmux.
- A crash-roundtrip regression covers two same-project Codex tabs with
  different UUIDs and verifies both exact identities survive independently.
- Full Go, Bash integration, shellcheck, installation, SHA-256, and code-sign
  checks remain required.
