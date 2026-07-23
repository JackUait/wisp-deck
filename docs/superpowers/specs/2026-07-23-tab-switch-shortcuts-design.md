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
