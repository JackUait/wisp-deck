# Clean Session First Frame Design

**Date:** 2026-07-15
**Status:** Approved

## Problem

Opening a project briefly shows the changes ledger across the full terminal.
The wrapper currently creates an attached tmux session with the ledger as its
first pane, then adds the AI and spare panes. tmux paints that intermediate
one-pane state before the command queue finishes building the final layout.

## Goal

Make the first visible tmux frame the complete three-pane workspace, with the
AI pane focused. Preserve the existing pane order, dimensions, restore
behavior, key bindings, hover routing, and automatic session teardown.

## Design

Create the tmux session detached and seed it with the current terminal width
and height. Run the existing option, binding, pane-split, and focus commands in
the same order while the session remains off-screen. Attach as the final tmux
command, after the AI pane has been selected.

The `exit-unattached` option cannot be enabled during detached construction:
tmux immediately destroys a session that has no client while that option is
active. The command queue will therefore attach first and enable
`exit-unattached` immediately afterward. tmux processes the queue in order, so
the session has a client by the time automatic teardown is enabled.

Detached sessions otherwise start at tmux's default size, commonly 80x24.
Building the pane percentages at that size and resizing on attachment would
distort the intended proportions. The wrapper will read the current terminal
geometry, use conservative 80x24 fallbacks when it is unavailable, and pass
the dimensions to `new-session` with `-x` and `-y`.

Restore remains unchanged. The restore watcher still starts before the tmux
command and replays the captured layout after the panes exist and after any
late Ghostty resize. Pane construction order stays identical, so saved tmux
layout cell identities continue to map correctly.

## Error Handling

Invalid or unavailable terminal geometry falls back to 80 columns by 24 rows.
All existing tmux command failure behavior remains unchanged. No loading pane,
temporary process, or readiness file is introduced.

## Testing

Wrapper tests will assert that:

- `new-session` is detached and receives explicit width and height;
- all splits and the final AI-pane selection precede `attach-session`;
- `exit-unattached` follows attachment rather than detached creation;
- the existing pane order, split percentages, bindings, and focus assertions
  remain valid.

A focused real-tmux integration test will verify that detached construction
produces all three panes before attachment and that the session still exits
after its client detaches.

## Non-Goals

- Redesigning the ledger or splash screen.
- Changing default pane proportions.
- Changing restored layouts.
- Adding a new user setting.
