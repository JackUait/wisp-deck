#!/bin/bash
# Claude shared-settings helper — pure, no side effects on source.
# A native Claude "account" is isolated by its own CLAUDE_CONFIG_DIR (see
# lib/claude-accounts.sh), so by default a non-Default login sees NONE of the
# standard ~/.claude login's settings: status line, permission mode, skills,
# subagents, slash commands, hooks, model, keybindings, plugins — all gone.
#
# sync_claude_shared_settings symlinks a curated allowlist of *settings* items
# from the standard login's config dir into a per-account CLAUDE_CONFIG_DIR, so
# every login shares ONE set of settings while each keeps its own credentials,
# identity (.claude.json), and session/runtime state. Symlinks (not copies) mean
# editing a setting once — in the standard login — propagates to all accounts,
# and re-running at every launch self-heals any drift (e.g. if Claude rewrote a
# settings file in place, severing the link).

# Items shared across logins. Each is a settings/customization artifact, never
# credentials or per-login state. Files that don't exist in the source are
# skipped, so this list can name everything Claude might use without harm.
WISP_DECK_CLAUDE_SHARED_ITEMS=(
  settings.json          # permissions (incl. defaultMode), model, hooks, env, statusLine, plugins
  settings.local.json    # machine-local permission overrides
  CLAUDE.md              # global user memory
  keybindings.json       # custom key bindings
  skills                 # custom skills
  commands               # custom slash commands
  agents                 # custom subagents
  plugins                # installed plugins + marketplace config
  statusline-wrapper.sh  # status line entrypoint referenced by settings.json
  statusline-command.sh  # status line helpers
  statusline-helpers.sh
  subagent-statusline.sh         # subagent panel row entrypoint (settings.json)
  subagent-statusline-helpers.sh # subagent panel row renderer
  tab-spinner-start.sh   # notification/tab hooks referenced by settings.json
  tab-spinner-stop.sh
)

# sync_claude_shared_settings <source_dir> <account_dir>
# Link every existing shared item from source_dir into account_dir, replacing any
# account-local copy with a symlink to the source. No-op (exit 0) when either dir
# is missing or when the two are the same path (the Default login uses the source
# dir directly and must be left untouched). Never reads, writes, or removes any
# item outside the allowlist, so credentials and session state are safe.
sync_claude_shared_settings() {
  local source_dir="$1" account_dir="$2" item src dest
  [ -n "$source_dir" ] && [ -n "$account_dir" ] || return 0
  [ -d "$source_dir" ] && [ -d "$account_dir" ] || return 0
  [ "$source_dir" = "$account_dir" ] && return 0

  for item in "${WISP_DECK_CLAUDE_SHARED_ITEMS[@]}"; do
    src="$source_dir/$item"
    dest="$account_dir/$item"
    [ -e "$src" ] || continue
    # Drop any existing account-local file/dir/symlink (removing a symlink-to-dir
    # deletes only the link, never the shared target), then point at the source.
    rm -rf "$dest"
    ln -sfn "$src" "$dest"
  done
}

# Conversation state shared across logins. Isolating these per account is what
# caused the post-reboot incident: transcripts written under one login were
# invisible to the other, so `/resume` history "disappeared" after switching
# accounts and session-restore's `--resume <sid>` failed (its `-c` fallback then
# opened a stale conversation from the other store). Credentials and identity
# (.credentials.json, .claude.json) are deliberately NOT here — sharing is for
# the user's work product only.
WISP_DECK_CLAUDE_SHARED_STATE_ITEMS=(
  projects       # conversation transcripts — the /resume list and resume data
  history.jsonl  # prompt (up-arrow) history
  todos          # per-session task lists
  session-env    # per-session env snapshots resumed sessions read back
  file-history   # /rewind edit history
  plans          # saved plan-mode plans
)

# Merge one account-local state path into the shared store before linking.
# Files missing from the store are moved in; on a same-path conflict the shared
# store's copy wins (transcript ids are unique per store, so real conflicts do
# not occur in practice) — except a top-level .jsonl file (history.jsonl), whose
# account entries are appended so neither login's prompt history is dropped.
# Usage: _wd_merge_state_into_source <src> <dest>
_wd_merge_state_into_source() {
  local src="$1" dest="$2" f rel
  if [ -f "$dest" ]; then
    if [ ! -e "$src" ]; then
      mkdir -p "$(dirname "$src")"
      mv "$dest" "$src"
    elif [ -f "$src" ] && [[ "$dest" == *.jsonl ]]; then
      cat "$dest" >> "$src"
    fi
    return 0
  fi
  [ -d "$dest" ] || return 0
  while IFS= read -r -d '' f; do
    rel="${f#"$dest"/}"
    if [ ! -e "$src/$rel" ]; then
      mkdir -p "$(dirname "$src/$rel")"
      mv "$f" "$src/$rel"
    fi
  done < <(find "$dest" -type f -print0 2>/dev/null)
}

# sync_claude_shared_state <source_dir> <account_dir>
# Make the account's conversation state an alias of the standard login's store:
# merge anything the account recorded locally into the store (never losing a
# transcript), then symlink each state item to it. Same guards as the settings
# sync; idempotent — an already-linked item is left alone, so repeated launches
# are cheap no-ops.
sync_claude_shared_state() {
  local source_dir="$1" account_dir="$2" item src dest
  [ -n "$source_dir" ] && [ -n "$account_dir" ] || return 0
  [ -d "$source_dir" ] && [ -d "$account_dir" ] || return 0
  [ "$source_dir" = "$account_dir" ] && return 0

  for item in "${WISP_DECK_CLAUDE_SHARED_STATE_ITEMS[@]}"; do
    src="$source_dir/$item"
    dest="$account_dir/$item"
    if [ -e "$dest" ] && [ ! -L "$dest" ]; then
      _wd_merge_state_into_source "$src" "$dest"
    fi
    # Nothing to share yet (neither side has the item): drop a dangling link if
    # one exists and move on.
    if [ ! -e "$src" ]; then
      [ -L "$dest" ] && rm -f "$dest"
      continue
    fi
    rm -rf "$dest"
    ln -sfn "$src" "$dest"
  done
}

# sync_all_claude_accounts_state <source_dir> <accounts_dir>
# Run the state sync for every registered account dir. Called before the
# session-restore gate so a reboot that follows an account switch still finds
# every transcript in the shared store — regardless of which login each
# pre-reboot session ran under or which one is active now.
sync_all_claude_accounts_state() {
  local source_dir="$1" accounts_dir="$2" acc
  [ -d "$accounts_dir" ] || return 0
  # Registered accounts imply Claude is in use; make sure the shared store's
  # root exists so account state has somewhere to merge into even before the
  # standard login's first run.
  mkdir -p "$source_dir" 2>/dev/null || true
  for acc in "$accounts_dir"/*/; do
    [ -d "$acc" ] || continue
    sync_claude_shared_state "$source_dir" "${acc%/}"
  done
  return 0
}
