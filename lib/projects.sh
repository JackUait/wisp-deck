#!/bin/bash
# Project file helpers — pure, no side effects on source.

# Reads a projects file and outputs valid lines (skips blanks and comments).
# Usage: mapfile -t projects < <(load_projects "$file")
load_projects() {
  local file="$1" line
  [ ! -f "$file" ] && return
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    echo "$line"
  done < "$file"
}

# Expands ~ to $HOME at the start of a path.
path_expand() {
  echo "${1/#\~/$HOME}"
}
