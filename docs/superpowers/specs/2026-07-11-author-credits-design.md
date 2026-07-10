# Author Credits — Design

**Date:** 2026-07-11
**Status:** Approved

## Goal

Attribute authorship of Wisp Deck to Evgeniy Pyatkov in three places: a repo
credits file, project metadata, and the Settings screen of the TUI.

## The credit line

Canonical text:

> Made by Evgeniy Pyatkov (@jackuait) · Telegram: @that_ai_guy — https://t.me/that_ai_guy

## 1. Interface — static credit line in Settings (Approach A)

Append a dim, **non-selectable** credit line to the Settings box in
`internal/tui/render_settings.go`, placed after the last settings section and
before the trailing empty-row / separator / help-row block.

- Rendered with the existing dim style, inside the menu box borders (matching
  `emptyRow` width so the right border stays aligned).
- Not a settings item: it is appended directly to `lines`, never added to the
  positional row-index machinery (`settingsItemCount`, row constants, handler
  indices). This deliberately avoids the index ripple across ~6 files and the
  index-hardcoded tests that adding a selectable row would cause.
- No keyboard/mouse handling, no navigation changes.

**Rejected — Approach B (selectable "About" row/panel):** more discoverable but
requires a new handler index that shifts every settings row and breaks
index-hardcoded tests, plus new panel plumbing. Overkill for a static credit.

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
