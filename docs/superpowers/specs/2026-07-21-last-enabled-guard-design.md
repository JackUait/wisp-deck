# Last-enabled guard — design

**Date:** 2026-07-21
**Status:** Approved

## Problem

Nothing stops the user from disabling every AI tool or every managed
subscription. A session then has no launchable agent (or the in-session
switcher has no managed subscription to offer), a state the UI never
intends.

## Rule

At least one AI tool and at least one managed subscription must always stay
enabled.

- **AI tools panel:** `x` refuses to disable a tool when it is the last
  installed-and-enabled one. The panel's existing error line shows
  "At least one AI tool must stay enabled". Enabling is never blocked.
- **Subscription modal:** `x` refuses to disable a managed subscription when
  it is the last enabled managed profile. Standard Claude is excluded from
  the count — it can never be disabled, but the user chose to guard the
  managed set independently. The modal's error line shows
  "At least one subscription must stay enabled". Enabling is never blocked.

## Placement

The guards live in the TUI handlers (`toggleFocusedToolDisabled`,
`toggleSubscriptionProfileDisabled`), not the persistence helpers: only the
handlers know the candidate sets (installed tools / managed profiles), and
no other code path writes the disabled files.

## Out of scope

- Repairing an already-all-disabled state left by older builds (the panel
  still lets the user enable anything).
- Bash-side enforcement.

## Testing

TDD, in the existing `internal/tui/ai_tools_disable_test.go` and
`internal/tui/subscription_modal_disable_test.go`:

- Disabling the last enabled tool/managed profile is refused: state and
  sidecar file unchanged, error message shown.
- Disabling with another enabled peer present still works.
- Re-enabling a disabled entry is never blocked, even when everything else
  is disabled.
