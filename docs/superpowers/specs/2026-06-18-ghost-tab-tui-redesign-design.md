# Ghost Tab TUI Redesign — Tabbed, Polished Main Menu

**Date:** 2026-06-18
**Status:** Approved design, pending implementation plan

## Problem

Ghost Tab's main menu has accumulated features and now feels cramped. Four
distinct pains, all confirmed by the user:

1. **Action-list bloat** — the `A/D/O/P/S/T` action stack grows over time and
   crowds the project list.
2. **Discoverability** — features hide in keybinds and sub-panels (worktrees,
   model mapping, Claude config); a new user can't see what exists.
3. **Visual density** — everything lives in one box, 2 rows per project, no
   hierarchy or breathing room.
4. **Navigation/flow** — a single flat list mixes projects, worktrees, and
   actions with no grouping or sense of place.

The user wants a **totally new UI that feels like a finished, polished product**,
not an incremental tweak.

## Goals

- Restructure the main menu around a clear, scalable interaction model.
- Make every feature discoverable without memorizing keybinds.
- Achieve a refined, cohesive "finished product" aesthetic.
- Preserve the pixel-ghost mascot as a prominent brand element.
- **Zero change to the bash orchestration contract** — the JSON result actions
  emitted by the TUI stay byte-for-byte compatible.

## Non-Goals

- No change to terminal adapters, process cleanup, or the wrapper.
- No new product features — this is a re-presentation of existing capability.
- No Nerd Font dependency.

## Decisions (locked with the user)

| Area | Decision |
|------|----------|
| Interaction model | **Tabbed sections**: Projects · Settings · Stats |
| Mascot | **Keep the big pixel ghost** beside the box (responsive, as today) |
| Aesthetic | **Refined retro-terminal** — warm per-AI palette, rounded borders, sparing pixel accents |
| Icons | **Unicode-block / geometric only** — no Nerd Fonts (renders everywhere) |
| Palette | Existing per-AI themes kept (claude amber, codex green, opencode gray) |
| Contract | `MainMenuResult` JSON + `actionNames` strings unchanged |

## Layout & Structure

```
╭ Ghost Tab ─────────────────────── ‹ Claude Code › ╮   header: brand + AI switch
│  ▌Projects▐   Settings   Stats                     │   tab bar (active = block accent)
├────────────────────────────────────────────────────┤
│                                                    │      ▄▄▄▄▄
│   (active tab body renders here)                   │     █ ◣◢ █   big pixel ghost,
│                                                    │     █ ▐▌ █   right side when wide
│                                                    │      ▀▀▀▀
├────────────────────────────────────────────────────┤
│  ▸ contextual action bar for current tab           │
╰────────────────────────────────────────────────────╯
  ↑↓ move · ←→ AI · ↵ select · O open once · P plain
```

- **Header** — brand title left (Primary bold), AI switcher right (`‹ Claude Code ›`).
  AI switch lives here on every tab; `←→` cycles it and re-themes the whole UI.
- **Tab bar** — `Projects · Settings · Stats`. Active tab is Primary bold with a
  block accent; inactive tabs are Dim. `Tab` / `Shift+Tab` cycle; `S` / `T` jump.
- **Body** — swaps per active tab.
- **Action bar** — one line, contextual to the active tab and current selection.
- **Footer hints** — rare global launches (`O` open once, `P` plain terminal)
  live here as labeled keys, not as box rows.
- **Ghost** — big at right when the terminal is wide enough; hidden when narrow
  (existing responsive rule in `CalculateLayout`). Bob + sleep animation retained.

## Projects Tab

```
├────────────────────────────────────────────────────┤
│ ▌ 1  blok                              1 worktree   │  selected: accent bar + bright
│      ~/Packages/blok                                │
│   2  knowledgebase                                  │
│      ~/Packages/knowledgebase                       │
│   3  backoffice-elements               1 worktree   │
│      ~/Packages/shiftmanager…/backoffice-elements   │
│      └ ▸ feat/login          add worktree           │  inline expand (w)
│   4  ghost-tab                                      │
│      ~/Packages/ghost-tab                           │
│                                                    │
│   +  Add project                                    │  always-last inline "new" row
├────────────────────────────────────────────────────┤
│  ▸ Open      ⟕ Worktrees      ✕ Delete              │  contextual to selected row
╰────────────────────────────────────────────────────╯
  ↑↓ move · ⇧↑↓ reorder · ←→ AI · ↵ open · O once · P plain
```

- **Project rows** — same 2-line format (name + path). `N worktree` badge Dim,
  right-aligned. 1-indexed numbers kept for jump-to. Stale projects dimmed +
  marked, with the existing launch-confirmation flow.
- **Worktrees** — inline expand/collapse (`w`), add-worktree row, force-remove
  confirmation — all current logic preserved, no regression.
- **Add project** — promoted to a visible `+ Add project` row at the bottom of
  the list (was buried in the action stack). Also reachable via `A`.
- **Contextual action bar** — reflects the selected row:
  - project → `Open · Worktrees · Delete`
  - worktree → `Open · Delete`
  - add-project row → `Add project`
- **Delete** — contextual (`D` / `Del`), same confirm and force-remove-worktree flow.
- **Open once / Plain terminal** — global one-shots; footer keys `O` / `P`.

## Settings Tab

A single list replacing the old settings sub-mode and scattered panels:

```
│  ▌ Config            ‹ Standard Claude ›           │  Claude only; ←→ cycles
│    Model mapping     opus→…  sonnet→…    ▸          │  shown when config ≠ Standard
│    Ghost             ‹ animated ›                   │
│    Tab title         ‹ full path ›                  │
│    Idle sound        ‹ Submarine ›                  │
│    Projects dir      ~/Packages                     │  inline edit on ↵
├────────────────────────────────────────────────────┤
│  ←→ change · ↵ edit / open                          │
```

- Each row: label left, current value right as `‹ value ›`. `←→` cycles values;
  `↵` edits (text rows) or opens a deeper panel.
- **Config row** present only when AI = Claude (existing `ClaudeConfigVisible`).
- **Model mapping** stays a deeper panel reached by `↵` on its row; only shown
  when a non-Standard config is active. API-key entry flow unchanged.
- Reuses `persistSetting`, `persistSound`, `persistClaudeConfig`, and the
  projects-root edit logic — only the framing changes.

## Stats Tab

The existing stats content (`stats.go`) renders inside the tab body rather than
as a pushed screen. The big ghost can sit beside it.

## Visual System

**Borders** — rounded single-line `╭╮╰╯`, Primary at Dim intensity. Internal
rules separate header / body / action bar.

**Color (per-AI palette kept):**
- Brand title: Primary bold.
- Active tab: Primary bold + block accent (`▌Projects▐`); inactive: Dim.
- Selected row: `▌` accent bar (Primary) + Bright text; unselected: Text / Dim.
- Badges and counts: Dim, right-aligned.
- Settings values `‹ … ›`: Accent; chevrons Dim.
- Stale / disabled: Dim + `·` marker.
- Feedback toast (success / error): Accent / red, auto-dismiss via the existing timer.

**Typography rhythm (monospace):** consistent column alignment — names at a fixed
column, paths indented, badges flush-right. One blank line before the `+ Add`
row. One space of padding inside the borders. No ALL-CAPS headers (the tab bar
carries hierarchy).

**Pixel accents (sparing):** the only texture is block glyphs — the tab accent
bar, the selection `▌`, and the ghost. No gradients, no Nerd Font icons.

**States (designed, not afterthoughts):**
- Empty (no projects): big centered ghost + "No projects yet · press A to add".
- Idle / sleep: ghost sleeps with `Zzz` (existing).
- Narrow terminal: ghost hidden, box stays full-featured (existing rule).
- Loading worktree: inline spinner on the row.

**Motion:** ghost bob + sleep retained; a one-frame accent flash on tab switch
and project move (reuses the existing `moveFlash` mechanism). Nothing gratuitous.

## Implementation Shape

`mainmenu.go` is ~3294 lines — already too large. This redesign is the moment to
split the render layer. Model + Update stay in `mainmenu.go`; rendering moves to
focused files:

- `render_chrome.go` — header, tab bar, footer, ghost composition, box assembly
- `render_projects.go` — projects + worktrees + add row + contextual action bar
- `render_settings.go` — settings rows + model-map panel
- `render_stats.go` — stats body (reuse `stats.go` logic)

**State change** — replace `settingsMode bool` with an `activeTab` value
(Projects / Settings / Stats). Stats stops being a pushed screen and becomes a
tab body. `Tab` / `Shift+Tab` cycle; `S` / `T` jump. Key handling routes per
active tab.

**Contract guard (critical)** — `MainMenuResult` JSON and the `actionNames`
strings stay identical:
- `↵` → `select-project`
- `A` → `add-project`
- `D` → `delete-project`
- `O` → `open-once`
- `P` → `plain-terminal`

The action *list UI* disappears; the action *strings* do not. Bash orchestration
sees zero change.

## Testing (TDD — test first, watch fail, then build)

- Tab switching: `Tab` cycles, `S` / `T` jump, active-tab state persists.
- Projects render: selected accent bar, worktree badge, `+ Add` row, inline expand.
- Contextual action bar matches the selected row type.
- Settings render: rows present, Config row gated by AI = claude, model-map gated
  by non-Standard config.
- Stats tab renders inside the body.
- **Regression: JSON action strings unchanged** — guards the bash contract.
- Empty state and narrow-terminal (ghost hidden) layouts.
- Existing tests (`mainmenu_render_test.go`, `mainmenu_reorder_test.go`,
  `mainmenu_worktree_test.go`, `mainmenu_stats_test.go`,
  `mainmenu_claudeconfig_test.go`) updated to the new layout.

Close-out per repo rules: `shellcheck` on any touched scripts, full
`./run-tests.sh`, then push.

## Risks

- **Large refactor of one file.** Mitigated by extracting render files and
  keeping the model/contract stable, so bash never breaks mid-flight.
- **Test churn.** Many existing render tests assert the old layout and will need
  rewriting; the JSON-contract regression test is the safety net.
- **Tab + worktree interaction.** Flat-index resolution (`ResolveItem`,
  `projectToFlatIndex`) is Projects-tab-only; tab routing must not disturb it.
