#!/bin/bash
# shellcheck source=../lib/statusline.sh
source "$(dirname "$0")/../lib/statusline.sh" 2>/dev/null \
  || source ~/.claude/statusline-helpers.sh 2>/dev/null \
  || true

input=$(cat)
# Record this pane's conversation id in the tmux session env so a session
# restored after a reboot reopens this exact conversation.
type gt_stamp_claude_session &>/dev/null && gt_stamp_claude_session "$input"
# Also record claude's LIVE (current) conversation id, ungated by durability, so
# a mid-session account switch after /new does not resume the just-closed one.
type gt_stamp_claude_live_session &>/dev/null && gt_stamp_claude_live_session "$input"
git_info=$(echo "$input" | bash ~/.claude/statusline-command.sh)
context_pct=$(echo "$input" | npx ccstatusline 2>/dev/null)
model_name=$(echo "$input" | sed -n 's/.*"display_name":"\([^"]*\)".*/\1/p')
# Reasoning effort (low/medium/high/xhigh/max). Conditionally present in the
# statusline JSON — only when the current model supports the effort parameter —
# so it is appended in brackets after the model name only when set.
effort_level=$(echo "$input" | sed -n 's/.*"effort":{"level":"\([^"]*\)".*/\1/p')
if [ -n "$effort_level" ]; then
  model_name="$model_name [$effort_level]"
fi

# Native Claude account label for this tab. wrapper.sh launches `claude` with the
# active account's isolated CLAUDE_CONFIG_DIR exported (unset for the Keychain
# Default), which this statusline process inherits. Shown only when the user has
# 2+ accounts connected (the accounts list holds at least two entries), so users
# with a single login get no extra clutter.
account_label=""
account_color=""
_gt_accounts_root="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
accounts_list="$_gt_accounts_root/claude-accounts.list"
# The Default login can be renamed via the account menu; its custom label lives
# here, so the statusline shows the renamed value instead of "Default".
default_label_file="$_gt_accounts_root/claude-account-default-label"
if type gt_claude_account_label &>/dev/null \
   && type gt_multiple_claude_accounts &>/dev/null \
   && gt_multiple_claude_accounts "$accounts_list"; then
  # When the rotation proxy is active it swaps accounts per-request while claude
  # keeps its single (Default) config dir, so the proxy's current account (from
  # WISP_DECK_PROXY_ACCOUNT_FILE) takes precedence over CLAUDE_CONFIG_DIR.
  account_dir="${CLAUDE_CONFIG_DIR:-}"
  if type gt_proxy_active_dir &>/dev/null; then
    proxy_dir=$(gt_proxy_active_dir "${WISP_DECK_PROXY_ACCOUNT_FILE:-}")
    [ -n "$proxy_dir" ] && account_dir="$proxy_dir"
  fi
  account_label=$(gt_claude_account_label "$account_dir" "$accounts_list" "$default_label_file")
  # Each account wears its own persistent color (assigned once, shared with the
  # TUI menu via the colors file). Key by the stable dir basename; the implicit
  # Default login has no config dir, so it keys under "default".
  if type gt_account_color &>/dev/null; then
    account_key="${account_dir##*/}"
    [ -z "$account_key" ] && account_key="default"
    account_color=$(gt_account_color "$_gt_accounts_root/claude-account-colors" "$account_key" \
      "$_gt_accounts_root/claude-config-colors")
  fi
fi
# When this pane runs a subscription backend (WISP_DECK_CLAUDE_CONFIG names
# the config file; stamped empty means standard Claude), the usage bars wear
# the SUBSCRIPTION's persistent color instead — the account is overridden
# while the backend runs, so the account color would tie the usage to the
# wrong identity. Shared with the ledger pill and the switcher rows via the
# claude-config-colors file; the account colors are the avoid set. Sits
# OUTSIDE the 2+ logins guard above: a subscription pane shows its usage even
# on a single-login setup, and it needs its color to do so.
if [ -n "${WISP_DECK_CLAUDE_CONFIG:-}" ] && type gt_config_color &>/dev/null; then
  config_color=$(gt_config_color "$_gt_accounts_root/claude-config-colors" \
    "$WISP_DECK_CLAUDE_CONFIG" "$_gt_accounts_root/claude-account-colors")
  [ -n "$config_color" ] && account_color="$config_color"
fi

# Usage pills for the active login: the 7-day (weekly) and 5-hour (rolling
# session) windows, each a bar painted in the account's own profile color (the
# fill vs. empty cells carry the amount; the color ties it to the login). Only
# present for subscribers after the first API response, so they stay empty
# otherwise, and only while the account segment is eligible (2+ logins).
weekly_bar=""
five_hour_bar=""
if [ -n "$account_label" ]; then
  if type gt_weekly_used_pct &>/dev/null; then
    weekly_pct=$(gt_weekly_used_pct "$input")
    [ -n "$weekly_pct" ] && weekly_bar=$(gt_usage_bar "$weekly_pct")
  fi
  if type gt_five_hour_used_pct &>/dev/null; then
    five_hour_pct=$(gt_five_hour_used_pct "$input")
    [ -n "$five_hour_pct" ] && five_hour_bar=$(gt_usage_bar "$five_hour_pct")
  fi
fi

# Auto-switch: when this session's quota usage reaches the threshold (98%),
# rotate the pane to the next pooled account in place and auto-continue the
# conversation there. The trigger lives in lib/auto-switch.sh, sourced from the
# live lib dir wrapper.sh stamped into the pane env (absent outside a Wisp Deck
# session — then this whole block is a no-op).
if [ -n "${WISP_DECK_LIB_DIR:-}" ] && [ -f "${WISP_DECK_LIB_DIR}/auto-switch.sh" ]; then
  # shellcheck source=../lib/auto-switch.sh
  source "${WISP_DECK_LIB_DIR}/auto-switch.sh" 2>/dev/null || true
  type auto_switch_maybe_trigger &>/dev/null \
    && auto_switch_maybe_trigger "${five_hour_pct:-}" "${weekly_pct:-}"
fi

# Subscription panes show the SUBSCRIPTION's real 5h/7d usage, never the
# login's: claude's own rate_limits describe the Anthropic account, which is
# overridden while a backend runs. The figures come from a disk cache
# maintained by `wisp-deck-tui subscription-usage` (each provider's real quota
# API); this render path only READS the cache — the refresher is spawned
# disowned with both fds dropped (they are claude's UI) and throttles itself,
# so spawning it every render is safe. A stale or missing snapshot leaves the
# pcts empty and the bars show their "…" placeholder rather than stale or
# native figures. Ordering matters: this sits AFTER the auto-switch trigger,
# which must keep reading the NATIVE pcts (it rotates Claude logins;
# subscription quota must never bounce the account).
if [ -n "${WISP_DECK_CLAUDE_CONFIG:-}" ]; then
  _gt_sub_cache="$_gt_accounts_root/subscription-usage/$WISP_DECK_CLAUDE_CONFIG"
  _gt_tui_bin="$(command -v wisp-deck-tui 2>/dev/null || true)"
  [ -z "$_gt_tui_bin" ] && [ -x "$HOME/.local/bin/wisp-deck-tui" ] \
    && _gt_tui_bin="$HOME/.local/bin/wisp-deck-tui"
  if [ -n "$_gt_tui_bin" ]; then
    "$_gt_tui_bin" subscription-usage \
      --configs-dir "$_gt_accounts_root/claude-configs" \
      --list "$_gt_accounts_root/claude-configs.list" \
      --config "$WISP_DECK_CLAUDE_CONFIG" \
      --cache "$_gt_sub_cache" >/dev/null 2>&1 &
    disown 2>/dev/null || true
  fi
  weekly_pct=""; five_hour_pct=""; weekly_bar=""; five_hour_bar=""
  _gt_sub_json=""
  [ -f "$_gt_sub_cache" ] && _gt_sub_json="$(cat "$_gt_sub_cache" 2>/dev/null)"
  if [ -n "$_gt_sub_json" ] && type gt_sub_usage_fresh &>/dev/null \
     && gt_sub_usage_fresh "$_gt_sub_json" "$(date +%s)"; then
    if type gt_weekly_used_pct &>/dev/null; then
      weekly_pct=$(gt_weekly_used_pct "$_gt_sub_json")
      [ -n "$weekly_pct" ] && weekly_bar=$(gt_usage_bar "$weekly_pct")
    fi
    if type gt_five_hour_used_pct &>/dev/null; then
      five_hour_pct=$(gt_five_hour_used_pct "$_gt_sub_json")
      [ -n "$five_hour_pct" ] && five_hour_bar=$(gt_usage_bar "$five_hour_pct")
    fi
  fi
fi

# Which usage pills to show is a user preference (Settings › Usage bars), stored
# as usage_bars=7d|5h|both|none in the wisp-deck settings file. Default is 7d
# (what the statusline showed before the 5h bar existed). Derive per-window show
# flags from it.
usage_bars_mode=$(sed -n 's/^usage_bars=//p' \
  "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" 2>/dev/null \
  | tr -d '[:space:]' | tail -n 1)
[ -n "$usage_bars_mode" ] || usage_bars_mode="7d"
show_7d=0
show_5h=0
case "$usage_bars_mode" in
  7d) show_7d=1 ;;
  5h) show_5h=1 ;;
  both) show_7d=1; show_5h=1 ;;
  none) ;;
  *) show_7d=1 ;;   # unknown value -> safe default (7d)
esac

# Find parent Claude Code process and get total tree memory + CPU usage
pid=$PPID
mem_label=""
cpu_label=""
while [ -n "$pid" ] && [ "$pid" != "1" ]; do
  comm=$(ps -o comm= -p "$pid" 2>/dev/null)
  # Recognize the Claude Code process even when comm is the resolved versioned
  # path (~/.local/share/claude/versions/X) instead of the `claude` symlink, so
  # the memory load renders at all times. ${comm##*/} is a space-safe basename.
  is_claude=""
  [ "${comm##*/}" = "claude" ] && is_claude=1
  case "$comm" in */claude/*) is_claude=1 ;; esac
  if [ -n "$is_claude" ]; then
    # Prefer phys_footprint (Activity Monitor's "Memory") — RSS overcounts shared
    # pages 2-4x, so it is the wrong memory load. Fall back to RSS if `footprint`
    # is unavailable, so the panel still shows a value.
    mem_kb=""
    if type get_tree_footprint_kb &>/dev/null; then
      mem_kb=$(get_tree_footprint_kb "$pid")
    fi
    if [ -z "$mem_kb" ]; then
      if type get_tree_rss_kb &>/dev/null; then
        mem_kb=$(get_tree_rss_kb "$pid")
      else
        mem_kb=$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ')
      fi
    fi
    if [ -n "$mem_kb" ] && [ "$mem_kb" -gt 0 ] 2>/dev/null; then
      mem_mb=$((mem_kb / 1024))
      if [ "$mem_mb" -ge 1024 ]; then
        mem_gb=$(echo "scale=1; $mem_mb / 1024" | bc)
        mem_label="${mem_gb}G"
      else
        mem_label="${mem_mb}M"
      fi
    fi
    # Real CPU load of this session's process tree (Activity Monitor's %CPU).
    cpu_pct=""
    if type get_tree_cpu_pct &>/dev/null; then
      cpu_pct=$(get_tree_cpu_pct "$pid")
    else
      cpu_pct=$(ps -o %cpu= -p "$pid" 2>/dev/null | tr -d ' ' \
        | LC_ALL=C awk 'NF { gsub(/,/, "."); printf "%d\n", $0 + 0.5 }')
    fi
    if [ -n "$cpu_pct" ]; then
      cpu_label="${cpu_pct}%"
    fi
    break
  fi
  pid=$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')
done

# Nerd Font glyphs prefix each metric so the three numbers — context %, memory,
# and CPU — are distinguishable at a glance (two of them are bare percentages).
# Literal UTF-8 is embedded directly: the wrapper runs under macOS bash 3.2
# (--posix), whose printf has no \u/\U escape support.
line=$(printf '%s | \033[01;33m󰧑\033[00m %s' "$git_info" "$context_pct")
if [ -n "$mem_label" ]; then
  line="$line$(printf ' | \033[01;35m󰆼 %s\033[00m' "$mem_label")"
fi
if [ -n "$cpu_label" ]; then
  line="$line$(printf ' | \033[01;33m %s\033[00m' "$cpu_label")"
fi
if [ -n "$model_name" ]; then
  line="$line$(printf ' | \033[01;34m%s\033[00m' "$model_name")"
fi
# The active login's usage pills. The account profile itself — its glyph (󰀄) and
# label — is deliberately NOT rendered; only its usage is. Each shown pill wears
# the login's own persistent color (shared with the account menu) so the color
# quietly ties the usage to a login; a dim tag ("5h" / "7d") names the window and
# the fill vs. empty cells carry the amount. Falls back to bold green when no
# color resolved. The 5h pill leads (shorter window first), then 7d, under a
# single " | " so both read as one usage group. Which windows appear is the
# usage_bars preference resolved above.
#
# A window is shown as soon as the account is eligible ($account_label set: 2+
# logins) — NOT gated on its figure being present. Before the first API response
# supplies rate_limits (e.g. the instant after a mid-session account switch
# relaunches claude), the figure is absent, so a "…" placeholder stands in for the
# bar — still painted in the account color, so the switched-to login is visible
# INSTANTLY rather than after the first response lands. Once the figure arrives the
# placeholder upgrades to the real bar in place.
# Eligibility: an account pill needs 2+ logins ($account_label set), but a
# subscription pane is ALWAYS eligible — its usage identity is the backend,
# which exists regardless of how many Claude logins are connected.
_usage_eligible=""
{ [ -n "$account_label" ] || [ -n "${WISP_DECK_CLAUDE_CONFIG:-}" ]; } && _usage_eligible=1
_usage_seg=""
if [ "$show_5h" = 1 ] && [ -n "$_usage_eligible" ]; then
  _seg="$five_hour_bar"; [ -z "$_seg" ] && _seg="…"
  if [ -n "$account_color" ]; then
    _usage_seg="$_usage_seg$(printf ' \033[02;38;5;%sm5h\033[00m \033[38;5;%sm%s\033[00m' "$account_color" "$account_color" "$_seg")"
  else
    _usage_seg="$_usage_seg$(printf ' \033[02;37m5h\033[00m \033[01;32m%s\033[00m' "$_seg")"
  fi
fi
if [ "$show_7d" = 1 ] && [ -n "$_usage_eligible" ]; then
  _seg="$weekly_bar"; [ -z "$_seg" ] && _seg="…"
  if [ -n "$account_color" ]; then
    _usage_seg="$_usage_seg$(printf ' \033[02;38;5;%sm7d\033[00m \033[38;5;%sm%s\033[00m' "$account_color" "$account_color" "$_seg")"
  else
    _usage_seg="$_usage_seg$(printf ' \033[02;37m7d\033[00m \033[01;32m%s\033[00m' "$_seg")"
  fi
fi
# One " | " prefix for the whole group (the segments above each begin with a
# space, so trimming the leading space keeps the "| 5h … 7d …" spacing uniform).
if [ -n "$_usage_seg" ]; then
  line="$line$(printf ' |%s' "$_usage_seg")"
fi
printf '%s' "$line"
