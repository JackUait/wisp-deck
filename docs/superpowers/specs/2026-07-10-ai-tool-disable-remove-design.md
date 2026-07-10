# AI tool disable / remove — design

Date: 2026-07-10
Status: approved

## Problem

The Settings → AI tools panel can install a tool and pick the default, but a
tool can neither be hidden from wisp-deck nor uninstalled from the system. A
user who tried Codex once is stuck seeing it in the tool-cycling row forever.

## Decisions (user-confirmed)

- Both actions ship: **disable** (wisp-deck ignores the tool, stays installed)
  and **remove** (system uninstall with a warning).
- Claude Code can be **disabled but not removed** — mirrors install, which also
  excludes claude (curl installer, no clean uninstall; wisp-deck's statusline
  and account machinery depend on it).
- The removal warning is a **popup modal** (same chrome as the account-switch
  modal: gray surround, faint dim, rounded corners), not an inline confirm.

## Data model

New file `~/.config/wisp-deck/disabled-tools` — one tool name per line
(`claude`, `opencode`, `codex`). Same one-file-per-concern pattern as
`ai-tool`. Absent file = nothing disabled. Read by both bash and Go.

## Disable

- Panel key `x` toggles the focused tool's disabled state (any tool).
- Disabled row renders dimmed with a gray `disabled` tag in the right column.
- `wrapper.sh` filters disabled tools out of `AI_TOOLS_AVAILABLE` after PATH
  detection. Safety valve: if filtering would leave zero tools, the disable
  list is ignored (warn) so launch never bricks.
- A disabled tool cannot become default. If the current default is disabled,
  the default falls back to the first enabled tool and is persisted via the
  existing `ai-tool` file path.
- Bash helper in `lib/ai-tools.sh`: `filter_disabled_ai_tools` (pure, file
  path passed in) so wrapper stays thin and the logic is testable.

## Remove

- Panel key `r` on an installed opencode/codex row opens the warning modal:
  tool name, the exact command that will run, `⏎` confirm / `esc` cancel.
- Confirm runs a background bash call (same seam as install:
  `installCmdFor`-style, `WISP_DECK_LIB_DIR` in env, function name from a
  fixed map): new `remove_opencode` / `remove_codex` in `lib/install.sh`.
- `remove_codex`: `npm uninstall -g @openai/codex`, then delete the
  `~/.local/bin/codex` launcher symlink if it points into npm's global prefix
  (`ensure_codex` creates it on lazy-nvm setups).
- `remove_opencode`: `npm uninstall -g opencode-ai`, same symlink cleanup.
  The modal notes OpenCode stays launchable via npx after uninstall (that is
  how detection works) and suggests `x` to hide it from wisp-deck.
- While removing, the row shows the same progress bar as install; on
  completion the panel re-detects and refreshes.

## Plumbing

- `lib/menu-tui.sh` passes `--disabled-tools-file` to `wisp-deck-tui
  main-menu`.
- `models.AITool` gains `Disabled bool`; `DetectAITools` stays PATH-only, the
  panel overlays disabled state from the file.
- Key-hint line becomes `⏎ install · d default · x disable · r remove · esc
  close` (remove hint only meaningful on removable rows).

## Error handling

- Uninstall failure → same error surface as install failure (panel error line
  + feedback message), tool stays listed as installed.
- Toggling disable writes the file atomically (write temp + mv, or plain
  truncate — file is tiny; follow existing `ai-tool` write style).

## Testing (TDD, tests first)

- bash: `filter_disabled_ai_tools` (empty file, missing file, all-disabled
  safety valve), `remove_codex` / `remove_opencode` (npm uninstall invoked,
  symlink cleaned, failure propagates) via mocked npm in `test/bash/`.
- Go: panel tests beside `internal/tui/ai_tools_panel_test.go` — `x` toggles
  and persists, disabled row rendering, default fallback on disable, `r` opens
  modal only for removable rows, confirm spawns remove and shows progress,
  cancel closes, done/failure paths.
- wrapper integration: disabled tool absent from the CSV handed to the menu.
