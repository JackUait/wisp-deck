#!/bin/bash
# Tab title watcher — detects AI tool waiting state, updates terminal tab title.
# Depends on: tui.sh (set_tab_title, set_tab_title_waiting)

_TAB_TITLE_WATCHER_PID=""

# Read a single key=value line from the Wisp Deck settings file. Echoes the
# value (whitespace stripped), or nothing if the file or key is absent. Used to
# re-read settings live each poll tick so a mid-session Settings-menu change
# reaches the running session.
# Usage: read_settings_value <settings_file> <key>
read_settings_value() {
  local file="$1" key="$2"
  [ -f "$file" ] || return 0
  grep "^${key}=" "$file" 2>/dev/null | head -1 | cut -d= -f2- | tr -d '[:space:]'
}

# Re-apply the theme accent to a running tmux session's chrome so a mid-session
# theme change takes effect without a relaunch: the outer active-pane border and
# (when lib/spare-tabs.sh is loaded) the nested spare-pane tab bar.
# Usage: apply_session_theme <tmux_cmd> <session_name> <accent>
apply_session_theme() {
  local tmux_cmd="$1" session_name="$2" accent="$3"
  [ -z "$accent" ] && return 0
  "$tmux_cmd" set-option -t "$session_name" pane-active-border-style "fg=colour${accent}" 2>/dev/null || true
  if declare -f spare_tabs_socket >/dev/null 2>&1 && declare -f spare_tabs_set_accent >/dev/null 2>&1; then
    spare_tabs_set_accent "$(spare_tabs_socket "$session_name")" "$accent"
  fi
}

# Repaint EVERY active wisp-deck session's chrome to the theme accent currently
# saved in the settings file. The per-session watcher only reaches sessions whose
# loop was started with the live-theme code, so a theme change misses sessions
# whose watcher predates the feature. This addresses each running session
# externally instead: it enumerates tmux sessions, skips non wisp-deck ones (only
# wisp-deck sessions export WISP_DECK=1), and resolves each session's accent from
# its own AI tool (the WISP_DECK_TOOL env captured at launch) so an "auto"/unset
# theme still picks the right hue per session.
# Usage: apply_theme_to_all_sessions <tmux_cmd> <settings_file>
apply_theme_to_all_sessions() {
  local tmux_cmd="$1" settings_file="$2"
  local theme_pref
  theme_pref="$(read_settings_value "$settings_file" theme)"
  # Pipe (not process substitution): the wrapper sources libs under bash --posix,
  # where `< <(...)` is disabled. A subshell is fine — we mutate tmux, not vars.
  "$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null | while IFS= read -r session; do
    [ -z "$session" ] && continue
    "$tmux_cmd" show-environment -t "$session" WISP_DECK >/dev/null 2>&1 || continue
    local tool accent
    tool="$("$tmux_cmd" show-environment -t "$session" WISP_DECK_TOOL 2>/dev/null | cut -d= -f2-)"
    accent="$(get_theme_accent "$(gt_resolve_theme "$theme_pref" "$tool")")"
    apply_session_theme "$tmux_cmd" "$session" "$accent"
  done
}

# Propagate every live-applicable setting to ALL active wisp-deck sessions at
# once — the watcher-age-independent path (a running session's watcher loop is
# frozen at its launch-time code, so it cannot pick up settings that postdate it).
# Called when the Settings menu closes so a change reaches every open window, not
# just newly-launched sessions. Per-session context (AI tool, project dir) comes
# from the env captured at launch.
#
# theme: a plain tmux session property — applied to every session here.
# tab_title: a per-terminal OSC escape that only the session's own watcher can
#   emit, so it is NOT handled here; new-code sessions update it live each tick.
# Usage: apply_settings_to_all_sessions <tmux_cmd> <settings_file> [lib_dir]
apply_settings_to_all_sessions() {
  local tmux_cmd="$1" settings_file="$2"
  local theme_pref
  theme_pref="$(read_settings_value "$settings_file" theme)"
  # Pipe (not process substitution): wrapper sources libs under bash --posix.
  "$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null | while IFS= read -r session; do
    [ -z "$session" ] && continue
    "$tmux_cmd" show-environment -t "$session" WISP_DECK >/dev/null 2>&1 || continue
    local tool accent
    tool="$("$tmux_cmd" show-environment -t "$session" WISP_DECK_TOOL 2>/dev/null | cut -d= -f2-)"
    accent="$(get_theme_accent "$(gt_resolve_theme "$theme_pref" "$tool")")"
    apply_session_theme "$tmux_cmd" "$session" "$accent"
  done
}

# Fingerprint of the settings file's current content ("missing" when absent).
# Captured before the menu opens; compared after it closes so the all-session
# propagation only runs when the user actually changed a setting.
# Usage: settings_fingerprint <settings_file>
settings_fingerprint() {
  local file="$1"
  if [ -f "$file" ]; then
    cksum < "$file"
  else
    echo "missing"
  fi
}

# Run apply_settings_to_all_sessions only when the settings file no longer
# matches <before> (a settings_fingerprint capture). Selecting a project
# without touching Settings costs zero tmux calls.
# Usage: apply_settings_to_all_sessions_if_changed <tmux_cmd> <settings_file> <before> [lib_dir]
apply_settings_to_all_sessions_if_changed() {
  local tmux_cmd="$1" settings_file="$2" before="$3"
  shift 3
  [ "$(settings_fingerprint "$settings_file")" = "$before" ] && return 0
  apply_settings_to_all_sessions "$tmux_cmd" "$settings_file" "$@"
}

# Read exactly one newline-terminated, carriage-return-free record no larger
# than the protocol limit. The record is returned in _ATTENTION_WATCH_RECORD.
_attention_watcher_read_record() {
  local file="${1-}" bytes line="" extra="" second_status=0
  [ -f "$file" ] || return 1
  bytes="$(wc -c <"$file" 2>/dev/null | tr -d '[:space:]')" || return 1
  case "$bytes" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$bytes" -gt 0 ] && [ "$bytes" -le 4096 ] || return 1
  {
    IFS= read -r line || return 1
    if IFS= read -r extra; then
      second_status=0
    else
      second_status=$?
    fi
  } <"$file"
  [ "$second_status" -ne 0 ] && [ -z "$extra" ] || return 1
  case "$line" in
    *$'\r'*) return 1 ;;
  esac
  _ATTENTION_WATCH_RECORD="$line"
}

_attention_watcher_valid_generation() {
  local generation="${1-}" suffix
  case "$generation" in
    generation.*) suffix="${generation#generation.}" ;;
    *) return 1 ;;
  esac
  case "$suffix" in
    ''|*[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789]*) return 1 ;;
  esac
}

_attention_watcher_valid_tool() {
  case "${1-}" in
    claude|codex|opencode) return 0 ;;
  esac
  return 1
}

# Canonical uint64 validation without shell arithmetic: macOS Bash uses signed
# longs and cannot represent the protocol's full sequence range.
_attention_watcher_valid_sequence() {
  local sequence="${1-}" length
  case "$sequence" in
    ''|*[!0-9]*) return 1 ;;
    0) return 0 ;;
    0*) return 1 ;;
  esac
  length=${#sequence}
  [ "$length" -lt 20 ] && return 0
  [ "$length" -eq 20 ] || return 1
  # shellcheck disable=SC2071  # Deliberate lexical compare; uint64 overflows Bash arithmetic.
  [[ "$sequence" > "18446744073709551615" ]] && return 1
  return 0
}

# Parse one strict descriptor and expose its three payload fields privately.
_attention_watcher_parse_descriptor() {
  local descriptor="${1-}" line tab version generation tool state rest root
  _attention_watcher_read_record "$descriptor" || return 1
  line="$_ATTENTION_WATCH_RECORD"
  tab=$'\t'
  version=${line%%"$tab"*}
  [ "$version" != "$line" ] || return 1
  rest=${line#*"$tab"}
  generation=${rest%%"$tab"*}
  [ "$generation" != "$rest" ] || return 1
  rest=${rest#*"$tab"}
  tool=${rest%%"$tab"*}
  [ "$tool" != "$rest" ] || return 1
  state=${rest#*"$tab"}
  case "$state" in
    *"$tab"*|'') return 1 ;;
  esac
  [ "$version" = "1" ] || return 1
  _attention_watcher_valid_generation "$generation" || return 1
  _attention_watcher_valid_tool "$tool" || return 1
  root=${descriptor%/*}
  [ -n "$root" ] && [ "$state" = "$root/$generation/state" ] || return 1
  _ATTENTION_WATCH_DESCRIPTOR_GENERATION="$generation"
  _ATTENTION_WATCH_DESCRIPTOR_TOOL="$tool"
  _ATTENTION_WATCH_DESCRIPTOR_STATE="$state"
}

# Usage: attention_watcher_read_descriptor <descriptor>
attention_watcher_read_descriptor() {
  _attention_watcher_parse_descriptor "${1-}" || return 1
  printf '%s\t%s\t%s\n' \
    "$_ATTENTION_WATCH_DESCRIPTOR_GENERATION" \
    "$_ATTENTION_WATCH_DESCRIPTOR_TOOL" \
    "$_ATTENTION_WATCH_DESCRIPTOR_STATE"
}

# Parse one strict state record for the descriptor's expected generation.
_attention_watcher_parse_state() {
  local state="${1-}" expected="${2-}" line tab
  local version generation sequence phase reason rest
  _attention_watcher_valid_generation "$expected" || return 1
  _attention_watcher_read_record "$state" || return 1
  line="$_ATTENTION_WATCH_RECORD"
  tab=$'\t'
  version=${line%%"$tab"*}
  [ "$version" != "$line" ] || return 1
  rest=${line#*"$tab"}
  generation=${rest%%"$tab"*}
  [ "$generation" != "$rest" ] || return 1
  rest=${rest#*"$tab"}
  sequence=${rest%%"$tab"*}
  [ "$sequence" != "$rest" ] || return 1
  rest=${rest#*"$tab"}
  phase=${rest%%"$tab"*}
  [ "$phase" != "$rest" ] || return 1
  reason=${rest#*"$tab"}
  case "$reason" in
    *"$tab"*|'') return 1 ;;
  esac
  [ "$version" = "1" ] && [ "$generation" = "$expected" ] || return 1
  _attention_watcher_valid_sequence "$sequence" || return 1
  case "$phase:$reason" in
    ready:-|working:-|unknown:-|attention:done|attention:question|attention:permission|attention:error) ;;
    *) return 1 ;;
  esac
  _ATTENTION_WATCH_STATE_GENERATION="$generation"
  _ATTENTION_WATCH_STATE_SEQUENCE="$sequence"
  _ATTENTION_WATCH_STATE_PHASE="$phase"
  _ATTENTION_WATCH_STATE_REASON="$reason"
}

# Usage: attention_watcher_read_state <state-file> <expected-generation>
attention_watcher_read_state() {
  _attention_watcher_parse_state "${1-}" "${2-}" || return 1
  printf '%s\t%s\t%s\t%s\n' \
    "$_ATTENTION_WATCH_STATE_GENERATION" \
    "$_ATTENTION_WATCH_STATE_SEQUENCE" \
    "$_ATTENTION_WATCH_STATE_PHASE" \
    "$_ATTENTION_WATCH_STATE_REASON"
}

# Read descriptor, state, then descriptor again. A generation rotation between
# those reads is stale and must never produce an alert for the obsolete state.
_attention_watcher_read_snapshot() {
  local descriptor="${1-}" generation tool state sequence phase reason
  _attention_watcher_parse_descriptor "$descriptor" || return 1
  generation="$_ATTENTION_WATCH_DESCRIPTOR_GENERATION"
  tool="$_ATTENTION_WATCH_DESCRIPTOR_TOOL"
  state="$_ATTENTION_WATCH_DESCRIPTOR_STATE"
  _attention_watcher_parse_state "$state" "$generation" || return 1
  sequence="$_ATTENTION_WATCH_STATE_SEQUENCE"
  phase="$_ATTENTION_WATCH_STATE_PHASE"
  reason="$_ATTENTION_WATCH_STATE_REASON"
  _attention_watcher_parse_descriptor "$descriptor" || return 1
  [ "$_ATTENTION_WATCH_DESCRIPTOR_GENERATION" = "$generation" ] \
    && [ "$_ATTENTION_WATCH_DESCRIPTOR_TOOL" = "$tool" ] \
    && [ "$_ATTENTION_WATCH_DESCRIPTOR_STATE" = "$state" ] || return 1
  _ATTENTION_WATCH_SNAPSHOT_GENERATION="$generation"
  _ATTENTION_WATCH_SNAPSHOT_TOOL="$tool"
  _ATTENTION_WATCH_SNAPSHOT_STATE="$state"
  _ATTENTION_WATCH_SNAPSHOT_SEQUENCE="$sequence"
  _ATTENTION_WATCH_SNAPSHOT_PHASE="$phase"
  _ATTENTION_WATCH_SNAPSHOT_REASON="$reason"
}

_attention_watcher_sequence_greater() {
  local candidate="${1-}" baseline="${2-}"
  [ ${#candidate} -gt ${#baseline} ] && return 0
  [ ${#candidate} -lt ${#baseline} ] && return 1
  # shellcheck disable=SC2071  # Equal-width canonical decimals compare lexically.
  [[ "$candidate" > "$baseline" ]]
}

# Echo the tab title to show in model mode: the AI tool's own pane title (set via
# an OSC escape inside its tmux pane), or the project name when the pane has no
# meaningful title yet — tmux defaults a pane's title to the hostname.
# Usage: model_tab_title <pane_title> <hostname> <project>
model_tab_title() {
  local pane_title="$1" host="$2" project="$3"
  if [ -z "$pane_title" ] || [ "$pane_title" = "$host" ]; then
    echo "$project"
  else
    echo "$pane_title"
  fi
}

# Write the terminal tab title for the given state, honoring the title mode.
# Usage: apply_tab_title <state> <mode> <project> <tool>
#   state: "waiting" (idle) or "active" — both render the plain title; the
#          waiting cue is Ghostty's native bell icon, not a text dot
#   mode:  "full" (project · tool), "project" (project only), or
#          "model" (leave the AI tool's own title alone — it set the title itself)
apply_tab_title() {
  local state="$1" mode="$2" project="$3" tool="$4"
  case "$mode" in
    model)
      # The model set the tab title to describe its task — don't clobber it.
      return 0
      ;;
    full)
      if [ "$state" = "waiting" ]; then
        set_tab_title_waiting "$project" "$tool"
      else
        set_tab_title "$project" "$tool"
      fi
      ;;
    *)
      if [ "$state" = "waiting" ]; then
        set_tab_title_waiting "$project"
      else
        set_tab_title "$project"
      fi
      ;;
  esac
}

# Discover the unique pane tagged by the controller as the AI pane.
# Usage: discover_ai_pane <session_name> <tmux_cmd>
# Outputs its stable tmux pane ID (for example, %42).
discover_ai_pane() {
  local session_name="${1-}" tmux_cmd="${2-}" listing line pane flag extra
  local found="" count=0 suffix
  listing="$("$tmux_cmd" list-panes -t "$session_name" -F '#{pane_id}\t#{@gt_ai}' 2>/dev/null)" || return 1
  while IFS= read -r line; do
    pane=${line%%$'\t'*}
    [ "$pane" != "$line" ] || continue
    flag=${line#*$'\t'}
    case "$flag" in
      *$'\t'*) extra=${flag#*$'\t'}; flag=${flag%%$'\t'*}; [ -z "$extra" ] || continue ;;
    esac
    [ "$flag" = "1" ] || continue
    case "$pane" in
      %*) suffix=${pane#%} ;;
      *) continue ;;
    esac
    case "$suffix" in
      ''|*[!0-9]*) continue ;;
    esac
    found="$pane"
    count=$((count + 1))
  done <<EOF
$listing
EOF
  [ "$count" -eq 1 ] || return 1
  printf '%s\n' "$found"
}

# Reset the reducer state. Public for deterministic shell tests; production
# calls it once inside the watcher subshell.
attention_watcher_reset() {
  _ATTENTION_WATCH_LAST_GENERATION=""
  _ATTENTION_WATCH_LAST_SEQUENCE=""
  _ATTENTION_WATCH_LAST_VALID_PHASE=""
  _ATTENTION_WATCH_LAST_VALID_REASON=""
  _ATTENTION_WATCH_LAST_PRESENT_PHASE=""
  _ATTENTION_WATCH_LAST_TOOL=""
  _ATTENTION_WATCH_LAST_TITLE_MODE=""
  _ATTENTION_WATCH_LAST_ACCENT=""
  _ATTENTION_WATCH_HOST="$(hostname 2>/dev/null)"
}

# Consume one snapshot. Missing, malformed, stale, or regressed state becomes
# unknown and never advances alert dedupe.
# Usage: attention_watcher_tick <session> <project> <fallback-title-mode> <tmux> <descriptor> <config-dir>
attention_watcher_tick() {
  local session_name="${1-}" project_name="${2-}" fallback_title="${3-}"
  local tmux_cmd="${4-}" descriptor="${5-}" config_dir="${6-}"
  local generation="" sequence="" phase="unknown" reason="-" tool=""
  local snapshot_valid=0 tuple_new=0 title_state="active" keep_state="active"
  local previous_tool="$_ATTENTION_WATCH_LAST_TOOL"
  local settings_file="" current_title="$fallback_title" current_pane=""

  # A valid descriptor still supplies current tool identity when its state is
  # temporarily absent or malformed. The complete double-read snapshot below
  # alone is allowed to drive alert state.
  if _attention_watcher_parse_descriptor "$descriptor"; then
    tool="$_ATTENTION_WATCH_DESCRIPTOR_TOOL"
  else
    tool="$previous_tool"
  fi
  if _attention_watcher_read_snapshot "$descriptor"; then
    snapshot_valid=1
    generation="$_ATTENTION_WATCH_SNAPSHOT_GENERATION"
    sequence="$_ATTENTION_WATCH_SNAPSHOT_SEQUENCE"
    phase="$_ATTENTION_WATCH_SNAPSHOT_PHASE"
    reason="$_ATTENTION_WATCH_SNAPSHOT_REASON"
    tool="$_ATTENTION_WATCH_SNAPSHOT_TOOL"
  fi

  if [ "$snapshot_valid" -eq 1 ]; then
    if [ -z "$_ATTENTION_WATCH_LAST_GENERATION" ] \
       || [ "$generation" != "$_ATTENTION_WATCH_LAST_GENERATION" ]; then
      tuple_new=1
    elif [ "$sequence" = "$_ATTENTION_WATCH_LAST_SEQUENCE" ]; then
      # Same identity may only repeat the exact same semantic state.
      if [ "$phase" != "$_ATTENTION_WATCH_LAST_VALID_PHASE" ] \
         || [ "$reason" != "$_ATTENTION_WATCH_LAST_VALID_REASON" ]; then
        snapshot_valid=0
        phase="unknown"
        reason="-"
      fi
    elif _attention_watcher_sequence_greater "$sequence" "$_ATTENTION_WATCH_LAST_SEQUENCE"; then
      tuple_new=1
    else
      snapshot_valid=0
      phase="unknown"
      reason="-"
    fi
  fi

  if [ "$snapshot_valid" -eq 1 ] && [ "$tuple_new" -eq 1 ]; then
    _ATTENTION_WATCH_LAST_GENERATION="$generation"
    _ATTENTION_WATCH_LAST_SEQUENCE="$sequence"
    _ATTENTION_WATCH_LAST_VALID_PHASE="$phase"
    _ATTENTION_WATCH_LAST_VALID_REASON="$reason"
  fi

  [ -n "$tool" ] && _ATTENTION_WATCH_LAST_TOOL="$tool"

  [ -n "$config_dir" ] && settings_file="$config_dir/settings"
  if [ -n "$settings_file" ] && [ -f "$settings_file" ]; then
    local saved_title
    saved_title="$(read_settings_value "$settings_file" tab_title)"
    [ -n "$saved_title" ] && current_title="$saved_title"
    if [ -n "$tool" ] \
       && declare -f gt_resolve_theme >/dev/null 2>&1 \
       && declare -f get_theme_accent >/dev/null 2>&1; then
      local theme accent
      theme="$(read_settings_value "$settings_file" theme)"
      accent="$(get_theme_accent "$(gt_resolve_theme "$theme" "$tool")")"
      if [ -n "$accent" ] && [ "$accent" != "$_ATTENTION_WATCH_LAST_ACCENT" ]; then
        apply_session_theme "$tmux_cmd" "$session_name" "$accent"
        _ATTENTION_WATCH_LAST_ACCENT="$accent"
      fi
    fi
  fi

  current_pane="$(discover_ai_pane "$session_name" "$tmux_cmd")" || current_pane=""

  case "$phase" in
    attention)
      title_state="waiting"
      keep_state="waiting"
      ;;
    ready)
      keep_state="waiting"
      ;;
    working|unknown)
      keep_state="active"
      ;;
  esac

  if [ -n "$config_dir" ] && declare -f keep_awake_tick >/dev/null 2>&1; then
    keep_awake_tick "$config_dir" "$session_name" "$$" "$keep_state" || true
  fi

  if [ "$current_title" = "model" ] && [ -n "$current_pane" ]; then
    local pane_title
    pane_title="$("$tmux_cmd" display-message -p -t "$current_pane" '#{pane_title}' 2>/dev/null)"
    set_tab_title "$(model_tab_title "$pane_title" "$_ATTENTION_WATCH_HOST" "$project_name")"
  fi

  if [ "$phase" != "$_ATTENTION_WATCH_LAST_PRESENT_PHASE" ] \
     || [ "$tool" != "$previous_tool" ] \
     || [ "$current_title" != "$_ATTENTION_WATCH_LAST_TITLE_MODE" ]; then
    apply_tab_title "$title_state" "$current_title" "$project_name" "$tool"
  fi

  if [ "$snapshot_valid" -eq 1 ] && [ "$tuple_new" -eq 1 ] \
     && [ "$phase" = "attention" ]; then
    if [ -n "$config_dir" ] && declare -f play_notification_sound >/dev/null 2>&1; then
      play_notification_sound "$tool" "$config_dir"
    fi
  fi

  _ATTENTION_WATCH_LAST_PRESENT_PHASE="$phase"
  _ATTENTION_WATCH_LAST_TITLE_MODE="$current_title"
}

# Start the semantic consumer before tmux exists. Pane discovery is retried on
# every tick, so the uniquely tagged stable pane ID can appear later.
# Usage: start_tab_title_watcher <session> <project> <title-mode> <tmux> <descriptor> <config-dir>
start_tab_title_watcher() {
  local session_name="${1-}" project_name="${2-}" title_mode="${3-}"
  local tmux_cmd="${4-}" descriptor="${5-}" config_dir="${6-}"
  local interval="${WISP_DECK_WATCH_INTERVAL:-0.5}"
  (
    attention_watcher_reset
    while true; do
      attention_watcher_tick "$session_name" "$project_name" "$title_mode" \
        "$tmux_cmd" "$descriptor" "$config_dir"
      sleep "$interval"
    done
  ) >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &
  _TAB_TITLE_WATCHER_PID=$!
}

# Stop and reap only the consumer. The attention runtime owns its files.
stop_tab_title_watcher() {
  if [ -n "$_TAB_TITLE_WATCHER_PID" ]; then
    kill "$_TAB_TITLE_WATCHER_PID" 2>/dev/null || true
    wait "$_TAB_TITLE_WATCHER_PID" 2>/dev/null || true
    _TAB_TITLE_WATCHER_PID=""
  fi
}
