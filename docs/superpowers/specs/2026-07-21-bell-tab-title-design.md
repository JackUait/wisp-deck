# Bell emoji in tab title on attention

Date: 2026-07-21

## Goal

When a session enters the `attention` phase (agent finished, asked a question,
waiting on permission, or errored — the same trigger as the notification
sound), the Ghostty tab title shows a `🔔 ` prefix to the left of the title.
The bell clears as soon as the phase leaves `attention` (agent working again,
or the user interacted and the phase returned to `ready`).

## Design

The attention watcher (`lib/tab-title-watcher.sh`) already computes a
`waiting`/`active` title state per tick and routes waiting-state titles to
`set_tab_title_waiting`, which is currently an alias of the plain
`set_tab_title` (the old waiting cue was Ghostty's native bell icon, since
disabled). Changes:

- `lib/tui.sh` — `set_tab_title_waiting` becomes a real variant: it delegates
  to `set_tab_title` with `🔔 ` prefixed to the project argument, so the
  emitted OSC 0 escape is `🔔 <project> · <tool>` / `🔔 <project>`. The
  `/dev/tty` probe stays in one place.
- `lib/tab-title-watcher.sh` — the model-mode per-tick re-emit (which mirrors
  the AI tool's own pane title into the tab) prefixes `🔔 ` when the tick's
  title state is `waiting`. Without this the bell would be clobbered within
  0.5s in model mode. `apply_tab_title`'s model branch stays a no-op.

Bell appears in all three title modes (full / project / model) and for all
attention reasons. No new state, settings, or processes.

## Testing

Extend the existing bash-integration tests:

- `test/bash/tui_test.go` — `set_tab_title_waiting` emits the `🔔 ` prefix
  (with and without a tool); plain `set_tab_title` stays bell-free.
- `test/bash/tab_title_watcher_test.go` — `apply_tab_title` renders the bell
  for waiting/full and not for active/full; a model-mode
  `attention_watcher_tick` prepends the bell to the pane title during
  `attention` and drops it when `working`.
