# Design: Restore open sessions after reboot

**Date:** 2026-06-09
**Status:** Approved, pending implementation

## Problem

Ghost Tab launches each "tab" as a terminal window → `wrapper.sh` → a tmux
session with three panes (lazygit / AI tool / spare shell). The tmux server
holds all session state in memory. A computer reboot kills the server and every
session; nothing is saved. Users lose their working set — the projects they had
open and the AI conversations in progress.

Goal: when the user opens Ghost Tab again after a reboot, silently recover all
the tabs that were open before the reboot, each resuming its AI conversation,
with the best possible UX.

## Decisions (from brainstorming)

- **Recovery trigger:** silent auto-restore, fired **once per boot** (first
  interactive launch after a reboot). Later launches in the same uptime are
  normal new tabs.
- **Track model:** **live snapshot derived from alive tmux sessions** (not a
  registry mutated on close). This is the only model robust against the
  reboot-vs-window-close ambiguity (both fire the same `cleanup` trap).
- **Spawn model:** non-interactive `wrapper.sh` restore mode + per-terminal
  "launch a window running this command" hook.
- **Custom lightweight** implementation — no vendoring tmux-resurrect /
  tmux-continuum (Ghost Tab's layout is a fixed 3-pane, so their generality is
  unneeded; continuum's boot LaunchAgent also can't drive Ghostty/WezTerm).
- **Opened window behavior:** the window the user just opened stays a normal
  picker; restore spawns N *additional* windows for the saved tabs.

## The core technical reality

A reboot and a user closing a window are indistinguishable to `wrapper.sh`'s
`cleanup()` trap — both arrive as SIGHUP/SIGTERM. So any registry that *removes
entries on close* gets wiped during a graceful reboot, and the feature silently
fails.

The fix: derive "what is open" from **alive tmux sessions**, snapshotted to a
file on a heartbeat tick. The tmux server dies instantly at reboot, so no
further ticks run and the snapshot file is frozen with the last live set.
`cleanup()` never touches the snapshot file. User-closed tabs disappear from the
snapshot within one heartbeat tick (their tmux session is gone on the next
re-derivation).

**Documented edge case:** closing your *last* remaining tab and then rebooting
will reopen that tab, because no surviving heartbeat exists to drop it from the
snapshot before the freeze. Accepted as minor.

## Architecture

New module: `lib/session-restore.sh`.

Modified:
- `wrapper.sh` — restore mode, heartbeat start, restore-on-launch gate, tmux
  session metadata stamping.
- `lib/terminals/*.sh` (ghostty, iterm2, kitty, wezterm) — new
  `terminal_launch_restore` hook.
- `lib/tmux-session.sh` — resume-aware AI launch command; metadata stamping
  helper.

Data files (under `${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/`):
- `last-session` — the live snapshot. One line per alive ghost-tab tmux session:
  `boot_id|project_name|project_path|ai_tool|terminal`.
- `last-restore-boot` — boot id of the most recent restore (gates once-per-boot).

## Components

### 1. Boot id
`current_boot_id()` reads the `sec=` value from `sysctl -n kern.boottime`.
Stable for the lifetime of one uptime; changes on every reboot.

### 2. Live snapshot
- **Stamp metadata** at session creation: `tmux set-environment` on the new
  session sets `GHOST_TAB=1`, `GHOST_TAB_PROJECT`, `GHOST_TAB_PATH`,
  `GHOST_TAB_TOOL`, `GHOST_TAB_TERMINAL`. Metadata lives in tmux, dies with it,
  no orphan sidecar files.
- **Heartbeat loop** (piggybacks the existing per-tab background watcher),
  every ~10s: re-derive the snapshot from alive tmux sessions whose environment
  contains `GHOST_TAB=1`; for each, read its metadata and emit a line; write
  the file atomically (temp + `mv`).
- A session ends → it is gone from the next re-derivation → dropped from the
  snapshot.
- `cleanup()` must NOT rewrite or delete `last-session` (reboot-safety: a
  re-derivation during a multi-tab shutdown would shrink the set as tabs die
  one by one).

### 3. Restore on launch
`maybe_restore_session()` — called only on interactive launch, before the
picker, and never in restore mode:
1. If `current_boot_id` != contents of `last-restore-boot`, AND `last-session`
   exists with lines whose `boot_id` differs from the current boot id (i.e.
   from a previous uptime):
   - Write the current boot id to `last-restore-boot` **first** (so concurrent
     or spawned launches don't re-trigger).
   - For each snapshot line, spawn a window via the terminal adapter's
     `terminal_launch_restore`.
2. Return; normal picker flow proceeds in the opened window.

### 4. Non-interactive restore mode
`wrapper.sh --restore <project_path> <ai_tool>`:
- cd into `<project_path>`, force `SELECTED_AI_TOOL=<ai_tool>`.
- Skip the picker and skip `maybe_restore_session` (prevents infinite spawn).
- Launch the session with the AI **resume** command.

### 5. Resume-aware AI launch
`build_ai_launch_cmd` gains a resume variant. Resume flags (cwd-scoped; each
tool persists transcripts to disk):
- claude → `claude -c` (`--continue`)
- codex → `codex resume --last`
- copilot → `copilot --continue`
- opencode → `opencode --continue` (`-c`)

Only used in restore mode. Normal new tabs launch fresh (no resume flag).

### 6. Per-terminal launch hook
New `terminal_launch_restore <wrapper_path> <project_path> <ai_tool>` per
adapter, opening a new window that runs `wrapper.sh --restore ...`:
- Ghostty: `open -na Ghostty --args -e "/bin/bash -l <wrapper> --restore ..."`
- iTerm2: `osascript` — create window with default profile + command
- kitty: `open -na kitty --args /bin/bash -l <wrapper> --restore ...`
- WezTerm: `open -na WezTerm --args start -- /bin/bash -l <wrapper> --restore ...`

Exact invocations are pinned by tests against mocked `open` / `osascript`.

## Data flow

```
open tab
  └─ stamp tmux session env (GHOST_TAB=1 + metadata)
  └─ heartbeat loop writes last-session every ~10s
        ...
reboot  → tmux dies → last-session frozen with last live set
        ...
next interactive launch
  └─ maybe_restore_session: boot id changed + prior-boot lines present
        └─ write last-restore-boot
        └─ for each line: terminal_launch_restore → new window
              └─ wrapper.sh --restore <path> <tool>
                    └─ session launches with AI resume flag
                          └─ conversation restored by cwd
  └─ picker shown in the originally opened window
```

## Testing (TDD — every change behavior-tested)

- `current_boot_id` parses `kern.boottime` sec value.
- Snapshot write: correct lines from mocked `tmux list-sessions` /
  `show-environment`; atomic; excludes non-ghost (no `GHOST_TAB=1`) sessions;
  drops dead sessions on re-derivation.
- `maybe_restore_session` gating: new boot + prior-boot lines → spawns per line
  and writes marker; same boot → no-op; empty snapshot → no-op; restore mode →
  no-op (no infinite spawn).
- `build_ai_launch_cmd` resume variant → correct continue flag per tool.
- `terminal_launch_restore` per adapter → correct command (assert against mocked
  `open` / `osascript`).
- `wrapper.sh --restore` arg parsing → cd, tool forced, picker skipped,
  maybe_restore skipped.

## Out of scope

- Auto-launch at macOS login (LaunchAgent). Recovery is triggered when the user
  opens Ghost Tab, not at boot.
- Restoring in-pane visual state (scrollback, lazygit cursor, editor buffers).
  Only layout + cwd + relaunched program + AI conversation come back.
- Vendoring tmux-resurrect / tmux-continuum.
