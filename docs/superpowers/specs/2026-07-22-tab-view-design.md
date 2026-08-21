# Tab view: per-tab session tabs via tmux windows

Date: 2026-07-22
Status: approved (autonomous goal directive)

## Goal

A visible tab bar at the top of every wisp session (the strip directly under
Ghostty's native tabs) from which the user can open additional sessions for
the **same folder** inside the **current tab**, and switch between them.

## Why windows, not sessions

The 2026-07-21 session-stacking experiment (reverted in 61e2bbe) modelled
"multiple sessions per tab" as multiple tmux *sessions* consolidated into one
client — which required a stack registry, cross-session repaint, an owner-pid
reaper, adoption handoff, and one-shot request claims. It broke session
opening and was removed at the user's request.

This feature models the same UX as tmux **windows** inside the tab's one
existing session. That keeps the "one Ghostty tab = one tmux session"
invariant from the revert intact and gets almost everything for free:

- The tab bar is a live tmux format (`#{W:...}`): chips appear/disappear as
  windows are created/killed. No repaint machinery, no registry.
- `cleanup_tmux_session` already TERM/KILLs every pane server-wide in the
  session (`list-panes -s`) and tears down the per-session spare socket —
  extra windows are cleaned up with zero new code.
- No cross-tab state: everything lives in the session.

## UX

- The outer status bar becomes always visible (`status on`, session-level, to
  override the user's global `status off`) and pinned to the top
  (`status-position top`), directly under Ghostty's tab bar.
- Bar contents (all in `status-left`, like the spare pane's inner tab bar):
  ` ⬡ project ` label, one numbered chip per window (1-based; active chip is
  numerals on the theme accent), and a trailing ` + ` button.
- Clicking a chip switches to that window. Clicking ` + ` — or pressing
  `prefix+c` — opens a **new window with the full three-pane wisp layout**
  for the same folder: compact-view ledger, a fresh AI conversation (same
  tool, account, settings and screenshot filter as the session), and a spare
  tabs pane on the session's existing inner spare server.

## Components

**`lib/tab-view.sh`** (new, independently sourceable, fail-open):

- `tab_view_status_left <project> <accent>` — prints the status-left format
  string. Window chips ride in `#[range=user|wdtab:#{window_id}]`, the +
  button in `#[range=user|wdnew]`, so clicks are identifiable via
  `#{mouse_status_range}` (same pattern as `spare_tabs_status_left`).
- `tab_view_new_window <tmux_cmd> <lib_dir> <session>` — reads the session's
  env (`WISP_DECK_RELAUNCH_FILE`, `WISP_DECK_CLAUDE_ACCOUNT`,
  `WISP_DECK_CLAUDE_PROVIDER`, `WISP_DECK_CODEX_CMD`), loads the relaunch
  context, builds a **fresh** AI launch via `build_switch_launch_cmd` (empty
  resume id; attention env explicitly blanked so the raw, unsupervised
  command is produced), and creates the window with the wrapper's exact
  geometry: ledger pane, `split-window -h -p 75` AI pane marked `@gt_ai`,
  `split-window -v -p 45` spare pane, AI pane focused.
- `tab_view_dispatch <tmux_cmd> <lib_dir> <session> <range>` — routes a
  status-line click: `wdtab:<id>` → `select-window`, `wdnew` →
  `tab_view_new_window`.

**`wrapper.sh`** wiring:

- `tab-view` joins `_gt_libs`.
- First batch (new-session): `status on`, `status-position top`,
  `status-left-length 400`, blank `window-status-format`/`-current-format`/
  `-separator`, `status-left` from `tab_view_status_left`.
- Second batch: `bind-key c` (new same-folder window) and
  `bind-key -n MouseDown1Status/StatusLeft/StatusRight` → dispatch. All binds
  use `#{q:session_name}` / `#{q:mouse_status_range}` (server-global binds
  must never bake a session name) and are placed **before** the ledger-hover
  `run-shell -b` so the hover key-table clone copies them.

## Critical-path discipline

Everything added to the launch path is pure bash string building (one extra
`source`, one format-string function call, a handful of tmux commands inside
the existing batches). No new subprocesses before or after the picker.
`tab_view_new_window` runs only on user action (click/keybind), off the
launch path.

## Known v1 limitations (accepted)

- Extra windows launch the AI tool **without** the attention adapter (no
  idle-sound/tab-bell/attention state for those panes) — one attention
  generation has one publisher, window 0's. Fresh conversations only.
- Post-reboot restore restores the session as a single window (the snapshot
  captures the session's stamped conversation, not per-window layouts).
- `find_ai_pane`-style session-wide `@gt_ai` lookups keep acting on the
  first marked pane; mid-session account/agent switching from a secondary
  window is unchanged-but-unscoped (acts on the pane the ledger pill's own
  window context resolves).

## Testing

TDD, `test/bash/tab_view_test.go` (+ wrapper-chain assertions in the
existing recordingTmuxMock pattern):

1. `tab_view_status_left` carries the project label, `wdtab:`/`wdnew` ranges,
   the accent colour, and the `#{W:` window iterator.
2. `tab_view_dispatch` routes `wdtab:@N` to `select-window -t @N`; `wdnew`
   to window creation.
3. `tab_view_new_window` (mock tmux + fixture relaunch context): creates the
   window in the project dir, splits AI (75%) and spare (45%) panes, marks
   `@gt_ai`, focuses the AI pane, launches the context's tool fresh (no
   `--resume`, no `claude-attention` supervisor), honours the session's
   account dir, and uses the session's spare socket/conf.
4. Wrapper chain: first batch turns the status bar on at top with the
   tab-view status-left; second batch binds `c` and the three status mouse
   keys before the hover install.

---

## Update 2026-08-21: chip modes

The bar has two chip modes, selected by `tab_bar` in the wisp-deck settings
file and cycled from Settings → Appearance → Tab bar.

- `compact` — the numbered chip this document describes.
- `large` (**default**) — a filled card: a rounded cap at each end enclosing
  the number, the tab's own title and the elapsed time of the turn running in
  it. The active card is filled with the tool's accent, an idle one with the
  bar's grey, and the `[+]` button is an idle-filled card too, so the bar reads
  as one row of cards. The caps are powerline codepoints Ghostty draws itself
  (U+E0B6/U+E0B4, one cell each in tmux); the fill is carried by an explicit
  `bg=` on every segment between them.

A large chip renders two window options, `@wd_tab_title` and
`@wd_tab_progress`. The per-session watcher (`attention_watcher_tick`) refreshes
both every tick from the window's `@gt_ai` pane: the title is its `pane_title`
(where the agent stamps a summary of the current turn), the progress is the
elapsed time parsed off its live status line. That keeps the "no repaint
machinery" property of the chip LIST — windows still appear and disappear as a
pure `#{W:...}` expansion — while the state inside a chip, which only becomes
valid after the agent boots, is re-resolved rather than loaded once.

See the CLAUDE.md section "A large tab chip renders state the agent owns, so
the bar sanitizes it" for the invariants, and `test/bash/tab_view_large_test.go`
plus `internal/tui/tab_bar_setting_test.go` for the guards.
