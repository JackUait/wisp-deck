#!/bin/bash
# Session restore — snapshot alive Ghost Tab tmux sessions and reopen them
# after a reboot. Depends on: terminals/adapter.sh (load_terminal_adapter).

# Print the current macOS boot id (the kern.boottime sec value).
# Stable for one uptime; changes on every reboot. Empty on failure.
current_boot_id() {
  local out
  out="$(sysctl -n kern.boottime 2>/dev/null)" || return 0
  echo "$out" | sed -n 's/.*[^u]sec = \([0-9][0-9]*\).*/\1/p'
}

# Re-derive the live snapshot from alive Ghost Tab tmux sessions.
# Usage: write_session_snapshot <tmux_cmd> <snapshot_file>
# A session is "ours" iff its session environment contains GHOST_TAB=1.
# Writes atomically (temp + mv). One line per session:
#   boot_id|project|path|tool|terminal
write_session_snapshot() {
  local tmux_cmd="$1" snap_file="$2"
  local tmp="${snap_file}.tmp.$$"
  : > "$tmp"
  local s env boot proj path tool term
  for s in $("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null); do
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    echo "$env" | grep -q '^GHOST_TAB=1$' || continue
    boot="$(echo "$env" | sed -n 's/^GHOST_TAB_BOOT=//p')"
    proj="$(echo "$env" | sed -n 's/^GHOST_TAB_PROJECT=//p')"
    path="$(echo "$env" | sed -n 's/^GHOST_TAB_PATH=//p')"
    tool="$(echo "$env" | sed -n 's/^GHOST_TAB_TOOL=//p')"
    term="$(echo "$env" | sed -n 's/^GHOST_TAB_TERMINAL=//p')"
    echo "${boot}|${proj}|${path}|${tool}|${term}" >> "$tmp"
  done
  mv "$tmp" "$snap_file"
}
