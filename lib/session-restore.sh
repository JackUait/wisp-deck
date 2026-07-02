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
#   boot_id|project|path|tool|terminal|claude_session_id
# claude_session_id (stamped by the statusline, may be empty) lets restore
# reopen each tab's own conversation instead of the project's most recent one.
# Field delimiter is '|' — project paths containing '|' are not supported.
write_session_snapshot() {
  local tmux_cmd="$1" snap_file="$2"
  # While a restore chain is draining (a fresh restore-queue exists), the
  # alive sessions are only the restored-so-far subset — rewriting the
  # snapshot now would lose the pointers to the not-yet-restored tabs. A
  # stale queue (>5 min, broken chain) no longer blocks; restore_queue_pop
  # discards it on the next launch anyway.
  local queue="${snap_file%/*}/restore-queue"
  if [ -f "$queue" ]; then
    local now mtime
    now="$(date +%s)"
    mtime="$(stat -f %m "$queue" 2>/dev/null || echo 0)"
    [ $((now - mtime)) -le 300 ] && return 0
  fi
  local sessions
  # If the tmux server is unreachable (e.g. just after a reboot), do NOT
  # overwrite the snapshot — leaving it frozen is what enables restore.
  sessions="$("$tmux_cmd" list-sessions -F '#{session_created} #{session_name}' 2>/dev/null)" || return 0
  local tmp="${snap_file}.tmp.$$"
  : > "$tmp"
  local s env boot proj path tool term sid
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
    sid="$(echo "$env" | sed -n 's/^WISP_DECK_CLAUDE_SESSION=//p')"
    echo "${boot}|${proj}|${path}|${tool}|${term}|${sid}" >> "$tmp"
  done <<< "$(echo "$sessions" | sort -sn)"
  mv "$tmp" "$snap_file"
}

# Once-per-boot restore gate. Call only on interactive launch, before the
# picker. Builds the restore queue (one boot_id|path|tool|claude_session_id
# line per prior-boot snapshot entry, in snapshot order) and stamps
# last-restore-boot. Spawns nothing itself — consumers pop entries via
# restore_queue_pop.
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

  # Keep a copy of the pre-reboot snapshot: the heartbeat rewrites
  # last-session from currently-alive sessions soon after restore starts, so
  # this backup is the only recovery artifact if the chain breaks.
  cp "$snap" "$snap.prev" 2>/dev/null || true

  local tmp="$queue.tmp.$$"
  : > "$tmp"
  local queued=0 b proj path tool term sid
  local entries=()
  while IFS='|' read -r b proj path tool term sid; do
    [ -n "$b" ] || continue
    [ "$b" = "$cur_boot" ] && continue
    # A stamped id is only trustworthy if its transcript is actually
    # resumable — the statusline may have stamped a brand-new session that
    # never got a transcript (or a model turn) before the reboot, and
    # `claude --resume <dead-id>` fails hard, dumping the tab to a bare
    # shell. Blank such ids so the tab falls back to `claude -c` (or the
    # duplicate-pinning below).
    if [ "$tool" = "claude" ] && [ -n "$sid" ] \
      && ! claude_transcript_resumable "$path" "$sid"; then
      sid=""
    fi
    entries+=("${path}|${tool}|${sid}")
  done < "$snap"

  # Unstamped duplicates: when several tabs of one project lack a conversation
  # id (claude never rendered a statusline after the id-stamping update), the
  # `claude -c` fallback would open the SAME most-recent conversation in all
  # of them. Pin each such tab to a distinct recent transcript instead,
  # skipping ids already claimed by stamped tabs of the same project. A lone
  # tab keeps the plain `-c` fallback — no guessing needed.
  local n=${#entries[@]} i j path2 tool2 sid2 dupes used
  for ((i = 0; i < n; i++)); do
    IFS='|' read -r path tool sid <<< "${entries[$i]}"
    if [ "$tool" = "claude" ] && [ -z "$sid" ]; then
      dupes=0
      used=""
      for ((j = 0; j < n; j++)); do
        [ "$j" -eq "$i" ] && continue
        IFS='|' read -r path2 tool2 sid2 <<< "${entries[$j]}"
        [ "$tool2" = "claude" ] && [ "$path2" = "$path" ] || continue
        dupes=1
        [ -n "$sid2" ] && used="${used}${sid2}"$'\n'
      done
      if [ "$dupes" -eq 1 ]; then
        sid="$(claude_pick_transcript "$path" "$used")"
        # Record the pick so the path's next duplicate skips it.
        entries[i]="${path}|${tool}|${sid}"
      fi
    fi
    echo "${cur_boot}|${path}|${tool}|${sid}" >> "$tmp"
    queued=1
  done
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
# Echoes "path|tool|claude_session_id" (id may be empty), or nothing when
# there is no consumable entry. A queue
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

# Claude's per-project transcript directory: the project path with every
# non-alphanumeric byte replaced by '-', under ~/.claude/projects/.
claude_project_dir() {
  echo "$HOME/.claude/projects/${1//[^A-Za-z0-9]/-}"
}

# True iff <sid>'s transcript for project <path> exists AND contains at least
# one model turn. Claude refuses to resume a session without a model turn
# ("No conversation found with session ID") exactly like a missing file, and
# the failed `--resume` exits to a bare shell instead of opening anything.
# Usage: claude_transcript_resumable <path> <sid>
claude_transcript_resumable() {
  local file
  file="$(claude_project_dir "$1")/$2.jsonl"
  [ -f "$file" ] && grep -q '"type":"assistant"' "$file"
}

# Print the most recently used conversation id for <path> that is resumable
# and not in <used> (a newline-separated id list). Prints nothing when the
# project has no transcript store or every transcript is taken.
# Usage: claude_pick_transcript <path> <used>
claude_pick_transcript() {
  local path="$1" used="$2"
  local dir f sid
  dir="$(claude_project_dir "$path")"
  [ -d "$dir" ] || return 0
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    grep -q '"type":"assistant"' "$f" 2>/dev/null || continue
    sid="${f##*/}"
    sid="${sid%.jsonl}"
    if ! printf '%s\n' "$used" | grep -qxF "$sid"; then
      echo "$sid"
      return 0
    fi
  done <<< "$(ls -t "$dir"/*.jsonl 2>/dev/null)"
  return 0
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
