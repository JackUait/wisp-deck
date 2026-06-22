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

# numstat_path returns the working-tree path for a `git --numstat` third field.
# For renames git encodes it as "old => new" (or, when a common prefix/suffix is
# shared, the brace form "pre{old => new}suf"); a clicked rename must open the
# diff for its CURRENT path, so we resolve to the post-rename path. Plain paths
# pass through unchanged.
# Usage: numstat_path <numstat-path-field>
numstat_path() {
  local p="$1"
  case "$p" in
    *'{'*' => '*'}'*)
      # pre{old => new}suf  ->  pre + new + suf
      local pre post mid new
      pre="${p%%\{*}"
      post="${p#*\}}"
      mid="${p#*\{}"; mid="${mid%%\}*}"   # "old => new"
      new="${mid#* => }"
      p="${pre}${new}${post}"
      ;;
    *' => '*)
      p="${p#* => }"
      ;;
  esac
  printf '%s' "$p"
}

# body_path_map emits ONE line per rendered body line: the file's path on a file
# row, and an EMPTY line on a group-header or trailing-blank row. A click's
# body-line index is looked up against this map to find the path to diff. It
# MUST mirror render_group's line structure exactly — 1 header line + N file
# rows + 1 trailing blank per non-empty group, staged first then modified, or a
# single (empty-path) line for the "no changes" state — so the Nth map line
# always describes the Nth body line. Untracked files are omitted (as in the
# ledger). Renames resolve to their post-rename path via numstat_path.
# Usage: body_path_map <staged numstat> <unstaged numstat>
body_path_map() {
  local staged="$1" unstaged="$2"
  emit_group() {
    local data="$1"
    [ -z "$data" ] && return
    printf '\n'                       # group-header row -> no path
    local a d f
    while IFS=$'\t' read -r a d f; do
      [ -z "$a" ] && continue
      printf '%s\n' "$(numstat_path "$f")"
    done <<< "$data"
    printf '\n'                       # trailing blank row -> no path
  }
  emit_group "$staged"
  emit_group "$unstaged"
  if [ -z "$staged" ] && [ -z "$unstaged" ]; then
    printf '\n'                       # "no changes" row -> no path
  fi
}

# body_line_for_click maps a clicked SCREEN row to a 1-based body-line index, or
# 0 when the click landed on the pinned 2-row header, the bottom scroll-status
# row, or past the end of the content. The body viewport starts at screen row 3,
# so view-row = row - 2; with overflow the scroll offset is added.
# Usage: body_line_for_click <row> <scroll> <avail> <total>
body_line_for_click() {
  local row="$1" scroll="$2" avail="$3" total="$4"
  local vr=$((row - 2))                 # 1-based row within the body viewport
  { [ "$vr" -lt 1 ] || [ "$vr" -gt "$avail" ]; } && { printf 0; return; }
  local line=$((scroll + vr))
  { [ "$line" -lt 1 ] || [ "$line" -gt "$total" ]; } && { printf 0; return; }
  printf '%d' "$line"
}

# nth_line stores the Nth (1-based) line of a newline-delimited string in the
# global NTH_LINE, WITHOUT forking — no `sed`, no `$()` subshell. The hover hot
# path calls this per mouse-motion event (any-motion tracking floods events as
# the cursor moves), and the old `printf | sed -n` spawned a subprocess every
# time, so the highlight crawled behind the cursor. Empty lines are preserved;
# an out-of-range or non-positive index yields an empty NTH_LINE.
# Usage: nth_line <text> <n>   -> result in $NTH_LINE
nth_line() {
  NTH_LINE=""
  local n="$2" i=0 line
  [ "$n" -lt 1 ] && return 0
  while IFS= read -r line; do
    i=$((i + 1))
    if [ "$i" -eq "$n" ]; then NTH_LINE="$line"; return 0; fi
  done <<< "$1"
  return 0
}

# open_diff_popup floats a whole-window tmux popup showing the WHOLE-FILE diff
# (full context, -U999999) for the clicked path versus HEAD, piped through less.
# display-popup overlays the entire client window (not just this pane) and is
# blocking, so the ledger loop pauses until the user closes it (q/Esc). No-op
# when tmux is unavailable. The path is shell-quoted so spaces survive.
#
# Presentation: a rounded, ORANGE-bordered popup. The redundant header block
# (diff --git / index / --- / +++ and the @@ hunk line) is stripped — the
# filename already lives in the title — so file content starts at the top.
# Because -U999999 emits the whole file as a single hunk, dropping everything
# through the first @@ line removes the header exactly. The colored body is
# rendered by the ghost-tab-tui diff-view pager, which scrolls (arrows/jk, page,
# mouse wheel) and closes on a single Esc (or q) — something less can't do
# cleanly, since Esc is its command prefix.
# Usage: open_diff_popup <project_dir> <file>
open_diff_popup() {
  local dir="$1" file="$2"
  command -v tmux &>/dev/null || return 0
  local qd qf
  qd=$(printf '%q' "$dir")
  qf=$(printf '%q' "$file")

  # Print every line only AFTER the first @@ hunk header (which the @@ rule then
  # marks); the header line itself is not printed. /@@/ matches even when the
  # line is wrapped in ANSI color escapes.
  local strip="awk 'f;/@@/{f=1}'"

  tmux display-popup -E -w 90% -h 90% -b rounded -S 'fg=colour208' \
    -T " git diff: ${file} " \
    "git -C ${qd} --no-pager diff HEAD -U999999 --color=always -- ${qf} | ${strip} | ghost-tab-tui diff-view --title ${qf}"
}

# enter_ui_mode prepares the live pane's terminal for the ledger UI: the
# ALTERNATE screen buffer (\033[?1049h) — which has NO scrollback, so the mouse
# wheel can't scroll past the rendered viewport into a pile of stale refresh
# frames — plus a hidden cursor and SGR mouse reporting. ?1003h is any-motion
# tracking, so hover (no-button) motion reports arrive and the row under the
# cursor can be highlighted. Emits nothing unless $1 is "1" (an interactive
# tty); the Go harness pipes stdin and must stay quiet so its assertions hold.
# Usage: enter_ui_mode <interactive>
enter_ui_mode() {
  [ "$1" = 1 ] || return 0
  printf '\033[?1049h\033[?25l\033[?1000h\033[?1003h\033[?1006h'
}

# exit_ui_mode reverses enter_ui_mode: disable mouse reporting, show the cursor,
# leave the alternate screen (restoring the user's shell view), and reset colors.
# Usage: exit_ui_mode <interactive>
exit_ui_mode() {
  [ "$1" = 1 ] || return 0
  printf '\033[?1003l\033[?1000l\033[?1006l\033[?25h\033[?1049l\033[0m'
}

# highlight_body_line wraps the Nth (1-based) line of <body> in an SGR <style>
# (e.g. a background colour) to show it as the hovered file row. File rows carry
# their own \033[0m resets between the +/- columns and the name, so the style is
# re-asserted after every internal reset to keep spanning the whole row. When
# <width> > 0 the row is padded with spaces (inside the highlight) so the bar
# spans the full pane width — left edge to right edge — not just the text. Other
# lines pass through untouched; an out-of-range line index returns the body as-is.
# Usage: highlight_body_line <body> <line> <style> [width]
highlight_body_line() {
  local body="$1" ln="$2" style="$3" width="${4:-0}"
  if [ "$ln" -lt 1 ]; then printf '%s' "$body"; return; fi
  # Build the substitution strings as variables holding REAL ESC bytes. zsh (the
  # pane's shell) does NOT interpret a $'...' literal inside the replacement half
  # of ${var//pat/repl} — only a variable's value is used verbatim — so a $'\033'
  # written inline there would leak as literal "$'\033'" text on the hovered row.
  local esc=$'\033'
  local reset="${esc}[0m"
  local on="${esc}[${style}m"
  local reassert="${reset}${on}"   # each internal reset becomes reset+re-style
  local i=0 row out="" visible rest pre pad padlen
  while IFS= read -r row; do
    i=$((i + 1))
    if [ "$i" -eq "$ln" ]; then
      pad=""
      if [ "$width" -gt 0 ]; then
        # Visible width = the row with its ANSI escapes stripped. The +/- ledger
        # and names are single-column glyphs (the "−" sign included), so a
        # character count matches the rendered columns. Strip inline with pure
        # parameter expansion (no `sed` fork) — this runs on every hover redraw.
        visible=""; rest="$row"
        while [ -n "$rest" ]; do
          case "$rest" in
            *"${esc}["*)
              pre="${rest%%"${esc}["*}"   # text before the first ESC[
              visible="${visible}${pre}"
              rest="${rest#*"${esc}["}"    # drop up to and including ESC[
              rest="${rest#*m}"            # drop the SGR params up to 'm'
              ;;
            *) visible="${visible}${rest}"; rest="" ;;
          esac
        done
        padlen=$((width - ${#visible}))
        [ "$padlen" -lt 0 ] && padlen=0
        [ "$padlen" -gt 0 ] && pad=$(printf '%*s' "$padlen" '')
      fi
      row="${row//${esc}\[0m/${reassert}}"
      out="${out}${on}${row}${pad}${reset}"$'\n'
    else
      out="${out}${row}"$'\n'
    fi
  done <<< "$body"
  printf '%s' "${out%$'\n'}"   # drop the single trailing newline the loop added
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
  local w h content header body body_total avail mbtn draw_body
  local staged unstaged body_map
  local mterm mrest mrow bl cpath hv prev_hover
  local scroll=0
  local need_build=1
  local need_draw=1
  local hover_line=0
  # SGR background for the hovered file row (a subtle selection bar).
  local hover_style='48;5;238'
  local interval="${COMPACT_VIEW_INTERVAL:-2}"
  while true; do
    [ "$_quit" = 1 ] && break   # Ctrl-C / TERM -> leave the loop, then clean up
    # Capture pane width/height outside subshell.
    # tput may return wrong values in tmux; query tmux directly.
    if [ -n "${TMUX:-}" ] && command -v tmux &>/dev/null; then
      # Target THIS pane ($TMUX_PANE). Without -t, display-message reports the
      # *active* pane's size — and in ghost-tab the AI pane is active and far
      # wider than this (left) one, so the heading/separator got sized for the
      # wide pane and wrapped into extra rows here.
      w=$(tmux display-message -p -t "${TMUX_PANE:-}" '#{pane_width}' 2>/dev/null || tput cols 2>/dev/null || echo 80)
      h=$(tmux display-message -p -t "${TMUX_PANE:-}" '#{pane_height}' 2>/dev/null || tput lines 2>/dev/null || echo 24)
    else
      w=$(tput cols 2>/dev/null || echo 80)
      h=$(tput lines 2>/dev/null || echo 24)
    fi
    [ -z "$h" ] && h=24
    [ "$h" -lt 4 ] && h=4

    # Rebuild the ledger only on a refresh tick; scroll keys just re-slice the
    # cached content so the wheel stays snappy (no git calls per keystroke).
    if [ "$need_build" = 1 ]; then
    # Gather the tracked changes up here (not inside the render subshell) so the
    # SAME data feeds both the rendered body and the click→path map. Untracked
    # files carry no +/- counts and are intentionally omitted from the ledger.
    staged=$(git -C "$project_dir" diff --cached --numstat 2>/dev/null)
    unstaged=$(git -C "$project_dir" diff --numstat 2>/dev/null)
    # One path per body line, mirroring the render below. Captured (like the
    # body) via $(), so both shed their trailing blank identically and the Nth
    # map line keeps describing the Nth body line.
    body_map=$(body_path_map "$staged" "$unstaged")
    content=$(
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

      # Tracked changes ($staged/$unstaged) are gathered by the parent shell and
      # inherited here, so the render and the click→path map share one snapshot.

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
      # %b (not %s): ahead_behind carries the literal "\033[..." color escapes,
      # which printf only interprets in a format string / via %b — a plain %s
      # would leak them as visible "\033[36m↑1\033[0m" text on the branch line.
      [ -n "$ahead_behind" ] && printf '%b' "$ahead_behind"
      [ -n "$plan" ] && printf " ${dim}·${reset} ${dim}%s${reset}" "$plan"
      if [ -n "$stamp" ] && [ "$pad" -ge 1 ]; then
        printf '%*s' "$pad" ''
        printf "${dim}%s %s${reset}  ${green}+%s${reset} ${red}−%s${reset}" \
          "$total_files" "$funit" "$ta" "$td"
      fi
      printf "\n"

      # Separator line — a full-width horizontal rule. printf '%.*s' only
      # *truncates* the one-char string to the precision (it never repeats it),
      # so it must be emitted iw times; '─%.0s' prints the dash per seq arg.
      printf " ${dimline}"
      printf '─%.0s' $(seq 1 "$iw")
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
    fi

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

    # Keep the hover highlight only on an actual file row: drop it if the
    # changeset refreshed out from under it (line gone or no longer a file).
    if [ "$hover_line" -gt 0 ]; then
      nth_line "$body_map" "$hover_line"
      if [ "$hover_line" -gt "$body_total" ] || [ -z "$NTH_LINE" ]; then
        hover_line=0
      fi
    fi

    # Redraw only when something visible changed (need_draw). Hover motion that
    # lands on the same row leaves need_draw=0, so the screen doesn't flicker.
    if [ "$need_draw" = 1 ]; then
      draw_body=$(highlight_body_line "$body" "$hover_line" "$hover_style" "$w")
      printf '\033[2J\033[H'
      printf '%s\n' "$header"
      if [ "$body_total" -le "$body_rows" ]; then
        printf '%s' "$draw_body"
      else
        printf '%s\n' "$draw_body" | viewport_slice "$scroll" "$avail"
        scroll_status "$scroll" "$avail" "$body_total"
      fi
      need_draw=0
    fi

    # Idle: just refresh on a timer. Interactive: refresh OR react to a key.
    if [ "$interactive" != 1 ]; then
      sleep "$interval"
      need_build=1
      need_draw=1
      continue
    fi

    if ! read_key "$interval"; then
      need_build=1   # timed out -> refresh the ledger
      need_draw=1
      continue
    fi
    need_build=0     # a key arrived -> re-slice cached content, stay snappy
    prev_hover="$hover_line"
    hover_line=0     # most keys clear the hover; mouse motion (below) re-sets it
    need_draw=1
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
              # SGR mouse "<btn;col;rowM": wheel up=64, down=65, left button=0.
              # The terminator is M on press, m on release — capture it so a
              # left-click opens a file only on press (not again on release).
              mbtn=""; mterm=""
              while read_key 0.05; do
                case "$KEY" in
                  M) mterm=M; break ;;
                  m) mterm=m; break ;;
                  *) mbtn="$mbtn$KEY" ;;
                esac
              done
              case "${mbtn%%;*}" in
                64) scroll=$((scroll - 3)) ;;
                65) scroll=$((scroll + 3)) ;;
                32|33|34|35)
                  # Mouse motion (hover/drag, SGR adds 32 to the button code):
                  # highlight the file row under the cursor. Only real file rows
                  # highlight; if the hovered row didn't change, skip the redraw
                  # so the screen doesn't flicker on every motion event.
                  mrest="${mbtn#*;}"; mrow="${mrest#*;}"
                  bl=$(body_line_for_click "$mrow" "$scroll" "$avail" "$body_total")
                  hv=0
                  nth_line "$body_map" "$bl"
                  if [ "$bl" != 0 ] && [ -n "$NTH_LINE" ]; then
                    hv="$bl"
                  fi
                  hover_line="$hv"
                  [ "$hv" = "$prev_hover" ] && need_draw=0
                  ;;
                0)
                  # Left-click: map the report's row (the 3rd ";"-field of
                  # "btn;col;row") to a body line, then to a path, and float the
                  # whole-file diff over the window.
                  if [ "$mterm" = M ]; then
                    mrest="${mbtn#*;}"          # "col;row"
                    mrow="${mrest#*;}"          # "row"
                    bl=$(body_line_for_click "$mrow" "$scroll" "$avail" "$body_total")
                    if [ "$bl" != 0 ]; then
                      nth_line "$body_map" "$bl"; cpath="$NTH_LINE"
                      if [ -n "$cpath" ]; then
                        open_diff_popup "$project_dir" "$cpath"
                        enter_ui_mode "$interactive"   # re-assert alt-screen + mouse
                        need_build=1                   # redraw after the popup closes
                      fi
                    fi
                  fi
                  ;;
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
