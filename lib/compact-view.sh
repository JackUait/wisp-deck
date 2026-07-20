#!/bin/bash
# shellcheck disable=SC2059  # Intentional: ANSI escape variables in printf format strings
# Compact view: a "changeset ledger" of working-tree changes instead of lazygit.
# Net +/- stamp as the heading, aligned +/- columns with filenames. The branch
# name is deliberately absent — it lives in the Claude statusline.
# Refreshes every 2 seconds. Scroll with the mouse wheel, arrows/j/k,
# space/b (page), g/G (top/bottom) when the list overflows. Ctrl-C to exit.
# Hover a file row to reveal a checkbox (☐) at its left edge; click the box to
# mark the file (☑), or click a ☑ again to unmark it. With 1+ files marked a
# "[ discard N ]" button appears at the TOP of the list, next to the first group
# header (modified/new/…) — click it, then click [ yes ] to discard them (or [ no ]
# to cancel). The x/d/y/n keys still drive the same mark/discard flow.
# Click the account pill in the bottom bar (when 2+ Claude logins exist) to switch
# the active account mid-session; the AI pane relaunches under the new login.

# Mid-session account-switch helpers (the ledger's account pill + the AI-pane
# relaunch behind it) and their deps, sourced relative to THIS file so the pane's
# fresh shell — which sources only this script — has them. Guarded so a bare
# unit-test source missing a sibling still loads the ledger.
#
# The pane runs this under the user's $SHELL, which is ZSH — where BASH_SOURCE is
# empty, so it can't self-locate. wrapper.sh exports WISP_DECK_LIB_DIR (the lib
# path) into the pane env for exactly this; fall back to BASH_SOURCE when sourced
# from bash (wrapper's own load, the test harness).
_cv_lib_dir="${WISP_DECK_LIB_DIR:-${BASH_SOURCE[0]%/*}}"
for _cv_dep in theme statusline claude-accounts claude-configs tmux-session claude-shared-settings session-pool account-switch; do
  # shellcheck source=/dev/null
  [ -f "$_cv_lib_dir/$_cv_dep.sh" ] && source "$_cv_lib_dir/$_cv_dep.sh"
done
unset _cv_dep

# format_file shows the file BASENAME only (the path is dropped), truncating
# with an ellipsis when it exceeds max width. The result is BOTH printed and
# published in the global FORMAT_FILE: render_group calls this once PER FILE on
# every build tick, and a command-substitution capture there forked a subshell
# per file (~4ms each — over a second per tick at 300 files), so hot-path callers
# read the global instead (guarded by TestLedger_no_per_file_command_substitution).
# Usage: format_file <path> <max_width>   -> result also in $FORMAT_FILE
format_file() {
  local p="$1" max="$2"
  local fname="${p##*/}"
  if [ "${#fname}" -le "$max" ]; then
    FORMAT_FILE="$fname"
  else
    FORMAT_FILE="${fname:0:$((max - 1))}…"
  fi
  printf '%s' "$FORMAT_FILE"
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

# format_group_header prints a status group's heading: " <glyph> <label>  (n)".
# The label starts at column 3 (one space, the 1-wide glyph, one space), which is
# the column format_ledger_row lines its counts up under. Emits a trailing
# newline.
# Usage: format_group_header <glyph_color> <glyph> <label> <count>
format_group_header() {
  local gcolor="$1" glyph="$2" label="$3" count="$4"
  local bold="\033[1m" reset="\033[0m" dim="\033[90m"
  printf " ${gcolor}${bold}%s${reset} ${gcolor}%s${reset}  ${dim}(%s)${reset}\n" \
    "$glyph" "$label" "$count"
}

# format_ledger_row prints one file row of the change ledger: the "+added" and
# "−deleted" counts, then the (already display-formatted) filename. The "+count"
# begins at column 3 — the same column as a group's section label (see
# format_group_header) — so the figures sit directly beneath the section name.
# Each count is LEFT-aligned from that column inside a fixed 4-wide cell (sign +
# up to 3 digits), so the "−deleted" column and the filename stay aligned
# regardless of digit count. The "−" is a format literal (not a %s arg) to keep
# its multibyte width from skewing printf's padding. The optional <name_color>
# tints the filename with its section title's color (yellow under "modified",
# green under "staged", cyan under "new") so a section reads as one colored
# block; absent, the name stays bright white. Emits a trailing newline.
# Usage: format_ledger_row <added> <deleted> <display> [name_color]
format_ledger_row() {
  local added="$1" deleted="$2" display="$3"
  local green="\033[32m" red="\033[31m" bright="\033[97m" reset="\033[0m"
  local name_color="${4:-$bright}"
  local pad_a pad_d
  pad_a=$((3 - ${#added})); [ "$pad_a" -lt 0 ] && pad_a=0
  pad_d=$((3 - ${#deleted})); [ "$pad_d" -lt 0 ] && pad_d=0
  printf "   ${green}+%s${reset}%*s ${red}−%s${reset}%*s  ${name_color}%s${reset}\n" \
    "$added" "$pad_a" '' "$deleted" "$pad_d" '' "$display"
}

# human_bytes renders a non-negative byte count as a short human string: bytes
# below 1 KiB stay as "<n>B", then one decimal of KiB ("1.5KB") up to 1 MiB, then
# MiB ("2.5MB"). Pure integer math (no bc): the fraction is a rounded tenth of the
# unit, so 1536 -> "1.5KB". Emits the string with NO trailing newline.
# Usage: human_bytes <bytes>
human_bytes() {
  local n="$1" t
  if [ "$n" -lt 1024 ]; then
    printf '%dB' "$n"
  elif [ "$n" -lt 1048576 ]; then
    t=$(( (n * 10 + 512) / 1024 ))          # tenths of KiB, rounded
    printf '%d.%dKB' "$((t / 10))" "$((t % 10))"
  else
    t=$(( (n * 10 + 524288) / 1048576 ))    # tenths of MiB, rounded
    printf '%d.%dMB' "$((t / 10))" "$((t % 10))"
  fi
}

# format_ledger_size_row prints one file row of the change ledger for an IMAGE (or
# any binary file numstat can't line-count): instead of "+added −deleted" it shows
# the file's byte-SIZE change — green "+<size>" when it grew, red "−<size>" when it
# shrank, dim "±0" when unchanged. The signed figure occupies the SAME 9-column
# field the "+NNN −NNN" counts fill, so the filename still starts at column 14 and
# stays aligned with normal rows (see format_ledger_row). The sign is a single
# rune emitted as a literal (not a %s arg) to keep its multibyte width from
# skewing printf's padding. The optional <name_color> tints the filename with
# its section title's color (see format_ledger_row); absent, it stays bright
# white. Emits a trailing newline.
# Usage: format_ledger_size_row <old_bytes> <new_bytes> <display> [name_color]
format_ledger_size_row() {
  local old="$1" new="$2" display="$3"
  local green="\033[32m" red="\033[31m" dim="\033[90m" bright="\033[97m" reset="\033[0m"
  local name_color="${4:-$bright}"
  local diff=$((new - old)) sign mag color
  if [ "$diff" -gt 0 ]; then
    sign="+"; color="$green"; mag=$(human_bytes "$diff")
  elif [ "$diff" -lt 0 ]; then
    sign="−"; color="$red"; mag=$(human_bytes "$((-diff))")
  else
    sign="±"; color="$dim"; mag="0"
  fi
  # Visible width of the figure = 1 rune (sign) + the ASCII magnitude. Pad it out
  # to the 9-col count field so the filename lands at column 14.
  local pad=$(( 8 - ${#mag} )); [ "$pad" -lt 0 ] && pad=0
  printf "   ${color}%s%s${reset}%*s  ${name_color}%s${reset}\n" \
    "$sign" "$mag" "$pad" '' "$display"
}

# git_blob_size echoes the byte size of the blob at <rev>:<path> in <dir>, or 0
# when that revision has no such blob (e.g. a file added since HEAD). An empty
# <rev> means the index (git's ":path" form). Usage: git_blob_size <dir> <rev> <path>
git_blob_size() {
  git -C "$1" cat-file -s "$2:$3" 2>/dev/null || printf 0
}

# worktree_size echoes the on-disk byte size of <dir>/<path>, or 0 when the file
# is absent (e.g. a deleted image). Usage: worktree_size <dir> <path>
worktree_size() {
  if [ -f "$1/$2" ]; then
    wc -c < "$1/$2" | tr -d ' '
  else
    printf 0
  fi
}

# format_image_row renders an image file's ledger row as a byte-size delta,
# picking the before/after sizes from the group the image sits in:
#   staged    -> index blob   vs HEAD blob   (what staging would commit)
#   unstaged  -> working tree vs index blob  (the unstaged edit)
#   untracked -> working tree vs nothing     (a brand-new file: full-size add)
# <file> is the raw numstat path (a rename may arrive as "old => new"); sizes are
# looked up against its current path via numstat_path. The local is named `relpath`,
# NOT `path`: the pane runs under zsh, where `path` is a special array tied to
# $PATH, so a `local path=…` here would clobber the command search path and make
# wc/tr/git vanish. The optional <name_color> tints the filename with its
# section title's color (see format_ledger_row). Usage:
#   format_image_row <dir> <staged|unstaged|untracked> <file> <display> [name_color]
format_image_row() {
  local dir="$1" ref="$2" file="$3" display="$4" name_color="${5:-}"
  local relpath old=0 new=0
  numstat_path "$file" >/dev/null
  relpath="$NUMSTAT_PATH"
  case "$ref" in
    staged)
      old=$(git_blob_size "$dir" HEAD "$relpath")
      new=$(git_blob_size "$dir" "" "$relpath")
      ;;
    unstaged)
      old=$(git_blob_size "$dir" "" "$relpath")
      new=$(worktree_size "$dir" "$relpath")
      ;;
    untracked)
      old=0
      new=$(worktree_size "$dir" "$relpath")
      ;;
  esac
  format_ledger_size_row "$old" "$new" "$display" "$name_color"
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
# same under bash and zsh. Pure shell (no `sed`): this runs on the hover redraw
# path — one slice per repaint while the list overflows — and a `sed` fork there
# (~12ms) added to the per-motion-event cost that made the highlight crawl.
# Usage: printf '%s\n' "$content" | viewport_slice <scroll> <count>
viewport_slice() {
  local start=$(($1 + 1)) end=$(($1 + $2)) i=0 line
  while IFS= read -r line; do
    i=$((i + 1))
    [ "$i" -lt "$start" ] && continue
    [ "$i" -gt "$end" ] && break
    printf '%s\n' "$line"
  done
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
# pass through unchanged. The result is BOTH printed and published in the global
# NUMSTAT_PATH: body_path_map calls this once PER FILE on every build tick, and
# a command-substitution capture there forked a subshell per file, so hot-path
# callers use the direct call / the global instead (guarded by
# TestLedger_no_per_file_command_substitution).
# Usage: numstat_path <numstat-path-field>   -> result also in $NUMSTAT_PATH
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
  NUMSTAT_PATH="$p"
  printf '%s' "$p"
}

# untracked_numstat lists the repo's just-created files — untracked and NOT
# gitignored (git ls-files --others --exclude-standard) — as `git --numstat`-
# shaped rows, "<added>\t0\t<path>", so a brand-new file feeds the SAME ledger
# render and click→path map as a tracked change. A new file is all additions, so
# <added> is its line count, matching git numstat's own semantics: a missing
# trailing newline still counts the last line, and binary files (NUL byte in the
# first 8000 bytes, git's buffer_is_binary heuristic) emit "-". The NUL-delimited
# ls-files keeps paths with spaces or newlines intact.
# This runs on EVERY ledger refresh tick, so counting is one perl pass over all
# files — not a `git diff --no-index` per file, whose ~8ms spawn made the tick
# cost scale with the number of new files.
# Usage: untracked_numstat <project_dir>
untracked_numstat() {
  local dir="$1"
  git -C "$dir" ls-files --others --exclude-standard -z 2>/dev/null \
    | (
        cd "$dir" 2>/dev/null || exit 0
        perl -0 -ne '
          chomp;
          my $f = $_;
          my ($n, $bin, $last, $first) = (0, 0, "", 1);
          if (open my $fh, "<", $f) {
            binmode $fh;
            my $chunk;
            while (read($fh, $chunk, 65536)) {
              $bin = 1 if $first && index(substr($chunk, 0, 8000), "\0") >= 0;
              $first = 0;
              $n += ($chunk =~ tr/\n//);
              $last = substr($chunk, -1);
            }
            close $fh;
            $n++ if !$bin && $last ne "" && $last ne "\n";
          }
          print(($bin ? "-" : $n), "\t0\t", $f, "\n");
        ' 2>/dev/null
      )
}

# body_path_map emits ONE line per rendered body line: the file's path on a file
# row, and an EMPTY line on a group-header or trailing-blank row. A click's
# body-line index is looked up against this map to find the path to diff. It
# MUST mirror render_group's line structure exactly — 1 header line + N file
# rows + 1 trailing blank per non-empty group, staged first, then modified, then
# new (untracked), or a single (empty-path) line for the "no changes" state — so
# the Nth map line always describes the Nth body line. Renames resolve to their
# post-rename path via numstat_path. The untracked snapshot (from
# untracked_numstat) is optional so the two-arg tracked-only callers still work.
# Usage: body_path_map <staged numstat> <unstaged numstat> [untracked numstat]
body_path_map() {
  local staged="$1" unstaged="$2" untracked="$3"
  emit_group() {
    local data="$1"
    [ -z "$data" ] && return
    printf '\n'                       # group-header row -> no path
    local a d f
    while IFS=$'\t' read -r a d f; do
      [ -z "$a" ] && continue
      # Direct call (stdout is already this function's output) — a $() capture
      # here forked a subshell PER FILE, seconds per tick at a few hundred files.
      numstat_path "$f"
      printf '\n'
    done <<< "$data"
    printf '\n'                       # trailing blank row -> no path
  }
  emit_group "$staged"
  emit_group "$unstaged"
  emit_group "$untracked"
  if [ -z "$staged" ] && [ -z "$unstaged" ] && [ -z "$untracked" ]; then
    printf '\n'                       # "no changes" row -> no path
  fi
}

# ledger_wisp_colors echoes the clean-tree mascot's six 256-colour slots for a
# theme key: "<cap> <body> <lower> <feet> <blush> <pupil>". These are the MUTED
# (sleep) stops of each Go palette (internal/tui/theme.go: SleepCap,
# SleepPrimary, SleepDim, SleepDarkFeet, SleepBlush, EyePupil) — the same stops
# the sleeping splash ghost wears, so the wisp reads as that character at half
# scale. The full-saturation stops belong to the splash, where the mascot is
# the point.
# Usage: ledger_wisp_colors <theme_key>
ledger_wisp_colors() {
  case "${1:-}" in
    purple) echo "146 103 60 236 139 235" ;;
    green)  echo "151 65 22 236 174 235" ;;
    blue)   echo "153 67 24 236 174 235" ;;
    rose)   echo "182 132 95 236 174 235" ;;
    cyan)   echo "152 66 23 236 174 235" ;;
    teal)   echo "108 29 23 22 72 232" ;;
    *)      echo "180 166 130 94 168 232" ;; # orange (default)
  esac
}

# ledger_empty_state renders the clean-tree placeholder: a small, muted wisp
# centered in the body viewport with a caption beneath it. It replaces the bare
# " no changes" label that used to sit alone in the pane's top-left corner.
#
# This is the SHELL FALLBACK renderer (the live pane execs the Go ledger, which
# draws the same placeholder from internal/tui/ledger_empty.go) — keep the two
# in step.
#
# The art is a 14x12 pixel map — '.' is transparent, every letter is a colour
# slot. Half-block glyphs pack two pixel rows into each cell ('▀' carries the
# top pixel as its foreground and the bottom pixel as its background), so the
# wisp occupies 14 columns and 6 rows. A transparent pixel falls back to the
# terminal's default colour, letting the pane background show through instead of
# a painted box. A pane too narrow or too short for the art degrades to the
# caption alone rather than wrapping the mascot into a mangled smear.
#
# Runs inside the pane's ZSH: no unquoted word-splitting, no `read -a` (zsh
# spells it -A), no array indexing (zsh is 1-based, bash 0-based), and substring
# indexing only via ${var:off:len} (both shells).
# Usage: ledger_empty_state <pane_width> <viewport_rows> [theme_key]
ledger_empty_state() {
  local width="${1:-80}" rows="${2:-24}" theme="${3:-}"
  local caption="working tree clean"
  local art_w=14 art_rows=6
  # art + one blank spacer + caption
  local total=$((art_rows + 2))

  local dim=$'\033[2m' reset=$'\033[0m'

  # Caption-only fallback, itself vertically centered.
  if [ "$width" -lt $((art_w + 2)) ] || [ "$rows" -lt "$total" ]; then
    local cpad=$(( (width - ${#caption}) / 2 ))
    [ "$cpad" -lt 1 ] && cpad=1
    local cblank=$(( (rows - 1) / 2 ))
    while [ "$cblank" -gt 0 ]; do printf '\n'; cblank=$((cblank - 1)); done
    printf '%*s%s%s%s\n' "$cpad" '' "$dim" "$caption" "$reset"
    return 0
  fi

  local c_cap c_body c_low c_feet c_blush c_pupil
  read -r c_cap c_body c_low c_feet c_blush c_pupil <<< "$(ledger_wisp_colors "$theme")"

  local pad=$(( (width - art_w) / 2 ))
  local top=$(( (rows - total) / 2 ))
  while [ "$top" -gt 0 ]; do printf '\n'; top=$((top - 1)); done

  # The wisp is the sleeping splash ghost at half scale, trait for trait: pale
  # cap rim, primary→dim body split at eye level, single-pixel closed lids
  # (bottom half of their cell, so they render as gentle ▄ marks) with blush
  # beneath, and a dark feet band ending in scallops.
  # Slots: C cap rim, P upper body, D lower body, B feet, L blush, K closed lids.
  local -a art=(
    '....CCCCCC....'
    '..CPPPPPPPPC..'
    '.PPPPPPPPPPPP.'
    'PPPPPPPPPPPPPP'
    'PPPPPPPPPPPPPP'
    'DDKKDDDDDDKKDD'
    'DDLLDDDDDDLLDD'
    'DDDDDDDDDDDDDD'
    'DDDDDDDDDDDDDD'
    'DDDDDDDDDDDDDD'
    'BBBBBBBBBBBBBB'
    'BB.BBB..BBB.BB'
  )

  # Pair the pixel rows up front — walking them two at a time would need array
  # indexing, which is 1-based in zsh and 0-based in bash.
  local -a pairs=()
  local rowdef pending=""
  for rowdef in "${art[@]}"; do
    if [ -z "$pending" ]; then
      pending="$rowdef"
    else
      pairs+=("${pending}|${rowdef}")
      pending=""
    fi
  done

  local pair upper lower out j ch_t ch_b col_t col_b
  for pair in "${pairs[@]}"; do
    upper="${pair%%|*}"
    lower="${pair##*|}"
    out=""
    j=0
    while [ "$j" -lt "${#upper}" ]; do
      ch_t="${upper:$j:1}"
      ch_b="${lower:$j:1}"
      j=$((j + 1))
      # Slot -> colour. An empty result marks a transparent pixel.
      case "$ch_t" in
        C) col_t="$c_cap" ;; P) col_t="$c_body" ;; D) col_t="$c_low" ;; B) col_t="$c_feet" ;; L) col_t="$c_blush" ;; K) col_t="$c_pupil" ;; *) col_t="" ;;
      esac
      case "$ch_b" in
        C) col_b="$c_cap" ;; P) col_b="$c_body" ;; D) col_b="$c_low" ;; B) col_b="$c_feet" ;; L) col_b="$c_blush" ;; K) col_b="$c_pupil" ;; *) col_b="" ;;
      esac
      # Reset every cell: a background colour left set would bleed across the
      # transparent margin and draw a dark slab around the mascot.
      out="${out}${reset}"
      if [ -z "$col_t" ] && [ -z "$col_b" ]; then
        out="${out} "
      elif [ -n "$col_t" ] && [ -z "$col_b" ]; then
        out="${out}"$'\033[38;5;'"${col_t}m▀"
      elif [ -z "$col_t" ] && [ -n "$col_b" ]; then
        out="${out}"$'\033[38;5;'"${col_b}m▄"
      elif [ "$col_t" = "$col_b" ]; then
        out="${out}"$'\033[38;5;'"${col_t}m█"
      else
        out="${out}"$'\033[38;5;'"${col_t}m"$'\033[48;5;'"${col_b}m▀"
      fi
    done
    printf '%*s%s%s\n' "$pad" '' "$out" "$reset"
  done

  printf '\n'
  printf '%*s%s%s%s\n' "$(( (width - ${#caption}) / 2 ))" '' "$dim" "$caption" "$reset"
}

# header_rows_for converts the heading's visible column <width> and the pane
# <pane_width> into the number of SCREEN rows the pinned header occupies: the
# wrapped heading rows — ceil(width / pane_width), at least one — plus the
# one-row separator beneath it. A heading exactly as wide as the pane does NOT
# wrap (terminals park the cursor at the pending-wrap margin), so it stays one
# row. This is the single source of truth for the body's vertical offset once
# an over-wide heading can overflow a narrow pane.
# Usage: header_rows_for <visible_width> <pane_width>
# wrap_rows_for returns how many SCREEN rows a line of <vis> visible columns
# occupies in a pane <pw> columns wide. A line exactly as wide as the pane does
# NOT wrap (terminals hold a pending wrap at the last column until the next glyph
# prints), so the threshold is vis > pw. Always at least one row.
wrap_rows_for() {
  local vis="$1" pw="$2"
  [ "$pw" -lt 1 ] && pw=1
  local wrapped=$(( (vis + pw - 1) / pw ))
  [ "$wrapped" -lt 1 ] && wrapped=1
  printf '%d' "$wrapped"
}

header_rows_for() {
  printf '%d' "$(( $(wrap_rows_for "$1" "$2") + 1 ))"
}

# viewport_avail is the single source of truth for the scrollable body viewport
# height: the <body_rows> below the pinned header MINUS the SCREEN rows the bottom
# bar occupies. The bottom bar is NOT a fixed one row — a wide bar (a long
# account-pill label plus markers) wraps
# it onto extra rows (wrap_rows_for of its <bar_visible_width>) — so its TRUE
# height is reserved. Reserving only one row would let the composed frame exceed
# the pane, which SCROLLS the pane up and shifts every file row out from under the
# top-anchored hover mapping (vr = row - header_rows), lighting the WRONG file
# under the cursor. Always at least 1, so a degenerate (tiny) pane still renders a
# one-row viewport rather than a zero/negative one. The caller updates this only
# when the bar or pane changes, never on the per-motion hover hot path.
# Usage: viewport_avail <body_rows> <bar_visible_width> <pane_width>
viewport_avail() {
  local body_rows="$1" bar_vis="$2" w="$3" avail
  avail=$(( body_rows - $(wrap_rows_for "$bar_vis" "$w") ))
  [ "$avail" -lt 1 ] && avail=1
  printf '%d' "$avail"
}

# heading_layout decides where the pinned ledger heading's +/- stamp goes and how
# many SCREEN rows the heading spans (excluding the separator). The stamp (file
# count + net +/-) is a single block that is NEVER split: it sits right-aligned on
# the heading line when it fits, and when there is no room it moves WHOLE onto its
# own new row below — never dropped, never wrapped mid-block.
#
# The heading line is: leading space + headtext + marker (ab_vis) + plan. All
# three widths are 0 today — the branch name that used to lead the heading
# lives in the Claude statusline now — but the parameters keep the fit math
# general. The pane width <w> lets an over-wide stamp line count its own
# wrapping too.
#
# Echoes "<mode> <pad> <head_rows>":
#   mode      = inline (stamp on the heading line) | below (own new row) | none
#   pad       = right-align spaces before the stamp on its target row
#   head_rows = screen rows the heading occupies (excl. the separator)
# Usage: heading_layout <iw> <headtext_w> <ab_vis> <plan_w> <stamp_w> <w>
heading_layout() {
  local iw="$1" headtext_w="$2" ab_vis="$3" plan_w="$4" stamp_w="$5" w="$6"
  local head_vis=$((1 + headtext_w + ab_vis + plan_w))

  if [ "$stamp_w" -le 0 ]; then
    printf 'none 0 %s\n' "$(wrap_rows_for "$head_vis" "$w")"
    return 0
  fi

  # Try to place the stamp inline, right-aligned after the left run.
  local pad=$((iw - headtext_w - ab_vis - plan_w - stamp_w))
  if [ "$pad" -ge 1 ]; then
    # Fits: the heading line fills exactly 1 + iw columns.
    printf 'inline %s %s\n' "$pad" "$(wrap_rows_for $((1 + iw)) "$w")"
    return 0
  fi

  # No room on the heading line — move the whole stamp to its own row below,
  # LEFT-aligned (one leading space, under the heading line's left edge) like
  # text wrapping to a new line. The heading now spans the first row(s) plus
  # the stamp row(s).
  local below_pad=1
  local stamp_vis=$((below_pad + stamp_w))
  local rows=$(( $(wrap_rows_for "$head_vis" "$w") + $(wrap_rows_for "$stamp_vis" "$w") ))
  printf 'below %s %s\n' "$below_pad" "$rows"
}

# body_line_for_click maps a clicked SCREEN row to a 1-based body-line index, or
# 0 when the click landed on the pinned header, the bottom scroll-status row, or
# past the end of the content. The header occupies <header_rows> screen rows (2
# when the heading fits one line; more when an over-wide heading wraps), so
# the body viewport starts at screen row header_rows+1 and view-row = row -
# header_rows; with overflow the scroll offset is added. header_rows defaults to
# 2 for callers that predate wrap-aware headers.
# The result is BOTH printed (for $()-capturing callers and tests) AND stored in
# the global BODY_LINE, so the per-motion hover hot path can read it WITHOUT a $()
# subshell fork — that fork cost ~8ms under load and was a chief reason the
# selection bar crawled behind the cursor. Hot-path callers redirect stdout to
# /dev/null (a fork-free redirect) and read $BODY_LINE; see set_hover_from_row.
# The optional <col> <width> bound the hit horizontally: the outer tmux runs with
# mouse OFF, so the active ledger pane receives motion/click reports for the WHOLE
# terminal width — including cursor positions over the neighbouring AI pane. A
# report whose column falls outside this pane's width belongs to that other pane,
# not a hover/click on our list, so it yields 0. Omitting them keeps the historical
# row-only behaviour for callers/tests that predate the horizontal bound.
# Usage: body_line_for_click <row> <scroll> <avail> <total> [header_rows] [col] [width]
body_line_for_click() {
  local row="$1" scroll="$2" avail="$3" total="$4" header_rows="${5:-2}" col="${6:-}" width="${7:-}"
  local vr=$((row - header_rows))       # 1-based row within the body viewport
  BODY_LINE=0
  # Cursor is in another pane (column past this pane's edge) -> not our row.
  if [ -n "$col" ] && [ -n "$width" ]; then
    { [ "$col" -lt 1 ] || [ "$col" -gt "$width" ]; } && { printf 0; return; }
  fi
  { [ "$vr" -lt 1 ] || [ "$vr" -gt "$avail" ]; } && { printf 0; return; }
  local line=$((scroll + vr))
  { [ "$line" -lt 1 ] || [ "$line" -gt "$total" ]; } && { printf 0; return; }
  BODY_LINE="$line"
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

# split_content splits the rendered ledger <content> into the pinned HEADER
# (stamp heading + separator) and the scrollable BODY, and counts the body
# lines into BODY_TOTAL — all via globals, WITHOUT forking (no `sed`/`wc`). The
# content's FIRST line is metadata: the number of SCREEN rows the header occupies
# once the heading's wrapping is accounted for (see header_rows_for), captured
# into HEADER_ROWS so the body's vertical offset tracks a wrapped heading. Lines
# 2-3 are the two pinned visual lines (heading + separator); the rest is body.
# The refresh loop used to derive these with two `sed` calls plus `wc | tr` on
# EVERY iteration — including every mouse-motion event under any-motion tracking
# (~31ms/event) — so this is gated behind a build tick AND kept fork-free. The
# content is captured via $() upstream, so it carries no trailing blank line.
# Usage: split_content <content>   -> $HEADER, $BODY, $BODY_TOTAL, $HEADER_ROWS
split_content() {
  HEADER=""; BODY=""; BODY_TOTAL=0; HEADER_ROWS=2
  local i=0 line
  while IFS= read -r line; do
    i=$((i + 1))
    if [ "$i" -eq 1 ]; then
      HEADER_ROWS="$line"
    elif [ "$i" -le 3 ]; then
      if [ "$i" -eq 2 ]; then HEADER="$line"; else HEADER="${HEADER}"$'\n'"${line}"; fi
    else
      if [ "$BODY_TOTAL" -eq 0 ]; then BODY="$line"; else BODY="${BODY}"$'\n'"${line}"; fi
      BODY_TOTAL=$((BODY_TOTAL + 1))
    fi
  done <<< "$1"
  return 0
}

# should_discard reports (via exit status) whether the diff-view pager left the
# "discard" marker in its decision file — i.e. the user confirmed discarding the
# file. Exit 0 means discard; non-zero (missing/empty/other content) means no.
# Usage: should_discard <decision_file>
should_discard() {
  local file="$1"
  [ -f "$file" ] || return 1
  [ "$(cat "$file" 2>/dev/null)" = "discard" ]
}

# discard_worktree_file discards a file the way its state demands. A tracked file
# (in the index — including a staged-new one) has its working-tree changes
# restored from the index (git's own "discard changes in working directory"),
# leaving any staged copy intact. A just-created UNTRACKED file has nothing in git
# to restore FROM, so "discard" means undoing the creation: the file is removed
# (git clean). ls-files --error-unmatch exits 0 only when the path is tracked,
# which is how the two cases are told apart. Returns git's exit status.
# Usage: discard_worktree_file <project_dir> <file>
discard_worktree_file() {
  local dir="$1" file="$2"
  if git -C "$dir" ls-files --error-unmatch -- "$file" >/dev/null 2>&1; then
    git -C "$dir" restore -- "$file"
  else
    git -C "$dir" clean -fq -- "$file"
  fi
}

# ── Multi-select (batch discard) ────────────────────────────────────────────
# The user can mark several files in the ledger (hover a row, press 'x') and
# discard them together (press 'd', confirm y/n). The selection is a
# newline-delimited set of file PATHS — tracked by path, not by body-line index,
# so it survives the ledger's periodic rebuild and any scrolling. These helpers
# manage and render that set; the loop wires them to the x/d/y/n keys.

# toggle_selection echoes <selected> with <path> removed when it is already a
# member (whole-line match) or appended when it is not. Empty lines are dropped.
# Usage: toggle_selection <selected> <path>
toggle_selection() {
  local selected="$1" filepath="$2" out="" line found=0
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    if [ "$line" = "$filepath" ]; then found=1; continue; fi
    out="${out}${line}"$'\n'
  done <<< "$selected"
  [ "$found" -eq 0 ] && out="${out}${filepath}"$'\n'
  printf '%s' "${out%$'\n'}"
}

# selection_contains exits 0 when <path> is a whole-line member of <selected>,
# non-zero otherwise. Whole-line so "a.txt" never matches "src/a.txt": the set
# is newline-fenced on both sides and the path is matched between two newlines.
# A single quoted `case` glob (the path expansion is LITERAL when quoted, so
# glob characters in filenames can't misfire) — not a while-read loop, whose
# herestring wrote a temp file per call; apply_checkboxes calls this once per
# file row on the hover repaint path.
# Usage: selection_contains <selected> <path>
selection_contains() {
  case $'\n'"$1"$'\n' in
    *$'\n'"$2"$'\n'*) return 0 ;;
  esac
  return 1
}

# selection_count echoes the number of non-empty members of <selected>.
# Usage: selection_count <selected>
selection_count() {
  local selected="$1" line n=0
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    n=$((n + 1))
  done <<< "$selected"
  printf '%d' "$n"
}

# prune_selection echoes <selected> minus any member absent from the
# newline-delimited <valid> path set (a file that left the changeset — was
# discarded, committed, or reverted externally), keeping survivors in order.
# Usage: prune_selection <selected> <valid>
prune_selection() {
  local selected="$1" valid="$2" out="" line
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    if selection_contains "$valid" "$line"; then
      out="${out}${line}"$'\n'
    fi
  done <<< "$selected"
  printf '%s' "${out%$'\n'}"
}

# discard_prompt echoes the armed-confirm footer shown while a batch discard is
# pending: "Discard N file(s)? [ yes ] [ no ]" — singular "file" when N == 1. The
# yes/no are CLICKABLE buttons (the loop maps their column spans via
# visible_width); the y/n keys still work too.
# Usage: discard_prompt <count>
discard_prompt() {
  local n="$1" unit="files"
  [ "$n" = 1 ] && unit="file"
  local yellow="\033[33m" bold="\033[1m" reset="\033[0m" dim="\033[2m" green="\033[32m"
  printf "${yellow}${bold}Discard %s %s?${reset} ${dim}[${reset} ${green}yes${reset} ${dim}]${reset} ${dim}[${reset} no ${dim}]${reset}" \
    "$n" "$unit"
}

# apply_checkboxes draws a checkbox to the LEFT of each file row: a CHECKED box
# (☑, green) on a row whose mapped path (the SAME line index in <body_map>) is a
# member of <selected>, and an EMPTY box (☐, dim) on the row the cursor is over
# (<hover>, a 1-based body-line index; 0 = none). Selection wins when a row is
# both selected and hovered. The box replaces the row's 3-space indent with a
# 3-column " ☑ "/" ☐ ", preserving the row's VISIBLE width (3 columns in, 3 out)
# so column alignment and the hover-highlight padding math stay intact. Non-file
# rows (empty map line) and rows that are neither selected nor hovered pass
# through untouched. With nothing selected AND nothing hovered the body returns
# verbatim (the redraw fast path). Fork-free AND single-pass: body and map are
# read in LOCKSTEP (the map on fd 3), never via nth_line — whose front-to-back
# rescan per body line made this O(N²) with a herestring temp file per call
# (~10s per hover repaint at 300 files; the pane froze). Guarded by
# TestApplyCheckboxes_hover_stays_fast_with_many_files.
# Usage: apply_checkboxes <body> <body_map> <selected> [hover]
apply_checkboxes() {
  local body="$1" map="$2" selected="$3" hover="${4:-0}"
  [ -z "$selected" ] && [ "$hover" -lt 1 ] 2>/dev/null && { printf '%s' "$body"; return; }
  local checked=$'\033[1;32m ☑ \033[0m'
  local empty=$'\033[2m ☐ \033[0m'
  local out="" bline mline i=0 marker
  while IFS= read -r bline; do
    i=$((i + 1))
    # The map mirrors the body line-for-line; past its end the row is a
    # non-file row (an empty mapping, matching an out-of-range lookup).
    IFS= read -r mline <&3 || mline=""
    marker=""
    if [ -n "$mline" ]; then
      if selection_contains "$selected" "$mline"; then
        marker="$checked"
      elif [ "$i" -eq "$hover" ] 2>/dev/null; then
        marker="$empty"
      fi
    fi
    [ -n "$marker" ] && bline="${marker}${bline#   }"   # swap indent for the box
    out="${out}${bline}"$'\n'
  done <<< "$body" 3<<< "$map"
  printf '%s' "${out%$'\n'}"
}

# visible_width echoes the number of DISPLAY COLUMNS a string occupies: its ANSI
# escapes stripped (fork-free, the same parameter-expansion strip as
# highlight_body_line), then the remaining runes counted — every ledger glyph
# (↑ ↓ · − ☑ ☐) is one column, so a rune count matches the rendered width. Used
# to place the bottom bar's clickable spans (the discard button, the yes/no
# confirm) exactly under their drawn text.
# Usage: visible_width <string>
visible_width() {
  local esc=$'\033' visible="" rest="$1" pre
  while [ -n "$rest" ]; do
    case "$rest" in
      *"${esc}["*)
        pre="${rest%%"${esc}["*}"
        visible="${visible}${pre}"
        rest="${rest#*"${esc}["}"
        rest="${rest#*m}"
        ;;
      *) visible="${visible}${rest}"; rest="" ;;
    esac
  done
  printf '%d' "${#visible}"
}

# discard_button renders the clickable "[ discard N ]" affordance shown in the
# bottom bar when 1+ files are marked. It echoes the drawable (colored) string on
# the first line and its VISIBLE click width on the second (like account_pill),
# so the loop can map a click on that column span to arming the discard. With no
# files marked it draws NOTHING (empty string, width 0).
# Usage: discard_button <marked_count>   -> "<str>\n<width>"
discard_button() {
  local count="$1"
  if ! [ "$count" -gt 0 ] 2>/dev/null; then
    printf '\n0'
    return
  fi
  local dim=$'\033[2m' bold=$'\033[1m' reset=$'\033[0m'
  local vis="[ discard ${count} ]"
  printf '%s\n%d' \
    "${dim}[${reset} ${bold}discard ${count}${reset} ${dim}]${reset}" "${#vis}"
}

# discard_worktree_files reverts every member path of <selected> back to the
# index (see discard_worktree_file). Returns non-zero when ANY restore fails.
# Usage: discard_worktree_files <project_dir> <selected>
discard_worktree_files() {
  local dir="$1" selected="$2" line rc=0
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    discard_worktree_file "$dir" "$line" || rc=1
  done <<< "$selected"
  return "$rc"
}

# is_image_file reports (via exit status) whether <path> has an image extension
# the preview popup can decode — only the formats the Go stdlib ships decoders
# for (png/jpg/jpeg/gif), case-insensitive. Everything else (webp, svg, …)
# keeps the regular diff popup.
# Usage: is_image_file <path>
is_image_file() {
  case "$1" in
    *.[pP][nN][gG]|*.[jJ][pP][gG]|*.[jJ][pP][eE][gG]|*.[gG][iI][fF]) return 0 ;;
    *) return 1 ;;
  esac
}

# file_diff_command echoes the shell command whose output feeds the diff-view
# pager for <file>. A tracked file diffs against HEAD — its committed version. A
# just-created UNTRACKED file has no HEAD blob, so diffing against HEAD would emit
# nothing (a blank popup); it diffs against /dev/null via --no-index instead,
# rendering the whole new file as additions. ls-files --error-unmatch exits 0
# only for a tracked path, which distinguishes the two. Paths are %q-quoted so
# spaces survive the tmux command string; -U999999 gives full context and
# --color=never leaves coloring to the pager.
# Usage: file_diff_command <project_dir> <file>
file_diff_command() {
  local dir="$1" file="$2" qd qf
  qd=$(printf '%q' "$dir")
  qf=$(printf '%q' "$file")
  if git -C "$dir" ls-files --error-unmatch -- "$file" >/dev/null 2>&1; then
    printf 'git -C %s --no-pager diff HEAD -U999999 --color=never -- %s' "$qd" "$qf"
  else
    printf 'git -C %s --no-pager diff --no-index -U999999 --color=never -- /dev/null %s' "$qd" "$qf"
  fi
}

# open_diff_popup floats a whole-window tmux popup showing the clicked path's
# diff versus HEAD (or, for an untracked file, its whole content as additions). The whole file is fed to the pager (-U999999, full context),
# but the pager DEFAULTS to a changes-only view — just the changed lines plus a
# few lines of context, the rest collapsed behind a "⋯ N unchanged lines" marker
# — with a "Full" toggle (click or 'f') to reveal the whole file. Sending the
# full file keeps line numbers accurate and makes the toggle instant (no respawn).
# display-popup overlays the entire client window (not just this pane) and is
# blocking, so the ledger loop pauses until the user closes it (q/Esc). No-op
# when tmux is unavailable. The path is shell-quoted so spaces survive.
#
# Presentation: the popup runs FULL-SCREEN and BORDERLESS (-B). The rounded
# ORANGE-bordered modal box and its margin are drawn by the wisp-deck-tui
# diff-view pager itself — this is deliberate: tmux 3.6 swallows mouse clicks
# that land outside a sub-full-screen popup, so to make a click OUTSIDE the box
# close it, the pager must own the whole window and treat margin clicks as close.
# The redundant git header block (diff --git / index / --- / +++ and the @@ hunk
# line) is stripped so file content starts at the top; -U999999 emits the whole
# file as a single hunk, so dropping everything through the first @@ removes the
# header exactly. The pager's own header shows just the file path plus the
# added/deleted line counts; it scrolls (arrows/jk, page, mouse wheel) and closes
# on a single Esc, q, ctrl-c, or a click outside the box.
# Usage: open_diff_popup <project_dir> <file>
open_diff_popup() {
  local dir="$1" file="$2"
  command -v tmux &>/dev/null || return 0
  local qf qtool
  qf=$(printf '%q' "$file")
  # The pager themes its chrome (border, rule, tabs) from --ai-tool. Forward the
  # session's active tool (exported as WISP_DECK_TOOL) so OpenCode sessions get
  # the purple chrome; default to claude (the pager's own default) otherwise.
  qtool=$(printf '%q' "${WISP_DECK_TOOL:-claude}")

  # Print every line only AFTER the first @@ hunk header (which the @@ rule then
  # marks); the header line itself is not printed. /@@/ matches even when the
  # line is wrapped in ANSI color escapes.
  local strip="awk 'f;/@@/{f=1}'"

  # Snapshot the screen behind the popup so the pager can show it DIMMED in the
  # margin around the box. tmux freezes the panes under a popup, so this snapshot
  # (taken just before opening it) matches what's behind. Serialized as a "W H"
  # header then one "PANE <left> <top>" + captured-lines + "ENDPANE" block per
  # pane; ParseBackdrop composites it. Captured plain (no -e) — the pager dims it
  # uniformly. Best-effort: any failure just yields a blank margin.
  local backdrop backdrop_arg=""
  backdrop=$(mktemp "${TMPDIR:-/tmp}/gtdiff.XXXXXX" 2>/dev/null) || backdrop=""
  if [ -n "$backdrop" ]; then
    {
      tmux display-message -p -t "${TMUX_PANE:-}" '#{client_width} #{client_height}'
      tmux list-panes -t "${TMUX_PANE:-}" -F '#{pane_id} #{pane_left} #{pane_top}' 2>/dev/null |
        while read -r pid pleft ptop; do
          printf 'PANE %s %s\n' "$pleft" "$ptop"
          tmux capture-pane -p -t "$pid" 2>/dev/null
          printf 'ENDPANE\n'
        done
    } >"$backdrop" 2>/dev/null
    backdrop_arg="--backdrop-file $(printf '%q' "$backdrop")"
  fi

  # The pager's [ Discard ] button writes "discard" to this file when the user
  # confirms; we read it after the popup closes and run the git restore here (git
  # mutations stay in the bash layer). A failed mktemp just disables discard.
  local decision decision_arg=""
  decision=$(mktemp "${TMPDIR:-/tmp}/gtdiscard.XXXXXX" 2>/dev/null) || decision=""
  [ -n "$decision" ] && decision_arg="--discard-file $(printf '%q' "$decision")"

  # Full-screen (-w/-h 100%) and borderless (-B) so the pager owns the whole
  # window: it draws its own rounded orange box, shows the dimmed snapshot in the
  # margin, and closes when a click lands in that margin (tmux ignores clicks
  # outside a smaller popup). No -T title — the pager's header already shows the
  # path + added/deleted counts.
  # An image that exists on disk gets a PREVIEW instead of a binary diff: its
  # raw bytes are piped to the pager's --image mode (no diff, no header strip).
  # The header badge can't be derived from binary bytes, so it's passed in:
  # an untracked file is brand-new ("added"), a tracked one "modified". A
  # deleted image has no bytes to show and falls back to the diff pipeline.
  local popup
  if is_image_file "$file" && [ -f "$dir/$file" ]; then
    # NOT named "status": the ledger loop runs under zsh, where status is a
    # read-only alias for $? and a local assignment fatals the whole loop.
    local img_status=modified qd
    git -C "$dir" ls-files --error-unmatch -- "$file" >/dev/null 2>&1 || img_status=added
    qd=$(printf '%q' "$dir")
    # The pager overlays the real pixels via kitty-graphics APC escapes. tmux
    # 3.6 POPUPS swallow DCS passthrough (panes forward it; popups don't), so
    # the pager writes the graphics straight to the CLIENT tty — the popup is
    # full-screen, so its cells map 1:1 onto the outer terminal. Passthrough is
    # still enabled as the fallback channel for tmux versions whose popups
    # forward it (used when --gfx-tty is absent or unopenable).
    tmux set -g allow-passthrough all 2>/dev/null || true
    local client_tty gfx_arg=""
    client_tty=$(tmux display-message -p -t "${TMUX_PANE:-}" '#{client_tty}' 2>/dev/null || true)
    [ -n "$client_tty" ] && gfx_arg="--gfx-tty $(printf '%q' "$client_tty")"
    # --path carries the on-disk location (the pager only sees bytes on its
    # stdin) so the header can offer "Open in Preview".
    popup="cat ${qd}/${qf} | wisp-deck-tui diff-view --image --status ${img_status} --ai-tool ${qtool} --title ${qf} --path ${qd}/${qf} ${gfx_arg} ${backdrop_arg} ${decision_arg}"
  else
    local diffcmd
    diffcmd=$(file_diff_command "$dir" "$file")
    popup="${diffcmd} | ${strip} | wisp-deck-tui diff-view --ai-tool ${qtool} --title ${qf} ${backdrop_arg} ${decision_arg}"
  fi
  tmux display-popup -E -B -w 100% -h 100% "$popup"

  # The user confirmed a discard in the pager: revert the file's working-tree
  # changes now that the popup has closed.
  if [ -n "$decision" ] && should_discard "$decision"; then
    discard_worktree_file "$dir" "$file"
  fi

  [ -n "$backdrop" ] && rm -f "$backdrop"
  [ -n "$decision" ] && rm -f "$decision"
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

# gt_ledger_native_capable [cache_dir] — true iff the installed wisp-deck-tui
# supports the native `ledger` subcommand. Probing execs the Go binary (a
# Gatekeeper-assessed spawn) synchronously before the pane's first paint, so
# the verdict is cached keyed by the binary's path+mtime+size (the
# gt_claude_filter_prefix pattern): only the first launch after an
# install/update pays the probe.
gt_ledger_native_capable() {
  local cache_dir="${1:-${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck}"
  local bin meta sig cache_file cached
  bin="$(command -v wisp-deck-tui 2>/dev/null)" || return 1
  [ -n "$bin" ] || return 1
  meta="$(stat -f '%m-%z' "$bin" 2>/dev/null || stat -c '%Y-%s' "$bin" 2>/dev/null)"
  sig="${bin}@${meta}"
  cache_file="$cache_dir/ledger-native-capable"
  if [ -f "$cache_file" ]; then
    # Grouped: the -f check is a TOCTOU; losing the race must stay silent.
    { IFS= read -r cached < "$cache_file"; } 2>/dev/null || cached=""
    case "$cached" in
      "${sig}|1") return 0 ;;
      "${sig}|0") return 1 ;;
    esac
  fi
  mkdir -p "$cache_dir" 2>/dev/null
  if wisp-deck-tui ledger --help >/dev/null 2>&1; then
    printf '%s|1\n' "$sig" > "$cache_file" 2>/dev/null
    return 0
  fi
  printf '%s|0\n' "$sig" > "$cache_file" 2>/dev/null
  return 1
}

compact_view() {
  local project_dir="${1:-.}"

  # Need to be in a git repo
  if ! git -C "$project_dir" rev-parse --is-inside-work-tree &>/dev/null; then
    echo "Not a git repository"
    exec "$SHELL"
    return
  fi

  # Interactive panes use the native, viewport-bounded renderer when the
  # installed binary advertises it. Keep the shell implementation as a skew and
  # recovery fallback; piped unit fixtures remain on that deterministic path.
  local native_eligible=0
  if [ -t 0 ] || [ "${WISP_DECK_LEDGER_NATIVE_TEST:-}" = 1 ]; then
    native_eligible=1
  fi
  if [ "$native_eligible" = 1 ] \
     && [ "${WISP_DECK_LEDGER_SHELL_FALLBACK:-}" != 1 ] \
     && gt_ledger_native_capable; then
    local refresh_interval="${COMPACT_VIEW_INTERVAL:-2}"
    # The shell interval is historically a bare second count; Go durations
    # require a unit. Preserve explicitly-unitized values for diagnostics.
    case "$refresh_interval" in
      *[!0-9.]*) ;;
      *) refresh_interval="${refresh_interval}s" ;;
    esac
    local -a ledger_args
    ledger_args=(--ai-tool "${WISP_DECK_TOOL:-claude}" ledger "$project_dir")
    [ -n "${WISP_DECK_RELAUNCH_FILE:-}" ] \
      && ledger_args+=(--relaunch-file "$WISP_DECK_RELAUNCH_FILE")
    [ -n "${WISP_DECK_LIB_DIR:-}" ] \
      && ledger_args+=(--lib-dir "$WISP_DECK_LIB_DIR")
    ledger_args+=(--refresh-interval "$refresh_interval")
    exec wisp-deck-tui "${ledger_args[@]}"
  fi

  compact_view_shell "$project_dir"
}

# compact_view_shell is the compatibility renderer retained for older binaries,
# noninteractive fixtures, and WISP_DECK_LEDGER_SHELL_FALLBACK=1 recovery.
compact_view_shell() {
  local project_dir="${1:-.}"

  # Theme key for the clean-tree mascot, resolved ONCE (a settings read per
  # refresh tick would sit on the build path). Honors the Settings-menu preset,
  # falling back to the session tool's hue — mirroring the Go ledger, which
  # paints the same mascot from the resolved theme. Empty (→ the orange default)
  # when theme.sh isn't beside this file.
  local _cv_theme=""
  if declare -f gt_resolve_theme >/dev/null 2>&1; then
    local _cv_pref
    _cv_pref="$(grep '^theme=' "${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
    _cv_theme="$(gt_resolve_theme "$_cv_pref" "${WISP_DECK_TOOL:-}")"
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

  # set_hover_from_row maps a mouse report's screen <row> to the body line of the
  # file currently under the cursor (against the live scroll offset) and stores it
  # in hover_line — or 0 when the cursor isn't over a file row. BOTH wheel and
  # motion reports call it so the selection bar tracks the cursor consistently: a
  # wheel report carries its own cursor row, so re-deriving the hover on every
  # scroll step keeps the highlight ON the row under the cursor instead of blanking
  # it. Without this the bar flipped off on each wheel frame and back on at the
  # next motion frame — a visible blink while scrolling. Reads the loop-scope
  # scroll/avail/body_total/header_rows/body_map via dynamic scope.
  set_hover_from_row() {
    # $1 = cursor row, $2 = cursor column. The column bounds the hover to THIS
    # pane's width ($w): with the outer tmux mouse OFF, the active ledger receives
    # motion reports for the whole terminal, so a cursor over the AI pane to the
    # right carries a column past $w — passing it to body_line_for_click clears the
    # highlight instead of leaving the same-row file lit after the pointer left.
    # Fork-free: body_line_for_click stores its result in $BODY_LINE; the
    # >/dev/null swallows its (redundant) printf without spawning a subshell. A
    # $() here cost ~8ms PER motion event under load — the hover hot path runs
    # once per buffered report, so that fork is what made the highlight crawl.
    local b
    body_line_for_click "$1" "$scroll" "$avail" "$body_total" "$header_rows" "$2" "$w" >/dev/null
    b="$BODY_LINE"
    nth_line "$body_map" "$b"
    if [ "$b" != 0 ] && [ -n "$NTH_LINE" ]; then hover_line="$b"; else hover_line=0; fi
  }

  # set_pill_hover maps a mouse report's screen row/col to the account pill's click
  # span — the bottom bar (row $h), cols 1..account_pill_cols — and lights the pill
  # (pill_hover=1) while the pointer is over it, so the "switch account" button
  # reads as pressable; 0 otherwise (and always 0 when no pill is shown). Mirrors
  # the click test at the `0)` case; reads $h and the pill vars via dynamic scope.
  set_pill_hover() {
    if [ -n "$account_pill_str" ] && [ "$1" = "$h" ] \
       && [ "$2" -ge 1 ] && [ "$2" -le "$account_pill_cols" ]; then
      pill_hover=1
    else
      pill_hover=0
    fi
  }

  # build_bottom_bar composes the pane's bottom row into $bottom_bar and records
  # the clickable column spans on it (row $h). While a discard is armed the row is
  # the confirm — "Discard N file(s)? [ yes ] [ no ]" — and confirm_{yes,no}_*
  # bound its two buttons. Otherwise the row leads with the account pill (when
  # shown), then the ↑N/↓M push/pull marker, the scroll position (on overflow),
  # and — when 1+ files are marked — the right-hand "[ discard N ]" button, whose span is
  # discard_btn_start..discard_btn_end (0 when unmarked). Reads the loop-scope
  # state via dynamic scope; called on the draw path only when bar_dirty.
  build_bottom_bar() {
    discard_btn_start=0; discard_btn_end=0
    confirm_yes_start=0; confirm_yes_end=0; confirm_no_start=0; confirm_no_end=0
    discard_overlay=""
    local dot=$'\033[90m·\033[0m'
    # Bottom bar: the account pill (when shown), the upstream divergence (↑N
    # commits to push, ↓M to pull — the branch NAME lives in the Claude
    # statusline now), and the scroll position (on overflow). Every piece is
    # optional, so the dim-dot separator is added only between present pieces —
    # never dangling. The batch-discard button and its confirm no longer live
    # here — they overlay the TOP group header (see $discard_overlay below).
    local div=""
    if [ "$ahead" -gt 0 ] 2>/dev/null; then
      div="$(printf '\033[36m↑%s\033[0m' "$ahead")"
    fi
    if [ "$behind" -gt 0 ] 2>/dev/null; then
      [ -n "$div" ] && div="${div} ${dot} "
      div="${div}$(printf '\033[33m↓%s\033[0m' "$behind")"
    fi
    bottom_bar=""
    if [ -n "$account_pill_str" ]; then
      if [ "$pill_hover" = 1 ]; then
        bottom_bar="$account_pill_hover_str"
      else
        bottom_bar="$account_pill_str"
      fi
      [ -n "$div" ] && bottom_bar="${bottom_bar} ${dot} ${div}"
    elif [ -n "$div" ]; then
      bottom_bar=" ${div}"
    fi
    if [ "$body_total" -gt "$avail" ]; then
      if [ -n "$bottom_bar" ]; then
        bottom_bar="${bottom_bar} ${dot}$(scroll_status "$scroll" "$avail" "$body_total")"
      else
        bottom_bar="$(scroll_status "$scroll" "$avail" "$body_total")"
      fi
    fi

    # Top-of-list discard affordance: a string spliced onto the FIRST group header
    # (body line 1) so it sits next to that title. While a discard is armed it is
    # the inline confirm "Discard N file(s)? [ yes ] [ no ]"; otherwise, with 1+
    # files marked, the "[ discard N ]" button. The recorded click spans are COLUMN
    # spans on that line; the click handler gates them to screen row header_rows+1
    # at scroll 0 (the only place body line 1 is shown). The header text carries no
    # ANSI-free width surprise (visible_width strips its color), and a 2-space gap
    # separates it from the button/confirm.
    local w0
    w0=$(visible_width "${body%%$'\n'*}")
    if [ "$discard_armed" = 1 ]; then
      local dn unit pre base
      dn=$(selection_count "$discard_set")
      unit="files"; [ "$dn" = 1 ] && unit="file"
      pre="Discard ${dn} ${unit}? "
      # "[ yes ]" (7 cols), a space, "[ no ]" (6 cols) follow the plain-ASCII prefix,
      # which starts at column w0+3 (header width + the 2-space gap).
      base=$(( w0 + 2 ))
      confirm_yes_start=$(( base + ${#pre} + 1 )); confirm_yes_end=$(( base + ${#pre} + 7 ))
      confirm_no_start=$(( base + ${#pre} + 9 )); confirm_no_end=$(( base + ${#pre} + 14 ))
      discard_overlay="  $(discard_prompt "$dn")"
    else
      local mcount
      mcount=$(selection_count "$SELECTED")
      if [ "$mcount" -gt 0 ]; then
        local btn btn_str btn_w
        btn=$(discard_button "$mcount")
        btn_str="${btn%$'\n'*}"; btn_w="${btn##*$'\n'}"
        discard_btn_start=$(( w0 + 3 )); discard_btn_end=$(( w0 + 2 + btn_w ))
        discard_overlay="  ${btn_str}"
      fi
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
  # The header label and each row's "+NNN −NNN" counts share column 3, so the
  # figures line up directly beneath the section name (see format_group_header /
  # format_ledger_row). Long filenames truncate at the right edge.
  # An image row shows a byte-SIZE delta (format_image_row) instead of the
  # meaningless "+0 −0" numstat gives a binary file; <sizeref> tells it which
  # revisions to size (staged/unstaged/untracked). project_dir is in the enclosing
  # scope (this whole builder runs inside compact_view).
  # Usage: render_group <numstat text> <glyph color> <glyph> <label> <name_width> <count> <sizeref>
  render_group() {
    local data="$1" gcolor="$2" glyph="$3" label="$4" name_width="$5" count="$6" sizeref="$7"
    [ -z "$data" ] && return
    format_group_header "$gcolor" "$glyph" "$label" "$count"
    local added deleted file display
    while IFS=$'\t' read -r added deleted file; do
      [ -z "$added" ] && continue
      # Read the FORMAT_FILE global — a $() capture here forked a subshell PER
      # FILE on every build tick (over a second at a few hundred files).
      format_file "$file" "$name_width" >/dev/null
      display="$FORMAT_FILE"
      if is_image_file "$file"; then
        format_image_row "$project_dir" "$sizeref" "$file" "$display" "$gcolor"
      else
        [ "$added" = "-" ] && added=0
        [ "$deleted" = "-" ] && deleted=0
        format_ledger_row "$added" "$deleted" "$display" "$gcolor"
      fi
    done <<< "$data"
    printf "\n"
  }

  # NOTE: declare loop-locals ONCE, before the loop. The pane runs this script
  # under zsh, where `local NAME` (no assignment) on an already-set variable is
  # a *display* command that prints "NAME=value" to stdout. Re-declaring `local
  # w` each iteration flashed "w=141" on screen until the next refresh.
  local w h content header body body_total avail mbtn draw_body frame draw_first
  local header_rows=2
  local staged unstaged untracked body_map
  # ahead/behind + ab_counts are (re)assigned every build tick below. They
  # MUST be declared here, ONCE, not with an in-loop `local`: under zsh (the
  # pane's shell) `local NAME` without an assignment on an already-set variable is
  # a *display* command that dumps "NAME=value" to stdout — the bottom bar blinked
  # a raw `ab_counts=$'8\t0'` on every tick after the first (see the NOTE above).
  local ahead behind ab_counts
  local mterm mrest mcol mrow bl cpath prev_hover prev_scroll hover_keep
  local prev_pill_hover pill_hover_keep
  # Multi-select batch discard: SELECTED is a newline-delimited set of marked
  # file PATHS (tracked by path so it survives the ledger's rebuild/scroll).
  # discard_armed shows the y/n confirm footer; discard_set is what a confirm
  # will restore (the selection, or the hovered file when nothing is selected).
  # state_dirty forces a repaint when the selection/armed state changes without a
  # rebuild or hover/scroll change.
  local SELECTED="" discard_armed=0 discard_set="" state_dirty=0
  # Bottom bar, built in loop scope (not the frame subshell) so its clickable
  # spans are known to the click handler. bar_dirty rebuilds it only when its
  # inputs change (divergence/pill/scroll/selection/armed) — never on a hover-only
  # repaint, since the bar no longer carries a hover hint (the checkbox lives on
  # the file row instead). $discard_overlay is spliced onto the first group header
  # (body line 1) at draw time — the "[ discard N ]" button, or the inline confirm
  # while armed — so it rides the TOP of the list next to that title. discard_btn_*
  # is that button's click span (COLS on screen row header_rows+1 at scroll 0; 0
  # when unmarked); confirm_{yes,no}_* are the armed confirm's button spans (same
  # row). Declared ONCE (never an in-loop `local` — see the zsh NOTE).
  local bottom_bar="" bar_dirty=1 discard_overlay=""
  local discard_btn_start=0 discard_btn_end=0
  local confirm_yes_start=0 confirm_yes_end=0 confirm_no_start=0 confirm_no_end=0
  # Frame-erase helpers, built ONCE: $nl is a literal newline (the match), $rowend
  # is "erase-to-end-of-line + newline" (the replacement). The flicker-free redraw
  # swaps every newline in the composed frame for $rowend so each row scrubs the
  # previous frame's trailing text without a full-screen clear. Kept as variables
  # because zsh won't expand a $'...' escape in the replacement half of ${//}.
  local nl=$'\n'
  local rowend=$'\033[K\n'
  local scroll=0
  local need_build=1
  local need_draw=1
  local hover_line=0
  # Account pill (mid-session login switcher). Recomputed each build tick from the
  # relaunch-context file wrapper.sh writes for an eligible claude session (2+
  # logins, no rotation proxy). account_pill_str is the drawable pill for the
  # bottom bar; account_pill_cols is its click width. Both empty/0 when no pill.
  local account_pill_str="" account_pill_cols=0
  # account_pill_hover_str is the same pill rendered with its hover highlight, drawn
  # in place of account_pill_str while pill_hover=1 (the pointer is over the pill).
  # Precomputed on the build tick so the hover hot path only picks between strings.
  local account_pill_hover_str=""
  # 1 while the pointer sits over the account pill's click span (bottom bar, row h,
  # cols 1..account_pill_cols) — lights the pill so it reads as pressable.
  local pill_hover=0
  # Per-build pill scratch — declared ONCE here, never with an in-loop `local`
  # (under zsh, the pane's shell, `local NAME` without assignment on an already-set
  # var is a *display* command that leaks "NAME=value" onto the frame; see the
  # ab_counts NOTE above).
  local _pill_label _pill_color _pill_glyph _pill
  local _rc_relaunch="${WISP_DECK_RELAUNCH_FILE:-}"
  local _rc_pointer="" _rc_list="" _rc_colors="" _rc_default_label="" \
    _rc_config_pointer="" _rc_configs_list=""
  if [ -n "$_rc_relaunch" ] && [ -f "$_rc_relaunch" ] \
     && command -v _read_relaunch_ctx >/dev/null 2>&1; then
    # The account file paths live in the relaunch context, so the pill needs only
    # WISP_DECK_RELAUNCH_FILE in the pane env. (The unused _rc_* vars are set too.)
    local _rc_tool="" _rc_tool_cmd="" _rc_settings="" \
      _rc_filter="" _rc_project_dir="" _rc_accounts_dir=""
    _read_relaunch_ctx "$_rc_relaunch"
  fi
  # SGR background for the hovered file row (a subtle selection bar).
  local hover_style='48;5;238'
  local interval="${COMPACT_VIEW_INTERVAL:-2}"

  # handle_key processes the ONE input token already in $KEY — a scroll/jump key,
  # an arrow/page CSI, or a full SGR mouse report — mutating scroll / hover_line
  # (and, on a click, opening the diff popup and setting need_build). It does NOT
  # repaint: the caller drains a whole burst of buffered reports through it, then
  # repaints once for the settled state (see the coalescing loop below). Runs in
  # the loop's scope (dynamic scoping) so it reads/writes scroll, avail,
  # body_total, header_rows, body_map, hover_line, need_build, etc. directly.
  handle_key() {
    case "$KEY" in
      k) scroll=$((scroll - 1)) ;;
      j) scroll=$((scroll + 1)) ;;
      b) scroll=$((scroll - avail)) ;;
      ' ') scroll=$((scroll + avail)) ;;
      g) scroll=0 ;;
      G) scroll=$body_total ;;
      x)
        # Toggle selection of the file row under the cursor. The drain loop zeroed
        # hover_line at the top of this event; hover_keep still holds the row the
        # cursor was on, so use it (and restore hover_line so the bar stays put).
        hover_line="$hover_keep"
        if [ "$hover_line" -gt 0 ]; then
          nth_line "$body_map" "$hover_line"
          if [ -n "$NTH_LINE" ]; then
            SELECTED=$(toggle_selection "$SELECTED" "$NTH_LINE")
            state_dirty=1
          fi
        fi
        ;;
      d)
        # Arm (or, when already armed, cancel) a batch discard. The set to discard
        # is the selected files, or — when nothing is selected — the file under
        # the cursor, so 'd' is never a dead key over a hovered row.
        if [ "$discard_armed" = 1 ]; then
          discard_armed=0
        else
          hover_line="$hover_keep"
          discard_set="$SELECTED"
          if [ -z "$discard_set" ] && [ "$hover_line" -gt 0 ]; then
            nth_line "$body_map" "$hover_line"
            [ -n "$NTH_LINE" ] && discard_set="$NTH_LINE"
          fi
          # The confirm rides the top group header (shown only at scroll 0), so snap
          # back to the top when arming, so it's on screen no matter where we were.
          [ -n "$discard_set" ] && { discard_armed=1; scroll=0; }
        fi
        state_dirty=1
        ;;
      y)
        # Confirm an armed batch discard: restore every path in the set, clear the
        # selection, and rebuild so the reverted files leave the ledger.
        if [ "$discard_armed" = 1 ]; then
          discard_worktree_files "$project_dir" "$discard_set"
          SELECTED=""
          discard_armed=0
          discard_set=""
          need_build=1
        fi
        ;;
      n)
        # Cancel an armed batch discard (the selection is kept).
        [ "$discard_armed" = 1 ] && { discard_armed=0; state_dirty=1; }
        ;;
      $'\e')
        # CSI sequence: arrows, page keys, or an SGR mouse report. A terminal
        # sends the whole sequence atomically, so its bytes are already buffered
        # and these reads return instantly — the timeout only bounds the wait when
        # a byte is momentarily late. It is generous (0.5s, not the old 0.02s) so
        # heavy CPU load can't starve a read mid-report and corrupt the parse: a
        # truncated mouse report drops the row and blanks the hover (the selection
        # bar then blinks off mid-scroll). Once we've seen "\033[<" a full report
        # is guaranteed in-stream, so reading its body patiently is always correct.
        # The cost is nil in normal use (a lone, unbound ESC just waits 0.5s for a
        # follow-up that never comes — and nothing here is bound to a bare ESC).
        read_key 0.5 || true
        if [ "$KEY" != "[" ] && [ "$discard_armed" = 1 ]; then
          # Lone Esc (no CSI follow-up) cancels an armed batch discard.
          discard_armed=0
          state_dirty=1
        fi
        if [ "$KEY" = "[" ]; then
          read_key 0.5 || true
          case "$KEY" in
            A) scroll=$((scroll - 1)) ;;
            B) scroll=$((scroll + 1)) ;;
            H) scroll=0 ;;
            F) scroll=$body_total ;;
            5) scroll=$((scroll - avail)); read_key 0.5 || true ;;  # PgUp + ~
            6) scroll=$((scroll + avail)); read_key 0.5 || true ;;  # PgDn + ~
            '<')
              # SGR mouse "<btn;col;rowM": wheel up=64, down=65, left button=0.
              # The terminator is M on press, m on release — capture it so a
              # left-click opens a file only on press (not again on release).
              mbtn=""; mterm=""
              while read_key 0.5; do
                case "$KEY" in
                  M) mterm=M; break ;;
                  m) mterm=m; break ;;
                  *) mbtn="$mbtn$KEY" ;;
                esac
              done
              # Only act on a WELL-FORMED report: a complete "btn;col;row" (three
              # numeric fields) closed by a terminator. Under heavy CPU load a
              # report can arrive truncated or misaligned — its button field still
              # matches a wheel/motion below, but the row field is garbage, which
              # would drive a bogus row into set_hover_from_row and BLANK the
              # selection bar (a blink) or mis-scroll. A malformed report is
              # discarded and the hover is restored to its pre-event value, so a
              # dropped report leaves the highlight exactly where it was.
              local mouse_re='^[0-9]+;[0-9]+;[0-9]+$'
              if [ -z "$mterm" ] || ! [[ "$mbtn" =~ $mouse_re ]]; then
                hover_line="$hover_keep"
                pill_hover="$pill_hover_keep"
              else
              # Cursor col+row from "btn;col;row" — every position-bearing report
              # (wheel and motion alike) carries both, so extract them once. The
              # column bounds the hover/click to this pane (mouse is OFF in the
              # outer tmux, so the active ledger sees the whole terminal's width).
              mrest="${mbtn#*;}"; mcol="${mrest%%;*}"; mrow="${mrest#*;}"
              case "${mbtn%%;*}" in
                64|65)
                  # Wheel up=64 / down=65. Adjust and clamp the scroll, then
                  # re-derive the hover from THIS report's cursor row so the
                  # selection bar stays on the file under the cursor across the
                  # scroll (clamping first keeps the highlight aligned with the
                  # row that will actually be on screen).
                  if [ "${mbtn%%;*}" = 64 ]; then
                    scroll=$((scroll - 3))
                  else
                    scroll=$((scroll + 3))
                  fi
                  scroll=$(clamp_scroll "$scroll" "$body_total" "$avail")
                  set_hover_from_row "$mrow" "$mcol"
                  set_pill_hover "$mrow" "$mcol"
                  ;;
                32|33|34|35)
                  # Mouse motion (hover/drag, SGR adds 32 to the button code):
                  # highlight the file row under the cursor, and light the account
                  # pill when the pointer is over it. The coalescing loop repaints
                  # only if the SETTLED hover differs from what's on screen, so
                  # same-row motion is a no-op without a per-event check.
                  set_hover_from_row "$mrow" "$mcol"
                  set_pill_hover "$mrow" "$mcol"
                  ;;
                0)
                  # Left-PRESS handling (mterm M). The discard affordance rides the
                  # TOP group header — screen row header_rows+1, shown only at scroll
                  # 0 — so its clicks are gated to that row. While a discard is armed
                  # that row is the confirm, so a click there hits its [ yes ] / [ no ]
                  # buttons and nothing else acts. Otherwise: the account pill (bottom
                  # bar) opens the login switcher; the "[ discard N ]" button arms the
                  # confirm; a click in a file row's CHECKBOX slot (cols 1-3, the left
                  # indent) toggles that file's mark; a click anywhere else on a file
                  # row floats its whole-file diff.
                  local top_row=$(( header_rows + 1 ))
                  if [ "$mterm" != M ]; then
                    :   # release — ignore (open on press only)
                  elif [ "$discard_armed" = 1 ]; then
                    if [ "$scroll" -eq 0 ] && [ "$mrow" -eq "$top_row" ] \
                       && [ "$mcol" -ge "$confirm_yes_start" ] && [ "$mcol" -le "$confirm_yes_end" ]; then
                      # Click [ yes ]: run the batch discard (mirrors the 'y' key).
                      discard_worktree_files "$project_dir" "$discard_set"
                      SELECTED=""; discard_armed=0; discard_set=""; need_build=1
                    elif [ "$scroll" -eq 0 ] && [ "$mrow" -eq "$top_row" ] \
                       && [ "$mcol" -ge "$confirm_no_start" ] && [ "$mcol" -le "$confirm_no_end" ]; then
                      # Click [ no ]: cancel the confirm (mirrors 'n'); selection kept.
                      discard_armed=0; state_dirty=1
                    fi
                  elif [ -n "$account_pill_str" ] \
                     && [ "$mrow" = "$h" ] && [ "$mcol" -le "$account_pill_cols" ]; then
                    # Re-read the switcher lib from disk so edits to the popup
                    # (size, flags) go live without restarting this long-running
                    # ledger — otherwise the resident function lags behind the file.
                    reload_switcher_lib "$_cv_lib_dir"
                    open_account_switcher tmux "$_rc_relaunch"
                    enter_ui_mode "$interactive"   # re-assert alt-screen + mouse
                    need_build=1                   # redraw + refresh the pill
                  elif [ "$discard_btn_start" -gt 0 ] && [ "$scroll" -eq 0 ] \
                     && [ "$mrow" -eq "$top_row" ] \
                     && [ "$mcol" -ge "$discard_btn_start" ] && [ "$mcol" -le "$discard_btn_end" ]; then
                    # Click [ discard N ]: arm the confirm over the marked set
                    # (mirrors the 'd' key).
                    discard_set="$SELECTED"; discard_armed=1; state_dirty=1
                  else
                    bl=$(body_line_for_click "$mrow" "$scroll" "$avail" "$body_total" "$header_rows" "$mcol" "$w")
                    if [ "$bl" != 0 ]; then
                      nth_line "$body_map" "$bl"; cpath="$NTH_LINE"
                      if [ -n "$cpath" ]; then
                        if [ "$mcol" -le 3 ]; then
                          # Checkbox slot (left indent): toggle this file's mark
                          # instead of opening its diff (mirrors the 'x' key).
                          SELECTED=$(toggle_selection "$SELECTED" "$cpath")
                          state_dirty=1
                        else
                          open_diff_popup "$project_dir" "$cpath"
                          enter_ui_mode "$interactive"   # re-assert alt-screen + mouse
                          need_build=1                   # redraw after the popup closes
                        fi
                      fi
                    fi
                  fi
                  ;;
              esac
              fi
              ;;
          esac
        fi
        ;;
    esac
  }

  while true; do
    [ "$_quit" = 1 ] && break   # Ctrl-C / TERM -> leave the loop, then clean up
    # Capture pane width/height — but ONLY on a build tick, never per keystroke.
    # Each `tmux display-message` is a fork+exec round-trip to the tmux server
    # (~11ms); querying both dimensions on EVERY mouse-motion event (any-motion
    # tracking floods them as the cursor moves) added ~23ms/event and made the
    # hover highlight crawl. The pane size only changes on a resize, which raises
    # SIGWINCH — that interrupts read_key, so the loop falls through to
    # need_build=1 and re-measures promptly without polling every event.
    if [ "$need_build" = 1 ] || [ -z "${w:-}" ]; then
      # tput may return wrong values in tmux; query tmux directly when attached.
      if [ -n "${TMUX:-}" ] && command -v tmux &>/dev/null; then
        # Target THIS pane ($TMUX_PANE). Without -t, display-message reports the
        # *active* pane's size — and in wisp-deck the AI pane is active and far
        # wider than this (left) one, so the heading/separator got sized for the
        # wide pane and wrapped into extra rows here.
        w=$(tmux display-message -p -t "${TMUX_PANE:-}" '#{pane_width}' 2>/dev/null || tput cols 2>/dev/null || echo 80)
        h=$(tmux display-message -p -t "${TMUX_PANE:-}" '#{pane_height}' 2>/dev/null || tput lines 2>/dev/null || echo 24)
      else
        # Ask the TERMINAL, not terminfo. `tput cols/lines` reports the static
        # xterm size (80x24) whenever it cannot read the tty — it then paints an
        # 80-wide ledger into a narrower pane and, worse, puts the account pill's
        # click target on row 24 of a pane that has fewer rows, so the pill can
        # never be hovered or clicked. stty reads the real size off the tty.
        local _sz=""
        _sz="$(stty size 2>/dev/null </dev/tty)" || _sz=""
        # stty prints "<rows> <cols>"; anything else (no tty, a 0 dimension)
        # means it could not measure, so fall back to terminfo's guess.
        case "$_sz" in
          [1-9]*' '[1-9]*)
            h="${_sz%% *}"
            w="${_sz##* }"
            ;;
          *)
            w=$(tput cols 2>/dev/null || echo 80)
            h=$(tput lines 2>/dev/null || echo 24)
            ;;
        esac
      fi
    fi
    [ -z "$h" ] && h=24
    [ "$h" -lt 4 ] && h=4

    # Rebuild the ledger only on a refresh tick; scroll keys just re-slice the
    # cached content so the wheel stays snappy (no git calls per keystroke).
    if [ "$need_build" = 1 ]; then
    # Gather the changes up here (not inside the render subshell) so the SAME data
    # feeds both the rendered body and the click→path map. Just-created files are
    # untracked, so git diff never lists them — untracked_numstat enumerates them
    # separately (as +N −0 rows) into the "new" group below.
    staged=$(git -C "$project_dir" diff --cached --numstat 2>/dev/null)
    unstaged=$(git -C "$project_dir" diff --numstat 2>/dev/null)
    untracked=$(untracked_numstat "$project_dir")
    # One path per body line, mirroring the render below. Captured (like the
    # body) via $(), so both shed their trailing blank identically and the Nth
    # map line keeps describing the Nth body line.
    body_map=$(body_path_map "$staged" "$unstaged" "$untracked")
    # Upstream divergence, gathered ONCE here (on the build tick, not the hover
    # hot path): ahead (commits to push) and behind (commits to pull) feed the
    # bottom bar's ↑N/↓M marker, built outside the content subshell. The branch
    # NAME is not gathered at all — it lives in the Claude statusline now.
    ahead=0; behind=0
    if git -C "$project_dir" rev-parse '@{u}' &>/dev/null 2>&1; then
      ab_counts=$(git -C "$project_dir" rev-list --left-right --count "HEAD...@{u}" 2>/dev/null)
      if [ -n "$ab_counts" ]; then
        ahead=$(echo "$ab_counts" | cut -f1)
        behind=$(echo "$ab_counts" | cut -f2)
      fi
    fi
    content=$(
      cd "$project_dir" || exit 1

      # Inner content width (2-space padding each side)
      local iw=$((w - 4))
      [ "$iw" -lt 20 ] && iw=20

      # The changes ($staged/$unstaged/$untracked) are gathered by the parent
      # shell and inherited here, so the render and the click→path map share one
      # snapshot.

      # Count totals
      local n_staged=0 n_unstaged=0 n_untracked=0
      [ -n "$staged" ] && n_staged=$(echo "$staged" | wc -l | tr -d ' ')
      [ -n "$unstaged" ] && n_unstaged=$(echo "$unstaged" | wc -l | tr -d ' ')
      [ -n "$untracked" ] && n_untracked=$(echo "$untracked" | wc -l | tr -d ' ')

      # Net line totals across all changes (the ledger "stamp"); a new file's
      # every line counts as an addition, so untracked rows feed the +total too.
      local sums ta td
      sums=$(printf '%s\n%s\n%s\n' "$staged" "$unstaged" "$untracked" | sum_numstat)
      ta=${sums% *}
      td=${sums#* }

      # ── Header: the changed-file count + net +/- stamp, right-aligned,
      # owning the row alone. The branch name lives in the Claude statusline,
      # the push/pull counts stay at the bottom bar, and the subscription/plan
      # lives on the account pill there, so all three are deliberately absent
      # here. This whole line (plus the separator below) is PINNED by the
      # renderer — it never scrolls — so the changeset size stays in view.
      local total_files=$((n_staged + n_unstaged + n_untracked))
      local funit="files"
      [ "$total_files" -eq 1 ] && funit="file"
      local stamp=""
      if [ "$total_files" -gt 0 ]; then
        stamp="${total_files} ${funit}  +${ta} −${td}"
      fi
      # Place the stamp with heading_layout: right-aligned on the heading line
      # when it fits (mode inline), else moved WHOLE onto its own new row below
      # (mode below) — the +/- block is never split across rows. The heading's
      # left run is empty (no branch, no marker, no plan), so headtext width is
      # 0. The returned head_rows counts every screen row the pinned heading
      # spans; +1 for the separator. Emit that total as the content's first
      # line for split_content; the renderer/click math read the pinned-header
      # offset from there so clicks/hover map to the right file row.
      local mode pad head_rows
      read -r mode pad head_rows <<< "$(heading_layout "$iw" 0 0 0 "${#stamp}" "$w")"
      printf '%s\n' "$((head_rows + 1))"

      # The leading space heading_layout's +1 accounts for.
      printf ' '
      # A newline before the stamp when it lives on its own row (mode below).
      [ "$mode" = "below" ] && printf "\n"
      if [ "$mode" = "inline" ] || [ "$mode" = "below" ]; then
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
      [ -n "$untracked" ] && has_content=1

      # ── Staged ──
      render_group "$staged" "$green" "●" "staged" "$name_width" "$n_staged" "staged"

      # ── Modified ──
      render_group "$unstaged" "$yellow" "●" "modified" "$name_width" "$n_unstaged" "unstaged"

      # ── New (untracked, just-created files) ──
      render_group "$untracked" "$cyan" "●" "new" "$name_width" "$n_untracked" "untracked"

      # Empty state: the mascot placeholder, centered in the body viewport. Its
      # height must match what the renderer will reserve, so mirror that math
      # here — the pinned header is head_rows+1 rows and the bottom bar takes
      # one (a wrapped bar costs a row, which just nudges the art up by one).
      if [ "$has_content" -eq 0 ]; then
        local empty_rows=$((h - head_rows - 2))
        [ "$empty_rows" -lt 1 ] && empty_rows=1
        ledger_empty_state "$w" "$empty_rows" "$_cv_theme"
      fi
    )
    # The header is the stamp heading + separator; it is PINNED — always drawn
    # at the top, never part of the scroll region. Only the body (the file groups)
    # scrolls beneath it. An over-wide heading can WRAP onto extra screen
    # rows, so header_rows (≥2) — not a fixed 2 — is the body's vertical offset.
    # Derived ONCE per build (fork-free, via split_content) — NOT per keystroke:
    # the old per-iteration `sed`×2 + `wc|tr` cost ~31ms on every mouse-motion
    # event and helped the hover highlight crawl.
    split_content "$content"
    header="$HEADER"; body="$BODY"; body_total="$BODY_TOTAL"; header_rows="$HEADER_ROWS"
    # Drop any marked file that left the changeset since the last build (it was
    # discarded, committed, or reverted externally), so the selection — and the
    # confirm count — never references a file that's no longer listed. body_map
    # carries one path per body line (empty on header/blank rows), which is the
    # valid-path set. Runs on the build tick only, never on the hover hot path.
    SELECTED=$(prune_selection "$SELECTED" "$body_map")

    # Agent/account pill: recompute so the bottom bar reflects what THIS
    # session's pane is running — the claude login (tmux-stamped session
    # account) or, for another agent, that agent's name. Empty when the
    # session is ineligible (no relaunch context, nothing to switch to, or the
    # helpers aren't loaded).
    account_pill_str=""; account_pill_cols=0; account_pill_hover_str=""
    if [ -n "$_rc_relaunch" ] && command -v account_pill_enabled >/dev/null 2>&1 \
       && account_pill_enabled "$_rc_relaunch" "$_rc_list"; then
      # Re-read the running tool each tick: a mid-session agent switch
      # rewrites the context's tool= while this ledger keeps running.
      _rc_tool="$(sed -n 's/^tool=//p' "$_rc_relaunch" 2>/dev/null)"
      IFS=$'\t' read -r _pill_label _pill_color _pill_glyph \
        < <(pill_current "${_rc_tool:-claude}" "$_rc_pointer" "$_rc_list" "$_rc_default_label" "$_rc_colors" tmux "$_rc_config_pointer" "$_rc_configs_list")
      _pill="$(account_pill "$_pill_label" "$_pill_color" 0 "$_pill_glyph")"
      account_pill_str="${_pill%$'\n'*}"
      account_pill_cols="${_pill##*$'\n'}"
      # Hover variant (same width) drawn while the pointer is over the pill.
      _pill="$(account_pill "$_pill_label" "$_pill_color" 1 "$_pill_glyph")"
      account_pill_hover_str="${_pill%$'\n'*}"
    fi
    # The build recomputed the divergence, pill, and changeset, so the bottom
    # bar (and its click spans) must be rebuilt on the next draw.
    bar_dirty=1
    fi

    local body_rows=$((h - header_rows))
    [ "$body_rows" -lt 1 ] && body_rows=1
    # The bottom row is reserved for the bottom bar (account pill + ↑N/↓M
    # push/pull commit counts — the branch NAME lives in the Claude statusline),
    # which sits at the bottom of the file-list view and also carries the
    # scroll position (on overflow). (The "[ discard N ]" button and its
    # yes/no confirm now overlay the TOP group header instead — see
    # $discard_overlay.) That bar is NOT always one row: a wide bar (a long
    # account-pill label plus markers) wraps onto extra rows.
    # Reserve its ACTUAL height so the composed frame never exceeds the pane — an
    # overflow scrolls the pane up and shifts every file row out from under the
    # top-anchored hover mapping, lighting the WRONG file under the cursor. Rebuild
    # the bar (only when its inputs changed) and re-measure via viewport_avail BEFORE
    # sizing the viewport, so THIS frame reserves the right number of rows.
    # build_bottom_bar reads `avail` only for the scroll indicator, so seed a
    # provisional one-row reserve for that decision, then take the real reserve from
    # the built bar. This whole block runs ONLY when bar_dirty (a build, resize, or
    # scroll/selection change) — never on the per-motion hover hot path, which reuses
    # the stored $avail and adds no measurement fork.
    if [ "$bar_dirty" = 1 ]; then
      avail=$((body_rows - 1)); [ "$avail" -lt 1 ] && avail=1
      build_bottom_bar
      avail=$(viewport_avail "$body_rows" "$(visible_width "$bottom_bar")" "$w")
      bar_dirty=0
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
      # Draw a checkbox on the selected/hovered file rows (☑/☐) first, then
      # overlay the hover bar. The boxes preserve each row's visible width, so the
      # hover padding math is unaffected.
      draw_body=$(apply_checkboxes "$body" "$body_map" "$SELECTED" "$hover_line")
      draw_body=$(highlight_body_line "$draw_body" "$hover_line" "$hover_style" "$w")
      # The bottom bar (and its $discard_overlay + click spans) was already rebuilt
      # above, before the viewport was sized, so its wrapped height could be reserved.
      # A hover-only repaint (bar_dirty=0) left it untouched, so it is reused here.
      # Splice the discard affordance onto the FIRST body line (the topmost group
      # header) so it sits next to that title. Fork-free string ops; only body line
      # 1 is touched, and it is never a checkbox/hover target so the overlays above
      # don't collide. Visible only at scroll 0 (the click handler gates on it).
      if [ -n "$discard_overlay" ] && [ "$scroll" -eq 0 ]; then
        draw_first="${draw_body%%$'\n'*}"
        if [ "$draw_first" = "$draw_body" ]; then
          draw_body="${draw_first}${discard_overlay}"
        else
          draw_body="${draw_first}${discard_overlay}"$'\n'"${draw_body#*$'\n'}"
        fi
      fi
      frame=$(
        printf '%s\n' "$header"
        printf '%s\n' "$draw_body" | viewport_slice "$scroll" "$avail"
        # Pad the body region up to a CONSTANT `avail` rows with blank lines, so
        # the bottom bar always lands on the pane's last row no matter how few
        # files are listed. viewport_slice printed min(avail, body_total - scroll)
        # rows; the rest are filler.
        _shown=$((body_total - scroll))
        [ "$_shown" -gt "$avail" ] && _shown="$avail"
        [ "$_shown" -lt 0 ] && _shown=0
        _blank=$((avail - _shown))
        while [ "$_blank" -gt 0 ]; do printf '\n'; _blank=$((_blank - 1)); done
        # Bottom bar, prebuilt in loop scope (build_bottom_bar) so its clickable
        # spans are known to the click handler. Emitted as real bytes (its ANSI is
        # already interpreted).
        printf '%s' "$bottom_bar"
      )
      # Home the cursor and overwrite the frame IN PLACE — never a full-screen
      # \033[2J, which blanks the pane for one frame and makes the list blink on
      # every scroll. Each row ends with \033[K (erase to end of line) to scrub
      # leftover characters from a previous, longer frame; the trailing \033[J
      # erases any rows below when the new frame is shorter. The whole frame is
      # one printf (one write) so the terminal never shows a half-drawn screen.
      # The newline and erase codes live in variables: zsh does not expand a
      # $'...' literal in the replacement half of ${var//pat/repl} (see
      # highlight_body_line), so an inline escape there would leak as text.
      printf '\033[H%s\033[K\033[J' "${frame//${nl}/${rowend}}"
      need_draw=0
    fi

    # Idle: just refresh on a timer. Interactive: refresh OR react to input.
    if [ "$interactive" != 1 ]; then
      sleep "$interval"
      need_build=1
      need_draw=1
      continue
    fi

    # Block for the next event, or fall through to a refresh on the interval.
    if ! read_key "$interval"; then
      need_build=1   # timed out -> refresh the ledger
      need_draw=1
      continue
    fi

    # COALESCE the input flood. Under any-motion tracking (?1003h) the terminal
    # emits one report per cursor cell, so a single fast mouse move buffers a
    # BURST of motion reports. Handle the report we just read, then drain every
    # report still queued in the tty — buffered reports arrive with no gap, so the
    # 6ms timeout only fires once the flood has settled — WITHOUT repainting
    # between them. Only the final cursor position is visible, so we repaint once,
    # after. Without this the loop repainted the whole pane per report and the
    # highlight crawled a long backlog behind the cursor (it walked; now it flies).
    prev_hover="$hover_line"   # what's on screen now, for the post-drain compare
    prev_pill_hover="$pill_hover"
    prev_scroll="$scroll"
    need_build=0
    state_dirty=0              # set by x/d/y/n when the selection or armed state changes
    while true; do
      hover_keep="$hover_line" # restored if this event is a malformed mouse report
      hover_line=0             # most keys clear the hover; mouse motion re-sets it
      pill_hover_keep="$pill_hover"
      pill_hover=0             # cleared like hover_line; motion re-sets it over the pill
      handle_key
      [ "$need_build" = 1 ] && break   # popup opened / rebuild -> stop draining
      read_key 0.006 || break          # no more buffered input -> flood settled
    done

    # The bottom bar depends on scroll (scroll status), the pill hover, and the
    # selection/armed state — but NOT the file hover — so flag it for a rebuild
    # only when one of those changed. A hover-only repaint keeps the cached bar.
    if [ "$scroll" != "$prev_scroll" ] || [ "$pill_hover" != "$prev_pill_hover" ] || [ "$state_dirty" = 1 ]; then
      bar_dirty=1
    fi

    # Repaint once — and only when the settled state actually differs from what is
    # already on screen (a flood that ends where it began draws nothing), or a
    # rebuild is pending after a popup.
    if [ "$need_build" = 1 ] || [ "$hover_line" != "$prev_hover" ] || [ "$pill_hover" != "$prev_pill_hover" ] || [ "$scroll" != "$prev_scroll" ] || [ "$state_dirty" = 1 ]; then
      need_draw=1
    else
      need_draw=0
    fi
  done

  # Restore the terminal: leave the alternate screen, re-enable echo/cursor.
  exit_ui_mode "$interactive"
  [ -n "$saved_stty" ] && stty "$saved_stty" 2>/dev/null
  return 0
}
