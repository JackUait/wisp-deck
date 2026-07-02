# Exact pane-position restore

## Problem

Wisp Deck snapshots alive tmux sessions (`lib/session-restore.sh`) so a reboot
reopens them as ordered tabs. Each snapshot line records
`boot_id|project|path|tool|terminal|claude_session_id` — but **nothing about
pane geometry**. On restore, `wrapper.sh` always rebuilds the three panes with
hardcoded split percentages (`_pane0_pct` = 75 for compact / 50 for lazygit,
spare pane = 45%). Any manual resize the user made before closing is lost: the
window comes back at the defaults, not "the exact same positions."

## Goal

When a Wisp Deck window is closed and later restored, its panes reopen at the
exact positions they held at close time.

## Approach

Capture tmux's own `#{window_layout}` string and replay it with
`select-layout`. tmux exposes the exact geometry of every pane in one opaque,
self-describing string, e.g.:

```
bdba,204x50,0,0{152x50,0,0,1,51x50,153,0[51x25,153,0,2,51x24,153,26,3]}
```

`select-layout <string>` reproduces that geometry exactly. When the restored
terminal window is a different size than at capture, tmux fits the layout to the
current client, preserving relative proportions (pixel-exact when the size
matches — the common case of restoring on the same monitor). The string
contains no `|`, so it is safe as a new trailing snapshot field.

## Changes

Four touch points, all in the existing restore data flow.

### 1. Capture — `write_session_snapshot` (lib/session-restore.sh)

For each ghost session, read the window-0 layout and append it as a 7th field:

```
boot|proj|path|tool|term|sid|layout
```

Layout via `tmux display-message -p -t "$s:0" '#{window_layout}'` (empty string
tolerated — old tmux, race, or unreachable server).

### 2. Queue build — `maybe_restore_session` (lib/session-restore.sh)

Read the 7th field from the snapshot and carry it into each queue entry:

```
cur_boot|path|tool|sid|layout
```

The existing unstamped-duplicate dedup loop rewrites `entries[i]` to
`path|tool|sid`, dropping any extra field. To keep that logic untouched, hold
the per-entry layouts in a **parallel array** (`layouts[i]`) indexed alongside
`entries`, and emit `${cur_boot}|${path}|${tool}|${sid}|${layouts[i]}`.

### 3. Pop — `restore_queue_pop` (lib/session-restore.sh)

No change: it already echoes `${line#*|}`, forwarding `path|tool|sid|layout`.

### 4. Replay — `wrapper.sh`

- Parse the popped entry with a 4th field: `_q_path _q_tool _q_sid _q_layout`.
- After the `new-session … select-pane -R` block builds the three panes, if
  `RESTORE_MODE=1` and `_q_layout` is non-empty, run:

  ```bash
  "$TMUX_CMD" select-layout -t "$SESSION_NAME:0" "$_q_layout" 2>/dev/null || true
  ```

Pane build order is deterministic and identical between capture and restore, so
pane indices line up and the captured geometry maps cleanly onto the rebuilt
panes.

## Backward compatibility

Old snapshots (and old queues) lack the 7th/4th field → `_q_layout` is empty →
`select-layout` is skipped and today's hardcoded default percentages apply.
Nothing breaks; the feature simply does not engage for pre-upgrade snapshots.

## Testing (TDD)

Bash integration tests (`test/bash/`):

- `write_session_snapshot` appends the captured `window_layout` as field 7.
- `write_session_snapshot` writes an empty 7th field when tmux returns no
  layout.
- `maybe_restore_session` carries the layout into the queue entry, surviving the
  unstamped-duplicate dedup pass.
- `wrapper.sh` restore path exports `_q_layout` and issues `select-layout` only
  in restore mode with a non-empty layout; no `select-layout` when the field is
  empty.

Existing exact-match snapshot/queue assertions gain the trailing field, and
their tmux mocks gain a `display-message` case returning a layout string.

Non-interactive verification: `shellcheck` on modified scripts, then
`./run-tests.sh`.
