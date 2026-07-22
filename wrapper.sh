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
_wd_launch_seq=""
if [ -f "$_wrapper_dir_early/lib/session-restore.sh" ]; then
  # shellcheck disable=SC1091  # Dynamic path
  source "$_wrapper_dir_early/lib/session-restore.sh"
  # Taken HERE, before any slow init: launch order = tab order (a restore
  # chain's tab N+1 only starts after tab N popped its entry, i.e. after tab
  # N already holds a lower seq). Stamped into the session env below so
  # snapshots reproduce tab order even for sessions created within the same
  # second (tmux's created stamp cannot tell those apart).
  mkdir -p "$SHARE_DIR" 2>/dev/null
  _wd_launch_seq="$(next_launch_seq "$SHARE_DIR")"
  if [ -z "$1" ]; then
    restore_queue_active "$SHARE_DIR" "$(current_boot_id)" && _restore_participant=1
  fi
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

_gt_libs=(theme ai-tools projects process input tui install menu-tui project-actions ledger-hover tmux-session settings-json notification-setup keep-awake tab-title-watcher terminals/ghostty session-restore claude-configs claude-accounts claude-shared-settings auto-switch attention account-switch compact-view screenshot spare-tabs tab-view ai-loading)
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
CODEX_CMD="$(command -v codex)"

# NOT resolved here. resolve_opencode_cmd's npx branch spawns node to find out
# WHICH npx invocation can launch OpenCode (6-13s, warm cache) — and this runs
# before the picker paints, so every launch of every tool used to pay it. The
# picker only needs to know OpenCode is *possible* (a PATH check); the command
# string is resolved on demand, below, on the branch that actually launches it.
OPENCODE_CMD=""

# AI tool preference
AI_TOOL_PREF_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/ai-tool"
AI_TOOLS_AVAILABLE=()
[ -n "$CLAUDE_CMD" ] && AI_TOOLS_AVAILABLE+=("claude")
opencode_available && AI_TOOLS_AVAILABLE+=("opencode")
[ -n "$CODEX_CMD" ] && AI_TOOLS_AVAILABLE+=("codex")

# Drop tools the user disabled in Settings → AI tools. No mapfile — this
# script runs under macOS's stock bash 3.2.
WISP_DECK_DISABLED_TOOLS_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/disabled-tools"
if [ ${#AI_TOOLS_AVAILABLE[@]} -gt 0 ]; then
  _gt_filtered=()
  while IFS= read -r _gt_tool; do _gt_filtered+=("$_gt_tool"); done \
    < <(filter_disabled_ai_tools "$WISP_DECK_DISABLED_TOOLS_FILE" "${AI_TOOLS_AVAILABLE[@]}")
  AI_TOOLS_AVAILABLE=("${_gt_filtered[@]}")
  unset _gt_filtered _gt_tool
fi

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
  # Only the queue builder, a chain-spawned tab holding the one-shot ticket,
  # or a crash-storm window that launched together with the build may pop
  # (see restore_pop_authorized). A tab the USER opens mid-drain matches none
  # of these and must never consume an entry meant for the chain — that
  # hijack restored someone else's project into the user's tab while their
  # intended session opened elsewhere (the wrong-tab bug).
  _queue_entry=""
  if restore_pop_authorized "$SHARE_DIR" "${WISP_DECK_RESTORE_BUILDER:-0}" \
    "$_wd_launch_epoch"; then
    _queue_entry="$(restore_queue_pop "$SHARE_DIR" "$WISP_DECK_BOOT_ID")"
    # Skip entries whose project directory no longer exists, and — last line of
    # defense against duplicate tabs — entries whose conversation is already
    # open in an alive session (a re-queued entry from any upstream failure).
    while [ -n "$_queue_entry" ] && ! restore_entry_wanted "$TMUX_CMD" "$_queue_entry"; do
      _queue_entry="$(restore_queue_pop "$SHARE_DIR" "$WISP_DECK_BOOT_ID")"
    done
  fi
  if [ -n "$_queue_entry" ]; then
    # Open the next tab immediately so the chain completes quickly while this
    # window continues its own setup.
    restore_advance "$SHARE_DIR"
    RESTORE_MODE=1
    IFS='|' read -r _q_path _q_tool _q_sid _q_layout _q_acct _q_identity_key <<< "$_queue_entry"
    cd "$_q_path" || exit 1
    PROJECT_NAME="$(basename "$_q_path")"
    SELECTED_AI_TOOL="$_q_tool"
    # This tab's own conversation id (may be empty on old snapshots);
    # build_ai_launch_cmd resumes it specifically instead of `claude -c`.
    # Deliberately NOT exported (same for the launch vars below): the tmux
    # server inherits the env of whichever wrapper starts it first, and every
    # pane of every later session inherits the server's env — an exported
    # per-tab resume sid leaked ONE tab's conversation into ALL tabs, priming
    # the account-switch stale-resume bug. build_ai_launch_cmd runs in this
    # shell and sees plain variables just fine.
    WISP_DECK_RESUME_SESSION="$_q_sid"
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
    # Remember whether keep-awake was on going into the menu: turning it off
    # in there is the in-app path to revoking the sudo rule (offered below).
    _keep_awake_was_on=0
    keep_awake_enabled "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" && _keep_awake_was_on=1
    if select_project_interactive "$PROJECTS_FILE"; then
      # The menu just closed: push any settings change (theme, panel mode) to
      # every OTHER already-running session so a toggle reaches all open windows,
      # not just newly-launched ones. This window's own session does not exist
      # yet, so it is untouched here.
      apply_settings_to_all_sessions_if_changed "$TMUX_CMD" "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" "$_settings_before" 2>/dev/null || true
      # If the user just turned keep-awake on, grant the sudo rule now, while a
      # terminal is still attached and a password prompt can be answered.
      keep_awake_ensure_sudoers "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" || true
      # And if they just turned it off, offer to revoke the sudo rule too.
      keep_awake_offer_revoke "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" "$_keep_awake_was_on" || true
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
        update)
          # The header notice's Update button. Run the npm update in the
          # foreground, then reopen the menu — it re-execs wisp-deck-tui from
          # disk, so the freshly installed build shows immediately.
          if type run_wisp_deck_update &>/dev/null; then
            run_wisp_deck_update || true
          fi
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
WISP_DECK_CODEX_SESSION_DIR="$SHARE_DIR/session-identities"
if ! (umask 077; mkdir -p "$WISP_DECK_CODEX_SESSION_DIR") \
   || ! chmod 700 "$WISP_DECK_CODEX_SESSION_DIR"; then
  printf '\033[31mError:\033[0m Could not initialize durable Codex session identities.\n' >&2
  exit 1
fi
prune_codex_session_identities "$TMUX_CMD" "$SHARE_DIR" 30 >/dev/null 2>&1 || true
WISP_DECK_CODEX_SESSION_FILE="$WISP_DECK_CODEX_SESSION_DIR/${SESSION_NAME}.codex"
# PIDs are eventually reused, while sidecars intentionally survive crashes.
# A newly allocated Wisp session must never inherit an old same-name UUID.
if ! rm -f "$WISP_DECK_CODEX_SESSION_FILE"; then
  printf '\033[31mError:\033[0m Could not reset the Codex session identity.\n' >&2
  exit 1
fi

# From here on this terminal belongs to the AI tool's full-screen UI, not to us:
# the interactive phase (picker, Settings, the keep-awake password window) is
# over, and everything below either backgrounds a loop that outlives it or hands
# the screen to tmux. Nobody is reading stderr anymore — but it is still wired to
# the terminal, so anything that writes there paints over the UI. That is how a
# failing read in the keep-awake reaper ended up inside Claude's input box.
#
# So point stderr at a per-session log now, once, instead of asking every job and
# every lib it calls to remember its own `2>/dev/null`. fd 3 keeps the real
# terminal for the one thing the user must still see: tmux failing to start at
# all, below — after which there is no UI left to protect.
exec 3>&2
_WD_ERROR_LOG="$SHARE_DIR/logs/${SESSION_NAME}.log"
gt_mute_terminal_stderr "$_WD_ERROR_LOG"
# Exported so the jobs below — which cut themselves off from the terminal at
# their own boundary — still have somewhere to report to.
export WISP_DECK_ERROR_LOG="$_WD_ERROR_LOG"

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

# Semantic attention runtime. A fresh generation is published below before the
# launch command is built; the watcher follows the stable descriptor.
WISP_DECK_ATTENTION_ROOT=""
WISP_DECK_ATTENTION_DESCRIPTOR=""
WISP_DECK_ATTENTION_GENERATION=""
WISP_DECK_ATTENTION_FILE=""

# Retire exact legacy sound plugins before launch. Unknown local plugins remain
# untouched and are inert because the strict adapter starts both OpenCode
# processes with --pure.
if [ "$SELECTED_AI_TOOL" = "opencode" ]; then
  if ! retire_known_opencode_sound_plugins; then
    printf '\033[31mError:\033[0m Failed to retire a known OpenCode sound plugin.\n' >&2
    exit 1
  fi
fi

# Background watcher: switch to the AI pane once it's ready. Resolves the AI
# pane via gt_ai_pane (marker/geometry) rather than a fixed index, so it is
# correct under any tmux pane-base-index.
#
# stdout is dropped here, as on every job this file backgrounds: it would
# otherwise be the session terminal, where the AI tool is drawing a full-screen
# UI, and these jobs keep running for the whole session. Nothing reads it.
# (stderr already goes to the session log — see gt_mute_terminal_stderr above.)
gt_focus_ai_pane_when_ready "$TMUX_CMD" "$SESSION_NAME" >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &
WATCHER_PID=$!

# Reap holders left by sessions that died without running their trap (SIGKILL,
# panic, power loss). Without this a single crash pins SleepDisabled on until
# some later session happens to go active and then idle again.
keep_awake_sync "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" 2>/dev/null || true

cleanup() {
  stop_tab_title_watcher
  # A session that logged nothing leaves no file behind; one that hit a real
  # error keeps its log (pruned after a week by gt_mute_terminal_stderr).
  if [ -n "${_WD_ERROR_LOG:-}" ] && [ ! -s "$_WD_ERROR_LOG" ]; then
    rm -f "$_WD_ERROR_LOG"
  fi
  # Release before anything else: whatever follows may fail, and leaving the
  # machine unable to sleep is worse than leaving a temp file behind.
  keep_awake_drop "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck" "$SESSION_NAME" 2>/dev/null || true
  [ -n "${HEARTBEAT_PID:-}" ] && kill_tree "$HEARTBEAT_PID" TERM 2>/dev/null || true
  cleanup_tmux_session "$SESSION_NAME" "$WATCHER_PID" "$TMUX_CMD"
  attention_cleanup "${WISP_DECK_ATTENTION_ROOT:-}" 2>/dev/null || true
  rm -f "$SHARE_DIR/spare-${SESSION_NAME}.conf"
  rm -f "$SHARE_DIR/proxy-${SESSION_NAME}.log"
  rm -f "$SHARE_DIR/proxy-account-${SESSION_NAME}"
  rm -f "$SHARE_DIR/relaunch-${SESSION_NAME}"
  rm -rf "$SHARE_DIR/spare-zdotdir-${SESSION_NAME}"
}
trap cleanup EXIT HUP TERM INT

# Every wrapper invocation, including a restored tab, owns a fresh private
# attention root and immutable launch generation. Build commands only after
# these exports exist so the adapter receives the generation it must publish.
if ! attention_session_create "${TMPDIR:-/tmp}" >/dev/null \
   || ! attention_begin_generation "$WISP_DECK_ATTENTION_ROOT" \
      "$SELECTED_AI_TOOL" >/dev/null; then
  printf '\033[31mError:\033[0m Could not initialize the attention runtime.\n' >&2
  exit 1
fi

if [ "$RESTORE_MODE" -eq 1 ]; then
  # shellcheck disable=SC2034  # read by build_ai_launch_cmd, sourced into this shell
  WISP_DECK_RESUME=1
fi

# Resolve the active Wisp settings overlay, migrate obsolete marker hooks out of
# the user's standard settings once, then build a private launch-local overlay
# inside this attention generation. Claude merges the additional --settings file
# with its normal user settings, so the global notification preference never
# needs a lease or cleanup mutation.
_gt_cfg_root="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
WISP_DECK_CLAUDE_SETTINGS=""
WISP_DECK_CLAUDE_SETTINGS_SOURCE="$(resolve_claude_config_path \
  "$_gt_cfg_root/claude-configs" "$_gt_cfg_root/claude-config")"
WISP_DECK_CLAUDE_PROVIDER="$(get_claude_config_provider \
  "$WISP_DECK_CLAUDE_SETTINGS_SOURCE")"
# The active subscription FILENAME (empty = standard Claude), stamped per-pane so
# the switcher's active dot marks THIS pane's backend even after another session
# flips the global config pointer (mirrors WISP_DECK_CLAUDE_ACCOUNT).
WISP_DECK_CLAUDE_CONFIG="$(get_active_claude_config "$_gt_cfg_root/claude-config")"
WISP_DECK_CODEX_CMD="$CODEX_CMD"
export WISP_DECK_CLAUDE_PROVIDER WISP_DECK_CLAUDE_CONFIG WISP_DECK_CODEX_CMD
# Upgrade cleanup is launch-wide, not tied to the initially selected tool: a
# Codex/OpenCode tab may switch to Claude later in the same wrapper lifetime.
remove_waiting_indicator_hooks "$HOME/.claude/settings.json" "$_gt_cfg_root" >/dev/null 2>&1 || true
# A pre-upgrade crash may have left the old global notification-channel lease
# applied. Restore it only if terminal_bell is still the exact value Wisp wrote;
# the launch-local overlay below owns notification suppression from now on.
migrate_legacy_claude_notif_channel "$HOME/.claude/settings.json" "$_gt_cfg_root" >/dev/null 2>&1 || true
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  if ! WISP_DECK_CLAUDE_SETTINGS="$(
    write_claude_launch_settings "${WISP_DECK_ATTENTION_FILE%/state}" \
      "$WISP_DECK_CLAUDE_SETTINGS_SOURCE"
  )"; then
    printf '\033[31mError:\033[0m Could not prepare Claude launch settings.\n' >&3
    exit 1
  fi
fi

# Resolve the active native Claude account (its isolated CLAUDE_CONFIG_DIR) and
# export for build_ai_launch_cmd. Default (empty) leaves CLAUDE_CONFIG_DIR unset
# so Claude uses the standard Keychain login.
WISP_DECK_CLAUDE_ACCOUNT_DIR=""
WISP_DECK_CLAUDE_BACKGROUND_MODE=default
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
    WISP_DECK_CLAUDE_BACKGROUND_MODE=isolated
    sync_claude_shared_settings "$HOME/.claude" "$WISP_DECK_CLAUDE_ACCOUNT_DIR"
    # Also share conversation state here (not only at wrapper start): an
    # account registered in the menu THIS launch didn't exist when the early
    # sync ran, and must not start a private transcript store.
    sync_claude_shared_state "$HOME/.claude" "$WISP_DECK_CLAUDE_ACCOUNT_DIR"
  fi
fi

# Every wrapper that has used an exact Claude account keeps one detached broker
# candidate for that root until its stable attention root is removed. Account
# and tool switches start additional roots through the same helper and never
# stop prior-root candidates, because their background jobs remain global.
if [ "$SELECTED_AI_TOOL" = "claude" ]; then
  attention_start_claude_background_candidate \
    "$CLAUDE_CMD" "${WISP_DECK_CLAUDE_ACCOUNT_DIR:-$HOME/.claude}" \
    "$_gt_cfg_root" "$WISP_DECK_ATTENTION_ROOT" \
    "$WISP_DECK_CLAUDE_BACKGROUND_MODE" \
    "${WISP_DECK_ERROR_LOG:-/dev/null}" 2>/dev/null || true
fi
# Auto-switch accounts is IN-PLACE rotation: the statusline watches this
# session's quota usage and, at the threshold, relaunches the AI pane under the
# next pooled account via the same mid-session switch the ledger pill drives
# (see auto_switch_maybe_trigger in lib/auto-switch.sh and auto_switch_relaunch
# in lib/account-switch.sh). Nothing to start here — the trigger reads the
# relaunch-context file written below.

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

# Pay the npx probe (see OPENCODE_CMD above) only now, and only when OpenCode is
# the tool actually being launched — the one case that needs the command string.
# A claude/codex session leaves OPENCODE_CMD empty in the relaunch context below;
# _tool_cmd_for resolves it on demand if the user later switches to OpenCode.
if [ "$SELECTED_AI_TOOL" = "opencode" ]; then
  OPENCODE_CMD="$(resolve_opencode_cmd)"
fi

# Build the AI tool launch command. Only opencode takes the project dir as a
# positional arg; claude takes the wrapper's CLI args, and codex takes neither
# (its pane cwd is already the project dir).
AI_TOOL_CMD="$(resolve_ai_tool_cmd "$SELECTED_AI_TOOL" "$CLAUDE_CMD" "$OPENCODE_CMD" "$CODEX_CMD")"
case "$SELECTED_AI_TOOL" in
  opencode)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$AI_TOOL_CMD" "$PROJECT_DIR")" || {
      printf '\033[31mError:\033[0m Refusing unsupported OpenCode launch command.\n' >&2
      exit 1
    }
    ;;
  codex)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$AI_TOOL_CMD")"
    ;;
  *)
    AI_LAUNCH_CMD="$(build_ai_launch_cmd "$SELECTED_AI_TOOL" "$AI_TOOL_CMD" "$*")" || {
      if [ "$WISP_DECK_CLAUDE_PROVIDER" = "openai-chatgpt" ]; then
        printf '\033[31mError:\033[0m OpenAI GPT requires Codex. Install Codex, then relaunch; ChatGPT sign-in will open automatically.\n' >&3
      else
        printf '\033[31mError:\033[0m Could not prepare the Claude launch command.\n' >&3
      fi
      exit 1
    }
    ;;
esac

# Ghostty is the only supported terminal; the snapshot's terminal field is
# kept for backward compatibility with restore.
WISP_DECK_TERMINAL="ghostty"
WISP_DECK_SNAPSHOT="$SHARE_DIR/last-session"

# Build pane 0 command: the compact changeset-ledger view.
_pane0_cmd="source \"$_WRAPPER_DIR/lib/compact-view.sh\" && compact_view \"$PROJECT_DIR\"; exec bash"
_pane0_pct=75

# Focus accent for the tmux chrome (active pane border + active spare-tab chip).
# Honor a user-chosen theme preset (Settings menu), falling back to the per-tool
# hue: purple for OpenCode, orange for claude. Mirrors the Go theme's Primary.
_gt_theme_pref="$(grep '^theme=' "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
_gt_accent="$(get_theme_accent "$(gt_resolve_theme "$_gt_theme_pref" "$SELECTED_AI_TOOL")")"

# Show a "Starting <tool>…" banner in the AI pane until the tool paints. The AI
# CLI can spend seconds — minutes when macOS's Security subsystem is contended
# (claude 2.1.217 blocks on keychain/trust XPC at startup) — before it draws
# anything, and a black pane reads as "the agent failed to load". The banner
# runs INSIDE the pane (prepended to the launch string, executed by tmux, never
# by this wrapper), so it adds no latency; the tool's alt-screen replaces it on
# paint. Applied to the same AI_LAUNCH_CMD the heal watcher rebuilds from.
_gt_loading_prefix="$(ai_pane_loading_prefix "$SELECTED_AI_TOOL" "$_gt_accent")" \
  && AI_LAUNCH_CMD="${_gt_loading_prefix}${AI_LAUNCH_CMD}"

if ! declare -f _sane_term_size >/dev/null 2>&1; then
  # shellcheck disable=SC1091  # Runtime library path
  source "$_WRAPPER_DIR/lib/loading.sh"
fi
# _sane_term_size, not _detect_term_size: a single-shot read can catch the pty
# before Ghostty delivers the tab's real size, and a tiny -x/-y makes the
# split-window commands below fail — the tab then sits attached to a lone
# full-width ledger pane (the stuck-launch bug).
read -r _tmux_rows _tmux_cols <<< "$(_sane_term_size)"

WISP_DECK_CLAUDE_SESSION=""
WISP_DECK_CODEX_SESSION=""
if [ "$RESTORE_MODE" -eq 1 ]; then
  case "$SELECTED_AI_TOOL" in
    claude) WISP_DECK_CLAUDE_SESSION="${WISP_DECK_RESUME_SESSION:-}" ;;
    codex) WISP_DECK_CODEX_SESSION="${WISP_DECK_RESUME_SESSION:-}" ;;
  esac
fi

# Spare pane: a nested tmux whose top status bar is a tab bar (project name on
# the first tab, numbered extras, a [ + ] add button and per-tab × close). The
# config is written ahead of time; the pane execs the inner server. See
# lib/spare-tabs.sh. Ledger routing enables outer mouse mode but preserves the
# normal send-keys -M path, so clicks still reach the inner tmux. Prepared
# BEFORE the launch because the spare split must ride in the same tmux batch
# as new-session: a deferred split leaves a 2-pane window that the heal
# watcher reads as a failed spare split and rebuilds — the duplicate-spare
# bug. The prep is ~100ms of bash; the expensive tail stays after the launch.
_spare_label="$(spare_tabs_socket "$SESSION_NAME")"
mkdir -p "$SHARE_DIR"
_spare_conf="$SHARE_DIR/spare-${SESSION_NAME}.conf"
spare_tabs_config "$PROJECT_NAME" "$PROJECT_DIR" "$_WRAPPER_DIR/lib/spare-tabs.sh" "$_spare_label" "$_gt_accent" > "$_spare_conf"
# Minimal cwd-only prompt for the spare shell (drops user@host and conda's
# "(base)"). Echoes empty for non-zsh shells, leaving them untouched.
_spare_zdotdir="$(spare_prompt_zdotdir "$SHARE_DIR" "$SESSION_NAME" "$SHELL" "${ZDOTDIR:-$HOME}")"
_spare_cmd="$(spare_tabs_launch_cmd "$_spare_label" "$_spare_conf" "$PROJECT_DIR" "$_spare_zdotdir")"
_spare_close_bind="bash -c 'source \"$_WRAPPER_DIR/lib/spare-tabs.sh\" && spare_tabs_close_current \"$_spare_label\"'"

# Launch the session with the COMPLETE three-pane layout FIRST — before any of
# the remaining setup (relaunch context, watchers, hover routing). The agent
# takes multiple seconds to boot; every millisecond of tail work done before
# this call is dead screen time, while tail work done after it runs in the
# shadow of that boot. All panes ride in this one batch so the heal watcher
# never observes a partial layout. -P prints the ledger and AI pane ids (into
# a file in the private attention root, which attention_cleanup removes) so
# the second batch below can name the ledger pane explicitly.
_gt_panes_file="$WISP_DECK_ATTENTION_ROOT/launch-panes"

# Layout self-heal: tmux runs every command of a chain even when one fails, so
# a failed split (any future cause, not just the pty-size race _sane_term_size
# closes) would strand this tab on a lone full-width ledger. The watcher
# rebuilds missing panes once the window has real space and exits the moment
# the three-pane layout exists.
gt_ensure_panes_watch "$TMUX_CMD" "$SESSION_NAME" "$PROJECT_DIR" \
  "$AI_LAUNCH_CMD" "$_spare_cmd" >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &

_wisp_deck_testing_tmux_args=()
if [[ "${WISP_DECK_TESTING:-}" == "1" ]]; then
  _wisp_deck_testing_tmux_args=(-e WISP_DECK_TESTING=1)
fi

env -u WISP_DECK_TESTING "$TMUX_CMD" new-session -d -P -F '#{pane_id}' -x "$_tmux_cols" -y "$_tmux_rows" -s "$SESSION_NAME" "${_wisp_deck_testing_tmux_args[@]}" -e "PATH=$PATH" -e "WISP_DECK_ATTENTION_ROOT=$WISP_DECK_ATTENTION_ROOT" -e "WISP_DECK_ATTENTION_DESCRIPTOR=$WISP_DECK_ATTENTION_DESCRIPTOR" -e "WISP_DECK_ATTENTION_GENERATION=$WISP_DECK_ATTENTION_GENERATION" -e "WISP_DECK_ATTENTION_FILE=$WISP_DECK_ATTENTION_FILE" -e "WISP_DECK=1" -e "WISP_DECK_BOOT=$WISP_DECK_BOOT_ID" -e "WISP_DECK_PROJECT=$PROJECT_NAME" -e "WISP_DECK_PATH=$PROJECT_DIR" -e "WISP_DECK_TOOL=$SELECTED_AI_TOOL" -e "WISP_DECK_TERMINAL=$WISP_DECK_TERMINAL" -e "WISP_DECK_CLAUDE_SESSION=$WISP_DECK_CLAUDE_SESSION" -e "WISP_DECK_CODEX_SESSION=$WISP_DECK_CODEX_SESSION" -e "WISP_DECK_CODEX_SESSION_FILE=$WISP_DECK_CODEX_SESSION_FILE" -e "WISP_DECK_CLAUDE_PROVIDER=$WISP_DECK_CLAUDE_PROVIDER" -e "WISP_DECK_CLAUDE_CONFIG=$WISP_DECK_CLAUDE_CONFIG" -e "WISP_DECK_CODEX_CMD=$WISP_DECK_CODEX_CMD" -e "WISP_DECK_PLAN=$WISP_DECK_PLAN" -e "WISP_DECK_RELAUNCH_FILE=$SHARE_DIR/relaunch-${SESSION_NAME}" -e "WISP_DECK_CLAUDE_ACCOUNT=${WISP_DECK_CLAUDE_ACCOUNT_DIR##*/}" -e "WISP_DECK_SEQ=${_wd_launch_seq}" -e "WISP_DECK_LIB_DIR=$_WRAPPER_DIR/lib" -c "$PROJECT_DIR" \
  "$_pane0_cmd" \; \
  set-option status on \; \
  set-option status-position top \; \
  set-option status-left-length 400 \; \
  set-option status-left "$(tab_view_status_left "$PROJECT_NAME" "$_gt_accent")" \; \
  set-option status-left-style "fg=colour238" \; \
  set-option status-style "bg=default" \; \
  set-option status-right "" \; \
  set-option window-status-format "" \; \
  set-option window-status-current-format "" \; \
  set-option window-status-separator "" \; \
  set-option set-titles off \; \
  set-option pane-border-style "fg=colour238" \; \
  set-option pane-active-border-style "fg=colour${_gt_accent}" \; \
  split-window -h -p "$_pane0_pct" -P -F '#{pane_id}' -c "$PROJECT_DIR" \
  "$AI_LAUNCH_CMD; exec bash" \; \
  set-option -p @gt_ai 1 \; \
  select-pane -L \; \
  split-window -v -p 45 -c "$PROJECT_DIR" "$_spare_cmd" \; \
  select-pane -R > "$_gt_panes_file" 2>&3
_gt_ledger_pane=""
_gt_ai_pane=""
{ { read -r _gt_ledger_pane; read -r _gt_ai_pane; } < "$_gt_panes_file"; } 2>/dev/null || true

# The tab bar draws as the agent pane's top border: now that the panes exist,
# read the AI pane's left offset so the second batch can realign the bar's ┬
# junction onto the real ledger/agent split (the batch-1 bar had no offset).
_gt_ai_left=""
if [ -n "$_gt_ai_pane" ]; then
  _gt_ai_left="$("$TMUX_CMD" display-message -p -t "$_gt_ai_pane" '#{pane_left}' 2>/dev/null)" || _gt_ai_left=""
fi

# Per-session refresh script for the resize/layout hooks: recomputes the bar
# from live state so the junction keeps tracking the split. A plain script
# file keeps the hook command free of nested quoting.
_gt_tabbar_refresh="$SHARE_DIR/tabbar-refresh-${SESSION_NAME}.sh"
{
  printf '#!/bin/bash\n'
  printf 'source %q/tab-view.sh 2>/dev/null || exit 0\n' "$_WRAPPER_DIR/lib"
  printf 'tab_view_refresh_bar %q %q %q\n' "$TMUX_CMD" "$_WRAPPER_DIR/lib" "$SESSION_NAME"
} > "$_gt_tabbar_refresh" 2>/dev/null && chmod +x "$_gt_tabbar_refresh" 2>/dev/null || _gt_tabbar_refresh=""

# ---- The agent is booting in its pane from here on; everything below runs ----
# ---- in the shadow of that boot, before the attach at the end.            ----

# Mid-session agent/account switch: for EVERY session (any tool), persist the
# launch context so the compact-view ledger's pill — and the auto-switch
# trigger — can relaunch the AI pane under another claude login OR another
# agent entirely. The pill's own eligibility gate (2+ logins or 2+ tools)
# lives in the ledger. Cleared by cleanup() on window close. The path matches
# the WISP_DECK_RELAUNCH_FILE stamp on new-session above.
WISP_DECK_RELAUNCH_FILE="$SHARE_DIR/relaunch-${SESSION_NAME}"
write_relaunch_context "$WISP_DECK_RELAUNCH_FILE" "$SELECTED_AI_TOOL" \
  "$AI_TOOL_CMD" "$WISP_DECK_CLAUDE_SETTINGS" \
  "$WISP_DECK_CLAUDE_FILTER" "$PROJECT_DIR" "$_gt_cfg_root" \
  "${AI_TOOLS_AVAILABLE[*]}" "$CLAUDE_CMD" "$OPENCODE_CMD" "$CODEX_CMD" \
  "$WISP_DECK_ATTENTION_ROOT" "$WISP_DECK_ATTENTION_DESCRIPTOR" \
  "$WISP_DECK_CLAUDE_SETTINGS_SOURCE"
export WISP_DECK_RELAUNCH_FILE

# Start the descriptor consumer before the attach (which blocks until the
# session ends).
start_tab_title_watcher "$SESSION_NAME" "$PROJECT_NAME" "$_tab_title_setting" "$TMUX_CMD" "$WISP_DECK_ATTENTION_DESCRIPTOR" "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"

# Session-restore snapshot heartbeat: re-derives the snapshot from all alive
# Wisp Deck sessions. Backgrounded lib function, not an inline loop: each tick
# re-sources the lib in a throwaway bash, so snapshot fixes reach sessions
# already running.
run_snapshot_heartbeat "$_WRAPPER_DIR" "$TMUX_CMD" "$WISP_DECK_SNAPSHOT" >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &
HEARTBEAT_PID=$!

# Drag-dropping a screenshot onto a specific tmux pane is unreliable: tmux
# delivers the paste to the *active* pane, not the pane under the cursor (an
# external file drag never produces a tmux mouse event, so tmux can't know the
# target). Two mitigations below:
#   1. The AI pane is left as the *active* pane (select-pane, and a distinct
#      pane-active-border so focus is visible) -- so a screenshot dropped while
#      the AI pane is focused lands in the AI tool.
#   2. prefix+i injects the most recent screenshot straight into the AI pane
#      regardless of which pane is active. See lib/screenshot.sh.
_screenshot_bind="bash -c 'source \"$_WRAPPER_DIR/lib/screenshot.sh\" && gt_paste_latest_screenshot'"

# tmux normally delivers pointer motion only to the pane underneath it, so the
# ledger cannot observe the event that enters its neighbour. Install a private
# session key table for the ledger pane (its id was captured at creation); it
# forwards the real event normally and injects one out-of-bounds motion into
# this ledger on pane leave.
# Run with `run-shell -b`: the install grabs a server-wide root-table lock (up
# to 15s on a busy many-session server), and running it in the foreground once
# held the spare split and attach hostage for that long.
_ledger_hover_setup="bash -c 'source \"$_WRAPPER_DIR/lib/ledger-hover.sh\" && ledger_hover_install \"\$1\" \"\$2\" \"\$3\" || true' ledger-hover \"$TMUX_CMD\" \"$SESSION_NAME\" \"$_gt_ledger_pane\""

# Restore: replay the captured pane geometry over the just-built panes. The
# build order (ledger, AI, spare) is deterministic and identical to capture
# time, so the panes line up with the layout's cells. MUST be backgrounded
# before the attach: it blocks until the session ends, so any replay placed
# after it never runs while the session is alive. The watcher also re-applies
# after Ghostty's late pty resize (a crash-restored tab is spawned before its
# final size lands, and tmux redistributes the delta equally across columns,
# corrupting the split) and exits once the window size settles.
# Skipped when no layout was captured (old snapshot) — the default split stays.
if [ "$RESTORE_MODE" -eq 1 ] && [ -n "${WISP_DECK_RESUME_LAYOUT:-}" ]; then
  restore_layout_watch "$TMUX_CMD" "$SESSION_NAME" "$WISP_DECK_RESUME_LAYOUT" >/dev/null 2>>"${WISP_DECK_ERROR_LOG:-/dev/null}" &
fi

# Tab view actions. Server-global binds must never bake a session name —
# #{q:...} expands (shell-escaped) at event time, so the handler always acts
# on the session the key/click landed in. The status mouse binds are added in
# the batch BEFORE the ledger-hover install: the hover setup clones the
# session's effective key table, and binds added after the clone would be
# invisible to it (status clicks lost).
_tab_view_dispatch_bind="bash -c 'source \"\$1/tab-view.sh\" && tab_view_dispatch \"\$2\" \"\$1\" \"\$3\" \"\$4\"' wisp-tab-view \"$_WRAPPER_DIR/lib\" \"$TMUX_CMD\" #{q:session_name} #{q:mouse_status_range}"
_tab_view_new_bind="bash -c 'source \"\$1/tab-view.sh\" && tab_view_new_window \"\$2\" \"\$1\" \"\$3\"' wisp-tab-view \"$_WRAPPER_DIR/lib\" \"$TMUX_CMD\" #{q:session_name}"

# Second batch: key binds and hover routing, then the attach. The pane layout
# is already complete (built whole in the new-session batch above, so the
# heal watcher never sees a partial layout it would "fix" into duplicate
# panes); the hover install names the captured ledger pane id rather than a
# position, which the focus watcher may have changed since the first batch.
# The server already exists (started by the sanitized new-session client
# above), so this client needs no env scrub.
# Realign the bar to the AI pane's offset and hook resize/layout changes so
# the ┬ junction keeps tracking the split. Built as an array so the hook
# commands drop out cleanly when the refresh script could not be written.
_gt_tabbar_chain=(set-option status-left "$(tab_view_status_left "$PROJECT_NAME" "$_gt_accent" "$_gt_ai_left")" ';')
if [ -n "$_gt_tabbar_refresh" ]; then
  _gt_tabbar_chain+=(set-hook -t "$SESSION_NAME" client-resized "run-shell -b \"$_gt_tabbar_refresh\"" ';')
  _gt_tabbar_chain+=(set-hook -t "$SESSION_NAME" window-layout-changed "run-shell -b \"$_gt_tabbar_refresh\"" ';')
fi

"$TMUX_CMD" \
  "${_gt_tabbar_chain[@]}" \
  bind-key i run-shell "$_screenshot_bind" \; \
  bind-key t run-shell "env -u TMUX -u TMUX_PANE tmux -L $_spare_label new-window -c \"$PROJECT_DIR\"" \; \
  bind-key w run-shell "$_spare_close_bind" \; \
  bind-key Tab run-shell "env -u TMUX -u TMUX_PANE tmux -L $_spare_label next-window" \; \
  bind-key BTab run-shell "env -u TMUX -u TMUX_PANE tmux -L $_spare_label previous-window" \; \
  bind-key c run-shell "$_tab_view_new_bind" \; \
  bind-key -n MouseDown1Status run-shell "$_tab_view_dispatch_bind" \; \
  bind-key -n MouseDown1StatusLeft run-shell "$_tab_view_dispatch_bind" \; \
  bind-key -n MouseDown1StatusRight run-shell "$_tab_view_dispatch_bind" \; \
  run-shell -b "$_ledger_hover_setup" \; \
  attach-session -t "$SESSION_NAME" \; \
  set-option exit-unattached on 2>&3
