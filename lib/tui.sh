#!/bin/bash
# Shared TUI helpers — colors, logging, cursor utilities.

# Basic colors (with terminal capability fallback)
if [ -t 1 ] && [ "$(tput colors 2>/dev/null)" -ge 8 ] 2>/dev/null; then
  _GREEN='\033[0;32m'
  _YELLOW='\033[0;33m'
  _RED='\033[0;31m'
  _BLUE='\033[0;34m'
  _BOLD='\033[1m'
  _NC='\033[0m'
else
  _GREEN='' _YELLOW='' _RED='' _BLUE='' _BOLD='' _NC=''
fi

# Logging helpers
success() { echo -e "${_GREEN}✓${_NC} $1"; }
warn()    { echo -e "${_YELLOW}!${_NC} $1"; }
error()   { echo -e "${_RED}✗${_NC} $1"; }
info()    { echo -e "${_BLUE}→${_NC} $1"; }
header()  { echo -e "\n${_BOLD}$1${_NC}"; }

# Pre-warm the wisp-deck-tui binary so the FIRST modal open (file-list diff,
# account switcher) doesn't stall. The first exec of a freshly built/installed
# binary pays a macOS Gatekeeper/XProtect assessment (~1s idle, multi-second
# under load) plus paging ~17MB in; every `make release` re-signs the local
# binary, which resets that assessment. Running `--version` once in a disowned
# background job pays it during session setup instead of on the user's click.
# Silent no-op when the binary is missing (the wrapper errors on that itself).
warm_tui_binary() {
  command -v wisp-deck-tui >/dev/null 2>&1 || return 0
  (wisp-deck-tui --version >/dev/null 2>&1 &) >/dev/null 2>&1
  return 0
}

# Set terminal/tab title. With tool: "project · tool", without: "project"
#
# Addressed to /dev/tty, not stdout. The tab-title watcher polls for the whole
# session and calls a dozen libs per tick; if any of them ever echoes, that line
# lands on the session terminal, in the middle of the AI tool's full-screen UI.
# The only way to drop the watcher's stdout is for its one legitimate output —
# this escape — to go somewhere else. The controlling terminal is the same
# window either way, so the title still lands.
#
# Falls back to stdout when there is no controlling terminal (go test, CI, a
# pipe), where the escape is inert anyway.
set_tab_title() {
  local project="$1"
  local tool="${2:-}"
  # Probed by opening it, not with `[ -w /dev/tty ]`: the device node is there
  # and reports writable even for a process with no controlling terminal, where
  # the open then fails with "Device not configured" — printed, of course, to
  # the terminal this whole exercise is about keeping clean.
  local out=/dev/stdout
  { : > /dev/tty; } 2>/dev/null && out=/dev/tty
  if [ -n "$tool" ]; then
    printf '\033]0;%s · %s\007' "$project" "$tool" > "$out"
  else
    printf '\033]0;%s\007' "$project" > "$out"
  fi
}

# Set terminal/tab title for the waiting state: the plain title with a bell
# emoji prefixed, the visual cue that the session needs the user's attention.
set_tab_title_waiting() {
  set_tab_title "🔔 $1" "${2:-}"
}

# Set terminal/tab title for the seen state: still waiting on the user, but the
# user has already looked at the tab and left without answering. The bell has
# done its job by then; the eyes say "you know about this one" so a glance at
# the tab strip separates it from a tab that has not been read at all.
set_tab_title_seen() {
  set_tab_title "👀 $1" "${2:-}"
}

# Extended TUI variables for interactive full-screen UIs.
# Call this before using any of the extended variables.
tui_init_interactive() {
  _CYAN=$'\033[0;36m'
  _DIM=$'\033[2m'
  _INVERSE=$'\033[7m'
  _BG_BLUE=$'\033[48;5;27m'
  _BG_RED=$'\033[48;5;160m'
  _WHITE=$'\033[1;37m'
  _HIDE_CURSOR=$'\033[?25l'
  _SHOW_CURSOR=$'\033[?25h'
  _MOUSE_ON=$'\033[?1000h\033[?1006h'
  _MOUSE_OFF=$'\033[?1000l\033[?1006l'
}

# Point this process's stderr at a log file for the rest of its life, so nothing
# it runs from here on can write to the session terminal.
#
# That terminal is where tmux hands the AI tool a full-screen UI, and it is also
# the wrapper's fd 2 — so a single stray line from any lib, any background loop,
# or the shell itself lands in the middle of that UI (it showed up inside
# Claude's input box). The wrapper used to defend against this by repeating
# `2>/dev/null` at every call site it backgrounded, plus inside the libs those
# jobs call: an invariant restated by hand in a dozen places, which holds only
# until someone forgets once. Closing fd 2 at the wrapper makes forgetting
# harmless.
#
# The output goes to a log rather than /dev/null so a real error is still
# recoverable — silencing the terminal must not mean losing the message. Logs
# from sessions that ended over a week ago are pruned here, since this is the
# only place that owns the directory.
#
# Never fails the session: if the log cannot be opened (read-only home, config
# dir replaced by a file), fd 2 goes to /dev/null instead. Silence is the
# requirement; the log is the bonus.
# Usage: gt_mute_terminal_stderr <log_file>
gt_mute_terminal_stderr() {
  local log="$1" dir="${1%/*}"

  if mkdir -p "$dir" 2>/dev/null && { : > "$log"; } 2>/dev/null; then
    find "$dir" -type f -name '*.log' -mtime +7 -delete 2>/dev/null || true
    exec 2>>"$log"
  else
    exec 2>/dev/null
  fi
}

# Move cursor to row;col
moveto() { printf '\033[%d;%dH' "$1" "$2"; }

# Print N spaces
pad() { printf "%*s" "$1" ""; }

# Draw logo using wisp-deck-tui binary
# Usage: draw_logo tool_name [row] [col]
# If row/col provided, they are ignored (for compatibility)
draw_logo() {
  # shellcheck disable=SC2034  # tool parameter reserved for future use
  local tool="$1"

  if command -v wisp-deck-tui &>/dev/null; then
    wisp-deck-tui show-logo 2>/dev/null || true
  else
    # Fallback: no logo if binary missing
    return 0
  fi
}
