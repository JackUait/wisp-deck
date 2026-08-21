#!/bin/bash
# TUI wrapper for main menu
# Uses wisp-deck-tui main-menu subcommand

# Interactive project selection using wisp-deck-tui main-menu
# Returns 0 if an actionable item was selected, 1 if quit/cancelled,
# 2 if the TUI itself failed (its error is surfaced, not discarded)
# Sets: _selected_project_name, _selected_project_path, _selected_project_action, _selected_ai_tool
select_project_interactive() {
  local projects_file="$1"

  if ! command -v wisp-deck-tui &>/dev/null; then
    error "wisp-deck-tui binary not found. Please reinstall."
    return 1
  fi

  # Read preferences from settings file
  local ghost_display="animated"
  local usage_bars="7d"
  local tab_title="full"
  local tab_bar="large"
  local keep_awake="off"
  local settings_file="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings"
  if [ -f "$settings_file" ]; then
    local saved_display
    saved_display=$(grep '^ghost_display=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_display" ]; then
      ghost_display="$saved_display"
    fi
    local saved_usage_bars
    saved_usage_bars=$(grep '^usage_bars=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_usage_bars" ]; then
      usage_bars="$saved_usage_bars"
    fi
    local saved_tab_title
    saved_tab_title=$(grep '^tab_title=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_tab_title" ]; then
      tab_title="$saved_tab_title"
    fi
    local saved_tab_bar
    saved_tab_bar=$(grep '^tab_bar=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_tab_bar" ]; then
      tab_bar="$saved_tab_bar"
    fi
    local saved_keep_awake
    saved_keep_awake=$(grep '^keep_awake=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_keep_awake" ]; then
      keep_awake="$saved_keep_awake"
    fi
  fi

  local gt_config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"

  # Build AI tools comma-separated list
  local ai_tools_csv
  ai_tools_csv=$(IFS=,; echo "${AI_TOOLS_AVAILABLE[*]}")

  # Build command args
  local ai_tool_file="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/ai-tool"
  local cmd_args=("main-menu" "--projects-file" "$projects_file")
  cmd_args+=("--ai-tool" "${SELECTED_AI_TOOL:-claude}")
  cmd_args+=("--ai-tools" "$ai_tools_csv")
  cmd_args+=("--ai-tool-file" "$ai_tool_file")
  cmd_args+=("--ghost-display" "$ghost_display")
  cmd_args+=("--usage-bars" "$usage_bars")
  cmd_args+=("--tab-title" "$tab_title")
  cmd_args+=("--tab-bar" "$tab_bar")
  cmd_args+=("--keep-awake" "$keep_awake")
  cmd_args+=("--settings-file" "$settings_file")
  # The Settings → AI tools panel installs a tool by sourcing lib/install.sh, so
  # tell the menu where lib/ lives. WISP_DECK_LIB_DIR is only exported inside a
  # running session; the launcher path derives it from this module's location.
  local _lib_dir="${WISP_DECK_LIB_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
  cmd_args+=("--lib-dir" "$_lib_dir")
  cmd_args+=("--disabled-tools-file" "$gt_config_dir/disabled-tools")
  # Only the file path is forwarded; the Go binary reads the preference
  # itself. The bash reader spawns python3, which must never run before the
  # picker (launch critical path).
  local sound_file="$gt_config_dir/${SELECTED_AI_TOOL:-claude}-features.json"
  cmd_args+=("--sound-file" "$sound_file")
  cmd_args+=("--claude-config-file" "$gt_config_dir/claude-config")
  cmd_args+=("--claude-configs-list" "$gt_config_dir/claude-configs.list")
  cmd_args+=("--claude-configs-dir" "$gt_config_dir/claude-configs")
  if [[ "${CODEX_CMD:-}" = /* ]]; then
    cmd_args+=("--codex" "$CODEX_CMD")
  fi
  cmd_args+=("--claude-account-file" "$gt_config_dir/claude-account")
  cmd_args+=("--claude-accounts-list" "$gt_config_dir/claude-accounts.list")
  cmd_args+=("--claude-accounts-dir" "$gt_config_dir/claude-accounts")
  cmd_args+=("--claude-default-label-file" "$gt_config_dir/claude-account-default-label")
  cmd_args+=("--auto-switch-file" "$gt_config_dir/auto-switch-accounts")
  # A pending update found by the background npm check surfaces in the menu
  # header (notice + Update button). Callers may pre-set _update_version.
  if [ -z "${_update_version:-}" ] && type get_update_version &>/dev/null; then
    _update_version="$(get_update_version "${WISP_DECK_INSTALL_DIR:-$HOME/.local/share/wisp-deck}")"
  fi
  if [ -n "${_update_version:-}" ]; then
    cmd_args+=("--update-version" "$_update_version")
  fi

  # The TUI paints on /dev/tty, so stderr carries nothing but errors — capture
  # it. A nonzero exit is the binary DYING (a user quit is exit 0 with action
  # "quit"), and its stderr is the only diagnostic; discarding it shipped a
  # first-run failure that closed the window with nothing to read.
  local result rc _tui_err_file detail=""
  _tui_err_file="$(mktemp 2>/dev/null)" || _tui_err_file=""
  result=$(wisp-deck-tui "${cmd_args[@]}" 2>"${_tui_err_file:-/dev/null}")
  rc=$?
  if [ -n "$_tui_err_file" ]; then
    detail="$(head -n 1 "$_tui_err_file" 2>/dev/null)"
    rm -f "$_tui_err_file" 2>/dev/null
  fi
  if [ "$rc" -ne 0 ]; then
    error "Project menu failed (wisp-deck-tui exit $rc)${detail:+: $detail}"
    return 2
  fi

  # One jq spawn instead of one per field (this path sits between menu close
  # and project open; a jq spawn is ~25ms). One field per line — a tab-joined
  # form would collapse empty fields, since tab is IFS whitespace.
  local parsed action ai_tool name filepath
  if ! parsed=$(echo "$result" | jq -r '.action // "", .ai_tool // "", .name // "", .path // ""' 2>/dev/null); then
    error "Failed to parse menu response"
    return 1
  fi
  {
    read -r action
    read -r ai_tool
    read -r name
    read -r filepath
  } <<< "$parsed"

  if [[ -z "$action" || "$action" == "null" ]]; then
    return 1
  fi

  # Update AI tool if changed (persist regardless of exit action)
  if [[ -n "$ai_tool" && "$ai_tool" != "null" ]]; then
    _selected_ai_tool="$ai_tool"
    # Persist for next session if tool changed
    if [[ "$ai_tool" != "${SELECTED_AI_TOOL:-}" ]]; then
      local ai_tool_file="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/ai-tool"
      mkdir -p "$(dirname "$ai_tool_file")"
      echo "$ai_tool" > "$ai_tool_file"
    fi
  fi

  _selected_project_action="$action"

  case "$action" in
    select-project|open-once)
      if [[ -z "$name" || "$name" == "null" ]]; then
        error "TUI returned invalid project name"
        return 1
      fi
      if [[ -z "$filepath" || "$filepath" == "null" ]]; then
        error "TUI returned invalid project path"
        return 1
      fi

      _selected_project_name="$name"
      _selected_project_path="$filepath"
      return 0
      ;;
    quit)
      return 1
      ;;
    *)
      # Other actions (plain-terminal, settings)
      return 0
      ;;
  esac
}
