#!/bin/bash
# shellcheck disable=SC2059  # Intentional: ANSI escape variables in printf format strings
# Compact view: a "changeset ledger" of working-tree changes instead of lazygit.
# Branch as a heading, net +/- stamp, aligned +/- columns with filenames.
# Refreshes every 2 seconds. Ctrl-C to exit.

# format_file shows the file BASENAME only (the path is dropped), truncating
# with an ellipsis when it exceeds max width.
# Usage: format_file <path> <max_width>
format_file() {
  local p="$1" max="$2"
  local fname="${p##*/}"
  if [ "${#fname}" -le "$max" ]; then
    printf '%s' "$fname"
  else
    printf '%.*s…' "$((max - 1))" "$fname"
  fi
}

# sum_numstat totals the added/deleted columns of `git --numstat` output read
# from stdin, treating binary markers ("-") as zero. Echoes "<added> <deleted>".
# Usage: printf '%s\n' "$numstat" | sum_numstat
sum_numstat() {
  local a=0 d=0 added deleted _rest
  while IFS=$'\t' read -r added deleted _rest; do
    [ -z "$added" ] && continue
    [ "$added" = "-" ] && added=0
    [ "$deleted" = "-" ] && deleted=0
    a=$((a + added))
    d=$((d + deleted))
  done
  printf '%d %d' "$a" "$d"
}

compact_view() {
  local project_dir="${1:-.}"

  # Need to be in a git repo
  if ! git -C "$project_dir" rev-parse --is-inside-work-tree &>/dev/null; then
    echo "Not a git repository"
    exec "$SHELL"
    return
  fi

  trap 'printf "\033[?25h\033[0m"; exit 0' INT TERM
  printf "\033[?25l" # hide cursor

  # ANSI helpers
  local dim="\033[90m"
  local bold="\033[1m"
  local cyan="\033[36m"
  local green="\033[32m"
  local red="\033[31m"
  local yellow="\033[33m"
  local bright="\033[97m"
  local reset="\033[0m"
  local dimline="\033[2m"

  # render_group prints a status group: a glyph header, then one row per file.
  # Each row leads with its own aligned "+NNN -NNN" columns, then the filename,
  # so on a narrow pane the numbers can never drift onto a neighbouring file.
  # Long filenames truncate at the right edge.
  # Usage: render_group <numstat text> <glyph color> <glyph> <label> <name_width> <count>
  render_group() {
    local data="$1" gcolor="$2" glyph="$3" label="$4" name_width="$5" count="$6"
    [ -z "$data" ] && return
    printf " ${gcolor}${bold}%s${reset} ${gcolor}%s${reset}  ${dim}(%s)${reset}\n" "$glyph" "$label" "$count"
    local added deleted file display pad_a pad_d
    while IFS=$'\t' read -r added deleted file; do
      [ -z "$added" ] && continue
      [ "$added" = "-" ] && added=0
      [ "$deleted" = "-" ] && deleted=0
      display=$(format_file "$file" "$name_width")
      # Right-align each number in a 4-wide cell (sign + up to 3 digits) so the
      # figures form a neat ledger column and the filename sits a tight 2 spaces
      # away — no trailing-space drift. The "−" is a format literal (not a %s
      # arg) to keep its multibyte width from skewing printf's padding.
      pad_a=$((3 - ${#added})); [ "$pad_a" -lt 0 ] && pad_a=0
      pad_d=$((3 - ${#deleted})); [ "$pad_d" -lt 0 ] && pad_d=0
      printf "   %*s${green}+%s${reset} %*s${red}−%s${reset}  ${bright}%s${reset}\n" \
        "$pad_a" '' "$added" "$pad_d" '' "$deleted" "$display"
    done <<< "$data"
    printf "\n"
  }

  # NOTE: declare loop-locals ONCE, before the loop. The pane runs this script
  # under zsh, where `local NAME` (no assignment) on an already-set variable is
  # a *display* command that prints "NAME=value" to stdout. Re-declaring `local
  # w` each iteration flashed "w=141" on screen until the next refresh.
  local w
  while true; do
    # Capture pane width outside subshell.
    # tput cols may return wrong value in tmux; query tmux directly.
    if [ -n "${TMUX:-}" ] && command -v tmux &>/dev/null; then
      w=$(tmux display-message -p '#{pane_width}' 2>/dev/null || tput cols 2>/dev/null || echo 80)
    else
      w=$(tput cols 2>/dev/null || echo 80)
    fi

    output=$(
      cd "$project_dir" || exit 1

      # Inner content width (2-space padding each side)
      local iw=$((w - 4))
      [ "$iw" -lt 20 ] && iw=20

      # Branch + ahead/behind
      local branch ahead_behind=""
      branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
      if git rev-parse '@{u}' &>/dev/null 2>&1; then
        local counts ahead behind
        counts=$(git rev-list --left-right --count "HEAD...@{u}" 2>/dev/null)
        if [ -n "$counts" ]; then
          ahead=$(echo "$counts" | cut -f1)
          behind=$(echo "$counts" | cut -f2)
          [ "$ahead" -gt 0 ] && ahead_behind=" ${cyan}↑${ahead}${reset}"
          [ "$behind" -gt 0 ] && ahead_behind="${ahead_behind} ${yellow}↓${behind}${reset}"
        fi
      fi

      # Gather data (tracked changes only — untracked files carry no +/- counts
      # and are excess for a line-change ledger, so they are intentionally omitted)
      local staged unstaged
      staged=$(git diff --cached --numstat 2>/dev/null)
      unstaged=$(git diff --numstat 2>/dev/null)

      # Count totals
      local n_staged=0 n_unstaged=0
      [ -n "$staged" ] && n_staged=$(echo "$staged" | wc -l | tr -d ' ')
      [ -n "$unstaged" ] && n_unstaged=$(echo "$unstaged" | wc -l | tr -d ' ')

      # Net line totals across tracked changes (the ledger "stamp")
      local sums ta td
      sums=$(printf '%s\n%s\n' "$staged" "$unstaged" | sum_numstat)
      ta=${sums% *}
      td=${sums#* }

      # Clear screen
      printf "\033[2J\033[H"

      # ── Header: branch heading with dimmed namespace + net stamp ──
      local leaf="${branch##*/}"
      local ns=""
      [ "$leaf" != "$branch" ] && ns="${branch%/*}/"
      local stamp=""
      if [ "$ta" -gt 0 ] || [ "$td" -gt 0 ]; then
        stamp="+${ta} −${td}"
      fi
      # Right-align the stamp on the heading line when it fits.
      local headtext="${ns}${leaf}"
      local pad=$((iw - ${#headtext} - ${#stamp}))
      printf " ${dim}%s${reset}${bold}${bright}%s${reset}" "$ns" "$leaf"
      [ -n "$ahead_behind" ] && printf "%s" "$ahead_behind"
      if [ -n "$stamp" ] && [ "$pad" -ge 1 ]; then
        printf '%*s' "$pad" ''
        printf "${green}+%s${reset} ${red}−%s${reset}" "$ta" "$td"
      fi
      printf "\n"

      # Separator line
      printf " ${dimline}"
      printf '%.*s' "$iw" '─'
      printf "${reset}\n"

      # Filenames follow the right-aligned "+NNN −NNN  " prefix:
      #   indent(3) + added cell(4) + 1 + deleted cell(4) + 2 spaces
      local name_width=$((iw - 14))
      [ "$name_width" -lt 8 ] && name_width=8

      local has_content=0
      [ -n "$staged" ] && has_content=1
      [ -n "$unstaged" ] && has_content=1

      # ── Staged ──
      render_group "$staged" "$green" "●" "staged" "$name_width" "$n_staged"

      # ── Modified ──
      render_group "$unstaged" "$yellow" "●" "modified" "$name_width" "$n_unstaged"

      # Empty state
      if [ "$has_content" -eq 0 ]; then
        printf " ${dim}no changes${reset}\n\n"
      fi
    )

    printf "%s" "$output"
    sleep "${COMPACT_VIEW_INTERVAL:-2}"
  done
}
