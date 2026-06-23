#!/bin/bash
# Tab title watcher — detects AI tool waiting state, updates terminal tab title.
# Depends on: tui.sh (set_tab_title, set_tab_title_waiting)

_TAB_TITLE_WATCHER_PID=""

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
# ("Clauding… (7m 56s …"), a live token counter ("↓ 28.1k tokens"), or an
# "esc to interrupt" hint. None of these are present once Claude is idle.
# The match is gerund-agnostic and fails safe: if a future Claude Code build
# changes the spinner, detection simply stops suppressing (degrades to the
# old over-notify behavior) rather than going silent.
# Usage: claude_pane_working <content>
claude_pane_working() {
  local content="$1"
  printf '%s\n' "$content" | grep -qE '↓ [0-9]|esc to interrupt|… \([0-9]'
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

# Echo the tmux set-titles value ("on"/"off") for the given tab title mode.
# Usage: tmux_set_titles_for_mode <mode>
#   model -> "on":  let the AI tool's own pane title flow through to the tab.
#   else  -> "off": Ghost Tab's watcher owns the title, so stop tmux from
#                   overwriting it.
tmux_set_titles_for_mode() {
  case "$1" in
    model) echo "on" ;;
    *) echo "off" ;;
  esac
}

# Write the terminal tab title for the given state, honoring the title mode.
# Usage: apply_tab_title <state> <mode> <project> <tool>
#   state: "waiting" (idle, ● prefix) or "active"
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

    local was_waiting=false
    while true; do
      sleep 0.5
      local state
      state=$(check_ai_tool_state "$ai_tool" "$session_name" "$tmux_cmd" "$marker_file" "$ai_pane")

      if [ "$state" = "waiting" ] && [ "$was_waiting" = false ]; then
        # Debounce: require the marker to persist for ~1s before notifying.
        # check_ai_tool_state already filters active work via the pane
        # spinner, so this only guards the sub-second race where a Stop fires
        # an instant before Claude resumes and the pane reflects it. No
        # cooldown/extended debounce — that delayed genuine notifications.
        local age
        age=$(marker_age "$marker_file") || continue
        if [ "$age" -ge 1 ]; then
          apply_tab_title "waiting" "$tab_title_setting" "$project_name" "$ai_tool"
          if [[ -n "$config_dir" ]]; then
            play_notification_sound "$ai_tool" "$config_dir"
          fi
          was_waiting=true
        fi
      elif [ "$state" = "active" ] && [ "$was_waiting" = true ]; then
        apply_tab_title "active" "$tab_title_setting" "$project_name" "$ai_tool"
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
