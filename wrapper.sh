#!/bin/bash
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"

SHARE_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
_wrapper_dir_early="$(cd "$(dirname "$0")" && pwd)"

# Restore-storm participant snapshot — taken FIRST, before the update check
# can delay it: a launch that starts while a current-boot restore queue is
# draining belongs to the restore chain (a chained Cmd+T tab, or a window
# macOS resume reopened after a crash). If it later pops an empty queue it is
# surplus and closes instead of showing the picker (see the interactive
# branch below).
_restore_participant=0
_wd_launch_epoch="$(date +%s)"
if [ -z "$1" ] && [ -f "$_wrapper_dir_early/lib/session-restore.sh" ]; then
  # shellcheck disable=SC1091  # Dynamic path
  source "$_wrapper_dir_early/lib/session-restore.sh"
  restore_queue_active "$SHARE_DIR" "$(current_boot_id)" && _restore_participant=1
fi

# shellcheck source=/dev/null
[ -f "$SHARE_DIR/lib/update.sh" ] && source "$SHARE_DIR/lib/update.sh"

notify_if_update_available
check_for_update "${HOME}/.local/share/wisp-deck"

# Show animated loading screen immediately in interactive mode (no args)
if [ -z "$1" ] && [ -f "$_wrapper_dir_early/lib/loading.sh" ]; then
  # shellcheck disable=SC1091  # Dynamic path
  source "$_wrapper_dir_early/lib/loading.sh"
  # shellcheck disable=SC1091  # Dynamic path
  [ -f "$_wrapper_dir_early/lib/theme.sh" ] && source "$_wrapper_dir_early/lib/theme.sh"
  # Mirrors AI_TOOL_PREF_FILE (defined after libs load); duplicated here because loading.sh runs before modules
  _ai_tool="$(cat "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/ai-tool" 2>/dev/null | tr -d '[:space:]')"
  # Honor a user-chosen theme preset for the splash (falls back to the tool hue).
  _theme_pref="$(grep '^theme=' "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
  if declare -f gt_resolve_theme >/dev/null 2>&1; then
    _splash_palette="$(get_theme_palette "$(gt_resolve_theme "$_theme_pref" "${_ai_tool:-}")")"
  fi
  show_loading_screen "${_ai_tool:-}" "${_splash_palette:-}"
fi

# Check if wisp-deck-tui binary is available
if ! command -v wisp-deck-tui &>/dev/null; then
  printf '\033[31mError:\033[0m wisp-deck-tui binary not found.\n' >&2
  printf 'Run \033[1mwisp-deck\033[0m to reinstall.\n' >&2
  printf 'Press any key to exit...\n' >&2
  read -rsn1
  exit 1
fi

# Load shared library functions
_WRAPPER_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ ! -d "$_WRAPPER_DIR/lib" ]; then
  printf '\033[31mError:\033[0m Wisp Deck libraries not found at %s/lib\n' "$_WRAPPER_DIR" >&2
  printf 'Run \033[1mwisp-deck\033[0m to reinstall.\n' >&2
  printf 'Press any key to exit...\n' >&2
  read -rsn1
  exit 1
fi

_gt_libs=(theme ai-tools projects process input tui menu-tui project-actions tmux-session settings-json notification-setup tab-title-watcher terminals/ghostty session-restore claude-configs claude-accounts claude-shared-settings auto-switch account-switch compact-view screenshot spare-tabs)
for _gt_lib in "${_gt_libs[@]}"; do
  if [ ! -f "$_WRAPPER_DIR/lib/${_gt_lib}.sh" ]; then
    printf '\033[31mError:\033[0m Missing library %s/lib/%s.sh\n' "$_WRAPPER_DIR" "$_gt_lib" >&2
    printf 'Run \033[1mwisp-deck\033[0m to reinstall.\n' >&2
    printf 'Press any key to exit...\n' >&2
    read -rsn1
    exit 1
  fi
  # shellcheck disable=SC1090  # Dynamic module loading
  source "$_WRAPPER_DIR/lib/${_gt_lib}.sh"
done
unset _gt_libs _gt_lib

# Pay the TUI binary's first-run Gatekeeper assessment now, in the background,
# so the first modal open (file-list diff / account switcher) doesn't stall.
warm_tui_binary

TMUX_CMD="$(command -v tmux)"
CLAUDE_CMD="$(command -v claude)"
OPENCODE_CMD="$(resolve_opencode_cmd)"

# AI tool preference
AI_TOOL_PREF_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/ai-tool"
AI_TOOLS_AVAILABLE=()
[ -n "$CLAUDE_CMD" ] && AI_TOOLS_AVAILABLE+=("claude")
[ -n "$OPENCODE_CMD" ] && AI_TOOLS_AVAILABLE+=("opencode")

# Read saved preference, default to first available
SELECTED_AI_TOOL=""
if [ -f "$AI_TOOL_PREF_FILE" ]; then
  SELECTED_AI_TOOL="$(cat "$AI_TOOL_PREF_FILE" 2>/dev/null | tr -d '[:space:]')"
fi
# Validate saved preference is still installed
validate_ai_tool "$AI_TOOL_PREF_FILE"

# Load user projects from config file if it exists
PROJECTS_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/projects"

# Boot id (stable per uptime) for once-per-boot restore.
WISP_DECK_BOOT_ID="$(current_boot_id)"

# Unify conversation state across Claude logins BEFORE the restore gate: merge
# every account's locally-recorded transcripts/history into ~/.claude and link
# the accounts to that shared store. Without this, switching the active login
# between boots split the history into per-account stores — /resume "lost" the
# other store's sessions and post-reboot restore resumed the wrong conversation
# (queue-build validates sids against ~/.claude while the restored tab may
# launch under an account's isolated CLAUDE_CONFIG_DIR).
sync_all_claude_accounts_state "$HOME/.claude" "$SHARE_DIR/claude-accounts"

# Set when this window restores a prior-boot session from the restore queue;
# makes the AI tool resume its conversation (WISP_DECK_RESUME).
RESTORE_MODE=0

# Select working directory
if [ -n "$1" ] && [ -d "$1" ]; then
  cd "$1" || exit 1
  shift
else
  # Interactive launch. (Stale args from pre-fix Ghostty instances that still
  # carry "--restore <path> <tool>" land here too — they must never force a
  # project open, that was the duplicated-tabs bug.)
  # The first interactive launch of a new boot builds the restore queue; every
  # interactive launch consumes one pending entry, so prior-boot sessions come
  # back as ordered tabs of this window instead of separate windows.
  maybe_restore_session "$SHARE_DIR" "$WISP_DECK_BOOT_ID"
  _queue_entry="$(restore_queue_pop "$SHARE_DIR" "$WISP_DECK_BOOT_ID")"
  # Skip entries whose project directory no longer exists, and — last line of
  # defense against duplicate tabs — entries whose conversation is already
  # open in an alive session (a re-queued entry from any upstream failure).
  while [ -n "$_queue_entry" ] && ! restore_entry_wanted "$TMUX_CMD" "$_queue_entry"; do
    _queue_entry="$(restore_queue_pop "$SHARE_DIR" "$WISP_DECK_BOOT_ID")"
  done
  if [ -n "$_queue_entry" ]; then
    # Open the next tab immediately so the chain completes quickly while this
    # window continues its own setup.
    restore_advance "$SHARE_DIR"
    RESTORE_MODE=1
    IFS='|' read -r _q_path _q_tool _q_sid _q_layout _q_acct <<< "$_queue_entry"
    cd "$_q_path" || exit 1
    PROJECT_NAME="$(basename "$_q_path")"
    SELECTED_AI_TOOL="$_q_tool"
    # This tab's own conversation id (may be empty on old snapshots);
    # build_ai_launch_cmd resumes it specifically instead of `claude -c`.
    export WISP_DECK_RESUME_SESSION="$_q_sid"
    # This tab's exact pane geometry at close time (may be empty on old
    # snapshots); replayed with select-layout after the panes are built so the
    # window reopens at the positions the user left it in.
    WISP_DECK_RESUME_LAYOUT="$_q_layout"
    type stop_loading_screen &>/dev/null && stop_loading_screen
  else

  # No queue entry for this launch. A launch that took part in a restore
  # drain (saw the active queue at start, or popped right after it emptied)
  # is a surplus tab of the crash-resume storm — close it quietly instead of
  # littering the window with picker tabs. The queue builder is exempt: it is
  # the user's own window and keeps the picker fallback.
  if restore_surplus_launch "$SHARE_DIR" "$_restore_participant" "${WISP_DECK_RESTORE_BUILDER:-0}" "$_wd_launch_epoch"; then
    restore_log "$SHARE_DIR" "surplus restore launch closed (participant=$_restore_participant)"
    type stop_loading_screen &>/dev/null && stop_loading_screen
    exit 0
  fi

  # Use TUI for project selection
  printf '\033]0;󰊠  Wisp Deck\007'

  # Stop loading animation before TUI takes over
  type stop_loading_screen &>/dev/null && stop_loading_screen

  while true; do
    # Fingerprint the settings file so the (expensive, all-session) propagation
    # below only runs when the menu actually changed a setting.
    _settings_before="$(settings_fingerprint "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings")"
    if select_project_interactive "$PROJECTS_FILE"; then
      # The menu just closed: push any settings change (theme, panel mode) to
      # every OTHER already-running session so a toggle reaches all open windows,
      # not just newly-launched ones. This window's own session does not exist
      # yet, so it is untouched here.
      apply_settings_to_all_sessions_if_changed "$TMUX_CMD" "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" "$_settings_before" 2>/dev/null || true
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
          # A plain Ghostty shell should still run `claude` under the login the
          # user has selected in the menu (the current Claude Code user), not the
          # Keychain default. Export the active account's isolated CLAUDE_CONFIG_DIR
          # before exec'ing the shell; Default leaves it unset (Keychain login).
          apply_plain_terminal_claude_account "$SHARE_DIR/claude-accounts" "$SHARE_DIR/claude-account"
          # An account registered in the menu this launch missed the early
          # state/settings sync — link it before claude can write a private
          # store or start with blank settings.
          if [ -n "${CLAUDE_CONFIG_DIR:-}" ]; then
            sync_claude_shared_state "$HOME/.claude" "$CLAUDE_CONFIG_DIR"
            sync_claude_shared_settings "$HOME/.claude" "$CLAUDE_CONFIG_DIR"
          fi
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
      # User quit (ESC/Ctrl-C) — still propagate any settings change they made
      # before quitting to the other running sessions.
      apply_settings_to_all_sessions_if_changed "$TMUX_CMD" "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" "$_settings_before" 2>/dev/null || true
      exit 0
    fi
  done
  fi
fi

PROJECT_DIR="$(pwd)"
export PROJECT_DIR
export PROJECT_NAME="${PROJECT_NAME:-$(basename "$PROJECT_DIR")}"
SESSION_NAME="dev-${PROJECT_NAME}-$$"

# Read settings
_settings_file="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings"

# Set terminal/tab title based on tab_title setting
_tab_title_setting="full"
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
WISP_DECK_MARKER_FILE="/tmp/wisp-deck-waiting-$$"
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  _claude_settings="${HOME}/.claude/settings.json"
  add_waiting_indicator_hooks "$_claude_settings" >/dev/null
  # Silence Claude's own idle notification (preferredNotifChannel=terminal_bell,
  # silent in Ghostty) so the wisp-deck sound flag — including "off" — is the
  # single source of truth for the idle sound.
  setup_sound_notification "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" "$_claude_settings"
fi

# Background watcher: switch to the AI pane once it's ready. Resolves the AI
# pane via gt_ai_pane (marker/geometry) rather than a fixed index, so it is
# correct under any tmux pane-base-index.
gt_focus_ai_pane_when_ready "$TMUX_CMD" "$SESSION_NAME" &
WATCHER_PID=$!

cleanup() {
  stop_tab_title_watcher "$WISP_DECK_MARKER_FILE"
  [ -n "${HEARTBEAT_PID:-}" ] && kill_tree "$HEARTBEAT_PID" TERM 2>/dev/null || true
  # Remove waiting indicator hooks if no other Wisp Deck sessions are running
  if [ "$SELECTED_AI_TOOL" = "claude" ]; then
    # Clean up orphaned markers and cooldown files from dead sessions (e.g., after SIGKILL)
    for marker in /tmp/wisp-deck-waiting-*; do
      [ -f "$marker" ] || continue
      # Skip cooldown files — they'll be cleaned with their parent marker
      [[ "$marker" == *-cooldown ]] && continue
      [[ "$marker" == *-ask ]] && continue
      local pid="${marker##*-}"
      if ! kill -0 "$pid" 2>/dev/null; then
        rm -f "$marker" "${marker}-cooldown" "${marker}-ask"
      fi
    done
    if ! ls /tmp/wisp-deck-waiting-* &>/dev/null; then
      remove_waiting_indicator_hooks "${HOME}/.claude/settings.json" >/dev/null 2>&1 || true
      # Restore the notification channel only when the last session exits, so a
      # concurrent session keeps Claude silenced.
      remove_sound_notification "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" "${HOME}/.claude/settings.json" >/dev/null 2>&1 || true
    fi
  fi
  cleanup_tmux_session "$SESSION_NAME" "$WATCHER_PID" "$TMUX_CMD"
  rm -f "$SHARE_DIR/spare-${SESSION_NAME}.conf"
  rm -f "$SHARE_DIR/proxy-${SESSION_NAME}.log"
  rm -f "$SHARE_DIR/proxy-account-${SESSION_NAME}"
  rm -f "$SHARE_DIR/relaunch-${SESSION_NAME}"
  rm -rf "$SHARE_DIR/spare-zdotdir-${SESSION_NAME}"
}
trap cleanup EXIT HUP TERM INT

if [ "$RESTORE_MODE" -eq 1 ]; then
  export WISP_DECK_RESUME=1
fi

# Resolve active Claude config (settings file) and export for build_ai_launch_cmd.
_gt_cfg_root="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
WISP_DECK_CLAUDE_SETTINGS=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  WISP_DECK_CLAUDE_SETTINGS="$(resolve_claude_config_path "$_gt_cfg_root/claude-configs" "$_gt_cfg_root/claude-config")"
fi
export WISP_DECK_CLAUDE_SETTINGS

# Resolve the active native Claude account (its isolated CLAUDE_CONFIG_DIR) and
# export for build_ai_launch_cmd. Default (empty) leaves CLAUDE_CONFIG_DIR unset
# so Claude uses the standard Keychain login.
WISP_DECK_CLAUDE_ACCOUNT_DIR=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  if [ "$RESTORE_MODE" -eq 1 ]; then
    # A restored tab must come back under the login ITS session ran (recorded
    # in the queue entry), not whatever the global pointer names right now —
    # the pointer is shared mutable state and resolving it here silently
    # flipped restored sessions' accounts after every reboot.
    WISP_DECK_CLAUDE_ACCOUNT_DIR="$(resolve_restore_claude_account_dir "$_gt_cfg_root/claude-accounts" "$_gt_cfg_root/claude-account" "${_q_acct:-}")"
  else
    WISP_DECK_CLAUDE_ACCOUNT_DIR="$(resolve_claude_account_dir "$_gt_cfg_root/claude-accounts" "$_gt_cfg_root/claude-account")"
  fi
  # A non-Default account has its own isolated CLAUDE_CONFIG_DIR, which otherwise
  # starts blank — no status line, permission mode, skills, hooks, model, etc.
  # Link the standard login's settings into it so every login shares one set of
  # settings while keeping its own credentials/session state. Self-heals drift on
  # each launch (Claude may rewrite a settings file in place, severing the link).
  if [ -n "$WISP_DECK_CLAUDE_ACCOUNT_DIR" ]; then
    sync_claude_shared_settings "$HOME/.claude" "$WISP_DECK_CLAUDE_ACCOUNT_DIR"
    # Also share conversation state here (not only at wrapper start): an
    # account registered in the menu THIS launch didn't exist when the early
    # sync ran, and must not start a private transcript store.
    sync_claude_shared_state "$HOME/.claude" "$WISP_DECK_CLAUDE_ACCOUNT_DIR"
  fi
fi
# Auto-switch accounts is IN-PLACE rotation: the statusline watches this
# session's quota usage and, at the threshold, relaunches the AI pane under the
# next pooled account via the same mid-session switch the ledger pill drives
# (see auto_switch_maybe_trigger in lib/auto-switch.sh and auto_switch_relaunch
# in lib/account-switch.sh). Nothing to start here — the trigger reads the
# relaunch-context file written below.
export WISP_DECK_CLAUDE_ACCOUNT_DIR

# Resolve the active subscription/plan display name for the compact-view ledger.
# Subscriptions are shared across agents, so this is resolved for every tool.
WISP_DECK_PLAN="$(get_active_claude_config_name "$_gt_cfg_root/claude-config" "$_gt_cfg_root/claude-configs.list")"
export WISP_DECK_PLAN

# Claude-only: run Claude behind the screenshot-drag filter so dragging a
# screenshot into the pane works (the filter copies the dropped screencaptureui
# temp file to a stable path and rewrites the path before Claude reads it,
# beating macOS's deletion of the temp file). Only enabled after probing that
# the installed TUI binary supports the subcommand, so an older binary safely
# falls back to launching Claude directly.
WISP_DECK_CLAUDE_FILTER=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  # Capability is cached per binary, so only the first launch after an
  # install/update pays the ~40ms probe. See gt_claude_filter_prefix.
  WISP_DECK_CLAUDE_FILTER="$(gt_claude_filter_prefix "$SHARE_DIR")"
fi
export WISP_DECK_CLAUDE_FILTER

# Build the AI tool launch command
case "$SELECTED_AI_TOOL" in
  opencode)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$CLAUDE_CMD" "$OPENCODE_CMD" "$PROJECT_DIR")"
    ;;
  *)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$CLAUDE_CMD" "$OPENCODE_CMD" "$*")"
    ;;
esac

# Mid-session account switch: for every claude session, persist the launch
# context so the compact-view ledger's account pill — and the auto-switch
# trigger — can relaunch the AI pane under another login (continue mode). The
# pill's own 2+-logins gate lives in the ledger; here we only skip opencode.
# Cleared by cleanup() on window close.
WISP_DECK_RELAUNCH_FILE=""
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  WISP_DECK_RELAUNCH_FILE="$SHARE_DIR/relaunch-${SESSION_NAME}"
  write_relaunch_context "$WISP_DECK_RELAUNCH_FILE" "$SELECTED_AI_TOOL" \
    "$CLAUDE_CMD" "$OPENCODE_CMD" "$WISP_DECK_CLAUDE_SETTINGS" \
    "$WISP_DECK_CLAUDE_FILTER" "$PROJECT_DIR" "$_gt_cfg_root"
fi
export WISP_DECK_RELAUNCH_FILE

# Start tab title watcher before tmux (which blocks until session ends)
start_tab_title_watcher "$SESSION_NAME" "$SELECTED_AI_TOOL" "$PROJECT_NAME" "$_tab_title_setting" "$TMUX_CMD" "$WISP_DECK_MARKER_FILE" "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"

# Session-restore snapshot: stamp metadata into the tmux session env via -e
# flags on new-session (below), and run a heartbeat that re-derives the
# snapshot from all alive Wisp Deck sessions.
# Ghostty is the only supported terminal; the snapshot's terminal field is
# kept for backward compatibility with restore.
WISP_DECK_TERMINAL="ghostty"
WISP_DECK_SNAPSHOT="$SHARE_DIR/last-session"
# Backgrounded lib function, not an inline loop: each tick re-sources the lib
# in a throwaway bash, so snapshot fixes reach sessions already running.
run_snapshot_heartbeat "$_WRAPPER_DIR" "$TMUX_CMD" "$WISP_DECK_SNAPSHOT" &
HEARTBEAT_PID=$!

# Build pane 0 command: the compact changeset-ledger view.
_pane0_cmd="source \"$_WRAPPER_DIR/lib/compact-view.sh\" && compact_view \"$PROJECT_DIR\"; exec bash"
_pane0_pct=75

# Drag-dropping a screenshot onto a specific tmux pane is unreliable: tmux
# delivers the paste to the *active* pane, not the pane under the cursor (an
# external file drag never produces a tmux mouse event, so tmux can't know the
# target). Two mitigations below:
#   1. The AI pane is left as the *active* pane (select-pane -R, and a distinct
#      pane-active-border so focus is visible) -- so a screenshot dropped while
#      the AI pane is focused lands in the AI tool.
#   2. prefix+i injects the most recent screenshot straight into the AI pane
#      regardless of which pane is active. See lib/screenshot.sh.
_screenshot_bind="bash -c 'source \"$_WRAPPER_DIR/lib/screenshot.sh\" && gt_paste_latest_screenshot'"

# Spare pane: a nested tmux whose top status bar is a tab bar (project name on
# the first tab, numbered extras, a [ + ] add button and per-tab × close). The
# config is written ahead of time; the pane execs the inner server. See
# lib/spare-tabs.sh. Outer mouse stays off so clicks reach the inner tmux.
_spare_label="$(spare_tabs_socket "$SESSION_NAME")"
mkdir -p "$SHARE_DIR"
_spare_conf="$SHARE_DIR/spare-${SESSION_NAME}.conf"
# Focus accent for the tmux chrome (active pane border + active spare-tab chip).
# Honor a user-chosen theme preset (Settings menu), falling back to the per-tool
# hue: purple for OpenCode, orange for claude. Mirrors the Go theme's Primary.
_gt_theme_pref="$(grep '^theme=' "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
_gt_accent="$(get_theme_accent "$(gt_resolve_theme "$_gt_theme_pref" "$SELECTED_AI_TOOL")")"
spare_tabs_config "$PROJECT_NAME" "$PROJECT_DIR" "$_WRAPPER_DIR/lib/spare-tabs.sh" "$_spare_label" "$_gt_accent" > "$_spare_conf"
# Minimal cwd-only prompt for the spare shell (drops user@host and conda's
# "(base)"). Echoes empty for non-zsh shells, leaving them untouched.
_spare_zdotdir="$(spare_prompt_zdotdir "$SHARE_DIR" "$SESSION_NAME" "$SHELL" "${ZDOTDIR:-$HOME}")"
_spare_cmd="$(spare_tabs_launch_cmd "$_spare_label" "$_spare_conf" "$PROJECT_DIR" "$_spare_zdotdir")"
_spare_close_bind="bash -c 'source \"$_WRAPPER_DIR/lib/spare-tabs.sh\" && spare_tabs_close_current \"$_spare_label\"'"

"$TMUX_CMD" new-session -s "$SESSION_NAME" -e "PATH=$PATH" -e "WISP_DECK_MARKER_FILE=$WISP_DECK_MARKER_FILE" -e "WISP_DECK=1" -e "WISP_DECK_BOOT=$WISP_DECK_BOOT_ID" -e "WISP_DECK_PROJECT=$PROJECT_NAME" -e "WISP_DECK_PATH=$PROJECT_DIR" -e "WISP_DECK_TOOL=$SELECTED_AI_TOOL" -e "WISP_DECK_TERMINAL=$WISP_DECK_TERMINAL" -e "WISP_DECK_CLAUDE_SESSION=${WISP_DECK_RESUME_SESSION:-}" -e "WISP_DECK_PLAN=$WISP_DECK_PLAN" -e "WISP_DECK_RELAUNCH_FILE=$WISP_DECK_RELAUNCH_FILE" -e "WISP_DECK_CLAUDE_ACCOUNT=${WISP_DECK_CLAUDE_ACCOUNT_DIR##*/}" -e "WISP_DECK_LIB_DIR=$_WRAPPER_DIR/lib" -c "$PROJECT_DIR" \
  "$_pane0_cmd" \; \
  set-option status-left " ⬡ ${PROJECT_NAME} " \; \
  set-option status-left-style "fg=white,bg=colour236,bold" \; \
  set-option status-style "bg=colour235" \; \
  set-option status-right "" \; \
  set-option set-titles off \; \
  set-option exit-unattached on \; \
  set-option pane-border-style "fg=colour238" \; \
  set-option pane-active-border-style "fg=colour${_gt_accent}" \; \
  bind-key i run-shell "$_screenshot_bind" \; \
  bind-key t run-shell "env -u TMUX -u TMUX_PANE tmux -L $_spare_label new-window -c \"$PROJECT_DIR\"" \; \
  bind-key w run-shell "$_spare_close_bind" \; \
  bind-key Tab run-shell "env -u TMUX -u TMUX_PANE tmux -L $_spare_label next-window" \; \
  bind-key BTab run-shell "env -u TMUX -u TMUX_PANE tmux -L $_spare_label previous-window" \; \
  split-window -h -p "$_pane0_pct" -c "$PROJECT_DIR" \
  "$AI_LAUNCH_CMD; exec bash" \; \
  set-option -p @gt_ai 1 \; \
  select-pane -L \; \
  split-window -v -p 45 -c "$PROJECT_DIR" "$_spare_cmd" \; \
  select-pane -R

# Restore: replay the captured pane geometry over the just-built panes. The
# build order above is deterministic and identical to capture time, so the
# panes line up with the layout's cells; select-layout reproduces their exact
# sizes (scaling proportionally if this terminal window is a different size).
# Skipped when no layout was captured (old snapshot) — the default split stays.
if [ "${RESTORE_MODE:-0}" = "1" ] && [ -n "${WISP_DECK_RESUME_LAYOUT:-}" ]; then
  "$TMUX_CMD" select-layout -t "$SESSION_NAME:0" "$WISP_DECK_RESUME_LAYOUT" 2>/dev/null || true
fi
