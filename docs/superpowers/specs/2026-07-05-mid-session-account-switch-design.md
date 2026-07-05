# Mid-session Claude account switch

## Goal

Let the user change their active native Claude account **while a wisp-deck session is
running**, without closing and reopening the session. The switch happens by clicking an
account pill in the compact-view ledger; the running `claude` in the AI pane is relaunched
under the newly chosen account's `CLAUDE_CONFIG_DIR` in continue (`-c`) mode, so the
conversation carries over (history is shared across accounts).

## Background

- An **account** is a native Claude login isolated by its own `CLAUDE_CONFIG_DIR`. The
  active one is a pointer file (`claude-account`) resolved at launch and baked into the
  `claude` launch command in the AI tmux pane (`build_ai_launch_cmd`).
- Accounts share one conversation store (symlinked into `~/.claude`), so `claude -c`
  after a switch resumes the same conversation.
- Account colors/labels are already shared between bash (`lib/statusline.sh`) and Go
  (`internal/claudeaccount`).
- The **compact-view ledger** (pane 0) is wisp-deck's own TUI: it does raw SGR mouse
  handling and floats `tmux display-popup` overlays (the diff popup). It is the only
  surface wisp-deck fully controls end-to-end — the AI pane's mouse belongs to Claude,
  and the outer tmux status bar has mouse deliberately off.

## Scope gate

The pill appears (and the switch is possible) only when ALL hold:
- `WISP_DECK_TOOL=claude`
- 2+ accounts exist (`gt_multiple_claude_accounts`: at least one managed login + Default)
- the auto-switch rotation proxy is **not** active (it already owns account rotation;
  a manual switch would fight it) — detected via the proxy account file being present.

## Components

### 1. Pill render (compact-view bottom bar)

`account_pill <label> <color>` (new `lib/account-switch.sh`) prints a colored
` 󰀄 <label>` pill string on line 1 and its visible click-width on line 2 (the
`ahead_behind_marker` two-line convention). Rendered as the **leftmost** element of the
ledger bottom bar, followed by ` · <branch>` and the existing scroll/hint segments.
Leftmost placement gives a fixed, deterministic click region (columns `1..width`).

The label/color are recomputed each build tick from the account files
(`claude-account` pointer + `claude-accounts.list` + `claude-account-colors`), reusing
`get_active_claude_account`, `gt_claude_account_label`, `gt_account_color`. So after a
switch the pill updates on the next tick with no extra plumbing.

The pill is not shown while a batch-discard confirm owns the bottom row.

### 2. Click dispatch (compact-view)

The bottom bar sits on the ledger's last screen row (`h`). In the SGR left-click branch
of `handle_key`, when `mrow == h` and the pill is shown and `mcol <= pill_width`, invoke
the switch flow (open popup, then relaunch) instead of a diff popup. Mirrors the existing
`open_diff_popup` click path: after the popup closes, `enter_ui_mode` is re-asserted and
`need_build=1`.

### 3. Switcher popup (Go: `wisp-deck-tui claude-account-switch`)

A standalone Bubbletea popup listing Default + managed accounts, each in its account
color, current one marked. Up/down/j/k move, Enter/click select, Esc/q/Ctrl-C cancel.
On select it writes the pointer (`claudeaccount.SetActive`) and prints
`{"selected":true,"dir":"<dir>","changed":<bool>}`; on cancel `{"selected":false}`.
`changed` = the chosen dir differs from the previously active one. Reuses
`internal/claudeaccount` for data, colors, and the pointer write. Pure
selection/persist/JSON logic is factored out and unit-tested (Bubbletea shell stays thin).

Flags: `--list`, `--accounts-dir`, `--pointer`, `--colors`, `--default-label`.

### 4. Relaunch (compact-view + `lib/account-switch.sh`)

At launch `wrapper.sh` writes a per-session **relaunch-context file** holding the pieces
needed to rebuild the claude launch for a different account: `claude_cmd`,
`opencode_cmd`, settings path, filter prefix, project dir, and the account file paths.
Its path is exported into the session env as `WISP_DECK_RELAUNCH_FILE`.

On a changed switch, the flow:
1. runs the switcher popup (writes the pointer),
2. reads `changed` from its JSON — no change ⇒ no-op,
3. resolves the new account's `CLAUDE_CONFIG_DIR` from the updated pointer,
4. rebuilds the launch command via `build_ai_launch_cmd` in continue mode
   (`WISP_DECK_RESUME=1`, no session id ⇒ `claude -c` with plain-claude fallback),
5. finds the AI pane via its `@gt_ai` marker and
   `tmux respawn-pane -k -t <pane> "<cmd>; exec bash"`.

The relaunched `claude` inherits the new env, so **its own statusline pill updates
automatically** — no statusline changes needed.

## Edge cases

- Same account chosen ⇒ `changed=false` ⇒ no respawn.
- Default (Keychain) ⇒ `CLAUDE_CONFIG_DIR` left unset.
- opencode sessions ⇒ pill never shown.
- Pill only responds when the ledger pane is focused (same as existing file-click).
- Popup cancelled ⇒ no pointer write, no respawn.

## Testing (TDD)

Bash (`test/bash/`):
- `account_switch_gate`: claude+list+no-proxy ⇒ show; opencode / <2 accounts / proxy
  active ⇒ hide.
- `account_pill`: renders ` 󰀄 label` + correct visible width.
- `find_ai_pane`: picks the `@gt_ai` pane from a mocked `tmux list-panes`.
- relaunch command build: mocked tmux asserts `respawn-pane -k` carries the new account's
  `CLAUDE_CONFIG_DIR` (or none for Default) and `-c`.

Go (`cmd/wisp-deck-tui/`):
- switch persist/JSON: selecting a dir writes the pointer and emits the right JSON with
  correct `changed`; cancel emits `{"selected":false}`.

Full suite (`./run-tests.sh`) + `shellcheck` on all modified scripts before push.
