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
