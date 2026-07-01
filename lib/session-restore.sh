#!/bin/bash
# Session restore — snapshot alive Wisp Deck tmux sessions and reopen them
# after a reboot as ordered tabs of a single window. The first interactive
# launch of a new boot builds a queue; every interactive launch pops one
# entry and opens the next tab (Cmd+T), chaining until the queue is empty.
# Depends on: terminals/ghostty.sh (terminal_launch_window) for the
# no-Accessibility-permission fallback.

# Print the current macOS boot id (the kern.boottime sec value).
# Stable for one uptime; changes on every reboot. Empty on failure.
current_boot_id() {
  local out
  out="$(sysctl -n kern.boottime 2>/dev/null)" || return 0
  echo "$out" | sed -n 's/.*[^u]sec = \([0-9][0-9]*\).*/\1/p'
}

# Re-derive the live snapshot from alive Wisp Deck tmux sessions.
# Usage: write_session_snapshot <tmux_cmd> <snapshot_file>
# A session is "ours" iff its session environment contains WISP_DECK=1.
# Sessions are ordered by creation time (tmux lists them alphabetically) so
# the snapshot's line order reproduces the order the tabs were opened in.
# Writes atomically (temp + mv). One line per session:
#   boot_id|project|path|tool|terminal
# Field delimiter is '|' — project paths containing '|' are not supported.
write_session_snapshot() {
  local tmux_cmd="$1" snap_file="$2"
  local sessions
  # If the tmux server is unreachable (e.g. just after a reboot), do NOT
  # overwrite the snapshot — leaving it frozen is what enables restore.
  sessions="$("$tmux_cmd" list-sessions -F '#{session_created} #{session_name}' 2>/dev/null)" || return 0
  local tmp="${snap_file}.tmp.$$"
  : > "$tmp"
  local s env boot proj path tool term
  # shellcheck disable=SC2034  # _created only orders the list; the name field is what's consumed
  local _created
  while read -r _created s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    echo "$env" | grep -q '^WISP_DECK=1$' || continue
    boot="$(echo "$env" | sed -n 's/^WISP_DECK_BOOT=//p')"
    proj="$(echo "$env" | sed -n 's/^WISP_DECK_PROJECT=//p')"
    path="$(echo "$env" | sed -n 's/^WISP_DECK_PATH=//p')"
    tool="$(echo "$env" | sed -n 's/^WISP_DECK_TOOL=//p')"
    term="$(echo "$env" | sed -n 's/^WISP_DECK_TERMINAL=//p')"
    echo "${boot}|${proj}|${path}|${tool}|${term}" >> "$tmp"
  done <<< "$(echo "$sessions" | sort -sn)"
  mv "$tmp" "$snap_file"
}

# Once-per-boot restore gate. Call only on interactive launch, before the
# picker. Builds the restore queue (one boot_id|path|tool line per prior-boot
# snapshot entry, in snapshot order) and stamps last-restore-boot. Spawns
# nothing itself — consumers pop entries via restore_queue_pop.
# Usage: maybe_restore_session <config_dir> <current_boot_id>
maybe_restore_session() {
  local config_dir="$1" cur_boot="$2"
  local snap="$config_dir/last-session"
  local marker="$config_dir/last-restore-boot"
  local queue="$config_dir/restore-queue"

  [ -n "$cur_boot" ] || return 0
  [ -f "$snap" ] || return 0

  local last_boot=""
  [ -f "$marker" ] && last_boot="$(tr -d '[:space:]' < "$marker" 2>/dev/null)"
  [ "$cur_boot" = "$last_boot" ] && return 0

  # Atomic once-per-boot claim. Several wrappers can start simultaneously at
  # login (macOS window reopening); only the noclobber winner may build the
  # queue — a rebuild would resurrect entries another wrapper already popped,
  # duplicating tabs. Claims from previous boots are cleaned up first.
  local claim="$marker.$cur_boot" old
  for old in "$marker".*; do
    [ -e "$old" ] || continue
    [ "$old" = "$claim" ] || rm -f "$old"
  done
  if ! (set -o noclobber; : > "$claim") 2>/dev/null; then
    return 0
  fi

  local tmp="$queue.tmp.$$"
  : > "$tmp"
  local queued=0 b proj path tool term
  while IFS='|' read -r b proj path tool term; do
    [ -n "$b" ] || continue
    [ "$b" = "$cur_boot" ] && continue
    echo "${cur_boot}|${path}|${tool}" >> "$tmp"
    queued=1
  done < "$snap"
  if [ "$queued" -eq 1 ]; then
    echo "$cur_boot" > "$marker"
    mv "$tmp" "$queue"
  else
    rm -f "$tmp"
  fi
  return 0
}

# Atomically pop the first pending entry from the restore queue.
# Usage: restore_queue_pop <config_dir> <current_boot_id>
# Echoes "path|tool", or nothing when there is no consumable entry. A queue
# from another boot, or one older than 5 minutes (a chain that broke), is
# discarded so it can never hijack a tab the user opens later.
restore_queue_pop() {
  local config_dir="$1" cur_boot="$2"
  local queue="$config_dir/restore-queue"
  [ -f "$queue" ] || return 0

  local now mtime
  now="$(date +%s)"
  mtime="$(stat -f %m "$queue" 2>/dev/null || echo 0)"
  if [ $((now - mtime)) -gt 300 ]; then
    rm -f "$queue"
    return 0
  fi

  # mkdir is the lock: the popping tab triggers the next one right away, so
  # two tabs can race on the queue; each entry must be consumed exactly once.
  local lock="$queue.lock" i=0
  until mkdir "$lock" 2>/dev/null; do
    i=$((i + 1))
    [ "$i" -ge 40 ] && return 0
    sleep 0.05
  done

  local line b
  line="$(head -n 1 "$queue" 2>/dev/null)"
  b="${line%%|*}"
  if [ -z "$line" ] || [ "$b" != "$cur_boot" ]; then
    rm -f "$queue"
    rmdir "$lock" 2>/dev/null
    return 0
  fi
  if [ "$(wc -l < "$queue")" -le 1 ]; then
    rm -f "$queue"
  else
    tail -n +2 "$queue" > "$queue.tmp.$$" && mv "$queue.tmp.$$" "$queue"
  fi
  rmdir "$lock" 2>/dev/null
  echo "${line#*|}"
}

# Continue the restore chain: when entries remain, open the next tab of this
# window (the new tab runs the wrapper, pops the next entry, and calls this
# again). When the Cmd+T keystroke fails (Accessibility permission not
# granted), degrade to one plain Ghostty window per remaining entry; each
# window runs the configured wrapper command and pops the queue itself, so
# the queue must be left in place.
# Usage: restore_advance <config_dir>
restore_advance() {
  local config_dir="$1"
  local queue="$config_dir/restore-queue"
  [ -s "$queue" ] || return 0
  if restore_trigger_tab; then
    return 0
  fi
  local n i=0
  n="$(wc -l < "$queue" | tr -d '[:space:]')"
  while [ "$i" -lt "$n" ]; do
    terminal_launch_window
    i=$((i + 1))
  done
  return 0
}

# Simulate Cmd+T in Ghostty so the next restored project opens as a tab of
# this window (Ghostty has no CLI/IPC for tabs on macOS). Requires the
# Accessibility permission for Ghostty; the non-zero exit on denial is the
# caller's signal to fall back to separate windows.
restore_trigger_tab() {
  osascript \
    -e 'tell application "Ghostty" to activate' \
    -e 'tell application "System Events" to keystroke "t" using command down' \
    >/dev/null 2>&1
}
