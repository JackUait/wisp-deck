#!/bin/bash
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"

SHARE_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"

# shellcheck source=/dev/null
[ -f "$SHARE_DIR/lib/update.sh" ] && source "$SHARE_DIR/lib/update.sh"

notify_if_update_available
check_for_update "${HOME}/.local/share/ghost-tab"

# Show animated loading screen immediately in interactive mode (no args)
_wrapper_dir_early="$(cd "$(dirname "$0")" && pwd)"
if [ -z "$1" ] && [ -f "$_wrapper_dir_early/lib/loading.sh" ]; then
  # shellcheck disable=SC1091  # Dynamic path
  source "$_wrapper_dir_early/lib/loading.sh"
  # Mirrors AI_TOOL_PREF_FILE (defined after libs load); duplicated here because loading.sh runs before modules
  _ai_tool="$(cat "${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/ai-tool" 2>/dev/null | tr -d '[:space:]')"
  show_loading_screen "${_ai_tool:-}"
fi

# Check if ghost-tab-tui binary is available
if ! command -v ghost-tab-tui &>/dev/null; then
  printf '\033[31mError:\033[0m ghost-tab-tui binary not found.\n' >&2
  printf 'Run \033[1mghost-tab\033[0m to reinstall.\n' >&2
  printf 'Press any key to exit...\n' >&2
  read -rsn1
  exit 1
fi

# Load shared library functions
_WRAPPER_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ ! -d "$_WRAPPER_DIR/lib" ]; then
  printf '\033[31mError:\033[0m Ghost Tab libraries not found at %s/lib\n' "$_WRAPPER_DIR" >&2
  printf 'Run \033[1mghost-tab\033[0m to reinstall.\n' >&2
  printf 'Press any key to exit...\n' >&2
  read -rsn1
  exit 1
fi

_gt_libs=(ai-tools projects process input tui menu-tui project-actions tmux-session settings-json notification-setup tab-title-watcher terminals/registry terminals/adapter session-restore claude-configs)
for _gt_lib in "${_gt_libs[@]}"; do
  if [ ! -f "$_WRAPPER_DIR/lib/${_gt_lib}.sh" ]; then
    printf '\033[31mError:\033[0m Missing library %s/lib/%s.sh\n' "$_WRAPPER_DIR" "$_gt_lib" >&2
    printf 'Run \033[1mghost-tab\033[0m to reinstall.\n' >&2
    printf 'Press any key to exit...\n' >&2
    read -rsn1
    exit 1
  fi
  # shellcheck disable=SC1090  # Dynamic module loading
  source "$_WRAPPER_DIR/lib/${_gt_lib}.sh"
done
unset _gt_libs _gt_lib

TMUX_CMD="$(command -v tmux)"
LAZYGIT_CMD="$(command -v lazygit)"
CLAUDE_CMD="$(command -v claude)"
CODEX_CMD="$(command -v codex)"
if command -v npx &>/dev/null; then
  OPENCODE_CMD="npx opencode-ai@latest"
else
  OPENCODE_CMD=""
fi

# AI tool preference
AI_TOOL_PREF_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/ai-tool"
AI_TOOLS_AVAILABLE=()
[ -n "$CLAUDE_CMD" ] && AI_TOOLS_AVAILABLE+=("claude")
[ -n "$CODEX_CMD" ] && AI_TOOLS_AVAILABLE+=("codex")
[ -n "$OPENCODE_CMD" ] && AI_TOOLS_AVAILABLE+=("opencode")

# Read saved preference, default to first available
SELECTED_AI_TOOL=""
if [ -f "$AI_TOOL_PREF_FILE" ]; then
  SELECTED_AI_TOOL="$(cat "$AI_TOOL_PREF_FILE" 2>/dev/null | tr -d '[:space:]')"
fi
# Validate saved preference is still installed
validate_ai_tool "$AI_TOOL_PREF_FILE"

# Load user projects from config file if it exists
PROJECTS_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/projects"

# Boot id (stable per uptime) for once-per-boot restore.
GHOST_TAB_BOOT_ID="$(current_boot_id)"

# Restore mode: wrapper.sh --restore <project_path> <ai_tool>
RESTORE_MODE=0
_restore_parsed="$(parse_restore_flag "$@")"
if [ -n "$_restore_parsed" ]; then
  RESTORE_MODE=1
  RESTORE_PATH="${_restore_parsed%%|*}"
  RESTORE_TOOL="${_restore_parsed##*|}"
fi

# Select working directory
if [ "$RESTORE_MODE" -eq 1 ]; then
  cd "$RESTORE_PATH" || exit 1
  PROJECT_NAME="$(basename "$RESTORE_PATH")"
  SELECTED_AI_TOOL="$RESTORE_TOOL"
elif [ -n "$1" ] && [ -d "$1" ]; then
  cd "$1" || exit 1
  shift
elif [ -z "$1" ]; then
  # First interactive launch after a reboot: reopen previous tabs.
  maybe_restore_session "$SHARE_DIR" "$GHOST_TAB_BOOT_ID" "$0"

  # Use TUI for project selection
  printf '\033]0;👻 Ghost Tab\007'

  # Stop loading animation before TUI takes over
  type stop_loading_screen &>/dev/null && stop_loading_screen

  while true; do
    if select_project_interactive "$PROJECTS_FILE"; then
      # Update AI tool if user cycled it in the menu (for all actions)
      if [[ -n "${_selected_ai_tool:-}" ]]; then
        SELECTED_AI_TOOL="$_selected_ai_tool"
      fi
      # shellcheck disable=SC2154
      case "$_selected_project_action" in
        select-project|open-once)
          PROJECT_NAME="$_selected_project_name"
          # shellcheck disable=SC2154
          cd "$_selected_project_path" || exit 1
          break
          ;;
        plain-terminal)
          exec "$SHELL"
          ;;
        add-worktree)
          # Loop back to menu — worktrees refresh on reload
          continue
          ;;
        *)
          # settings or unknown — loop back to menu
          continue
          ;;
      esac
    else
      # User quit (ESC/Ctrl-C)
      exit 0
    fi
  done
fi

PROJECT_DIR="$(pwd)"
export PROJECT_DIR
export PROJECT_NAME="${PROJECT_NAME:-$(basename "$PROJECT_DIR")}"
SESSION_NAME="dev-${PROJECT_NAME}-$$"

# Set terminal/tab title based on tab_title setting
_tab_title_setting="full"
_settings_file="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab/settings"
if [ -f "$_settings_file" ]; then
  _saved_tab_title=$(grep '^tab_title=' "$_settings_file" 2>/dev/null | cut -d= -f2)
  if [ -n "$_saved_tab_title" ]; then
    _tab_title_setting="$_saved_tab_title"
  fi
fi
if [ "$_tab_title_setting" = "full" ]; then
  set_tab_title "$PROJECT_NAME" "$SELECTED_AI_TOOL"
else
  set_tab_title "$PROJECT_NAME"
fi

# Tab title waiting indicator
GHOST_TAB_MARKER_FILE="/tmp/ghost-tab-waiting-$$"
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  _claude_settings="${HOME}/.claude/settings.json"
  add_waiting_indicator_hooks "$_claude_settings" >/dev/null
fi

# Background watcher: switch to Claude pane once it's ready
(
  while true; do
    sleep 0.5
    content=$("$TMUX_CMD" capture-pane -t "$SESSION_NAME:0.1" -p 2>/dev/null)
    # All three tools show a prompt character when ready
    if echo "$content" | grep -qE '[>$❯]'; then
      "$TMUX_CMD" select-pane -t "$SESSION_NAME:0.1"
      break
    fi
  done
) &
WATCHER_PID=$!

cleanup() {
  stop_tab_title_watcher "$GHOST_TAB_MARKER_FILE"
  [ -n "${HEARTBEAT_PID:-}" ] && kill_tree "$HEARTBEAT_PID" TERM 2>/dev/null || true
  # Remove waiting indicator hooks if no other Ghost Tab sessions are running
  if [ "$SELECTED_AI_TOOL" = "claude" ]; then
    # Clean up orphaned markers and cooldown files from dead sessions (e.g., after SIGKILL)
    for marker in /tmp/ghost-tab-waiting-*; do
      [ -f "$marker" ] || continue
      # Skip cooldown files — they'll be cleaned with their parent marker
      [[ "$marker" == *-cooldown ]] && continue
      [[ "$marker" == *-ask ]] && continue
      local pid="${marker##*-}"
      if ! kill -0 "$pid" 2>/dev/null; then
        rm -f "$marker" "${marker}-cooldown" "${marker}-ask"
      fi
    done
    if ! ls /tmp/ghost-tab-waiting-* &>/dev/null; then
      remove_waiting_indicator_hooks "${HOME}/.claude/settings.json" >/dev/null 2>&1 || true
    fi
  fi
  cleanup_tmux_session "$SESSION_NAME" "$WATCHER_PID" "$TMUX_CMD"
}
trap cleanup EXIT HUP TERM INT

if [ "$RESTORE_MODE" -eq 1 ]; then
  export GHOST_TAB_RESUME=1
fi

# Resolve active Claude config (settings file) and export for build_ai_launch_cmd.
GHOST_TAB_CLAUDE_SETTINGS=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  _gt_cfg_root="${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"
  GHOST_TAB_CLAUDE_SETTINGS="$(resolve_claude_config_path "$_gt_cfg_root/claude-configs" "$_gt_cfg_root/claude-config")"
fi
export GHOST_TAB_CLAUDE_SETTINGS

# Build the AI tool launch command
case "$SELECTED_AI_TOOL" in
  codex|opencode)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$CLAUDE_CMD" "$CODEX_CMD" "$OPENCODE_CMD" "$PROJECT_DIR")"
    ;;
  *)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$CLAUDE_CMD" "$CODEX_CMD" "$OPENCODE_CMD" "$*")"
    ;;
esac

# Start tab title watcher before tmux (which blocks until session ends)
start_tab_title_watcher "$SESSION_NAME" "$SELECTED_AI_TOOL" "$PROJECT_NAME" "$_tab_title_setting" "$TMUX_CMD" "$GHOST_TAB_MARKER_FILE" "${XDG_CONFIG_HOME:-$HOME/.config}/ghost-tab"

# Session-restore snapshot: stamp metadata into the tmux session env via -e
# flags on new-session (below), and run a heartbeat that re-derives the
# snapshot from all alive Ghost Tab sessions.
GHOST_TAB_TERMINAL="$(load_terminal_preference "$SHARE_DIR/terminal")"
GHOST_TAB_SNAPSHOT="$SHARE_DIR/last-session"
(
  while true; do
    write_session_snapshot "$TMUX_CMD" "$GHOST_TAB_SNAPSHOT"
    sleep 10
  done
) &
HEARTBEAT_PID=$!

"$TMUX_CMD" new-session -s "$SESSION_NAME" -e "PATH=$PATH" -e "GHOST_TAB_MARKER_FILE=$GHOST_TAB_MARKER_FILE" -e "GHOST_TAB=1" -e "GHOST_TAB_BOOT=$GHOST_TAB_BOOT_ID" -e "GHOST_TAB_PROJECT=$PROJECT_NAME" -e "GHOST_TAB_PATH=$PROJECT_DIR" -e "GHOST_TAB_TOOL=$SELECTED_AI_TOOL" -e "GHOST_TAB_TERMINAL=$GHOST_TAB_TERMINAL" -c "$PROJECT_DIR" \
  "$LAZYGIT_CMD; exec bash" \; \
  set-option status-left " ⬡ ${PROJECT_NAME} " \; \
  set-option status-left-style "fg=white,bg=colour236,bold" \; \
  set-option status-style "bg=colour235" \; \
  set-option status-right "" \; \
  set-option set-titles off \; \
  set-option exit-unattached on \; \
  split-window -h -p 50 -c "$PROJECT_DIR" \
  "$AI_LAUNCH_CMD; exec bash" \; \
  select-pane -t 0 \; \
  split-window -v -p 30 -c "$PROJECT_DIR" \; \
  select-pane -t 2
