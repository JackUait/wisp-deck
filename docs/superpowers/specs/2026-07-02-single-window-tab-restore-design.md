# Single-Window Tab Restore

**Date:** 2026-07-02
**Status:** Approved (autonomous session; supersedes the window-per-session restore flow from 2026-06-09-session-restore-design.md)

## Problem

After a Mac reboot, Wisp Deck restores each saved session with
`open -na Ghostty --args -e …` — one **separate window** per project. The user
had those projects as **tabs of a single window**, and the restore order is
whatever `tmux list-sessions` prints (alphabetical), not the order the tabs
had before the reboot.

## Root cause of duplicated tabs

Observed live: a Ghostty process running with launch args
`-e /bin/bash -l …/wrapper.sh --restore /Users/jackuait/Packages/blok claude`.
`open -na Ghostty --args -e …` makes that `-e` command the **default command
of the entire Ghostty instance**, not just its first window. Every new tab
(Cmd+T) opened inside such a restored window re-runs
`wrapper.sh --restore <same path>` and opens the same project again (two
`dev-blok-*` tmux sessions existed, the second created a minute after the
restore). Those duplicates then land in the snapshot, so the next reboot
restores the project twice — the repetition compounds across reboots.

Fix: no Ghostty instance may ever carry `--restore` in its launch args. The
`--restore` flag is removed entirely; restore state lives in a queue file
that each entry can be popped from exactly once. The once-per-boot gate also
becomes atomic (claim file) so simultaneous wrapper starts at login cannot
each rebuild the queue.

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
   granted), `restore_advance` spawns one **plain** Ghostty window per
   remaining queue entry (`open -na Ghostty`, no args). Each window runs the
   configured wrapper command, pops one queue entry, and restores it.
   Separate windows (old behavior), but no `--restore` args are ever baked
   into an instance, so no duplication.

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
| `maybe_restore_session <dir> <boot>` | writes `restore-queue` + marker under an atomic claim; spawns nothing |
| `restore_queue_pop <dir> <boot>` | new — atomic pop, echoes `path\|tool` |
| `restore_advance <dir>` | new — Cmd+T trigger, or plain-window-per-entry fallback |
| `restore_trigger_tab` | new — osascript wrapper (mockable) |
| `launch_restore_window` / `parse_restore_flag` | **deleted** (duplication vector) |
| `terminal_launch_restore` (ghostty.sh) | replaced by `terminal_launch_window` — `open -na Ghostty`, no args |

### wrapper.sh

The `--restore` flag and `RESTORE_MODE` argument parsing are removed. The
interactive branch (no directory argument) calls `maybe_restore_session`,
then loops `restore_queue_pop` (skipping dead paths). On a hit it calls
`restore_advance`, stops the loading splash, cds into the project, sets the
tool, and marks the session as a resume (`WISP_DECK_RESUME=1`, previously
the `--restore` semantics). On a miss it falls through to the project
picker as before. Stale windows still launching with `--restore` args (from
pre-fix Ghostty instances) no longer match a directory argument and fall
into the interactive branch — pop or picker, never a forced duplicate.

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
