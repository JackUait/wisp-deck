#!/bin/bash
# Per-tab session tabs (the tab view). The outer tmux status bar — forced
# visible at the top of the tab, directly under Ghostty's native tabs — shows
# one numbered chip per tmux window plus a [ + ] button. Clicking + (or
# prefix+c) opens a NEW window carrying the full three-pane wisp layout for
# the SAME project folder: compact-view ledger, a fresh AI conversation under
# the session's current tool/account/settings, and a spare pane joining the
# session's existing inner spare server.
#
# Windows, not sessions: the one-Ghostty-tab-one-session invariant holds, the
# chip list is a live tmux format (no repaint machinery), and the wrapper's
# session-wide cleanup already covers every window's panes.
#
# Everything here is fail-open: helpers print nothing and exit 0 when the tmux
# server, the session env, or the relaunch context is missing.
# See docs/superpowers/specs/2026-07-22-tab-view-design.md.

# tab_view_status_left <project_name> [accent_colour]
# Print the outer status-left format: project label, the window chip list, and
# the + button. Chips are numbered 1-based (#{e|+:...} — outer windows keep
# tmux's 0-based indexes) and ride in named click ranges so the status mouse
# binds can identify their target via #{mouse_status_range}. Inside #{W:...}
# every style comma is escaped (#,) or tmux would read it as the iterator's
# format separator. The bar's base style (fg=white,bg=colour236,bold,
# status-left-style set at launch) is restored after each coloured chip.
tab_view_status_left() {
  local project="$1" accent="${2:-209}"
  local restore='#[default]#[fg=white#,bg=colour236#,bold]'
  local num='#{e|+:#{window_index},1}'
  local active="#[fg=colour235#,bg=colour${accent}#,bold] ${num} ${restore}"
  local inactive="#[fg=colour245] ${num} ${restore}"
  printf ' ⬡ %s #{W:#[range=user|wdtab:#{window_id}]#{?window_active,%s,%s}#[norange] }#[range=user|wdnew]#[fg=colour%s,bold] + #[nobold]#[norange]' \
    "$project" "$active" "$inactive" "$accent"
}

# _tab_view_session_env <tmux_cmd> <session> <var>
# Print a session-env value, empty when unset or the server is gone. tmux
# prints `NAME=value` when set and `-NAME` when unset — only the former counts.
_tab_view_session_env() {
  local tmux_cmd="$1" session="$2" var="$3" line
  line="$("$tmux_cmd" show-environment -t "$session" "$var" 2>/dev/null)" || return 0
  case "$line" in
    "$var"=*) printf '%s\n' "${line#"$var"=}" ;;
  esac
  return 0
}

# tab_view_new_window <tmux_cmd> <lib_dir> <session>
# Open a new window in <session> with the wrapper's exact three-pane layout
# for the session's project folder. The AI pane launches a FRESH conversation
# built from the session's relaunch context (same tool, account, settings and
# screenshot filter) with the attention env explicitly blanked: one attention
# generation has one publisher — window 0's — so extra windows run the raw,
# unsupervised launch.
tab_view_new_window() {
  local tmux_cmd="$1" lib_dir="$2" session="$3"
  local relaunch_file share_dir account account_dir provider codex_cmd

  relaunch_file="$(_tab_view_session_env "$tmux_cmd" "$session" WISP_DECK_RELAUNCH_FILE)"
  [ -n "$relaunch_file" ] && [ -f "$relaunch_file" ] || return 0
  share_dir="$(dirname "$relaunch_file")"

  # Dispatch runs in a fresh bash (tmux run-shell), outside wrapper.sh's
  # module stack. account-switch.sh self-sources its own deps.
  # shellcheck source=/dev/null
  declare -f build_ai_launch_cmd >/dev/null 2>&1 || source "$lib_dir/tmux-session.sh"
  # shellcheck source=/dev/null
  declare -f build_switch_launch_cmd >/dev/null 2>&1 || source "$lib_dir/account-switch.sh"
  # shellcheck source=/dev/null
  declare -f spare_tabs_socket >/dev/null 2>&1 || source "$lib_dir/spare-tabs.sh"

  local _rc_tool='' _rc_tool_cmd='' _rc_settings='' _rc_settings_source='' \
    _rc_filter='' _rc_project_dir='' _rc_accounts_dir='' _rc_pointer='' \
    _rc_list='' _rc_colors='' _rc_default_label='' _rc_tools='' \
    _rc_claude_cmd='' _rc_opencode_cmd='' _rc_codex_cmd='' _rc_tool_pref='' \
    _rc_attention_root='' _rc_attention_descriptor='' _rc_config_pointer='' \
    _rc_configs_dir='' _rc_configs_list=''
  _read_relaunch_ctx "$relaunch_file"
  [ -n "$_rc_tool" ] && [ -n "$_rc_tool_cmd" ] && [ -d "$_rc_project_dir" ] || return 0
  local dir="$_rc_project_dir"

  # The session's active login (basename, stamped per-session at launch and on
  # every mid-session switch). Empty = the Default Keychain login.
  account="$(_tab_view_session_env "$tmux_cmd" "$session" WISP_DECK_CLAUDE_ACCOUNT)"
  account_dir=""
  [ -n "$account" ] && [ -n "$_rc_accounts_dir" ] && account_dir="$_rc_accounts_dir/$account"

  # Backend context for build_ai_launch_cmd's ChatGPT-bridge branch.
  provider="$(_tab_view_session_env "$tmux_cmd" "$session" WISP_DECK_CLAUDE_PROVIDER)"
  codex_cmd="$(_tab_view_session_env "$tmux_cmd" "$session" WISP_DECK_CODEX_CMD)"

  local ai_cmd
  ai_cmd="$(WISP_DECK_ATTENTION_FILE='' WISP_DECK_ATTENTION_GENERATION='' \
    WISP_DECK_CLAUDE_PROVIDER="$provider" WISP_DECK_CODEX_CMD="$codex_cmd" \
    build_switch_launch_cmd "$_rc_tool" "$_rc_tool_cmd" "$_rc_settings" \
    "$_rc_filter" "$dir" "$account_dir" "")" || return 0
  [ -n "$ai_cmd" ] || return 0

  # The spare pane joins the session's EXISTING inner spare server (per-session
  # socket, config written by the wrapper at launch); its new-session lands as
  # a sibling inner session and the wrapper's spare_tabs_cleanup already kills
  # the whole socket. spare_tabs_launch_cmd falls back to a plain shell itself.
  local spare_label spare_conf spare_zdotdir spare_cmd
  spare_label="$(spare_tabs_socket "$session")"
  spare_conf="$share_dir/spare-${session}.conf"
  spare_zdotdir="$share_dir/spare-zdotdir-${session}"
  [ -d "$spare_zdotdir" ] || spare_zdotdir=""
  spare_cmd="$(spare_tabs_launch_cmd "$spare_label" "$spare_conf" "$dir" "$spare_zdotdir")"

  # Build the window with the wrapper's geometry: ledger, AI (75% right, marked
  # @gt_ai for pane consumers), spare (45% bottom-left), AI focused. `-t
  # <session>:` (empty window part) appends at the session's next free index.
  local pane0_cmd ledger_pane ai_pane
  pane0_cmd="source \"$lib_dir/compact-view.sh\" && compact_view \"$dir\"; exec bash"
  ledger_pane="$("$tmux_cmd" new-window -t "${session}:" -P -F '#{pane_id}' \
    -c "$dir" "$pane0_cmd" 2>/dev/null)" || return 0
  [ -n "$ledger_pane" ] || return 0
  ai_pane="$("$tmux_cmd" split-window -h -p 75 -P -F '#{pane_id}' -c "$dir" \
    -t "$ledger_pane" "$ai_cmd; exec bash" 2>/dev/null)" || true
  if [ -n "$ai_pane" ]; then
    "$tmux_cmd" set-option -p -t "$ai_pane" @gt_ai 1 2>/dev/null || true
  fi
  "$tmux_cmd" split-window -v -p 45 -c "$dir" -t "$ledger_pane" "$spare_cmd" 2>/dev/null || true
  if [ -n "$ai_pane" ]; then
    "$tmux_cmd" select-pane -t "$ai_pane" 2>/dev/null || true
  fi
  return 0
}

# tab_view_dispatch <tmux_cmd> <lib_dir> <session> <mouse_status_range>
# Route a status-bar click by its user-range tag. Bound server-globally in
# wrapper.sh with #{q:session_name}/#{q:mouse_status_range}, so it always acts
# on the session the click landed in.
tab_view_dispatch() {
  local tmux_cmd="$1" lib_dir="$2" session="$3" range="${4:-}"
  case "$range" in
    wdnew)
      tab_view_new_window "$tmux_cmd" "$lib_dir" "$session"
      ;;
    wdtab:*)
      "$tmux_cmd" select-window -t "${range#wdtab:}" 2>/dev/null || true
      ;;
  esac
  return 0
}
