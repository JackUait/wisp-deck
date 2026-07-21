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

# Append the lines of <extra> that are not already in <base> onto <base>.
# A severed history.jsonl link leaves the account with a full COPY of the shared
# history plus its new entries — a blind `cat` append would duplicate the whole
# history on every heal. An empty/missing base falls back to a plain append
# (grep -f with an empty pattern file matches nothing, which -v would invert to
# "append everything" on GNU but is undefined on BSD).
# Usage: _wd_append_jsonl_dedup <base> <extra>
_wd_append_jsonl_dedup() {
  local base="$1" extra="$2" add
  if [ -s "$base" ]; then
    # Buffer the missing lines before appending — appending to the pattern
    # file while grep might still read it is undefined (SC2094).
    add="$(grep -vxF -f "$base" "$extra" 2>/dev/null)" || true
    [ -n "$add" ] && printf '%s\n' "$add" >> "$base"
  else
    cat "$extra" >> "$base" 2>/dev/null || true
  fi
  return 0
}

# Drain an aside copy (a renamed-away account item) into the shared store,
# deleting only what was actually merged. Files missing from the store are
# moved in; identical duplicates are dropped; a differing same-path copy is
# preserved next to the store copy as *.conflict-<pid> (never silently
# deleted — it may hold turns the store copy lacks). Anything that could not
# be merged (e.g. an unwritable store) stays in the aside path for the next
# launch to retry — this function must NEVER unconditionally delete.
# Usage: _wd_drain_state_aside <src> <aside>
_wd_drain_state_aside() {
  local src="$1" aside="$2" f rel target
  if [ -f "$aside" ]; then
    if [ ! -e "$src" ]; then
      mkdir -p "$(dirname "$src")" 2>/dev/null || true
      mv "$aside" "$src" 2>/dev/null || true
    elif [[ "$src" == *.jsonl ]]; then
      _wd_append_jsonl_dedup "$src" "$aside" && rm -f "$aside"
    elif cmp -s "$src" "$aside" 2>/dev/null; then
      rm -f "$aside"
    else
      mv "$aside" "$src.conflict-$$" 2>/dev/null || true
    fi
    return 0
  fi
  [ -d "$aside" ] || return 0
  while IFS= read -r -d '' f; do
    rel="${f#"$aside"/}"
    target="$src/$rel"
    if [ ! -e "$target" ]; then
      mkdir -p "$(dirname "$target")" 2>/dev/null || true
      mv "$f" "$target" 2>/dev/null || true
    elif cmp -s "$target" "$f" 2>/dev/null; then
      rm -f "$f"
    else
      mv "$f" "$target.conflict-$$" 2>/dev/null || true
    fi
  done < <(find "$aside" -type f -print0 2>/dev/null)
  # Remove only emptied directories; unmerged files keep the aside alive so a
  # later launch retries. `-delete` prunes depth-first, including the root.
  find "$aside" -type d -empty -delete 2>/dev/null || true
  return 0
}

# sync_claude_shared_state <source_dir> <account_dir>
# Make the account's conversation state an alias of the standard login's store.
# Steady state (item already linked to the store) is a strict no-op — no
# rm/relink, because that would open a per-launch window where a live claude
# append lands in a fresh real file that a concurrent migration could delete.
# A real (unlinked) item is migrated by RENAME-ASIDE: atomically mv it away,
# link the store into place immediately (a live writer's next path-open lands
# in the store), then drain the aside copy file-by-file. Only one concurrent
# launch can win each mv; losers see the link and skip. Nothing is ever
# rm -rf'd: unmerged files survive in the aside path until a launch drains
# them. Same guards as the settings sync.
sync_claude_shared_state() {
  # The compact-view pane runs this under ZSH (the user's $SHELL) via the
  # mid-session account switch. In zsh an unmatched glob is a FATAL error by
  # default (the `nomatch` option), so the `"$dest".migrating.*` loop below —
  # which normally matches nothing — would abort the whole pane, killing the
  # file-list view. `local_options` confines the change to this function; a
  # no-op under bash (where the guard is false).
  [ -n "${ZSH_VERSION:-}" ] && setopt local_options no_nomatch 2>/dev/null
  local source_dir="$1" account_dir="$2" item src dest aside aside_pid
  [ -n "$source_dir" ] && [ -n "$account_dir" ] || return 0
  [ -d "$source_dir" ] && [ -d "$account_dir" ] || return 0
  [ "$source_dir" = "$account_dir" ] && return 0

  for item in "${WISP_DECK_CLAUDE_SHARED_STATE_ITEMS[@]}"; do
    src="$source_dir/$item"
    dest="$account_dir/$item"
    # Finish any migration a previous (interrupted/failed) launch left behind —
    # but only when its owner (the pid suffix) is gone. A live aside belongs to
    # a concurrent launch mid-drain; draining it from two processes at once
    # would append the same history entries twice.
    for aside in "$dest".migrating.*; do
      [ -e "$aside" ] || continue
      aside_pid="${aside##*.}"
      case "$aside_pid" in '' | *[!0-9]*) aside_pid="" ;; esac
      if [ -n "$aside_pid" ] && kill -0 "$aside_pid" 2>/dev/null; then
        continue
      fi
      _wd_drain_state_aside "$src" "$aside"
    done
    # Steady state: already linked to the store — leave it strictly alone.
    # `-ef` is a test builtin (no fork): true when the link resolves to the
    # same file as the store item. This runs per item per account on the
    # launch critical path, where a readlink subprocess each was ~0.2s per
    # launch. readlink remains only as the fallback that recognizes a
    # correct-but-dangling link (store item not created yet), which -ef
    # cannot see; that path is rare and may fork.
    if [ -L "$dest" ]; then
      if [ "$dest" -ef "$src" ] \
         || { [ ! -e "$src" ] && [ "$(readlink "$dest" 2>/dev/null)" = "$src" ]; }; then
        continue
      fi
    fi
    if [ -e "$dest" ] && [ ! -L "$dest" ]; then
      aside="$dest.migrating.$$"
      # Atomic claim; a concurrent launch losing this mv just skips.
      mv "$dest" "$aside" 2>/dev/null || continue
      if [ ! -e "$src" ]; then
        # Store has no such item yet: the account copy becomes the store copy.
        mkdir -p "$(dirname "$src")" 2>/dev/null || true
        if ! mv "$aside" "$src" 2>/dev/null; then
          # Could not seed the store (unwritable?) — roll back and retry later.
          mv "$aside" "$dest" 2>/dev/null || true
          continue
        fi
      fi
      # No -f: never clobber a file a live claude re-created at this path in
      # the tiny window since the mv — it holds real data; if present, mv it
      # into the aside pile and retry the link once.
      if ! ln -s "$src" "$dest" 2>/dev/null; then
        if [ -e "$dest" ] && [ ! -L "$dest" ]; then
          mkdir -p "$aside" 2>/dev/null || true
          mv "$dest" "$aside/$(basename "$dest").reappeared.$$" 2>/dev/null || true
          ln -s "$src" "$dest" 2>/dev/null || true
        fi
      fi
      [ -e "$aside" ] && _wd_drain_state_aside "$src" "$aside"
    else
      # dest absent, or a symlink pointing somewhere stale.
      if [ ! -e "$src" ]; then
        [ -L "$dest" ] && rm -f "$dest"
        continue
      fi
      rm -f "$dest" 2>/dev/null
      ln -s "$src" "$dest" 2>/dev/null || true
    fi
  done
  return 0
}

# sync_all_claude_accounts_state <source_dir> <accounts_dir>
# Run the state sync for every registered account dir. Called before the
# session-restore gate so a reboot that follows an account switch still finds
# every transcript in the shared store — regardless of which login each
# pre-reboot session ran under or which one is active now.
sync_all_claude_accounts_state() {
  # Same zsh nomatch guard as sync_claude_shared_state: the `"$accounts_dir"/*/`
  # loop matches nothing before any account is registered, which would abort a
  # zsh caller. local_options confines it; a no-op under bash.
  [ -n "${ZSH_VERSION:-}" ] && setopt local_options no_nomatch 2>/dev/null
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
