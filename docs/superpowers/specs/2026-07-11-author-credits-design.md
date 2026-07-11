# Author Credits — Design

**Date:** 2026-07-11
**Status:** Approved

## Goal

Attribute authorship of Wisp Deck to Evgeniy Pyatkov in three places: a repo
credits file, project metadata, and the Settings screen of the TUI.

## The credit line

Canonical text:

> Made by Evgeniy Pyatkov (@jackuait) · Telegram: @that_ai_guy — https://t.me/that_ai_guy

## 1. Interface — About shortcut in Settings

**Revised (2026-07-11):** the credit is no longer an always-visible line. It is
hidden behind an **About shortcut** on the Settings tab.

- Pressing `a` on the Settings tab opens a small About card (popup panel). On
  every other tab `a` keeps its long-standing add-project meaning. Esc / Enter /
  `a` / `q` close the card.
- The card is an appended modal panel (`renderAboutPanel`), mirroring the
  account / model-map panels — same box chrome, appended below the Settings box
  in `MainMenuModel.View`, gated on a new `aboutOpen` flag.
- Key handling: `aboutOpen` intercepts input first in `Update` (via
  `updateAbout`); the `a` rebind lives in `handleRune`.
- Not a settings item: `settingsItemCount()` stays `rowKeepAwake + 1`, so the
  positional row-index machinery is untouched. The Settings footer gains an
  `a about` hint.

**Rejected — always-visible static line:** simpler, but the user asked for the
credit to be tucked away behind a shortcut rather than shown on every visit.

## 2. Credits file

- Add `CREDITS.md` at repo root containing the Made-by line and the Telegram
  link.
- Add a short **Credits** section near the bottom of `README.md` linking to
  `CREDITS.md`.

## 3. Project metadata

Enrich `package.json` `"author"` from the string `"JackUait"` to a structured
object:

```json
"author": { "name": "Evgeniy Pyatkov", "url": "https://t.me/that_ai_guy" }
```

## Testing (TDD — test first, watch fail, then implement)

- **Interface:** a Go render test that renders the Settings box and asserts the
  output contains the credit text, AND that `settingsItemCount()` (selectable
  row count) is unchanged — proving the line is not a selectable item.
- **package.json:** a Go test that parses `package.json` and asserts the author
  object has `name = "Evgeniy Pyatkov"` and `url = "https://t.me/that_ai_guy"`.
- **CREDITS.md / README:** static content, no behavior test.

## Out of scope

- Splash-logo and help-footer credit placements (considered, not chosen).
- Any interactive/clickable About entry.
