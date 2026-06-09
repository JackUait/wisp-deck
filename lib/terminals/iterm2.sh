#!/bin/bash
# iTerm2 terminal adapter using Dynamic Profiles.

# Return the path to the Ghost Tab dynamic profile.
terminal_get_config_path() {
  echo "$HOME/Library/Application Support/iTerm2/DynamicProfiles/ghost-tab.json"
}

# Return the path where the wrapper script should be.
terminal_get_wrapper_path() {
  echo "$HOME/.config/ghost-tab/wrapper.sh"
}

# Install iTerm2 via Homebrew cask.
terminal_install() {
  ensure_cask "iterm2" "iTerm"
}

# Create a "Ghost Tab" dynamic profile for iTerm2 and set it as default.
# Args: profile_path wrapper_path
terminal_setup_config() {
  local profile_path="$1" wrapper_path="$2"

  mkdir -p "$(dirname "$profile_path")"

  cat > "$profile_path" << EOF
{
  "Profiles": [
    {
      "Name": "Ghost Tab",
      "Guid": "ghost-tab-profile",
      "Custom Command": "Yes",
      "Command": "$wrapper_path"
    }
  ]
}
EOF

  # Save current default profile GUID and set Ghost Tab as default
  local saved_guid_path
  saved_guid_path="$HOME/.config/ghost-tab/iterm2-previous-guid"
  mkdir -p "$(dirname "$saved_guid_path")"
  if defaults read com.googlecode.iterm2 "Default Bookmark Guid" > "$saved_guid_path" 2>/dev/null; then
    defaults write com.googlecode.iterm2 "Default Bookmark Guid" -string "ghost-tab-profile"
    success "Set Ghost Tab as default iTerm2 profile"
    info "Restart iTerm2 for changes to take effect"
  else
    rm -f "$saved_guid_path"
    success "Created Ghost Tab profile in iTerm2"
    info "Set 'Ghost Tab' as your default profile in iTerm2 Preferences → Profiles"
  fi
}

# Open a new iTerm2 window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
# Note: paths containing a single quote are not supported (accepted limitation).
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  osascript -e "tell application \"iTerm\" to create window with default profile command \"/bin/bash -l '$wrapper' --restore '$path' '$tool'\""
}

# Remove the Ghost Tab dynamic profile from iTerm2 and restore previous default.
terminal_cleanup_config() {
  local profile_path="$1"

  if [ -f "$profile_path" ]; then
    rm -f "$profile_path"
    success "Removed Ghost Tab profile from iTerm2"
  fi

  # Restore previous default profile
  local saved_guid_path
  saved_guid_path="$HOME/.config/ghost-tab/iterm2-previous-guid"
  if [ -f "$saved_guid_path" ]; then
    local previous_guid
    previous_guid="$(cat "$saved_guid_path")"
    if [ -n "$previous_guid" ]; then
      defaults write com.googlecode.iterm2 "Default Bookmark Guid" -string "$previous_guid"
    fi
    rm -f "$saved_guid_path"
  fi
}
