#!/bin/bash
# npm-based update check for wisp-deck.

# Show update-available notification if a previous background check found a newer version.
# Deletes the flag after displaying.
notify_if_update_available() {
  local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
  local flag="${config_home}/wisp-deck/update-available"
  [ -f "$flag" ] || return 0

  local version
  version="$(cat "$flag")"
  rm -f "$flag"
  echo "  ↑ Update available: v${version} — run 'npx wisp-deck' to update"
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

    [ "$local_version" = "$remote_version" ] && return
    echo "$remote_version" > "$flag"
    # stderr dropped: this outlives the shell that spawned it (disowned), so a
    # network error surfacing minutes later would print onto whatever is on the
    # terminal by then. The result is communicated through $flag, not stderr.
  ) 2>/dev/null &
  disown
}
