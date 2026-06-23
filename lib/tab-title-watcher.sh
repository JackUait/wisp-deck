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

# Check if the AI tool is waiting for user input.
# Usage: check_ai_tool_state <ai_tool> <session_name> <tmux_cmd> <marker_file> <pane_index>
# Outputs "waiting" or "active".
check_ai_tool_state() {
  local ai_tool="$1" session_name="$2" tmux_cmd="$3" marker_file="$4"
  local pane_index="${5:-3}"

  if [ "$ai_tool" = "claude" ]; then
    # Claude uses marker-file-only detection.
    # Stop hook creates the marker (Claude stopped generating).
    # UserPromptSubmit hook removes it (user answered).
    # PreToolUse hook also removes it (Claude is calling tools).
    if [ -f "$marker_file" ]; then
      echo "waiting"
    else
      echo "active"
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
        # Debounce: only notify if marker has existed long enough.
        # After tool completions (subagent results, file writes, etc.),
        # Claude thinks for 2-15+ seconds before acting. A PostToolUse
        # hook creates a cooldown file to signal this window. When the
        # cooldown file exists and is < 30s old, we use a longer 15s
        # debounce to avoid false notifications. Otherwise, use the
        # normal 1s debounce for genuine idle detection.
        local age debounce_threshold=1
        age=$(marker_age "$marker_file") || continue
        local ask_file="${marker_file}-ask"
        if [ -f "$ask_file" ]; then
          # AskUserQuestion detected — definitive user-input-needed signal.
          # Use short debounce regardless of cooldown.
          debounce_threshold=1
        else
          local cooldown_file="${marker_file}-cooldown"
          if [ -f "$cooldown_file" ]; then
            local cooldown_age
            cooldown_age=$(marker_age "$cooldown_file") || true
            if [ -n "$cooldown_age" ] && [ "$cooldown_age" -lt 30 ]; then
              debounce_threshold=15
            fi
          fi
        fi
        if [ "$age" -ge "$debounce_threshold" ]; then
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
