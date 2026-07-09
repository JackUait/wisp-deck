#!/bin/bash
# Theme presets for the bash-side chrome (tmux pane border + spare-tab chip +
# loading-screen ramp). The single source of truth for colours lives in the Go
# theme (internal/tui/theme.go); these tables mirror it so the bash surfaces a
# user picks in the Settings menu render in the same palette.
#
# Pure functions only — safe to source more than once (the loader sources this
# early; the lib loop sources it again).

# gt_resolve_theme <pref> <tool> — map a user theme preference to a concrete
# preset key. A named preset wins; "auto"/empty/unknown follows the AI tool
# (claude → orange, opencode → purple), matching Go's ResolveTheme.
gt_resolve_theme() {
  local pref="${1:-}" tool="${2:-}"
  case "$pref" in
    orange|purple|green|blue|rose|cyan) echo "$pref"; return ;;
  esac
  case "$tool" in
    opencode) echo "purple" ;;
    codex)    echo "teal" ;;
    *)        echo "orange" ;;
  esac
}

# get_theme_accent <theme_key> — the single focus accent (active pane border +
# active spare-tab chip), mirroring each Go palette's Primary. Prints a
# 256-colour number.
get_theme_accent() {
  case "${1:-}" in
    purple) echo "141" ;;
    green)  echo "78" ;;
    blue)   echo "75" ;;
    rose)   echo "211" ;;
    cyan)   echo "80" ;;
    teal)   echo "36" ;;
    *)      echo "209" ;; # orange (default)
  esac
}

# get_theme_palette <theme_key> — the 8-stop dark→light ramp the loading screen
# cycles through. Prints space-separated 256-colour numbers.
get_theme_palette() {
  case "${1:-}" in
    purple) echo "60 61 62 99 135 141 147 183" ;;
    green)  echo "22 28 34 35 41 77 78 120" ;;
    blue)   echo "17 18 25 26 31 32 75 117" ;;
    rose)   echo "52 89 125 161 168 205 211 218" ;;
    cyan)   echo "23 30 37 43 44 80 116 123" ;;
    teal)   echo "23 29 30 36 43 49 86 158" ;;
    *)      echo "130 166 172 208 209 214 215 220" ;; # orange (default)
  esac
}
