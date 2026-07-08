#!/bin/bash
# Ghostty terminal adapter.

# Return Ghostty's config file path. Per Ghostty's own docs ("the default
# configuration file paths are currently only the XDG config path"), this is
# the ONLY file Ghostty reads by default — verified directly by real-launch
# testing (a working command placed only in ~/Library/Application
# Support/com.mitchellh.ghostty/config does NOT launch). Do not add a second
# location here without re-verifying against a real Ghostty launch first: an
# earlier version of this fix assumed Ghostty also read the Application
# Support path and added unused repair logic for it.
terminal_get_config_path() {
  echo "$HOME/.config/ghostty/config"
}

# Return the path where the wrapper script should be.
terminal_get_wrapper_path() {
  echo "$HOME/.config/wisp-deck/wrapper.sh"
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
    info "Install Ghostty and re-run: wisp-deck"
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
  terminal_disable_window_save_state "$config_path"
}

# Disable Ghostty's native window restoration in the managed config. Its
# default saves window state when macOS resume triggers — i.e. exactly after a
# crash — and every restored window re-runs the wrapper, colliding with
# wisp-deck's own restore queue (the post-crash duplicated-tabs storm: more
# wrapper launches than queue entries). The queue must be the single restore
# mechanism. Appends only when NO window-save-state line exists — a value the
# user set themselves is never overridden (the wrapper's surplus-launch guard
# still contains the storm in that case).
terminal_disable_window_save_state() {
  local config_path="$1"
  [ -f "$config_path" ] && grep -q '^window-save-state[[:space:]]*=' "$config_path" && return 0
  echo "window-save-state = never" >> "$config_path"
}

# Pattern matching a command line WISP-DECK manages, in ANY historical form:
# the launch form (bare path, "~/..." path, "/bin/bash -l …", "direct:…") does
# not matter — what identifies it as ours is the wrapper path. That covers the
# current project dir (wisp-deck) AND the legacy dirs (ghost-tab,
# vibecode-editor) and the oldest claude-wrapper.sh entry point, since an
# upgraded user's config can still carry any of them. Anchored to our known
# paths so a command line the user wrote themselves is never mistaken for ours.
_WISP_MANAGED_COMMAND_RE='^command[[:space:]]*=.*(/(wisp-deck|ghost-tab|vibecode-editor)/wrapper\.sh|/claude-wrapper\.sh)'

# Return 0 if the config contains a wisp-deck-managed command line.
# Args: config_path
terminal_config_has_wisp_command() {
  local config_path="$1"
  [ -f "$config_path" ] || return 1
  grep -Eq "$_WISP_MANAGED_COMMAND_RE" "$config_path"
}

# Decide-and-apply the wrapper command line into the Ghostty config.
#
# A config that already contains OUR command line (any historical form) is
# ALWAYS repaired to the current correct "direct:/bin/bash -l <abs>" form,
# regardless of $choice — an old bare-path line makes Ghostty "fail to launch
# the wrapper" on 1.2.x, and a stale wisp-deck line is ours to fix. Only when
# the existing config has NO wisp-deck line do we honour $choice ("1" = merge,
# anything else = skip), so a user's own `command` is never clobbered silently.
# A missing config is created with our command line.
# Args: config_path wrapper_path choice
terminal_apply_config() {
  local config_path="$1" wrapper_path="$2" choice="$3"

  if terminal_config_has_wisp_command "$config_path"; then
    terminal_setup_config "$config_path" "$wrapper_path"
    return 0
  fi

  if [ ! -f "$config_path" ]; then
    mkdir -p "$(dirname "$config_path")"
    terminal_setup_config "$config_path" "$wrapper_path"
    return 0
  fi

  case "$choice" in
    1) terminal_setup_config "$config_path" "$wrapper_path" ;;
    *) info "Skipped config modification. Add the wrapper manually." ;;
  esac
}

# Remove wisp-deck command line from Ghostty config.
# Matches only wisp-deck's own wrapper line so a user-written command survives.
terminal_cleanup_config() {
  local config_path="$1"
  if [ -f "$config_path" ]; then
    sed -i '' '/^command[[:space:]]*=.*wisp-deck\/wrapper\.sh/d' "$config_path"
  fi
}

# Suppress the macOS login(1) "Last login: ... on ttysNNN" banner.
# Ghostty launches the wrapper through `login -flp`, which prints that banner
# before bash even runs. It lingers on screen through the login-shell profile
# load until the wrapper's loading splash clears it, so a fresh tab/window
# flashes a bare shell prompt before the Wisp Deck splash appears. `login`
# skips the banner entirely when ~/.hushlogin exists, so the window goes
# straight to the splash. Creates the file only when absent — an existing
# hushlogin (the user's own) is left untouched.
# Args: [home_dir]  — defaults to $HOME (override for tests).
ensure_hushlogin() {
  local home="${1:-$HOME}"
  local hushfile="$home/.hushlogin"
  [ -e "$hushfile" ] && return 0
  : > "$hushfile"
}

# Open a plain Ghostty window. Deliberately no --args: a command baked into
# the launch args becomes that instance's default command, so every new tab
# opened in the window would re-run it — this was the root cause of restored
# projects duplicating. The window runs the configured wrapper command, which
# pops the restore queue itself.
terminal_launch_window() {
  open -na Ghostty
}
