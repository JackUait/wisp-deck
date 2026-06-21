#!/bin/bash
# Claude config helpers — pure, no side effects on source.
# A "config" is a settings JSON launched via `claude --settings <file>`.
# Storage: <root>/claude-configs/<file>.json, named in <root>/claude-configs.list
# (name:file), with active filename in <root>/claude-config.

# load_claude_configs <list_file> — prints valid name:file lines (skips blanks/comments).
load_claude_configs() {
  local file="$1" line
  [ ! -f "$file" ] && return 0
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    echo "$line"
  done < "$file"
}

# get_active_claude_config <pointer_file> — prints active filename or empty.
get_active_claude_config() {
  local file="$1" line
  [ -f "$file" ] || return 0
  IFS= read -r line < "$file" || true
  line="${line//[[:space:]]/}"
  [ "$line" = "standard" ] && return 0
  printf '%s\n' "$line"
}

# get_active_claude_config_name <pointer_file> <list_file> — prints the active
# subscription's display name, mirroring the menu's PLAN label. Standard (no
# active pointer) and any filename not found in the list read as "Standard
# Claude".
get_active_claude_config_name() {
  local pointer_file="$1" list_file="$2" active name file
  active="$(get_active_claude_config "$pointer_file")"
  if [ -n "$active" ] && [ -f "$list_file" ]; then
    while IFS=: read -r name file; do
      [[ -z "$name" || "$name" == \#* ]] && continue
      if [ "$file" = "$active" ]; then
        printf '%s\n' "$name"
        return 0
      fi
    done < "$list_file"
  fi
  printf 'Standard Claude\n'
}

# set_active_claude_config <pointer_file> <filename> — empty/standard removes the file.
set_active_claude_config() {
  local file="$1" filename="$2"
  if [ -z "$filename" ] || [ "$filename" = "standard" ]; then
    rm -f "$file"
    return 0
  fi
  mkdir -p "$(dirname "$file")"
  printf '%s\n' "$filename" > "$file"
}

# resolve_claude_config_path <configs_dir> <pointer_file> — abs path iff active file exists.
resolve_claude_config_path() {
  local configs_dir="$1" pointer_file="$2" active
  active="$(get_active_claude_config "$pointer_file")"
  [ -z "$active" ] && return 0
  local path="$configs_dir/$active"
  [ -f "$path" ] && printf '%s\n' "$path"
}

# Mutations (add / rename / delete) live in Go — the single source of truth —
# exposed as `ghost-tab-tui claude-config <action>` and called by config-tui.sh.
# See internal/claudeconfig.
