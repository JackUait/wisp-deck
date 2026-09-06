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
  if [ $((now - mtime)) -gt 10 ]; then
    # A builder that died between its lock pre-acquire and its pop leaves an
    # owner stamp behind; rmdir needs the dir empty.
    rm -f "$lock/owner" 2>/dev/null
    rmdir "$lock" 2>/dev/null
  fi
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

# True iff the value is a canonical lowercase Codex thread UUID.
# Usage: codex_session_id_valid <id>
codex_session_id_valid() {
  [[ "$1" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]
}

# True iff a snapshot identity key is a safe sidecar basename. Keys are never
# interpreted as paths: resolution is rooted beneath session-identities.
# Usage: codex_identity_key_valid <key>
codex_identity_key_valid() {
  local key="$1"
  case "$key" in
    "" | "." | ".." | */* | *"|"* | *$'\n'* | *$'\r'*) return 1 ;;
    *.codex) return 0 ;;
    *) return 1 ;;
  esac
}

# Print the safe sidecar basename when <path> is directly inside this config
# directory's session-identities directory.
# Usage: codex_identity_key <config_dir> <path>
codex_identity_key() {
  local config_dir="$1" filepath="$2" key
  [ -n "$filepath" ] || return 1
  [ "${filepath%/*}" = "$config_dir/session-identities" ] || return 1
  key="${filepath##*/}"
  codex_identity_key_valid "$key" || return 1
  echo "$key"
}

# Print the canonical UUID stored by a safe identity key. Symlinks are
# rejected so a snapshot key can never escape the controlled directory.
# Usage: codex_identity_read <config_dir> <key>
codex_identity_read() {
  local config_dir="$1" key="$2" file value
  codex_identity_key_valid "$key" || return 1
  file="$config_dir/session-identities/$key"
  [ -f "$file" ] && [ ! -L "$file" ] || return 1
  value="$(cat "$file" 2>/dev/null)" || return 1
  codex_session_id_valid "$value" || return 1
  echo "$value"
}

# True iff a durable Codex identity is still named by a recovery artifact or
# a live Wisp tmux session. Failure to inspect tmux is treated conservatively:
# pruning waits for a later launch instead of risking a live identity.
# Usage: codex_identity_referenced <tmux_cmd> <config_dir> <key>
codex_identity_referenced() {
  local tmux_cmd="$1" config_dir="$2" key="$3" artifact sessions session env
  codex_identity_key_valid "$key" || return 1
  for artifact in last-session last-session.prev restore-queue; do
    if [ -f "$config_dir/$artifact" ] \
      && grep -Fq -- "|$key" "$config_dir/$artifact" 2>/dev/null; then
      return 0
    fi
  done
  sessions="$("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null)" || return 0
  while IFS= read -r session; do
    [ -n "$session" ] || continue
    env="$("$tmux_cmd" show-environment -t "$session" 2>/dev/null)" || return 0
    if printf '%s\n' "$env" \
      | grep -Fqx -- "WISP_DECK_CODEX_SESSION_FILE=$config_dir/session-identities/$key"; then
      return 0
    fi
  done < <(printf '%s\n' "$sessions")
  return 1
}

# Remove only old sidecars that no frozen snapshot, restore queue, backup, or
# live Wisp session can still use. The age threshold makes this opportunistic
# housekeeping and keeps launch-time races conservative.
# Usage: prune_codex_session_identities <tmux_cmd> <config_dir> [age_days]
prune_codex_session_identities() {
  local tmux_cmd="$1" config_dir="$2" age_days="${3:-30}" identity_dir filepath key
  case "$age_days" in "" | *[!0-9]*) return 1 ;; esac
  identity_dir="$config_dir/session-identities"
  [ -d "$identity_dir" ] && [ ! -L "$identity_dir" ] || return 0
  while IFS= read -r filepath; do
    [ "${filepath%/*}" = "$identity_dir" ] || continue
    key="${filepath##*/}"
    codex_identity_key_valid "$key" || continue
    codex_identity_referenced "$tmux_cmd" "$config_dir" "$key" && continue
    rm -f -- "$filepath" 2>/dev/null || true
  done < <(find "$identity_dir" -type f -name '*.codex' -mtime "+$age_days" -print 2>/dev/null)
}

# Re-derive the live snapshot from alive Wisp Deck tmux sessions.
# Usage: write_session_snapshot <tmux_cmd> <snapshot_file>
# A session is "ours" iff its session environment contains WISP_DECK=1.
# Sessions are ordered by creation time (tmux lists them alphabetically) so
# the snapshot's line order reproduces the order the tabs were opened in.
# Writes atomically (temp + mv). One line per session:
#   boot_id|project|path|tool|terminal|conversation_id|window_layout|account|identity_key
# conversation_id (stamped by the active tool or read from its durable
# sidecar, may be empty) lets restore
# reopen each tab's own conversation instead of the project's most recent one.
# identity_key is a basename rooted beneath session-identities; it lets queue
# construction recover a Codex UUID written after the last heartbeat began.
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
  local config_dir="${snap_file%/*}"
  local s env boot proj filepath tool term sid layout acct seq identity_file identity_key
  local _created _line _marked _acct_present _claude_sid _codex_sid
  while read -r _created s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    # ONE pass over the environment block. Each field used to come from its own
    # `echo "$env" | sed`/`grep` pipeline -- eleven processes per session, inside
    # a loop over EVERY session on the machine. Every session runs this
    # heartbeat and they all produce the identical file, so that made the
    # machine-wide cost quadratic in the size of the deck: measured at 11
    # spawns for one session and 110 for ten.
    boot=""; proj=""; filepath=""; tool=""; term=""; identity_file=""; seq=""
    _marked=0; _acct_present=0; _claude_sid=""; _codex_sid=""; acct=""
    while IFS= read -r _line; do
      case "$_line" in
        'WISP_DECK=1') _marked=1 ;;
        WISP_DECK_BOOT=*) boot="${_line#WISP_DECK_BOOT=}" ;;
        WISP_DECK_PROJECT=*) proj="${_line#WISP_DECK_PROJECT=}" ;;
        WISP_DECK_PATH=*) filepath="${_line#WISP_DECK_PATH=}" ;;
        WISP_DECK_TOOL=*) tool="${_line#WISP_DECK_TOOL=}" ;;
        WISP_DECK_TERMINAL=*) term="${_line#WISP_DECK_TERMINAL=}" ;;
        WISP_DECK_CODEX_SESSION_FILE=*) identity_file="${_line#WISP_DECK_CODEX_SESSION_FILE=}" ;;
        WISP_DECK_CLAUDE_SESSION=*) _claude_sid="${_line#WISP_DECK_CLAUDE_SESSION=}" ;;
        WISP_DECK_CODEX_SESSION=*) _codex_sid="${_line#WISP_DECK_CODEX_SESSION=}" ;;
        WISP_DECK_CLAUDE_ACCOUNT=*) _acct_present=1; acct="${_line#WISP_DECK_CLAUDE_ACCOUNT=}" ;;
        WISP_DECK_SEQ=*) seq="${_line#WISP_DECK_SEQ=}" ;;
      esac
    done < <(printf '%s\n' "$env")
    [ "$_marked" -eq 1 ] || continue
    identity_key="$(codex_identity_key "$config_dir" "$identity_file" 2>/dev/null || true)"
    case "$tool" in
      claude)
        sid="$_claude_sid"
        identity_key=""
        ;;
      codex)
        sid="$_codex_sid"
        codex_session_id_valid "$sid" || sid=""
        if [ -n "$identity_key" ]; then
          local durable_sid
          durable_sid="$(codex_identity_read "$config_dir" "$identity_key" 2>/dev/null || true)"
          [ -n "$durable_sid" ] && sid="$durable_sid"
        fi
        ;;
      *)
        sid=""
        identity_key=""
        ;;
    esac
    # A stamped EMPTY account is a positive "this session runs Default" —
    # encode it as "default" so restore can tell it apart from "unknown"
    # (var absent, empty field) which falls back to the pointer.
    if [ "$_acct_present" -eq 1 ] && [ -z "$acct" ]; then
      acct="default"
    fi
    # The exact pane geometry (7th field). tmux's #{window_layout} is an opaque
    # string that select-layout can replay to reproduce the panes at the sizes
    # they hold right now. It contains no '|', so it is delimiter-safe. Empty
    # when unavailable (old tmux / race) — restore falls back to the default split.
    layout="$("$tmux_cmd" display-message -p -t "$s:0" '#{window_layout}' 2>/dev/null || true)"
    # seq came out of the single parse pass above; only its validation is left.
    case "$seq" in '' | *[!0-9]*) seq="$_created" ;; esac
    echo "${seq} ${boot}|${proj}|${filepath}|${tool}|${term}|${sid}|${layout}|${acct}|${identity_key}" >> "$keyed"
  done < <(printf '%s\n' "$sessions")
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
# boot_id|path|tool|conversation_id|window_layout|account|identity_key line per
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
  local queued=0 b proj filepath tool term sid layout acct identity_key identity_dedupe
  local entries=()
  # Parallel to entries[]: the exact pane layout and the session's Claude
  # login for each entry, held aside so the unstamped-duplicate dedup pass
  # (which rewrites entries[] to path|tool|sid) never has to carry them.
  local layouts=()
  local accts=()
  local identity_keys=()
  # Non-empty sids queued so far. A snapshot must never yield two entries for
  # one conversation — whatever upstream failure duplicates a line, restoring
  # the same sid twice would open duplicate tabs.
  local queued_sids=$'\n'
  while IFS='|' read -r b proj filepath tool term sid layout acct identity_key; do
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
    if [ "$tool" = "codex" ]; then
      codex_session_id_valid "$sid" || sid=""
      codex_identity_key_valid "$identity_key" || identity_key=""
      if [ -n "$identity_key" ]; then
        local durable_sid
        durable_sid="$(codex_identity_read "$config_dir" "$identity_key" 2>/dev/null || true)"
        [ -n "$durable_sid" ] && sid="$durable_sid"
      fi
    else
      identity_key=""
    fi
    if [ "$tool" = "claude" ] && [ -n "$sid" ] \
      && ! claude_transcript_resumable "$filepath" "$sid"; then
      sid=""
    fi
    if [ -n "$sid" ]; then
      identity_dedupe="${tool}|${sid}"
      case "$queued_sids" in
        *$'\n'"$identity_dedupe"$'\n'*)
          restore_log "$config_dir" "queue-build dropped duplicate sid $tool|$sid ($filepath)"
          continue
          ;;
      esac
      queued_sids="${queued_sids}${identity_dedupe}"$'\n'
    fi
    entries+=("${filepath}|${tool}|${sid}")
    layouts+=("$layout")
    accts+=("$acct")
    identity_keys+=("$identity_key")
  done < "$snap"

  # Unstamped duplicates: when several tabs of one project lack a conversation
  # id (claude never rendered a statusline after the id-stamping update), the
  # `claude -c` fallback would open the SAME most-recent conversation in all
  # of them. Pin each such tab to a distinct recent transcript instead,
  # skipping ids already claimed by stamped tabs of the same project. A lone
  # tab keeps the plain `-c` fallback — no guessing needed.
  local n=${#entries[@]} i j path2 tool2 sid2 dupes used
  for ((i = 0; i < n; i++)); do
    IFS='|' read -r filepath tool sid < <(printf '%s\n' "${entries[$i]}")
    if [ "$tool" = "claude" ] && [ -z "$sid" ]; then
      dupes=0
      used=""
      for ((j = 0; j < n; j++)); do
        [ "$j" -eq "$i" ] && continue
        IFS='|' read -r path2 tool2 sid2 < <(printf '%s\n' "${entries[$j]}")
        [ "$tool2" = "claude" ] && [ "$path2" = "$filepath" ] || continue
        dupes=1
        [ -n "$sid2" ] && used="${used}${sid2}"$'\n'
      done
      if [ "$dupes" -eq 1 ]; then
        sid="$(claude_pick_transcript "$filepath" "$used")"
        # Record the pick so the path's next duplicate skips it.
        entries[i]="${filepath}|${tool}|${sid}"
      fi
    fi
    echo "${cur_boot}|${filepath}|${tool}|${sid}|${layouts[$i]}|${accts[$i]}|${identity_keys[$i]}" >> "$tmp"
    queued=1
  done
  if [ "$queued" -eq 1 ]; then
    # Build stamp for restore_pop_authorized's storm grace window and
    # restore_surplus_launch's build-time participant rule. The queue file's
    # own mtime cannot serve: every pop rewrites it.
    date +%s > "$config_dir/restore-queue-built-at" 2>/dev/null || true
    # Pre-acquire the pop lock BEFORE publishing the queue and stamp this
    # process as its owner: the builder's own first pop consumes the handoff
    # (it matches on $$, which survives the command-substitution subshell pops
    # run in). Without this, concurrently-authorized storm launches could
    # drain every entry between the mv below and the builder's pop — the
    # builder (the user's own window) then popped empty and fell through to
    # the picker while its session opened in another tab. Best effort: if the
    # lock cannot be taken, the builder degrades to racing like before.
    _sweep_stale_lock "$queue.lock"
    if mkdir "$queue.lock" 2>/dev/null; then
      echo "$$" > "$queue.lock/owner" 2>/dev/null || true
    fi
    mv "$tmp" "$queue"
    # Marker LAST, after the queue is live: it is the once-per-boot gate that
    # tells other launches "the build already happened" — written any earlier
    # it opens a window where a concurrent launch sees the gate closed but no
    # queue to pop, and falls through to the picker. A crash between the mv
    # and this write self-heals: the claim file still blocks a rebuild and
    # the published queue is poppable.
    echo "$cur_boot" > "$marker"
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
# Echoes "path|tool|conversation_id|window_layout|account|identity_key" (id,
# layout, account, and key may be empty), or nothing when there is no
# consumable entry. A queue
# from another boot, or one older than 5 minutes (a chain that broke), is
# discarded so it can never hijack a tab the user opens later.
restore_queue_pop() {
  local config_dir="$1" cur_boot="$2"
  local queue="$config_dir/restore-queue"
  local lock="$queue.lock"
  # The queue builder pre-acquired the lock when publishing the queue (see
  # maybe_restore_session) so its own first pop cannot be outraced; consume
  # the handoff exactly once. $$ matches even though pops run in command
  # substitution subshells — bash reports the parent's PID there.
  local held=0 owner
  { owner="$(cat "$lock/owner")"; } 2>/dev/null || owner=""
  if [ "$owner" = "$$" ]; then
    rm -f "$lock/owner"
    held=1
  fi
  if [ ! -f "$queue" ]; then
    [ "$held" = "1" ] && rmdir "$lock" 2>/dev/null
    return 0
  fi

  local now mtime
  now="$(date +%s)"
  mtime="$(stat -f %m "$queue" 2>/dev/null || echo 0)"
  if [ $((now - mtime)) -gt 300 ]; then
    restore_log "$config_dir" "pop discarded stale queue (age $((now - mtime))s)"
    rm -f "$queue"
    [ "$held" = "1" ] && rmdir "$lock" 2>/dev/null
    return 0
  fi

  # mkdir is the lock: the popping tab triggers the next one right away, so
  # two tabs can race on the queue; each entry must be consumed exactly once.
  local i=0
  if [ "$held" != "1" ]; then
    _sweep_stale_lock "$lock"
    until mkdir "$lock" 2>/dev/null; do
      i=$((i + 1))
      [ "$i" -ge 40 ] && return 0
      sleep 0.05
    done
  fi

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

# True iff a restore drain is actually progressing right now. A leftover queue
# proves only that a drain STARTED — when its chain breaks (a Cmd+T that never
# produced a tab), the entries sit there and every later launch looked like a
# storm participant and closed itself, locking the user out of the terminal
# until the queue aged out. Liveness is any of: a pop within the window (the
# queue's mtime), an outstanding ticket still inside its 60s claim window, or a
# drain that finished moments ago. The 120s window must stay well above the
# gap between pops of a healthy drain — a first tab whose update check stalls
# has been observed 57s behind the queue build.
# Usage: restore_chain_alive <config_dir>
restore_chain_alive() {
  local config_dir="$1" now stamp mtime
  now="$(date +%s)"
  local queue="$config_dir/restore-queue"
  if [ -f "$queue" ]; then
    mtime="$(stat -f %m "$queue" 2>/dev/null || echo 0)"
    [ $((now - mtime)) -le 120 ] && return 0
  fi
  local ticket="$config_dir/restore-chain-ticket"
  if [ -f "$ticket" ]; then
    { stamp="$(tr -d '[:space:]' < "$ticket")"; } 2>/dev/null || stamp=""
    case "$stamp" in '' | *[!0-9]*) stamp=0 ;; esac
    [ $((now - stamp)) -le 60 ] && return 0
  fi
  local marker="$config_dir/restore-drained-at"
  if [ -f "$marker" ]; then
    { stamp="$(tr -d '[:space:]' < "$marker")"; } 2>/dev/null || stamp=""
    case "$stamp" in '' | *[!0-9]*) stamp=0 ;; esac
    [ $((now - stamp)) -le 120 ] && return 0
  fi
  return 1
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
  # Gated on liveness: participant only records that a queue existed at start.
  # Closing on a dead chain is what denied the user a terminal entirely.
  [ "$participant" = "1" ] && restore_chain_alive "$config_dir" && return 0
  # Storm launches are also recognized by the queue-build stamp: one that
  # STARTED at (or before, or within the grace of) the build is a restore
  # participant even when it raced the builder so hard that its pop ran
  # before the queue file landed — or timed out waiting for an in-flight
  # build. Popping nothing must close it; the drained-at rules below cannot
  # see it because the drain has not happened yet (observed as picker tabs on
  # a loaded CI runner).
  local built
  { built="$(tr -d '[:space:]' < "$config_dir/restore-queue-built-at")"; } 2>/dev/null || built=""
  case "$built" in '' | *[!0-9]*) built="" ;; esac
  if [ -n "$built" ]; then
    case "$launch_epoch" in
      '' | *[!0-9]*) ;;
      *) [ "$launch_epoch" -le $((built + 15)) ] && return 0 ;;
    esac
  fi
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

# True iff an alive Wisp Deck tmux session is already running the active
# tool's conversation <sid>. Codex also consults its live durable sidecar.
# Usage: restore_sid_already_open <tmux_cmd> <tool> <sid>
restore_sid_already_open() {
  local tmux_cmd="$1" tool="$2" sid="$3"
  [ -n "$sid" ] || return 1
  local sessions s env identity_file live_sid
  sessions="$("$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null)" || return 1
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    echo "$env" | grep -q '^WISP_DECK=1$' || continue
    case "$tool" in
      claude)
        echo "$env" | grep -qxF "WISP_DECK_CLAUDE_SESSION=$sid" && return 0
        ;;
      codex)
        echo "$env" | grep -qxF "WISP_DECK_CODEX_SESSION=$sid" && return 0
        identity_file="$(echo "$env" | sed -n 's/^WISP_DECK_CODEX_SESSION_FILE=//p')"
        if [ -f "$identity_file" ] && [ ! -L "$identity_file" ]; then
          live_sid="$(cat "$identity_file" 2>/dev/null || true)"
          codex_session_id_valid "$live_sid" && [ "$live_sid" = "$sid" ] && return 0
        fi
        ;;
    esac
  done < <(printf '%s\n' "$sessions")
  return 1
}

# True iff a popped queue entry should actually be restored in this window:
# its project directory must still exist AND its conversation must not already
# be open in an alive session. The second check is the last line of defense
# against duplicate tabs — whatever upstream failure re-queues an
# already-restored session, the tab that pops it refuses the entry. An empty
# sid carries no identity and is never refused on those grounds (legit
# multi-tab projects from old snapshots must still restore).
# Usage: restore_entry_wanted <tmux_cmd> <entry>
#   entry = path|tool|sid|layout|account|identity_key
restore_entry_wanted() {
  local tmux_cmd="$1" entry="$2" filepath tool sid _layout _account _identity_key
  IFS='|' read -r filepath tool sid _layout _account _identity_key < <(printf '%s\n' "$entry")
  [ -d "$filepath" ] || return 1
  ! restore_sid_already_open "$tmux_cmd" "$tool" "$sid"
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
  local filepath="$1" used="$2"
  local dir f sid
  dir="$(claude_project_dir "$filepath")"
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
  done < <(printf '%s\n' "$(ls -t "$dir"/*.jsonl 2>/dev/null)")
  return 0
}

# Replay a captured #{window_layout} onto <session>'s window 0, re-applying
# until the window size settles. Run BACKGROUNDED by the wrapper BEFORE its
# tmux launch command — its final attach blocks until the session ends, so a
# replay placed after it never runs while the session is alive (the original
# dead-code bug). Polling for a settled size matters just as much:
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

# The chain ticket: a bare Ghostty tab spawned by restore_advance is
# otherwise indistinguishable from a tab the USER opens during the drain —
# both run the wrapper with no args. Before the fix, ANY interactive launch
# popped the next queue entry, so a user's fresh tab was hijacked into
# restoring a queued project while their intended session landed in a
# different tab. restore_advance now issues a one-shot ticket right before
# spawning the next tab, and only the queue builder or the launch that claims
# the ticket may pop. The exposure window shrinks from the queue's whole
# 5-minute lifetime to the instant between spawn and claim.

# Usage: restore_issue_chain_ticket <config_dir>
restore_issue_chain_ticket() {
  date +%s > "$1/restore-chain-ticket" 2>/dev/null || true
}

# Atomically claim the pending chain ticket. The mv is the claim — exactly
# one concurrent launch can win the rename. A stale ticket (>60s, a chain
# that broke before its tab launched) is swept and not claimable, so it can
# never hijack a tab the user opens later.
# Usage: restore_claim_chain_ticket <config_dir>
restore_claim_chain_ticket() {
  local config_dir="$1"
  local ticket="$config_dir/restore-chain-ticket"
  [ -f "$ticket" ] || return 1
  local stamp now
  { stamp="$(tr -d '[:space:]' < "$ticket")"; } 2>/dev/null || stamp=""
  case "$stamp" in '' | *[!0-9]*) stamp=0 ;; esac
  now="$(date +%s)"
  if [ $((now - stamp)) -gt 60 ]; then
    rm -f "$ticket"
    return 1
  fi
  local claimed="$ticket.claimed.$$"
  mv "$ticket" "$claimed" 2>/dev/null || return 1
  rm -f "$claimed"
  return 0
}

# Decide whether an interactive launch may pop the restore queue. Three ways
# in: it built the queue (the boot's first launch, which owns the drain); it
# holds the chain ticket its spawner issued; or it STARTED within a short
# grace window of the queue build — that is the macOS crash-resume storm,
# where every resumed window launches simultaneously with the builder, before
# any ticket exists, and those windows must still drain the queue. A tab the
# user opens later, mid-drain, matches none of these — before this gate it
# popped an entry meant for the chain, restoring another project into the
# user's tab while their intended session opened elsewhere.
# Usage: restore_pop_authorized <config_dir> <builder> <launch_epoch>
restore_pop_authorized() {
  local config_dir="$1" builder="${2:-0}" launch_epoch="${3:-}"
  [ "$builder" = "1" ] && return 0
  restore_claim_chain_ticket "$config_dir" && return 0
  local built
  { built="$(tr -d '[:space:]' < "$config_dir/restore-queue-built-at")"; } 2>/dev/null || built=""
  case "$built" in '' | *[!0-9]*) return 1 ;; esac
  case "$launch_epoch" in '' | *[!0-9]*) return 1 ;; esac
  [ "$launch_epoch" -le $((built + 15)) ]
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
  # Issued BEFORE the spawn so the new tab finds it no matter how fast it
  # launches; it is what authorizes that tab's queue pop (see
  # restore_claim_chain_ticket).
  restore_issue_chain_ticket "$config_dir"
  if ! restore_trigger_tab; then
    terminal_launch_window
    return 0
  fi
  # A zero exit from osascript is NOT proof of a tab: System Events reports
  # success for a keystroke delivered to whatever app happened to be frontmost,
  # which is how a boot-time restore loses its Cmd+T and strands the queue. The
  # ticket being claimed is the proof. Backgrounded because this tab must keep
  # setting itself up meanwhile — the chain's speed is the whole point of
  # advancing before our own session is built.
  restore_chain_watchdog "$config_dir" >/dev/null 2>&1 &
  return 0
}

# Revive a chain whose spawned tab never appeared. Runs detached from the tab
# that called restore_advance.
# Usage: restore_chain_watchdog <config_dir>
restore_chain_watchdog() {
  local config_dir="$1"
  restore_chain_tab_started "$config_dir" && return 0
  restore_log "$config_dir" "chain tab never started; reviving with a window"
  # Re-issued because the window earns its pop by claiming a ticket, and the
  # original is close to the 60s claim window restore_claim_chain_ticket
  # enforces. A tab that turns up late simply claims whichever ticket is
  # outstanding; whoever loses the race pops nothing and closes as surplus.
  restore_issue_chain_ticket "$config_dir"
  terminal_launch_window
}

# Wait for the tab restore_advance just spawned to claim its chain ticket.
# Returns 0 as soon as the ticket disappears, 1 if it is still unclaimed when
# the wait runs out. The default must clear a COLD chain tab, which pays an
# update check before it pops — one has been observed 57s behind the tab that
# spawned it — while staying inside the ticket's 60s claim window.
# Usage: restore_chain_tab_started <config_dir>
restore_chain_tab_started() {
  local config_dir="$1"
  local ticket="$config_dir/restore-chain-ticket"
  local default_wait=45
  # Tests drive the unclaimed path on every advance; the real wait would add
  # 45s per spawn to the suite.
  [[ "${WISP_DECK_TESTING:-}" == "1" ]] && default_wait=1
  local limit="${WISP_DECK_CHAIN_TAB_WAIT:-$default_wait}"
  case "$limit" in '' | *[!0-9]*) limit="$default_wait" ;; esac
  local ticks=$((limit * 4)) i=0
  while [ "$i" -lt "$ticks" ]; do
    [ -f "$ticket" ] || return 0
    sleep 0.25
    i=$((i + 1))
  done
  [ -f "$ticket" ] && return 1
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
