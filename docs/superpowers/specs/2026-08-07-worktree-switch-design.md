# Switching worktrees from the Switch modal

The modal reached by clicking the ledger's account pill switches the agent, the
Claude login, and the subscription backend. It gains a fourth thing to switch:
which **git worktree** of the project this tab is pointed at.

## What a worktree pick does

It rebuilds **this tab in place**. All three panes respawn at the chosen
worktree: the ledger re-runs there, the agent relaunches with a fresh
conversation in the new checkout, the spare pane follows. Same Ghostty tab, same
tmux session, same agent and account — a different checkout.

Picking a *new* Ghostty tab or a new tmux window was considered and rejected:
"switch" in this modal has meant "this pane is now X" for every other row, and a
worktree row that quietly opened something else would be the odd one out.

## Rows

After the agent rows: a blank separator, a non-selectable `󰘬 Worktree` group
header, then one row per worktree — the **main checkout first** (git's first
porcelain block), then the rest in git's order. Rows nest under the header the
same way Claude logins nest under `󰵲 Claude`.

- Label is the branch name; a detached worktree reads `(detached)`.
- Row hue is **114** (green), which is outside `claudeaccount.Palette`, so a
  worktree row can never be mistaken for a login.
- The worktree the session currently runs carries its own `●`. This is the first
  time two rows are active at once (the running account *and* the current
  worktree), so the dot stops being "the row the cursor started on" for worktree
  rows only — every existing row keeps its exact current behaviour.
- **The group is omitted entirely when the project has one worktree.** No header,
  no separator, no rows. There is nothing to switch to.

The title becomes **"Switch"**. "Switch agent" stops being true once the modal
also moves the checkout, and a title that changed with the worktree count would
flicker between sessions. The legacy claude-only popup keeps
"Switch Claude login".

### The header refactor this forces

`headerLines()` returns *one* number and every consumer adds it to a row index:
layout height, `--measure`, and the mouse mapping all assume a single header
above all rows. A second group header silently lands every click one row off —
the failure its own comment already warns about.

It is replaced by an explicit display-entry list (each entry is a row index, or
-1 for a header/blank line) that rendering, sizing and click mapping all read.
One source of truth instead of three agreeing by hand.

## Applying the pick

`worktree:<path>` travels through the existing result file. `open_account_switcher`
parses it into a new `worktree` kind in `_apply_account_switch_choice_loaded`,
which:

1. **Revalidates** the target against `git -C <project_dir> worktree list
   --porcelain`. A popup can sit open while worktrees are removed elsewhere; an
   unvalidated path would respawn three panes at an arbitrary directory.
2. Rewrites `project_dir=` in the relaunch context. Future switches,
   `tab_view_new_window` and `gt_ensure_panes_watch` all read it.
3. Sets the tmux session env `WISP_DECK_PATH`. Without it a crash-restore
   reopens the **old** checkout.
4. Relaunches the agent pane through the existing draft-preserving path, so
   attention fencing, the settings overlay and the unsent-draft replay keep
   working — but **fresh, never resumed**. `relaunch_ai_pane` always resumes
   `current_ai_session`; that transcript belongs to the old checkout, so a new
   `_gt_fresh_launch` opt-out (the file's existing dynamic-scoping convention)
   blanks it for this path only.
5. Regenerates the spare server's config at the new dir and respawns the ledger
   and spare panes with `-c <new>`. The inner spare tmux runs
   `exit-unattached on`, so it re-reads the regenerated config on respawn and its
   `@gt_dir` and `bind t` follow for free. The outer `bind-key t` is rebound too,
   matching what a launch at that dir would have set.

Panes are located by `#{pane_start_command}` (the ledger's carries
`compact_view`) rather than by index, and the agent pane keeps its existing
`@gt_ai` marker.

## What deliberately does not change

Project name, tmux session name, Ghostty tab title, tab-view bar. The main menu
already gives a worktree the **project's** name (`mainmenu.go:2094`) — a worktree
is the same project on another branch. The branch itself is shown by the ledger
header, which repoints when its pane respawns.

## Plumbing

- `--worktrees <file>` — `branch:path` lines, split on the first colon (git
  forbids `:` in a ref name, so the remainder is always the path). A file, not a
  flag value, matching `--list` / `--configs`; paths need no quoting.
- `--active-worktree <path>` — the checkout this session runs, marked with `●`.
- `switcher_supports_worktree_rows()` probes `--active-worktree` in `--help`,
  mirroring the three capability probes already there, so an older installed
  binary just gets no worktree group.

## Tests

Go (`cmd/wisp-deck-tui`):
- worktree rows render under the header, main first, `●` on the running checkout
- a single-worktree project renders no group at all
- `switchResultValue` yields `worktree:<path>`
- a click on a worktree row selects **that** row — two headers above it
- `--measure` still matches the rendered card with worktree rows present

Bash (`test/bash`):
- the apply path rewrites `project_dir`, sets `WISP_DECK_PATH`, and respawns all
  three panes with `-c <new>`
- a path that is not a worktree of the project is refused, with no respawn
- the current worktree is a no-op
- the agent relaunch carries no resume session id
- `open_account_switcher` passes the new flags only when the binary supports them
