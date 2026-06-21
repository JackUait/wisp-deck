#!/bin/bash
# shellcheck disable=SC2059  # Intentional: ANSI escape variables in printf format strings
# Compact view: a "changeset ledger" of working-tree changes instead of lazygit.
# Branch as a heading, net +/- stamp, aligned +/- columns with filenames.
# Refreshes every 2 seconds. Scroll with the mouse wheel, arrows/j/k,
# space/b (page), g/G (top/bottom) when the list overflows. Ctrl-C to exit.

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

# clamp_scroll keeps a scroll offset within [0, total - avail]. When the content
# fits (total <= avail) the result is 0.
# Usage: clamp_scroll <scroll> <total_lines> <viewport_lines>
clamp_scroll() {
  local scroll="$1" total="$2" avail="$3"
  local max=$((total - avail))
  [ "$max" -lt 0 ] && max=0
  [ "$scroll" -lt 0 ] && scroll=0
  [ "$scroll" -gt "$max" ] && scroll="$max"
  printf '%d' "$scroll"
}

# viewport_slice prints <count> lines of stdin starting after <scroll> lines,
# clipping at end-of-input. Line-based (no array indexing) so it behaves the
# same under bash and zsh.
# Usage: printf '%s\n' "$content" | viewport_slice <scroll> <count>
viewport_slice() {
  local start=$(($1 + 1)) end=$(($1 + $2))
  sed -n "${start},${end}p"
}

# scroll_status renders the bottom position indicator "first-last/total" with
# ↑/↓ arrows showing whether more content sits above/below the viewport.
# Usage: scroll_status <scroll> <viewport_lines> <total_lines>
scroll_status() {
  local scroll="$1" avail="$2" total="$3"
  local first=$((scroll + 1)) last=$((scroll + avail))
  [ "$last" -gt "$total" ] && last="$total"
  local up=" " down=" "
  [ "$scroll" -gt 0 ] && up="↑"
  [ "$last" -lt "$total" ] && down="↓"
  printf ' \033[2m%s%s %d-%d/%d\033[0m' "$up" "$down" "$first" "$last" "$total"
}

# enter_ui_mode prepares the live pane's terminal for the ledger UI: the
# ALTERNATE screen buffer (\033[?1049h) — which has NO scrollback, so the mouse
# wheel can't scroll past the rendered viewport into a pile of stale refresh
# frames — plus a hidden cursor and SGR mouse-wheel reporting. Emits nothing
# unless $1 is "1" (an interactive tty); the Go harness pipes stdin and must
# stay quiet so its output assertions hold.
# Usage: enter_ui_mode <interactive>
enter_ui_mode() {
  [ "$1" = 1 ] || return 0
  printf '\033[?1049h\033[?25l\033[?1000h\033[?1006h'
}

# exit_ui_mode reverses enter_ui_mode: disable mouse reporting, show the cursor,
# leave the alternate screen (restoring the user's shell view), and reset colors.
# Usage: exit_ui_mode <interactive>
exit_ui_mode() {
  [ "$1" = 1 ] || return 0
  printf '\033[?1000l\033[?1006l\033[?25h\033[?1049l\033[0m'
}

compact_view() {
  local project_dir="${1:-.}"

  # Need to be in a git repo
  if ! git -C "$project_dir" rev-parse --is-inside-work-tree &>/dev/null; then
    echo "Not a git repository"
    exec "$SHELL"
    return
  fi

  # Interactive only when stdin is a real terminal (the live tmux pane). The Go
  # test harness pipes stdin, so it falls through to the timed-refresh path.
  local interactive=0
  [ -t 0 ] && interactive=1

  # Put the tty in no-echo, char-at-a-time mode. Without this, scroll events that
  # arrive while the script is mid-render (between read_key calls) get echoed by
  # the line discipline as literal "[<65;40;18M" mouse-report text on screen.
  local saved_stty=""
  if [ "$interactive" = 1 ]; then
    saved_stty=$(stty -g 2>/dev/null || true)
    stty -echo -icanon 2>/dev/null || true
  fi

  # Switch into the alternate screen (no scrollback -> the wheel can't scroll
  # the list past its clamped viewport), hide the cursor, enable SGR mouse
  # reporting. The trap only flips a quit flag; the loop breaks on it and runs
  # cleanup ONCE afterwards. We must not `exit` from the trap: under zsh (the
  # pane's shell) an `exit` issued from a trap that interrupted `read -t` does
  # not terminate the script — the loop would keep running with echo re-enabled,
  # resurrecting the very leak this guards against.
  local _quit=0
  trap '_quit=1' INT TERM
  enter_ui_mode "$interactive"

  # read_key reads ONE keystroke into the global KEY within <timeout> seconds,
  # returning non-zero on timeout. bash and zsh spell single-char reads
  # differently (-n vs -k); IFS= preserves a literal space keypress.
  read_key() {
    KEY=''
    if [ -n "${ZSH_VERSION:-}" ]; then
      IFS= read -rs -t "$1" -k 1 KEY 2>/dev/null
    else
      IFS= read -rs -t "$1" -n 1 KEY 2>/dev/null
    fi
  }

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
  local w h content header body body_total avail mbtn
  local scroll=0
  local need_build=1
  local interval="${COMPACT_VIEW_INTERVAL:-2}"
  while true; do
    [ "$_quit" = 1 ] && break   # Ctrl-C / TERM -> leave the loop, then clean up
    # Capture pane width/height outside subshell.
    # tput may return wrong values in tmux; query tmux directly.
    if [ -n "${TMUX:-}" ] && command -v tmux &>/dev/null; then
      w=$(tmux display-message -p '#{pane_width}' 2>/dev/null || tput cols 2>/dev/null || echo 80)
      h=$(tmux display-message -p '#{pane_height}' 2>/dev/null || tput lines 2>/dev/null || echo 24)
    else
      w=$(tput cols 2>/dev/null || echo 80)
      h=$(tput lines 2>/dev/null || echo 24)
    fi
    [ -z "$h" ] && h=24
    [ "$h" -lt 4 ] && h=4

    # Rebuild the ledger only on a refresh tick; scroll keys just re-slice the
    # cached content so the wheel stays snappy (no git calls per keystroke).
    [ "$need_build" = 1 ] && content=$(
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

      # ── Header: branch heading with dimmed namespace + changed-file count and
      # net +/- stamp. This whole line (plus the separator below) is PINNED by
      # the renderer — it never scrolls — so the changeset size stays in view.
      local leaf="${branch##*/}"
      local ns=""
      [ "$leaf" != "$branch" ] && ns="${branch%/*}/"
      local total_files=$((n_staged + n_unstaged))
      local funit="files"
      [ "$total_files" -eq 1 ] && funit="file"
      local stamp=""
      if [ "$total_files" -gt 0 ]; then
        stamp="${total_files} ${funit}  +${ta} −${td}"
      fi
      # Active subscription/plan (PLAN control), shown inline after the branch so
      # the ledger always states which plan the session runs on. The " · "
      # separator is 3 columns wide; reserve them (plus the name) in the pad so
      # the right-aligned stamp can't collide with it.
      local plan="${GHOST_TAB_PLAN:-}"
      local plan_w=0
      [ -n "$plan" ] && plan_w=$(( ${#plan} + 3 ))

      # Right-align the stamp on the heading line when it fits.
      local headtext="${ns}${leaf}"
      local pad=$((iw - ${#headtext} - plan_w - ${#stamp}))
      printf " ${dim}%s${reset}${bold}${bright}%s${reset}" "$ns" "$leaf"
      [ -n "$ahead_behind" ] && printf "%s" "$ahead_behind"
      [ -n "$plan" ] && printf " ${dim}·${reset} ${dim}%s${reset}" "$plan"
      if [ -n "$stamp" ] && [ "$pad" -ge 1 ]; then
        printf '%*s' "$pad" ''
        printf "${dim}%s %s${reset}  ${green}+%s${reset} ${red}−%s${reset}" \
          "$total_files" "$funit" "$ta" "$td"
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

    # The header is the first 2 lines (branch heading + separator); it is PINNED
    # — always drawn at the top, never part of the scroll region. Only the body
    # (the file groups) scrolls beneath it.
    header=$(printf '%s\n' "$content" | sed -n '1,2p')
    body=$(printf '%s\n' "$content" | sed -n '3,$p')
    body_total=$(printf '%s\n' "$body" | wc -l | tr -d ' ')

    local body_rows=$((h - 2))
    [ "$body_rows" -lt 1 ] && body_rows=1
    # Reserve the last row for the position indicator when the body overflows.
    if [ "$body_total" -gt "$body_rows" ]; then
      avail=$((body_rows - 1))
      [ "$avail" -lt 1 ] && avail=1
    else
      avail="$body_rows"
    fi
    scroll=$(clamp_scroll "$scroll" "$body_total" "$avail")

    printf '\033[2J\033[H'
    printf '%s\n' "$header"
    if [ "$body_total" -le "$body_rows" ]; then
      printf '%s' "$body"
    else
      printf '%s\n' "$body" | viewport_slice "$scroll" "$avail"
      scroll_status "$scroll" "$avail" "$body_total"
    fi

    # Idle: just refresh on a timer. Interactive: refresh OR react to a key.
    if [ "$interactive" != 1 ]; then
      sleep "$interval"
      need_build=1
      continue
    fi

    if ! read_key "$interval"; then
      need_build=1   # timed out -> refresh the ledger
      continue
    fi
    need_build=0     # a key arrived -> re-slice cached content, stay snappy
    case "$KEY" in
      k) scroll=$((scroll - 1)) ;;
      j) scroll=$((scroll + 1)) ;;
      b) scroll=$((scroll - avail)) ;;
      ' ') scroll=$((scroll + avail)) ;;
      g) scroll=0 ;;
      G) scroll=$body_total ;;
      $'\e')
        # CSI sequence: arrows, page keys, or an SGR mouse-wheel report.
        read_key 0.02 || true
        if [ "$KEY" = "[" ]; then
          read_key 0.02 || true
          case "$KEY" in
            A) scroll=$((scroll - 1)) ;;
            B) scroll=$((scroll + 1)) ;;
            H) scroll=0 ;;
            F) scroll=$body_total ;;
            5) scroll=$((scroll - avail)); read_key 0.02 || true ;;  # PgUp + ~
            6) scroll=$((scroll + avail)); read_key 0.02 || true ;;  # PgDn + ~
            '<')
              # SGR mouse "<btn;col;rowM": wheel up=64, down=65.
              mbtn=""
              while read_key 0.05; do
                case "$KEY" in
                  M|m) break ;;
                  *) mbtn="$mbtn$KEY" ;;
                esac
              done
              case "${mbtn%%;*}" in
                64) scroll=$((scroll - 3)) ;;
                65) scroll=$((scroll + 3)) ;;
              esac
              ;;
          esac
        fi
        ;;
    esac
  done

  # Restore the terminal: leave the alternate screen, re-enable echo/cursor.
  exit_ui_mode "$interactive"
  [ -n "$saved_stty" ] && stty "$saved_stty" 2>/dev/null
  return 0
}
