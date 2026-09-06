#!/bin/bash
# Keep-awake — hold the kernel SleepDisabled flag while an AI agent is working,
# so a closed lid does not suspend the machine mid-turn.
#
# Why not caffeinate: caffeinate creates power *assertions*, which veto only the
# idle-sleep timer. A lid close is a separate, explicit sleep request that the
# assertion layer does not gate, so `caffeinate -dimsu` sleeps anyway on Apple
# Silicon. The only flag that survives a lid close is IOPMrootDomain's
# SleepDisabled, set via `pmset -a disablesleep 1` — which requires root.
#
# Root comes from a narrowly-scoped /etc/sudoers.d/wisp-deck rule granting
# NOPASSWD for exactly those two pmset invocations and nothing else. The rule is
# opt-in: installed only when the user turns on the Settings toggle. Without it
# `sudo -n` fails and every entry point here degrades to a silent no-op rather
# than stalling a session behind a password prompt.
#
# SleepDisabled is GLOBAL machine state, not per-process: nothing releases it if
# we die holding it, and a laptop that never sleeps cooks itself in a bag. So
# holders are refcounted as files under <config_dir>/keep-awake.d/<session>,
# each containing the owning wrapper's PID, and every sync reaps holders whose
# PID is gone. The flag drops as soon as the last live holder does.

# Path to pmset/sudo, overridable so tests never touch the real kernel flag.
keep_awake_pmset() { echo "${WISP_DECK_PMSET:-/usr/bin/pmset}"; }
keep_awake_sudo() { echo "${WISP_DECK_SUDO:-/usr/bin/sudo}"; }

# Directory holding one file per session that currently wants the machine awake.
keep_awake_holders_dir() { echo "$1/keep-awake.d"; }

# Marker recording that wisp-deck set the sleep veto. Written before the flag
# is raised and removed after it is cleared, so a crash between the two leaves
# the marker behind — a later idle sync still probes pmset and clears the
# stranded flag. Its ABSENCE is what lets the common case (feature unused,
# nothing held) skip the ~130ms pmset -g spawn on every launch and close.
keep_awake_veto_marker() { echo "$1/keep-awake.veto"; }

# Return 0 when the settings file opts into the feature. Default is off: this
# takes standing root and defeats the lid switch, so it must never turn itself on.
# Usage: keep_awake_enabled <settings_file>
keep_awake_enabled() {
  local settings_file="$1" line
  [ -f "$settings_file" ] || return 1
  # Read in the shell: this runs on every watcher tick in every open session,
  # and it is the first thing the tick does even when the feature is off, which
  # is the default. The grep it replaces was anchored at both ends and allowed
  # only trailing whitespace, so both halves of that are reproduced here.
  # `|| [ -n "$line" ]` keeps a final line with no trailing newline, which grep
  # would have seen.
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      keep_awake=on) return 0 ;;
      keep_awake=on*)
        case "${line#keep_awake=on}" in
          *[![:space:]]*) ;;
          *) return 0 ;;
        esac
        ;;
    esac
  done < "$settings_file"
  return 1
}

# Return 0 when the sudoers rule is in place (passwordless pmset works).
# The probe MUST be one of the exact commands the rule grants — anything else
# (e.g. `pmset -g`) fails `sudo -n` even with the rule installed, which made
# every launch re-ask for the password. `-l` asks sudo whether the command is
# allowed without running it, so the probe never touches the kernel flag.
keep_awake_can_sudo() {
  "$(keep_awake_sudo)" -n -l "$(keep_awake_pmset)" -a disablesleep 1 >/dev/null 2>&1
}

# Echo the current kernel flag: "1" when sleep is disabled, else "0".
# pmset omits the SleepDisabled line entirely when the flag is clear.
keep_awake_sleep_disabled() {
  if "$(keep_awake_pmset)" -g 2>/dev/null | grep -qE '^[[:space:]]*SleepDisabled[[:space:]]+1'; then
    echo 1
  else
    echo 0
  fi
}

# Set the kernel flag. Quiet no-op when the sudoers rule is absent.
# When a config dir is given, maintains the veto marker: written BEFORE
# raising the flag (crash-safe — a stranded flag always has its marker),
# removed after clearing it.
# Usage: keep_awake_set <0|1> [config_dir]
keep_awake_set() {
  local want="$1" config_dir="${2:-}"
  if [ -n "$config_dir" ] && [ "$want" = "1" ]; then
    : > "$(keep_awake_veto_marker "$config_dir")" 2>/dev/null || true
  fi
  "$(keep_awake_sudo)" -n "$(keep_awake_pmset)" -a disablesleep "$want" >/dev/null 2>&1 || true
  if [ -n "$config_dir" ] && [ "$want" = "0" ]; then
    rm -f "$(keep_awake_veto_marker "$config_dir")" 2>/dev/null || true
  fi
  return 0
}

# Drop holder files whose owning PID is dead — a crashed session must not pin
# the flag on forever. A holder with an unreadable/empty PID is also dropped.
#
# Liveness is `ps -p`, not `kill -0`: kill(2) returns EPERM for a live process
# owned by another user, which would read as "dead" and let one user's session
# silently release the flag another user's session is holding.
#
# Every live session reaps the whole directory twice a second, so two reapers
# routinely race: one rm's a holder between the other's glob and its read. The
# read must therefore stay silent when the file is already gone — an open
# failure IS the other reaper doing this one's job. The redirect is wrapped in a
# group so stderr is rerouted BEFORE the input redirect runs; a trailing
# `2>/dev/null` on the assignment is applied left-to-right and comes too late,
# so the failure printed straight to the session terminal, on top of the AI
# tool's UI.
# Usage: keep_awake_reap <config_dir>
keep_awake_reap() {
  local holders
  holders="$1/keep-awake.d"
  [ -d "$holders" ] || return 0
  local f pid entries="" batch="" saved_ifs="$IFS"
  for f in "$holders"/*; do
    [ -e "$f" ] || continue
    pid=""
    { read -r pid < "$f"; } 2>/dev/null || pid=""
    pid="${pid//[[:space:]]/}"
    # A PID that is empty, non-numeric, or wider than the kernel's own range
    # can never be probed, and `ps -p` refuses the WHOLE batch on one of them
    # ("process id too large") -- which would report every other holder as dead
    # and release the veto out from under every live session. Drop it here.
    case "$pid" in
      '' | *[!0-9]*)
        rm -f "$f" 2>/dev/null || true
        continue
        ;;
    esac
    if [ "${#pid}" -gt 5 ]; then
      rm -f "$f" 2>/dev/null || true
      continue
    fi
    entries="$entries$pid $f
"
    batch="$batch,$pid"
  done
  [ -n "$batch" ] || return 0

  # ONE liveness probe for the whole directory. Every live session reaps every
  # other session's holder twice a second, so a probe per holder made the
  # machine-wide cost quadratic in sessions -- 24 of a watcher tick's 52 forks
  # on a 17-session deck.
  #
  # Every PID above is 1-5 digits, so ps cannot fail on the argument itself: a
  # non-zero status here means none of them are alive, not that the probe broke.
  local live
  live="$(ps -o pid= -p "${batch#,}" 2>/dev/null)"

  local live_set=" " token
  # Split on newline/space/tab in the current shell. A pipe would run the loop
  # in a subshell, and a here-string is a 512-byte pipe under bash 5.3.
  IFS="
 	"
  set -f
  for token in $live; do
    [ -n "$token" ] && live_set="$live_set$token "
  done
  IFS="
"
  for token in $entries; do
    pid="${token%% *}"
    f="${token#* }"
    case "$live_set" in
      *" $pid "*) ;;
      *) rm -f "$f" 2>/dev/null || true ;;
    esac
  done
  set +f
  IFS="$saved_ifs"
}

# Reconcile the kernel flag with the live holder set. Called on every watcher
# tick, so it reads the current flag first and shells out to sudo only on an
# actual transition.
# Usage: keep_awake_sync <config_dir>
keep_awake_sync() {
  local config_dir="$1"
  keep_awake_reap "$config_dir"

  local holders want=0
  holders="$(keep_awake_holders_dir "$config_dir")"
  if [ -d "$holders" ] && [ -n "$(ls -A "$holders" 2>/dev/null)" ]; then
    want=1
  fi

  # Idle fast path: nothing held and no record that we ever raised the veto —
  # the flag cannot be ours to clear, so skip the pmset probe entirely. This
  # runs on every launch and window close, where the probe was pure overhead.
  if [ "$want" -eq 0 ] && [ ! -e "$(keep_awake_veto_marker "$config_dir")" ]; then
    return 0
  fi

  if [ "$(keep_awake_sleep_disabled)" = "$want" ]; then
    # Already reconciled. A leftover marker with the flag down would make
    # every future idle sync probe again — retire it here.
    [ "$want" -eq 0 ] && rm -f "$(keep_awake_veto_marker "$config_dir")" 2>/dev/null
    return 0
  fi
  keep_awake_set "$want" "$config_dir"
}

# Register this session as needing the machine awake, then sync.
# Usage: keep_awake_hold <config_dir> <session> <pid>
keep_awake_hold() {
  local config_dir="$1" session="$2" pid="$3"
  local holders
  holders="$(keep_awake_holders_dir "$config_dir")"
  mkdir -p "$holders" || return 0
  printf '%s\n' "$pid" > "$holders/$session"
  keep_awake_sync "$config_dir"
}

# Deregister this session, then sync. Clears the flag when it was the last one.
# Usage: keep_awake_drop <config_dir> <session>
keep_awake_drop() {
  local config_dir="$1" session="$2"
  rm -f "$(keep_awake_holders_dir "$config_dir")/$session"
  keep_awake_sync "$config_dir"
}

# Reconcile this session's holder with the agent's current state. Called from
# the tab-title watcher's poll loop, which already classifies the AI pane as
# "active" (mid-turn) or "waiting" (idle at the prompt).
#
# The setting is re-read every tick rather than captured at launch, so toggling
# it off in the Settings menu releases a session that is mid-turn — otherwise a
# user who changes their mind stays pinned awake until the agent finishes.
# Usage: keep_awake_tick <config_dir> <session> <pid> <state>
keep_awake_tick() {
  local config_dir="$1" session="$2" pid="$3" state="$4"
  if keep_awake_enabled "$config_dir/settings" && [ "$state" = "active" ]; then
    keep_awake_hold "$config_dir" "$session" "$pid"
    return 0
  fi
  # Releasing costs a pmset probe, so skip it entirely in the overwhelmingly
  # common case: feature off (or agent idle) and this session holds nothing.
  # Without this every session pays two subprocesses a second for a feature it
  # is not using.
  # The path is written out rather than taken from keep_awake_holders_dir: a
  # bash function cannot hand back a string without a subshell, and this line
  # runs twice a second in every open session for a feature that is off by
  # default. Measured over 1000 ticks: 0.681s of child CPU through the
  # substitution, 0.000s through the literal.
  # TestKeepAwakeTick_holder_path_matches_the_helper pins the two together.
  [ -e "$config_dir/keep-awake.d/$session" ] || return 0
  keep_awake_drop "$config_dir" "$session"
}

# Echo the sudoers rule granting passwordless access to exactly the two pmset
# invocations this module makes — nothing else. Kept as a pure function so a
# test can assert the blast radius without touching /etc.
# Usage: keep_awake_sudoers_content <user>
keep_awake_sudoers_content() {
  local user="$1"
  cat << EOF
# Installed by wisp-deck so an AI agent can hold the machine awake mid-turn.
# Grants ONLY the two pmset calls that toggle the lid-close sleep veto.
# Remove this file to revoke: sudo rm /etc/sudoers.d/wisp-deck
$user ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 0
$user ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 1
EOF
}

# Clear the screen and draw a centered, theme-accented rounded box. The first
# line is rendered bold as the title. Used for the two moments keep-awake talks
# to the user directly: granting the sudo rule and revoking it.
# Usage: keep_awake_window <config_dir> <title> <body line>...
keep_awake_window() {
  local config_dir="$1"
  shift
  local inner=62

  # Border in the theme accent when theme.sh is loaded (the wrapper sources it
  # before this runs); plain when the module is used standalone.
  local c="" b=$'\033[1m' n=$'\033[0m'
  if declare -f get_theme_accent >/dev/null 2>&1; then
    local _pref _tool
    _pref="$(grep '^theme=' "$config_dir/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
    _tool="$({ tr -d '[:space:]' < "$config_dir/ai-tool"; } 2>/dev/null || true)"
    c=$'\033[38;5;'"$(get_theme_accent "$(gt_resolve_theme "$_pref" "$_tool")")m"
  fi

  local cols margin pad
  cols="$(tput cols 2>/dev/null)" || cols=80
  [ -n "$cols" ] || cols=80
  pad=$(( (cols - inner - 4) / 2 ))
  [ "$pad" -gt 0 ] || pad=0
  margin="$(printf '%*s' "$pad" '')"

  # Built by loop, not tr: tr is byte-wise and mangles the multibyte '─'.
  local hr="" i
  for ((i = 0; i < inner + 2; i++)); do hr+='─'; done

  printf '\033[2J\033[H\n\n'
  printf '%s%s╭%s╮%s\n' "$margin" "$c" "$hr" "$n"
  local idx=0 line style
  for line in "$@"; do
    style=""
    [ "$idx" -eq 0 ] && style="$b"
    printf '%s%s│%s %s%s%s%*s %s│%s\n' \
      "$margin" "$c" "$n" "$style" "$line" "$n" "$((inner - ${#line}))" "" "$c" "$n"
    idx=$((idx + 1))
  done
  printf '%s%s╰%s╯%s\n\n' "$margin" "$c" "$hr" "$n"
}

# Render the one-time password window: why root is needed, exactly what the
# sudoers rule grants, and how to revoke it. Clears the screen first — the
# launch splash is still up when this runs, and sudo's bare prompt spilling
# over it is exactly what this window replaces. sudo's Password prompt lands
# directly beneath the box.
# Body lines are ASCII-only: padding is computed from ${#line}, which counts
# bytes under a C locale — a multibyte character would knock the border loose.
# Usage: keep_awake_prompt_window <config_dir>
keep_awake_prompt_window() {
  keep_awake_window "$1" \
    "Keep the Mac awake - one-time setup" \
    "" \
    "\"Keep awake while working\" is now on. While an agent is" \
    "busy, wisp-deck holds a system flag so closing the lid does" \
    "not put the Mac to sleep and stall the agent mid-turn." \
    "" \
    "Setting that flag needs root, so wisp-deck installs a sudo" \
    "rule. It allows ONLY these two commands, nothing else:" \
    "" \
    "    pmset -a disablesleep 1" \
    "    pmset -a disablesleep 0" \
    "" \
    "Revoke it anytime:  sudo rm /etc/sudoers.d/wisp-deck" \
    "" \
    "Enter your macOS password to finish, or press Ctrl-C to" \
    "skip - keep-awake will simply stay off."
}

# Grant the sudoers rule if the feature is on and the rule is missing. Called
# once, right after the Settings menu closes — a password prompt belongs there,
# not inside the tmux session where nothing is listening on the terminal.
# Never prompts when the feature is off. Failure is non-fatal: the session runs,
# it just cannot hold the machine awake.
# Usage: keep_awake_ensure_sudoers <config_dir>
keep_awake_ensure_sudoers() {
  local config_dir="$1"
  keep_awake_enabled "$config_dir/settings" || return 0
  keep_awake_can_sudo && return 0

  keep_awake_prompt_window "$config_dir"
  keep_awake_install_sudoers || {
    printf '\n  Could not install the rule — keep-awake stays inactive.\n' >&2
    return 0
  }
  printf '\n  Keep-awake is ready.\n'
}

# The in-app path to revoke the standing root grant: offer to remove the
# sudoers rule right after the user turns the keep-awake toggle OFF in the
# Settings menu. Fires only on the on→off flip while the rule is still
# granted — every launch passes through this call site, so any other
# combination must stay silent. Anything but an explicit yes keeps the rule:
# it is access the user consciously granted, so "keep" is the default.
# Usage: keep_awake_offer_revoke <config_dir> <was_on 0|1>
keep_awake_offer_revoke() {
  local config_dir="$1" was_on="$2"
  [ "$was_on" = "1" ] || return 0
  keep_awake_enabled "$config_dir/settings" && return 0
  keep_awake_can_sudo || return 0

  keep_awake_window "$config_dir" \
    "Keep-awake is off - revoke its sudo access?" \
    "" \
    "The sudo rule that let wisp-deck hold the machine awake is" \
    "still installed. It grants only these two commands:" \
    "" \
    "    pmset -a disablesleep 1" \
    "    pmset -a disablesleep 0" \
    "" \
    "Removing it asks for your macOS password once. Keeping it" \
    "is harmless while the feature is off, and you can always" \
    "remove it later:  sudo rm /etc/sudoers.d/wisp-deck"

  local answer=""
  printf '  Remove the rule now? [y/N] '
  read -r answer || true
  case "$answer" in
    y | Y | yes | YES) ;;
    *) return 0 ;;
  esac

  # Clear the kernel flag while the rule still permits it — revoking with
  # SleepDisabled stuck at 1 would leave a machine that can never sleep.
  keep_awake_set 0
  if "$(keep_awake_sudo)" -p $'  \033[1mPassword:\033[0m ' rm -f "$(keep_awake_sudoers_path)" 2>/dev/null; then
    printf '\n  Sudo access revoked.\n'
  else
    printf '\n  Could not remove the rule — it is still installed.\n' >&2
    return 0
  fi
}

keep_awake_sudoers_path() { echo "${WISP_DECK_SUDOERS:-/etc/sudoers.d/wisp-deck}"; }
keep_awake_visudo() { echo "${WISP_DECK_VISUDO:-/usr/sbin/visudo}"; }

# Install the sudoers rule. Interactive: sudo prompts for a password once, here,
# and never again. Returns non-zero (installing nothing) if anything fails.
#
# The rule is syntax-checked with `visudo -cf` on a temp file BEFORE it is moved
# into /etc/sudoers.d. sudo refuses to run at all when any file in that
# directory is malformed, so an unvalidated write could lock the user out of
# root on their own machine.
# Usage: keep_awake_install_sudoers [user]
keep_awake_install_sudoers() {
  local user="${1:-$(id -un)}"
  local target tmp
  target="$(keep_awake_sudoers_path)"

  tmp="$(mktemp -t wisp-deck-sudoers)" || return 1
  keep_awake_sudoers_content "$user" > "$tmp"

  if ! "$(keep_awake_visudo)" -cf "$tmp" >/dev/null 2>&1; then
    rm -f "$tmp"
    return 1
  fi

  # 0440 — sudo ignores sudoers.d entries that are group- or world-writable.
  # Ownership needs no flags: install runs as root here, so the file is root's.
  # -p: this is the one interactive sudo in the module; the prompt sits right
  # under the explanation window, so style it to read as part of it.
  if ! "$(keep_awake_sudo)" -p $'  \033[1mPassword:\033[0m ' install -m 0440 "$tmp" "$target" 2>/dev/null; then
    rm -f "$tmp"
    return 1
  fi
  rm -f "$tmp"
}
