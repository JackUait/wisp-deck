#!/bin/bash
# Tab title watcher — detects AI tool waiting state, updates terminal tab title.
# Depends on: tui.sh (set_tab_title, set_tab_title_waiting)

_TAB_TITLE_WATCHER_PID=""

# Read a single key=value line from the Ghost Tab settings file. Echoes the
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

# Repaint EVERY active ghost-tab session's chrome to the theme accent currently
# saved in the settings file. The per-session watcher only reaches sessions whose
# loop was started with the live-theme code, so a theme change misses sessions
# whose watcher predates the feature. This addresses each running session
# externally instead: it enumerates tmux sessions, skips non ghost-tab ones (only
# ghost-tab sessions export GHOST_TAB=1), and resolves each session's accent from
# its own AI tool (the GHOST_TAB_TOOL env captured at launch) so an "auto"/unset
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
    "$tmux_cmd" show-environment -t "$session" GHOST_TAB >/dev/null 2>&1 || continue
    local tool accent
    tool="$("$tmux_cmd" show-environment -t "$session" GHOST_TAB_TOOL 2>/dev/null | cut -d= -f2-)"
    accent="$(get_theme_accent "$(gt_resolve_theme "$theme_pref" "$tool")")"
    apply_session_theme "$tmux_cmd" "$session" "$accent"
  done
}

# Discover the ledger/lazygit pane (top-left: leftmost column, topmost row).
# This is pane 0's *position*, but tmux pane indices are not creation-order
# stable, so resolve it by geometry rather than assuming index 0.
# Usage: discover_ledger_pane <session_name> <tmux_cmd>
discover_ledger_pane() {
  local session_name="$1" tmux_cmd="$2"
  "$tmux_cmd" list-panes -t "$session_name" -F '#{pane_index} #{pane_left} #{pane_top}' 2>/dev/null \
    | sort -k2,2n -k3,3n | head -1 | awk '{print $1}'
}

# Switch a running session's ledger pane between compact (usage ledger) and full
# (lazygit) layout so a mid-session panel_mode change reaches it. Only acts when
# the session's recorded mode (@gt_panel_mode) differs from the requested one, so
# unchanged sessions are never disrupted; legacy sessions with no tag default to
# "compact" (the historical default). Respawns the ledger pane with the new mode's
# command and resizes the AI pane to the mode's width (75% AI for compact, 50%
# for full — mirroring the launch-time split).
# Usage: apply_session_panel_mode <tmux_cmd> <session> <mode> <project_dir> <lib_dir> <lazygit_cmd>
apply_session_panel_mode() {
  local tmux_cmd="$1" session="$2" mode="$3" project_dir="$4" lib_dir="$5" lazygit_cmd="$6"
  local cur
  cur="$("$tmux_cmd" show-options -t "$session" -v @gt_panel_mode 2>/dev/null)"
  [ -z "$cur" ] && cur="compact"
  [ "$cur" = "$mode" ] && return 0

  local pane0_cmd ai_pct
  if [ "$mode" = "compact" ]; then
    pane0_cmd="source \"$lib_dir/compact-view.sh\" && compact_view \"$project_dir\"; exec bash"
    ai_pct=75
  else
    pane0_cmd="$lazygit_cmd; exec bash"
    ai_pct=50
  fi

  local ledger
  ledger="$(discover_ledger_pane "$session" "$tmux_cmd")"
  [ -z "$ledger" ] && return 0
  # Pass the command RAW, exactly as the launch-time new-session does (wrapper.sh).
  # tmux runs it through the same shell, so compact_view's bash-isms still work —
  # and, unlike a bash -c '...' wrapper, a single quote in $project_dir/$lib_dir
  # (e.g. /Users/o'brien) cannot break out and corrupt the command.
  "$tmux_cmd" respawn-pane -k -t "$session:0.$ledger" "$pane0_cmd" 2>/dev/null || return 0

  local ai_pane win_w cells
  ai_pane="$(discover_ai_pane "$session" "$tmux_cmd")"
  win_w="$("$tmux_cmd" display-message -p -t "$session" '#{window_width}' 2>/dev/null)"
  if [ -n "$ai_pane" ] && [ -n "$win_w" ]; then
    cells=$(( win_w * ai_pct / 100 ))
    "$tmux_cmd" resize-pane -t "$session:0.$ai_pane" -x "$cells" 2>/dev/null || true
  fi
  "$tmux_cmd" set-option -t "$session" @gt_panel_mode "$mode" 2>/dev/null || true
}

# Propagate every live-applicable setting to ALL active ghost-tab sessions at
# once — the watcher-age-independent path (a running session's watcher loop is
# frozen at its launch-time code, so it cannot pick up settings that postdate it).
# Called when the Settings menu closes so a change reaches every open window, not
# just newly-launched sessions. Per-session context (AI tool, project dir) comes
# from the env captured at launch.
#
# theme: a plain tmux session property — applied to every session here.
# panel_mode: a structural pane respawn — applied here (single-source, so it is
#   never double-applied by a watcher tick).
# tab_title: a per-terminal OSC escape that only the session's own watcher can
#   emit, so it is NOT handled here; new-code sessions update it live each tick.
# Usage: apply_settings_to_all_sessions <tmux_cmd> <settings_file> [lib_dir] [lazygit_cmd]
apply_settings_to_all_sessions() {
  local tmux_cmd="$1" settings_file="$2"
  local lib_dir="${3:-${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/lib}"
  local lazygit_cmd="${4:-lazygit}"
  local theme_pref panel_mode
  theme_pref="$(read_settings_value "$settings_file" theme)"
  panel_mode="$(read_settings_value "$settings_file" panel_mode)"
  [ -z "$panel_mode" ] && panel_mode="compact"
  # Pipe (not process substitution): wrapper sources libs under bash --posix.
  "$tmux_cmd" list-sessions -F '#{session_name}' 2>/dev/null | while IFS= read -r session; do
    [ -z "$session" ] && continue
    "$tmux_cmd" show-environment -t "$session" GHOST_TAB >/dev/null 2>&1 || continue
    local tool project_dir accent
    tool="$("$tmux_cmd" show-environment -t "$session" GHOST_TAB_TOOL 2>/dev/null | cut -d= -f2-)"
    project_dir="$("$tmux_cmd" show-environment -t "$session" GHOST_TAB_PATH 2>/dev/null | cut -d= -f2-)"
    accent="$(get_theme_accent "$(gt_resolve_theme "$theme_pref" "$tool")")"
    apply_session_theme "$tmux_cmd" "$session" "$accent"
    apply_session_panel_mode "$tmux_cmd" "$session" "$panel_mode" "$project_dir" "$lib_dir" "$lazygit_cmd"
  done
}

# Return the age of the marker file in seconds.
# Usage: marker_age <file>
# Outputs the number of seconds since the file was last modified.
# Returns 1 if the file does not exist or stat fails.
marker_age() {
  local file="$1"
  local now mtime
  now=$(date +%s)
  mtime=$(stat -f %m "$file" 2>/dev/null) || return 1
  echo $(( now - mtime ))
}

# Return success (0) if the captured Claude pane content shows Claude is
# actively working rather than waiting for the user. Claude's TUI renders a
# spinner status line while generating — a gerund with an elapsed timer
# ("Clauding… (7m 56s …"), a live token counter ("↓ 28.1k tokens" or
# "↑ 7.6k tokens" during upload), or an "esc to interrupt" hint. None of
# these are present once Claude is idle.
#
# The patterns are anchored to the spinner's real shape — a token count
# ending in "tokens", or the parenthesized elapsed-time format right after
# the gerund's ellipsis — so idle prose like "Updated files… (12 insertions)"
# or "↓ 5 results below" is NOT mistaken for working (which would silence the
# sound forever). The match is gerund-agnostic and fails safe: a future
# spinner change degrades to over-notify, never to silence.
# Usage: claude_pane_working <content>
claude_pane_working() {
  local content="$1"
  printf '%s\n' "$content" \
    | grep -qE '[↓↑] [0-9]+(\.[0-9]+)?k? tokens|esc to interrupt|… \([0-9]+m [0-9]+s|… \([0-9]+s'
}

# Check if the AI tool is waiting for user input.
# Usage: check_ai_tool_state <ai_tool> <session_name> <tmux_cmd> <marker_file> <pane_index>
# Outputs "waiting" or "active".
check_ai_tool_state() {
  local ai_tool="$1" session_name="$2" tmux_cmd="$3" marker_file="$4"
  local pane_index="${5:-3}"

  if [ "$ai_tool" = "claude" ]; then
    # The Stop hook creates the marker, UserPromptSubmit/PreToolUse remove it.
    # But the marker alone is not enough: the Stop hook also fires during
    # mid-turn "thinking" gaps after tool completions, and when a blocking
    # Stop hook makes Claude continue — leaving the marker present while
    # Claude is still working. Confirm against the live pane: marker present
    # AND no working spinner means Claude is genuinely waiting for the user.
    if [ ! -f "$marker_file" ]; then
      echo "active"
    else
      local content
      content="$("$tmux_cmd" capture-pane -t "$session_name:0.$pane_index" -p 2>/dev/null | tail -n 15 || true)"
      if claude_pane_working "$content"; then
        echo "active"
      else
        echo "waiting"
      fi
    fi
  else
    local content last_line
    content=$("$tmux_cmd" capture-pane -t "$session_name:0.$pane_index" -p 2>/dev/null || true)
    last_line=$(echo "$content" | grep -v '^$' | tail -1)
    if echo "$last_line" | grep -qE '[>$❯]\s*$'; then
      echo "waiting"
    else
      echo "active"
    fi
  fi
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

# Discover the AI tool pane (rightmost pane in the tmux session).
# Usage: discover_ai_pane <session_name> <tmux_cmd>
# Outputs the pane index of the rightmost pane.
discover_ai_pane() {
  local session_name="$1" tmux_cmd="$2"
  "$tmux_cmd" list-panes -t "$session_name" -F '#{pane_index} #{pane_left}' 2>/dev/null \
    | sort -k2 -rn | head -1 | awk '{print $1}'
}

# Start the tab title watcher background loop.
# Usage: start_tab_title_watcher <session_name> <ai_tool> <project_name> <tab_title_setting> <tmux_cmd> <marker_file> [config_dir]
start_tab_title_watcher() {
  local session_name="$1" ai_tool="$2" project_name="$3"
  local tab_title_setting="$4" tmux_cmd="$5" marker_file="$6"
  local config_dir="${7:-}"

  (
    # Find the AI tool pane (rightmost pane in the layout)
    local ai_pane=""
    while [ -z "$ai_pane" ]; do
      ai_pane=$(discover_ai_pane "$session_name" "$tmux_cmd")
      [ -z "$ai_pane" ] && sleep 0.5
    done

    # Model mode mirrors the AI pane's own title; cache the hostname once so we
    # can tell "the model set a title" from tmux's default (the hostname).
    local host
    host=$(hostname 2>/dev/null)

    # Settings are re-read live each tick (below) so a mid-session change in the
    # Settings menu reaches THIS running session — and since every session runs
    # its own watcher, the change lands across all open windows at once. The
    # launch-time arg is the fallback when a value is unset.
    local settings_file=""
    [ -n "$config_dir" ] && settings_file="$config_dir/settings"
    local cur_tab_title="$tab_title_setting"
    local last_tab_title="$tab_title_setting"
    local last_accent=""

    local was_waiting=false
    while true; do
      sleep 0.5

      # Live settings re-read: pick up tab-title mode + theme changes mid-session.
      if [ -n "$settings_file" ] && [ -f "$settings_file" ]; then
        local _tt
        _tt=$(read_settings_value "$settings_file" tab_title)
        [ -n "$_tt" ] && cur_tab_title="$_tt"
        if declare -f gt_resolve_theme >/dev/null 2>&1; then
          local _theme _accent
          _theme=$(read_settings_value "$settings_file" theme)
          _accent=$(get_theme_accent "$(gt_resolve_theme "$_theme" "$ai_tool")")
          # Only touch tmux when the accent actually changed, to avoid a
          # set-option subprocess on every 0.5s tick.
          if [ "$_accent" != "$last_accent" ]; then
            apply_session_theme "$tmux_cmd" "$session_name" "$_accent"
            last_accent="$_accent"
          fi
        fi
      fi

      local state
      state=$(check_ai_tool_state "$ai_tool" "$session_name" "$tmux_cmd" "$marker_file" "$ai_pane")

      # In model mode the AI tool owns the title: read the AI pane's title each
      # poll and mirror it to the tab (falling back to the project name before
      # the model has set one). The waiting/sound logic below still runs.
      if [ "$cur_tab_title" = "model" ]; then
        local pane_title
        pane_title=$("$tmux_cmd" display-message -p -t "$session_name:0.$ai_pane" '#{pane_title}' 2>/dev/null)
        set_tab_title "$(model_tab_title "$pane_title" "$host" "$project_name")"
      fi

      # If the title mode changed mid-session, refresh the tab for the current
      # state immediately (the waiting/active transitions below only fire on a
      # state flip, so without this a mode change would not show until then).
      if [ "$cur_tab_title" != "$last_tab_title" ]; then
        apply_tab_title "$state" "$cur_tab_title" "$project_name" "$ai_tool"
        last_tab_title="$cur_tab_title"
      fi

      if [ "$state" = "waiting" ] && [ "$was_waiting" = false ]; then
        # Debounce: require the marker to persist for ~1s before notifying.
        # check_ai_tool_state already filters active work via the pane
        # spinner, so this only guards the sub-second race where a Stop fires
        # an instant before Claude resumes and the pane reflects it. No
        # cooldown/extended debounce — that delayed genuine notifications.
        local age
        age=$(marker_age "$marker_file") || continue
        if [ "$age" -ge 1 ]; then
          apply_tab_title "waiting" "$cur_tab_title" "$project_name" "$ai_tool"
          if [[ -n "$config_dir" ]]; then
            play_notification_sound "$ai_tool" "$config_dir"
          fi
          was_waiting=true
        fi
      elif [ "$state" = "active" ] && [ "$was_waiting" = true ]; then
        apply_tab_title "active" "$cur_tab_title" "$project_name" "$ai_tool"
        was_waiting=false
      fi
    done
  ) &
  _TAB_TITLE_WATCHER_PID=$!
}

# Stop the tab title watcher and clean up.
# Usage: stop_tab_title_watcher [marker_file]
stop_tab_title_watcher() {
  local marker_file="${1:-}"
  if [ -n "$_TAB_TITLE_WATCHER_PID" ]; then
    kill "$_TAB_TITLE_WATCHER_PID" 2>/dev/null || true
  fi
  if [ -n "$marker_file" ]; then
    rm -f "$marker_file"
    rm -f "${marker_file}-cooldown"
    rm -f "${marker_file}-ask"
  fi
}
