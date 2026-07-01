#!/bin/bash
# Auto-switch (account-rotation proxy) setting helpers — pure, no side effects on
# source. The setting turns on Wisp Deck's local rotation proxy, which pools
# multiple Claude accounts and switches between them as quota is exhausted (see
# internal/proxy and lib/claude-accounts.sh). Stored as a single-value flag file
# (on/off), mirroring the claude-account pointer-file style.

# get_auto_switch <flag_file> — prints "on" or "off" (default off; any value
# other than "on" reads as off).
get_auto_switch() {
  local file="$1" val=""
  if [ -f "$file" ]; then
    IFS= read -r val < "$file" || true
    val="${val//[[:space:]]/}"
  fi
  if [ "$val" = "on" ]; then
    echo "on"
  else
    echo "off"
  fi
}

# set_auto_switch <flag_file> <on|off> — writes the normalized value ("on" only
# when exactly "on", otherwise "off").
set_auto_switch() {
  local file="$1" value="$2"
  mkdir -p "$(dirname "$file")"
  if [ "$value" = "on" ]; then
    echo "on" > "$file"
  else
    echo "off" > "$file"
  fi
}

# proxy_startup_port <json_line> — extracts the "port" number from the proxy's
# startup JSON ({"port":N,"key":"..."}), or empty if not present.
proxy_startup_port() {
  printf '%s' "$1" | sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p'
}

# proxy_startup_key <json_line> — extracts the "key" string from the proxy's
# startup JSON, or empty if not present.
proxy_startup_key() {
  printf '%s' "$1" | sed -n 's/.*"key"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

# is_auto_switch_enabled <flag_file> — exit 0 when the setting is on.
is_auto_switch_enabled() {
  [ "$(get_auto_switch "$1")" = "on" ]
}

# auto_switch_eligible <accounts_list_file> — exit 0 when the account list holds
# at least two accounts (label:dir lines, skipping comments/blanks), i.e. there
# is something to rotate between.
auto_switch_eligible() {
  local file="$1" count=0 line
  [ -f "$file" ] || return 1
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"  # ltrim
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" != *:* ]] && continue
    count=$((count + 1))
  done < "$file"
  [ "$count" -ge 2 ]
}
