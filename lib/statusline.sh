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
