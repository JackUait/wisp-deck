#!/bin/bash
# Ghostty terminal adapter.

# Return the path to Ghostty's config file.
terminal_get_config_path() {
  echo "$HOME/.config/ghostty/config"
}

# Return the path where the wrapper script should be.
terminal_get_wrapper_path() {
  echo "$HOME/.config/ghost-tab/wrapper.sh"
}

# Install Ghostty: check for the app, open download page if missing.
terminal_install() {
  local app_path="${GHOSTTY_APP_PATH:-/Applications/Ghostty.app}"
  if [ -d "$app_path" ]; then
    success "Ghostty found"
    return 0
  fi

  info "Ghostty not found. Opening download page..."
  open "https://ghostty.org/download"
  echo ""
  echo "  Download and install Ghostty from the page that just opened."
  echo "  Press Enter when installation is complete."
  read -r < /dev/tty

  if [ ! -d "$app_path" ]; then
    error "Ghostty still not found at $app_path"
    info "Install Ghostty and re-run: ghost-tab --terminal"
    return 1
  fi
  success "Ghostty installed"
}

# Write or merge the wrapper command into Ghostty config.
# Uses "/bin/bash -l <wrapper>" instead of a bare script path to avoid a
# Ghostty 1.2.x bug: bare paths trigger "bash --noprofile --norc -c exec -l
# <path>" wrapping, where bash only sees "exec" as the -c string (no-op) and
# exits immediately. Using "/bin/bash -l <path>" as the command makes Ghostty
# use its "/bin/sh -c" argument-expansion code path instead, which runs the
# script correctly.
# Args: config_path wrapper_path
terminal_setup_config() {
  local config_path="$1" wrapper_path="$2"
  local wrapper_line="command = direct:/bin/bash -l $wrapper_path"

  if [ -f "$config_path" ] && grep -q '^command[[:space:]]*=' "$config_path"; then
    sed -i '' 's|^command[[:space:]]*=.*|'"$wrapper_line"'|' "$config_path"
    success "Replaced existing command line in config"
  else
    echo "$wrapper_line" >> "$config_path"
    success "Appended wrapper command to config"
  fi
}

# Remove ghost-tab command line from Ghostty config.
# Matches only ghost-tab's own wrapper line so a user-written command survives.
terminal_cleanup_config() {
  local config_path="$1"
  if [ -f "$config_path" ]; then
    sed -i '' '/^command[[:space:]]*=.*ghost-tab\/wrapper\.sh/d' "$config_path"
  fi
}

# Open a new Ghostty window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  open -na Ghostty --args -e /bin/bash -l "$wrapper" --restore "$path" "$tool"
}
