# Context-sensitive enable/disable help — design

**Date:** 2026-07-20
**Status:** Approved

## Problem

Disabling an AI tool (settings → AI tools panel, `x`) or a subscription
(Subscription modal, `x`) is a toggle: pressing `x` on a disabled row
re-enables it. But nothing teaches this. Both help footers statically read
`x disable`, so after disabling an agent the user has no visible way back —
they hit a dead end the mechanism doesn't actually have.

## Goal

After disabling an AI tool or subscription, the UI must show the user how to
enable it again.

## Design

Context-sensitive help labels. No behavior changes — `x` keeps toggling
exactly as it does today.

### AI tools panel (`internal/tui/ai_tools_panel.go`)

The help footer in `renderAIToolsPanel` currently hardcodes `x disable`.
It will consult the focused row (`m.aiToolRows[m.aiToolsCursor]`): when that
tool is disabled, the segment renders `x enable`; otherwise `x disable`.
All other footer segments are unchanged.

### Subscription modal (`internal/tui/subscription_modal.go`)

The help line (`↑↓ profile · → details · x disable · Tab pane · Enter action
· Esc close`) gets the same treatment: `x enable` when the focused row is a
disabled subscription profile. On rows where `x` is a no-op today (standard
account row, login rows) the label stays `x disable`, unchanged.

## Out of scope

- Inline hints on the disabled rows themselves.
- Any change to the toggle mechanics, persistence files, or which surfaces
  hide disabled entries (the in-session switcher keeps hiding them).

## Testing

TDD, Go render tests in the existing files:

- `internal/tui/ai_tools_disable_test.go`: with the cursor on a disabled
  tool, the panel shows `x enable`; on an enabled tool, `x disable`.
- `internal/tui/subscription_modal_disable_test.go`: same pair for the
  modal's focused subscription profile, plus the no-op rows keep `x disable`.
