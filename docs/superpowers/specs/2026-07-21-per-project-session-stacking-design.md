# Per-Project Session Stacking

**Date:** 2026-07-21
**Status:** Approved design, pre-implementation

## Problem

Opening the same project in multiple wisp-deck tabs spawns a full duplicate
stack per tab — another AI process (Claude Code / Codex / OpenCode), another
lazygit, another ledger renderer. Tabs proliferate, system load climbs (lag,
slow new-session starts, crash risk), and the user ends up with "too many tabs
with the same thing."

A shared process pool is not possible: each AI tool is an interactive TUI bound
to exactly one conversation, one pty, and one working directory fixed at
launch. Load scales with live conversations, not tabs. What CAN be
consolidated is tabs: one Ghostty tab per project, hosting several sessions.

## Concept

One Ghostty tab per project. A tab hosts a **stack** of wisp sessions for its
project. Each session in the stack is a complete, independent wisp session:
its own tmux session, its own three-pane layout (AI tool + lazygit + spare),
its own conversation. A session bar in the tab shows the stack; a keybinding
switches the tab between sessions instantly via `tmux switch-client`.
Switching never touches the sessions' processes — everything keeps running.

**Explicitly out of scope:** freezing/pausing background sessions (deferred to
a separate project), cross-project tab sharing, clickable bar chips (v1 is
keyboard-only).

## Behavior

### Consolidation moment (re-picking an already-open project)

Ghostty cannot programmatically focus an existing tab (the adapter can only
`open -na Ghostty`), so the roles flip — with the same visible outcome as
"jump to the existing tab":

1. After `select_project_interactive`, wrapper.sh detects whether a live wisp
   session already exists for the picked project. Detection is cheap tmux
   introspection only (`tmux ls` + per-session env lookup) — **no blocking
   subprocess**; the launch-critical-path budget (~130ms) and its guard test
   must stay green.
2. The new tab creates its fresh session as normal and **adopts** the
   project's existing session(s) into its stack. All sessions live on the same
   tmux server, so any tab's client can switch to any of them.
3. The old tab hands off ownership of its session(s) to the new tab and closes
   itself **without** killing its session's process tree.

Result: the user lands in the front tab, in a fresh conversation, with the old
conversation one switch away. One tab per project remains.

### Session bar

- The **outer tmux status bar** (currently unused) becomes the session bar:
  one chip per session in the stack. The active session's chip uses the tool's
  accent colour; inactive chips are plain — mirroring the spare pane's inner
  tab-bar aesthetic.
- Chip content: index + AI tool + a short identity hint (age or first-prompt
  snippet; exact content is an implementation detail).
- The bar is always visible when the stack has ≥ 2 sessions. Whether it shows
  for a single-session tab is an implementation choice (prefer hidden, so the
  common case looks like today).

### Switching and stack management (keyboard-only in v1)

- A hotkey cycles to the next session in the stack (`tmux switch-client`);
  the bar shows the new position.
- A hotkey starts an **additional** fresh session for this project from inside
  the tab — built **in-place** by `wrapper.sh --stack-new <owner> <client>`
  (backgrounded from the bind): the builder constructs a full session,
  registers it in the CURRENT tab's stack file, restamps its owner-pid to the
  owner wrapper (register-before-restamp, mirroring the adoption ordering),
  switches the pressing client to it, and exits. No new Ghostty tab, no
  adoption handoff, no tab churn. (v1 used the consolidation code path via a
  simulated Cmd+T; that detached the old tabs' clients, and any session whose
  wrapper predated stacking killed itself on that detach.)
- A hotkey closes the **current** session only: kills that session's process
  tree (existing grace-period logic), drops it from the stack, switches to a
  neighbour. Closing the last session closes the tab.
- Clicks are out: the outer tmux deliberately keeps mouse off so clicks fall
  through to the spare pane's inner tmux tab bar. v1 must not change that.

### Ownership & cleanup (stack-aware)

Today wrapper.sh's window-close cleanup kills its single session's process
tree — the zombie-prevention core feature. This becomes stack-aware:

- A tab owns **all** sessions currently in its stack. Closing the tab kills
  every session in the stack, full tree, using the existing per-session
  cleanup (`kill-session` path in tmux-session.sh, spare-tabs cleanup, ledger
  hover uninstall — all of it, once per session).
- The consolidation handoff **atomically transfers** a session from the old
  tab's ownership to the new tab's before the old tab exits. Invariants:
  - A session is owned by exactly one tab at every instant.
  - The old tab's exit path must not kill a session it no longer owns
    (no wrong-kill).
  - A crash of either tab mid-handoff must not leave a session unowned
    (no zombie): the handoff orders operations so that at worst the session is
    still owned by the old tab and dies with it.
- Single-session close (bar hotkey) removes exactly that session's tree and
  its ownership record; other stack members are untouched.

### Known v1 limitations

- If the adopting tab dies mid-handoff **after** marking (its stack file
  already lists the session and `WISP_DECK_ADOPTED_BY`/`WISP_DECK_OWNER_PID`
  already point at it), the adopted session's owner is now a dead PID. The
  orphan reaper (`stack_reap_orphans`) will tear that session down like any
  other orphan once its two-strike window elapses. The adopted conversation
  is therefore **destroyed rather than leaked** — no-zombie is prioritized
  over no-loss.

### Upgrade boundary (live install)

The install is a live symlink: new code deploys the moment it lands, but
long-running wrapper processes keep their old script and traps in memory.
Two consequences, both handled explicitly:

- **Adoption is gated on protocol capability.** A session launched by a
  pre-stacking wrapper lacks the `WISP_DECK_OWNER_PID` env stamp, and its
  still-running wrapper kills its own session unconditionally when its client
  detaches — adopting it would close it instead of stacking it. Consolidation
  therefore adopts only sessions carrying the stamp
  (`stack_adoptable_sessions_for_project`); older sessions keep their own tabs,
  and `--stack-new` refuses to build for an unstamped owner.
- **`exit-unattached off` is written on every launch.** Pre-stacking wrappers
  set the server-wide `exit-unattached on`; a server started by one keeps that
  fossil, and one all-clients-detached moment would kill every background
  stack session. Dropping the old `on` from the chain was not enough.

## Interactions with existing subsystems

- **Crash restore:** unchanged in v1. Restored sessions reopen as separate
  tabs exactly as today; stacking applies only to interactive picks. The
  restore-queue authorization invariants (`restore_pop_authorized`) are not
  touched. Merging restore into stacks is a follow-up.
- **Session identity:** each stacked session keeps its own durable identities
  (Codex sidecar file, Claude session stamps, session-pool dir) — nothing in
  those contracts changes; there are simply more live sessions per tab.
- **Spare pane inner tmux:** each session keeps its own inner spare-tabs
  server (socket derived from the session name) — no sharing.
- **Statusline / ledger:** per-session as today. The session bar is additive
  chrome on the outer status bar only.
- **Launch invariants:** all three guarded launch invariants hold — no
  blocking subprocess pre-picker, none between pick and tmux (detection is
  tmux-only), no foreground `run-shell` in the launch chain.

## Testing

TDD throughout (test first, watch it fail). Bash integration tests in
`test/bash/` using the existing helpers; heaviest coverage on ownership:

- **Detection:** live-session-for-project lookup — found / not found / stale
  (dead session name lingering) / multiple projects live at once.
- **Handoff:** ownership transfers atomically; old tab's cleanup after handoff
  kills nothing; crash-before-handoff leaves session owned (and killed) by the
  old tab; no double-kill.
- **Tab close:** all stacked sessions' trees die; no survivors (zombie check).
- **Single-session close:** exactly one tree dies; stack shrinks; last-session
  close closes the tab.
- **Bar:** content generation for 1/2/N sessions; active-chip accent follows
  the current session's tool.
- **Critical path:** `launch_critical_path_test.go` stays green; detection
  adds no blocking subprocess (extend the property test's mock set if needed).
