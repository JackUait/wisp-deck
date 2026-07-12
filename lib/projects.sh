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

# get_projects_root <file> — prints the stored projects root, or empty string.
get_projects_root() {
  local file="$1"
  local line
  [ -f "$file" ] || return 0
  IFS= read -r line < "$file" && printf '%s\n' "$line" || true
}

# set_projects_root <file> <path> — writes tilde-expanded path; removes file if path is empty.
set_projects_root() {
  local file="$1"
  local filepath="$2"
  if [ -z "$filepath" ]; then
    rm -f "$file"
    return 0
  fi
  local expanded
  expanded="$(path_expand "$filepath")"
  printf '%s\n' "$expanded" > "$file"
}