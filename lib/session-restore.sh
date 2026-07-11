#!/bin/bash
# Session restore — snapshot alive Wisp Deck tmux sessions and reopen them
# after a reboot as ordered tabs of a single window. The first interactive
# launch of a new boot builds a queue; every interactive launch pops one
# entry and opens the next tab (Cmd+T), chaining until the queue is empty.
# Depends on: terminals/ghostty.sh (terminal_launch_window) for the
# no-Accessibility-permission fallback.

# Print the current macOS boot id. Stable for one uptime; changes on every
# reboot. Empty on failure.
# kern.bootsessionuuid is the source of truth: it is minted once per boot and
# never moves. kern.boottime is only a fallback for macOS versions without the
# uuid — it is computed as now-minus-uptime, so an NTP clock step right after
# login SHIFTS it (observed drifting 1s between two wrapper launches of one
# boot), which made the once-per-boot restore gate fire twice and duplicate
# restored tabs.
current_boot_id() {
  local out
  out="$(sysctl -n kern.bootsessionuuid 2>/dev/null | tr -d '[:space:]')"
  if [ -n "$out" ]; then
    echo "$out"
    return 0
  fi
  out="$(sysctl -n kern.boottime 2>/dev/null)" || return 0
  echo "$out" | sed -n 's/.*[^u]sec = \([0-9][0-9]*\).*/\1/p'
}

# True iff <boot_id> identifies the CURRENT boot, given <cur_boot> (this
# launch's current_boot_id). Exact match, plus a ±10s window around
# kern.boottime's sec for purely numeric ids: those are legacy
# boottime-derived ids (stamped by pre-uuid wrappers this boot, or computed on
# macOS without the uuid), and boottime shifts under an NTP clock step — a
# drifted id must not make the same boot look like a new one. 10s is far below
# any real reboot cycle, so a genuinely previous boot can never fall inside
# the window.
# Usage: boot_id_is_current <boot_id> <cur_boot>
boot_id_is_current() {
  local b="$1" cur="$2" out sec d
  [ -n "$b" ] || return 1
  [ "$b" = "$cur" ] && return 0
  case "$b" in *[!0-9]*) return 1 ;; esac
  out="$(sysctl -n kern.boottime 2>/dev/null)" || return 1
  sec="$(echo "$out" | sed -n 's/.*[^u]sec = \([0-9][0-9]*\).*/\1/p')"
  [ -n "$sec" ] || return 1
  d=$((b - sec))
  [ "${d#-}" -le 10 ]
}

# Append one timestamped line to the restore decision log. The log exists so
# a future restore incident can be reconstructed from facts instead of file
# mtimes; it is trimmed to its last 500 lines on each queue build.
# Usage: restore_log <config_dir> <message...>
restore_log() {
  local config_dir="$1"
  shift
  echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" >> "$config_dir/restore.log" 2>/dev/null || true
}

# Remove an mkdir mutex orphaned by a holder killed inside the critical
# section (shutdown/crash SIGKILLs wrappers at any point). The config dir
# survives reboots, so an orphaned lock would otherwise block its mutex
# FOREVER — every restore_queue_pop would give up and return "empty", killing
# restore silently. Real holds last milliseconds; anything older than 10s is
# orphaned.
# Usage: _sweep_stale_lock <lock_dir>
_sweep_stale_lock() {
  local lock="$1" now mtime
  [ -d "$lock" ] || return 0
  now="$(date +%s)"
  mtime="$(stat -f %m "$lock" 2>/dev/null || echo 0)"
  [ $((now - mtime)) -gt 10 ] && rmdir "$lock" 2>/dev/null
  return 0
}

# Print a strictly-increasing launch sequence number and persist it. tmux's
# #{session_created} has ONE-SECOND resolution: a restore chain (or any burst
# of launches) creates several sessions within the same second, and the
# snapshot's tie-break was tmux's alphabetical list order — restored tabs came
# back alphabetized, not in their real order, and the damage persisted through
# every later snapshot. Each launch takes the next number here and stamps it
# into the session env (WISP_DECK_SEQ); the snapshot orders by it. Seeded from
# the epoch so it stays comparable with the #{session_created} fallback of
# sessions stamped by pre-fix wrappers.
# Usage: next_launch_seq <config_dir>
next_launch_seq() {
  local config_dir="$1" lock i=0 prev next now
  local f="$config_dir/launch-seq"
  lock="$f.lock"
  _sweep_stale_lock "$lock"
  until mkdir "$lock" 2>/dev/null; do
    i=$((i + 1))
    [ "$i" -ge 40 ] && break
    sleep 0.05
  done
  now="$(date +%s)"
  # Grouped so stderr is closed before the open. The -f test is a TOCTOU — this
  # runs from the background heartbeat against a file other sessions rewrite and
  # delete — and losing that race must not print onto the session terminal.
  { prev="$(tr -d '[:space:]' < "$f")"; } 2>/dev/null || prev=""
  case "$prev" in '' | *[!0-9]*) prev=0 ;; esac
  next=$((prev + 1))
  [ "$now" -gt "$next" ] && next="$now"
  echo "$next" > "$f"
  rmdir "$lock" 2>/dev/null
  echo "$next"
}

# Re-derive the live snapshot from alive Wisp Deck tmux sessions.
# Usage: write_session_snapshot <tmux_cmd> <snapshot_file>
# A session is "ours" iff its session environment contains WISP_DECK=1.
# Sessions are ordered by creation time (tmux lists them alphabetically) so
# the snapshot's line order reproduces the order the tabs were opened in.
# Writes atomically (temp + mv). One line per session:
#   boot_id|project|path|tool|terminal|claude_session_id|window_layout|account
# claude_session_id (stamped by the statusline, may be empty) lets restore
# reopen each tab's own conversation instead of the project's most recent one.
# window_layout is tmux's #{window_layout} (may be empty) so restore reproduces
# the pane sizes the session had when Wisp Deck was closed.
# account is the Claude login THIS session runs (WISP_DECK_CLAUDE_ACCOUNT,
# stamped at launch and kept current by the mid-session switch): a managed
# login's dir name, "default" for a stamped Default (Keychain) login, or empty
# when unknown (pre-stamp session). Restore relaunches each tab under ITS
# recorded login — the global claude-account pointer must not decide here, or
# every reboot would silently flip sessions onto whatever login the pointer
# happened to name (the "account changed by itself" bug).
# Field delimiter is '|' — project paths containing '|' are not supported.
write_session_snapshot() {
  local tmux_cmd="$1" snap_file="$2"
  # Sweep tmp files orphaned by writers killed mid-write (shutdown SIGKILLs
  # the heartbeat). Only stale ones — a fresh tmp may belong to a concurrent
  # writer about to mv it into place.
  find "${snap_file%/*}" -maxdepth 1 -name "${snap_file##*/}.tmp.*" \
    -mmin +60 -delete 2>/dev/null || true
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
  # Lines are collected keyed by launch order first ("<key> <line>"), then
  # sorted and stripped. The key is the session's stamped WISP_DECK_SEQ, or
  # its creation time for sessions stamped by pre-fix wrappers — created has
  # one-second resolution, and same-second ties used to fall back to tmux's
  # alphabetical list order, alphabetizing restored tabs (see next_launch_seq).
  local keyed="$tmp.keyed"
  : > "$keyed"
  local s env boot proj path tool term sid layout acct seq
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
    # A stamped EMPTY account is a positive "this session runs Default" —
    # encode it as "default" so restore can tell it apart from "unknown"
    # (var absent, empty field) which falls back to the pointer.
    if echo "$env" | grep -q '^WISP_DECK_CLAUDE_ACCOUNT='; then
      acct="$(echo "$env" | sed -n 's/^WISP_DECK_CLAUDE_ACCOUNT=//p')"
      [ -z "$acct" ] && acct="default"
    else
      acct=""
    fi
    # The exact pane geometry (7th field). tmux's #{window_layout} is an opaque
    # string that select-layout can replay to reproduce the panes at the sizes
    # they hold right now. It contains no '|', so it is delimiter-safe. Empty
    # when unavailable (old tmux / race) — restore falls back to the default split.
    layout="$("$tmux_cmd" display-message -p -t "$s:0" '#{window_layout}' 2>/dev/null || true)"
    seq="$(echo "$env" | sed -n 's/^WISP_DECK_SEQ=//p')"
    case "$seq" in '' | *[!0-9]*) seq="$_created" ;; esac
    echo "${seq} ${boot}|${proj}|${path}|${tool}|${term}|${sid}|${layout}|${acct}" >> "$keyed"
  done <<< "$sessions"
  sort -sn "$keyed" | cut -d' ' -f2- > "$tmp"
  rm -f "$keyed"
  mv "$tmp" "$snap_file"
}

# run_snapshot_heartbeat <wrapper_dir> <tmux_cmd> <snapshot_file> [interval] —
# rewrite the snapshot every interval (default 10s) until killed; the wrapper
# backgrounds this for the life of the session. Each tick re-sources this lib
# in a throwaway bash instead of calling the write_session_snapshot captured
# at session launch: heartbeats live for days, so a launch-time copy freezes
# the snapshot FORMAT until the session closes. That staleness class was real
# — after the snapshot gained its account field, pre-fix heartbeats kept
# writing account-less lines, so a reboot still flipped restored sessions
# onto the global pointer's login. A broken tick (mid-edit lib save) is
# skipped, not fatal; the next tick recovers.
run_snapshot_heartbeat() {
  local wrapper_dir="$1" tmux_cmd="$2" snapshot="$3" interval="${4:-10}"
  while true; do
    bash -c 'source "$1/lib/session-restore.sh" && write_session_snapshot "$2" "$3"' \
      snapshot-tick "$wrapper_dir" "$tmux_cmd" "$snapshot" 2>/dev/null || true
    sleep "$interval"
  done
}

# A launch that lost the once-per-boot claim while the winner is still
# BUILDING must not race ahead: its very next step is the queue pop, and an
# empty pop with no drain marker yet falls through to the picker — the storm
# symptom via a build/pop race. Wait briefly (≤3s) for the in-flight build's
# queue to land. Bounded to FRESH claims (≤30s): a claim whose builder died
# mid-build persists all boot, and later launches must not pay the wait.
# Usage: _restore_wait_for_inflight_build <config_dir> <claim_path>
_restore_wait_for_inflight_build() {
  local config_dir="$1" claim="$2" now mtime i=0
  now="$(date +%s)"
  mtime="$(stat -f %m "$claim" 2>/dev/null || echo 0)"
  [ $((now - mtime)) -le 30 ] || return 0
  while [ ! -f "$config_dir/restore-queue" ] && [ "$i" -lt 30 ]; do
    sleep 0.1
    i=$((i + 1))
  done
  return 0
}

# Once-per-boot restore gate. Call only on interactive launch, before the
# picker. Builds the restore queue (one
# boot_id|path|tool|claude_session_id|window_layout|account line per
# prior-boot snapshot entry, in snapshot order) and stamps
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
  { last_boot="$(tr -d '[:space:]' < "$marker")"; } 2>/dev/null || last_boot=""
  # Drift-tolerant: a marker stamped with a boottime-derived id of THIS boot
  # (pre-NTP-step, or by a pre-uuid wrapper) must still hold the gate.
  boot_id_is_current "$last_boot" "$cur_boot" && return 0

  # Atomic once-per-boot claim. Several wrappers can start simultaneously at
  # login (macOS window reopening); only the noclobber winner may build the
  # queue — a rebuild would resurrect entries another wrapper already popped,
  # duplicating tabs. A claim from THIS boot under ANY id form (a legacy
  # numeric id from a pre-uuid wrapper, or a drifted boottime id) gates just
  # as hard — deleting it and reclaiming under the new form was exactly the
  # double-build path. Only claims from previous boots are swept.
  local claim="$marker.$cur_boot" old
  for old in "$marker".*; do
    [ -e "$old" ] || continue
    [ "$old" = "$claim" ] && continue
    if boot_id_is_current "${old##*.}" "$cur_boot"; then
      restore_log "$config_dir" "queue-build blocked: current-boot claim ${old##*/} already exists (cur=$cur_boot)"
      _restore_wait_for_inflight_build "$config_dir" "$old"
      return 0
    fi
    rm -f "$old"
  done
  if ! (set -o noclobber; : > "$claim") 2>/dev/null; then
    _restore_wait_for_inflight_build "$config_dir" "$claim"
    return 0
  fi

  # Keep a copy of the pre-reboot snapshot: the heartbeat rewrites
  # last-session from currently-alive sessions soon after restore starts, so
  # this backup is the only recovery artifact if the chain breaks.
  cp "$snap" "$snap.prev" 2>/dev/null || true

  local tmp="$queue.tmp.$$"
  : > "$tmp"
  local queued=0 b proj path tool term sid layout acct
  local entries=()
  # Parallel to entries[]: the exact pane layout and the session's Claude
  # login for each entry, held aside so the unstamped-duplicate dedup pass
  # (which rewrites entries[] to path|tool|sid) never has to carry them.
  local layouts=()
  local accts=()
  # Non-empty sids queued so far. A snapshot must never yield two entries for
  # one conversation — whatever upstream failure duplicates a line, restoring
  # the same sid twice would open duplicate tabs.
  local queued_sids=$'\n'
  while IFS='|' read -r b proj path tool term sid layout acct; do
    [ -n "$b" ] || continue
    # Skip sessions of the current boot — they are alive right now, restoring
    # them would duplicate their tabs. Drift-tolerant so entries stamped with
    # a shifted boottime-derived id of THIS boot are also recognized.
    boot_id_is_current "$b" "$cur_boot" && continue
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
    if [ -n "$sid" ]; then
      case "$queued_sids" in
        *$'\n'"$sid"$'\n'*)
          restore_log "$config_dir" "queue-build dropped duplicate sid $sid ($path)"
          continue
          ;;
      esac
      queued_sids="${queued_sids}${sid}"$'\n'
    fi
    entries+=("${path}|${tool}|${sid}")
    layouts+=("$layout")
    accts+=("$acct")
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
    echo "${cur_boot}|${path}|${tool}|${sid}|${layouts[$i]}|${accts[$i]}" >> "$tmp"
    queued=1
  done
  if [ "$queued" -eq 1 ]; then
    echo "$cur_boot" > "$marker"
    mv "$tmp" "$queue"
    # This launch created the queue — it is the user's own window (or the
    # claim winner of a crash-resume storm) and must never be closed as a
    # surplus launch; it keeps the picker fallback when every entry is
    # skipped. Read by the wrapper via restore_surplus_launch.
    # shellcheck disable=SC2034  # consumed by the sourcing wrapper, not here
    WISP_DECK_RESTORE_BUILDER=1
    # Trim the decision log so it can't grow unbounded, then record the build.
    if [ -f "$config_dir/restore.log" ]; then
      tail -n 500 "$config_dir/restore.log" > "$config_dir/restore.log.tmp.$$" 2>/dev/null \
        && mv "$config_dir/restore.log.tmp.$$" "$config_dir/restore.log"
    fi
    restore_log "$config_dir" "queue-built $n entries (boot $cur_boot)"
  else
    rm -f "$tmp"
  fi
  return 0
}

# Atomically pop the first pending entry from the restore queue.
# Usage: restore_queue_pop <config_dir> <current_boot_id>
# Echoes "path|tool|claude_session_id|window_layout|account" (id, layout and
# account may be empty), or nothing when there is no consumable entry. A queue
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
    restore_log "$config_dir" "pop discarded stale queue (age $((now - mtime))s)"
    rm -f "$queue"
    return 0
  fi

  # mkdir is the lock: the popping tab triggers the next one right away, so
  # two tabs can race on the queue; each entry must be consumed exactly once.
  local lock="$queue.lock" i=0
  _sweep_stale_lock "$lock"
  until mkdir "$lock" 2>/dev/null; do
    i=$((i + 1))
    [ "$i" -ge 40 ] && return 0
    sleep 0.05
  done

  local line b
  line="$(head -n 1 "$queue" 2>/dev/null)"
  b="${line%%|*}"
  # Drift-tolerant: a queue built with a pre-NTP-step boottime id must stay
  # consumable by tabs that computed the post-step id (same boot).
  if [ -z "$line" ] || ! boot_id_is_current "$b" "$cur_boot"; then
    restore_log "$config_dir" "pop discarded queue: head boot '$b' is not current (cur=$cur_boot)"
    rm -f "$queue"
    rmdir "$lock" 2>/dev/null
    return 0
  fi
  if [ "$(wc -l < "$queue")" -le 1 ]; then
    # Genuine drain: the last entry is being consumed. Stamp the moment so a
    # straggler launch of the same restore storm (macOS crash resume opens
    # more wrapper tabs than the queue has entries) can recognize it popped
    # empty because the chain finished, and close instead of showing the
    # picker (see restore_surplus_launch). Discards above must NOT stamp.
    # Stamped BEFORE the queue is removed: a concurrent pop must never observe
    # "no queue" without the marker already in place.
    date +%s > "${queue%/*}/restore-drained-at" 2>/dev/null || true
    rm -f "$queue"
  else
    tail -n +2 "$queue" > "$queue.tmp.$$" && mv "$queue.tmp.$$" "$queue"
  fi
  rmdir "$lock" 2>/dev/null
  restore_log "$config_dir" "popped ${line#*|}"
  echo "${line#*|}"
}

# True iff a restore queue for the CURRENT boot exists and is fresh (same
# 5-minute liveness window restore_queue_pop uses). Checked at wrapper start:
# a launch that begins while a current-boot queue is draining is a restore
# participant — if it later pops nothing, it is surplus (see
# restore_surplus_launch), not a window the user opened for the picker.
# Usage: restore_queue_active <config_dir> <current_boot_id>
restore_queue_active() {
  local config_dir="$1" cur_boot="$2"
  local queue="$config_dir/restore-queue"
  [ -f "$queue" ] || return 1
  local now mtime
  now="$(date +%s)"
  mtime="$(stat -f %m "$queue" 2>/dev/null || echo 0)"
  [ $((now - mtime)) -le 300 ] || return 1
  local b
  b="$(head -n 1 "$queue" 2>/dev/null)"
  boot_id_is_current "${b%%|*}" "$cur_boot"
}

# Decide whether an interactive launch that popped NOTHING from the queue is
# a surplus member of a restore storm and should close its tab instead of
# opening the project picker. After a macOS crash, resume relaunches Ghostty
# with its saved windows — each re-runs the wrapper — while the restore chain
# also spawns one tab per queue entry; launches beyond the queue length used
# to fall through to the picker ("several wisp-deck tabs on the main page").
# Surplus iff NOT the queue builder (the user's own window keeps its picker
# fallback) AND any of:
#   - the launch saw an active current-boot queue at start (participant=1);
#   - the launch STARTED before the drain finished (launch_epoch <= drained
#     stamp) — the precise storm signal, immune to a slow init (an update
#     check stalling past any post-drain window);
#   - the queue drained within the last 15 seconds — covers a chained Cmd+T
#     tab spawned just before the drain whose process started just after.
# A launch the user opens after the restore completed matches none of these.
# Usage: restore_surplus_launch <config_dir> <participant> <builder> [launch_epoch]
restore_surplus_launch() {
  local config_dir="$1" participant="${2:-0}" builder="${3:-0}" launch_epoch="${4:-}"
  [ "$builder" = "1" ] && return 1
  [ "$participant" = "1" ] && return 0
  local marker="$config_dir/restore-drained-at" drained now
  [ -f "$marker" ] || return 1
  { drained="$(tr -d '[:space:]' < "$marker")"; } 2>/dev/null || drained=""
  case "$drained" in '' | *[!0-9]*) return 1 ;; esac
  case "$launch_epoch" in
    '' | *[!0-9]*) ;;
    *) [ "$launch_epoch" -le "$drained" ] && return 0 ;;
  esac
  now="$(date +%s)"
  [ $((now - drained)) -le 15 ]
}

# True iff an alive Wisp Deck tmux session is already running conversation
# <sid>. The wrapper stamps WISP_DECK_CLAUDE_SESSION at session creation for
# restored tabs and the statusline keeps it current afterwards.
# Usage: restore_sid_already_open <tmux_cmd> <sid>
restore_sid_already_open() {
  local tmux_cmd="$1" sid="$2"
  [ -n "$sid" ] || return 1
  local sessions s env
  sessions="$("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null)" || return 1
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    echo "$env" | grep -q '^WISP_DECK=1$' || continue
    echo "$env" | grep -qxF "WISP_DECK_CLAUDE_SESSION=$sid" && return 0
  done <<< "$sessions"
  return 1
}

# True iff a popped queue entry should actually be restored in this window:
# its project directory must still exist AND its conversation must not already
# be open in an alive session. The second check is the last line of defense
# against duplicate tabs — whatever upstream failure re-queues an
# already-restored session, the tab that pops it refuses the entry. An empty
# sid carries no identity and is never refused on those grounds (legit
# multi-tab projects from old snapshots must still restore).
# Usage: restore_entry_wanted <tmux_cmd> <entry>   entry = path|tool|sid|layout|account
restore_entry_wanted() {
  local tmux_cmd="$1" entry="$2" path sid
  path="${entry%%|*}"
  sid="$(echo "$entry" | cut -d'|' -f3)"
  [ -d "$path" ] || return 1
  ! restore_sid_already_open "$tmux_cmd" "$sid"
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

# Replay a captured #{window_layout} onto <session>'s window 0, re-applying
# until the window size settles. Run BACKGROUNDED by the wrapper BEFORE its
# `tmux new-session` — that call attaches and blocks until the session ends,
# so a replay placed after it never runs while the session is alive (the
# original dead-code bug). Polling for a settled size matters just as much:
# after a macOS crash, Ghostty respawns a restored tab's command before the
# tab's pty reaches its final size, and when the real size lands tmux
# distributes the width delta EQUALLY between the two columns (not
# proportionally), corrupting the 25/75 split — the layout must be applied
# again AFTER that resize. A failed select-layout (panes still being built
# mid new-session chain) does not count as applied and is retried.
# Exits on its own — whichever comes first:
#   - the user drags a pane border (#{window_layout} changed at a CONSTANT
#     window size): the user has taken over, stand down immediately and never
#     re-apply over their arrangement;
#   - the size has been stable for settle_ticks after a successful apply
#     (default 40 ticks = 10s — generous on purpose: in the observed crash
#     storm the machine was loaded enough that queue pops ran 30s apart, so a
#     short settle could expire before the pty resize even arrived);
#   - the session dies, or max_ticks (default 240 = 60s) elapse.
# Usage: restore_layout_watch <tmux_cmd> <session> <layout>
#        [interval] [settle_ticks] [max_ticks]
restore_layout_watch() {
  local tmux_cmd="$1" sess="$2" layout="$3"
  local interval="${4:-0.25}" settle_ticks="${5:-40}" max_ticks="${6:-240}"
  [ -n "$layout" ] || return 0
  local i=0 out size cur applied_size="" applied_layout="" stable=0
  while [ "$i" -lt "$max_ticks" ]; do
    i=$((i + 1))
    out="$("$tmux_cmd" display-message -p -t "$sess:0" '#{window_width}x#{window_height} #{window_layout}' 2>/dev/null)"
    size="${out%% *}"
    cur="${out#* }"
    if [ -z "$size" ]; then
      # Session gone after we already applied → done. Not created yet → wait.
      [ -n "$applied_size" ] && return 0
    elif [ "$size" = "$applied_size" ] && [ "$cur" != "$applied_layout" ]; then
      # Layout changed while the window size did not: a user pane drag.
      return 0
    elif [ "$size" != "$applied_size" ]; then
      if "$tmux_cmd" select-layout -t "$sess:0" "$layout" 2>/dev/null; then
        applied_size="$size"
        # Track tmux's own rendering of the applied layout (pane ids and
        # checksum differ from the captured string) for drag detection.
        applied_layout="$("$tmux_cmd" display-message -p -t "$sess:0" '#{window_layout}' 2>/dev/null)"
        stable=0
      fi
    else
      stable=$((stable + 1))
      [ "$stable" -ge "$settle_ticks" ] && return 0
    fi
    sleep "$interval"
  done
  return 0
}

# Continue the restore chain: when entries remain, open the next tab of this
# window (the new tab runs the wrapper, pops the next entry, and calls this
# again). When the Cmd+T keystroke fails (Accessibility permission not
# granted), degrade to exactly ONE plain Ghostty window: it runs the
# configured wrapper command, pops the next entry, and advances the chain
# itself, so the queue must be left in place. One-per-remaining-entry here
# would multiply with each spawned window's own advance call and open surplus
# empty windows.
# Usage: restore_advance <config_dir>
restore_advance() {
  local config_dir="$1"
  local queue="$config_dir/restore-queue"
  [ -s "$queue" ] || return 0
  if restore_trigger_tab; then
    return 0
  fi
  terminal_launch_window
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
