#!/bin/bash
# shellcheck source=../lib/statusline.sh
source "$(dirname "$0")/../lib/statusline.sh" 2>/dev/null \
  || source ~/.claude/statusline-helpers.sh 2>/dev/null \
  || true

input=$(cat)
# Record this pane's conversation id in the tmux session env so a session
# restored after a reboot reopens this exact conversation.
type gt_stamp_claude_session &>/dev/null && gt_stamp_claude_session "$input"
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
    account_color=$(gt_account_color "$_gt_accounts_root/claude-account-colors" "$account_key")
  fi
fi

# Weekly (7-day) usage of the active login, shown right of the account label so
# the user sees how much of that account's rolling quota is spent. Rendered as a
# pill bar painted in the account's own profile color (the fill vs. empty cells
# carry the amount; the color ties it to the login). Only present for subscribers
# after the first API response, so it stays empty otherwise.
weekly_bar=""
if [ -n "$account_label" ] && type gt_weekly_used_pct &>/dev/null; then
  weekly_pct=$(gt_weekly_used_pct "$input")
  if [ -n "$weekly_pct" ]; then
    weekly_bar=$(gt_usage_bar "$weekly_pct")
  fi
fi

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
# The active login's WEEKLY (7-day) usage pill. The account profile itself — its
# glyph (󰀄) and label — is deliberately NOT rendered here; only its usage is. The
# pill still wears the login's own persistent color (each login gets a distinct
# one, shared with the account menu) so the color quietly ties the usage to a
# login; the dim "7d" tag names the window and the fill vs. empty cells carry the
# amount. Falls back to bold green when no color resolved. Gated on the weekly bar
# — itself only computed for a 2+ account setup that has a weekly figure — so solo
# users get nothing.
if [ -n "$weekly_bar" ]; then
  if [ -n "$account_color" ]; then
    line="$line$(printf ' | \033[02;38;5;%sm7d\033[00m \033[38;5;%sm%s\033[00m' "$account_color" "$account_color" "$weekly_bar")"
  else
    line="$line$(printf ' | \033[02;37m7d\033[00m \033[01;32m%s\033[00m' "$weekly_bar")"
  fi
fi
printf '%s' "$line"
