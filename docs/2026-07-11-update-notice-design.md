# Update Notice in the Main Menu Header — Design

Date: 2026-07-11

## Problem

`lib/update.sh` already detects newer npm versions in the background and writes
`~/.config/wisp-deck/update-available`, but the only surfacing is a plain
`echo` at wrapper startup that is immediately hidden by the loading screen and
TUI. The `--update-version` flag on `wisp-deck-tui main-menu` is declared but
never applied to the model, and the model's `renderUpdateRow` (a stale
full-width "brew upgrade" row) is dead code. Users never learn an update
exists, and there is no way to update from the menu.

## Goal

Show that a new version is available directly under the right-aligned
"Wisp Deck" wordmark in the main-menu header, and provide a button (keyboard
`U` + mouse click) that performs the update.

## Design

### Go TUI (internal/tui)

- New right-aligned notice, rendered on the row directly beneath whichever
  header row hosts the "Wisp Deck" wordmark:
  - PLAN/subscription row present (wordmark lives there) → notice right-aligns
    on the AGENT/title row.
  - No PLAN row (wordmark on the title row) → notice right-aligns on the
    header spacer row below it.
  Both rows already exist, so header height, scroll math, and click-row maps
  are unchanged.
- Notice content: `⇡ v<version> available · U Update`. The version part is
  yellow (the existing update/stale color 220); `U Update` is accent-colored
  and reads as the button, mirroring the action-bar keybinding style. Hover
  brightens/bolds the button.
- The notice renders on all three tabs (Projects / Settings / Stats), whose
  boxes share the header-row helpers.
- Interaction: `u`/`U` (only while a notice is shown) and mouse click on the
  notice exit the TUI with result `{"action": "update"}` (a new `regionUpdate`
  mouse region).
- Remove the dead `renderUpdateRow`, its call site, and the `updateVersion`
  adjustments in the scroll/click math.

### cmd/wisp-deck-tui

- `main-menu` applies the already-declared `--update-version` flag via
  `model.SetUpdateVersion`.

### Bash

- `lib/update.sh`:
  - New `get_update_version <install_dir>`: prints the flag-file version only
    when it exists and differs from `<install_dir>/.version` (guards against a
    stale flag after an update, since the 24h throttle delays re-checks).
  - `notify_if_update_available` no longer deletes the flag (the menu needs to
    keep seeing it until the update actually happens) and uses the same
    differs-from-installed guard.
  - `check_for_update` deletes the flag when local == remote (self-heals stale
    flags on its next run).
  - New `run_wisp_deck_update`: runs `npx wisp-deck@latest` (forces the latest
    tarball past npx's cache).
- `lib/menu-tui.sh`: when `_update_version` is unset, populate it from
  `get_update_version "$HOME/.local/share/wisp-deck"` so the existing
  `--update-version` pass-through fires.
- `wrapper.sh`: handle the new `update` menu action — run
  `run_wisp_deck_update`, then loop back to the menu (which re-execs the
  freshly installed binary).

## Error handling

- Missing/empty flag file, missing `.version`: `get_update_version` prints
  nothing; the TUI simply shows no notice.
- `npx` failure: the installer's own output shows the error; the wrapper loops
  back to the menu regardless, leaving the notice up.

## Testing

TDD throughout: Go render/key/mouse tests in `internal/tui`, cmd wiring test,
and bash tests in `test/bash` for `get_update_version`, the notify/check flag
lifecycle changes, `run_wisp_deck_update` (mocked `npx`), menu-tui flag
population, and the wrapper's `update` case.
