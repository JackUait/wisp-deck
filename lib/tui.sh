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
set_tab_title() {
  local project="$1"
  local tool="${2:-}"
  if [ -n "$tool" ]; then
    printf '\033]0;%s · %s\007' "$project" "$tool"
  else
    printf '\033]0;%s\007' "$project"
  fi
}

# Set terminal/tab title for the waiting state. Identical to set_tab_title — no
# dot prefix. Ghostty's native bell icon (terminal_bell notif channel) is the
# sole waiting indicator, so a separate text dot would be redundant clutter.
set_tab_title_waiting() {
  set_tab_title "$@"
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
