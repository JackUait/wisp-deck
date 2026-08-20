#!/bin/bash
# shellcheck disable=SC2059  # Intentional: ANSI escape variables in printf format strings
# Mid-session Claude account switching — the clickable ledger pill and the AI-pane
# relaunch behind it. Lets the user change the active native Claude login WHILE a
# session runs: click the account pill in the compact-view ledger, pick a login in
# the popup, and the running `claude` is relaunched under that login's isolated
# CLAUDE_CONFIG_DIR in continue (`-c`) mode. History is shared across accounts
# (symlinked into ~/.claude), so the conversation carries over.
#
# Depends on (sourced alongside by the caller): statusline.sh (gt_* helpers),
# claude-accounts.sh (pointer helpers), tmux-session.sh (build_ai_launch_cmd),
# and — for the relaunch's shared-state sync — claude-shared-settings.sh.

# ai-tools.sh is the one dependency we self-source rather than assume, because
# _tool_cmd_for needs resolve_opencode_cmd in contexts that do NOT load the full
# wrapper lib set: the ledger pane (zsh, hence the WISP_DECK_LIB_DIR fallback —
# BASH_SOURCE is empty there) and the auto-switch subshell. Guarded on the
# function, so the usual case (caller already sourced it) costs nothing.
if ! declare -f resolve_opencode_cmd >/dev/null 2>&1; then
  _as_lib_dir="${WISP_DECK_LIB_DIR:-${BASH_SOURCE[0]%/*}}"
  # shellcheck source=/dev/null
  [ -f "$_as_lib_dir/ai-tools.sh" ] && source "$_as_lib_dir/ai-tools.sh"
  unset _as_lib_dir
fi

# The ledger and auto-switch paths source this module in fresh zsh/bash
# processes, outside wrapper.sh's module stack. Load the generation runtime here
# as well so every respawn can fence the previous adapter before rebuilding.
if ! declare -f attention_begin_generation >/dev/null 2>&1; then
  _as_attention_lib_dir="${WISP_DECK_LIB_DIR:-${BASH_SOURCE[0]%/*}}"
  # shellcheck source=/dev/null
  [ -f "$_as_attention_lib_dir/attention.sh" ] && source "$_as_attention_lib_dir/attention.sh"
  unset _as_attention_lib_dir
fi

# Generation-local Claude settings must be rebuilt by the independent ledger
# and auto-switch shells too; they do not inherit wrapper.sh's sourced helpers.
if ! declare -f write_claude_launch_settings >/dev/null 2>&1; then
  _as_settings_lib_dir="${WISP_DECK_LIB_DIR:-${BASH_SOURCE[0]%/*}}"
  # shellcheck source=/dev/null
  [ -f "$_as_settings_lib_dir/settings-json.sh" ] && source "$_as_settings_lib_dir/settings-json.sh"
  unset _as_settings_lib_dir
fi

# claude-configs.sh provides the subscription/backend pointer helpers
# (get_active_claude_config, resolve_claude_config_path, set_active_claude_config,
# get_claude_config_provider) the mid-session backend switch needs. Self-sourced
# with the same guard/fallback so the ledger pane and bare unit-test sources that
# don't load the full lib set can still switch subscriptions.
if ! declare -f get_active_claude_config >/dev/null 2>&1; then
  _as_configs_lib_dir="${WISP_DECK_LIB_DIR:-${BASH_SOURCE[0]%/*}}"
  # shellcheck source=/dev/null
  [ -f "$_as_configs_lib_dir/claude-configs.sh" ] && source "$_as_configs_lib_dir/claude-configs.sh"
  unset _as_configs_lib_dir
fi

# reload_switcher_lib <lib_dir> — re-source THIS module from disk so a
# long-running ledger picks up on-disk edits to the switcher (popup dimensions,
# flags, backdrop) without the whole pane having to restart. Called right before
# opening the switcher. No-op when the file is missing (keeps the resident copy).
reload_switcher_lib() {
  local lib_dir="$1"
  [ -n "$lib_dir" ] || return 0
  # session-pool.sh first: the switch flow depends on its helpers, and a
  # long-running ledger that predates the pool never sourced it at all —
  # without this, capture and handoff are silently skipped until the pane
  # restarts.
  # shellcheck source=/dev/null
  [ -f "$lib_dir/session-pool.sh" ] && source "$lib_dir/session-pool.sh"
  # shellcheck source=/dev/null
  [ -f "$lib_dir/account-switch.sh" ] && source "$lib_dir/account-switch.sh"
  return 0
}

# switcher_supports_session_flags — exit 0 when the installed wisp-deck-tui
# accepts --active/--result-file on claude-account-switch. The lib is installed
# as a live symlink while the binary is a copy, so a newer lib can face an older
# binary that would REJECT the new flags (cobra errors out before the UI runs)
# and the switcher would silently stop switching. Legacy is only assumed when
# the help output positively shows the command WITHOUT --result-file; a missing
# binary or unreadable help counts as supported (the popup can't run either
# way, and the current binary is the norm). Cached per process ($$-keyed would
# be overkill: reload_switcher_lib re-sources this file, so keep the cache in a
# var this file does not reset).
_probe_switcher_capabilities() {
  if [ -n "${_GT_SWITCHER_FLAGS_PROBE:-}" ] \
     && [ -n "${_GT_SWITCHER_TOOLS_PROBE:-}" ] \
     && [ -n "${_GT_SWITCHER_CONFIGS_PROBE:-}" ] \
     && [ -n "${_GT_SWITCHER_WORKTREES_PROBE:-}" ]; then
    return 0
  fi
  local help has_command=0
  help="$(wisp-deck-tui claude-account-switch --help 2>&1)" || help=""
  case "$help" in
    *claude-account-switch*) has_command=1 ;;
  esac
  case "$has_command:$help" in
    1:*--result-file*) _GT_SWITCHER_FLAGS_PROBE=ok ;;
    1:*) _GT_SWITCHER_FLAGS_PROBE=legacy ;;
    *) _GT_SWITCHER_FLAGS_PROBE=ok ;;
  esac
  case "$has_command:$help" in
    1:*--tools*) _GT_SWITCHER_TOOLS_PROBE=ok ;;
    1:*) _GT_SWITCHER_TOOLS_PROBE=legacy ;;
    *) _GT_SWITCHER_TOOLS_PROBE=ok ;;
  esac
  case "$has_command:$help" in
    1:*--active-config*) _GT_SWITCHER_CONFIGS_PROBE=ok ;;
    1:*) _GT_SWITCHER_CONFIGS_PROBE=legacy ;;
    *) _GT_SWITCHER_CONFIGS_PROBE=ok ;;
  esac
  case "$has_command:$help" in
    1:*--active-worktree*) _GT_SWITCHER_WORKTREES_PROBE=ok ;;
    1:*) _GT_SWITCHER_WORKTREES_PROBE=legacy ;;
    *) _GT_SWITCHER_WORKTREES_PROBE=ok ;;
  esac
}

switcher_supports_session_flags() {
  _probe_switcher_capabilities
  [ "$_GT_SWITCHER_FLAGS_PROBE" = ok ]
}

# switcher_supports_agent_rows — exit 0 when the installed wisp-deck-tui
# accepts --tools/--active-tool on claude-account-switch (agent rows). Same
# legacy-detection contract as switcher_supports_session_flags: only a help
# output that positively shows the command WITHOUT --tools counts as legacy.
switcher_supports_agent_rows() {
  _probe_switcher_capabilities
  [ "$_GT_SWITCHER_TOOLS_PROBE" = ok ]
}

# switcher_supports_subscription_rows — exit 0 when the installed wisp-deck-tui
# accepts --active-config on claude-account-switch (subscription/backend rows).
# Same legacy-detection contract as the probes above: only a help output that
# positively shows the command WITHOUT --active-config counts as legacy.
switcher_supports_subscription_rows() {
  _probe_switcher_capabilities
  [ "$_GT_SWITCHER_CONFIGS_PROBE" = ok ]
}

# switcher_supports_worktree_rows — exit 0 when the installed wisp-deck-tui
# accepts --worktrees/--active-worktree on claude-account-switch (checkout rows).
# Same legacy-detection contract as the probes above: only a help output that
# positively shows the command WITHOUT --active-worktree counts as legacy.
switcher_supports_worktree_rows() {
  _probe_switcher_capabilities
  [ "$_GT_SWITCHER_WORKTREES_PROBE" = ok ]
}

# _session_worktrees <project_dir> — print the project's checkouts as
# "branch:path" lines, in git's own order (the main checkout is always its first
# porcelain block, so it leads). A detached checkout reports "(detached)".
# Silent and successful outside a repo, or without git at all: the switcher then
# simply shows no checkout group.
_session_worktrees() {
  # NOT `path`: the ledger's fallback renderer calls this under zsh, where `path`
  # is tied to $PATH — `local path=""` would blank the search path for the whole
  # function, so `command -v git` below would fail and the switcher would show no
  # checkout group at all.
  local dir="$1" line wt_path="" branch=""
  [ -n "$dir" ] && [ -d "$dir" ] || return 0
  command -v git >/dev/null 2>&1 || return 0
  while IFS= read -r line; do
    case "$line" in
      "worktree "*)
        wt_path="${line#worktree }"
        branch=""
        ;;
      "branch refs/heads/"*)
        branch="${line#branch refs/heads/}"
        ;;
      detached)
        branch="(detached)"
        ;;
      "")
        [ -n "$wt_path" ] && printf '%s:%s\n' "${branch:-(detached)}" "$wt_path"
        wt_path=""
        branch=""
        ;;
    esac
  done < <(git -C "$dir" worktree list --porcelain 2>/dev/null)
  # git ends every block with a blank line, so this only fires for a truncated
  # final block.
  [ -n "$wt_path" ] && printf '%s:%s\n' "${branch:-(detached)}" "$wt_path"
  return 0
}

# _resolve_dir <path> — print the physical path (symlinks resolved), or the
# input unchanged when it is not a reachable directory. `git worktree list`
# always reports resolved paths, so every comparison against that listing has to
# be made in the same terms: a project reached through a symlink (a macOS
# /var → /private/var temp dir, a symlinked checkout) would otherwise match no
# row at all, losing both the running checkout's marker and the no-op guard.
_resolve_dir() {
  local dir="$1" resolved
  [ -n "$dir" ] || return 0
  resolved="$(cd "$dir" 2>/dev/null && pwd -P)" || resolved=""
  printf '%s\n' "${resolved:-$dir}"
}

# _worktree_choice_ready <project_dir> <path> — revalidate a chosen checkout
# immediately before mutation. The popup can sit open while worktrees are
# removed elsewhere, and an unvalidated path would respawn all three panes into
# an arbitrary directory.
_worktree_choice_ready() {
  local project_dir="$1" wanted="$2" line
  [ -n "$wanted" ] && [ -d "$wanted" ] || return 1
  wanted="$(_resolve_dir "$wanted")"
  while IFS= read -r line; do
    [ "${line#*:}" = "$wanted" ] && return 0
  done < <(_session_worktrees "$project_dir")
  return 1
}

# _session_side_panes <tmux_cmd> — print "<ledger_pane> <spare_pane>" (either
# may be empty). The agent pane carries @gt_ai; of the two that don't, the
# ledger is the one whose start command runs compact_view and the spare is the
# other. Identifying them by start command rather than by index survives a pane
# heal, which rebuilds them in whatever order the splits succeeded. The field
# separator is '|' because an unset @gt_ai renders EMPTY — space-separated
# fields would then collapse and shift the start command into the flag.
_session_side_panes() {
  local tmux_cmd="$1" id flag cmd ledger="" spare=""
  while IFS='|' read -r id flag cmd; do
    [ -n "$id" ] || continue
    [ "$flag" = "1" ] && continue
    case "$cmd" in
      *compact_view*) [ -z "$ledger" ] && ledger="$id" ;;
      *) [ -z "$spare" ] && spare="$id" ;;
    esac
  done < <("$tmux_cmd" list-panes -s -F '#{pane_id}|#{@gt_ai}|#{pane_start_command}' 2>/dev/null)
  printf '%s %s\n' "$ledger" "$spare"
}

# account_pill_enabled <relaunch_file> <list_file> — exit 0 when the ledger
# should show the switch pill: the relaunch file exists AND there is anything
# to switch to — 2+ claude logins (a single managed login + the implicit
# Default) OR 2+ agents in the context's tools list (the switcher offers other
# agents even with a single claude login).
account_pill_enabled() {
  local relaunch_file="$1" list_file="$2"
  [ -n "$relaunch_file" ] && [ -f "$relaunch_file" ] || return 1
  gt_multiple_claude_accounts "$list_file" && return 0
  # wc -w, not `set -- $tools`: this runs inside the compact-view pane's zsh,
  # which does not word-split an unquoted expansion.
  local tools configs_list
  tools="$(sed -n 's/^tools=//p' "$relaunch_file" 2>/dev/null)"
  [ "$(printf '%s\n' "$tools" | wc -w)" -ge 2 ] && return 0
  # A configured subscription is switchable too, so the pill must show even with
  # a single login and no other agent.
  configs_list="$(sed -n 's/^configs_list=//p' "$relaunch_file" 2>/dev/null)"
  [ -n "$configs_list" ] && [ -s "$configs_list" ] && return 0
  # So are the project's other checkouts — without this the worktree rows are
  # unreachable for a single-login, single-agent session.
  local project_dir
  project_dir="$(sed -n 's/^project_dir=//p' "$relaunch_file" 2>/dev/null)"
  _project_has_worktrees "$project_dir"
}

# _project_has_worktrees <dir> — exit 0 when the project has more than one
# checkout. Decided by two stats rather than a `git worktree list` subprocess:
# this runs on every ledger refresh tick, and the pane's hot path must stay free
# of spawns. A linked worktree has a .git FILE (the main checkout is always
# there to go back to); a main checkout with linked worktrees has a
# .git/worktrees directory. Deliberately no glob over that directory — this
# also runs under the compact-view pane's zsh, where an unmatched glob is fatal.
_project_has_worktrees() {
  local dir="$1"
  [ -n "$dir" ] || return 1
  [ -f "$dir/.git" ] && return 0
  [ -d "$dir/.git/worktrees" ]
}

# pill_current <tool> <pointer_file> <list_file> <default_label_file> \
#   <colors_file> [tmux_cmd] — agent-aware pill content: for a non-claude tool
# print its display name in the tool's accent color (the pane runs that agent;
# a claude login label would lie); for claude, delegate to account_current.
pill_current() {
  local tool="$1"
  shift
  if [ -n "$tool" ] && [ "$tool" != "claude" ]; then
    local label
    case "$tool" in
      opencode) label="OpenCode" ;;
      codex) label="Codex" ;;
      *) label="$tool" ;;
    esac
    printf '%s\t%s\t\xF3\xB0\x80\x84\n' "$label" "$(get_tool_accent "$tool")"
    return 0
  fi
  # claude: when a subscription backend runs, it replaces the account on the
  # pill — the account is overridden while a backend runs, so a login label
  # would lie. config_pointer/configs_list are the extra trailing args the
  # ledger passes; absent for legacy callers, which fall back to the pane's
  # launch-frozen WISP_DECK_PLAN (same label the header shows) before the account.
  local pointer_file="$1" list_file="$2" default_label_file="$3" colors_file="$4" \
    tmux_cmd="${5:-}" config_pointer="${6:-}" configs_list="${7:-}"
  if [ -n "$config_pointer" ]; then
    local active_config
    if [ -n "$tmux_cmd" ]; then
      active_config="$(current_session_config "$tmux_cmd" "$config_pointer")"
    else
      active_config="$(get_active_claude_config "$config_pointer")"
    fi
    if [ -n "$active_config" ]; then
      # The subscription's own persistent color (claude-config-colors next to
      # the configs list; account colors are the avoid set), matching the
      # switcher rows and the statusline usage bars. 111 is the legacy shared
      # blue for callers without gt_config_color loaded.
      local config_color=111
      if command -v gt_config_color >/dev/null 2>&1 && [ -n "$configs_list" ]; then
        config_color="$(gt_config_color "$(dirname "$configs_list")/claude-config-colors" \
          "$active_config" "$colors_file")"
        [ -n "$config_color" ] || config_color=111
      fi
      printf '%s\t%s\t\xE2\x9C\xA6\n' "$(claude_config_name "$active_config" "$configs_list")" "$config_color"
      return 0
    fi
    # config_pointer present but active_config empty: the per-session stamp
    # authoritatively says standard Claude — show the account, never the stale
    # launch plan below.
  elif [ -n "${WISP_DECK_PLAN:-}" ] && [ "${WISP_DECK_PLAN:-}" != "Standard Claude" ]; then
    # Legacy relaunch context (session launched before the subscription feature)
    # carries no config_pointer, so the per-session machinery above is
    # unavailable. WISP_DECK_PLAN — the pane's launch-frozen subscription label,
    # the same one the ledger header already shows — is the fallback so the pill
    # agrees with the header without waiting for a full relaunch. It is frozen at
    # launch (never the global pointer), so it cannot "switch back" when another
    # session flips the pointer.
    printf '%s\t%s\t\xE2\x9C\xA6\n' "$WISP_DECK_PLAN" 111
    return 0
  fi
  # account_current prints "label\tcolor"; the pill contract appends the
  # identity-kind glyph as a third field — the account person here.
  printf '%s\t\xF3\xB0\x80\x84\n' \
    "$(account_current "$pointer_file" "$list_file" "$default_label_file" "$colors_file" "$tmux_cmd")"
}

# account_pill <label> <color> [hover] [glyph] — render the account pill for the ledger
# bottom bar. Line 1 is the drawable string (a pad space + the 󰀄 glyph + a space +
# the label + a trailing pad space, the glyph/label in the account's 256-color);
# line 2 is its VISIBLE click width so the click handler can bound the hit region.
# Width = pad space + glyph + space + label + trailing pad space. When hover is 1
# the pill gains a background bar (48;5;238, matching the file-row hover) so the
# pointer target reads as pressable; the background+fg SGR open at the very start
# and the reset closes after the trailing pad, so BOTH pad spaces are highlighted —
# a small margin of breathing room on the left and right of the text. The two pad
# spaces are reserved in the plain pill too, so the visible width is identical
# either way and the bottom bar never shifts on hover.
account_pill() {
  local label="$1" color="$2" hover="${3:-0}" glyph="${4:-}"
  # The identity-kind glyph: callers pass the subscription spark (✦) while a
  # backend runs; no arg keeps the account person (both are one column, so the
  # width math below is glyph-independent).
  [ -n "$glyph" ] || glyph="$(printf '\xF3\xB0\x80\x84')"
  if [ "$hover" = 1 ]; then
    printf '\033[48;5;238;38;5;%sm %s %s \033[0m\n' "$color" "$glyph" "$label"
  else
    printf ' \033[38;5;%sm%s %s\033[0m \n' "$color" "$glyph" "$label"
  fi
  printf '%s\n' "$((4 + ${#label}))"
}

# current_session_account <tmux_cmd> <pointer_file> — print the account dir name
# THIS session's AI pane is running. The active-account POINTER is global (any
# session's switch or the launcher rewrites it), so a mid-session switch stamps
# the pane's own account into the tmux session env (WISP_DECK_CLAUDE_ACCOUNT,
# set at launch by wrapper.sh and updated by relaunch_ai_pane). A stamped empty
# value means the Default login and must NOT fall back; only an UNSTAMPED
# session (pre-stamp launch: tmux prints `-NAME`) falls back to the pointer.
current_session_account() {
  local tmux_cmd="$1" pointer_file="$2" session_env="" line
  if [ "$#" -ge 3 ]; then
    session_env="$3"
  else
    session_env="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_ACCOUNT 2>/dev/null)" \
      || session_env=""
  fi
  while IFS= read -r line; do
    case "$line" in
      WISP_DECK_CLAUDE_ACCOUNT=*)
        printf '%s\n' "${line#WISP_DECK_CLAUDE_ACCOUNT=}"
        return 0
        ;;
    esac
  done <<< "$session_env"
  get_active_claude_account "$pointer_file"
}

# _current_session_identities <tmux_cmd> <account_pointer> <config_pointer>
# Set the caller-local session_acct/session_config after reading both stamps with
# one tmux client. A missing stamp falls back to its global pointer; a stamped
# empty remains authoritative. Uses the same dynamic-scoping contract as
# _read_relaunch_ctx, avoiding a command-substitution subshell.
_current_session_identities() {
  local tmux_cmd="$1" account_pointer="$2" config_pointer="$3"
  local session_env="" account="" config="" account_stamped=0 config_stamped=0 line
  session_env="$("$tmux_cmd" show-environment 2>/dev/null)" || session_env=""
  while IFS= read -r line; do
    case "$line" in
      WISP_DECK_CLAUDE_ACCOUNT=*)
        account="${line#WISP_DECK_CLAUDE_ACCOUNT=}"
        account_stamped=1
        ;;
      WISP_DECK_CLAUDE_CONFIG=*)
        config="${line#WISP_DECK_CLAUDE_CONFIG=}"
        config_stamped=1
        ;;
    esac
  done <<< "$session_env"
  if [ "$account_stamped" = 1 ]; then
    session_acct="$account"
  else
    session_acct="$(current_session_account "$tmux_cmd" "$account_pointer" "$session_env")"
  fi
  if [ "$config_stamped" = 1 ]; then
    session_config="$config"
  else
    session_config="$(current_session_config "$tmux_cmd" "$config_pointer" "$session_env")"
  fi
}

# current_session_config <tmux_cmd> <config_pointer> — print the subscription
# filename THIS session's AI pane is running (empty = standard Claude). Mirrors
# current_session_account: the active-config POINTER is global (any session's
# switch rewrites it), so the pane's own backend is stamped in the tmux session
# env (WISP_DECK_CLAUDE_CONFIG). A stamped value — including a stamped empty
# (standard) — wins; only an UNSTAMPED session falls back to the pointer.
current_session_config() {
  local tmux_cmd="$1" pointer_file="$2" session_env="" line
  if [ "$#" -ge 3 ]; then
    session_env="$3"
  else
    session_env="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_CONFIG 2>/dev/null)" \
      || session_env=""
  fi
  while IFS= read -r line; do
    case "$line" in
      WISP_DECK_CLAUDE_CONFIG=*)
        printf '%s\n' "${line#WISP_DECK_CLAUDE_CONFIG=}"
        return 0
        ;;
    esac
  done <<< "$session_env"
  get_active_claude_config "$pointer_file"
}

# account_current <pointer_file> <list_file> <default_label_file> <colors_file> \
#   [tmux_cmd] — print "<label>\t<color>" for the account to show on the pill.
# With tmux_cmd, that is the account THIS session's pane is running (see
# current_session_account) — the global pointer alone would make the pill
# "switch back" whenever another session or the launcher rewrites it. Without
# tmux_cmd (legacy callers), the pointer is used. The label mirrors the
# statusline (gt_claude_account_label reads the dir off the tail of its first
# arg, so the bare dir name works); the color is the persisted per-account hue.
account_current() {
  local pointer_file="$1" list_file="$2" default_label_file="$3" colors_file="$4" tmux_cmd="${5:-}"
  local dir label color
  if [ -n "$tmux_cmd" ]; then
    dir="$(current_session_account "$tmux_cmd" "$pointer_file")"
  else
    dir="$(get_active_claude_account "$pointer_file")"
  fi
  label="$(gt_claude_account_label "$dir" "$list_file" "$default_label_file")"
  # Subscription colors are the avoid set so a fresh login assignment never
  # collides with a subscription's hue (mirrors the pill's gt_config_color).
  color="$(gt_account_color "$colors_file" "$dir" "$(dirname "$colors_file")/claude-config-colors")"
  printf '%s\t%s\n' "$label" "$color"
}

# find_ai_pane <tmux_cmd> — print the pane id of the AI pane. wrapper.sh tags it
# with the per-pane option @gt_ai=1; list every session pane's id + flag and pick
# the tagged one. Empty when none is tagged (e.g. the harness).
find_ai_pane() {
  local tmux_cmd="$1" id flag
  while read -r id flag; do
    [ "$flag" = "1" ] && { printf '%s\n' "$id"; return 0; }
  done < <("$tmux_cmd" list-panes -s -F '#{pane_id} #{@gt_ai}' 2>/dev/null)
  return 0
}

# current_ai_session <tmux_cmd> — print the conversation id this pane's switch
# should resume: the resume-validated (durable) id the statusline stamped into
# the session env (gt_stamp_claude_session sets WISP_DECK_CLAUDE_SESSION only
# once the transcript is durable). tmux prints `NAME=value` when set, or `-NAME`
# (leading dash) when unset — only the former yields an id. Empty when unstamped,
# so the relaunch falls back to `-c`.
#
# Guard against resurrecting a just-closed conversation: the statusline also
# records claude's LIVE session id every render (WISP_DECK_CLAUDE_LIVE_SESSION,
# no durability gate). When the user runs /new (or /clear), the live id changes
# at once while the durable stamp keeps naming the OLD conversation (the fresh
# one has no model turn yet, so it is not resumable). If the live id is stamped
# AND differs from the durable one, the durable id no longer names the pane's
# current conversation — return empty so the switch launches a FRESH claude
# rather than reopening the session the user just closed. An unstamped live var
# (older statusline, or a pane before its first render) never suppresses.
current_ai_session() {
  local tmux_cmd="$1" line sid live
  line="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_SESSION 2>/dev/null)" || return 0
  case "$line" in
    WISP_DECK_CLAUDE_SESSION=*) sid="${line#WISP_DECK_CLAUDE_SESSION=}" ;;
    *) return 0 ;;
  esac
  [ -n "$sid" ] || return 0
  live="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_LIVE_SESSION 2>/dev/null)" || live=""
  case "$live" in
    WISP_DECK_CLAUDE_LIVE_SESSION=*)
      live="${live#WISP_DECK_CLAUDE_LIVE_SESSION=}"
      [ -n "$live" ] && [ "$live" != "$sid" ] && return 0
      ;;
  esac
  printf '%s\n' "$sid"
  return 0
}

# _ai_input_line_empty <tmux_cmd> <pane> — exit 0 when the pane's input line is
# EMPTY. The input box renders below the transcript, so the input line is the
# LAST ❯-prefixed row of the frame (a bare ❯ higher up is transcript content;
# dialog rows like "❯ 2. No, exit" carry text and read as non-empty). Same
# empty-prompt heuristic wait_ai_pane_ready trusts, incl. the U+00A0 pad. A
# frame with no ❯ at all is "can't tell" — exit 1 so the caller stays on the
# slow, fail-open path.
_ai_input_line_empty() {
  local tmux_cmd="$1" pane="$2" nbsp line last=""
  nbsp="$(printf '\302\240')"
  while IFS= read -r line; do
    case "$line" in "❯"*) last="$line" ;; esac
  done < <("$tmux_cmd" capture-pane -p -t "$pane" 2>/dev/null)
  [ -n "$last" ] || return 1
  printf '%s\n' "$last" | grep -qE "^❯[ ${nbsp}]*$"
}

# stash_ai_draft <tmux_cmd> <pane> <history_file> [project_dir] — extract the
# AI pane's unsent draft by making claude itself persist it: Esc Esc with a
# non-empty input appends the draft (full text, newlines, [Image #N]/[Pasted
# text #N] markers) to the shared prompt history. The lone Escape first
# interrupts a streaming turn (no-op when idle). History growth within the
# poll window is the "there WAS a draft" signal — an empty input appends
# nothing. The history is shared by EVERY live claude session, so a foreign
# session's entry can land in the same window: with project_dir given, only
# appended entries whose "project" matches count (never replay another
# session's prompt into this pane). Prints the stashed draft text; exit 0 iff
# stashed. Fail-open: any miss just means the switch behaves as before this
# feature.
stash_ai_draft() {
  local tmux_cmd="$1" pane="$2" hist="$3" project="${4:-}"
  command -v python3 >/dev/null 2>&1 || return 1
  # Fast path: an empty input box has nothing to stash — skip the Esc-Esc
  # dance and its several-second no-growth poll entirely. This is the common
  # case, so it is most of the mid-session switch's latency.
  if _ai_input_line_empty "$tmux_cmd" "$pane"; then
    return 1
  fi
  local before=0 after out
  [ -f "$hist" ] && before="$(wc -l < "$hist")"
  "$tmux_cmd" send-keys -t "$pane" Escape 2>/dev/null || return 1
  sleep 0.2
  "$tmux_cmd" send-keys -t "$pane" Escape Escape 2>/dev/null || return 1
  for _ in $(seq 1 15); do
    after=0
    [ -f "$hist" ] && after="$(wc -l < "$hist")"
    if [ "$after" -gt "$before" ]; then
      out="$(python3 - "$hist" "$before" "$project" <<'PYEOF'
import json, sys
path, skip, project = sys.argv[1], int(sys.argv[2]), sys.argv[3]
with open(path, "rb") as f:
    lines = [l for l in f.read().splitlines() if l.strip()][skip:]
for line in reversed(lines):
    try:
        entry = json.loads(line)
    except ValueError:
        continue
    if project and entry.get("project") != project:
        continue
    print(entry.get("display", ""), end="")
    break
PYEOF
)" || out=""
      if [ -n "$out" ]; then
        printf '%s' "$out"
        return 0
      fi
      # Growth was only foreign sessions' entries — keep waiting for ours.
    fi
    sleep 0.1
  done
  return 1
}

# draft_cache_root <accounts_dir> <session_acct> — config root of the account
# the pane WAS running before the switch: its image-cache/ holds the draft's
# pasted images (written at paste time). Empty acct = the Default (Keychain)
# login, whose root is ~/.claude. The NEW login only needs to READ this path,
# so no cache sharing across accounts is required.
draft_cache_root() {
  local accounts_dir="$1" acct="$2"
  if [ -n "$acct" ]; then
    printf '%s/%s\n' "$accounts_dir" "$acct"
  else
    printf '%s/.claude\n' "$HOME"
  fi
}

# wait_ai_pane_ready <tmux_cmd> <pane> [iters] — poll (iters × 0.5s, default
# ~30s) until the relaunched claude shows an EMPTY ready input line: "❯"
# alone on its line. Trust/login/update dialogs also render "❯"-prefixed rows
# but always with text after it, so this match keeps the replay's pastes away
# from dialogs. Timeout is fail-open: the draft stays one Up-press away in
# prompt history.
wait_ai_pane_ready() {
  local tmux_cmd="$1" pane="$2" iters="${3:-60}" nbsp
  # claude pads the empty prompt with a NO-BREAK space (U+00A0), not an ASCII
  # space — accept both (printf keeps this bash-3.2/zsh safe, no $'\u..').
  nbsp="$(printf '\302\240')"
  for _ in $(seq 1 "$iters"); do
    if "$tmux_cmd" capture-pane -p -t "$pane" 2>/dev/null | grep -qE "^❯[ ${nbsp}]*$"; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

# send_continue_message <tmux_cmd> <pane> — type "continue" into the pane's
# input and submit it. Used by the auto-switch relaunch so the conversation
# picks up in the new login without the user re-prompting. The literal text
# (-l) and the Enter are separate sends with a beat in between so claude
# registers the text before the submit. Best-effort.
send_continue_message() {
  local tmux_cmd="$1" pane="$2"
  "$tmux_cmd" send-keys -t "$pane" -l "continue" 2>/dev/null || return 0
  sleep 0.2
  "$tmux_cmd" send-keys -t "$pane" Enter 2>/dev/null || true
  return 0
}

# _draft_paste <tmux_cmd> <pane> <text> — bracketed-paste literal text into
# the pane via a named tmux buffer. Bracketed (-p) is load-bearing twice
# over: embedded newlines must not submit, and a pasted image PATH is only
# re-attached as a live image chip when it arrives as a paste (the same
# mechanism the screenshot drop uses). Best-effort: a failed paste degrades
# to "draft stays in history".
_draft_paste() {
  local tmux_cmd="$1" pane="$2" text="$3"
  printf '%s' "$text" | "$tmux_cmd" load-buffer -b wispdraft - 2>/dev/null || return 0
  "$tmux_cmd" paste-buffer -p -b wispdraft -t "$pane" 2>/dev/null || true
  return 0
}

# replay_ai_draft <tmux_cmd> <pane> <draft> <cache_root> <sid> — rebuild the
# input field from the stashed draft text. Split at [Image #N] markers; text
# segments paste verbatim, and each marker whose cached PNG exists
# (<cache_root>/image-cache/<sid>/<N>.png, written by the OLD claude at paste
# time) pastes as that absolute path — the new claude re-attaches it as a
# live image. Missing file, unstamped sid, or a malformed marker degrade to
# the literal marker text. [Pasted text #N] markers are never split on: their
# bytes died with the old process (memory-only), so they ride along inside
# text segments. Nothing here ever submits.
replay_ai_draft() {
  local tmux_cmd="$1" pane="$2" draft="$3" cache_root="$4" sid="$5"
  local rest="$draft" pre marker n img
  while [ -n "$rest" ]; do
    pre="${rest%%\[Image #*}"
    if [ "$pre" = "$rest" ]; then
      _draft_paste "$tmux_cmd" "$pane" "$rest"
      break
    fi
    [ -n "$pre" ] && _draft_paste "$tmux_cmd" "$pane" "$pre"
    rest="${rest#"$pre"}"
    if [ "${rest#*\]}" = "$rest" ]; then
      # No closing bracket: not a real marker — paste the remainder literally.
      _draft_paste "$tmux_cmd" "$pane" "$rest"
      break
    fi
    marker="${rest%%\]*}]"
    rest="${rest#*\]}"
    n="${marker#\[Image #}"
    n="${n%\]}"
    img=""
    case "$n" in
      *[!0-9]* | '') ;; # non-numeric: leave img empty -> literal
      *) [ -n "$sid" ] && img="$cache_root/image-cache/$sid/$n.png" ;;
    esac
    if [ -n "$img" ] && [ -f "$img" ]; then
      _draft_paste "$tmux_cmd" "$pane" "$img"
    else
      _draft_paste "$tmux_cmd" "$pane" "$marker"
    fi
    sleep 0.3 # let the image chip render before the next segment lands
  done
  return 0
}

# build_switch_launch_cmd <tool> <tool_cmd> <settings> <filter> \
#   <project_dir> <new_account_dir> [resume_session] [claude_handoff_arg]
#   [opencode_handoff_prompt] — build
# the launch command that respawns the AI pane under new_account_dir.
#
# resume_session is the id of THIS pane's active conversation (the statusline
# stamps it only once the transcript is durable — see current_ai_session). Its
# presence is the "the previous account HAD an active session" signal:
#   - non-empty: reopen THAT exact conversation via build_ai_launch_cmd's resume
#     chain (`--resume <id>` → `-c` → plain claude). The stamped id keeps a
#     multi-tab/window project's pane on ITS own conversation, which bare `-c`
#     could not guarantee.
#   - empty: the pane had NO active session, so do NOT resume — launching `-c`
#     would restore the cwd's most-recent conversation, one that was never this
#     pane's. Build a plain, fresh claude under the new login instead.
# new_account_dir empty = the Default (Keychain) login, so CLAUDE_CONFIG_DIR is
# left unset.
build_switch_launch_cmd() {
  local tool="$1" tool_cmd="$2" settings="$3" filter="$4" \
    project_dir="$5" new_account_dir="$6" resume_session="${7:-}" \
    claude_handoff_arg="${8:-}" opencode_handoff_prompt="${9:-}"
  if [ -n "$resume_session" ]; then
    WISP_DECK_RESUME=1 \
    WISP_DECK_RESUME_SESSION="$resume_session" \
    WISP_DECK_CLAUDE_ACCOUNT_DIR="$new_account_dir" \
    WISP_DECK_CLAUDE_SETTINGS="$settings" \
    WISP_DECK_CLAUDE_FILTER="$filter" \
    WISP_DECK_OPENCODE_HANDOFF_PROMPT="$opencode_handoff_prompt" \
      build_ai_launch_cmd "$tool" "$tool_cmd" "$project_dir"
    return 0
  fi
  # Fresh launch: no resume. claude takes no positional dir (its cwd is set by
  # respawn-pane's -c); opencode does, so only it gets project_dir as the extra.
  # WISP_DECK_RESUME/WISP_DECK_RESUME_SESSION are explicitly blanked: every
  # pane of a restored tab inherits the wrapper's launch-time exports, and
  # build_ai_launch_cmd reads them from the environment — without the override
  # a "fresh" switch would resume a stale conversation that was never this
  # pane's.
  local extra=""
  [ "$tool" = "opencode" ] && extra="$project_dir"
  [ "$tool" = "claude" ] && extra="$claude_handoff_arg"
  WISP_DECK_RESUME='' \
  WISP_DECK_RESUME_SESSION='' \
  WISP_DECK_CLAUDE_ACCOUNT_DIR="$new_account_dir" \
  WISP_DECK_CLAUDE_SETTINGS="$settings" \
  WISP_DECK_CLAUDE_FILTER="$filter" \
  WISP_DECK_OPENCODE_HANDOFF_PROMPT="$opencode_handoff_prompt" \
    build_ai_launch_cmd "$tool" "$tool_cmd" "$extra"
}

# _read_relaunch_ctx <relaunch_file> — load the key=value relaunch context into the
# caller's _rc_* locals (declared by the caller). IFS='=' keeps values verbatim,
# including any trailing space in the screenshot filter prefix.
_read_relaunch_ctx() {
  local file="$1" k v
  while IFS='=' read -r k v; do
    case "$k" in
      tool) _rc_tool="$v" ;;
      tool_cmd) _rc_tool_cmd="$v" ;;
      settings) _rc_settings="$v" ;;
      settings_source) _rc_settings_source="$v" ;;
      filter) _rc_filter="$v" ;;
      project_dir) _rc_project_dir="$v" ;;
      accounts_dir) _rc_accounts_dir="$v" ;;
      pointer) _rc_pointer="$v" ;;
      list) _rc_list="$v" ;;
      colors) _rc_colors="$v" ;;
      default_label) _rc_default_label="$v" ;;
      tools) _rc_tools="$v" ;;
      claude_cmd) _rc_claude_cmd="$v" ;;
      opencode_cmd) _rc_opencode_cmd="$v" ;;
      codex_cmd) _rc_codex_cmd="$v" ;;
      tool_pref) _rc_tool_pref="$v" ;;
      attention_root) _rc_attention_root="$v" ;;
      attention_descriptor) _rc_attention_descriptor="$v" ;;
      config_pointer) _rc_config_pointer="$v" ;;
      configs_dir) _rc_configs_dir="$v" ;;
      configs_list) _rc_configs_list="$v" ;;
    esac
  done < "$file"
}

# _set_relaunch_kv <file> <key> <value> [<key2> <value2>] — rewrite one
# or two key/value pairs in a single pass, appending missing keys and leaving
# every other line byte-for-byte. Backend switches update settings_source and
# settings together to avoid scanning and renaming the relaunch file twice.
_set_relaunch_kv() {
  local file="$1" key="$2" value="$3" key2="${4:-}" value2="${5:-}"
  local tmp found=0 found2=0 line k
  [ -f "$file" ] || return 1
  tmp="$(mktemp "${file}.XXXXXX")" || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    k="${line%%=*}"
    if [ "$k" = "$key" ]; then
      printf '%s=%s\n' "$key" "$value"
      found=1
    elif [ -n "$key2" ] && [ "$k" = "$key2" ]; then
      printf '%s=%s\n' "$key2" "$value2"
      found2=1
    else
      printf '%s\n' "$line"
    fi
  done < "$file" > "$tmp"
  [ "$found" = 1 ] || printf '%s=%s\n' "$key" "$value" >> "$tmp"
  if [ -n "$key2" ] && [ "$found2" != 1 ]; then
    printf '%s=%s\n' "$key2" "$value2" >> "$tmp"
  fi
  mv -f "$tmp" "$file"
}

# claude_config_context_budget <settings_path> — print the context window the
# config declares, or nothing. The value is validated as bare digits: it becomes
# an arithmetic operand below, and a hand-edited profile must not be able to
# smuggle anything else in.
claude_config_context_budget() {
  local settings_path="${1:-}" budget=""
  [ -f "$settings_path" ] || return 0
  command -v jq >/dev/null 2>&1 || return 0
  budget="$(jq -er '
    .env.CLAUDE_CODE_MAX_CONTEXT_TOKENS
    | select(type == "string")
  ' "$settings_path" 2>/dev/null)" || return 0
  case "$budget" in
    '' | *[!0-9]*) return 0 ;;
  esac
  printf '%s\n' "$budget"
}

# ai_transcript_context_tokens <transcript> — print the prompt size of the most
# recent recorded assistant turn: input + cache-read + cache-creation, which is
# exactly what a provider counts against its context window. Output tokens are
# excluded — they are not part of the next request's prompt.
#
# Zero totals are skipped, not reported. A failed turn is written as a
# "<synthetic>" assistant entry with all-zero usage and it lands AFTER the last
# real turn — precisely the tail a stranded session ends on — so taking the
# final entry blindly reports a 254k conversation as 0 and waves through the one
# switch this measurement exists to stop.
#
# Streamed rather than slurped: a long conversation's transcript runs to tens of
# megabytes.
ai_transcript_context_tokens() {
  local transcript="${1:-}"
  [ -f "$transcript" ] || return 0
  command -v jq >/dev/null 2>&1 || return 0
  jq -r '
    select(.message.usage.input_tokens != null)
    | .message.usage
    | (.input_tokens // 0) + (.cache_read_input_tokens // 0)
      + (.cache_creation_input_tokens // 0)
    | select(. > 0)
  ' "$transcript" 2>/dev/null | tail -1
}

# subscription_holds_conversation <settings_path> <transcript> — exit 0 unless
# the live conversation is measurably larger than the target subscription's
# context window.
#
# Retargeting a pane replays the WHOLE transcript to the new provider, and a
# transcript that overflows its cap cannot be recovered from afterwards:
# /compact must itself send the oversized transcript, so it fails with the same
# 400 as every other turn. The check therefore has to happen while the roomier
# backend is still live.
#
# Unknown is not the same as too big: an absent transcript, no jq, or a target
# that declares no window all allow the switch. Blocking on missing data would
# refuse routine switches, which is a worse trade than the overflow it guards.
subscription_holds_conversation() {
  local settings_path="${1:-}" transcript="${2:-}" budget tokens
  budget="$(claude_config_context_budget "$settings_path")"
  [ -n "$budget" ] || return 0
  tokens="$(ai_transcript_context_tokens "$transcript")"
  [ -n "$tokens" ] || return 0
  [ "$tokens" -le "$budget" ]
}

# _guard_subscription_context <tmux_cmd> <config_file> — exit 0 unless
# retargeting this pane at config_file would strand its conversation past that
# subscription's window. The refusal is announced on the tmux status line: a
# switch that silently does nothing reads as a bug, and the user needs to know
# /compact is the way out WHILE the roomier backend can still run it.
# Reads _rc_configs_dir/_rc_configs_list/_rc_project_dir from the caller's scope,
# the same dynamic-scoping contract _read_relaunch_ctx uses.
_guard_subscription_context() {
  local tmux_cmd="$1" config_file="$2" settings sid transcript name
  [ -n "${_rc_configs_dir:-}" ] || return 0
  settings="$_rc_configs_dir/$config_file"
  [ -f "$settings" ] || return 0
  sid="$(current_ai_session "$tmux_cmd")"
  [ -n "$sid" ] || return 0
  # Same rule as session-restore.sh's claude_project_dir, repeated rather than
  # sourced: that module pulls in terminals/ghostty.sh, which the switch has no
  # use for. TestClaudeProjectDirRule_matches_session_restore pins the two.
  transcript="$HOME/.claude/projects/${_rc_project_dir//[^A-Za-z0-9]/-}/$sid.jsonl"
  subscription_holds_conversation "$settings" "$transcript" && return 0
  name="$(claude_config_name "$config_file" "${_rc_configs_list:-}")"
  "$tmux_cmd" display-message \
    "Not switching: this conversation is bigger than $name can hold. Run /compact first." \
    2>/dev/null || true
  return 1
}

# _apply_subscription <tmux_cmd> <relaunch_file> <config_file|standard> — retarget
# the pane's Claude backend before the relaunch. Sets the global active-config
# pointer, rewrites the relaunch context's settings source to the chosen config
# (empty = standard, so the relaunch — which re-reads the file — rebuilds the
# overlay from it), exports WISP_DECK_CLAUDE_PROVIDER so build_ai_launch_cmd wraps
# the ChatGPT bridge when needed, and stamps the pane's per-session backend
# (WISP_DECK_CLAUDE_CONFIG / WISP_DECK_CLAUDE_PROVIDER) so the pill and the next
# switch read THIS pane's backend. Reads _rc_config_pointer/_rc_configs_dir from
# the caller's scope (the same dynamic-scoping contract _read_relaunch_ctx uses).
_apply_subscription() {
  local tmux_cmd="$1" relaunch_file="$2" config_file="$3"
  local stamp="$config_file" new_source new_provider
  [ "$stamp" = "standard" ] && stamp=""
  set_active_claude_config "$_rc_config_pointer" "$config_file"
  new_source="$(resolve_claude_config_path "$_rc_configs_dir" "$_rc_config_pointer")"
  new_provider="$(get_claude_config_provider "$new_source")"
  _set_relaunch_kv "$relaunch_file" settings_source "$new_source" settings "$new_source"
  export WISP_DECK_CLAUDE_PROVIDER="$new_provider"
  "$tmux_cmd" set-environment WISP_DECK_CLAUDE_CONFIG "$stamp" 2>/dev/null || true
  "$tmux_cmd" set-environment WISP_DECK_CLAUDE_PROVIDER "$new_provider" 2>/dev/null || true
}

# write_relaunch_context <out_file> <tool> <tool_cmd> <settings> \
#   <filter> <project_dir> <cfg_root> [tools] [claude_cmd] [opencode_cmd] \
#   [codex_cmd] [attention_root] [attention_descriptor]
#   [claude_settings_source] — persist everything
# the mid-session switch needs to rebuild
# the AI launch and locate the account files. wrapper.sh writes it once per
# launch (every tool) and passes its path to the pane as
# WISP_DECK_RELAUNCH_FILE. key=value, one per line — read back by
# _read_relaunch_ctx with IFS='=' so a value's spaces (the filter prefix)
# survive. tools is the space-separated available-tool list and the *_cmd
# trailers each tool's binary — what lets the switcher offer OTHER agents and
# relaunch the pane under one of them; tool_pref is where the launcher reads
# its default tool, so a switch can steer future launches too.
write_relaunch_context() {
  local out="$1" tool="$2" tool_cmd="$3" settings="$4" \
    filter="$5" project_dir="$6" cfg_root="$7" tools="${8:-}" \
    claude_cmd="${9:-}" opencode_cmd="${10:-}" codex_cmd="${11:-}" \
    attention_root="${12:-}" attention_descriptor="${13:-}" \
    claude_settings_source="${14:-}"
  case "$out" in */*) mkdir -p "${out%/*}" 2>/dev/null ;; esac
  # Published by rename: the ledger pane reads this file on its own schedule, and
  # an in-place rewrite lets it observe a truncated prefix — which parses into a
  # context with no accounts, i.e. a pane with no account pill.
  local tmp="$out.$$.tmp"
  {
    printf 'tool=%s\n' "$tool"
    printf 'tool_cmd=%s\n' "$tool_cmd"
    printf 'settings=%s\n' "$settings"
    printf 'settings_source=%s\n' "$claude_settings_source"
    printf 'filter=%s\n' "$filter"
    printf 'project_dir=%s\n' "$project_dir"
    printf 'accounts_dir=%s\n' "$cfg_root/claude-accounts"
    printf 'pointer=%s\n' "$cfg_root/claude-account"
    printf 'list=%s\n' "$cfg_root/claude-accounts.list"
    printf 'colors=%s\n' "$cfg_root/claude-account-colors"
    printf 'default_label=%s\n' "$cfg_root/claude-account-default-label"
    printf 'config_pointer=%s\n' "$cfg_root/claude-config"
    printf 'configs_dir=%s\n' "$cfg_root/claude-configs"
    printf 'configs_list=%s\n' "$cfg_root/claude-configs.list"
    printf 'tools=%s\n' "$tools"
    printf 'claude_cmd=%s\n' "$claude_cmd"
    printf 'opencode_cmd=%s\n' "$opencode_cmd"
    printf 'codex_cmd=%s\n' "$codex_cmd"
    printf 'tool_pref=%s\n' "$cfg_root/ai-tool"
    printf 'attention_root=%s\n' "$attention_root"
    printf 'attention_descriptor=%s\n' "$attention_descriptor"
  } > "$tmp" || { rm -f "$tmp" 2>/dev/null; return 1; }
  mv -f "$tmp" "$out" || { rm -f "$tmp" 2>/dev/null; return 1; }
}

# stage_claude_relaunch_settings <tool> <previous-generated-path>
#   <active-wisp-settings-source> <attention-root>
# Build the complete launch overlay before fencing the live generation. The
# staged file lives on the same private filesystem as the future generation, so
# publication after rotation is one rename and never rereads a source that may
# have been deleted or rewritten in the meantime. Print an empty line for tools
# and legacy contexts that do not use a semantic Claude overlay.
stage_claude_relaunch_settings() {
  local tool="$1" previous="${2:-}" source="${3:-}" root="${4:-}"
  local stage_dir stage_path
  if [ "$tool" != "claude" ] || [ -z "$root" ]; then
    printf '\n'
    return 0
  fi
  [ -d "$root" ] || return 1
  declare -f write_claude_launch_settings >/dev/null 2>&1 || return 1
  # A pre-migration live context may carry its durable selected config in the
  # old settings field. A deleted prior-generation path is never reused.
  if [ -z "$source" ] && [ -f "$previous" ]; then
    source="$previous"
  fi
  stage_dir="$(umask 077; mktemp -d "$root/generation.XXXXXX")" || return 1
  if ! stage_path="$(write_claude_launch_settings "$stage_dir" "$source" \
    "$HOME/.claude/settings.json")"; then
    rmdir "$stage_dir" 2>/dev/null || true
    return 1
  fi
  printf '%s\n' "$stage_path"
}

# discard_claude_relaunch_settings_stage <attention-root> <staged-file>
# Remove only the one-file staging directory created above.
discard_claude_relaunch_settings_stage() {
  local root="${1:-}" staged="${2:-}" stage_dir
  [ -n "$root" ] && [ -n "$staged" ] || return 0
  stage_dir="${staged%/*}"
  case "$staged" in
    "$root"/generation.*/claude-settings.json) ;;
    *) return 1 ;;
  esac
  rm -f "$staged" || return 1
  rmdir "$stage_dir" 2>/dev/null || true
}

# prepare_claude_relaunch_settings <tool> <previous-generated-path>
#   <active-wisp-settings-source> <staged-file>
# Publish the preflighted overlay into the generation that
# prepare_attention_relaunch just created. Older contexts without a semantic
# runtime keep their durable settings path.
prepare_claude_relaunch_settings() {
  local tool="$1" previous="${2:-}" source="${3:-}" staged="${4:-}"
  local target stage_dir
  if [ "$tool" != "claude" ] \
     || [ -z "${WISP_DECK_ATTENTION_FILE:-}" ]; then
    printf '%s\n' "$previous"
    return 0
  fi
  [ -f "$staged" ] || return 1
  stage_dir="${staged%/*}"
  target="${WISP_DECK_ATTENTION_FILE%/state}/claude-settings.json"
  if ! mv -f "$staged" "$target" || ! chmod 600 "$target"; then
    return 1
  fi
  rmdir "$stage_dir" 2>/dev/null || true
  printf '%s\n' "$target"
}

# prepare_attention_relaunch <tmux> <root> <descriptor> <tool>
# Rotate first, stamp the environment the next pane inherits, then let callers
# construct and respawn the command. Empty roots are legacy contexts and retain
# their pre-attention fail-open behavior.
prepare_attention_relaunch() {
  local tmux_cmd="$1" root="$2" descriptor="$3" tool="$4"

  WISP_DECK_TOOL="$tool"
  export WISP_DECK_TOOL
  if [ -z "$root" ]; then
    "$tmux_cmd" set-environment WISP_DECK_TOOL "$tool" 2>/dev/null || true
    return 0
  fi
  [ "$descriptor" = "$root/descriptor" ] || return 1
  declare -f attention_begin_generation >/dev/null 2>&1 || return 1
  attention_begin_generation "$root" "$tool" >/dev/null || return 1
  [ "$WISP_DECK_ATTENTION_DESCRIPTOR" = "$descriptor" ] || return 1

  "$tmux_cmd" set-environment WISP_DECK_TOOL "$tool" 2>/dev/null || return 1
  "$tmux_cmd" set-environment WISP_DECK_ATTENTION_ROOT \
    "$WISP_DECK_ATTENTION_ROOT" 2>/dev/null || return 1
  "$tmux_cmd" set-environment WISP_DECK_ATTENTION_DESCRIPTOR \
    "$WISP_DECK_ATTENTION_DESCRIPTOR" 2>/dev/null || return 1
  "$tmux_cmd" set-environment WISP_DECK_ATTENTION_GENERATION \
    "$WISP_DECK_ATTENTION_GENERATION" 2>/dev/null || return 1
  "$tmux_cmd" set-environment WISP_DECK_ATTENTION_FILE \
    "$WISP_DECK_ATTENTION_FILE" 2>/dev/null || return 1
  return 0
}

# relaunch_ai_pane <tmux_cmd> <relaunch_file> [chosen] — respawn the AI pane
# under a new login. With the optional 3rd arg, that is the login the switcher
# popup reported through its result file ("" or "default" = the Default login,
# a dir name = that managed login, resolved only while its dir still exists).
# Without it (legacy callers), the global pointer decides — but the pointer is
# shared mutable state: between the popup's write and this respawn another
# session's switch (or the launcher) can rewrite it, and resolving it here
# would respawn THIS pane under a login the user never picked. Resolves the
# account's isolated config dir, brings its shared conversation state/settings
# up to date (so a login not synced this boot still sees the shared history),
# builds the continue-mode launch, and respawn-panes the tagged AI pane. No-op
# when the AI pane can't be found.
relaunch_ai_pane() {
  local tmux_cmd="$1" relaunch_file="$2"
  local have_chosen=0 chosen=""
  if [ "$#" -ge 3 ]; then
    have_chosen=1
    chosen="$3"
  fi
  local _rc_tool="" _rc_tool_cmd="" _rc_settings="" _rc_settings_source="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" \
    _rc_tool_pref="" _rc_attention_root="" _rc_attention_descriptor=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  local new_dir pane cmd launch_settings staged_settings="" \
    background_mode=default attention_lock_held=0
  if [ "$have_chosen" = 1 ]; then
    new_dir=""
    if [ -n "$chosen" ] && [ "$chosen" != "default" ] && [ -d "$_rc_accounts_dir/$chosen" ]; then
      new_dir="$_rc_accounts_dir/$chosen"
    fi
  else
    new_dir="$(resolve_claude_account_dir "$_rc_accounts_dir" "$_rc_pointer")"
  fi

  # Bring the target login's shared conversation state + settings up to date, so
  # `claude -c` under it resumes the same history (mirrors wrapper.sh's per-account
  # sync at launch). Best-effort — helpers may be absent in a bare test source.
  if [ -n "$new_dir" ]; then
    background_mode=isolated
    command -v sync_claude_shared_state >/dev/null 2>&1 \
      && sync_claude_shared_state "$HOME/.claude" "$new_dir"
    command -v sync_claude_shared_settings >/dev/null 2>&1 \
      && sync_claude_shared_settings "$HOME/.claude" "$new_dir"
  fi

  pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$pane" ] || return 0
  if [ "$_rc_tool" = "opencode" ] && [ -n "$_rc_attention_root" ]; then
    opencode_adapter_prefix "$_rc_tool_cmd" >/dev/null || return 0
  fi
  if [ -n "$_rc_attention_root" ]; then
    declare -f attention_relaunch_lock_acquire >/dev/null 2>&1 || return 0
    attention_relaunch_lock_acquire "$_rc_attention_root" || return 0
    attention_lock_held=1
  fi

  # Resume the exact conversation this pane was on (the statusline stamped its id),
  # so the switch carries over THIS session rather than the cwd's most-recent one.
  # An empty sid means the previous account had no active session — the switch
  # then launches a fresh claude rather than resuming (see build_switch_launch_cmd).
  #
  # A caller in the enclosing scope can force a FRESH launch instead
  # (_gt_fresh_launch, the same dynamic-scoping contract _gt_send_continue
  # uses): a worktree switch moves the pane to a different checkout, and the
  # stamped conversation belongs to the tree being left.
  local sid
  sid="$(current_ai_session "$tmux_cmd")"
  [ -n "${_gt_fresh_launch:-}" ] && sid=""
  staged_settings="$(stage_claude_relaunch_settings "$_rc_tool" \
    "$_rc_settings" "$_rc_settings_source" "$_rc_attention_root")" || {
    [ "$attention_lock_held" = 1 ] \
      && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
    return 0
  }
  if ! prepare_attention_relaunch "$tmux_cmd" "$_rc_attention_root" \
    "$_rc_attention_descriptor" "$_rc_tool"; then
    discard_claude_relaunch_settings_stage "$_rc_attention_root" \
      "$staged_settings" 2>/dev/null || true
    [ "$attention_lock_held" = 1 ] \
      && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
    return 0
  fi
  launch_settings="$(prepare_claude_relaunch_settings "$_rc_tool" \
    "$_rc_settings" "$_rc_settings_source" "$staged_settings")" || {
    discard_claude_relaunch_settings_stage "$_rc_attention_root" \
      "$staged_settings" 2>/dev/null || true
    [ "$attention_lock_held" = 1 ] \
      && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
    return 0
  }
  if [ "$_rc_tool" = "claude" ] \
     && declare -f attention_start_claude_background_candidate >/dev/null 2>&1; then
    attention_start_claude_background_candidate \
      "$_rc_tool_cmd" "${new_dir:-$HOME/.claude}" \
      "${_rc_accounts_dir%/claude-accounts}" "$_rc_attention_root" \
      "$background_mode" \
      "${WISP_DECK_ERROR_LOG:-/dev/null}" 2>/dev/null || true
  fi
  cmd="$(build_switch_launch_cmd "$_rc_tool" "$_rc_tool_cmd" \
    "$launch_settings" "$_rc_filter" "$_rc_project_dir" "$new_dir" "$sid")"

  "$tmux_cmd" respawn-pane -k -t "$pane" -c "$_rc_project_dir" "$cmd; exec bash"

  # Stamp what this session's pane NOW runs into the tmux session env, so the
  # pill and the next switch decision read this pane's actual account rather
  # than the global pointer. Default stamps an EMPTY value (set, not unset) —
  # an unset var means "pre-stamp session" and falls back to the pointer.
  "$tmux_cmd" set-environment WISP_DECK_CLAUDE_ACCOUNT "${new_dir##*/}" 2>/dev/null
  [ "$attention_lock_held" = 1 ] \
    && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
  return 0
}

# _relaunch_preserving_draft <tmux_cmd> <relaunch_file> <session_acct> [chosen]
# — the "switch is happening" path shared by the result-file and legacy flows:
# stash the pane's unsent draft (claude only — opencode has no Esc-Esc
# stash), relaunch under the new login (the explicit popup choice when given,
# else the pointer — see relaunch_ai_pane), then hand the stashed text to a
# DISOWNED background waiter that replays it once the new claude shows its
# empty prompt. Reads _rc_tool/_rc_accounts_dir from the caller's scope (the
# same dynamic-scoping contract _read_relaunch_ctx uses). When the caller
# scope sets _gt_send_continue=1 (the auto-switch path), the waiter also
# submits a "continue" message the moment the new claude is ready — BEFORE
# the draft replay, so the continued turn streams while the unsent draft
# lands back in the input box. Every step is fail-open: a missed stash or a
# never-ready pane leaves the switch exactly as it behaved before this
# feature (worst case the draft sits in prompt history, one Up away).
_relaunch_preserving_draft() {
  local tmux_cmd="$1" relaunch_file="$2" session_acct="$3"
  local draft="" sid="" pane send_continue="${_gt_send_continue:-}"
  if [ "$_rc_tool" = "claude" ]; then
    sid="$(current_ai_session "$tmux_cmd")"
    pane="$(find_ai_pane "$tmux_cmd")"
    if [ -n "$pane" ]; then
      draft="$(stash_ai_draft "$tmux_cmd" "$pane" \
        "${WISP_DECK_HISTORY_FILE:-$HOME/.claude/history.jsonl}" \
        "$_rc_project_dir")" || draft=""
    fi
  fi
  if [ "$#" -ge 4 ]; then
    relaunch_ai_pane "$tmux_cmd" "$relaunch_file" "$4"
  else
    relaunch_ai_pane "$tmux_cmd" "$relaunch_file"
  fi
  [ -n "$draft" ] || [ -n "$send_continue" ] || return 0
  local cache_root new_pane
  cache_root="$(draft_cache_root "$_rc_accounts_dir" "$session_acct")"
  new_pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$new_pane" ] || return 0
  (
    if wait_ai_pane_ready "$tmux_cmd" "$new_pane"; then
      [ -n "$send_continue" ] && send_continue_message "$tmux_cmd" "$new_pane"
      [ -n "$draft" ] && replay_ai_draft "$tmux_cmd" "$new_pane" "$draft" "$cache_root" "$sid"
    fi
  ) >/dev/null 2>&1 &
  disown 2>/dev/null || true
  return 0
}

# _pool_tmux_env <tmux_cmd> <var> — print the tmux session env value of <var>,
# empty when unset (`-VAR` line) or unreadable.
_pool_tmux_env() {
  local line
  line="$("$1" show-environment "$2" 2>/dev/null)" || return 0
  case "$line" in
    "${2}="*) printf '%s\n' "${line#"${2}"=}" ;;
  esac
  return 0
}

_pool_codex_identity() {
  local tmux_cmd="$1" identity_file sid
  identity_file="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_CODEX_SESSION_FILE)"
  [ -f "$identity_file" ] && [ ! -L "$identity_file" ] || return 0
  sid="$(cat "$identity_file" 2>/dev/null || true)"
  _codex_session_id_valid "$sid" || return 0
  printf '%s\n' "$sid"
}

# _pool_capture_leaving_tool <tmux_cmd> <pool_dir> — record the LEAVING agent's
# session into the shared pool before the pane is respawned: stamp its native
# session id into the tmux env (what a later switch back resumes) and export
# its transcript tail as the agent-neutral handoff (what seeds a DIFFERENT
# agent). Reads _rc_tool/_rc_project_dir from the caller's scope (the same
# dynamic-scoping contract _read_relaunch_ctx uses). Fail-open: any miss just
# leaves the pool as it was.
#
# claude's special case: an empty current_ai_session WITH a durable stamp and
# a diverged live id means the user /new-closed the conversation — the pool's
# handoff still describes the closed one, so clear it rather than let the next
# agent resurrect it. An unstamped pane (no conversation yet) keeps the pool
# untouched: the pool's conversation is still whatever an earlier stint put
# there.
_pool_capture_leaving_tool() {
  local tmux_cmd="$1" pool="$2"
  [ -n "$pool" ] || return 0
  local meta="$pool/meta"
  case "$_rc_tool" in
    claude)
      local leave_sid durable live
      leave_sid="$(current_ai_session "$tmux_cmd")"
      if [ -n "$leave_sid" ]; then
        pool_set "$meta" claude "$leave_sid"
        if export_claude_handoff \
          "$(pool_claude_transcript "$_rc_project_dir" "$leave_sid")" \
          "$pool/handoff.md" 2>/dev/null; then
          pool_set "$meta" last_export_tool claude
        fi
      else
        durable="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_CLAUDE_SESSION)"
        live="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_CLAUDE_LIVE_SESSION)"
        if [ -n "$durable" ] && [ -n "$live" ] && [ "$live" != "$durable" ]; then
          rm -f "$pool/handoff.md"
          pool_set "$meta" last_export_tool ""
        fi
      fi
      ;;
    codex)
      local croot since csid
      croot="${WISP_DECK_CODEX_SESSIONS_DIR:-$HOME/.codex/sessions}"
      # Bound the capture to THIS pane's codex stint: the stint-start stamp
      # when a switch launched it, the tmux session's creation time when the
      # wrapper did (no stamp).
      since="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_CODEX_STARTED_AT)"
      [ -n "$since" ] \
        || since="$("$tmux_cmd" display-message -p '#{session_created}' 2>/dev/null)" \
        || since=""
      csid="$(_pool_codex_identity "$tmux_cmd")"
      [ -n "$csid" ] \
        || csid="$(codex_current_session "$croot" "$_rc_project_dir" "${since:-0}")"
      if [ -n "$csid" ]; then
        "$tmux_cmd" set-environment WISP_DECK_CODEX_SESSION "$csid" 2>/dev/null
        pool_set "$meta" codex "$csid"
        if export_codex_handoff "$(codex_rollout_for "$croot" "$csid")" \
          "$pool/handoff.md" 2>/dev/null; then
          pool_set "$meta" last_export_tool codex
        fi
      fi
      ;;
    opencode)
      "$tmux_cmd" set-environment WISP_DECK_OPENCODE_ACTIVE 1 2>/dev/null
      pool_set "$meta" opencode 1
      ;;
  esac
  return 0
}

# _tool_cmd_for <tool> — the tool's binary, from the relaunch context's *_cmd
# keys (caller scope, via _read_relaunch_ctx), falling back to PATH lookup for
# a context written before the keys existed. Empty when unresolvable.
_tool_cmd_for() {
  local tool="$1" cmd=""
  case "$tool" in
    claude) cmd="${_rc_claude_cmd:-}" ;;
    opencode) cmd="${_rc_opencode_cmd:-}" ;;
    codex) cmd="${_rc_codex_cmd:-}" ;;
  esac
  if [ -z "$cmd" ] && [ "$tool" = "${_rc_tool:-}" ]; then
    cmd="${_rc_tool_cmd:-}"
  fi
  # OpenCode is the one tool that can run without a binary on PATH (via npx), so
  # a plain `command -v` returns empty for exactly the users who most need the
  # fallback. It is also the tool wrapper.sh deliberately leaves unresolved in a
  # claude/codex context — resolving it costs an npx probe that the launch path
  # must not pay. Pay it here instead: a switch TO OpenCode is the moment the
  # answer is finally needed, and it happens once, on an explicit user action.
  if [ -z "$cmd" ] && [ "$tool" = "opencode" ] \
    && declare -f resolve_opencode_cmd >/dev/null 2>&1; then
    cmd="$(resolve_opencode_cmd)"
  fi
  [ -n "$cmd" ] || cmd="$(command -v "$tool" 2>/dev/null)" || cmd=""
  printf '%s\n' "$cmd"
}

# _tool_command_ready <tool> — validate a configured tool executable without
# requiring that tool to be selectable. ChatGPT subscriptions use Codex as a
# hidden bridge even when Codex is disabled as an agent.
_tool_command_ready() {
  local target="$1" tool_cmd=""
  case "$target" in
    claude|opencode|codex) ;;
    *) return 1 ;;
  esac

  tool_cmd="$(_tool_cmd_for "$target")"
  [ -n "$tool_cmd" ] || return 1
  case "$target:$tool_cmd" in
    opencode:npx\ --no-install\ opencode-ai|opencode:npx\ --prefer-offline\ opencode-ai@latest)
      command -v npx >/dev/null 2>&1
      ;;
    opencode:opencode)
      command -v opencode >/dev/null 2>&1
      ;;
    *:/*|*:*/*)
      [ -x "$tool_cmd" ]
      ;;
    *)
      command -v "$tool_cmd" >/dev/null 2>&1
      ;;
  esac
}

# _tool_choice_ready <tool> — revalidate an in-process choice against the latest
# relaunch context immediately before it can mutate or respawn the pane.
_tool_choice_ready() {
  local target="$1"
  case " ${_rc_tools:-} " in
    *" $target "*) ;;
    "  ") [ "$target" = "${_rc_tool:-}" ] || return 1 ;;
    *) return 1 ;;
  esac
  _tool_command_ready "$target"
}

# relaunch_switch_tool <tmux_cmd> <relaunch_file> <target_tool> [chosen_account]
# — respawn the AI pane under ANOTHER agent (the popup's "tool:<name>" result,
# or a claude login picked while a different agent runs — then target_tool is
# claude and chosen_account the login). Beyond the respawn it keeps every
# tool-identity source consistent: the tmux session env (WISP_DECK_TOOL — the
# pill and the next switch read it) and the relaunch context itself (the next
# switch must know what the pane NOW runs). The semantic watcher consumes that
# identity and remains the sole owner of title, sound, theme, and keep-awake
# policy. This function deliberately leaves the launcher's ai-tool preference
# alone — that file
# chooses the tool for future/other sessions, and a mid-session switch is
# session-scoped, so steering it would leak this choice everywhere. Leaving Claude first
# makes it persist any unsent draft into prompt history (Esc-Esc) — there is
# no replay into a different agent's input, so the draft stays one Up-press
# away for the user's next claude stint. Switching TO claude resumes the
# conversation an earlier claude stint stamped, under chosen_account (or the
# Default login). Fail-open everywhere.
relaunch_switch_tool() {
  local tmux_cmd="$1" relaunch_file="$2" target="$3" chosen_acct="${4:-}"
  local _rc_tool="" _rc_tool_cmd="" _rc_settings="" _rc_settings_source="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" \
    _rc_tool_pref="" _rc_attention_root="" _rc_attention_descriptor=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  local tool_cmd pane staged_settings="" attention_lock_held=0
  tool_cmd="$(_tool_cmd_for "$target")"
  [ -n "$tool_cmd" ] || return 0
  pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$pane" ] || return 0
  if [ "$target" = "opencode" ] && [ -n "$_rc_attention_root" ]; then
    opencode_adapter_prefix "$tool_cmd" >/dev/null || return 0
  fi
  if [ -n "$_rc_attention_root" ]; then
    declare -f attention_relaunch_lock_acquire >/dev/null 2>&1 || return 0
    attention_relaunch_lock_acquire "$_rc_attention_root" || return 0
    attention_lock_held=1
  fi

  staged_settings="$(stage_claude_relaunch_settings "$target" \
    "$_rc_settings" "$_rc_settings_source" "$_rc_attention_root")" || {
    [ "$attention_lock_held" = 1 ] \
      && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
    return 0
  }

  if [ "$_rc_tool" = "claude" ]; then
    stash_ai_draft "$tmux_cmd" "$pane" \
      "${WISP_DECK_HISTORY_FILE:-$HOME/.claude/history.jsonl}" \
      "$_rc_project_dir" >/dev/null 2>&1 || true
  fi

  # The shared session pool for this wisp session (lib/session-pool.sh; guarded
  # so a bare unit-test source without it still switches). Keyed by the
  # relaunch file's per-session suffix. Capture the LEAVING agent into it
  # before the respawn kills its process.
  local cfg_root="${_rc_accounts_dir%/claude-accounts}" pool="" pool_key
  pool_key="${relaunch_file##*/}"
  pool_key="${pool_key#relaunch-}"
  if command -v pool_dir >/dev/null 2>&1; then
    pool="$(pool_dir "$cfg_root" "$pool_key" 2>/dev/null)" || pool=""
  fi
  _pool_capture_leaving_tool "$tmux_cmd" "$pool"

  local new_dir="" sid="" background_mode=default
  if [ "$target" = "claude" ]; then
    if [ -n "$chosen_acct" ] && [ "$chosen_acct" != "default" ] \
       && [ -d "$_rc_accounts_dir/$chosen_acct" ]; then
      new_dir="$_rc_accounts_dir/$chosen_acct"
    fi
    if [ -n "$new_dir" ]; then
      background_mode=isolated
      command -v sync_claude_shared_state >/dev/null 2>&1 \
        && sync_claude_shared_state "$HOME/.claude" "$new_dir"
      command -v sync_claude_shared_settings >/dev/null 2>&1 \
        && sync_claude_shared_settings "$HOME/.claude" "$new_dir"
    fi
    # Resume the conversation an earlier claude stint of THIS pane stamped
    # (claude → codex → claude carries the session over); unstamped = fresh.
    sid="$(current_ai_session "$tmux_cmd")"
  elif [ "$target" = "codex" ]; then
    # Resume the codex session an earlier codex stint of this pane left behind
    # (stamped by _pool_capture_leaving_tool), and stamp the new stint's start
    # so the NEXT switch away can bound its capture.
    sid="$(_pool_codex_identity "$tmux_cmd")"
    [ -n "$sid" ] || sid="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_CODEX_SESSION)"
    _codex_session_id_valid "$sid" || sid=""
    "$tmux_cmd" set-environment WISP_DECK_CODEX_STARTED_AT "$(date +%s)" 2>/dev/null
  elif [ "$target" = "opencode" ]; then
    # A pane that ran opencode before continues its project-scoped session;
    # the marker value is only a flag — build_ai_launch_cmd's opencode resume
    # is `--continue`, no per-id resume.
    [ -n "$(_pool_tmux_env "$tmux_cmd" WISP_DECK_OPENCODE_ACTIVE)" ] && sid=1
  fi

  # Cross-agent handoff: the target has no session of its own, but the pool
  # holds another agent's exported conversation — seed the fresh launch with
  # an initial prompt pointing at it. Claude and Codex take it positionally;
  # OpenCode's adapter appends and submits it through the authenticated TUI API.
  local handoff_arg="" handoff_prompt_text=""
  if [ -z "$sid" ] && [ -n "$pool" ] && [ -f "$pool/handoff.md" ]; then
    local handoff_from
    handoff_from="$(pool_get "$pool/meta" last_export_tool)"
    if [ -n "$handoff_from" ] && command -v handoff_prompt >/dev/null 2>&1; then
      handoff_prompt_text="$(handoff_prompt "$pool/handoff.md" "$handoff_from")"
      handoff_arg=" $(printf '%q' "$handoff_prompt_text")"
    fi
  fi

  local cmd launch_settings builder_handoff="" opencode_handoff="" trailing_handoff="$handoff_arg"
  if ! prepare_attention_relaunch "$tmux_cmd" "$_rc_attention_root" \
    "$_rc_attention_descriptor" "$target"; then
    discard_claude_relaunch_settings_stage "$_rc_attention_root" \
      "$staged_settings" 2>/dev/null || true
    [ "$attention_lock_held" = 1 ] \
      && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
    return 0
  fi
  launch_settings="$(prepare_claude_relaunch_settings "$target" \
    "$_rc_settings" "$_rc_settings_source" "$staged_settings")" || {
    discard_claude_relaunch_settings_stage "$_rc_attention_root" \
      "$staged_settings" 2>/dev/null || true
    [ "$attention_lock_held" = 1 ] \
      && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
    return 0
  }
  if [ "$target" = "claude" ] \
     && declare -f attention_start_claude_background_candidate >/dev/null 2>&1; then
    attention_start_claude_background_candidate \
      "$tool_cmd" "${new_dir:-$HOME/.claude}" \
      "${_rc_accounts_dir%/claude-accounts}" "$_rc_attention_root" \
      "$background_mode" \
      "${WISP_DECK_ERROR_LOG:-/dev/null}" 2>/dev/null || true
  fi
  # The attention adapter wraps Claude's complete argv in `bash -c`. A
  # cross-agent prompt therefore has to be part of the raw inner command;
  # appending it to the built wrapper would turn it into bash's $0.
  if [ "$target" = "claude" ] \
     && [ -n "${WISP_DECK_ATTENTION_GENERATION:-}" ] \
     && [ -n "${WISP_DECK_ATTENTION_FILE:-}" ]; then
    builder_handoff="$handoff_arg"
    trailing_handoff=""
  fi
  if [ "$target" = "opencode" ] \
     && [ -n "${WISP_DECK_ATTENTION_GENERATION:-}" ] \
     && [ -n "${WISP_DECK_ATTENTION_FILE:-}" ]; then
    opencode_handoff="$handoff_prompt_text"
    trailing_handoff=""
  fi
  cmd="$(build_switch_launch_cmd "$target" "$tool_cmd" \
    "$launch_settings" "$_rc_filter" "$_rc_project_dir" "$new_dir" "$sid" \
    "$builder_handoff" "$opencode_handoff")"
  "$tmux_cmd" respawn-pane -k -t "$pane" -c "$_rc_project_dir" "$cmd$trailing_handoff; exec bash"

  [ -n "$pool" ] && pool_set "$pool/meta" last_tool "$target"
  if [ "$target" = "claude" ]; then
    "$tmux_cmd" set-environment WISP_DECK_CLAUDE_ACCOUNT "${new_dir##*/}" 2>/dev/null
  fi
  # NOTE: deliberately does NOT write the launcher's ai-tool preference. That
  # file steers which tool wrapper.sh picks for the NEXT launch and for OTHER
  # sessions (wrapper.sh reads it at splash and at SELECTED_AI_TOOL); a
  # mid-session switch is momentary and must not leak into future sessions.
  # This session's tool identity lives in WISP_DECK_TOOL, the pool meta, and
  # the relaunch context rewritten below — none of which need the global file.
  write_relaunch_context "$relaunch_file" "$target" "$tool_cmd" \
    "$_rc_settings" "$_rc_filter" "$_rc_project_dir" \
    "${_rc_accounts_dir%/claude-accounts}" "$_rc_tools" \
    "$_rc_claude_cmd" "$_rc_opencode_cmd" "$_rc_codex_cmd" \
    "$_rc_attention_root" "$_rc_attention_descriptor" \
    "$_rc_settings_source"
  [ "$attention_lock_held" = 1 ] \
    && attention_relaunch_lock_release "$_rc_attention_root" 2>/dev/null || true
  return 0
}

# auto_switch_relaunch <tmux_cmd> <relaunch_file> <target> — the automatic
# quota-rotation entry point (fired by auto_switch_maybe_trigger via `tmux
# run-shell -b`). Reuses the exact mid-session switch the ledger pill drives —
# stash the unsent draft, respawn the pane under the target login resuming the
# same conversation — and additionally auto-submits "continue" once the new
# claude is ready, so the interrupted turn picks up without the user typing.
# No-op when the pane already runs the target (a stale trigger racing a manual
# switch).
auto_switch_relaunch() {
  local tmux_cmd="$1" relaunch_file="$2" target="$3"
  local _rc_tool="" _rc_tool_cmd="" _rc_settings="" _rc_settings_source="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" \
    _rc_tool_pref="" _rc_attention_root="" _rc_attention_descriptor=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"
  local session_acct want
  session_acct="$(current_session_account "$tmux_cmd" "$_rc_pointer")"
  # The rotation names the Default login "default"; the session env stamps it
  # as the empty string — normalize before comparing so a stale trigger can't
  # bounce a pane already sitting on the Default.
  want="$target"
  [ "$want" = "default" ] && want=""
  [ "$want" = "$session_acct" ] && return 0
  local _gt_send_continue=1
  _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct" "$target"
  return 0
}

# _account_choice_ready <account-dir> - revalidate a preloaded managed-account
# row against both sources of truth. Deletion removes the list entry before the
# directory, so existence alone can accept an account already being removed.
# Empty/default is the virtual Keychain account and is always available.
_account_choice_ready() {
  local wanted="${1:-}" label="" dir=""
  [ -z "$wanted" ] || [ "$wanted" = "default" ] && return 0
  [ -d "$_rc_accounts_dir/$wanted" ] && [ -f "$_rc_list" ] || return 1
  while IFS=: read -r label dir; do
    case "$label" in
      ""|\#*) continue ;;
    esac
    [ "$dir" = "$wanted" ] && return 0
  done < "$_rc_list"
  return 1
}

# _subscription_choice_ready <config-file> - revalidate a preloaded chooser row
# immediately before mutation. The list/file/disabled checks catch concurrent
# config edits; ChatGPT additionally requires its Codex bridge executable, while
# API providers still require their stored Anthropic token.
_subscription_choice_ready() {
  local wanted="$1" name="" file="" selected_name="" config_path="" provider="" lower disabled line
  [ -n "$wanted" ] && [ -f "$_rc_configs_list" ] || return 1
  while IFS=: read -r name file; do
    case "$name" in
      ""|\#*) continue ;;
    esac
    if [ "$file" = "$wanted" ]; then
      selected_name="$name"
      break
    fi
  done < "$_rc_configs_list"
  [ -n "$selected_name" ] || return 1

  disabled="${_rc_configs_list%/*}/claude-configs.disabled"
  if [ -f "$disabled" ]; then
    while IFS= read -r line; do
      [ "$line" = "$wanted" ] && return 1
    done < "$disabled"
  fi

  config_path="$_rc_configs_dir/$wanted"
  [ -f "$config_path" ] || return 1
  provider="$(get_claude_config_provider "$config_path")"
  if [ -z "$provider" ]; then
    lower="$(printf '%s' "$selected_name" | tr '[:upper:]' '[:lower:]')"
    case "$lower" in
      *"openai gpt"*|*chatgpt*) provider=openai-chatgpt ;;
      *mimo*|*xiaomi*) provider=mimo ;;
      # Checked before the bare *kimi* case, matching the catalog's provider
      # order: the coding subscription and the open platform are separate
      # gateways that reject each other's keys.
      *"kimi for coding"*|*"kimi coding"*|*kimi-coding*) provider=moonshot-coding ;;
      *moonshot*|*kimi*) provider=moonshot ;;
      *) provider=zhipu ;;
    esac
  fi
  if [ "$provider" = "openai-chatgpt" ]; then
    _tool_command_ready codex
    return
  fi
  command -v jq >/dev/null 2>&1 || return 1
  jq -er '.env.ANTHROPIC_AUTH_TOKEN | select(type == "string" and length > 0)' "$config_path" >/dev/null 2>&1
}

# _retarget_session_context <tmux_cmd> <relaunch_file> <new_project_dir>
# Point this tab's durable identity at another checkout. Reads _rc_* from the
# caller's scope and retargets _rc_project_dir there (the same dynamic-scoping
# contract _read_relaunch_ctx uses). Runs FIRST in every retarget:
# relaunch_ai_pane re-reads this file for the pane's cwd, and it is also what
# every later switch, tab_view_new_window and gt_ensure_panes_watch read.
_retarget_session_context() {
  local tmux_cmd="$1" relaunch_file="$2" new_dir="$3"
  _set_relaunch_kv "$relaunch_file" project_dir "$new_dir" || return 1
  _rc_project_dir="$new_dir"

  # A crash-restore rebuilds the tab from the session env; without this it would
  # reopen the checkout the tab just left.
  "$tmux_cmd" set-environment WISP_DECK_PATH "$new_dir" 2>/dev/null || true
  return 0
}

# _retarget_session_side_panes <tmux_cmd> <relaunch_file> <new_project_dir>
# Move the ledger and the spare terminal to a checkout the context already
# points at. Every step is fail-open: a pane that cannot be found simply keeps
# its old cwd rather than aborting a retarget the caller has already made.
_retarget_session_side_panes() {
  local tmux_cmd="$1" relaunch_file="$2" new_dir="$3"
  local lib_dir session share_dir side_panes ledger="" spare="" accent
  local spare_label spare_conf spare_zdotdir project

  # Split by prefix, NOT `read -r ledger spare`: _session_side_panes leaves the
  # ledger field EMPTY when the session has no ledger pane, and read would
  # collapse the fields and hand the SPARE's id over as the ledger — respawning
  # the user's terminal as a changeset ledger.
  side_panes="$(_session_side_panes "$tmux_cmd")"
  ledger="${side_panes%% *}"
  spare="${side_panes#* }"
  lib_dir="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_LIB_DIR)"
  [ -n "$lib_dir" ] || return 0

  if [ -n "$ledger" ]; then
    "$tmux_cmd" respawn-pane -k -t "$ledger" -c "$new_dir" \
      "source \"$lib_dir/compact-view.sh\" && compact_view \"$new_dir\"; exec bash" \
      2>/dev/null || true
  fi

  [ -n "$spare" ] || return 0
  session="$("$tmux_cmd" display-message -p '#{session_name}' 2>/dev/null)" || session=""
  [ -n "$session" ] || return 0
  share_dir="${relaunch_file%/*}"
  # shellcheck source=/dev/null
  declare -f spare_tabs_socket >/dev/null 2>&1 || source "$lib_dir/spare-tabs.sh"
  spare_label="$(spare_tabs_socket "$session")"
  spare_conf="$share_dir/spare-${session}.conf"
  spare_zdotdir="$share_dir/spare-zdotdir-${session}"
  [ -d "$spare_zdotdir" ] || spare_zdotdir=""
  accent="$(get_tool_accent "${_rc_tool:-claude}" 2>/dev/null)" || accent=""
  project="$(_pool_tmux_env "$tmux_cmd" WISP_DECK_PROJECT)"
  # The inner spare server runs `exit-unattached on`, so respawning its pane
  # restarts it and it re-reads this config — its @gt_dir (the + button) and its
  # own prefix+t follow from the regenerated file rather than needing live
  # tmux calls against a server that is about to die.
  spare_tabs_config "$project" "$new_dir" "$lib_dir/spare-tabs.sh" \
    "$spare_label" "${accent:-209}" "$session" > "$spare_conf" 2>/dev/null || true
  "$tmux_cmd" respawn-pane -k -t "$spare" -c "$new_dir" \
    "$(spare_tabs_launch_cmd "$spare_label" "$spare_conf" "$new_dir" "$spare_zdotdir")" \
    2>/dev/null || true
  # The OUTER prefix+t opens a spare tab too, with the dir baked in at launch.
  "$tmux_cmd" bind-key t run-shell \
    "env -u TMUX -u TMUX_PANE tmux -L $spare_label new-window -c \"$new_dir\"" \
    2>/dev/null || true
  return 0
}

# _apply_worktree_switch <tmux_cmd> <relaunch_file> <new_project_dir>
# Rebuild this tab at another checkout of the same project — the user's own
# choice from the switcher, so the agent is rebuilt with it.
_apply_worktree_switch() {
  local tmux_cmd="$1" relaunch_file="$2" new_dir="$3"

  _retarget_session_context "$tmux_cmd" "$relaunch_file" "$new_dir" || return 1

  # The agent, through the established draft-preserving path so attention
  # fencing, the settings overlay and the unsent-draft replay all still apply —
  # but launched fresh, never resuming the old tree's conversation.
  local _gt_fresh_launch=1
  _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" ""

  _retarget_session_side_panes "$tmux_cmd" "$relaunch_file" "$new_dir"
}

# follow_agent_checkout <tmux_cmd> <relaunch_file> <new_project_dir>
# Move this tab to the checkout its own agent moved into — the automatic
# counterpart of the switcher's worktree row. The agent pane is deliberately
# NOT rebuilt: the conversation running in it is the one that just created the
# worktree, and a relaunch would throw it away as a side effect of the agent
# doing its job. Only the panes that merely display a checkout follow.
follow_agent_checkout() {
  local tmux_cmd="$1" relaunch_file="$2" new_dir="$3"
  local _rc_tool="" _rc_tool_cmd="" _rc_settings="" _rc_settings_source="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" \
    _rc_tool_pref="" _rc_attention_root="" _rc_attention_descriptor="" \
    _rc_config_pointer="" _rc_configs_dir="" _rc_configs_list=""
  [ -n "$new_dir" ] || return 1
  [ -f "$relaunch_file" ] || return 1
  _read_relaunch_ctx "$relaunch_file"

  # The agent can cd anywhere; only a checkout git itself reports for THIS
  # project may move the session.
  _worktree_choice_ready "$_rc_project_dir" "$new_dir" || return 1
  # Compared (and applied) in git's own terms — see _resolve_dir.
  new_dir="$(_resolve_dir "$new_dir")"
  [ "$new_dir" = "$(_resolve_dir "$_rc_project_dir")" ] && return 0

  _retarget_session_context "$tmux_cmd" "$relaunch_file" "$new_dir" || return 1
  _retarget_session_side_panes "$tmux_cmd" "$relaunch_file" "$new_dir"
}

# _apply_account_switch_choice_loaded <tmux_cmd> <relaunch_file>
#   <account|subscription|tool> <value> <session-account> <session-config>
# Dispatch one exact switch choice after the caller has loaded _rc_* and, when
# needed, resolved the pane's session-scoped identities. Shared by the native
# in-process chooser and the standalone compatibility popup.
_apply_account_switch_choice_loaded() {
  local tmux_cmd="$1" relaunch_file="$2" kind="$3" value="${4:-}"
  local session_acct="${5:-}" session_config="${6:-}"
  case "$kind" in
    tool)
      _tool_choice_ready "$value" || return 1
      [ "$value" = "$_rc_tool" ] \
        || relaunch_switch_tool "$tmux_cmd" "$relaunch_file" "$value"
      ;;
    subscription)
      [ -n "$value" ] || return 1
      _subscription_choice_ready "$value" || return 1
      if [ -n "$session_acct" ] && [ ! -d "$_rc_accounts_dir/$session_acct" ]; then
        return 1
      fi
      _tool_choice_ready claude || return 1
      if [ "$value" != "$session_config" ]; then
        _guard_subscription_context "$tmux_cmd" "$value" || return 1
        _apply_subscription "$tmux_cmd" "$relaunch_file" "$value"
        if [ "$_rc_tool" != "claude" ]; then
          relaunch_switch_tool "$tmux_cmd" "$relaunch_file" claude "$session_acct"
        else
          _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct" "$session_acct"
        fi
      fi
      ;;
    account)
      _account_choice_ready "$value" || return 1
      _tool_choice_ready claude || return 1
      set_active_claude_account "$_rc_pointer" "$value" || return 1
      if [ "$_rc_tool" != "claude" ]; then
        if [ -n "$session_config" ]; then
          _apply_subscription "$tmux_cmd" "$relaunch_file" standard
        fi
        relaunch_switch_tool "$tmux_cmd" "$relaunch_file" claude "$value"
      elif [ -n "$session_config" ]; then
        _apply_subscription "$tmux_cmd" "$relaunch_file" standard
        _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct" "$value"
      elif [ "$value" != "$session_acct" ]; then
        _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct" "$value"
      fi
      ;;
    worktree)
      [ -n "$value" ] || return 1
      _worktree_choice_ready "$_rc_project_dir" "$value" || return 1
      # Compared (and applied) in git's own terms — see _resolve_dir.
      value="$(_resolve_dir "$value")"
      # The checkout already running: rebuilding the tab would throw away a live
      # conversation for nothing.
      [ "$value" = "$(_resolve_dir "$_rc_project_dir")" ] && return 0
      _apply_worktree_switch "$tmux_cmd" "$relaunch_file" "$value" || return 1
      ;;
    *)
      return 1
      ;;
  esac
  return 0
}

# apply_account_switch_choice <tmux_cmd> <relaunch_file>
#   <account|subscription|tool> <value>
# Apply a choice already collected by an in-process caller. This is the native
# ledger's hot path: no popup, backdrop capture, TUI exec, or capability probe
# runs before the relaunch. Tool choices skip account/config tmux queries they
# cannot use. The existing draft/session/generation machinery remains the sole
# owner of the actual pane transition.
apply_account_switch_choice() {
  local tmux_cmd="$1" relaunch_file="$2" kind="$3" value="${4:-}"
  local _rc_tool="" _rc_tool_cmd="" _rc_settings="" _rc_settings_source="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" \
    _rc_tool_pref="" _rc_attention_root="" _rc_attention_descriptor="" \
    _rc_config_pointer="" _rc_configs_dir="" _rc_configs_list=""
  [ -f "$relaunch_file" ] || return 1
  _read_relaunch_ctx "$relaunch_file"

  local session_acct="" session_config=""
  case "$kind" in
    tool)
      ;;
    worktree)
      # The checkout switch keeps the pane's login and backend exactly as they
      # are, so it needs neither identity query.
      ;;
    subscription)
      _current_session_identities "$tmux_cmd" "$_rc_pointer" "$_rc_config_pointer"
      ;;
    account)
      _current_session_identities "$tmux_cmd" "$_rc_pointer" "$_rc_config_pointer"
      ;;
    *)
      return 1
      ;;
  esac
  _apply_account_switch_choice_loaded "$tmux_cmd" "$relaunch_file" "$kind" \
    "$value" "$session_acct" "$session_config"
}

# open_account_switcher <tmux_cmd> <relaunch_file> — the click handler entry point.
# Float the account switcher popup (which writes the global pointer on select),
# then relaunch the AI pane only if the choice differs from the account THIS
# pane is running. The popup reports the choice through a result file (its
# stdout is swallowed by display-popup); no file = cancelled. Comparing against
# the SESSION's account — not the pointer before/after — is what makes the
# switch stick: the pointer is global, so another session's switch (or the
# launcher) can already have flipped it to the login the user now picks here,
# and a pointer-diff would silently skip the relaunch, leaving this pane on the
# old login (the account appeared to "switch back").
open_account_switcher() {
  local tmux_cmd="$1" relaunch_file="$2"
  local _rc_tool="" _rc_tool_cmd="" _rc_settings="" _rc_settings_source="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_tools="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_codex_cmd="" \
    _rc_tool_pref="" _rc_attention_root="" _rc_attention_descriptor="" \
    _rc_config_pointer="" _rc_configs_dir="" _rc_configs_list=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  # The account this pane runs (stamped in the tmux session env), and the file
  # the popup reports the user's choice through. The result path is minted but
  # NOT created — its existence after the popup is the "user picked something"
  # signal. An older binary rejects the new flags (see
  # switcher_supports_session_flags); it gets the legacy pointer-diff flow.
  local session_acct="" session_config="" result_file session_flags=""
  _current_session_identities "$tmux_cmd" "$_rc_pointer" "$_rc_config_pointer"
  result_file=""
  if switcher_supports_session_flags; then
    result_file=$(mktemp "${TMPDIR:-/tmp}/gtswitchsel.XXXXXX" 2>/dev/null) || result_file=""
    [ -n "$result_file" ] && rm -f "$result_file"
    [ -n "$result_file" ] && session_flags="--active $(printf '%q' "$session_acct") \
--result-file $(printf '%q' "$result_file") "
  fi

  # Offer the other agents as rows too (the popup filters "claude" out — the
  # account rows ARE claude). Comma-joined for the flag; skipped for a legacy
  # binary or a context written before the tools key existed.
  if [ -n "$_rc_tools" ] && switcher_supports_agent_rows; then
    session_flags="${session_flags}--tools $(printf '%s' "$_rc_tools" | tr ' ' ',') \
--active-tool $(printf '%q' "$_rc_tool") "
  fi

  # Offer the configured subscription backends as rows under Claude (the popup
  # marks the pane's active one via --active-config). Only with a capable binary,
  # a live result-file channel, and at least one configured subscription.
  if [ -n "$result_file" ] && [ -s "$_rc_configs_list" ] \
     && switcher_supports_subscription_rows; then
    session_flags="${session_flags}--configs $(printf '%q' "$_rc_configs_list") \
--configs-dir $(printf '%q' "$_rc_configs_dir") \
--active-config $(printf '%q' "$session_config") "
  fi

  # Offer the project's other checkouts as rows (the popup marks the one this
  # pane runs via --active-worktree). The list is handed over as a FILE, like
  # the accounts and subscriptions lists, so a path never has to survive
  # quoting. A project with a single checkout has nothing to switch to, so the
  # flags are left off entirely and the popup renders no group.
  local worktree_file="" worktree_count=0
  if [ -n "$result_file" ] && switcher_supports_worktree_rows; then
    worktree_file=$(mktemp "${TMPDIR:-/tmp}/gtswitchwt.XXXXXX" 2>/dev/null) || worktree_file=""
    if [ -n "$worktree_file" ]; then
      _session_worktrees "$_rc_project_dir" > "$worktree_file" 2>/dev/null || true
      worktree_count=$(grep -c . "$worktree_file" 2>/dev/null) || worktree_count=0
      if [ "${worktree_count:-0}" -gt 1 ]; then
        # Resolved, so the marker matches a row git printed — see _resolve_dir.
        session_flags="${session_flags}--worktrees $(printf '%q' "$worktree_file") \
--active-worktree $(printf '%q' "$(_resolve_dir "$_rc_project_dir")") "
      else
        rm -f "$worktree_file"
        worktree_file=""
      fi
    fi
  fi

  # The switcher's full flag string, shared by the --measure probe and the popup
  # command so the measured card can never disagree with the card actually drawn.
  # Every value is %q-quoted (session_flags already is), so eval is safe.
  local switcher_flags
  switcher_flags="--list $(printf '%q' "$_rc_list") \
--accounts-dir $(printf '%q' "$_rc_accounts_dir") \
--pointer $(printf '%q' "$_rc_pointer") \
--colors $(printf '%q' "$_rc_colors") \
--default-label $(printf '%q' "$_rc_default_label") \
${session_flags}"

  # Tie the switcher to the LEDGER pane: the account pill lives in the ledger's
  # footer and is the only thing that opens this card, so the card is centered in
  # the ledger's rectangle and reads as that pill's own menu. Centering it in the
  # window (as this once did) floats it out in the agent's transcript, detached
  # from the affordance the user just clicked.
  #
  # Size the popup to the CARD itself via the popup binary's --measure seam, so
  # the switcher floats as a small card rather than a full-window overlay.
  # Fallbacks, in order: a ledger too small for the card, or no ledger pane at
  # all → the window's center (tmux clips a popup at the pane edge, it does not
  # shrink it, so a clipped card would be worse than a detached one); measure
  # unavailable or a card too big for the window → a full-window popup in
  # dimmed-backdrop mode, so the switcher still works.
  local win_width="" win_height="" status_val="" status_pos="" card_mode=0
  local -a popup_geom=(-w 100% -h 100%)
  read -r win_width win_height status_val status_pos < <(
    "$tmux_cmd" display-message -p \
      '#{window_width} #{window_height} #{status} #{status-position}' 2>/dev/null
  )

  # tmux measures a popup's -x/-y in CLIENT rows, but pane and window geometry is
  # measured from the WINDOW's top-left. A status line at the top offsets the two
  # by its height — a popup at -y 0 paints OVER the status line — so every row
  # coordinate below is shifted past it. Columns need no such correction: a
  # status line steals rows, not columns.
  local client_top=0
  if [ "$status_pos" = "top" ]; then
    case "$status_val" in
      on) client_top=1 ;;
      [1-9]) client_top="$status_val" ;;
    esac
  fi

  if [ -n "$win_width" ] && [ -n "$win_height" ]; then
    local card_size="" card_w="" card_h=""
    card_size="$(eval "wisp-deck-tui claude-account-switch --measure $switcher_flags" 2>/dev/null)" || card_size=""
    if [[ "$card_size" =~ ^[0-9]+\ [0-9]+$ ]]; then
      card_w="${card_size%% *}"
      card_h="${card_size##* }"
      # The rectangle the card is centered in: the ledger pane when it can hold
      # the card, else the whole window.
      local host_left=0 host_top=0 host_width="$win_width" host_height="$win_height"
      # Split by prefix rather than `read a b`: _session_side_panes prints
      # "<ledger> <spare>", and a session with no ledger prints a LEADING empty
      # field that `read` would collapse — silently handing the spare pane's id
      # over as the ledger and centering the card on the wrong pane.
      local side_panes="" ledger_pane="" led_left="" led_top="" led_width="" led_height=""
      side_panes="$(_session_side_panes "$tmux_cmd")"
      ledger_pane="${side_panes%% *}"
      if [ -n "$ledger_pane" ]; then
        read -r led_left led_top led_width led_height < <(
          "$tmux_cmd" display-message -p -t "$ledger_pane" \
            '#{pane_left} #{pane_top} #{pane_width} #{pane_height}' 2>/dev/null
        )
        if [ -n "$led_width" ] && [ -n "$led_height" ] \
           && [ "$card_w" -le "$led_width" ] && [ "$card_h" -le "$led_height" ]; then
          host_left="$led_left" host_top="$led_top"
          host_width="$led_width" host_height="$led_height"
        fi
      fi
      if [ "$card_w" -gt 0 ] && [ "$card_h" -gt 0 ] \
         && [ "$card_w" -le "$host_width" ] && [ "$card_h" -le "$host_height" ]; then
        # -x is the popup's LEFT edge but -y is its BOTTOM boundary (the man page
        # gives this away by defining position P as "the bottom left of the
        # pane"), so the wanted top row is handed over as top + height. Passing
        # the top row raw — as this did — hoists the card a full card-height above
        # where it belongs, and tmux then clamps it flat against the top of the
        # terminal rather than leaving the mistake visible as a near miss.
        popup_geom=(-x "$((host_left + (host_width - card_w) / 2))" \
                    -y "$((client_top + host_top + (host_height - card_h) / 2 + card_h))" \
                    -w "$card_w" -h "$card_h")
        card_mode=1
      fi
    fi
  fi

  # Snapshot the screen behind the popup so the switcher can show it DIMMED in the
  # margin around the card. tmux freezes the panes under a popup, so this snapshot
  # (taken just before opening it) matches what's behind. Serialized as a "W H"
  # header then one "PANE <left> <top>" + captured-lines + "ENDPANE" block per
  # pane; ParseBackdrop composites it. Best-effort: any failure yields a blank
  # margin. This mirrors the diff pager's backdrop (lib/compact-view.sh).
  # EVERY pane is captured: this popup covers the whole window, so a pane left
  # out would read as a blank hole rather than a dimmed session.
  # A card-sized popup has no margin around the card, so there is nothing to dim
  # — skip the snapshot entirely in card mode.
  local backdrop="" backdrop_arg=""
  [ "$card_mode" -eq 0 ] && { backdrop=$(mktemp "${TMPDIR:-/tmp}/gtswitch.XXXXXX" 2>/dev/null) || backdrop=""; }
  if [ -n "$backdrop" ]; then
    {
      "$tmux_cmd" display-message -p -t "${TMUX_PANE:-}" '#{client_width} #{client_height}'
      "$tmux_cmd" list-panes -t "${TMUX_PANE:-}" -F '#{pane_id} #{pane_left} #{pane_top}' 2>/dev/null |
        while read -r pid pleft ptop; do
          printf 'PANE %s %s\n' "$pleft" "$ptop"
          "$tmux_cmd" capture-pane -p -t "$pid" 2>/dev/null
          printf 'ENDPANE\n'
        done
    } >"$backdrop" 2>/dev/null
    backdrop_arg="--backdrop-file $(printf '%q' "$backdrop")"
  fi

  # Borderless (-B) so the switcher draws its own rounded card over the dimmed
  # snapshot and closes when a click lands outside the card (tmux ignores clicks
  # outside the popup). Geometry is the AI pane's rectangle when confined, else
  # the full window (-w/-h 100%).
  local before
  before="$(get_active_claude_account "$_rc_pointer")"
  "$tmux_cmd" display-popup -E -B "${popup_geom[@]}" \
    "wisp-deck-tui claude-account-switch ${switcher_flags}${backdrop_arg}" 2>/dev/null || true
  [ -n "$backdrop" ] && rm -f "$backdrop"
  [ -n "$worktree_file" ] && rm -f "$worktree_file"

  # No result file = the popup was cancelled (or never ran) — a clean no-op.
  # Otherwise relaunch iff the chosen login differs from what this pane runs.
  local chosen after
  if [ -n "$result_file" ]; then
    if [ -f "$result_file" ]; then
      IFS= read -r chosen < "$result_file" || chosen=""
      rm -f "$result_file"
      local kind=account value="$chosen"
      case "$chosen" in
        tool:*)
          kind=tool
          value="${chosen#tool:}"
          ;;
        config:*)
          kind=subscription
          value="${chosen#config:}"
          ;;
        worktree:*)
          kind=worktree
          value="${chosen#worktree:}"
          ;;
      esac
      if ! _apply_account_switch_choice_loaded "$tmux_cmd" "$relaunch_file" \
        "$kind" "$value" "$session_acct" "$session_config"; then
        [ "$kind" = "account" ] \
          && set_active_claude_account "$_rc_pointer" "$before" >/dev/null 2>&1 || true
        return 1
      fi
    fi
  else
    # Legacy binary (no result-file contract): fall back to the pointer-diff
    # decision — it can't fix a pointer that already matches the choice, but it
    # keeps the switcher working until the binary is updated.
    after="$(get_active_claude_account "$_rc_pointer")"
    [ "$before" != "$after" ] && _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct"
  fi
  return 0
}
