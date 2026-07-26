#!/bin/bash
# npm-based update check for wisp-deck.

# The update screen is the splash wearing a progress bar: same wordmark, same
# palettes, same size detection. Source them when the caller has not already.
if ! declare -f render_loading_frame >/dev/null 2>&1; then
  # shellcheck source=lib/loading.sh
  source "$(dirname "${BASH_SOURCE[0]}")/loading.sh"
fi

# Print the pending update version found by a previous background check, or
# nothing when there is none. A flag equal to the installed version is stale
# (written before an update, inside the 24h re-check throttle) and is ignored.
# Args: install_dir (where the .version marker lives)
get_update_version() {
  local install_dir="$1"
  local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
  local flag="${config_home}/wisp-deck/update-available"
  [ -f "$flag" ] || return 0

  local remote_version local_version
  remote_version="$(tr -d '[:space:]' < "$flag")"
  local_version="$(cat "$install_dir/.version" 2>/dev/null | tr -d '[:space:]')"
  [ -n "$remote_version" ] || return 0
  [ "$remote_version" = "$local_version" ] && return 0
  echo "$remote_version"
}

# Show update-available notification if a previous background check found a
# newer version. Keeps the flag: the main menu reads it too (via
# get_update_version) and must keep offering the update until it runs.
# Args: install_dir (optional; defaults to the npm install location)
notify_if_update_available() {
  local install_dir="${1:-$HOME/.local/share/wisp-deck}"
  local version
  version="$(get_update_version "$install_dir")"
  [ -n "$version" ] || return 0
  echo "  ↑ Update available: v${version} — run 'npx wisp-deck' to update"
}

# True when this machine already holds a usable wisp-deck configuration — i.e.
# the installer is running as an update, not a first-time setup. The saved AI
# tool is the marker: it is the one thing the installer *asks* for, so its
# presence is exactly "the user has already answered the setup questions".
wisp_deck_is_configured() {
  local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
  [ -s "${config_home}/wisp-deck/ai-tool" ]
}

# Decide which AI tool this installer run configures, and how it found out.
# Args: update_mode (1 = never ask)
# Sets: _selected_ai_tool, _selected_ai_tools, _selected_ai_tool_source
#       ("saved" when reused, "selected" when the user just picked it)
#
# Pressing U in the menu used to run the first-time setup script verbatim, so an
# update stopped on the "Select AI Tools" screen — a question the user answered
# once, re-asked in the middle of an operation that should have returned them to
# the menu. An update reuses the answer; only a machine with no answer at all is
# asked for one.
resolve_setup_ai_tool() {
  local update_mode="${1:-0}"
  local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"

  if [ "$update_mode" = "1" ] || wisp_deck_is_configured; then
    local saved=""
    if [ -f "${config_home}/wisp-deck/ai-tool" ]; then
      saved="$(tr -d '[:space:]' < "${config_home}/wisp-deck/ai-tool")"
    fi
    _selected_ai_tool="${saved:-claude}"
    _selected_ai_tools="$_selected_ai_tool"
    _selected_ai_tool_source="saved"
    return 0
  fi

  _selected_ai_tool_source="selected"
  select_ai_tool_interactive
}

# Map a line of installer output to a step the user recognises. Returns 1 for
# anything unrecognised so the caller keeps the step already on screen instead
# of flickering through npm's warnings.
update_step_label() {
  case "$1" in
    *"Downloading wisp-deck-tui"*|*"Updating wisp-deck-tui"*|*"wisp-deck-tui"*"already up to date"*)
      echo "Downloading interface" ;;
    *"Installing wisp-deck"*|*"Installed wisp-deck"*|*"wisp-deck"*"already up to date"*)
      echo "Installing files" ;;
    "Setup complete"*|*"Setup complete!"*)
      echo "Finishing up" ;;
    *"Setting up"*|*"Checking"*|*"Linked"*|*"Created"*)
      echo "Configuring" ;;
    *) return 1 ;;
  esac
}

# Width of the indeterminate progress track, in cells, and of the fixed field
# the step label is drawn into beneath it.
_UPDATE_BAR_WIDTH=32
_UPDATE_STEP_FIELD=40

# Draw the progress block: a comet sweeping a track, then the current step.
# The bar is deliberately indeterminate — npm reports no percentage, and a fake
# one that jumps to 90% and sits there is worse than an honest sweep.
_render_update_bar() {
  local frame="$1" pal_len="$2"
  shift 2
  local -a palette=("$@")

  local seg=8 span=$(( _UPDATE_BAR_WIDTH + 8 ))
  local head=$(( frame % span - seg ))
  local i out=""
  for (( i = 0; i < _UPDATE_BAR_WIDTH; i++ )); do
    if (( i >= head && i < head + seg )); then
      local ramp=$(( (i - head) * pal_len / seg ))
      out+=$'\033[38;5;'"${palette[$ramp]}"'m━'
    else
      out+=$'\033[38;5;238m─'
    fi
  done
  printf '%s\033[0m' "$out"
}

# Render one frame of the update screen: the splash wordmark, then a title and
# a progress bar centred beneath it.
# Args: frame cols rows palette_override version step
render_update_frame() {
  local frame="$1" cols="${2:-80}" rows="${3:-24}"
  local palette_override="$4" version="$5" step="$6"

  local -a palette
  read -ra palette <<< "${palette_override:-$(get_tool_palette claude)}"
  local pal_len=${#palette[@]}
  local hi="${palette[$(( pal_len - 1 ))]}"

  # Four reserved rows: a blank gutter, then title / bar / step. The three are
  # centred on the same axis as the wordmark so the whole screen reads as one
  # stack rather than art with a footer stuck under it.
  local start_row art_height
  read -r start_row _ art_height _ \
    <<< "$(loading_art_geometry "$rows" "$cols" 4)"

  render_loading_frame "" "$frame" "$cols" "$rows" "$palette_override" 4

  local title title_cells
  if [ -n "$version" ]; then
    title="⇡  Updating to v${version}"
    # Counted in cells, not bytes: "⇡" plus two spaces is 3 and "Updating to v"
    # is 13, while ${#var} would count the arrow's three UTF-8 bytes under a C
    # locale and shift the whole line left.
    title_cells=$(( 3 + 13 + ${#version} ))
  else
    title="⇡  Updating Wisp Deck"
    title_cells=$(( 3 + 18 ))
  fi

  local title_row=$(( start_row + art_height + 1 ))
  local title_col=$(( (cols - title_cells) / 2 + 1 ))
  local bar_col=$(( (cols - _UPDATE_BAR_WIDTH) / 2 + 1 ))
  # The step is drawn into a fixed-width field, not centred on its own length:
  # the screen redraws in place and never clears, so a shorter step has to
  # overwrite the tails of the longer one it replaces.
  local field="$_UPDATE_STEP_FIELD"
  if (( ${#step} > field )); then field=${#step}; fi
  local field_col=$(( (cols - field) / 2 + 1 ))
  local lead=$(( (field - ${#step}) / 2 ))
  local trail=$(( field - lead - ${#step} ))
  if (( title_col < 1 )); then title_col=1; fi
  if (( bar_col < 1 )); then bar_col=1; fi
  if (( field_col < 1 )); then field_col=1; fi

  printf '\033[%d;%dH\033[1;38;5;%dm%s\033[0m' "$title_row" "$title_col" "$hi" "$title"
  printf '\033[%d;%dH' "$(( title_row + 1 ))" "$bar_col"
  _render_update_bar "$frame" "$pal_len" "${palette[@]}"
  printf '\033[%d;%dH\033[2;38;5;250m%*s%s%*s\033[0m' \
    "$(( title_row + 2 ))" "$field_col" "$lead" "" "$step" "$trail" ""
}

# Run one installer invocation behind the progress view, animating until it
# exits. Returns the installer's status; the caller owns the terminal.
# Args: log version palette_override [installer args...]
_run_update_behind_screen() {
  local log="$1" version="$2" pal_override="$3"
  shift 3

  local rows cols
  read -r rows cols <<< "$(_detect_term_size)"

  npx --yes wisp-deck@latest "$@" > "$log" 2>&1 &
  local pid=$!

  local frame=0 step="Installing files" line label
  while kill -0 "$pid" 2>/dev/null; do
    while IFS= read -r line; do
      # Condition form, not `a && b`: under `set -e` an unrecognised line would
      # make the list return 1 and kill the update mid-flight.
      if label="$(update_step_label "$line")"; then step="$label"; fi
    done < "$log"
    render_update_frame "$frame" "$cols" "$rows" "$pal_override" "$version" "$step"
    frame=$(( frame + 1 ))
    sleep 0.12
    read -r rows cols <<< "$(_detect_term_size)"
  done

  local rc=0
  wait "$pid" || rc=$?
  if [ "$rc" -ne 0 ]; then
    return "$rc"
  fi

  render_update_frame "$frame" "$cols" "$rows" "$pal_override" "$version" "Done, reopening menu"
  if [ -z "${WISP_DECK_UPDATE_NO_WAIT:-}" ]; then sleep 0.5; fi
  return 0
}

# Run the update behind a full-screen progress view. @latest forces npx past its
# cached tarball so the freshly published version installs even when an older
# one is cached; --update tells the installer this is not a first-time setup.
#
# The installer's output goes to a log, never to the screen: this runs straight
# after the menu drops its alt screen, so anything printed here lands on top of
# the leftover splash art and scrolls the window for the length of an npm
# install. The log is what the failure path shows.
# Args: [version] [palette_override]
run_wisp_deck_update() {
  local version="${1:-}" pal_override="${2:-}"
  local share_dir="${HOME}/.local/share/wisp-deck"
  local log="${share_dir}/update.log"

  mkdir -p "$share_dir"
  : > "$log"
  [ -n "$version" ] || version="$(get_update_version "$share_dir")"

  printf '\033[?25l\033[2J\033[3J\033[H'
  local rc=0
  _run_update_behind_screen "$log" "$version" "$pal_override" --update || rc=$?
  printf '\033[2J\033[3J\033[H\033[0m\033[?25h'
  if [ "$rc" -eq 0 ]; then
    return 0
  fi

  # --update only exists in installers from this version on. An install whose
  # lib/ is newer than the package @latest resolves to — a dev checkout running
  # off live symlinks, a cached or pinned tarball — is rejected outright for the
  # flag. That version is still perfectly installable, so fall back to the
  # flagless invocation the previous updater used rather than calling a working
  # update a failure. The terminal goes back to the user for it: the old
  # installer asks setup questions, and a prompt hidden behind the progress view
  # hangs with nothing on screen to explain it.
  if grep -q 'Unknown flag: --update' "$log" 2>/dev/null; then
    npx --yes wisp-deck@latest 2>&1 | tee "$log"
    rc="${PIPESTATUS[0]}"
    if [ "$rc" -eq 0 ]; then
      return 0
    fi
  fi

  printf '\n  \033[1;31m✗ Update failed\033[0m  (installer exited %d)\n\n' "$rc"
  printf '  Log: %s\n\n' "$log"
  local line
  while IFS= read -r line; do
    printf '  \033[2m│\033[0m %s\n' "$line"
  done < <(tail -n 8 "$log" 2>/dev/null)
  printf '\n  Press any key to return to the menu.\n'
  if [ -z "${WISP_DECK_UPDATE_NO_WAIT:-}" ] && [ -r /dev/tty ]; then
    read -rsn1 < /dev/tty
  fi
  return "$rc"
}
