#!/bin/bash
# Auto-switch setting helpers — pure, no side effects on source. The setting
# turns on in-place account rotation: when a session's quota usage reaches the
# threshold (98%), the AI pane is relaunched under the next pooled account via
# the same mid-session switch the ledger pill drives (lib/account-switch.sh),
# and the conversation is continued automatically in the new login. Stored as a
# single-value flag file (on/off), mirroring the claude-account pointer-file
# style.

# get_auto_switch <flag_file> — prints "on" or "off" (default off; any value
# other than "on" reads as off).
get_auto_switch() {
  local file="$1" val=""
  if [ -f "$file" ]; then
    IFS= read -r val < "$file" || true
    val="${val//[[:space:]]/}"
  fi
  if [ "$val" = "on" ]; then
    echo "on"
  else
    echo "off"
  fi
}

# set_auto_switch <flag_file> <on|off> — writes the normalized value ("on" only
# when exactly "on", otherwise "off").
set_auto_switch() {
  local file="$1" value="$2"
  mkdir -p "$(dirname "$file")"
  if [ "$value" = "on" ]; then
    echo "on" > "$file"
  else
    echo "off" > "$file"
  fi
}

# proxy_startup_port <json_line> — extracts the "port" number from the proxy's
# startup JSON ({"port":N,"key":"..."}), or empty if not present.
proxy_startup_port() {
  printf '%s' "$1" | sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p'
}

# proxy_startup_key <json_line> — extracts the "key" string from the proxy's
# startup JSON, or empty if not present.
proxy_startup_key() {
  printf '%s' "$1" | sed -n 's/.*"key"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

# proxy_startup_ca <json_line> — extracts the "ca" cert path from the proxy's
# startup JSON (present only in MITM mode), or empty if absent.
proxy_startup_ca() {
  printf '%s' "$1" | sed -n 's/.*"ca"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

# is_auto_switch_enabled <flag_file> — exit 0 when the setting is on.
is_auto_switch_enabled() {
  [ "$(get_auto_switch "$1")" = "on" ]
}

# auto_switch_eligible <accounts_list_file> — exit 0 when there are at least two
# accounts to rotate between. The list holds only the *managed* logins
# (label:dir lines, skipping comments/blanks); the implicit Default (Keychain)
# login always exists on top of them and IS part of the in-place rotation, so a
# single managed entry already gives two accounts. (The proxy-era rule needed 2+
# managed logins because the Keychain login could not be pooled.)
auto_switch_eligible() {
  local file="$1" line
  [ -f "$file" ] || return 1
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"  # ltrim
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" != *:* ]] && continue
    return 0
  done < "$file"
  return 1
}

# The usage percentage at which auto-switch rotates to the next account, and how
# long a rotated-away-from account stays suppressed (one 5h quota window) so a
# still-exhausted login can't retrigger in a loop.
GT_AUTO_SWITCH_THRESHOLD=98
GT_AUTO_SWITCH_WINDOW_SECS=18000

# auto_switch_threshold_reached [pct]... — exit 0 when ANY given usage
# percentage is a number at or above the threshold. Non-numeric or empty values
# never trip it (the statusline passes raw window figures that may be absent).
auto_switch_threshold_reached() {
  local pct
  for pct in "$@"; do
    case "$pct" in
      '' | *[!0-9]*) continue ;;
    esac
    [ "$pct" -ge "$GT_AUTO_SWITCH_THRESHOLD" ] && return 0
  done
  return 1
}

# auto_switch_next_account <list_file> <current_dir> — print the account AFTER
# current in rotation order: the implicit Default login first, then the managed
# dirs in list order, wrapping at the end (so the last managed entry rotates
# back to the Default). Prints a managed dir name, or the literal "default" for
# the Default login (callers map it — relaunch_ai_pane already treats
# ""/"default" as the Keychain login). Current "" and "default" both mean the
# Default; an unknown current (a dir since removed from the list) restarts at
# the first managed entry. Exit 1 when the list holds no managed account at all
# (only the Default exists — nothing to rotate to).
auto_switch_next_account() {
  local file="$1" current="$2" label dir
  local cycle=("default") idx=0 i=1
  [ -f "$file" ] || return 1
  [ -n "$current" ] || current="default"
  while IFS=: read -r label dir; do
    label="${label#"${label%%[![:space:]]*}"}"  # ltrim
    [[ -z "$label" || "$label" == \#* ]] && continue
    [ -n "$dir" ] || continue
    cycle+=("$dir")
    [ "$dir" = "$current" ] && idx=$((i))
    i=$((i + 1))
  done < "$file"
  [ "${#cycle[@]}" -ge 2 ] || return 1
  local next
  next="${cycle[$(((idx + 1) % ${#cycle[@]}))]}"
  [ "$next" != "$current" ] || return 1
  printf '%s\n' "$next"
}

# auto_switch_guard <state_file> <account> <now_epoch> — the once-per-window
# latch: exit 0 (and record "epoch:account") only when the account has NOT been
# auto-switched away from within the last quota window; exit 1 otherwise. The
# state file is shared across sessions on purpose — quota is per-account, so one
# session rotating off an exhausted login covers them all. Expired entries are
# pruned on each pass.
auto_switch_guard() {
  local file="$1" acct="$2" now="$3" epoch entry_acct keep=""
  local cutoff=$((now - GT_AUTO_SWITCH_WINDOW_SECS))
  if [ -f "$file" ]; then
    while IFS=: read -r epoch entry_acct; do
      case "$epoch" in '' | *[!0-9]*) continue ;; esac
      [ "$epoch" -gt "$cutoff" ] || continue
      [ "$entry_acct" = "$acct" ] && return 1
      keep="$keep$epoch:$entry_acct
"
    done < "$file"
  fi
  mkdir -p "$(dirname "$file")" 2>/dev/null
  printf '%s%s:%s\n' "$keep" "$now" "$acct" > "$file"
  return 0
}

# auto_switch_maybe_trigger <five_hour_pct> <weekly_pct> — the statusline hook.
# Runs inside claude's statusline process on every refresh; when the session's
# usage reaches the threshold (and the setting is on, 2+ managed accounts exist,
# and this account hasn't rotated away this window), it kicks off the in-place
# switch to the next pooled account. The switch itself runs via `tmux run-shell
# -b` — hosted by the tmux SERVER, not this pane — because the relaunch respawns
# the pane this very process lives in; anything less detached would be killed
# mid-switch. Reads its context from the pane env wrapper.sh stamped at launch
# (WISP_DECK_RELAUNCH_FILE, WISP_DECK_LIB_DIR, CLAUDE_CONFIG_DIR). Always exits
# 0 — the statusline must render no matter what.
auto_switch_maybe_trigger() {
  local five_hour="${1:-}" weekly="${2:-}"
  [ -n "${TMUX:-}" ] || return 0
  local relaunch="${WISP_DECK_RELAUNCH_FILE:-}" lib="${WISP_DECK_LIB_DIR:-}"
  [ -n "$relaunch" ] && [ -f "$relaunch" ] || return 0
  [ -n "$lib" ] && [ -f "$lib/account-switch.sh" ] || return 0
  local root="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck"
  is_auto_switch_enabled "$root/auto-switch-accounts" || return 0
  auto_switch_eligible "$root/claude-accounts.list" || return 0
  auto_switch_threshold_reached "$five_hour" "$weekly" || return 0
  # The account THIS claude runs is its own config dir's basename (empty =
  # Default) — authoritative even right after a mid-session switch, unlike the
  # global pointer.
  local cur="${CLAUDE_CONFIG_DIR:-}" target
  cur="${cur##*/}"
  target="$(auto_switch_next_account "$root/claude-accounts.list" "$cur")" || return 0
  [ -n "$target" ] || return 0
  # Latch BEFORE launching the switch: the statusline refreshes continuously,
  # and the respawn takes seconds — without the latch every refresh in between
  # would queue another switch.
  auto_switch_guard "$root/auto-switch-state" "${cur:-default}" "$(date +%s)" || return 0
  local cmd
  cmd="$(printf 'source %q && source %q && source %q && source %q && source %q && auto_switch_relaunch tmux %q %q' \
    "$lib/statusline.sh" "$lib/claude-accounts.sh" "$lib/claude-shared-settings.sh" \
    "$lib/tmux-session.sh" "$lib/account-switch.sh" \
    "$relaunch" "$target")"
  tmux run-shell -b "bash -c $(printf '%q' "$cmd")" 2>/dev/null || true
  return 0
}
