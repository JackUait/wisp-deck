#!/bin/bash
# Claude account helpers — pure, no side effects on source.
# An "account" is a native Claude login isolated by its own CLAUDE_CONFIG_DIR,
# so multiple subscriptions (work, personal) can stay logged in at once and be
# switched between by relaunching `claude` under a different config dir.
# Storage: <root>/claude-accounts/<dir>/ holds the login, named in
# <root>/claude-accounts.list (label:dir), with the active dir in
# <root>/claude-account. The Default account (absent/"default" pointer) is the
# user's standard ~/.claude login (Keychain) — no CLAUDE_CONFIG_DIR is set.

# load_claude_accounts <list_file> — prints valid label:dir lines (skips blanks/comments).
load_claude_accounts() {
  local file="$1" line
  [ ! -f "$file" ] && return 0
  while IFS= read -r line; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    echo "$line"
  done < "$file"
}

# get_active_claude_account <pointer_file> — prints active dir name or empty (Default).
get_active_claude_account() {
  local file="$1" line
  [ -f "$file" ] || return 0
  IFS= read -r line < "$file" || true
  line="${line//[[:space:]]/}"
  [ "$line" = "default" ] && return 0
  printf '%s\n' "$line"
}

# get_active_claude_account_name <pointer_file> <list_file> — prints the active
# account's display label, mirroring the menu's ACCOUNT row. Default (no active
# pointer) and any dir not found in the list read as "Default".
get_active_claude_account_name() {
  local pointer_file="$1" list_file="$2" active label dir
  active="$(get_active_claude_account "$pointer_file")"
  if [ -n "$active" ] && [ -f "$list_file" ]; then
    while IFS=: read -r label dir; do
      [[ -z "$label" || "$label" == \#* ]] && continue
      if [ "$dir" = "$active" ]; then
        printf '%s\n' "$label"
        return 0
      fi
    done < "$list_file"
  fi
  printf 'Default\n'
}

# set_active_claude_account <pointer_file> <dir> — empty/default removes the file.
set_active_claude_account() {
  local file="$1" dirname="$2"
  if [ -z "$dirname" ] || [ "$dirname" = "default" ]; then
    rm -f "$file"
    return 0
  fi
  mkdir -p "$(dirname "$file")"
  printf '%s\n' "$dirname" > "$file"
}

# resolve_claude_account_dir <accounts_dir> <pointer_file> — abs path iff the
# active account directory exists. Default (empty pointer) resolves to empty so
# the launcher leaves CLAUDE_CONFIG_DIR unset and Claude uses the Keychain login.
resolve_claude_account_dir() {
  local accounts_dir="$1" pointer_file="$2" active
  active="$(get_active_claude_account "$pointer_file")"
  [ -z "$active" ] && return 0
  local path="$accounts_dir/$active"
  [ -d "$path" ] && printf '%s\n' "$path"
}

# apply_plain_terminal_claude_account <accounts_dir> <pointer_file> — exports
# CLAUDE_CONFIG_DIR for the active account so `claude` launched in a plain Ghostty
# shell (the "plain terminal" menu action) loads the login the user currently has
# selected, not the Keychain default. The plain shell is exec'd before wrapper.sh
# does its normal per-tool account resolution, so this re-applies it here. Default
# (or a missing account dir) leaves CLAUDE_CONFIG_DIR unset, so Claude falls back
# to the standard ~/.claude Keychain login.
apply_plain_terminal_claude_account() {
  local accounts_dir="$1" pointer_file="$2" dir
  dir="$(resolve_claude_account_dir "$accounts_dir" "$pointer_file")"
  [ -z "$dir" ] && return 0
  export CLAUDE_CONFIG_DIR="$dir"
}

# Account registration, rename, and removal all live in the Go TUI (the single
# source of truth). Adding a login only registers its isolated CLAUDE_CONFIG_DIR;
# no `claude auth login` is run there — the account starts empty and Claude logs
# the user in on first launch under that config dir. See
# internal/tui/claude_account_menu.go and internal/claudeaccount.
