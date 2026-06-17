# Claude Config Switching — Design

**Date:** 2026-06-17
**Status:** Approved (pending spec review)

## Summary

Let the user keep several Claude Code settings files and switch between them from
Ghost Tab. A "config" is a settings JSON passed to Claude via `claude --settings <file>`.
Selection happens at launch via an inline cycle in the main menu (same pattern as the
existing AI-tool and sound cycles), defaults to the last-used config, and always offers
a "Standard Claude" entry (plain `claude`, no `--settings`). Configs are managed
(add / rename / delete) from the existing config menu.

## Goals

- Maintain multiple Claude settings files, each with a friendly name.
- Switch the active config at launch without leaving the main menu.
- Remember the last-used config across sessions.
- Always be able to fall back to plain Claude ("Standard Claude").

## Non-goals (YAGNI)

- No live switch in a running pane (Claude reads `--settings` once at start; switching
  means relaunch on the next session).
- No in-TUI editing of a config file's JSON contents — the user edits the file in their
  own editor; the TUI only manages which files exist and their names.
- No per-project config binding — a single global last-used config.
- Claude-only — not wired for codex / copilot / opencode.

## Storage

All under `${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/`:

- `claude-configs/` — directory of settings JSON files (e.g. `work.json`, `personal.json`).
- `claude-configs.list` — `name:filename` per line (display name decoupled from filename,
  mirroring the `projects` file format). Lines starting with `#` and empty lines are skipped.
- `claude-config` — active pointer. Holds the active config's **filename** (e.g. `work.json`),
  or is empty / `standard` for plain Claude. This file *is* the "remember last used" store.

"Standard Claude" is a virtual entry — never written as a file, always present in the cycle.

## Launch (wrapper.sh + tmux-session.sh)

`build_ai_launch_cmd` in `lib/tmux-session.sh` gains an optional Claude settings-path
argument. When building the claude command and that path is non-empty, it appends
`--settings "<path>"`; otherwise it builds the plain command (current behavior).

`wrapper.sh`, when `SELECTED_AI_TOOL=claude`:
1. Reads the `claude-config` pointer.
2. If it names a file that exists under `claude-configs/`, resolves the absolute path and
   passes it into `build_ai_launch_cmd`.
3. Otherwise (empty, `standard`, or missing file) launches plain Claude.

Resume mode (`GHOST_TAB_RESUME=1`) keeps current behavior; the settings path also applies
to the `claude -c` resume branch so a resumed session uses the same config.

## Inline switch in main menu (Approach A)

Mirror the existing sound cycle (`CycleSoundName`):

- New `MainMenuModel` state: `claudeConfigs []ClaudeConfig` (each `{Name, File}`, plus the
  implicit Standard at index 0), `selectedConfig int`, and `claudeConfigFile` (pointer path).
- New `CycleClaudeConfig(direction)` — cycles forward/back through
  `[Standard, <config1>, <config2>, …]`, wrapping around, and persists the selected
  filename (empty for Standard) to the pointer file immediately (like `persistAITool`).
- Render a `Config: <name>` line near the AI-tool / sound area, shown **only when the
  current AI tool is claude**; hidden otherwise.
- A keybinding cycles it (consistent with how AI tool / sound are cycled — exact key chosen
  to match existing keymap conventions, documented in the footer hints).

Plumbing:
- `cmd/ghost-tab-tui/main_menu.go` gains `--claude-config-file` and `--claude-configs-list`
  flags, parsed into the model via setters (`SetClaudeConfigFile`, `SetClaudeConfigs`).
- `lib/menu-tui.sh` passes those two paths when building `main-menu` args.

## Management TUI (add / rename / delete)

- New `claude-config-menu` subcommand (selector-pattern clone) listing existing configs
  plus Add. Reachable from `lib/config-tui.sh` via a new `manage-claude-configs` action
  added to the config menu.
- Actions:
  - **Add** — prompt for a name → slugify to `<slug>.json`, create the file containing `{}`
    in `claude-configs/`, append `name:<slug>.json` to the list. (User fills in settings later
    in their editor.)
  - **Rename** — update the name in the list (filename unchanged).
  - **Delete** — remove the file and its list line; if it was the active config, reset the
    pointer to Standard.
- Bash dispatcher in `lib/config-tui.sh` handles the JSON actions and the filesystem/list
  mutations, with TUI feedback via the standard `header/success/error` helpers.

## Testing (TDD — test first, watch fail, then code)

Bash (`test/bash/`):
- `build_ai_launch_cmd` appends `--settings "<path>"` when a settings path is given for
  claude, and omits it when empty — including the resume branch.
- Pointer/list parsing: active filename resolves to an existing file; empty/`standard`/missing
  file → plain claude.
- List mutations: add appends `name:file`; rename changes name only; delete removes the line.
- Delete of the active config resets the pointer to Standard.

Go (`internal/tui/`, `cmd/ghost-tab-tui/`):
- `CycleClaudeConfig` order includes Standard at index 0, wraps around both directions, and
  persists the correct filename (empty for Standard) to the pointer file.
- Config line hidden when tool ≠ claude, shown when claude.
- `claude-config-menu` add/delete emit the expected JSON actions.

Every change: write the test, run it, watch it fail, implement, watch it pass, then run the
full suite (`./run-tests.sh`) and `shellcheck` on modified scripts.

## Affected files

- `wrapper.sh` — read pointer, resolve path, pass to launch builder.
- `lib/tmux-session.sh` — `build_ai_launch_cmd` settings-path arg.
- `lib/menu-tui.sh` — pass config flags to `main-menu`.
- `lib/config-tui.sh` — `manage-claude-configs` action + list/file mutations.
- `internal/tui/mainmenu.go` — config state, `CycleClaudeConfig`, render line, setters.
- `cmd/ghost-tab-tui/main_menu.go` — new flags.
- New `cmd/ghost-tab-tui/claude_config_menu.go` + `internal/tui/claude_config_menu.go`
  (management selector).
- Tests alongside each.
