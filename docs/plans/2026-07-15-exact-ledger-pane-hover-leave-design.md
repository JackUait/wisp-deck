# Exact ledger pane hover leave

## Problem

The native ledger receives mouse motion only while tmux routes the pointer to
the ledger pane. When the pointer enters a neighboring pane, tmux sends that
motion to the new pane and does not emit a leave event to the ledger. The stale
row therefore remains highlighted.

An idle timeout hides the stale row, but it also removes a legitimate hover
while the pointer is stationary. Edge heuristics are also incorrect because
tmux may route a direct jump without first reporting the ledger's edge cell.

## Constraints

- A stationary pointer inside the ledger must keep its hover indefinitely.
- Entering any other pane must clear the ledger hover.
- The rightmost ledger cell remains a valid hit target.
- Keyboard input, configured root bindings, clicks, wheel events, and mouse
  input in neighboring applications must keep their normal behavior.
- Routing changes must be scoped to one Wisp session and cleaned up with it.
- Mouse-motion handling must remain constant-time and must not launch a process
  per event.

## Design

Wisp will install a session-specific tmux key table after the ledger pane has
been created and before the session becomes interactive.

1. Clone the session's current default key table into a uniquely named table.
   This preserves prefix and user root bindings.
2. Add an `Any` fallback binding to the clone. tmux 3.6a does not allow
   `MouseMovePane` to be named in `bind-key`, but its dispatch path checks an
   `Any` binding before forwarding unbindable mouse movement.
3. For a mouse event targeting a pane other than the ledger, inject one
   synthetic out-of-bounds SGR motion report into the ledger pane. The existing
   ledger bounds check converts that report into a hover clear.
4. Forward the original mouse event with `send-keys -M`, preserving the target
   pane and full mouse metadata. Forward ordinary unbound keys with argumentless
   `send-keys`, which re-injects the binding's current key event.
5. Set the Wisp session's `key-table` option to the clone. Other tmux sessions
   continue using their existing tables.
6. Remove the cloned table during normal session cleanup. Installation is
   best-effort: if setup fails, the session remains usable with its original
   key table.

The native ledger idle timeout and its timer state will be removed. Hover will
again change only in response to real or synthetic mouse events.

## Components

- `lib/ledger-hover.sh`: install and remove the scoped key table.
- `wrapper.sh`: mark the ledger pane, install routing during the tmux command
  chain, and clean it up with the session.
- `internal/tui/ledger.go`: remove timeout configuration and timer messages;
  retain the exact `[0, width)` horizontal bounds check.
- Tests: direct model and PTY coverage plus a real isolated two-pane tmux test.

## Error handling

Key-table setup must never prevent a Wisp session from opening. The installer
will build the clone before switching the session to it; any failure removes a
partial table and leaves the original session key table active. Cleanup is
idempotent.

## Verification

- Hover remains visible after more than the former timeout while no input is
  received.
- A real tmux client moves from the ledger into a neighboring pane and the
  ledger receives the synthetic leave report.
- The neighboring pane receives the original motion event.
- Ordinary keys and a copied root binding retain their behavior.
- The last ledger column remains interactive.
- Existing native-ledger scale, scroll, click, selection, and popup tests pass.
