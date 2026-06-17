# Claude Config Switching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user keep several named Claude Code settings files and switch the active one at launch from Ghost Tab's main menu, remembering the last-used choice.

**Architecture:** A "config" is a settings JSON launched via `claude --settings <file>`. Configs live in `~/.config/ghost-tab/claude-configs/`, named in `claude-configs.list` (`name:file`), with the active filename stored in the `claude-config` pointer. `wrapper.sh` reads the pointer and exports `GHOST_TAB_CLAUDE_SETTINGS`; `build_ai_launch_cmd` appends `--settings` when that env var is set. Selection is a new "Claude Config" row in the main menu's settings sub-view (mirrors the Sound cycle), shown only when the AI tool is claude. Management (add/rename/delete) is a new `claude-config-menu` reachable from the config menu.

**Tech Stack:** Bash (sourced `lib/*.sh` modules), Go + Bubbletea (`internal/tui`, `cmd/ghost-tab-tui`), Go-based bash integration tests (`test/bash/`).

## Global Constraints

- Shell: `set -e` scripts, always quote variables, `[[ ]]` for conditionals, `$()` not backticks. Run `shellcheck` on every modified script; fix ALL warnings.
- TDD is mandatory (project IRON RULE): write the test, run it, watch it FAIL, then implement, watch it PASS. Bug/feature code written before its test must be deleted and redone.
- Config root: `${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab`.
- "Standard Claude" is a virtual entry (no file); active pointer empty or `standard` → plain `claude`, no `--settings`.
- Claude-only feature. Other tools (codex/copilot/opencode) unaffected.
- Bash tests use helpers in `test/bash/helpers_test.go` (`runBashFunc`, `runBashSnippet`, `writeTempFile`, `assertContains`, `assertNotContains`, `assertExitCode`, `t.TempDir()`).
- Go tests live alongside source as `*_test.go`; run with `go test ./...`.
- Full verification before done: `./run-tests.sh`, `shellcheck` on modified scripts, then `git push`.

---

## File Structure

- `lib/claude-configs.sh` — **new.** Pure storage/parse helpers: list parsing, active pointer get/set, path resolution, add/rename/delete, slugify. One responsibility: claude-config file management.
- `lib/tmux-session.sh` — **modify.** `build_ai_launch_cmd` appends `--settings` from `GHOST_TAB_CLAUDE_SETTINGS`.
- `wrapper.sh` — **modify.** Resolve active config path, export `GHOST_TAB_CLAUDE_SETTINGS`.
- `lib/menu-tui.sh` — **modify.** Pass `--claude-config-file` / `--claude-configs-list` to `main-menu`.
- `internal/tui/mainmenu.go` — **modify.** Config state, `CycleClaudeConfig`, settings-row render, nav-count helper, setters.
- `cmd/ghost-tab-tui/main_menu.go` — **modify.** New flags wired to setters.
- `internal/tui/claude_config_menu.go` — **new.** Bubbletea model for the management menu (list + add/rename/delete intent), mirrors `internal/tui/terminal_selector.go`.
- `cmd/ghost-tab-tui/claude_config_menu.go` — **new.** Cobra subcommand wrapping the model, emits JSON.
- `lib/config-tui.sh` — **modify.** New `manage-claude-configs` action dispatching to `claude-config-menu` and applying mutations via `lib/claude-configs.sh`.
- Tests: `test/bash/claude_configs_test.go`, `test/bash/tmux_session_settings_test.go`, `test/bash/wrapper_claude_config_test.go`, `internal/tui/mainmenu_claudeconfig_test.go`, `cmd/ghost-tab-tui/cmd_test.go` (extend).

---

## Task 1: `build_ai_launch_cmd` appends `--settings` from env

**Files:**
- Modify: `lib/tmux-session.sh` (function `build_ai_launch_cmd`, lines ~9-46)
- Test: `test/bash/tmux_session_settings_test.go` (new)

**Interfaces:**
- Consumes: existing `build_ai_launch_cmd <tool> <claude_cmd> <codex_cmd> <copilot_cmd> <opencode_cmd> [extra]`.
- Produces: when env `GHOST_TAB_CLAUDE_SETTINGS` is a non-empty path AND the resolved tool command is claude (normal or resume branch), the emitted command contains `--settings "<path>"`. No change for non-claude tools or when the env var is empty/unset.

- [ ] **Step 1: Write the failing test**

```go
package bash_test

import (
	"path/filepath"
	"testing"
)

func TestBuildAILaunchCmd_appends_settings_for_claude(t *testing.T) {
	env := []string{"GHOST_TAB_CLAUDE_SETTINGS=/cfg/work.json"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "codex", "copilot", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `claude /proj --settings "/cfg/work.json"`)
}

func TestBuildAILaunchCmd_no_settings_when_env_empty(t *testing.T) {
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "codex", "copilot", "opencode", "/proj"}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "--settings")
}

func TestBuildAILaunchCmd_settings_on_resume(t *testing.T) {
	env := []string{"GHOST_TAB_RESUME=1", "GHOST_TAB_CLAUDE_SETTINGS=/cfg/work.json"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude", "codex", "copilot", "opencode"}, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, `claude -c --settings "/cfg/work.json"`)
}

func TestBuildAILaunchCmd_settings_ignored_for_codex(t *testing.T) {
	env := []string{"GHOST_TAB_CLAUDE_SETTINGS=/cfg/work.json"}
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"codex", "claude", "codex", "copilot", "opencode", "/proj"}, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "--settings")
	_ = filepath.Separator
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestBuildAILaunchCmd -v`
Expected: FAIL (settings tests find no `--settings` in output).

- [ ] **Step 3: Implement the change**

In `lib/tmux-session.sh`, at the top of `build_ai_launch_cmd` after `local extra="$*"`, add a settings suffix and append it only to claude branches:

```bash
build_ai_launch_cmd() {
  local tool="$1" claude_cmd="$2" codex_cmd="$3" copilot_cmd="$4" opencode_cmd="$5"
  shift 5
  local extra="$*"

  # Claude-only: append --settings when a config is active.
  local claude_settings=""
  if [ -n "${GHOST_TAB_CLAUDE_SETTINGS:-}" ]; then
    claude_settings=" --settings \"${GHOST_TAB_CLAUDE_SETTINGS}\""
  fi

  # Resume mode: relaunch into the most recent (cwd-scoped) conversation.
  if [ "${GHOST_TAB_RESUME:-0}" = "1" ]; then
    case "$tool" in
      codex)    echo "$codex_cmd resume --last" ;;
      copilot)  echo "$copilot_cmd --continue" ;;
      opencode) echo "$opencode_cmd --continue" ;;
      *)        echo "$claude_cmd -c${claude_settings}" ;;
    esac
    return 0
  fi

  case "$tool" in
    codex)
      echo "$codex_cmd --cd \"$extra\""
      ;;
    copilot)
      echo "$copilot_cmd"
      ;;
    opencode)
      echo "$opencode_cmd \"$extra\""
      ;;
    *)
      if [ -n "$extra" ]; then
        echo "$claude_cmd $extra${claude_settings}"
      else
        echo "$claude_cmd${claude_settings}"
      fi
      ;;
  esac
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestBuildAILaunchCmd -v`
Expected: PASS (all four).

- [ ] **Step 5: shellcheck + commit**

```bash
shellcheck lib/tmux-session.sh
git add lib/tmux-session.sh test/bash/tmux_session_settings_test.go
git commit -m "feat(claude-config): pass --settings via GHOST_TAB_CLAUDE_SETTINGS"
```

---

## Task 2: `lib/claude-configs.sh` storage + parse helpers

**Files:**
- Create: `lib/claude-configs.sh`
- Test: `test/bash/claude_configs_test.go` (new)

**Interfaces:**
- Produces (all read config root / files via args, no globals):
  - `load_claude_configs <list_file>` → prints valid `name:file` lines (skips blanks + `#`).
  - `get_active_claude_config <pointer_file>` → prints active filename, or empty.
  - `set_active_claude_config <pointer_file> <filename>` → writes filename; empty or `standard` removes the file.
  - `resolve_claude_config_path <configs_dir> <pointer_file>` → prints absolute path iff the active file exists in `configs_dir`, else empty.
  - `slugify <name>` → lowercase, non-alnum→`-`, collapsed, trimmed.
  - `add_claude_config <list_file> <configs_dir> <name>` → creates `<slug>.json` containing `{}`, appends `name:<slug>.json`, prints the filename. On filename collision appends `-2`, `-3`, …
  - `rename_claude_config <list_file> <file> <new_name>` → rewrites the matching line's name only.
  - `delete_claude_config <list_file> <configs_dir> <pointer_file> <file>` → removes file + list line; if `file` was active, clears pointer.

- [ ] **Step 1: Write the failing test**

```go
package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadClaudeConfigs_skips_comments_blanks(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "list", "# header\n\nWork:work.json\nPersonal:personal.json\n")
	out, code := runBashFunc(t, "lib/claude-configs.sh", "load_claude_configs",
		[]string{filepath.Join(dir, "list")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work:work.json")
	assertContains(t, out, "Personal:personal.json")
	assertNotContains(t, out, "header")
}

func TestActivePointer_get_set_and_standard_clears(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	if _, code := runBashFunc(t, "lib/claude-configs.sh", "set_active_claude_config",
		[]string{ptr, "work.json"}, nil); code != 0 {
		t.Fatalf("set failed")
	}
	out, _ := runBashFunc(t, "lib/claude-configs.sh", "get_active_claude_config", []string{ptr}, nil)
	assertContains(t, out, "work.json")
	if _, code := runBashFunc(t, "lib/claude-configs.sh", "set_active_claude_config",
		[]string{ptr, "standard"}, nil); code != 0 {
		t.Fatalf("set standard failed")
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("pointer should be removed for standard")
	}
}

func TestResolveClaudeConfigPath_existing_vs_missing(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, cfgDir, "work.json", "{}")
	ptr := filepath.Join(dir, "claude-config")
	writeTempFile(t, dir, "claude-config", "work.json")
	out, _ := runBashFunc(t, "lib/claude-configs.sh", "resolve_claude_config_path",
		[]string{cfgDir, ptr}, nil)
	if strings.TrimSpace(out) != filepath.Join(cfgDir, "work.json") {
		t.Fatalf("got %q", out)
	}
	writeTempFile(t, dir, "claude-config", "missing.json")
	out2, _ := runBashFunc(t, "lib/claude-configs.sh", "resolve_claude_config_path",
		[]string{cfgDir, ptr}, nil)
	if strings.TrimSpace(out2) != "" {
		t.Fatalf("expected empty for missing file, got %q", out2)
	}
}

func TestAddClaudeConfig_creates_file_and_list_line(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	list := filepath.Join(dir, "list")
	out, code := runBashFunc(t, "lib/claude-configs.sh", "add_claude_config",
		[]string{list, cfgDir, "My Work"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "my-work.json" {
		t.Fatalf("got filename %q", out)
	}
	data, _ := os.ReadFile(filepath.Join(cfgDir, "my-work.json"))
	if strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("file should contain {}")
	}
	listData, _ := os.ReadFile(list)
	assertContains(t, string(listData), "My Work:my-work.json")
}

func TestDeleteClaudeConfig_active_resets_pointer(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	_ = os.MkdirAll(cfgDir, 0o755)
	writeTempFile(t, cfgDir, "work.json", "{}")
	list := filepath.Join(dir, "list")
	writeTempFile(t, dir, "list", "Work:work.json\n")
	ptr := filepath.Join(dir, "claude-config")
	writeTempFile(t, dir, "claude-config", "work.json")
	if _, code := runBashFunc(t, "lib/claude-configs.sh", "delete_claude_config",
		[]string{list, cfgDir, ptr, "work.json"}, nil); code != 0 {
		t.Fatal("delete failed")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "work.json")); !os.IsNotExist(err) {
		t.Fatal("config file should be gone")
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatal("pointer should be cleared when active config deleted")
	}
	listData, _ := os.ReadFile(list)
	assertNotContains(t, string(listData), "work.json")
}

func TestRenameClaudeConfig_changes_name_only(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "list")
	writeTempFile(t, dir, "list", "Work:work.json\n")
	if _, code := runBashFunc(t, "lib/claude-configs.sh", "rename_claude_config",
		[]string{list, "work.json", "Day Job"}, nil); code != 0 {
		t.Fatal("rename failed")
	}
	listData, _ := os.ReadFile(list)
	assertContains(t, string(listData), "Day Job:work.json")
	assertNotContains(t, string(listData), "Work:work.json")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run "TestLoadClaudeConfigs|TestActivePointer|TestResolveClaudeConfigPath|TestAddClaudeConfig|TestDeleteClaudeConfig|TestRenameClaudeConfig" -v`
Expected: FAIL (module/functions don't exist).

- [ ] **Step 3: Create `lib/claude-configs.sh`**

```bash
#!/bin/bash
# Claude config helpers — pure, no side effects on source.
# A "config" is a settings JSON launched via `claude --settings <file>`.
# Storage: <root>/claude-configs/<file>.json, named in <root>/claude-configs.list
# (name:file), with active filename in <root>/claude-config.

# load_claude_configs <list_file> — prints valid name:file lines (skips blanks/comments).
load_claude_configs() {
  local file="$1" line
  [ ! -f "$file" ] && return 0
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    echo "$line"
  done < "$file"
}

# get_active_claude_config <pointer_file> — prints active filename or empty.
get_active_claude_config() {
  local file="$1" line
  [ -f "$file" ] || return 0
  IFS= read -r line < "$file" || true
  line="${line//[[:space:]]/}"
  [ "$line" = "standard" ] && return 0
  printf '%s\n' "$line"
}

# set_active_claude_config <pointer_file> <filename> — empty/standard removes the file.
set_active_claude_config() {
  local file="$1" filename="$2"
  if [ -z "$filename" ] || [ "$filename" = "standard" ]; then
    rm -f "$file"
    return 0
  fi
  mkdir -p "$(dirname "$file")"
  printf '%s\n' "$filename" > "$file"
}

# resolve_claude_config_path <configs_dir> <pointer_file> — abs path iff active file exists.
resolve_claude_config_path() {
  local configs_dir="$1" pointer_file="$2" active
  active="$(get_active_claude_config "$pointer_file")"
  [ -z "$active" ] && return 0
  local path="$configs_dir/$active"
  [ -f "$path" ] && printf '%s\n' "$path"
}

# slugify <name> — lowercase, non-alnum to single dashes, trimmed.
slugify() {
  local s="$1"
  s="$(printf '%s' "$s" | tr '[:upper:]' '[:lower:]')"
  s="$(printf '%s' "$s" | tr -c 'a-z0-9' '-')"
  s="$(printf '%s' "$s" | tr -s '-')"
  s="${s#-}"
  s="${s%-}"
  printf '%s' "$s"
}

# add_claude_config <list_file> <configs_dir> <name> — creates <slug>.json ({}), appends
# name:file to list, prints filename. Resolves filename collisions with -2, -3, ...
add_claude_config() {
  local list_file="$1" configs_dir="$2" name="$3"
  local slug base file n
  slug="$(slugify "$name")"
  [ -z "$slug" ] && slug="config"
  base="$slug"
  file="$base.json"
  n=2
  while [ -e "$configs_dir/$file" ]; do
    file="$base-$n.json"
    n=$((n + 1))
  done
  mkdir -p "$configs_dir"
  printf '{}\n' > "$configs_dir/$file"
  mkdir -p "$(dirname "$list_file")"
  printf '%s:%s\n' "$name" "$file" >> "$list_file"
  printf '%s' "$file"
}

# rename_claude_config <list_file> <file> <new_name> — rewrites the matching line's name.
rename_claude_config() {
  local list_file="$1" file="$2" new_name="$3" line name f tmp
  [ -f "$list_file" ] || return 0
  tmp="$(mktemp)"
  while IFS= read -r line; do
    name="${line%%:*}"
    f="${line#*:}"
    if [ "$f" = "$file" ]; then
      printf '%s:%s\n' "$new_name" "$file" >> "$tmp"
    else
      printf '%s\n' "$line" >> "$tmp"
    fi
  done < "$list_file"
  mv "$tmp" "$list_file"
}

# delete_claude_config <list_file> <configs_dir> <pointer_file> <file> — remove file + line;
# clear pointer if it was active.
delete_claude_config() {
  local list_file="$1" configs_dir="$2" pointer_file="$3" file="$4" line f tmp active
  rm -f "$configs_dir/$file"
  if [ -f "$list_file" ]; then
    tmp="$(mktemp)"
    while IFS= read -r line; do
      f="${line#*:}"
      [ "$f" = "$file" ] && continue
      printf '%s\n' "$line" >> "$tmp"
    done < "$list_file"
    mv "$tmp" "$list_file"
  fi
  active="$(get_active_claude_config "$pointer_file")"
  [ "$active" = "$file" ] && set_active_claude_config "$pointer_file" ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run "TestLoadClaudeConfigs|TestActivePointer|TestResolveClaudeConfigPath|TestAddClaudeConfig|TestDeleteClaudeConfig|TestRenameClaudeConfig" -v`
Expected: PASS (all).

- [ ] **Step 5: shellcheck + commit**

```bash
shellcheck lib/claude-configs.sh
git add lib/claude-configs.sh test/bash/claude_configs_test.go
git commit -m "feat(claude-config): add claude-configs storage helpers"
```

---

## Task 3: Wire `wrapper.sh` to export `GHOST_TAB_CLAUDE_SETTINGS`

**Files:**
- Modify: `wrapper.sh` (libs list at line ~42; settings resolution before the `build_ai_launch_cmd` block ~217)
- Test: `test/bash/wrapper_claude_config_test.go` (new)

**Interfaces:**
- Consumes: `resolve_claude_config_path` (Task 2), `build_ai_launch_cmd` env contract (Task 1).
- Produces: when `SELECTED_AI_TOOL=claude` and an active config file exists, `GHOST_TAB_CLAUDE_SETTINGS` holds its absolute path; otherwise it is empty/unset.

- [ ] **Step 1: Write the failing test**

This tests the resolution snippet in isolation (the env-export logic), since `wrapper.sh` as a whole launches tmux.

```go
package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapperResolvesActiveConfigForClaude(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	_ = os.MkdirAll(cfgDir, 0o755)
	writeTempFile(t, cfgDir, "work.json", "{}")
	writeTempFile(t, dir, "claude-config", "work.json")

	script := `
source lib/claude-configs.sh
SELECTED_AI_TOOL=claude
GT_CONFIG_DIR="` + dir + `"
GHOST_TAB_CLAUDE_SETTINGS=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  GHOST_TAB_CLAUDE_SETTINGS="$(resolve_claude_config_path "$GT_CONFIG_DIR/claude-configs" "$GT_CONFIG_DIR/claude-config")"
fi
echo "RESULT=$GHOST_TAB_CLAUDE_SETTINGS"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	if !strings.Contains(out, "RESULT="+filepath.Join(cfgDir, "work.json")) {
		t.Fatalf("got %q", out)
	}
}

func TestWrapperNoConfigWhenStandard(t *testing.T) {
	dir := t.TempDir()
	script := `
source lib/claude-configs.sh
SELECTED_AI_TOOL=claude
GT_CONFIG_DIR="` + dir + `"
GHOST_TAB_CLAUDE_SETTINGS="$(resolve_claude_config_path "$GT_CONFIG_DIR/claude-configs" "$GT_CONFIG_DIR/claude-config")"
echo "RESULT=[$GHOST_TAB_CLAUDE_SETTINGS]"
`
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "RESULT=[]")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestWrapper -v`
Expected: FAIL (`runBashSnippet` can't `source lib/claude-configs.sh` until Task 2 exists — Task 2 must be merged first; if so this FAILs only because wrapper wiring/asserts differ). If Task 2 is present, write the test to assert the wrapper export line below; run and watch fail before editing `wrapper.sh`.

- [ ] **Step 3: Edit `wrapper.sh`**

Add `claude-configs` to the libs array (line ~42):

```bash
_gt_libs=(ai-tools projects process input tui menu-tui project-actions tmux-session settings-json notification-setup tab-title-watcher terminals/registry terminals/adapter session-restore claude-configs)
```

Before the `# Build the AI tool launch command` block (~line 217), resolve and export the active config:

```bash
# Resolve active Claude config (settings file) and export for build_ai_launch_cmd.
GHOST_TAB_CLAUDE_SETTINGS=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  _gt_cfg_root="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"
  GHOST_TAB_CLAUDE_SETTINGS="$(resolve_claude_config_path "$_gt_cfg_root/claude-configs" "$_gt_cfg_root/claude-config")"
fi
export GHOST_TAB_CLAUDE_SETTINGS
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/bash/... -run TestWrapper -v`
Expected: PASS.

- [ ] **Step 5: shellcheck + commit**

```bash
shellcheck wrapper.sh
git add wrapper.sh test/bash/wrapper_claude_config_test.go
git commit -m "feat(claude-config): wrapper exports active settings path"
```

---

## Task 4: Main-menu model — config state, cycle, setters

**Files:**
- Modify: `internal/tui/mainmenu.go`
- Test: `internal/tui/mainmenu_claudeconfig_test.go` (new)

**Interfaces:**
- Produces (on `*MainMenuModel`):
  - type `ClaudeConfig struct { Name, File string }`
  - `SetClaudeConfigs(configs []ClaudeConfig)` — stores list; selected index resolved from the active pointer (set via `SetActiveClaudeConfig`).
  - `SetClaudeConfigFile(path string)` and `SetActiveClaudeConfig(file string)` — pointer path + initial active filename; selects matching index (0 = Standard if empty/no match).
  - `CurrentClaudeConfigName() string` — "Standard Claude" at index 0, else config name.
  - `CurrentClaudeConfigFile() string` — "" for Standard, else filename.
  - `CycleClaudeConfig(direction string)` — "next"/"prev" over `[Standard, configs...]`, wraps, persists filename ("" for Standard) to the pointer file.
  - `ClaudeConfigVisible() bool` — true iff `CurrentAITool() == "claude"`.

- [ ] **Step 1: Write the failing test**

```go
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func newClaudeMenu(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude", "codex"}, "claude", "none")
	m.SetClaudeConfigFile(ptr)
	m.SetClaudeConfigs([]ClaudeConfig{{Name: "Work", File: "work.json"}, {Name: "Personal", File: "personal.json"}})
	m.SetActiveClaudeConfig("")
	return m, ptr
}

func TestClaudeConfig_starts_standard(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if m.CurrentClaudeConfigName() != "Standard Claude" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
	if m.CurrentClaudeConfigFile() != "" {
		t.Fatalf("standard should have empty file")
	}
}

func TestClaudeConfig_cycle_wraps_and_persists(t *testing.T) {
	m, ptr := newClaudeMenu(t)
	m.CycleClaudeConfig("next") // Work
	if m.CurrentClaudeConfigName() != "Work" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
	data, _ := os.ReadFile(ptr)
	if strings.TrimSpace(string(data)) != "work.json" {
		t.Fatalf("pointer = %q", string(data))
	}
	m.CycleClaudeConfig("next") // Personal
	m.CycleClaudeConfig("next") // wrap to Standard
	if m.CurrentClaudeConfigName() != "Standard Claude" {
		t.Fatalf("expected wrap to Standard, got %q", m.CurrentClaudeConfigName())
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("standard should clear pointer")
	}
}

func TestClaudeConfig_prev_from_standard_to_last(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.CycleClaudeConfig("prev")
	if m.CurrentClaudeConfigName() != "Personal" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
}

func TestClaudeConfig_active_preselected(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.SetActiveClaudeConfig("personal.json")
	if m.CurrentClaudeConfigName() != "Personal" {
		t.Fatalf("got %q", m.CurrentClaudeConfigName())
	}
}

func TestClaudeConfig_visibility_follows_tool(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if !m.ClaudeConfigVisible() {
		t.Fatal("should be visible for claude")
	}
	m.CycleAITool("next") // -> codex
	if m.ClaudeConfigVisible() {
		t.Fatal("should hide for non-claude")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestClaudeConfig -v`
Expected: FAIL (undefined `ClaudeConfig`, `SetClaudeConfigs`, etc).

- [ ] **Step 3: Implement state + methods in `internal/tui/mainmenu.go`**

Add the type near the top-level types and fields to the struct (next to `soundFile string`, ~line 221):

```go
// ClaudeConfig is one selectable Claude settings file (display name + filename).
type ClaudeConfig struct {
	Name string
	File string
}
```

Struct fields (add inside `MainMenuModel`):

```go
	claudeConfigs       []ClaudeConfig // Standard is implicit index 0, not stored here
	selectedConfig      int            // 0 = Standard, 1.. = claudeConfigs[i-1]
	claudeConfigFile    string         // pointer file path for persistence
```

Methods (add after the sound methods, ~line 700):

```go
// SetClaudeConfigFile sets the pointer file path used to persist the active config.
func (m *MainMenuModel) SetClaudeConfigFile(path string) { m.claudeConfigFile = path }

// SetClaudeConfigs stores the managed config list (excluding the implicit Standard).
func (m *MainMenuModel) SetClaudeConfigs(configs []ClaudeConfig) { m.claudeConfigs = configs }

// SetActiveClaudeConfig selects the entry matching filename ("" or no match = Standard).
func (m *MainMenuModel) SetActiveClaudeConfig(file string) {
	m.selectedConfig = 0
	if file == "" {
		return
	}
	for i, c := range m.claudeConfigs {
		if c.File == file {
			m.selectedConfig = i + 1
			return
		}
	}
}

// CurrentClaudeConfigName returns the active config's display name.
func (m *MainMenuModel) CurrentClaudeConfigName() string {
	if m.selectedConfig <= 0 || m.selectedConfig > len(m.claudeConfigs) {
		return "Standard Claude"
	}
	return m.claudeConfigs[m.selectedConfig-1].Name
}

// CurrentClaudeConfigFile returns the active config's filename ("" for Standard).
func (m *MainMenuModel) CurrentClaudeConfigFile() string {
	if m.selectedConfig <= 0 || m.selectedConfig > len(m.claudeConfigs) {
		return ""
	}
	return m.claudeConfigs[m.selectedConfig-1].File
}

// ClaudeConfigVisible reports whether the config control should be shown.
func (m *MainMenuModel) ClaudeConfigVisible() bool { return m.CurrentAITool() == "claude" }

// CycleClaudeConfig moves through [Standard, configs...] and persists the choice.
func (m *MainMenuModel) CycleClaudeConfig(direction string) {
	n := len(m.claudeConfigs) + 1 // +1 for Standard
	if direction == "prev" {
		m.selectedConfig = (m.selectedConfig - 1 + n) % n
	} else {
		m.selectedConfig = (m.selectedConfig + 1) % n
	}
	m.persistClaudeConfig()
}

// persistClaudeConfig writes the active filename ("" clears) to the pointer file.
func (m *MainMenuModel) persistClaudeConfig() {
	if m.claudeConfigFile == "" {
		return
	}
	file := m.CurrentClaudeConfigFile()
	if file == "" {
		_ = os.Remove(m.claudeConfigFile)
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.claudeConfigFile), 0755)
	_ = os.WriteFile(m.claudeConfigFile, []byte(file+"\n"), 0644)
}
```

(`os` and `filepath` are already imported in `mainmenu.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestClaudeConfig -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/mainmenu.go internal/tui/mainmenu_claudeconfig_test.go
git commit -m "feat(claude-config): main-menu config state and cycle"
```

---

## Task 5: Render config row in settings sub-view + flags + bash plumbing

**Files:**
- Modify: `internal/tui/mainmenu.go` (settings render ~2227, `updateSettings` ~1441, nav counts)
- Modify: `cmd/ghost-tab-tui/main_menu.go` (flags)
- Modify: `lib/menu-tui.sh` (pass flags)
- Test: `internal/tui/mainmenu_claudeconfig_test.go` (extend — render + nav)

**Interfaces:**
- Consumes: Task 4 model methods.
- Produces: a "Claude Config" settings row (index 4) shown only when `ClaudeConfigVisible()`; ←/→/Enter cycle it. New `main-menu` flags `--claude-config-file`, `--claude-configs-list`. `menu-tui.sh` passes both.

- [ ] **Step 1: Write the failing test (render + nav count)**

Append to `internal/tui/mainmenu_claudeconfig_test.go`:

```go
func TestSettings_shows_config_row_for_claude(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.OpenSettings() // enters settings mode (helper added in this task)
	view := m.renderSettingsForTest()
	if !strings.Contains(view, "Claude Config") {
		t.Fatalf("settings should show Claude Config row:\n%s", view)
	}
	if !strings.Contains(view, "Standard Claude") {
		t.Fatalf("should show current config name")
	}
}

func TestSettings_hides_config_row_for_non_claude(t *testing.T) {
	m, _ := newClaudeMenu(t)
	m.CycleAITool("next") // codex
	m.OpenSettings()
	view := m.renderSettingsForTest()
	if strings.Contains(view, "Claude Config") {
		t.Fatalf("config row must be hidden for non-claude:\n%s", view)
	}
}

func TestSettings_nav_count_includes_config_when_visible(t *testing.T) {
	m, _ := newClaudeMenu(t)
	if got := m.settingsItemCount(); got != 5 {
		t.Fatalf("claude should have 5 settings items, got %d", got)
	}
	m.CycleAITool("next")
	if got := m.settingsItemCount(); got != 4 {
		t.Fatalf("non-claude should have 4 settings items, got %d", got)
	}
}
```

Add small test helpers at the end of the file (test-only accessors):

```go
func (m *MainMenuModel) renderSettingsForTest() string { return m.renderSettings() }
```

(If `renderSettings` has a different name/signature, point the helper at the real method; keep the helper test-only.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestSettings_ -v`
Expected: FAIL (`OpenSettings`, `settingsItemCount`, config row undefined).

- [ ] **Step 3: Implement**

(a) Nav-count helper + replace hardcoded `4`/`n=4` in `updateSettings` with it. Add near `SettingsSelected` (~860):

```go
// settingsItemCount returns the number of settings rows (5 when the Claude
// config row is visible, otherwise 4).
func (m *MainMenuModel) settingsItemCount() int {
	if m.ClaudeConfigVisible() {
		return 5
	}
	return 4
}

// OpenSettings enters settings mode (test/utility entry point).
func (m *MainMenuModel) OpenSettings() { m.settingsMode = true; m.settingsSelected = 0 }
```

In `updateSettings` (lines ~1441-1519) replace each `const n = 4` / `% 4` with `m.settingsItemCount()`:

```go
	case tea.KeyUp:
		n := m.settingsItemCount()
		m.settingsSelected = (m.settingsSelected - 1 + n) % n
		return m, nil
	case tea.KeyDown:
		n := m.settingsItemCount()
		m.settingsSelected = (m.settingsSelected + 1) % n
		return m, nil
```

In the same function's `KeyEnter`, `KeyRight`, `KeyLeft` switches add `case 4` for the config (only reachable when visible, since nav can't land on 4 otherwise):

```go
	// inside KeyEnter switch:
		case 4:
			m.CycleClaudeConfig("next")
	// inside KeyRight switch:
		case 4:
			m.CycleClaudeConfig("next")
	// inside KeyLeft switch:
		case 4:
			m.CycleClaudeConfig("prev")
```

And the `j`/`k` rune handlers: replace `% 4` and `const n = 4` with `m.settingsItemCount()`.

(b) Render the row. In the settings render function, after the "Default projects dir" item block (~line 2264, after the `else { lines = append(... "Default projects dir" ...) }`), add:

```go
	// Claude Config item (only for the claude tool)
	if m.ClaudeConfigVisible() {
		cfgName := m.CurrentClaudeConfigName()
		var cfgColor lipgloss.Color
		if m.CurrentClaudeConfigFile() != "" {
			cfgColor = lipgloss.Color("114") // green when a config is active
		} else {
			cfgColor = lipgloss.Color("241") // gray for Standard
		}
		cfgStyle := lipgloss.NewStyle().Foreground(cfgColor)
		lines = append(lines, m.renderSettingsItem(4, "Claude Config", "["+cfgName+"]", cfgStyle, primaryBoldStyle, leftBorder, rightBorder))
	}
```

(`renderSettingsItem(index, label, state, stateStyle, selStyle, leftBorder, rightBorder)` — same signature used by the Sound row at line 2227.)

(c) Flags in `cmd/ghost-tab-tui/main_menu.go`. Add vars and flag registration:

```go
	mainMenuClaudeConfigFile  string
	mainMenuClaudeConfigsList string
```

```go
	mainMenuCmd.Flags().StringVar(&mainMenuClaudeConfigFile, "claude-config-file", "", "Path to active Claude config pointer file")
	mainMenuCmd.Flags().StringVar(&mainMenuClaudeConfigsList, "claude-configs-list", "", "Path to Claude configs list (name:file)")
```

In `runMainMenu`, after the sound-file setter block, load and wire configs:

```go
	if mainMenuClaudeConfigFile != "" {
		model.SetClaudeConfigFile(mainMenuClaudeConfigFile)
	}
	if mainMenuClaudeConfigsList != "" {
		model.SetClaudeConfigs(tui.LoadClaudeConfigsList(mainMenuClaudeConfigsList))
		model.SetActiveClaudeConfig(tui.ReadActiveClaudeConfig(mainMenuClaudeConfigFile))
	}
```

Add the two small loader helpers to `internal/tui/mainmenu.go` (parse the same `name:file` format and pointer the bash side writes):

```go
// LoadClaudeConfigsList parses a name:file list file into ClaudeConfig entries.
func LoadClaudeConfigsList(path string) []ClaudeConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []ClaudeConfig
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		out = append(out, ClaudeConfig{Name: line[:i], File: line[i+1:]})
	}
	return out
}

// ReadActiveClaudeConfig returns the active filename from the pointer file ("" if none/standard).
func ReadActiveClaudeConfig(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(data))
	if v == "standard" {
		return ""
	}
	return v
}
```

(`strings` is already imported in mainmenu.go.)

(d) `lib/menu-tui.sh` — pass the flags. After the `--sound-file` arg block (~line 63), add:

```bash
  cmd_args+=("--claude-config-file" "$gt_config_dir/claude-config")
  cmd_args+=("--claude-configs-list" "$gt_config_dir/claude-configs.list")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run "TestClaudeConfig|TestSettings_" -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: shellcheck + commit**

```bash
shellcheck lib/menu-tui.sh
git add internal/tui/mainmenu.go cmd/ghost-tab-tui/main_menu.go lib/menu-tui.sh internal/tui/mainmenu_claudeconfig_test.go
git commit -m "feat(claude-config): settings-row UI, flags, menu plumbing"
```

---

## Task 6: Management menu — Go subcommand returning intent JSON

**Files:**
- Create: `internal/tui/claude_config_menu.go`
- Create: `cmd/ghost-tab-tui/claude_config_menu.go`
- Test: `cmd/ghost-tab-tui/cmd_test.go` (extend)

**Interfaces:**
- Produces: subcommand `claude-config-menu --configs-list <path>` rendering a list of configs plus an "Add new config…" entry. Emits one JSON object on stdout:
  - `{"action":"add","name":"<entered name>"}`
  - `{"action":"rename","file":"<file>","name":"<new name>"}`
  - `{"action":"delete","file":"<file>"}`
  - `{"action":"quit"}`
- Pattern: mirror `internal/tui/terminal_selector.go` (model with list, Init/Update/View) and `cmd/ghost-tab-tui/select_terminal.go` (TTY opts, safe type assertion, JSON print). Add/rename use an inline `textinput` prompt; delete asks y/N inline.

- [ ] **Step 1: Write the failing test**

Follow the existing `TestRootCmd_SubcommandRegistered` pattern (cobra introspection, no execution — `cmd_test.go` has no run helper). Two tests: registration and the configs-list flag. Add to `cmd/ghost-tab-tui/cmd_test.go`:

```go
func TestClaudeConfigMenuCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"claude-config-menu"})
	if err != nil {
		t.Fatalf("subcommand not found: %v", err)
	}
	if cmd.Name() != "claude-config-menu" {
		t.Fatalf("got %q", cmd.Name())
	}
}

func TestClaudeConfigMenuCmd_HasConfigsListFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"claude-config-menu"})
	if cmd.Flags().Lookup("configs-list") == nil {
		t.Fatal("expected --configs-list flag")
	}
}
```

Also add an entry `"claude-config-menu"` to the `subcommands` slice in `TestRootCmd_SubcommandRegistered`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ghost-tab-tui/ -run "TestClaudeConfigMenu|TestRootCmd_SubcommandRegistered" -v`
Expected: FAIL (unknown command / flag not found).

- [ ] **Step 3: Implement the model and subcommand**

`internal/tui/claude_config_menu.go` — a Bubbletea model mirroring `terminal_selector.go`. Key elements (full logic; boilerplate follows the existing selector):

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
)

// ClaudeConfigMenuResult is the chosen action emitted as JSON by the subcommand.
type ClaudeConfigMenuResult struct {
	Action string `json:"action"`           // add | rename | delete | quit
	File   string `json:"file,omitempty"`   // target filename for rename/delete
	Name   string `json:"name,omitempty"`   // entered name for add/rename
}

type ccmMode int

const (
	ccmList ccmMode = iota
	ccmAddInput
	ccmRenameInput
	ccmDeleteConfirm
)

type ClaudeConfigMenuModel struct {
	configs  []ClaudeConfig // managed configs (no Standard)
	cursor   int            // 0..len(configs)-1 = configs; len = "Add new config…"
	mode     ccmMode
	input    textinput.Model
	result   *ClaudeConfigMenuResult
	quitting bool
}

func NewClaudeConfigMenu(configs []ClaudeConfig) ClaudeConfigMenuModel {
	ti := textinput.New()
	ti.Placeholder = "config name"
	return ClaudeConfigMenuModel{configs: configs, input: ti}
}

func (m ClaudeConfigMenuModel) Init() tea.Cmd { return nil }

func (m ClaudeConfigMenuModel) Result() *ClaudeConfigMenuResult { return m.result }

func (m ClaudeConfigMenuModel) addRowIndex() int { return len(m.configs) }

func (m ClaudeConfigMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.mode {
	case ccmAddInput, ccmRenameInput:
		switch key.Type {
		case tea.KeyEnter:
			name := m.input.Value()
			if name == "" {
				return m, nil
			}
			if m.mode == ccmAddInput {
				m.result = &ClaudeConfigMenuResult{Action: "add", Name: name}
			} else {
				m.result = &ClaudeConfigMenuResult{Action: "rename", File: m.configs[m.cursor].File, Name: name}
			}
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.mode = ccmList
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	case ccmDeleteConfirm:
		switch key.String() {
		case "y", "Y":
			m.result = &ClaudeConfigMenuResult{Action: "delete", File: m.configs[m.cursor].File}
			m.quitting = true
			return m, tea.Quit
		default:
			m.mode = ccmList
			return m, nil
		}
	default: // ccmList
		switch key.String() {
		case "q", "esc", "ctrl+c":
			m.result = &ClaudeConfigMenuResult{Action: "quit"}
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < m.addRowIndex() {
				m.cursor++
			}
		case "a":
			m.mode = ccmAddInput
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case "r":
			if m.cursor < len(m.configs) {
				m.mode = ccmRenameInput
				m.input.SetValue(m.configs[m.cursor].Name)
				m.input.Focus()
				return m, textinput.Blink
			}
		case "d":
			if m.cursor < len(m.configs) {
				m.mode = ccmDeleteConfirm
			}
		case "enter":
			if m.cursor == m.addRowIndex() {
				m.mode = ccmAddInput
				m.input.SetValue("")
				m.input.Focus()
				return m, textinput.Blink
			}
		}
	}
	return m, nil
}

func (m ClaudeConfigMenuModel) View() string {
	// Render configs list, an "Add new config…" row, current mode prompt,
	// and a help line: "↑↓ move · a add · r rename · d delete · q quit".
	// Follow the styling helpers used in terminal_selector.go View().
	return renderClaudeConfigMenu(m) // implement using lipgloss like terminal_selector.go
}
```

Implement `renderClaudeConfigMenu` with the same lipgloss styles the terminal selector uses (list rows with a cursor marker, the add row, the input view when in an input mode, the y/N prompt when confirming, and the help footer).

`cmd/ghost-tab-tui/claude_config_menu.go` — mirror `select_terminal.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/ghost-tab/internal/tui"
	"github.com/jackuait/ghost-tab/internal/util"
	"github.com/spf13/cobra"
)

var ccmConfigsList string

var claudeConfigMenuCmd = &cobra.Command{
	Use:   "claude-config-menu",
	Short: "Manage Claude config files (add/rename/delete)",
	RunE:  runClaudeConfigMenu,
}

func init() {
	claudeConfigMenuCmd.Flags().StringVar(&ccmConfigsList, "configs-list", "", "Path to configs list (name:file)")
	rootCmd.AddCommand(claudeConfigMenuCmd)
}

func runClaudeConfigMenu(cmd *cobra.Command, args []string) error {
	configs := tui.LoadClaudeConfigsList(ccmConfigsList)
	model := tui.NewClaudeConfigMenu(configs)

	ttyOpts, cleanup, err := util.TUITeaOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open terminal: %v\n", err)
		fmt.Println(`{"action":"quit"}`)
		return nil
	}
	defer cleanup()

	opts := append([]tea.ProgramOption{tea.WithAltScreen()}, ttyOpts...)
	p := tea.NewProgram(model, opts...)
	finalModel, runErr := p.Run()

	m, ok := finalModel.(tui.ClaudeConfigMenuModel)
	if !ok || m.Result() == nil {
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", runErr)
		}
		fmt.Println(`{"action":"quit"}`)
		return nil
	}
	out, _ := json.Marshal(m.Result())
	fmt.Println(string(out))
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/ghost-tab-tui/ -run "TestClaudeConfigMenu|TestRootCmd_SubcommandRegistered" -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/claude_config_menu.go cmd/ghost-tab-tui/claude_config_menu.go cmd/ghost-tab-tui/cmd_test.go
git commit -m "feat(claude-config): management menu subcommand"
```

---

## Task 7: Wire management menu into the config menu (`lib/config-tui.sh`)

**Files:**
- Modify: `lib/config-tui.sh`
- Modify: `internal/tui/config_menu.go` + `cmd/ghost-tab-tui/config_menu.go` (add a "Manage Claude configs" entry returning `{"action":"manage-claude-configs"}`)
- Test: `test/bash/config_tui_claude_test.go` (new)

**Interfaces:**
- Consumes: `claude-config-menu` JSON (Task 6); `lib/claude-configs.sh` mutations (Task 2).
- Produces: a `manage-claude-configs` action in the config menu; a bash loop that runs `claude-config-menu`, dispatches add/rename/delete to the storage helpers, and loops until quit.

- [ ] **Step 1: Write the failing test**

The dispatch logic is pure bash given a stubbed `ghost-tab-tui`. Test it by mocking `ghost-tab-tui claude-config-menu` to emit an `add` then a `quit`, and asserting a file + list line appear.

```go
package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigMenu_dispatch_add_then_quit(t *testing.T) {
	dir := t.TempDir()
	cfgRoot := filepath.Join(dir, "ghost-tab")
	_ = os.MkdirAll(cfgRoot, 0o755)

	// Mock ghost-tab-tui: first call returns add, second returns quit.
	bin := mockCommand(t, dir, "ghost-tab-tui", `
state="`+dir+`/calls"
n=$(cat "$state" 2>/dev/null || echo 0)
echo $((n+1)) > "$state"
case "$1" in
  claude-config-menu)
    if [ "$n" = "0" ]; then echo '{"action":"add","name":"Work"}'; else echo '{"action":"quit"}'; fi ;;
  *) echo '{}' ;;
esac
`)
	env := buildEnv(t, []string{bin}, "XDG_CONFIG_HOME="+dir)

	script := `
source lib/claude-configs.sh
source lib/config-tui.sh
manage_claude_configs_interactive
ls "` + cfgRoot + `/claude-configs"
cat "` + cfgRoot + `/claude-configs.list"
`
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "work.json")
	if !strings.Contains(out, "Work:work.json") {
		t.Fatalf("list should contain entry:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/bash/... -run TestConfigMenu_dispatch -v`
Expected: FAIL (`manage_claude_configs_interactive` undefined).

- [ ] **Step 3: Implement**

(a) Add the dispatch loop to `lib/config-tui.sh` (source `claude-configs.sh` at top alongside the other sources):

```bash
# shellcheck source=lib/claude-configs.sh
[ "$(type -t load_claude_configs 2>/dev/null)" = "function" ] || source "$_config_tui_dir/claude-configs.sh"
```

```bash
# Interactive Claude config management loop.
manage_claude_configs_interactive() {
  if ! command -v ghost-tab-tui &>/dev/null; then
    error "ghost-tab-tui binary not found. Please reinstall."
    return 1
  fi
  local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"
  local list_file="$config_dir/claude-configs.list"
  local configs_dir="$config_dir/claude-configs"
  local pointer_file="$config_dir/claude-config"

  while true; do
    local result action file name
    result="$(ghost-tab-tui claude-config-menu --configs-list "$list_file" 2>/dev/null)" || return 1
    action="$(echo "$result" | jq -r '.action' 2>/dev/null)"
    case "$action" in
      add)
        name="$(echo "$result" | jq -r '.name' 2>/dev/null)"
        [ -n "$name" ] && [ "$name" != "null" ] && add_claude_config "$list_file" "$configs_dir" "$name" >/dev/null
        ;;
      rename)
        file="$(echo "$result" | jq -r '.file' 2>/dev/null)"
        name="$(echo "$result" | jq -r '.name' 2>/dev/null)"
        rename_claude_config "$list_file" "$file" "$name"
        ;;
      delete)
        file="$(echo "$result" | jq -r '.file' 2>/dev/null)"
        delete_claude_config "$list_file" "$configs_dir" "$pointer_file" "$file"
        ;;
      quit|""|null)
        return 0
        ;;
      *)
        error "Unknown action: $action"
        return 1
        ;;
    esac
  done
}
```

(b) In the existing `config_menu_interactive` loop, add a case under the action switch:

```bash
      manage-claude-configs)
        manage_claude_configs_interactive
        ;;
```

(c) Add the menu entry in `internal/tui/config_menu.go` (a "Manage Claude configs" row returning `{"action":"manage-claude-configs"}`) following the existing "manage-terminals" / "reinstall" entries; expose it through `cmd/ghost-tab-tui/config_menu.go` if it enumerates actions there. Mirror the existing entries exactly.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./test/bash/... -run TestConfigMenu_dispatch -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: shellcheck + commit**

```bash
shellcheck lib/config-tui.sh
git add lib/config-tui.sh internal/tui/config_menu.go cmd/ghost-tab-tui/config_menu.go test/bash/config_tui_claude_test.go
git commit -m "feat(claude-config): manage configs from config menu"
```

---

## Task 8: Full verification

- [ ] **Step 1: Run the full suite**

Run: `./run-tests.sh`
Expected: all green.

- [ ] **Step 2: Lint every modified script**

Run: `shellcheck lib/claude-configs.sh lib/tmux-session.sh lib/menu-tui.sh lib/config-tui.sh wrapper.sh`
Expected: no warnings.

- [ ] **Step 3: Build the TUI**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 4: Manual smoke (optional but recommended)**

- Create a config via the config menu → confirm `~/.config/ghost-tab/claude-configs/<slug>.json` + list line.
- In the main menu settings, cycle Claude Config → confirm `~/.config/ghost-tab/claude-config` updates.
- Launch a claude project → confirm the pane runs `claude … --settings <path>` (e.g. `pgrep -af claude`).

- [ ] **Step 5: Push**

```bash
git pull --rebase
git push
git status   # must show up to date with origin
```

---

## Self-Review Notes

- **Spec coverage:** storage (Task 2), launch wiring (Tasks 1+3), inline switch (Tasks 4+5), management TUI (Tasks 6+7), Standard-always + remember-last-used (pointer file, Tasks 2/4), testing throughout. All spec sections mapped.
- **Type consistency:** `ClaudeConfig{Name,File}`, `CycleClaudeConfig(direction)`, `CurrentClaudeConfigFile()`, `LoadClaudeConfigsList`, `ReadActiveClaudeConfig`, `GHOST_TAB_CLAUDE_SETTINGS`, list format `name:file`, pointer = filename — used consistently across tasks.
- **Open detail for the implementer:** the exact lipgloss styling in `renderClaudeConfigMenu` (Task 6 View) and the precise config-menu row wording follow existing patterns in `terminal_selector.go` / `config_menu.go`; match the surrounding code rather than inventing new styles.
