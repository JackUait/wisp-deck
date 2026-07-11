#!/bin/bash
# npm-based update check for wisp-deck.

# Print the pending update version found by a previous background check, or
# nothing when there is none. A flag equal to the installed version is stale
# (written before an update, inside the 24h re-check throttle) and is ignored.
# Args: install_dir (where the .version marker lives)
get_update_version() {
  local install_dir="$1"
  local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
  local flag="${config_home}/wisp-deck/update-available"
  [ -f "$flag" ] || return 0

  local remote_version local_version
  remote_version="$(tr -d '[:space:]' < "$flag")"
  local_version="$(cat "$install_dir/.version" 2>/dev/null | tr -d '[:space:]')"
  [ -n "$remote_version" ] || return 0
  [ "$remote_version" = "$local_version" ] && return 0
  echo "$remote_version"
}

# Show update-available notification if a previous background check found a
# newer version. Keeps the flag: the main menu reads it too (via
# get_update_version) and must keep offering the update until it runs.
# Args: install_dir (optional; defaults to the npm install location)
notify_if_update_available() {
  local install_dir="${1:-$HOME/.local/share/wisp-deck}"
  local version
  version="$(get_update_version "$install_dir")"
  [ -n "$version" ] || return 0
  echo "  ↑ Update available: v${version} — run 'npx wisp-deck' to update"
}

# Run the update interactively. @latest forces npx past its cached tarball so
# the freshly published version installs even when an older one is cached.
run_wisp_deck_update() {
  echo "  ⇡ Updating wisp-deck..."
  npx --yes wisp-deck@latest
}

# Run a background check against the npm registry.
# Throttled: only checks at most once every 24 hours.
# If a newer version exists, writes a flag file for notify_if_update_available.
# Args: install_dir (where .version marker lives)
check_for_update() {
  local install_dir="$1"
  local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
  local flag="${config_home}/wisp-deck/update-available"
  local ts_file="${config_home}/wisp-deck/last-update-check"

  # Need npm and a local version to compare
  command -v npm &>/dev/null || return 0
  local local_version
  local_version="$(cat "$install_dir/.version" 2>/dev/null | tr -d '[:space:]')"
  [ -n "$local_version" ] || return 0

  # Throttle: skip if checked within the last 24 hours
  # Treat future timestamps (negative elapsed) as expired so a clock correction
  # cannot permanently suppress the check.
  if [ -f "$ts_file" ]; then
    local last_check now elapsed
    last_check="$(cat "$ts_file" 2>/dev/null | tr -d '[:space:]')"
    now="$(date +%s)"
    elapsed=$(( now - last_check ))
    if [ "$elapsed" -ge 0 ] && [ "$elapsed" -lt 86400 ]; then
      return 0
    fi
  fi

  (
    local remote_version
    remote_version="$(npm view wisp-deck version 2>/dev/null | tr -d '[:space:]')" || return
    [ -n "$remote_version" ] || return

    mkdir -p "${config_home}/wisp-deck"
    # Always update the timestamp (even when up to date) so we throttle correctly
    date +%s > "$ts_file"

    if [ "$local_version" = "$remote_version" ]; then
      # Clear any flag written before the update installed this version.
      rm -f "$flag"
      return
    fi
    echo "$remote_version" > "$flag"
    # stderr dropped: this outlives the shell that spawned it (disowned), so a
    # network error surfacing minutes later would print onto whatever is on the
    # terminal by then. The result is communicated through $flag, not stderr.
  ) 2>/dev/null &
  disown
}
