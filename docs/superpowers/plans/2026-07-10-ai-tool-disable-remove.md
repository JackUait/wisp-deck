# AI Tool Disable/Remove Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the Settings → AI tools panel disable a tool (wisp-deck ignores it) or uninstall opencode/codex from the system behind a warning modal.

**Architecture:** A `~/.config/wisp-deck/disabled-tools` file (one name per line) is the single source of truth, read by bash (`wrapper.sh` filters `AI_TOOLS_AVAILABLE`) and Go (panel overlays `Disabled` on rows). Removal reuses the install seam: a fixed map of tool → bash function, run in the background with the existing progress bar.

**Tech Stack:** bash (lib/*.sh), Go + Bubbletea (internal/tui, internal/models), Go tests in test/bash via os/exec.

## Global Constraints

- TDD: every task writes the failing test first, watches it fail, then implements.
- shellcheck must stay clean on all modified scripts.
- Claude Code: disable yes, remove no. OpenCode/Codex: both.
- Work directly on main; push at the end.

---

### Task 1: bash `filter_disabled_ai_tools` (lib/ai-tools.sh)

**Files:**
- Modify: `lib/ai-tools.sh`
- Test: `test/bash/disabled_tools_test.go` (new)

**Interfaces:**
- Produces: `filter_disabled_ai_tools <disabled_file> <tool>...` — echoes surviving tools one per line. If the file is missing/empty, echoes all. Safety valve: if every tool would be filtered, echoes all tools unchanged and warns on stderr.

- [ ] **Step 1: Write failing tests** — `TestFilterDisabledAITools_removes_listed_tools`, `_missing_file_keeps_all`, `_all_disabled_keeps_all_and_warns` using `runBashFunc(t, "lib/ai-tools.sh", "filter_disabled_ai_tools", ...)`.
- [ ] **Step 2: Run, watch fail** (`go test ./test/bash/ -run TestFilterDisabled -v`).
- [ ] **Step 3: Implement:**

```bash
# Filter a tool list against the disabled-tools file (one name per line).
# Usage: filter_disabled_ai_tools <disabled_file> <tool>...
# Echoes surviving tools one per line. If filtering would leave nothing,
# the disable list is ignored so a launch can never end up tool-less.
filter_disabled_ai_tools() {
  local disabled_file="$1"; shift
  local survivors=() _t
  for _t in "$@"; do
    if [ -f "$disabled_file" ] && grep -qx "$_t" "$disabled_file" 2>/dev/null; then
      continue
    fi
    survivors+=("$_t")
  done
  if [ ${#survivors[@]} -eq 0 ] && [ $# -gt 0 ]; then
    echo "wisp-deck: all AI tools disabled — ignoring disable list" >&2
    printf '%s\n' "$@"
    return 0
  fi
  printf '%s\n' "${survivors[@]}"
}
```

- [ ] **Step 4: Run, watch pass.**
- [ ] **Step 5: Commit** `feat(ai-tools): filter_disabled_ai_tools helper`.

### Task 2: bash `remove_opencode` / `remove_codex` (lib/install.sh)

**Files:**
- Modify: `lib/install.sh`
- Test: `test/bash/disabled_tools_test.go`

**Interfaces:**
- Produces: `remove_codex` / `remove_opencode` — `npm uninstall -g <pkg>`, then delete the `~/.local/bin/<tool>` symlink when it points into `npm prefix -g`. Return 1 when npm is missing or uninstall fails.

- [ ] **Step 1: Failing tests** — uninstall invoked with right pkg; `~/.local/bin/codex` symlink into the npm prefix is deleted; a non-npm `~/.local/bin/codex` (regular file or symlink elsewhere) is left alone; missing npm → return 1 + warn.
- [ ] **Step 2: Watch fail.**
- [ ] **Step 3: Implement** (shared private helper `_remove_npm_tool <tool> <pkg> <display>`):

```bash
# Uninstall an npm-installed tool and clean up the ~/.local/bin launcher link
# ensure_* may have created (only when it points into npm's global prefix —
# never delete a launcher the user put there themselves).
_remove_npm_tool() {
  local tool="$1" pkg="$2" display="$3"
  if ! command -v npm &>/dev/null; then
    warn "npm not found — cannot remove $display"
    return 1
  fi
  info "Removing $display..."
  if ! npm uninstall -g "$pkg" &>/dev/null; then
    warn "Failed to remove $display"
    return 1
  fi
  local link="$HOME/.local/bin/$tool" npm_prefix
  npm_prefix="$(npm prefix -g 2>/dev/null)"
  if [ -L "$link" ] && [ -n "$npm_prefix" ]; then
    case "$(readlink "$link")" in
      "$npm_prefix"/*) rm -f "$link" ;;
    esac
  fi
  success "$display removed"
}

remove_opencode() { _remove_npm_tool "opencode" "opencode-ai" "OpenCode"; }
remove_codex()    { _remove_npm_tool "codex" "@openai/codex" "Codex"; }
```

- [ ] **Step 4: Watch pass.** Step 5: Commit `feat(install): remove_opencode/remove_codex uninstallers`.

### Task 3: wrapper + menu-tui wiring

**Files:**
- Modify: `wrapper.sh:98-101` (filter after building), `lib/menu-tui.sh` (pass `--disabled-tools-file`)
- Test: `test/bash/disabled_tools_test.go` (snippet test simulating wrapper's build+filter)

- [ ] **Step 1: Failing test** — snippet builds `AI_TOOLS_AVAILABLE=(claude codex)`, disabled file lists codex, applies the wrapper's filter line, asserts only claude remains.
- [ ] **Step 3: Implement in wrapper.sh** after line 101:

```bash
WISP_DECK_DISABLED_TOOLS_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/disabled-tools"
if [ ${#AI_TOOLS_AVAILABLE[@]} -gt 0 ]; then
  mapfile -t AI_TOOLS_AVAILABLE < <(filter_disabled_ai_tools "$WISP_DECK_DISABLED_TOOLS_FILE" "${AI_TOOLS_AVAILABLE[@]}")
fi
```

In `lib/menu-tui.sh` after the `--lib-dir` arg:

```bash
cmd_args+=("--disabled-tools-file" "$gt_config_dir/disabled-tools")
```

- [ ] **Step 4: Tests pass + shellcheck.** Step 5: Commit `feat(wrapper): honor disabled-tools file`.

### Task 4: Go disabled-tools persistence (internal/models)

**Files:**
- Create: `internal/models/disabledtools.go`, `internal/models/disabledtools_test.go`

**Interfaces:**
- Produces: `LoadDisabledTools(path string) map[string]bool` (missing file → empty map), `ToggleDisabledTool(path, tool string) (bool, error)` — flips membership, rewrites the file (one name per line, stable order), returns the new disabled state.

- [ ] **Step 1: Failing tests** — load missing file, load two lines, toggle on writes line, toggle off removes it, file created with parent dir.
- [ ] **Step 3: Implement** with `os.ReadFile`/`os.WriteFile` + `strings.Fields`-style line split; `os.MkdirAll(filepath.Dir(path), 0755)` before write.
- [ ] **Step 5: Commit** `feat(models): disabled-tools persistence`.

### Task 5: panel disable toggle (`x`)

**Files:**
- Modify: `internal/tui/ai_tools_panel.go`, `internal/tui/mainmenu.go` (new fields `disabledToolsFile string`), `cmd/wisp-deck-tui/main_menu.go` (flag)
- Test: `internal/tui/ai_tools_panel_test.go`

**Interfaces:**
- Consumes: Task 4's `models.LoadDisabledTools` / `models.ToggleDisabledTool`.
- Produces: `models.AITool.Disabled bool`; rows overlay disabled state in `openAIToolsPanel`/`applyAIToolInstallDone` re-detect; `x` in `updateAIToolsPanel` toggles.

Behavior:
- `x` on any row toggles; persists via `ToggleDisabledTool(m.disabledToolsFile, tool.Name)`.
- Disabled row: name rendered gray, right column gray `disabled` (wins over installed/default tags, but not over an in-flight install bar).
- Disabling the current default: pick the first enabled tool in `m.aiTools`, set `m.selectedAI`, `persistAITool()`.
- `d` on a disabled tool: no-op.
- Help line: `⏎ install · d default · x disable · r remove · esc close`.

- [ ] Steps: failing tests (toggle persists+renders, default falls back, `d` refused on disabled, disabled hidden tag precedence) → fail → implement → pass → commit `feat(settings): disable AI tools from the panel`.

### Task 6: remove modal (`r`)

**Files:**
- Modify: `internal/tui/ai_tools_panel.go`, `internal/models/aitool.go` (if needed)
- Test: `internal/tui/ai_tools_panel_test.go`

**Interfaces:**
- Produces: `removableTools = map[string]string{"opencode": "remove_opencode", "codex": "remove_codex"}`; `removeCmdFor(tool, libDir)` mirroring `installCmdFor`; model fields `aiToolRemovePending string` (modal open for tool), `aiToolRemoving string` + reuse `aiToolInstallPct` ticker for progress; `aiToolRemoveDoneMsg{tool string, err error}`.

Behavior:
- `r` on an installed opencode/codex row (not while an install/remove runs) sets `aiToolRemovePending`.
- Modal replaces the panel list: same `╭─╮` chrome, red bold title `Remove <Display>?`, body: `Runs npm uninstall -g <pkg> — removes it from this system.`, opencode body adds `OpenCode stays launchable via npx; press x to hide it from wisp-deck.`, help `⏎ remove · esc cancel`.
- Enter confirms → background `removeCmdFor(...).Run()` + progress bar on the row (`removing` state renders the same bar as install); Esc cancels.
- Done: re-detect rows; failure → panel error line + feedback, like install.
- `r` on claude or a not-installed tool: no-op.

- [ ] Steps: failing tests (modal opens only for removable installed rows, esc cancels, enter spawns remove & bar shows, done refreshes, failure surfaces, claude refused) → fail → implement → pass → commit `feat(settings): remove OpenCode/Codex from the system behind a warning modal`.

### Task 7: finish

- [ ] shellcheck all scripts; `./run-tests.sh` full suite.
- [ ] Rebuild local TUI binary (Makefile `install` target path: build to `~/.local/bin/wisp-deck-tui` + warm-up exec) so the user sees the feature.
- [ ] `git pull --rebase && git push`, verify up to date.
