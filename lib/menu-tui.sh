#!/bin/bash
# TUI wrapper for main menu
# Uses ghost-tab-tui main-menu subcommand

# Interactive project selection using ghost-tab-tui main-menu
# Returns 0 if an actionable item was selected, 1 if quit/cancelled
# Sets: _selected_project_name, _selected_project_path, _selected_project_action, _selected_ai_tool
select_project_interactive() {
  local projects_file="$1"

  if ! command -v ghost-tab-tui &>/dev/null; then
    error "ghost-tab-tui binary not found. Please reinstall."
    return 1
  fi

  # Read preferences from settings file
  local ghost_display="animated"
  local tab_title="full"
  local settings_file="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/settings"
  if [ -f "$settings_file" ]; then
    local saved_display
    saved_display=$(grep '^ghost_display=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_display" ]; then
      ghost_display="$saved_display"
    fi
    local saved_tab_title
    saved_tab_title=$(grep '^tab_title=' "$settings_file" 2>/dev/null | cut -d= -f2)
    if [ -n "$saved_tab_title" ]; then
      tab_title="$saved_tab_title"
    fi
  fi

  # Read sound notification state
  local sound_name=""
  local gt_config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"
  if type get_sound_name &>/dev/null; then
    sound_name="$(get_sound_name "${SELECTED_AI_TOOL:-claude}" "$gt_config_dir")"
  fi

  # Build AI tools comma-separated list
  local ai_tools_csv
  ai_tools_csv=$(IFS=,; echo "${AI_TOOLS_AVAILABLE[*]}")

  # Build command args
  local ai_tool_file="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/ai-tool"
  local projects_root_file="$gt_config_dir/projects-root"
  local cmd_args=("main-menu" "--projects-file" "$projects_file")
  cmd_args+=("--projects-root-file" "$projects_root_file")
  cmd_args+=("--ai-tool" "${SELECTED_AI_TOOL:-claude}")
  cmd_args+=("--ai-tools" "$ai_tools_csv")
  cmd_args+=("--ai-tool-file" "$ai_tool_file")
  cmd_args+=("--ghost-display" "$ghost_display")
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
  if [ -n "${_update_version:-}" ]; then
    cmd_args+=("--update-version" "$_update_version")
  fi

  local result
  if ! result=$(ghost-tab-tui "${cmd_args[@]}" 2>/dev/null); then
    return 1
  fi

  local action
  if ! action=$(echo "$result" | jq -r '.action' 2>/dev/null); then
    error "Failed to parse menu response"
    return 1
  fi

  if [[ -z "$action" || "$action" == "null" ]]; then
    return 1
  fi

  # Update AI tool if changed (persist regardless of exit action)
  local ai_tool
  ai_tool=$(echo "$result" | jq -r '.ai_tool // ""' 2>/dev/null)
  if [[ -n "$ai_tool" && "$ai_tool" != "null" ]]; then
    _selected_ai_tool="$ai_tool"
    # Persist for next session if tool changed
    if [[ "$ai_tool" != "${SELECTED_AI_TOOL:-}" ]]; then
      local ai_tool_file="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/ai-tool"
      mkdir -p "$(dirname "$ai_tool_file")"
      echo "$ai_tool" > "$ai_tool_file"
    fi
  fi

  _selected_project_action="$action"

  case "$action" in
    select-project|open-once)
      local name path
      name=$(echo "$result" | jq -r '.name' 2>/dev/null)
      path=$(echo "$result" | jq -r '.path' 2>/dev/null)

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
