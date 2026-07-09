# Managing AI tools from the Settings menu

**Date:** 2026-07-10
**Status:** Approved

## Goal

Let a user install OpenCode or Codex, and choose which AI tool is the default for
new sessions, from inside the running Settings menu — instead of only during the
one-time `wisp-deck` setup.

## Background

`ensure_opencode` and `ensure_codex` (`lib/install.sh`) run exactly once, from
`bin/wisp-deck`'s setup flow. A user who skipped a tool there has no in-app way
to add it; the only recourse is re-running setup or `npm install -g` by hand.

Choosing the *default* tool already exists — the main menu's AI-tool row cycles
with ←→ and persists to `~/.config/wisp-deck/ai-tool` — but it can only cycle
tools that were already installed when bash computed `AI_TOOLS_AVAILABLE`.

## Design

### 1. The row and the panel

A new `Tools` section sits between `Appearance` and `Notifications`, holding one
row whose state shows the current default:

```
Tools
    AI tools                              [Claude Code]
```

`↵` opens a panel modelled on the existing Account panel
(`internal/tui/claude_account_menu.go`), listing every *known* tool with its
state:

```
┌─ AI tools ──────────────────────┐
│ ● Claude Code          default  │
│ ● OpenCode          installed   │
│ ○ Codex             ↵ install   │
│                                 │
│ ↵ install   d default   esc     │
└─────────────────────────────────┘
```

- `↑`/`↓` move the cursor
- `↵` installs the highlighted tool when it is missing; no-op when installed
- `d` makes the highlighted tool the default; only permitted when installed
- `esc` closes

Claude Code is listed but never installable here: its installer is
`curl -fsSL … | bash`, which is not something to run from inside a TUI. It shows
state only. OpenCode and Codex are both plain `npm install -g`.

The panel follows the established modal conventions: a `aiToolsPanelOpen` field
intercepted in `Update` before focus handling (alongside `modelMapOpen` and
`accountMenuOpen`), and appended via `appendModal` in the `TabSettings` render
branch.

### 2. Settings row indices

Settings rows are keyed by integer index. `settingsEnter` and
`settingsValueRight` hardcode `case 6:` (Subscription) and `case 7:` (Account),
while `loginRowIndex()` and `autoSwitchRowIndex()` compute
`settingsItemCount()-2` and `-1`. These agree only because `ClaudeConfigVisible()`
unconditionally returns `true`, pinning the count at 9.

Appending a tenth row makes `loginRowIndex()` evaluate to 8, colliding with
Auto-switch — `↵` on Account would toggle auto-switch instead. So before adding
the row, replace the arithmetic with named constants:

```go
const (
    rowMascot         = 0
    rowTabTitle       = 1
    rowIdleSound      = 2
    rowTheme          = 3
    rowProjectsFolder = 4
    rowUsageBars      = 5
    rowSubscription   = 6
    rowAccount        = 7
    rowAutoSwitch     = 8
    rowAITools        = 9
)
```

`loginRowIndex()` and `autoSwitchRowIndex()` become thin accessors returning
`rowAccount` / `rowAutoSwitch`, so existing call sites do not churn.
`settingsItemCount()` returns 10.

This is the targeted fix that makes the feature safe — not unrelated
refactoring.

### 3. Running the install

The menu is a long-running Bubbletea app and `npm install -g` takes tens of
seconds. The codebase already has a pattern for this: `worktree add` returns a
`tea.Cmd` closure that shells out and posts a done-message, with the result
surfaced through `feedbackMsg` (`internal/tui/mainmenu.go`, `worktreeDoneMsg`).
This design follows it exactly — no `tea.ExecProcess`, no terminal handoff. The
UI stays responsive and the row reads `installing…` in the meantime.

The install shells into the **existing bash installers** rather than
reimplementing them:

```
bash -c 'source "$LIB/tui.sh" && source "$LIB/install.sh" && ensure_codex'
```

`ensure_opencode` and `ensure_codex` already handle the npm/Node fallbacks and
already have test coverage. Reimplementing `npm install -g @openai/codex` in Go
would create two sources of truth that drift.

`$LIB` comes from a new `--lib-dir` flag passed by `lib/menu-tui.sh`, falling
back to the `WISP_DECK_LIB_DIR` the session already exports. Command
construction is factored into a pure, testable function:

```go
func installCmdFor(tool, libDir string) (*exec.Cmd, error)
```

It returns an error for `claude` and for unknown tools, so "not installable" is
enforced in one place rather than only in the key handler.

### 4. After a successful install

On `aiToolInstallDoneMsg`:

- re-run `models.DetectAITools()` and refresh the panel's rows
- append the newly-installed tool to the model's `aiTools` slice

The append matters. The main menu is the project selector shown *before* tmux
launches, so a tool installed here is immediately selectable for the session
about to start. Without it, the AI-tool row would still only cycle the tools
bash detected at startup.

Failures set `feedbackMsg` to `Failed to install Codex: <err>` and leave the
tool marked not-installed.

### 5. Setting the default

`d` on an installed row points `m.selectedAI` at that tool and reuses the
existing persistence path (`m.aiToolFile`) — the same writer the AI-tool row
uses. `d` on a not-installed row is a no-op.

## Out of scope

- Uninstalling or removing a tool
- Installing Claude Code from the panel
- Per-project tool overrides

## Testing

Test-first, per the repository's IRON RULE.

**Go (`internal/tui`, `test/internal/tui`)**

- row constants are distinct and `settingsItemCount()` equals the highest + 1
- `settingsSections()` contains `rowAITools` under a `Tools` section
- `↵` on Account still opens the account panel after the constants change
  (the collision this design exists to prevent)
- panel: open/close, cursor bounds, `esc`
- `d` sets the default only for an installed tool; no-op otherwise
- `↵` issues an install command only when the tool is missing
- `aiToolInstallDoneMsg` on success refreshes rows, appends to `aiTools`, sets
  feedback; on error sets error feedback and leaves state unchanged
- `installCmdFor`: correct `bash -c` invocation per tool; error for `claude` and
  unknown tools; honours `libDir`

**Bash**

- `ensure_codex` / `ensure_opencode` are already covered
  (`test/bash/codex_setup_test.go`, `test/bash/install_test.go`)
- `menu-tui.sh` passes `--lib-dir`

**Regression**

The constants change touches the index-hardcoded settings tests across roughly
six files. They must pass unchanged in behavior.
