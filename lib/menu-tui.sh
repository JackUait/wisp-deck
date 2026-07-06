#!/bin/bash
# TUI wrapper for main menu
# Uses wisp-deck-tui main-menu subcommand

# Interactive project selection using wisp-deck-tui main-menu
# Returns 0 if an actionable item was selected, 1 if quit/cancelled
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
  fi

  # Read sound notification state
  local sound_name=""
  local gt_config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
  if type get_sound_name &>/dev/null; then
    sound_name="$(get_sound_name "${SELECTED_AI_TOOL:-claude}" "$gt_config_dir")"
  fi

  # Build AI tools comma-separated list
  local ai_tools_csv
  ai_tools_csv=$(IFS=,; echo "${AI_TOOLS_AVAILABLE[*]}")

  # Build command args
  local ai_tool_file="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/ai-tool"
  local projects_root_file="$gt_config_dir/projects-root"
  local cmd_args=("main-menu" "--projects-file" "$projects_file")
  cmd_args+=("--projects-root-file" "$projects_root_file")
  cmd_args+=("--ai-tool" "${SELECTED_AI_TOOL:-claude}")
  cmd_args+=("--ai-tools" "$ai_tools_csv")
  cmd_args+=("--ai-tool-file" "$ai_tool_file")
  cmd_args+=("--ghost-display" "$ghost_display")
  cmd_args+=("--usage-bars" "$usage_bars")
  cmd_args+=("--tab-title" "$tab_title")
  cmd_args+=("--settings-file" "$settings_file")
  local sound_file="$gt_config_dir/${SELECTED_AI_TOOL:-claude}-features.json"
  cmd_args+=("--sound-file" "$sound_file")
  if [[ -n "$sound_name" ]]; then
    cmd_args+=("--sound-name" "$sound_name")
  fi
  cmd_args+=("--claude-config-file" "$gt_config_dir/claude-config")
  cmd_args+=("--claude-configs-list" "$gt_config_dir/claude-configs.list")
  cmd_args+=("--claude-configs-dir" "$gt_config_dir/claude-configs")
  cmd_args+=("--claude-account-file" "$gt_config_dir/claude-account")
  cmd_args+=("--claude-accounts-list" "$gt_config_dir/claude-accounts.list")
  cmd_args+=("--claude-accounts-dir" "$gt_config_dir/claude-accounts")
  cmd_args+=("--claude-default-label-file" "$gt_config_dir/claude-account-default-label")
  cmd_args+=("--auto-switch-file" "$gt_config_dir/auto-switch-accounts")
  if [ -n "${_update_version:-}" ]; then
    cmd_args+=("--update-version" "$_update_version")
  fi

  local result
  if ! result=$(wisp-deck-tui "${cmd_args[@]}" 2>/dev/null); then
    return 1
  fi

  # One jq spawn instead of one per field (this path sits between menu close
  # and project open; a jq spawn is ~25ms). One field per line — a tab-joined
  # form would collapse empty fields, since tab is IFS whitespace.
  local parsed action ai_tool name path
  if ! parsed=$(echo "$result" | jq -r '.action // "", .ai_tool // "", .name // "", .path // ""' 2>/dev/null); then
    error "Failed to parse menu response"
    return 1
  fi
  {
    read -r action
    read -r ai_tool
    read -r name
    read -r path
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
      if [[ -z "$path" || "$path" == "null" ]]; then
        error "TUI returned invalid project path"
        return 1
      fi

      _selected_project_name="$name"
      _selected_project_path="$path"
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
