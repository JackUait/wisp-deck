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
# Default), which this statusline process inherits. Shown only when the
# multi-account feature is actually in use — the accounts list has at least one
# entry — so single-login users get no extra clutter.
account_label=""
accounts_list="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/claude-accounts.list"
if type gt_claude_account_label &>/dev/null \
   && grep -q '^[^#[:space:]]' "$accounts_list" 2>/dev/null; then
  account_label=$(gt_claude_account_label "${CLAUDE_CONFIG_DIR:-}" "$accounts_list")
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
# A Nerd Font account glyph (󰀄) precedes the active Claude account label so the
# user can tell at a glance which login this tab is talking to. Literal UTF-8 is
# embedded directly: this runs under macOS bash 3.2 (--posix), whose printf has
# no \u/\U escape support.
if [ -n "$account_label" ]; then
  line="$line$(printf ' | \033[01;32m󰀄 %s\033[00m' "$account_label")"
fi
printf '%s' "$line"
