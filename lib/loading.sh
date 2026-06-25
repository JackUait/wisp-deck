#!/bin/bash
# Loading screen with ASCII art, tool-specific color palettes, and animation.

# Print the loading screen ASCII art to stdout.
get_loading_art() {
  cat << 'ART'
+--------------------------------------------------------------------------------------+
|                                                                                      |
|        .d8888b. 888                     888         88888888888     888              |
|       d88P  Y88b888                     888             888         888              |
|       888    888888                     888             888         888              |
|       888       88888b.  .d88b. .d8888b 888888          888  8888b. 88888b.          |
|       888  88888888 "88bd88""88b88K     888             888     "88b888 "88b         |
|       888    888888  888888  888"Y8888b.888             888 .d888888888  888         |
|       Y88b  d88P888  888Y88..88P     X88Y88b.           888 888  888888 d88P         |
|        "Y8888P88888  888 "Y88P"  88888P' "Y888          888 "Y88888888888P"          |
|                                                                                      |
+--------------------------------------------------------------------------------------+
ART
}

# Get color palette for a given AI tool. Prints space-separated 256-color codes.
# Falls back to Claude palette for unknown or empty tool names.
get_tool_palette() {
  case "${1:-}" in
    opencode) echo "60 61 62 99 135 141 147 183" ;;       # violet ramp (OpenCode mascot)
    *)        echo "130 166 172 208 209 214 215 220" ;;   # orange/amber (claude default)
  esac
}

# Render a single frame of the loading screen.
# Args: tool_name frame_number term_cols term_rows [palette_override]
# palette_override is a space-separated 256-colour ramp; when given it wins over
# the tool's default (so a user-chosen theme preset colours the splash).
render_loading_frame() {
  local tool="$1" frame="$2"
  local cols="${3:-80}" rows="${4:-24}"
  local palette_override="${5:-}"

  # Get art lines into array
  local art
  art="$(get_loading_art)"
  local -a lines=()
  while IFS= read -r line; do
    lines+=("$line")
  done <<< "$art"

  # Get palette
  local -a palette
  read -ra palette <<< "${palette_override:-$(get_tool_palette "$tool")}"
  local pal_len=${#palette[@]}

  # Calculate art dimensions
  local art_height=${#lines[@]}
  local art_width=0
  for line in "${lines[@]}"; do
    local len=${#line}
    if (( len > art_width )); then
      art_width=$len
    fi
  done

  # Center position
  local start_row=$(( (rows - art_height) / 2 + 1 ))
  local start_col=$(( (cols - art_width) / 2 + 1 ))
  if (( start_row < 1 )); then start_row=1; fi
  if (( start_col < 1 )); then start_col=1; fi

  # Draw each line with gradient color shifted by frame
  local i
  for i in "${!lines[@]}"; do
    local color_idx=$(( (i + frame) % pal_len ))
    local color="${palette[$color_idx]}"
    printf '\033[%d;%dH\033[38;5;%dm%s' \
      "$((start_row + i))" "$start_col" "$color" "${lines[$i]}"
  done

  printf '\033[0m'
}

# PID of the background animation process.
_LOADING_SCREEN_PID=""

# Detect terminal dimensions reliably. Prints "rows cols" to stdout.
_detect_term_size() {
  local _r _c

  # Method 1: stty size via /dev/tty (most reliable in pty context)
  local _size
  if _size=$( (stty size </dev/tty) 2>/dev/null ) && read -r _r _c <<< "$_size"; then
    if (( _r > 0 && _c > 0 )); then
      echo "$_r $_c"
      return
    fi
  fi

  # Method 2: stty size from stdin (command substitution avoids process substitution
  # which is disabled in bash --posix mode triggered by Ghostty's /bin/sh -c path)
  _size=$(stty size 2>/dev/null)
  if read -r _r _c <<< "$_size"; then
    if (( _r > 0 && _c > 0 )); then
      echo "$_r $_c"
      return
    fi
  fi

  # Method 3: tput (uses terminfo + ioctl)
  _c=$(tput cols 2>/dev/null || echo 0)
  _r=$(tput lines 2>/dev/null || echo 0)
  if (( _r > 0 && _c > 0 )); then
    echo "$_r $_c"
    return
  fi

  # Fallback
  echo "24 80"
}

# Show animated loading screen with tool-specific colors.
# Args: [tool_name] [palette_override] — tool defaults to claude; palette_override
# (a space-separated ramp) lets a user-chosen theme preset colour the splash.
# Sets _LOADING_SCREEN_PID for the caller to stop later.
show_loading_screen() {
  local tool="${1:-claude}"
  local _pal_override="${2:-}"

  # Clear screen, hide cursor (instant dark feedback)
  printf '\033[2J\033[H\033[?25l'

  # Render first frame synchronously so the user always sees loading art,
  # even if stop_loading_screen is called before the background loop starts.
  local _init_rows _init_cols
  read -r _init_rows _init_cols <<< "$(_detect_term_size)"
  render_loading_frame "$tool" 0 "$_init_cols" "$_init_rows" "$_pal_override"

  # First-frame race-condition fix: the PTY may not have reported its final
  # window size when we queried above (returning the fallback 24×80 or a stale
  # value). Sleep briefly to let the PTY deliver the TIOCGWINSZ response, then
  # re-query. If the size changed, clear and re-render so frame 0 is correctly
  # centred before the background animation loop takes over.
  sleep 0.05
  local _recheck_rows _recheck_cols
  read -r _recheck_rows _recheck_cols <<< "$(_detect_term_size)"
  if (( _recheck_rows != _init_rows || _recheck_cols != _init_cols )); then
    printf '\033[2J'
    render_loading_frame "$tool" 0 "$_recheck_cols" "$_recheck_rows" "$_pal_override"
  fi

  # Symbols for floating particles
  local symbols=('·' '•' '°' '∘' '⋅' '∙')

  # Start animation in background
  (
    # Restore the cursor only when the loader is *aborted* (Ctrl-C / window
    # close) so the user lands in a usable shell. A planned stop sends SIGTERM
    # (see stop_loading_screen) and is deliberately left untrapped: the cursor
    # stays hidden so it never blinks over the splash while the menu's alt
    # screen takes over — the menu owns the cursor from then on.
    trap 'printf "\033[?25h\033[0m"; exit 0' INT HUP

    # Brief delay so terminal reports its final size after window opens
    sleep 0.1

    local frame=1
    local rows cols
    local -a prev_sym_positions=()
    local prev_rows=0 prev_cols=0

    while true; do
      # Detect terminal size each frame (handles late window resizes)
      read -r rows cols <<< "$(_detect_term_size)"
      if (( rows != prev_rows || cols != prev_cols )); then
        printf '\033[2J'  # Clear screen on resize to avoid ghost artifacts
        prev_rows="$rows" prev_cols="$cols"
        prev_sym_positions=()
      fi

      # Redraw art with shifted colors
      render_loading_frame "$tool" "$frame" "$cols" "$rows" "$_pal_override"

      # Clear previous floating symbols
      for pos in "${prev_sym_positions[@]}"; do
        local sr sc
        IFS=';' read -r sr sc <<< "$pos"
        printf '\033[%d;%dH ' "$sr" "$sc"
      done
      prev_sym_positions=()

      # Draw new floating symbols
      local -a palette
      read -ra palette <<< "${_pal_override:-$(get_tool_palette "$tool")}"
      local pal_len=${#palette[@]}
      local _s
      for _s in 0 1 2; do
        local sym_row=$(( RANDOM % rows + 1 ))
        local sym_col=$(( RANDOM % cols + 1 ))
        local sym_color="${palette[$(( RANDOM % pal_len ))]}"
        local sym="${symbols[$(( RANDOM % ${#symbols[@]} ))]}"
        printf '\033[%d;%dH\033[2m\033[38;5;%dm%s\033[0m' \
          "$sym_row" "$sym_col" "$sym_color" "$sym"
        prev_sym_positions+=("${sym_row};${sym_col}")
      done

      frame=$(( (frame + 1) % pal_len ))
      sleep 0.15
    done
  ) &
  _LOADING_SCREEN_PID=$!
}

# Stop loading screen animation for the handoff to the menu.
# Sends SIGTERM (untrapped in the animation loop) so the cursor stays hidden:
# the menu runs on the alt screen and owns the cursor from here, restoring it
# on exit. Leaving it hidden avoids a cursor blinking over the splash during
# the menu binary's startup gap. Only colours are reset.
stop_loading_screen() {
  if [ -n "${_LOADING_SCREEN_PID:-}" ]; then
    kill "$_LOADING_SCREEN_PID" 2>/dev/null
    wait "$_LOADING_SCREEN_PID" 2>/dev/null
    _LOADING_SCREEN_PID=""
  fi
  printf '\033[0m'
}
