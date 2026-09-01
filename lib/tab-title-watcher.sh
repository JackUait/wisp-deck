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
  bytes="$(wc -c 2>/dev/null <"$file" | tr -d '[:space:]')" || return 1
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
#   state: "waiting" (needs attention — bell emoji prefixed), "seen" (still
#          needs attention, but the user has looked at the tab without
#          answering — eyes emoji prefixed), "error" (the turn failed — cross),
#          "warning" (the session may be in trouble — warning sign) or
#          "active" (plain)
#   mode:  "full" (project · tool), "project" (project only), or
#          "model" (leave the AI tool's own title alone — it set the title
#          itself; the per-tick model re-emit carries the cue instead)
apply_tab_title() {
  local state="$1" mode="$2" project="$3" tool="$4"
  case "$mode" in
    model)
      # The model set the tab title to describe its task — don't clobber it.
      return 0
      ;;
    full)
      case "$state" in
        waiting) set_tab_title_waiting "$project" "$tool" ;;
        seen) set_tab_title_seen "$project" "$tool" ;;
        error) set_tab_title_error "$project" "$tool" ;;
        warning) set_tab_title_warning "$project" "$tool" ;;
        *) set_tab_title "$project" "$tool" ;;
      esac
      ;;
    *)
      case "$state" in
        waiting) set_tab_title_waiting "$project" ;;
        seen) set_tab_title_seen "$project" ;;
        error) set_tab_title_error "$project" ;;
        warning) set_tab_title_warning "$project" ;;
        *) set_tab_title "$project" ;;
      esac
      ;;
  esac
}

# Discover the unique pane tagged by the controller as the AI pane.
# Usage: discover_ai_pane <session_name> <tmux_cmd>
# Outputs its stable tmux pane ID (for example, %42).
discover_ai_pane() {
  local session_name="${1-}" tmux_cmd="${2-}" listing line pane flag extra
  local found="" count=0 suffix
  listing="$("$tmux_cmd" list-panes -t "$session_name" -F $'#{pane_id}\t#{@gt_ai}' 2>/dev/null)" || return 1
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

# True when the terminal tab hosting this session currently holds the user's
# focus. One Ghostty tab is exactly one client of the session, and tmux marks a
# client "focused" from the terminal's own focus reporting — which is why the
# wrapper turns `focus-events on` before attaching. Where focus reporting is
# unavailable tmux calls every client focused, so this degrades to "the user has
# seen it", never to a bell that outlives the visit.
# Usage: attention_watcher_tab_focused <session> <tmux_cmd>
attention_watcher_tab_focused() {
  local session_name="${1-}" tmux_cmd="${2-}" flags
  flags="$("$tmux_cmd" list-clients -t "$session_name" -F '#{client_flags}' 2>/dev/null)" || return 1
  case ",$flags," in
    *,focused,*) return 0 ;;
  esac
  return 1
}

# Reset the reducer state. Public for deterministic shell tests; production
# calls it once inside the watcher subshell.
# The libraries follow_agent_checkout needs, sourced in a FRESH bash: the
# wrapper may be running under `bash --posix` (Ghostty's /bin/sh -c launch),
# where the process substitution these functions read git through is disabled.
# shellcheck disable=SC2016  # $1..$4 are the CHILD bash's positional arguments:
# the paths reach it as arguments, never interpolated into the program text.
_ATTENTION_WATCH_FOLLOW_SCRIPT='
. "$1/statusline.sh"
. "$1/claude-accounts.sh"
. "$1/tmux-session.sh"
. "$1/account-switch.sh"
follow_agent_checkout "$2" "$3" "$4"
'

# attention_watcher_follow_agent <tmux_cmd> <state-file> <relaunch-file> <lib-dir>
# Move the tab to the checkout its agent moved into. The Claude attention
# runtime publishes the supervised session's working directory beside its state
# file; a session whose agent entered a git worktree is simply one whose
# published directory stopped matching the checkout the tab is pointed at.
#
# This runs on the attention tick, so the steady state must not fork: the two
# reads are builtins, and only a directory that differs from the session's own
# reaches the shell that validates and applies it. Each distinct directory is
# attempted ONCE — a refused one (the agent cd'd out of the project entirely)
# would otherwise spawn a shell twice a second for as long as the agent stayed
# there. Convergence clears the memo, so re-entering a worktree still follows.
attention_watcher_follow_agent() {
  local tmux_cmd="${1-}" state_file="${2-}" relaunch_file="${3-}" lib_dir="${4-}"
  local cwd="" project_dir="" line

  [ -n "$tmux_cmd" ] && [ -n "$state_file" ] || return 0
  [ -n "$relaunch_file" ] && [ -f "$relaunch_file" ] || return 0
  [ -n "$lib_dir" ] && [ -d "$lib_dir" ] || return 0

  # Grouped so stderr is closed BEFORE the open is attempted: bash applies
  # redirections left to right, and this shell's stderr is the pane the agent
  # paints on. The generation directory is removed out from under this read
  # whenever a launch rotates.
  { IFS= read -r cwd < "${state_file%/*}/cwd"; } 2>/dev/null || cwd=""
  # Anything but a plain absolute path is a truncated or junk read, never a
  # directory to respawn panes into.
  case "$cwd" in
    /*) ;;
    *) return 0 ;;
  esac
  case "$cwd" in
    *[[:space:]]*|*';'*|*'$'*|*'`'*) return 0 ;;
  esac

  {
    while IFS= read -r line; do
      case "$line" in
        project_dir=*)
          project_dir="${line#project_dir=}"
          break
          ;;
      esac
    done < "$relaunch_file"
  } 2>/dev/null
  [ -n "$project_dir" ] || return 0

  if [ "$cwd" = "$project_dir" ]; then
    _ATTENTION_WATCH_FOLLOW_TRIED=""
    return 0
  fi
  [ "$cwd" = "$_ATTENTION_WATCH_FOLLOW_TRIED" ] && return 0
  _ATTENTION_WATCH_FOLLOW_TRIED="$cwd"

  # fd 1 and 2 are the terminal the agent paints on — see wrapper.sh.
  bash -c "$_ATTENTION_WATCH_FOLLOW_SCRIPT" -- \
    "$lib_dir" "$tmux_cmd" "$relaunch_file" "$cwd" >/dev/null 2>&1 || true
  return 0
}

attention_watcher_reset() {
  _ATTENTION_WATCH_LAST_GENERATION=""
  _ATTENTION_WATCH_LAST_SEQUENCE=""
  _ATTENTION_WATCH_LAST_VALID_PHASE=""
  _ATTENTION_WATCH_LAST_VALID_REASON=""
  _ATTENTION_WATCH_LAST_PRESENT_PHASE=""
  _ATTENTION_WATCH_LAST_TOOL=""
  _ATTENTION_WATCH_LAST_TITLE_MODE=""
  _ATTENTION_WATCH_LAST_TITLE_STATE=""
  _ATTENTION_WATCH_SEEN=""
  _ATTENTION_WATCH_LAST_ACCENT=""
  # "-" is "not observed yet", which no tab bar mode can be — the first tick
  # adopts what it finds instead of treating it as a change.
  _ATTENTION_WATCH_LAST_TAB_BAR="-"
  _ATTENTION_WATCH_FOLLOW_TRIED=""
  _ATTENTION_WATCH_QUIET_TICKS=0
  _ATTENTION_WATCH_EVER_VALID=""
  # Ticks of unreadable state before the tab warns. At the 0.5s interval the
  # default is ~30s, long enough that a routine missed read never shows.
  _ATTENTION_WATCH_QUIET_LIMIT="${WISP_DECK_WATCH_QUIET_TICKS:-60}"
  case "$_ATTENTION_WATCH_QUIET_LIMIT" in
    '' | *[!0-9]* | 0) _ATTENTION_WATCH_QUIET_LIMIT=60 ;;
  esac
  _ATTENTION_WATCH_HOST="$(hostname 2>/dev/null)"
}

# Consume one snapshot. Missing, malformed, stale, or regressed state becomes
# unknown and never advances alert dedupe.
# Usage: attention_watcher_tick <session> <project> <fallback-title-mode> <tmux>
#          <descriptor> <config-dir> [<relaunch-file> <lib-dir>]
attention_watcher_tick() {
  local session_name="${1-}" project_name="${2-}" fallback_title="${3-}"
  local tmux_cmd="${4-}" descriptor="${5-}" config_dir="${6-}"
  local relaunch_file="${7-}" lib_dir="${8-}"
  local generation="" sequence="" phase="unknown" reason="-" tool=""
  local snapshot_valid=0 tuple_new=0 title_state="active" keep_state="active"
  local previous_tool="$_ATTENTION_WATCH_LAST_TOOL"
  local settings_file="" current_title="$fallback_title" current_pane=""
  local saved_title="" theme="" accent="" tab_bar=""

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
    attention_watcher_follow_agent "$tmux_cmd" "$_ATTENTION_WATCH_SNAPSHOT_STATE" \
      "$relaunch_file" "$lib_dir"
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

  # A session that reported fine and then went quiet is how a stalled adapter
  # looks from here, so it earns the warning cue — but only when SUSTAINED, and
  # never before the first valid read: one missed read is routine, and a session
  # that has never reported is a launch in progress, not a fault.
  if [ "$snapshot_valid" -eq 1 ]; then
    _ATTENTION_WATCH_EVER_VALID=1
    _ATTENTION_WATCH_QUIET_TICKS=0
  else
    _ATTENTION_WATCH_QUIET_TICKS=$((_ATTENTION_WATCH_QUIET_TICKS + 1))
  fi

  [ -n "$tool" ] && _ATTENTION_WATCH_LAST_TOOL="$tool"

  [ -n "$config_dir" ] && settings_file="$config_dir/settings"
  if [ -n "$settings_file" ] && [ -f "$settings_file" ]; then
    saved_title="$(read_settings_value "$settings_file" tab_title)"
    [ -n "$saved_title" ] && current_title="$saved_title"
    theme="$(read_settings_value "$settings_file" theme)"
    tab_bar="$(read_settings_value "$settings_file" tab_bar)"
  fi
  if [ -n "$tool" ] \
     && declare -f gt_resolve_theme >/dev/null 2>&1 \
     && declare -f get_theme_accent >/dev/null 2>&1; then
    accent="$(get_theme_accent "$(gt_resolve_theme "$theme" "$tool")")"
    if [ -n "$accent" ] && [ "$accent" != "$_ATTENTION_WATCH_LAST_ACCENT" ]; then
      apply_session_theme "$tmux_cmd" "$session_name" "$accent"
      _ATTENTION_WATCH_LAST_ACCENT="$accent"
    fi
  fi

  current_pane="$(discover_ai_pane "$session_name" "$tmux_cmd")" || current_pane=""

  case "$phase" in
    attention)
      # Sticky for the whole waiting turn: the user looked once, and wandering
      # off again does not un-see it. Cleared below when the turn ends — an
      # `unknown` (a state read that was missing, malformed or stale) is not a
      # turn ending, so a transient miss must never re-ring a seen bell.
      if [ "$reason" = "error" ]; then
        # Checked before the seen swap and left set afterwards: the swap says
        # "you know about this", which is not the same as fixed.
        title_state="error"
      elif [ "$_ATTENTION_WATCH_SEEN" = "1" ] \
         || attention_watcher_tab_focused "$session_name" "$tmux_cmd"; then
        _ATTENTION_WATCH_SEEN=1
        title_state="seen"
      else
        title_state="waiting"
      fi
      keep_state="waiting"
      ;;
    ready)
      keep_state="waiting"
      _ATTENTION_WATCH_SEEN=""
      ;;
    working)
      keep_state="active"
      _ATTENTION_WATCH_SEEN=""
      ;;
    unknown)
      keep_state="active"
      ;;
  esac

  # Never over a cue the phase already earned: a tab waiting on the user has
  # something specific to say, and the silence is only a guess.
  if [ "$title_state" = "active" ] \
     && [ "$_ATTENTION_WATCH_EVER_VALID" = "1" ] \
     && [ "$_ATTENTION_WATCH_QUIET_TICKS" -ge "$_ATTENTION_WATCH_QUIET_LIMIT" ]; then
    title_state="warning"
  fi

  if [ -n "$config_dir" ] && declare -f keep_awake_tick >/dev/null 2>&1; then
    keep_awake_tick "$config_dir" "$session_name" "$$" "$keep_state" || true
  fi

  if [ "$current_title" = "model" ] && [ -n "$current_pane" ]; then
    local pane_title model_title
    pane_title="$("$tmux_cmd" display-message -p -t "$current_pane" '#{pane_title}' 2>/dev/null)"
    model_title="$(model_tab_title "$pane_title" "$_ATTENTION_WATCH_HOST" "$project_name")"
    case "$title_state" in
      waiting) set_tab_title_waiting "$model_title" ;;
      seen) set_tab_title_seen "$model_title" ;;
      error) set_tab_title_error "$model_title" ;;
      warning) set_tab_title_warning "$model_title" ;;
      *) set_tab_title "$model_title" ;;
    esac
  fi

  # The bell → eyes swap moves no phase, tool or mode, so the rendered state is
  # part of the guard or the tab keeps ringing at a user who already looked.
  if [ "$phase" != "$_ATTENTION_WATCH_LAST_PRESENT_PHASE" ] \
     || [ "$title_state" != "$_ATTENTION_WATCH_LAST_TITLE_STATE" ] \
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

  # The tab bar's large chips render per-window options, and this is what keeps
  # them current: a tab's title and the progress of its turn are state that
  # becomes valid LATER (the agent has not named the turn when the window is
  # created), so they are re-resolved every tick rather than loaded once. Absent
  # setting means large, matching tab_view_mode; compact skips the pane capture
  # entirely because nothing renders it.
  [ "$tab_bar" = "compact" ] || tab_bar="large"
  if [ "$tab_bar" != "compact" ] \
     && declare -f tab_view_stamp_windows >/dev/null 2>&1; then
    tab_view_stamp_windows "$tmux_cmd" "$session_name" "$project_name" \
      "$_ATTENTION_WATCH_HOST"
  fi

  # The bar's FORMAT is written once, by the launch chain, so a mode changed in
  # Settings would otherwise not reach a window that is already open. The first
  # tick only records what it found: the launch chain already drew that bar, and
  # refreshing on every session's first tick is churn nobody sees.
  if [ "$_ATTENTION_WATCH_LAST_TAB_BAR" != "-" ] \
     && [ "$_ATTENTION_WATCH_LAST_TAB_BAR" != "$tab_bar" ] \
     && declare -f tab_view_refresh_bar >/dev/null 2>&1; then
    tab_view_refresh_bar "$tmux_cmd" "${WISP_DECK_LIB_DIR:-}" "$session_name"
  fi
  _ATTENTION_WATCH_LAST_TAB_BAR="$tab_bar"

  _ATTENTION_WATCH_LAST_PRESENT_PHASE="$phase"
  _ATTENTION_WATCH_LAST_TITLE_STATE="$title_state"
  _ATTENTION_WATCH_LAST_TITLE_MODE="$current_title"
}

# Start the semantic consumer before tmux exists. Pane discovery is retried on
# every tick, so the uniquely tagged stable pane ID can appear later.
# Usage: start_tab_title_watcher <session> <project> <title-mode> <tmux>
#          <descriptor> <config-dir> [<relaunch-file> <lib-dir>]
start_tab_title_watcher() {
  local session_name="${1-}" project_name="${2-}" title_mode="${3-}"
  local tmux_cmd="${4-}" descriptor="${5-}" config_dir="${6-}"
  local relaunch_file="${7-}" lib_dir="${8-}"
  local interval="${WISP_DECK_WATCH_INTERVAL:-0.5}"
  (
    attention_watcher_reset
    while true; do
      attention_watcher_tick "$session_name" "$project_name" "$title_mode" \
        "$tmux_cmd" "$descriptor" "$config_dir" "$relaunch_file" "$lib_dir"
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
