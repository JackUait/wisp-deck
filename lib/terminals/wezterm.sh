#!/bin/bash
# WezTerm terminal adapter.

# Return the path to WezTerm's config file.
terminal_get_config_path() {
  echo "$HOME/.wezterm.lua"
}

# Return the path where the wrapper script should be.
terminal_get_wrapper_path() {
  echo "$HOME/.config/ghost-tab/wrapper.sh"
}

# Install WezTerm via Homebrew cask.
terminal_install() {
  ensure_cask "wezterm" "WezTerm"
}

# Write or merge the wrapper command into WezTerm Lua config.
# Args: config_path wrapper_path
terminal_setup_config() {
  local config_path="$1" wrapper_path="$2"

  if [ -f "$config_path" ] && grep -q 'default_prog' "$config_path"; then
    # Replace existing default_prog line
    sed -i '' "s|config\.default_prog[[:space:]]*=.*|config.default_prog = { '$wrapper_path' }|" "$config_path"
    success "Replaced existing default_prog in WezTerm config"
  elif [ -f "$config_path" ]; then
    # Insert before 'return config'
    sed -i '' "s|return config|config.default_prog = { '$wrapper_path' }\nreturn config|" "$config_path"
    success "Added default_prog to WezTerm config"
  else
    # Create minimal config
    cat > "$config_path" << EOF
local wezterm = require 'wezterm'
local config = wezterm.config_builder()
config.default_prog = { '$wrapper_path' }
return config
EOF
    success "Created WezTerm config with wrapper"
  fi
}

# Remove ghost-tab default_prog from WezTerm config.
terminal_cleanup_config() {
  local config_path="$1"
  if [ -f "$config_path" ]; then
    sed -i '' '/default_prog/d' "$config_path"
  fi
}

# Open a new WezTerm window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  open -na WezTerm --args start -- /bin/bash -l "$wrapper" --restore "$path" "$tool"
}
