#!/bin/bash
# Statusline helper functions — pure, no side effects on source.

# Returns total RSS in KB for a process and all its descendants.
# Usage: get_tree_rss_kb 12345  =>  "92160"
get_tree_rss_kb() {
  local root_pid="$1"
  local total=0
  local queue=("$root_pid")

  while [ ${#queue[@]} -gt 0 ]; do
    local pid="${queue[0]}"
    queue=("${queue[@]:1}")

    local rss
    rss=$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ')
    if [ -n "$rss" ] && [ "$rss" -gt 0 ] 2>/dev/null; then
      total=$((total + rss))
    fi

    local children
    children=$(pgrep -P "$pid" 2>/dev/null) || true
    if [ -n "$children" ]; then
      while IFS= read -r child; do
        queue+=("$child")
      done <<< "$children"
    fi
  done

  echo "$total"
}

# Returns combined phys_footprint in KB for a process and all its descendants,
# using macOS `footprint`. phys_footprint matches Activity Monitor's "Memory"
# column and is the correct memory load: RSS overcounts shared dyld/framework
# pages 2-4x. Echoes nothing if `footprint` is unavailable or yields no data, so
# callers can fall back to RSS.
# Usage: get_tree_footprint_kb 12345  =>  "288352"
get_tree_footprint_kb() {
  local root_pid="$1"
  command -v footprint >/dev/null 2>&1 || return 0

  # Collect the root pid and every descendant.
  local pids=() queue=("$root_pid")
  while [ ${#queue[@]} -gt 0 ]; do
    local pid="${queue[0]}"
    queue=("${queue[@]:1}")
    pids+=("$pid")

    local children
    children=$(pgrep -P "$pid" 2>/dev/null) || true
    if [ -n "$children" ]; then
      while IFS= read -r child; do
        queue+=("$child")
      done <<< "$children"
    fi
  done

  # Sum the per-process `phys_footprint:` lines (ignoring `phys_footprint_peak:`).
  # gsub + LC_ALL=C make the parse locale-independent: macOS emits a comma
  # decimal (e.g. "1,5 GB") under comma-locales, which would otherwise truncate.
  footprint "${pids[@]}" 2>/dev/null | LC_ALL=C awk '
    /^[[:space:]]*phys_footprint:/ {
      val = $2; unit = $3; mult = 1
      gsub(/,/, ".", val)
      if (unit == "B")  mult = 1 / 1024
      else if (unit == "KB") mult = 1
      else if (unit == "MB") mult = 1024
      else if (unit == "GB") mult = 1024 * 1024
      total += val * mult
    }
    END { if (total > 0) printf "%d\n", total }
  '
}

# Returns the real CPU load (summed `ps -o %cpu`, rounded to an integer) for a
# process and all its descendants. macOS `ps %cpu` is a recent-usage average and
# is read synchronously — a `top -l 2` sample would stall the statusline ~1s. The
# sum can exceed 100 on multi-core machines. An idle session reports 0. Echoes
# nothing only when no pid yields a reading (the process tree is gone), so the
# caller omits the segment rather than showing a stale value.
# Usage: get_tree_cpu_pct 12345  =>  "23"
get_tree_cpu_pct() {
  local root_pid="$1"
  local queue=("$root_pid")
  local cpus=()

  while [ ${#queue[@]} -gt 0 ]; do
    local pid="${queue[0]}"
    queue=("${queue[@]:1}")

    local cpu
    cpu=$(ps -o %cpu= -p "$pid" 2>/dev/null | tr -d ' ')
    if [ -n "$cpu" ]; then
      cpus+=("$cpu")
    fi

    local children
    children=$(pgrep -P "$pid" 2>/dev/null) || true
    if [ -n "$children" ]; then
      while IFS= read -r child; do
        queue+=("$child")
      done <<< "$children"
    fi
  done

  [ ${#cpus[@]} -eq 0 ] && return 0

  # gsub + LC_ALL=C make the sum locale-independent: macOS `ps` emits a comma
  # decimal (e.g. "10,4") under comma-locales, which awk would otherwise read as
  # 10 and silently under-report the CPU load.
  printf '%s\n' "${cpus[@]}" | LC_ALL=C awk '
    { gsub(/,/, "."); total += $0 }
    END { printf "%d\n", total + 0.5 }
  '
}

# Stamp the current Claude conversation id into the enclosing tmux session's
# environment (WISP_DECK_CLAUDE_SESSION). The statusline runs as a child of
# claude inside the tmux pane, so this is the only reliable place where "this
# tab" and "this conversation" are known together. The session snapshot picks
# the id up so a restored tab reopens its own conversation (`claude --resume`)
# instead of the project's most recent one.
# Usage: gt_stamp_claude_session <statusline_json>
# Exit 0 when the user has 2+ Claude accounts to juggle, so the statusline
# account segment is worth showing. The list holds only the *managed* logins;
# the implicit Default (Keychain) login always exists on top of them — mirroring
# the Go account menu, where row 0 is the Default and rows 1..len are the managed
# entries. So the user has 2+ accounts as soon as the list holds a single managed
# label:dir entry (skipping comments, blanks, and malformed lines): that one plus
# the Default. An empty or missing list means only the Default exists — one
# account, nothing to disambiguate — so the segment stays hidden. (This differs
# from auto_switch_eligible, which needs 2+ *managed* logins to rotate between.)
# Usage: gt_multiple_claude_accounts <list_file> && show_account_segment
gt_multiple_claude_accounts() {
  local file="$1" line
  [ -f "$file" ] || return 1
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"  # ltrim
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" != *:* ]] && continue
    return 0  # a single managed login + the implicit Default = 2 accounts
  done < "$file"
  return 1
}

# Print the account-rotation proxy's currently-serving account dir name, read
# from the state file the proxy rewrites on each switch (empty if the path is
# blank, missing, or empty). When set, it overrides CLAUDE_CONFIG_DIR for the
# statusline label: the proxy rotates accounts while `claude` keeps its single
# (Default) config dir, so the env var alone would never reflect a switch.
# Usage: gt_proxy_active_dir "$WISP_DECK_PROXY_ACCOUNT_FILE"  =>  "work"
gt_proxy_active_dir() {
  local file="$1" dir=""
  { [ -n "$file" ] && [ -s "$file" ]; } || return 0
  IFS= read -r dir < "$file" || true
  dir="${dir#"${dir%%[![:space:]]*}"}"  # ltrim
  dir="${dir%"${dir##*[![:space:]]}"}"  # rtrim
  printf '%s\n' "$dir"
}

# The 256-color indices a Claude account's identity can wear. Kept in lock-step
# with claudeaccount.Palette in Go (internal/claudeaccount/colors.go) so the
# statusline and the TUI menu paint an account the same color. A spread of
# distinct, readable-on-dark hues. A real array (not a space-separated string) so
# the pill path — account_current -> gt_account_color runs under the pane's ZSH,
# which does NOT word-split an unquoted scalar — iterates the members rather than
# treating the whole string as one entry (which assigned an empty color and
# poisoned the shared colors file the bash statusline reads back).
GT_ACCOUNT_PALETTE=(39 208 170 78 203 141 43 220 205 75 156 214)

# Return the persisted 256-color index for a Claude account dir, assigning a new
# one and appending it to the colors file if the dir has none yet. The new color
# is picked at random ($RANDOM) from palette entries not already used by another
# account, so distinct accounts stay distinct ("non-repeating") until the palette
# is exhausted, then it falls back to a random palette member. The empty dir (the
# implicit Default login) is keyed under "default", matching Go. The file is the
# source of truth — once assigned the color is stable and shared with the menu.
# Usage: gt_account_color <colors_file> <dir>  =>  "141"
gt_account_color() {
  local file="$1" dir="$2" k v
  [ -z "$dir" ] && dir="default"
  # Already assigned? Return the first match (first writer wins on any race).
  if [ -f "$file" ]; then
    while IFS=: read -r k v; do
      [ "$k" = "$dir" ] && { printf '%s\n' "$v"; return 0; }
    done < "$file"
  fi
  # Collect indices already handed out, then split the palette into the full set
  # and the entries still free. Iterate the quoted palette array so this works the
  # same under bash and zsh (the pill path runs under the pane's zsh).
  local used=" " pal=() avail=() n
  if [ -f "$file" ]; then
    while IFS=: read -r k v; do
      [ -n "$v" ] && used="$used$v "
    done < "$file"
  fi
  for n in "${GT_ACCOUNT_PALETTE[@]}"; do
    pal+=("$n")
    case "$used" in *" $n "*) ;; *) avail+=("$n") ;; esac
  done
  # Prefer an unused color (keeps accounts distinct); once the palette is
  # exhausted, reuse a random one rather than fail. Select the random member by
  # COUNTING through "${pool[@]}" rather than indexing pool[$n] directly: bash
  # arrays are 0-indexed but zsh's are 1-indexed, so a bare numeric index would
  # land off-by-one (an empty color) under the pane's zsh.
  local pool=("${avail[@]}")
  [ "${#pool[@]}" -eq 0 ] && pool=("${pal[@]}")
  local target=$((RANDOM % ${#pool[@]})) i=0 pick="" c
  for c in "${pool[@]}"; do
    [ "$i" -eq "$target" ] && { pick="$c"; break; }
    i=$((i + 1))
  done
  mkdir -p "$(dirname "$file")" 2>/dev/null
  printf '%s:%s\n' "$dir" "$pick" >> "$file"
  printf '%s\n' "$pick"
}

# Map an account's isolated CLAUDE_CONFIG_DIR to its display label, so the
# statusline can show which native Claude account this tab is using. The
# statusline runs as a child of `claude`, which wrapper.sh launches with the
# active account's CLAUDE_CONFIG_DIR exported (unset for the Keychain Default),
# so $1 is that inherited value and its basename is the account's dir name. An
# empty config dir (Default) or a dir absent from the list reads as the Default
# login's label, mirroring the menu/ledger's ACCOUNT row. The Default login can
# be renamed via the account menu (persisted in the optional default-label file
# $3); when set, that custom label is shown instead of the literal "Default".
# Usage: gt_claude_account_label "$CLAUDE_CONFIG_DIR" <list_file> [default_label_file]
gt_claude_account_label() {
  local config_dir="$1" list_file="$2" default_label_file="${3:-}"
  local active label dir default_label="Default"
  # Resolve the Default login's display label — a custom name if the user
  # renamed it, else the literal "Default" (mirrors Go's GetDefaultLabel).
  if [ -n "$default_label_file" ] && [ -f "$default_label_file" ]; then
    IFS= read -r default_label < "$default_label_file" || true
    default_label="${default_label#"${default_label%%[![:space:]]*}"}"  # ltrim
    default_label="${default_label%"${default_label##*[![:space:]]}"}"  # rtrim
    [ -z "$default_label" ] && default_label="Default"
  fi
  [ -z "$config_dir" ] && { printf '%s\n' "$default_label"; return 0; }
  active="${config_dir##*/}"
  if [ -n "$active" ] && [ -f "$list_file" ]; then
    while IFS=: read -r label dir; do
      [[ -z "$label" || "$label" == \#* ]] && continue
      if [ "$dir" = "$active" ]; then
        printf '%s\n' "$label"
        return 0
      fi
    done < "$list_file"
  fi
  printf '%s\n' "$default_label"
}

# Render a percentage (0-100) as a fixed-width segmented pill bar of squares:
# full (◼) for the used share, empty (◻) for what remains, and one half square
# (◧) when the fill lands on a half-cell — so a 10-cell bar reads to 5% at a
# glance without a number. The fill is rounded to the nearest HALF cell and
# clamped to 0..width, so out-of-range input never overflows. Width defaults to
# 10 (one cell per 10%). The math runs locale-independently (macOS awk reads a
# comma decimal as a truncated integer otherwise). The cells are laid down with
# `printf FMT arg-per-cell` rather than appending in a loop: macOS bash 3.2
# corrupts a multibyte character appended to a variable ("$out◼") under a UTF-8
# locale, but repeating it through a printf format and joining via command
# substitution is byte-safe. Literal UTF-8 squares are embedded directly (bash
# 3.2 printf has no \u/\U escapes).
# Usage: gt_usage_bar 45 [10]  =>  "◼◼◼◼◧◻◻◻◻◻"
gt_usage_bar() {
  local pct="$1" width="${2:-10}" halves full half empty a="" b="" c=""
  # Count of half-cells filled, rounded to nearest and clamped to [0, 2*width].
  halves=$(printf '%s %s\n' "$pct" "$width" | LC_ALL=C awk '
    { p = $1; w = $2; gsub(/,/, ".", p)
      n = int(p / 100 * w * 2 + 0.5)
      if (n < 0) n = 0; if (n > 2 * w) n = 2 * w
      print n }')
  full=$((halves / 2))
  half=$((halves % 2))
  empty=$((width - full - half))
  # seq feeds one arg per cell so printf stamps the glyph exactly that many
  # times; unquoted on purpose (word splitting is what supplies the args).
  # shellcheck disable=SC2046
  [ "$full" -gt 0 ] && a=$(printf '◼%.0s' $(seq 1 "$full"))
  [ "$half" -gt 0 ] && b='◧'
  # shellcheck disable=SC2046
  [ "$empty" -gt 0 ] && c=$(printf '◻%.0s' $(seq 1 "$empty"))
  printf '%s%s%s\n' "$a" "$b" "$c"
}

# Pull the subscriber's 7-day (weekly) usage out of the statusline JSON as a
# rounded whole-number percentage — the raw figure the wrapper turns into a bar
# (gt_usage_bar). Claude Code embeds this under
# rate_limits.seven_day for Pro/Max logins, but only after the session's first
# API response, and it omits a window that has no data yet. So an absent
# rate_limits, an absent seven_day, or a seven_day carrying only five_hour's
# number must all yield nothing (the segment hides) — hence the regex is anchored
# inside the seven_day object's own braces ([^}]* never crosses into a sibling
# window). The round is locale-independent (a comma decimal would truncate).
# Usage: gt_weekly_used_pct "$statusline_json"  =>  "42"
gt_weekly_used_pct() {
  local input="$1" pct
  pct=$(printf '%s' "$input" \
    | sed -n 's/.*"seven_day":{[^}]*"used_percentage":\([0-9][0-9.]*\).*/\1/p')
  [ -n "$pct" ] || return 0
  printf '%s\n' "$pct" | LC_ALL=C awk '{ gsub(/,/, "."); printf "%d\n", $0 + 0.5 }'
}

# Pull the subscriber's 5-hour (rolling session) usage out of the statusline JSON
# as a rounded whole-number percentage — the 5h analog of gt_weekly_used_pct. The
# regex is anchored inside the five_hour object's own braces ([^}]* never crosses
# into seven_day), so an absent rate_limits, an absent five_hour, or a payload
# carrying only seven_day's number all yield nothing (the segment hides). The
# round is locale-independent (a comma decimal would truncate).
# Usage: gt_five_hour_used_pct "$statusline_json"  =>  "25"
gt_five_hour_used_pct() {
  local input="$1" pct
  pct=$(printf '%s' "$input" \
    | sed -n 's/.*"five_hour":{[^}]*"used_percentage":\([0-9][0-9.]*\).*/\1/p')
  [ -n "$pct" ] || return 0
  printf '%s\n' "$pct" | LC_ALL=C awk '{ gsub(/,/, "."); printf "%d\n", $0 + 0.5 }'
}

gt_stamp_claude_session() {
  [ -n "${TMUX:-}" ] || return 0
  local sid transcript
  sid="$(echo "$1" | sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p')"
  [ -n "$sid" ] || return 0
  # Only stamp ids that `claude --resume` would accept: the transcript must
  # exist on disk and contain a model turn. A freshly-launched (or just
  # /clear'd) session already shows a session_id while neither is true yet —
  # stamping it would make restore fail hard ("No conversation found") and
  # dump the tab to a bare shell. Until the new conversation is durable, the
  # previously stamped (resumable) id stays in place. Payloads without a
  # transcript_path (older claude) stamp unconditionally, as before; restore
  # re-validates the id anyway.
  transcript="$(echo "$1" | sed -n 's/.*"transcript_path":"\([^"]*\)".*/\1/p')"
  if [ -n "$transcript" ]; then
    { [ -f "$transcript" ] && grep -q '"type":"assistant"' "$transcript"; } || return 0
  fi
  tmux set-environment WISP_DECK_CLAUDE_SESSION "$sid" 2>/dev/null || true
}
