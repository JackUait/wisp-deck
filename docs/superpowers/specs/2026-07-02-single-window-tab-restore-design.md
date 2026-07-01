# Single-Window Tab Restore

**Date:** 2026-07-02
**Status:** Approved (autonomous session; supersedes the window-per-session restore flow from 2026-06-09-session-restore-design.md)

## Problem

After a Mac reboot, Wisp Deck restores each saved session with
`open -na Ghostty --args -e …` — one **separate window** per project. The user
had those projects as **tabs of a single window**, and the restore order is
whatever `tmux list-sessions` prints (alphabetical), not the order the tabs
had before the reboot.

## Constraints discovered

- Ghostty (macOS) has **no CLI/IPC** to open a tab in an existing window.
  The feature request was closed as not planned
  (ghostty-org/ghostty#12136, April 2026); the scripting API (#2353) is
  unshipped.
- `open -na Ghostty` spawns a **separate app process** per call, so macOS
  window tabbing (`AppleWindowTabbingMode`) cannot group them.
- The only working mechanism is simulating **Cmd+T** via
  `osascript`/System Events (requires the Accessibility permission for
  Ghostty; fails cleanly with a non-zero exit when not granted).
- The user's Ghostty config binds `cmd+t=new_tab` and sets
  `command = direct:/bin/bash -l …/wrapper.sh`, so every new tab runs the
  wrapper interactively.
- Ghostty tab order is not queryable; tmux `session_created` (launch order)
  is the best available proxy for pre-reboot tab order.

## Design

Queue-driven tab chain, all inside the window the user opens after reboot:

1. **Snapshot ordering** — `write_session_snapshot` lists sessions sorted by
   `#{session_created}` (ascending), so snapshot line order = launch order.
2. **Queue instead of windows** — on the first interactive launch of a new
   boot, `maybe_restore_session` no longer spawns windows. It writes
   `$config_dir/restore-queue` with one `boot_id|path|tool` line per
   prior-boot session (in snapshot order) and stamps `last-restore-boot`.
3. **Pop + take over** — the interactive wrapper calls
   `restore_queue_pop <config_dir> <cur_boot>`. If it returns an entry, the
   wrapper skips the picker and becomes that project's session (same
   semantics as `--restore`, including `WISP_DECK_RESUME=1`). Entries whose
   path no longer exists are skipped.
4. **Chain** — after popping, `restore_advance <config_dir> <wrapper>`
   checks the queue. If non-empty it triggers Cmd+T via `osascript`
   (activate Ghostty + System Events keystroke). The new tab runs the
   wrapper, pops the next entry, and repeats — tabs open left-to-right in
   snapshot order.
5. **Fallback** — if `osascript` fails (Accessibility permission not
   granted), `restore_advance` drains the queue through the legacy
   `launch_restore_window` (separate windows, old behavior) and deletes the
   queue. Restore never silently loses sessions.

### Queue hygiene

- Each line carries the boot id; `restore_queue_pop` discards the whole
  queue when the boot id doesn't match the current boot.
- A queue older than 5 minutes (mtime) is discarded — a broken chain must
  not hijack a tab the user opens manually later.
- Pop is guarded by a `mkdir` lock so two tabs racing on the queue can't
  double-consume a line.

### Functions (lib/session-restore.sh)

| Function | Change |
|---|---|
| `write_session_snapshot` | sort sessions by `session_created` |
| `maybe_restore_session` | writes `restore-queue` + marker; spawns nothing |
| `restore_queue_pop <dir> <boot>` | new — atomic pop, echoes `path\|tool` |
| `restore_advance <dir> <wrapper>` | new — Cmd+T trigger or windows fallback |
| `restore_trigger_tab` | new — osascript wrapper (mockable) |
| `launch_restore_window` | unchanged (fallback path) |

### wrapper.sh

The `[ -z "$1" ]` interactive branch calls `maybe_restore_session`, then
loops `restore_queue_pop` (skipping dead paths). On a hit it sets
`RESTORE_MODE=1`, `RESTORE_PATH`, `RESTORE_TOOL`, calls `restore_advance`,
and proceeds exactly like `--restore` mode. On a miss it falls through to
the project picker as before. The first restored project takes over the
window the user opened, so no stray picker tab is left behind.

## Testing

Bash integration tests in `test/bash/session_restore_test.go` (TDD):
snapshot ordering, queue writing/gating, atomic pop (boot mismatch, stale
mtime, missing file), advance trigger, and windows fallback on osascript
failure. Wrapper-level pop/skip-picker covered in
`test/bash/wrapper_restore_test.go` following the existing mocked-binaries
pattern.

## Known trade-offs

- First restore after this ships prompts the user once for the
  Accessibility permission (System Events keystrokes from Ghostty). Until
  granted, restore degrades to the previous separate-windows behavior.
- Launch order is used as tab order; manual tab re-ordering before a reboot
  is not captured (Ghostty exposes no tab-order API).
