#!/bin/bash
# Config menu TUI dispatcher
# Uses ghost-tab-tui config-menu subcommand in a loop

# Source dependencies if not already loaded
_config_tui_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/tui.sh
[ "$(type -t header 2>/dev/null)" = "function" ] || source "$_config_tui_dir/tui.sh"
# shellcheck source=lib/terminal-select-tui.sh
[ "$(type -t select_terminal_interactive 2>/dev/null)" = "function" ] || source "$_config_tui_dir/terminal-select-tui.sh"
# shellcheck source=lib/terminals/registry.sh
[ "$(type -t get_terminal_display_name 2>/dev/null)" = "function" ] || source "$_config_tui_dir/terminals/registry.sh"
# shellcheck source=lib/claude-configs.sh
[ "$(type -t load_claude_configs 2>/dev/null)" = "function" ] || source "$_config_tui_dir/claude-configs.sh"

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
        [ -n "$name" ] && [ "$name" != "null" ] && ghost-tab-tui claude-config add --list "$list_file" --dir "$configs_dir" --name "$name" >/dev/null
        ;;
      rename)
        file="$(echo "$result" | jq -r '.file' 2>/dev/null)"
        name="$(echo "$result" | jq -r '.name' 2>/dev/null)"
        [ -n "$file" ] && [ "$file" != "null" ] && [ -n "$name" ] && [ "$name" != "null" ] && ghost-tab-tui claude-config rename --list "$list_file" --file "$file" --name "$name"
        ;;
      delete)
        file="$(echo "$result" | jq -r '.file' 2>/dev/null)"
        [ -n "$file" ] && [ "$file" != "null" ] && ghost-tab-tui claude-config delete --list "$list_file" --dir "$configs_dir" --pointer "$pointer_file" --file "$file"
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

# Interactive config menu loop.
# Calls ghost-tab-tui config-menu, dispatches on action, loops until quit.
config_menu_interactive() {
  if ! command -v ghost-tab-tui &>/dev/null; then
    error "ghost-tab-tui binary not found. Please reinstall."
    return 1
  fi

  local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"

  # Read VERSION from project root
  local version=""
  local version_file="$_config_tui_dir/../VERSION"
  if [ -f "$version_file" ]; then
    version="$(tr -d '[:space:]' < "$version_file")"
  fi

  while true; do
    # Resolve terminal display name from saved preference
    local terminal_slug="" terminal_display=""
    if [ -f "$config_dir/terminal" ]; then
      terminal_slug="$(tr -d '[:space:]' < "$config_dir/terminal")"
      terminal_display="$(get_terminal_display_name "$terminal_slug")"
    fi

    local result
    if ! result=$(ghost-tab-tui config-menu --terminal-name "$terminal_display" --version "$version" 2>/dev/null); then
      return 1
    fi

    local action
    if ! action=$(echo "$result" | jq -r '.action' 2>/dev/null); then
      error "Failed to parse config menu response"
      return 1
    fi

    case "$action" in
      manage-terminals)
        export GHOST_TAB_TERMINAL_PREF="$config_dir/terminal"
        if select_terminal_interactive; then
          # shellcheck disable=SC2154
          echo "$_selected_terminal" > "$config_dir/terminal"
          success "Terminal set to $(get_terminal_display_name "$_selected_terminal")"
          echo ""
          read -rsn1 -p "Press any key to continue..." </dev/tty
        fi
        ;;
      reinstall)
        local script_dir
        script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
        if [ -f "$script_dir/bin/ghost-tab" ]; then
          exec bash "$script_dir/bin/ghost-tab"
        else
          error "Installer not found. Re-clone the repository."
          echo ""
          read -rsn1 -p "Press any key to continue..." </dev/tty
        fi
        ;;
      manage-claude-configs)
        manage_claude_configs_interactive
        ;;
      quit|"")
        return 0
        ;;
      *)
        error "Unknown action: $action"
        ;;
    esac
  done
}
