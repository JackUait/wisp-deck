# Tab-switch keyboard shortcuts

## Problem

The tab view (`lib/tab-view.sh`) draws one numbered chip per tmux window in the
outer session's top bar (`wisp-deck 1 +`). Today a tab is switched only by
**clicking** its chip; there is no friendly keyboard shortcut. Worse, the chips
are labelled `window_index + 1` (`tab_view_status_left`, `lib/tab-view.sh:35`),
so the tmux default `prefix+1` jumps to window index 1 — the chip labelled
**`2`**. Jump-by-number is not just missing, it is off by one.

## Goal

Add non-conflicting outer-session prefix binds to **cycle** and **jump** between
tab-view windows, correcting the off-by-one so the number pressed matches the
chip shown.

## Design

Added to the existing second-batch `bind-key` block in `wrapper.sh` (currently
`bind-key c` … at `wrapper.sh:767-777`):

**Jump to tab N** — `prefix+1` … `prefix+9`:
- `prefix+1` → `select-window -t :0`, `prefix+2` → `:1`, … N → index `N-1`.
- The chip is `window_index + 1`, so pressing the number selects the window
  whose chip carries that number.

**Cycle** — `prefix+n` / `prefix+p`:
- `prefix+n` → `next-window`, `prefix+p` → `previous-window` (both wrap).
- These are the tmux-native meaning for window cycling and are unused elsewhere
  in the outer session, so they add no conflict.

### Why these keys are non-conflicting

Prefix keys are consumed by tmux and never reach the AI pane, so the only
conflict surface is other prefix binds in this session. Reserved today:
`prefix+c` (new tab), `prefix+i` (screenshot), and `prefix+t/w/Tab/BTab` — all
routed to the **spare** terminal. `prefix+Tab` therefore stays with the spare;
we use `n`/`p` and the number row instead. The number binds only correct the
(wrong-target) tmux default; they steal nothing.

## Making it work from every pane

The AI and ledger panes have no nested tmux, so the outer prefix binds above
already reach them. The **spare pane runs a nested tmux that owns the prefix**
(`lib/spare-tabs.sh:77`), so `prefix+n/p/1-9` there hit the *inner* server (the
spare's own windows), never the outer tabs.

Following the codebase's existing pattern — `lib/spare-tabs.sh:81` mirrors
`prefix+t` into the inner config so it "works the same regardless of pane
focus" — `spare_tabs_config` now takes the outer session name and, when given
it, emits forwarding binds in the inner config:

```
bind n run-shell "env -u TMUX -u TMUX_PANE tmux next-window -t <outer> ..."
bind p run-shell "env -u TMUX -u TMUX_PANE tmux previous-window -t <outer> ..."
bind 1..9 run-shell "env -u TMUX -u TMUX_PANE tmux select-window -t <outer>:N-1 ..."
```

The `-u TMUX` scrub makes tmux target the **outer default socket** (the outer
session runs on the default socket via `TMUX_CMD`), so the inner keys switch the
project tabs. `wrapper.sh` passes `$SESSION_NAME` as the new 6th arg.

**Tradeoff:** this repurposes the spare's own default `n`/`p`/`1-9` window
navigation (from inside the spare pane) to switch project tabs instead. The
spare is a secondary terminal, usually single-window, and its own tabs stay
reachable by mouse and by the outer `prefix+Tab`/`BTab` cycle — so consistent
project-tab switching from every pane is the better trade.

## Out of scope

- `base-index` (would break the chip numbering formula).
- The spare `Tab`/`BTab` binds.
- Prefix-free (`bind -n`) keys — higher collision risk with the AI tool.

## Testing

Extend `TestWrapper_tab_view_bar_and_binds` (`test/bash/tab_view_test.go`) to
assert the recorded tmux invocation contains:
- `bind-key n next-window` and `bind-key p previous-window`
- `bind-key 1 select-window -t :0` (proves the off-by-one correction)

Written failing first (binds absent), then the `wrapper.sh` binds are added to
make it pass.
