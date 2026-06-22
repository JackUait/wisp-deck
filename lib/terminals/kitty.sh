#!/bin/bash
# kitty terminal adapter.

# Return the path to kitty's config file.
terminal_get_config_path() {
  echo "$HOME/.config/kitty/kitty.conf"
}

# Return the path where the wrapper script should be.
terminal_get_wrapper_path() {
  echo "$HOME/.config/ghost-tab/wrapper.sh"
}

# Install kitty via Homebrew cask.
terminal_install() {
  ensure_cask "kitty" "kitty"
}

# symbol_map directive routing the Nerd Font glyph ranges to the Symbols Nerd
# Font. kitty has no automatic fallback for missing glyphs, so without this the
# statusline icons render as tofu. The mapping is surgical (only icon ranges),
# leaving the user's primary font in charge of text and box-drawing. The two
# ranges that carry the statusline icons are U+F0001-U+F1AF0 (Material Design
# Icons: brain U+F09D1, memory U+F035B) and U+F000-U+F2FF (FontAwesome: cpu
# gauge U+F0E4).
_KITTY_NERD_SYMBOL_MAP="symbol_map U+23FB-U+23FE,U+2665,U+26A1,U+2B58,U+E000-U+E00A,U+E0A0-U+E0A3,U+E0B0-U+E0D4,U+E200-U+E2A9,U+E300-U+E3E3,U+E5FA-U+E6B7,U+E700-U+E8EF,U+EA60-U+EC1E,U+ED00-U+EFCE,U+F000-U+F2FF,U+F300-U+F375,U+F400-U+F532,U+F0001-U+F1AF0 Symbols Nerd Font Mono"

# Write or merge the wrapper command into kitty config, and ensure the Nerd Font
# symbol_map is present so the statusline icons render.
# Args: config_path wrapper_path
terminal_setup_config() {
  local config_path="$1" wrapper_path="$2"
  local shell_line="shell $wrapper_path"

  if [ -f "$config_path" ] && grep -q '^shell[[:space:]]' "$config_path"; then
    sed -i '' 's|^shell[[:space:]].*|'"$shell_line"'|' "$config_path"
    success "Replaced existing shell line in config"
  else
    echo "$shell_line" >> "$config_path"
    success "Appended wrapper command to config"
  fi

  # Append the symbol_map once (idempotent — keyed on our exact font name).
  if [ ! -f "$config_path" ] || ! grep -q '^symbol_map .*Symbols Nerd Font Mono' "$config_path"; then
    echo "$_KITTY_NERD_SYMBOL_MAP" >> "$config_path"
    success "Mapped Nerd Font glyph ranges to Symbols Nerd Font"
  fi
}

# Remove ghost-tab's lines from kitty config: the wrapper shell line and the
# Nerd Font symbol_map. Matches only ghost-tab's own lines so user config
# survives.
terminal_cleanup_config() {
  local config_path="$1"
  if [ -f "$config_path" ]; then
    sed -i '' '/^shell[[:space:]].*ghost-tab\/wrapper\.sh/d' "$config_path"
    sed -i '' '/^symbol_map .*Symbols Nerd Font Mono/d' "$config_path"
  fi
}

# Open a new kitty window running the wrapper in restore mode.
# Args: wrapper_path project_path ai_tool
terminal_launch_restore() {
  local wrapper="$1" path="$2" tool="$3"
  open -na kitty --args /bin/bash -l "$wrapper" --restore "$path" "$tool"
}
