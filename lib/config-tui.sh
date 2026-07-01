#!/bin/bash
# Config menu TUI dispatcher
# Uses wisp-deck-tui config-menu subcommand in a loop

# Source dependencies if not already loaded
_config_tui_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/tui.sh
[ "$(type -t header 2>/dev/null)" = "function" ] || source "$_config_tui_dir/tui.sh"
# shellcheck source=lib/claude-configs.sh
[ "$(type -t load_claude_configs 2>/dev/null)" = "function" ] || source "$_config_tui_dir/claude-configs.sh"
# shellcheck source=lib/auto-switch.sh
[ "$(type -t get_auto_switch 2>/dev/null)" = "function" ] || source "$_config_tui_dir/auto-switch.sh"

# Interactive Claude config management loop.
manage_claude_configs_interactive() {
  if ! command -v wisp-deck-tui &>/dev/null; then
    error "wisp-deck-tui binary not found. Please reinstall."
    return 1
  fi
  local config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
  local list_file="$config_dir/claude-configs.list"
  local configs_dir="$config_dir/claude-configs"
  local pointer_file="$config_dir/claude-config"

  while true; do
    local result action file name
    result="$(wisp-deck-tui claude-config-menu --configs-list "$list_file" 2>/dev/null)" || return 1
    action="$(echo "$result" | jq -r '.action' 2>/dev/null)"
    case "$action" in
      add)
        name="$(echo "$result" | jq -r '.name' 2>/dev/null)"
        [ -n "$name" ] && [ "$name" != "null" ] && wisp-deck-tui claude-config add --list "$list_file" --dir "$configs_dir" --pointer "$pointer_file" --name "$name" >/dev/null
        ;;
      rename)
        file="$(echo "$result" | jq -r '.file' 2>/dev/null)"
        name="$(echo "$result" | jq -r '.name' 2>/dev/null)"
        [ -n "$file" ] && [ "$file" != "null" ] && [ -n "$name" ] && [ "$name" != "null" ] && wisp-deck-tui claude-config rename --list "$list_file" --dir "$configs_dir" --pointer "$pointer_file" --file "$file" --name "$name"
        ;;
      delete)
        file="$(echo "$result" | jq -r '.file' 2>/dev/null)"
        [ -n "$file" ] && [ "$file" != "null" ] && wisp-deck-tui claude-config delete --list "$list_file" --dir "$configs_dir" --pointer "$pointer_file" --file "$file"
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
# Calls wisp-deck-tui config-menu, dispatches on action, loops until quit.
config_menu_interactive() {
  if ! command -v wisp-deck-tui &>/dev/null; then
    error "wisp-deck-tui binary not found. Please reinstall."
    return 1
  fi

  # Read VERSION from project root
  local version=""
  local version_file="$_config_tui_dir/../VERSION"
  if [ -f "$version_file" ]; then
    version="$(tr -d '[:space:]' < "$version_file")"
  fi

  local cfg_root="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
  local auto_switch_flag="$cfg_root/auto-switch-accounts"

  while true; do
    local auto_switch
    auto_switch="$(get_auto_switch "$auto_switch_flag")"

    local result
    if ! result=$(wisp-deck-tui config-menu --version "$version" --auto-switch "$auto_switch" 2>/dev/null); then
      return 1
    fi

    local action
    if ! action=$(echo "$result" | jq -r '.action' 2>/dev/null); then
      error "Failed to parse config menu response"
      return 1
    fi

    case "$action" in
      reinstall)
        local script_dir
        script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
        if [ -f "$script_dir/bin/wisp-deck" ]; then
          exec bash "$script_dir/bin/wisp-deck"
        else
          error "Installer not found. Re-clone the repository."
          echo ""
          read -rsn1 -p "Press any key to continue..." </dev/tty
        fi
        ;;
      manage-claude-configs)
        manage_claude_configs_interactive
        ;;
      toggle-auto-switch)
        # Flip the account-rotation setting; the loop re-renders the new state.
        if [ "$auto_switch" = "on" ]; then
          set_auto_switch "$auto_switch_flag" "off"
        else
          set_auto_switch "$auto_switch_flag" "on"
          if ! auto_switch_eligible "$cfg_root/claude-accounts.list"; then
            warn "Add a second Claude account for rotation to take effect."
            read -rsn1 -p "Press any key to continue..." </dev/tty
          fi
        fi
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
