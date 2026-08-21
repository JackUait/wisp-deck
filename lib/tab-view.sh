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

# tab_view_mode [settings_file]
# Print the tab bar's chip mode: `large` (numbered chip + the tab's own title
# and the progress of the turn running in it) or `compact` (the bare numbered
# chip). Absent file, absent key and unknown values all mean large — the mode
# a session comes up in must never depend on a setting nobody has written yet.
tab_view_mode() {
  local file="${1:-${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings}" value=""
  if [ -f "$file" ]; then
    value="$(grep '^tab_bar=' "$file" 2>/dev/null | head -1 | cut -d= -f2- | tr -d '[:space:]')"
  fi
  case "$value" in
    compact) printf 'compact\n' ;;
    *) printf 'large\n' ;;
  esac
  return 0
}

# tab_view_status_left <project_name> [accent_colour] [ai_pane_left] [mode]
# Print the outer status-left format. The bar is drawn as the AGENT pane's top
# border rather than a full-width block: a plain border rule spans the ledger
# column, a ┬ junction lands on the ledger/agent split (ai_pane_left - 1), and
# the project label + window chips + the [+] button sit right after it — over
# the agent pane. A long trailing rule runs to the window edge (tmux clips it
# at status-left-length). Without ai_pane_left the lead rule is skipped.
#
# Chips are numbered 1-based (#{e|+:...} — outer windows keep tmux's 0-based
# indexes) and ride in named click ranges so the status mouse binds can
# identify their target via #{mouse_status_range}. Inside #{W:...} every style
# comma is escaped (#,) or tmux would read it as the iterator's format
# separator — a comma inside a nested #{...} belongs to that format and is left
# alone. The bar's base style (the border rule: fg=colour238, status-left-style
# set at launch) is restored after each coloured segment.
#
# A large chip renders two window options, @wd_tab_title and @wd_tab_progress,
# which tab_view_stamp_windows keeps current. The progress segment is wrapped
# in a #{?...} so an idle tab shows no dangling separator, and its dot carries
# the accent so a working tab is findable at a glance from any other tab.
tab_view_status_left() {
  local project="$1" accent="${2:-209}" ai_left="${3:-}" mode="${4:-large}"
  local restore='#[default]'
  local num='#{e|+:#{window_index},1}'
  local active="#[fg=colour235#,bg=colour${accent}#,bold] ${num} ${restore}"
  local inactive="#[fg=colour245] ${num} ${restore}"
  if [ "$mode" != "compact" ]; then
    local title='#{@wd_tab_title}'
    local prog_active="#{?#{@wd_tab_progress},#[fg=colour${accent}] ● #{@wd_tab_progress},}"
    local prog_inactive="#{?#{@wd_tab_progress},#[fg=colour${accent}] ● #[fg=colour241]#{@wd_tab_progress},}"
    active="#[fg=colour235#,bg=colour${accent}#,bold] ${num} ${restore} #[fg=colour252]${title}${prog_active}${restore}"
    inactive="#[fg=colour240] ${num} #[fg=colour248]${title}${prog_inactive}${restore}"
  fi
  local lead="" tail="" i
  if [ -n "$ai_left" ] && [ "$ai_left" -gt 1 ] 2>/dev/null; then
    for ((i = 0; i < ai_left - 1; i++)); do lead+='─'; done
    lead+='┬'
  fi
  for ((i = 0; i < 400; i++)); do tail+='─'; done
  printf '%s─ #[fg=white,bold]⬡ %s#[default] #{W:#[range=user|wdtab:#{window_id}]#{?window_active,%s,%s}#[norange] }#[range=user|wdnew]#[fg=colour%s,bold] + #[nobold]#[norange] %s' \
    "$lead" "$project" "$active" "$inactive" "$accent" "$tail"
}

# tab_view_title_budget <cols> <window_count>
# Print how many characters of title one chip may spend. Every chip is rendered
# from the SAME format, so they share one budget: it has to shrink as tabs are
# opened or the bar runs past the window edge and tmux clips the right-hand
# tabs out of existence. The reserve covers the ⬡ label and the [+] button
# (20) plus each chip's own number badge, progress and gap (16).
tab_view_title_budget() {
  local cols="${1:-0}" windows="${2:-1}" avail budget
  case "$cols" in ''|*[!0-9]*) cols=0 ;; esac
  case "$windows" in ''|*[!0-9]*) windows=1 ;; esac
  [ "$windows" -lt 1 ] && windows=1
  avail=$((cols - 20))
  budget=$((avail / windows - 16))
  [ "$budget" -gt 32 ] && budget=32
  [ "$budget" -lt 8 ] && budget=8
  printf '%s\n' "$budget"
  return 0
}

# tab_view_chip_title <pane_title> <host> <project> <max_chars>
# Print the title a chip shows: the agent pane's own title, which is where the
# tool stamps a summary of the turn it is running.
#
# Three things happen to it on the way to the bar. tmux defaults a pane's title
# to the hostname, which means the agent has not named anything yet — the tab
# is named after its project instead. The tool's leading glyph (Claude's ✳) is
# dropped, since the chip already carries the accent. And every `#` is deleted:
# tmux draws the status line by parsing #[...] out of the EXPANDED format, so a
# model that titles a turn "fix #[fg=red]" would otherwise repaint the rest of
# the bar in a colour of its choosing.
tab_view_chip_title() {
  local title="${1-}" host="${2-}" project="${3-}" max="${4:-24}" first rest
  case "$max" in ''|*[!0-9]*) max=24 ;; esac
  if [ -z "$title" ] || [ "$title" = "$host" ]; then
    title="$project"
  else
    first="${title%% *}"
    rest="${title#* }"
    if [ "$first" != "$title" ]; then
      case "$first" in
        *[A-Za-z0-9]*) ;;
        *) title="$rest" ;;
      esac
    fi
  fi
  title="${title//#/}"
  title="${title//$'\t'/ }"
  if [ ${#title} -gt "$max" ] && [ "$max" -gt 1 ]; then
    title="${title:0:max-1}…"
  fi
  printf '%s\n' "$title"
  return 0
}

# tab_view_progress_from_pane  (captured pane text on stdin)
# Print the elapsed time of the turn the agent is running right now, or nothing
# when it is idle.
#
# The agent's own live status line is the source: Claude prints
# `✽ Hyperspacing… (8m 25s · ↓ 16.9k tokens)` while a turn runs and replaces it
# in place with a past-tense `✻ Cooked for 1h 38m 25s` when the turn ends. The
# ellipsis before the parenthesis is what separates the two — a summary is
# history, not progress, and a tab reporting it would claim to be working
# forever. The LAST match wins so a line further up the transcript can never
# outrank the running one.
tab_view_progress_from_pane() {
  local line rest elapsed=""
  while IFS= read -r line; do
    case "$line" in
      *'… ('*) ;;
      *) continue ;;
    esac
    rest="${line#*'… ('}"
    rest="${rest%%)*}"
    rest="${rest%% · *}"
    case "$rest" in
      [0-9]*) elapsed="$rest" ;;
    esac
  done
  [ -n "$elapsed" ] && printf '%s\n' "$elapsed"
  return 0
}

# tab_view_stamp_windows <tmux_cmd> <session> <project> [host]
# Refresh every window's @wd_tab_title / @wd_tab_progress from its agent pane,
# so the large bar has something current to render.
#
# One list-panes call inventories the whole session — window, geometry, the
# @gt_ai flag, the values already stamped and the pane's title — because this
# runs twice a second in every open session on the machine. Only the agent pane
# is captured, and a value that has not moved is not written again: a
# set-option repaints the client, so stamping unconditionally would redraw
# every bar on every tick.
#
# A window whose agent pane is missing (a layout still building, or one the
# user broke apart) is skipped rather than stamped with a blank identity.
tab_view_stamp_windows() {
  local tmux_cmd="${1-}" session="${2-}" project="${3-}" host="${4-}"
  local fmt inventory wid wwidth ai pid otitle oprog ptitle
  local cols=0 windows=0 seen=$'\n' budget title prog
  local sep=$'\037'

  [ -n "$tmux_cmd" ] && [ -n "$session" ] || return 0
  if [ -z "$host" ]; then
    [ -n "${_TAB_VIEW_HOST:-}" ] || _TAB_VIEW_HOST="$(hostname 2>/dev/null)"
    host="$_TAB_VIEW_HOST"
  fi

  # A tab is an IFS whitespace character, so bash collapses runs of them and a
  # window with two empty fields in a row would shift every later field left.
  # The unit separator is not whitespace, so empty fields survive it.
  fmt="#{window_id}${sep}#{window_width}${sep}#{@gt_ai}${sep}#{pane_id}${sep}#{@wd_tab_title}${sep}#{@wd_tab_progress}${sep}#{pane_title}"
  inventory="$("$tmux_cmd" list-panes -s -t "$session" -F "$fmt" 2>/dev/null)" || return 0
  [ -n "$inventory" ] || return 0

  while IFS="$sep" read -r wid wwidth ai pid otitle oprog ptitle; do
    [ -n "$wid" ] || continue
    case "$seen" in
      *$'\n'"$wid"$'\n'*) ;;
      *) seen="${seen}${wid}"$'\n'; windows=$((windows + 1)) ;;
    esac
    case "$wwidth" in
      ''|*[!0-9]*) ;;
      *) [ "$wwidth" -gt "$cols" ] && cols="$wwidth" ;;
    esac
  done <<< "$inventory"

  budget="$(tab_view_title_budget "$cols" "$windows")"

  while IFS="$sep" read -r wid wwidth ai pid otitle oprog ptitle; do
    [ -n "$wid" ] && [ "$ai" = "1" ] && [ -n "$pid" ] || continue
    title="$(tab_view_chip_title "$ptitle" "$host" "$project" "$budget")"
    prog="$("$tmux_cmd" capture-pane -p -t "$pid" 2>/dev/null \
      | tab_view_progress_from_pane)"
    if [ "$title" != "$otitle" ]; then
      "$tmux_cmd" set-option -w -t "$wid" @wd_tab_title "$title" 2>/dev/null || true
    fi
    if [ "$prog" != "$oprog" ]; then
      "$tmux_cmd" set-option -w -t "$wid" @wd_tab_progress "$prog" 2>/dev/null || true
    fi
  done <<< "$inventory"
  return 0
}

# tab_view_refresh_bar <tmux_cmd> <lib_dir> <session>
# Recompute the bar from live session state — project name, the active tool's
# accent, the AI pane's current left offset and the saved chip mode — and
# re-set status-left so the ┬ junction tracks the real pane split and a
# mid-session mode change reaches an open window. Bound to the resize/layout
# hooks and safe to call any time; fail-open like everything here.
tab_view_refresh_bar() {
  local tmux_cmd="$1" lib_dir="$2" session="$3"
  local project tool accent mode ai_pane="" ai_left="" id flag
  project="$(_tab_view_session_env "$tmux_cmd" "$session" WISP_DECK_PROJECT)"
  [ -n "$project" ] || return 0
  tool="$(_tab_view_session_env "$tmux_cmd" "$session" WISP_DECK_TOOL)"
  # The watcher calls this with no lib dir (it already has the module stack
  # loaded); only a caller that supplies one may source into itself.
  # shellcheck source=/dev/null
  if ! declare -f get_tool_accent >/dev/null 2>&1 && [ -n "$lib_dir" ]; then
    source "$lib_dir/tmux-session.sh"
  fi
  accent="$(get_tool_accent "${tool:-claude}" 2>/dev/null)" || accent=""
  : "${accent:=209}"
  while read -r id flag; do
    [ "$flag" = "1" ] && { ai_pane="$id"; break; }
  done < <("$tmux_cmd" list-panes -t "$session" -F '#{pane_id} #{@gt_ai}' 2>/dev/null)
  if [ -n "$ai_pane" ]; then
    ai_left="$("$tmux_cmd" display-message -p -t "$ai_pane" '#{pane_left}' 2>/dev/null)"
  fi
  mode="$(tab_view_mode)"
  "$tmux_cmd" set-option -t "$session" status-left \
    "$(tab_view_status_left "$project" "$accent" "$ai_left" "$mode")" 2>/dev/null || true
  return 0
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

# tab_view_close_window <tmux_cmd> <lib_dir> <session> [window]
# Close ONE tab and kill everything that was running in its terminals.
#
# tmux's own kill-window only SIGHUPs each pane's process group. That reaps a
# plain background job but reaches neither a descendant that left the group nor
# — the leak this exists for — the spare pane's terminals: that pane runs a
# client of a nested tmux server shared by every tab, so the tab's inner
# session and every shell in it reparent away from the pane and outlive the
# close forever. Session-wide cleanup covers this when the whole Ghostty tab
# goes (cleanup_tmux_session + spare_tabs_cleanup); a per-tab close had
# nothing, so this is the window-scoped mirror of it: inner spare session
# first (while the pane ttys that identify it still exist), then each pane's
# process tree, then the window.
#
# Fail-open like the rest of this file. Closing the last window ends the
# session, which is the wrapper's own cleanup path and is left to it.
tab_view_close_window() {
  local tmux_cmd="$1" lib_dir="$2" session="$3" window="${4:-}"
  local label pid tty inner
  local pids=()

  [ -n "$window" ] || window="$("$tmux_cmd" display-message -p -t "$session" \
    '#{window_id}' 2>/dev/null)" || return 0
  [ -n "$window" ] || return 0

  # shellcheck source=/dev/null
  declare -f kill_tree >/dev/null 2>&1 || source "$lib_dir/process.sh"
  # shellcheck source=/dev/null
  declare -f spare_tabs_socket >/dev/null 2>&1 || source "$lib_dir/spare-tabs.sh"
  label="$(spare_tabs_socket "$session")"

  while read -r pid tty; do
    [ -n "$pid" ] || continue
    pids+=("$pid")
    inner="$(spare_tabs_session_for_tty "$label" "$tty")"
    [ -n "$inner" ] && spare_tabs_kill_session "$label" "$inner"
  done < <("$tmux_cmd" list-panes -t "$window" -F '#{pane_pid} #{pane_tty}' 2>/dev/null)

  for pid in ${pids[@]+"${pids[@]}"}; do kill_tree "$pid" TERM; done
  "$tmux_cmd" kill-window -t "$window" 2>/dev/null || true
  sleep 0.3
  for pid in ${pids[@]+"${pids[@]}"}; do kill_tree "$pid" KILL; done
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
