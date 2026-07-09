# Adding Codex as an AI tool option

**Date:** 2026-07-10
**Status:** Approved

## Goal

Make OpenAI's `codex` CLI a third selectable AI tool in Wisp Deck, as a peer of
OpenCode: detectable, selectable, launchable, and visually distinct — while
opting out of the Claude-exclusive machinery (statusline, account switch, draft
stash, sound notifications) exactly the way OpenCode does.

## Background

Every per-tool `case` in the codebase uses `claude` as its `default` arm. A new
tool identifier that is not explicitly handled therefore inherits Claude's
launch flags (`--settings`, `env -u CLAUDE_CONFIG_DIR`), Claude's ghost mascot,
and Claude's orange palette. Adding Codex means touching every one of those
branches, not just the tool list.

Separately, `build_ai_launch_cmd` takes the tools' commands as parallel
positional slots (`<tool> <claude_cmd> <opencode_cmd> [extra]`) and selects one
by name internally. That shape has no room for a third tool and degrades with
each addition. It is fixed as part of this work.

## Design

### 1. Detection and enumeration

Codex is detected on `PATH` only (`command -v codex`) — no npx fallback. It is
appended after OpenCode so Claude retains first-available priority.

| Site | Change |
| --- | --- |
| `wrapper.sh` | `CODEX_CMD="$(command -v codex)"`; append `codex` to `AI_TOOLS_AVAILABLE` |
| `internal/models/aitool.go` | `DetectAITools` gains `{Name: "codex", Command: "codex"}` |
| `lib/ai-select-tui.sh` | `priority_order=("claude" "opencode" "codex")` |
| `internal/models/aitool.go` | `String()` and `DisplayName()` map `codex` → `Codex` |
| `internal/tui/mainmenu.go` | `aiToolDisplayNames` gains `codex` → `Codex` |
| `internal/tui/multiselect.go` | `installerToolDisplayName` returns `Codex (openai)` |

`internal/tui/multiselect.go`'s pre-check logic needs no change: Claude is
unconditionally pre-checked and every other tool is pre-checked iff installed,
which is the desired behavior for Codex.

### 2. Launch command

`build_ai_launch_cmd` collapses to `<tool> <tool_cmd> [extra]`. A new pure
helper in `lib/ai-tools.sh` maps identifier to binary:

```bash
resolve_ai_tool_cmd <tool> <claude_cmd> <opencode_cmd> <codex_cmd>
```

Callers resolve the command once and pass a single slot. The per-tool `case`
inside `build_ai_launch_cmd` continues to own the flags:

- `opencode` → `$tool_cmd "$extra"`
- `codex` → `$tool_cmd` (bare; no settings, no env prefix, no positional path)
- `claude`/unknown → `$claude_account$claude_filter$tool_cmd $extra$claude_settings`

Codex needs its own arm rather than the default one because the default arm
prepends `env -u CLAUDE_CONFIG_DIR` and appends `--settings <path>`, neither of
which `codex` accepts.

Panes are created with `-c "$PROJECT_DIR"`, so Codex needs no positional path
argument.

**Resume mode.** `build_ai_launch_cmd`'s resume branch returns
`$tool_cmd --continue` for OpenCode and a guarded `--resume → -c → plain` chain
for Claude. Codex returns a plain launch: it relaunches fresh rather than
resuming. See "Out of scope".

**Ripples.** `wrapper.sh` (the `case` at the launch site, which must gain a
`codex)` arm since the default arm forwards Claude's CLI args), and
`lib/account-switch.sh`'s `build_switch_launch_cmd`, `write_relaunch_context`,
and `_read_relaunch_ctx`. The relaunch file's `claude_cmd=` and `opencode_cmd=`
keys become a single `tool_cmd=`. Relaunch files are per-session and removed by
`cleanup()`, so no format migration is required.

Go's `util.BuildAILaunchCmd` already takes a single `command` parameter and only
needs a `case "codex": return command`. The parallel-slot problem is bash-only.

### 3. Theming

Codex's auto-theme accent is 256-color **36** (`#00af87`), the nearest match to
OpenAI's brand `#10a37f`. It is deliberately not 78 (`green` preset) or 80
(`cyan` preset): reusing a preset's Primary would make Codex's auto theme
indistinguishable from a manually-selected preset.

| Site | Change |
| --- | --- |
| `internal/tui/theme.go` | new `themes["codex"]` entry, Primary 36 |
| `lib/theme.sh` | `gt_resolve_theme` maps `codex` → `teal`; `get_theme_accent`/`get_theme_palette` gain a `teal` key |
| `lib/tmux-session.sh` | `get_tool_accent` returns `36` for `codex` |
| `lib/loading.sh` | `get_tool_palette` returns a teal ramp for `codex` |
| `internal/tui/ghost.go` | `ghostCodex` + `ghostCodexSleeping`; `case "codex"` in `GhostForTheme` |
| `cmd/ghost-width-check/main.go` | include `codex` in the width check |

`teal` is added as a theme *key* but **not** to the user-selectable preset list
(`presetNames`, the Settings picker). Adding a Settings row shifts positional
indices across roughly six files and breaks index-hardcoded tests, for no
benefit — nothing here requires users to pick teal manually.

`ResolveTheme(tool, pref)` routes a named preset through `presetThemesOpencode`
for OpenCode and `presetThemesClaude` otherwise, so a Codex user who picks a
named preset gets the Claude preset table. `ghostCodex` is therefore drawn
against Claude's color-slot semantics (`Cap`, `DarkFeet`, `Blush`, …) so those
presets recolor it correctly.

### 4. Claude-exclusive machinery

No changes required. Every exclusive gate is a positive `[ "$tool" = "claude" ]`
test rather than `!= "opencode"`, so Codex is excluded automatically:

- `wrapper.sh` — statusline hooks, sound notification setup/teardown,
  `WISP_DECK_CLAUDE_SETTINGS`, `WISP_DECK_CLAUDE_ACCOUNT_DIR`,
  `WISP_DECK_CLAUDE_FILTER`, `WISP_DECK_RELAUNCH_FILE`
- `lib/account-switch.sh` — `_relaunch_preserving_draft`'s draft stash
- `lib/session-restore.sh` — dead-sid blanking, duplicate-transcript pinning

`lib/tab-title-watcher.sh`'s `check_ai_tool_state` handles Codex through its
generic `else` branch, which greps the pane for a shell prompt.

**Known limitation.** That `else` branch detects idle by matching `❯` or a shell
prompt. Whether Codex's TUI renders something matchable is unverified — Codex is
not installed on the development machine. If it does not match, the tab title
will not flip to "waiting". This degrades quietly and breaks nothing.

### 5. Installer

`bin/wisp-deck` gains a `codex) ensure_codex ;;` arm. `lib/install.sh` gains
`ensure_codex()`, shaped like `ensure_opencode` minus the npx fallback and the
brew-uninstall step:

1. `codex` already on PATH → success
2. `npm` available → `npm install -g @openai/codex`
3. otherwise → warn that Node.js is required

`bin/wisp-deck`'s closing summary `sed` gains `s/codex/Codex/`.

## Out of scope

- **Usage stats.** `internal/usage/` has per-tool transcript parsers. Codex
  sessions contribute nothing; the aggregator already tolerates a missing
  per-tool data directory.
- **`opencode.json` subscription sync** — OpenCode-specific.
- **Codex session resume.** `codex resume --last` is plausible but unverifiable
  without the binary installed. Codex relaunches fresh on session restore.
- Statusline, account switch, draft stash, auto-switch, idle sound hooks.

## Testing

Test-first, per the repository's IRON RULE. Each change below is preceded by a
failing test.

**Bash (`test/bash/`)**

- `codex_cmd_test.go` (new) — `resolve_ai_tool_cmd` returns the right binary per
  tool and empty for an unknown one
- `tmux_session_test.go` — `build_ai_launch_cmd codex` emits the bare command:
  no `--settings`, no `env -u CLAUDE_CONFIG_DIR`, no positional path; and in
  resume mode emits a plain launch rather than `--continue`
- `theme_test.go` — `gt_resolve_theme codex` → `teal`; `get_theme_accent teal`
  → `36`
- `loading_test.go` — `get_tool_palette codex` returns the teal ramp
- `ai_select_test.go` — priority order picks `claude` over `codex`
- `install_test.go` — `ensure_codex` succeeds when `codex` is on PATH; calls
  `npm install -g @openai/codex` when it is not

**Go**

- `test/internal/models/aitool_test.go` — `DetectAITools` includes `codex`;
  `DisplayName("codex") == "Codex"`
- `test/internal/util/launch_test.go` — `BuildAILaunchCmd("codex", …)` returns
  the bare command
- `test/internal/tui/ghost_test.go` — the Codex shape differs from Claude's and
  uses Primary 36
- `test/internal/tui/theme_test.go` — `ThemeForTool("codex")` is the Codex theme

**Regression.** The `build_ai_launch_cmd` signature collapse touches
`lib/account-switch.sh`, whose behavior is covered by `account_switch_test.go`,
`account_switch_e2e_test.go`, `draft_restore_test.go`, and
`restore_account_test.go`. These must pass unchanged; they are the safety net
for the refactor.

Codex cannot be launched end-to-end on the development machine, so its launch
command is verified by unit test only.
