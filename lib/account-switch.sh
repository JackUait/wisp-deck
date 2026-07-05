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

# account_pill_enabled <relaunch_file> <list_file> — exit 0 when the ledger should
# show the switch pill. The tool/proxy eligibility gate lives in wrapper.sh, which
# only writes the relaunch-context file for a claude session with the rotation
# proxy OFF; so here it is enough that the relaunch file exists AND there are 2+
# accounts to switch between (a single managed login + the implicit Default).
account_pill_enabled() {
  local relaunch_file="$1" list_file="$2"
  [ -n "$relaunch_file" ] && [ -f "$relaunch_file" ] || return 1
  gt_multiple_claude_accounts "$list_file"
}

# account_pill <label> <color> — render the account pill for the ledger bottom bar.
# Line 1 is the drawable string (a leading space + the 󰀄 glyph + the label, all in
# the account's 256-color); line 2 is its VISIBLE click width so the click handler
# can bound the hit region. Width = leading space + glyph + space + label.
account_pill() {
  local label="$1" color="$2"
  printf ' \033[38;5;%sm\xF3\xB0\x80\x84 %s\033[0m\n' "$color" "$label"
  printf '%s\n' "$((3 + ${#label}))"
}

# account_current <pointer_file> <list_file> <default_label_file> <colors_file> —
# print "<label>\t<color>" for the active account, for account_pill. The active
# dir comes from the pointer (empty = Default); the label mirrors the statusline
# (gt_claude_account_label reads the dir off the tail of its first arg, so the bare
# dir name works), and the color is the persisted per-account hue.
account_current() {
  local pointer_file="$1" list_file="$2" default_label_file="$3" colors_file="$4"
  local dir label color
  dir="$(get_active_claude_account "$pointer_file")"
  label="$(gt_claude_account_label "$dir" "$list_file" "$default_label_file")"
  color="$(gt_account_color "$colors_file" "$dir")"
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

# current_ai_session <tmux_cmd> — print the resume-validated conversation id the
# statusline stamped into the session env (gt_stamp_claude_session sets
# WISP_DECK_CLAUDE_SESSION only once the transcript is durable). tmux prints
# `NAME=value` when set, or `-NAME` (leading dash) when unset — only the former
# yields an id. Empty when unstamped, so the relaunch falls back to `-c`.
current_ai_session() {
  local tmux_cmd="$1" line
  line="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_SESSION 2>/dev/null)" || return 0
  case "$line" in
    WISP_DECK_CLAUDE_SESSION=*) printf '%s\n' "${line#WISP_DECK_CLAUDE_SESSION=}" ;;
  esac
  return 0
}

# build_switch_launch_cmd <tool> <claude_cmd> <opencode_cmd> <settings> <filter> \
#   <project_dir> <new_account_dir> [resume_session] — build the launch command that
# respawns the AI pane under new_account_dir. Reuses build_ai_launch_cmd in resume
# mode: with resume_session it re-opens THAT exact conversation (`--resume <id>`,
# falling back to `-c` then plain `claude`); without it, `-c` (most-recent-in-cwd).
# Passing the stamped id keeps a multi-tab/window project's pane on ITS own
# conversation, which bare `-c` could not guarantee. new_account_dir empty = the
# Default (Keychain) login, so CLAUDE_CONFIG_DIR is left unset.
build_switch_launch_cmd() {
  local tool="$1" claude_cmd="$2" opencode_cmd="$3" settings="$4" filter="$5" \
    project_dir="$6" new_account_dir="$7" resume_session="${8:-}"
  WISP_DECK_RESUME=1 \
  WISP_DECK_RESUME_SESSION="$resume_session" \
  WISP_DECK_CLAUDE_ACCOUNT_DIR="$new_account_dir" \
  WISP_DECK_CLAUDE_SETTINGS="$settings" \
  WISP_DECK_CLAUDE_FILTER="$filter" \
    build_ai_launch_cmd "$tool" "$claude_cmd" "$opencode_cmd" "$project_dir"
}

# _read_relaunch_ctx <relaunch_file> — load the key=value relaunch context into the
# caller's _rc_* locals (declared by the caller). IFS='=' keeps values verbatim,
# including any trailing space in the screenshot filter prefix.
_read_relaunch_ctx() {
  local file="$1" k v
  while IFS='=' read -r k v; do
    case "$k" in
      tool) _rc_tool="$v" ;;
      claude_cmd) _rc_claude_cmd="$v" ;;
      opencode_cmd) _rc_opencode_cmd="$v" ;;
      settings) _rc_settings="$v" ;;
      filter) _rc_filter="$v" ;;
      project_dir) _rc_project_dir="$v" ;;
      accounts_dir) _rc_accounts_dir="$v" ;;
      pointer) _rc_pointer="$v" ;;
      list) _rc_list="$v" ;;
      colors) _rc_colors="$v" ;;
      default_label) _rc_default_label="$v" ;;
    esac
  done < "$file"
}

# write_relaunch_context <out_file> <tool> <claude_cmd> <opencode_cmd> <settings> \
#   <filter> <project_dir> <cfg_root> — persist everything the mid-session switch
# needs to rebuild the AI launch and locate the account files. wrapper.sh writes it
# once per launch (for an eligible claude session) and passes its path to the pane
# as WISP_DECK_RELAUNCH_FILE. key=value, one per line — read back by
# _read_relaunch_ctx with IFS='=' so a value's spaces (the filter prefix) survive.
write_relaunch_context() {
  local out="$1" tool="$2" claude_cmd="$3" opencode_cmd="$4" settings="$5" \
    filter="$6" project_dir="$7" cfg_root="$8"
  mkdir -p "$(dirname "$out")" 2>/dev/null
  {
    printf 'tool=%s\n' "$tool"
    printf 'claude_cmd=%s\n' "$claude_cmd"
    printf 'opencode_cmd=%s\n' "$opencode_cmd"
    printf 'settings=%s\n' "$settings"
    printf 'filter=%s\n' "$filter"
    printf 'project_dir=%s\n' "$project_dir"
    printf 'accounts_dir=%s\n' "$cfg_root/claude-accounts"
    printf 'pointer=%s\n' "$cfg_root/claude-account"
    printf 'list=%s\n' "$cfg_root/claude-accounts.list"
    printf 'colors=%s\n' "$cfg_root/claude-account-colors"
    printf 'default_label=%s\n' "$cfg_root/claude-account-default-label"
  } > "$out"
}

# relaunch_ai_pane <tmux_cmd> <relaunch_file> — respawn the AI pane under whatever
# account the pointer now names (the switcher popup wrote it just before). Resolves
# the account's isolated config dir, brings its shared conversation state/settings
# up to date (so a login not synced this boot still sees the shared history), builds
# the continue-mode launch, and respawn-panes the tagged AI pane. No-op when the AI
# pane can't be found.
relaunch_ai_pane() {
  local tmux_cmd="$1" relaunch_file="$2"
  local _rc_tool="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_settings="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  local new_dir pane cmd
  new_dir="$(resolve_claude_account_dir "$_rc_accounts_dir" "$_rc_pointer")"

  # Bring the target login's shared conversation state + settings up to date, so
  # `claude -c` under it resumes the same history (mirrors wrapper.sh's per-account
  # sync at launch). Best-effort — helpers may be absent in a bare test source.
  if [ -n "$new_dir" ]; then
    command -v sync_claude_shared_state >/dev/null 2>&1 \
      && sync_claude_shared_state "$HOME/.claude" "$new_dir"
    command -v sync_claude_shared_settings >/dev/null 2>&1 \
      && sync_claude_shared_settings "$HOME/.claude" "$new_dir"
  fi

  pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$pane" ] || return 0

  # Resume the exact conversation this pane was on (the statusline stamped its id),
  # so the switch carries over THIS session rather than the cwd's most-recent one.
  local sid
  sid="$(current_ai_session "$tmux_cmd")"
  cmd="$(build_switch_launch_cmd "$_rc_tool" "$_rc_claude_cmd" "$_rc_opencode_cmd" \
    "$_rc_settings" "$_rc_filter" "$_rc_project_dir" "$new_dir" "$sid")"

  "$tmux_cmd" respawn-pane -k -t "$pane" -c "$_rc_project_dir" "$cmd; exec bash"
}

# open_account_switcher <tmux_cmd> <relaunch_file> — the click handler entry point.
# Float the account switcher popup (which writes the pointer on select), then
# relaunch the AI pane only if the active account actually changed. Reading the
# pointer before/after is the source of truth, so a cancelled popup is a clean
# no-op without parsing the popup's stdout.
open_account_switcher() {
  local tmux_cmd="$1" relaunch_file="$2"
  local _rc_tool="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_settings="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  local before after
  before="$(get_active_claude_account "$_rc_pointer")"
  "$tmux_cmd" display-popup -E -w 44 -h 14 \
    "wisp-deck-tui claude-account-switch --list $(printf '%q' "$_rc_list") \
--accounts-dir $(printf '%q' "$_rc_accounts_dir") \
--pointer $(printf '%q' "$_rc_pointer") \
--colors $(printf '%q' "$_rc_colors") \
--default-label $(printf '%q' "$_rc_default_label")" 2>/dev/null || true
  after="$(get_active_claude_account "$_rc_pointer")"

  [ "$before" != "$after" ] && relaunch_ai_pane "$tmux_cmd" "$relaunch_file"
  return 0
}
