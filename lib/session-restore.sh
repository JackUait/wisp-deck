#!/bin/bash
# Session restore — snapshot alive Ghost Tab tmux sessions and reopen them
# after a reboot. Depends on: terminals/ghostty.sh (terminal_launch_restore).

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
# Field delimiter is '|' — project paths containing '|' are not supported.
write_session_snapshot() {
  local tmux_cmd="$1" snap_file="$2"
  local sessions
  # If the tmux server is unreachable (e.g. just after a reboot), do NOT
  # overwrite the snapshot — leaving it frozen is what enables restore.
  sessions="$("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null)" || return 0
  local tmp="${snap_file}.tmp.$$"
  : > "$tmp"
  local s env boot proj path tool term
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    echo "$env" | grep -q '^GHOST_TAB=1$' || continue
    boot="$(echo "$env" | sed -n 's/^GHOST_TAB_BOOT=//p')"
    proj="$(echo "$env" | sed -n 's/^GHOST_TAB_PROJECT=//p')"
    path="$(echo "$env" | sed -n 's/^GHOST_TAB_PATH=//p')"
    tool="$(echo "$env" | sed -n 's/^GHOST_TAB_TOOL=//p')"
    term="$(echo "$env" | sed -n 's/^GHOST_TAB_TERMINAL=//p')"
    echo "${boot}|${proj}|${path}|${tool}|${term}" >> "$tmp"
  done <<< "$sessions"
  mv "$tmp" "$snap_file"
}

# Open a window restoring <path>/<tool>. Ghostty is the only supported
# terminal; the <terminal> argument is accepted for backward compatibility
# with the snapshot format but is ignored.
# Usage: launch_restore_window <terminal> <wrapper_path> <project_path> <ai_tool>
# Relies on terminal_launch_restore being available (sourced by the caller,
# e.g. wrapper.sh).
launch_restore_window() {
  local wrapper="$2" path="$3" tool="$4"
  terminal_launch_restore "$wrapper" "$path" "$tool"
}

# Once-per-boot restore gate. Call only on interactive launch, before the
# picker, and never in --restore mode.
# Usage: maybe_restore_session <config_dir> <current_boot_id> <wrapper_path>
# Spawns one window per snapshot line whose boot_id predates this boot, then
# stamps last-restore-boot. No-op if already restored this boot, snapshot
# missing, or no prior-boot lines exist.
maybe_restore_session() {
  local config_dir="$1" cur_boot="$2" wrapper="$3"
  local snap="$config_dir/last-session"
  local marker="$config_dir/last-restore-boot"

  [ -n "$cur_boot" ] || return 0
  [ -f "$snap" ] || return 0

  local last_boot=""
  [ -f "$marker" ] && last_boot="$(tr -d '[:space:]' < "$marker" 2>/dev/null)"
  [ "$cur_boot" = "$last_boot" ] && return 0

  local restored=0 b proj path tool term
  while IFS='|' read -r b proj path tool term; do
    [ -n "$b" ] || continue
    [ "$b" = "$cur_boot" ] && continue
    if [ "$restored" -eq 0 ]; then
      echo "$cur_boot" > "$marker"
      restored=1
    fi
    launch_restore_window "$term" "$wrapper" "$path" "$tool"
  done < "$snap"
  return 0
}

# If args start with --restore, echo "path|tool"; otherwise echo nothing.
# Usage: parse_restore_flag "$@"
parse_restore_flag() {
  if [ "$1" = "--restore" ] && [ -n "$2" ] && [ -n "$3" ]; then
    echo "$2|$3"
  fi
}
