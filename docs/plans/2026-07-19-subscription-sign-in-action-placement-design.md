# Subscription Sign-In Action Placement Design

## Problem

The OpenAI / ChatGPT profile renders `[ Sign in / switch account ]` directly
under the CONNECTION metadata. The control is visually mixed with
Authentication and Endpoint values even though it is an action.

Its keyboard order already follows the four model-routing rows:

```text
Opus → Sonnet → Haiku → Fable → Sign in → Rename → Delete → Save
```

Because the sign-in button is drawn above MODEL ROUTING, moving down from
Fable jumps visually upward. The rendering hierarchy, focus order, and action
semantics disagree.

## Requirements

- Render the ChatGPT sign-in control as the first item under ACTIONS.
- Give sign-in a dedicated row above Rename, Delete, and Save changes.
- Preserve keyboard order:
  Fable → Sign in → Rename → Delete → Save.
- Keep Authentication status and Endpoint in CONNECTION.
- Keep browser-waiting state, manual URL, open-browser errors, and login errors
  adjacent to the sign-in action.
- Preserve mouse hover and click behavior.
- Preserve existing responsive wrapping for Rename, Delete, and Save.
- Do not change native Claude or API-key provider layouts.

## Considered Approaches

### 1. Dedicated primary row under ACTIONS

Place sign-in on its own row below the ACTIONS heading. Render profile
management actions on the following row or rows using the existing wrapping
logic.

This establishes a clear primary/secondary action hierarchy and matches the
existing keyboard order.

### 2. Put every action on one row

Place sign-in, Rename, Delete, and Save together.

This aligns the control with ACTIONS but creates a long, crowded row and wraps
poorly in compact layouts.

### 3. Put sign-in beside Authentication

Render the button at the right edge of the Authentication row.

This keeps account context close but still mixes data and actions, reduces the
status value's available width, and does not resolve the visual/focus-order
mismatch.

## Decision

Use approach 1.

CONNECTION remains read-only status:

```text
CONNECTION ─────────────────────────
  Authentication  Signed in
  Endpoint        Local Codex bridge
```

The action area becomes:

```text
ACTIONS ────────────────────────────
[ Sign in / switch account ]
[ Rename ]  [ Delete ]  [ Save changes ]
```

While authentication is pending, the first row changes to
`[ Waiting for browser… ]`. Any manual URL or authentication error follows
that row before the profile-management actions.

## Interaction and Data Flow

The existing `subscriptionDetailAuth` cursor and `subscriptionHitAuth`
target remain the semantic identifiers for sign-in. Only rendering and cursor
line geometry change.

Keyboard flow remains:

1. Down from Fable focuses sign-in.
2. Down from sign-in focuses Rename.
3. Left and Right navigate Rename, Delete, and Save.
4. Enter on sign-in starts or switches ChatGPT authentication.

Mouse targeting continues matching the visible sign-in or waiting label, so
moving the rendered line does not change activation behavior.

## Responsive Layout

Sign-in always occupies one dedicated row. Rename, Delete, and Save retain the
current layout rules:

- one row when all three fit;
- Rename and Delete together with Save below when only the pair fits;
- one button per row at narrow widths.

The detail cursor-line calculation will include the sign-in row and any
authentication feedback rows so scrolling keeps the focused control visible.

## Error Handling

No authentication behavior changes. Pending state, manual browser URL,
browser-open errors, and authentication errors remain sourced from the
existing auth state.

Providers without ChatGPT authentication do not render the sign-in row. API
key editing continues to use its current model-detail row.

## Testing

- Add a render-order test requiring:
  CONNECTION < MODEL ROUTING < ACTIONS < Sign in < Rename.
- Assert CONNECTION contains status and endpoint but no sign-in control.
- Preserve the existing keyboard test for Fable → Sign in → Rename.
- Add cursor-line coverage proving sign-in is visible at its new action row.
- Keep mouse click coverage for the moved button.
- Verify pending URL and error feedback render between sign-in and profile
  management actions.
- Run focused subscription modal tests, the full TUI package, `make install`,
  installed-path/SHA-256/signature checks, and a local modal render check.
