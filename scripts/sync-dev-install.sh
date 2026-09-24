#!/usr/bin/env bash
# Copy HEAD's distribution into the dev install (~/.local/share/wisp-deck, or
# WISP_DECK_INSTALL_DIR) and stamp it with .dev-install, which stops
# `npx wisp-deck` and the menu's update from installing the older npm package
# over it. Run inside the repo; .githooks/post-commit runs it after a commit
# that touches a distribution entry.
#
# Files are replaced by rename, never rewritten in place: a running bash reads
# wrapper.sh and lib/ lazily, and truncating a file it is executing corrupts it.
set -euo pipefail

# Must equal copyDistribution's entries in bin/npx-wisp-deck.js
# (checked by test/npx/dev_install_test.go).
DIST_ENTRIES=(
  bin/wisp-deck
  bin/wisp-deck-config
  lib
  templates
  defaults
  ghostty
  terminals
  wrapper.sh
  VERSION
)

# sync_replace_file <src> <dst> — leave an identical file alone, otherwise
# rename a copy over it from the same directory (same filesystem).
sync_replace_file() {
  local src="$1" dst="$2" mode tmp
  mode="$(stat -f %Lp "$src")"
  if [ -f "$dst" ] && [ ! -L "$dst" ] && cmp -s "$src" "$dst"; then
    [ "$(stat -f %Lp "$dst")" = "$mode" ] || chmod "$mode" "$dst"
    return 0
  fi
  if [ -d "$dst" ] && [ ! -L "$dst" ]; then
    rm -rf "$dst"
  fi
  mkdir -p "$(dirname "$dst")"
  tmp="$(mktemp "$(dirname "$dst")/.sync.XXXXXX")"
  cp -p "$src" "$tmp"
  mv -f "$tmp" "$dst"
}

# sync_write_marker <path> <content>
sync_write_marker() {
  local tmp
  tmp="$(mktemp "$(dirname "$1")/.sync.XXXXXX")"
  printf '%s\n' "$2" > "$tmp"
  chmod 644 "$tmp"
  mv -f "$tmp" "$1"
}

sync_dev_install_main() {
  local install_dir="${WISP_DECK_INSTALL_DIR:-}"
  if [ -z "$install_dir" ]; then
    if [ "${WISP_DECK_TESTING:-}" = "1" ]; then
      echo "sync-dev-install: refusing the real install under WISP_DECK_TESTING=1; set WISP_DECK_INSTALL_DIR" >&2
      return 1
    fi
    install_dir="$HOME/.local/share/wisp-deck"
  fi
  if [ ! -d "$install_dir" ]; then
    echo "sync-dev-install: no install at $install_dir; run 'npx wisp-deck' first" >&2
    return 1
  fi

  local sha work entry src dst rel
  local -a present=()
  sha="$(git rev-parse HEAD)"
  work="$(mktemp -d "${TMPDIR:-/tmp}/wisp-dev-sync.XXXXXX")"
  # shellcheck disable=SC2064  # expand $work now; it is gone at trap time otherwise
  trap "rm -rf '$work'" RETURN

  # git archive fails on a path missing at HEAD, so name only present ones.
  for entry in "${DIST_ENTRIES[@]}"; do
    if git cat-file -e "$sha:$entry" 2>/dev/null; then
      present+=("$entry")
    fi
  done
  if [ "${#present[@]}" -gt 0 ]; then
    git archive "$sha" "${present[@]}" | tar -x -C "$work"
  fi

  # Marker first: an npx run during the copy must already see a dev install.
  sync_write_marker "$install_dir/.dev-install" "$sha"

  for entry in "${DIST_ENTRIES[@]}"; do
    src="$work/$entry"
    dst="$install_dir/$entry"
    if [ ! -e "$src" ]; then
      rm -rf "$dst"
    elif [ -d "$src" ]; then
      if [ -e "$dst" ] && { [ ! -d "$dst" ] || [ -L "$dst" ]; }; then
        rm -rf "$dst"
      fi
      while IFS= read -r -d '' rel; do
        sync_replace_file "$src/$rel" "$dst/$rel"
      done < <(cd "$src" && find . -type f -print0)
      while IFS= read -r -d '' rel; do
        [ -e "$src/$rel" ] || rm -f "$dst/$rel"
      done < <(cd "$dst" && find . \( -type f -o -type l \) -print0)
      find "$dst" -mindepth 1 -type d -empty -delete
    else
      sync_replace_file "$src" "$dst"
    fi
  done

  if [ -f "$work/VERSION" ]; then
    sync_write_marker "$install_dir/.version" "$(tr -d '[:space:]' < "$work/VERSION")"
  fi
  echo "sync-dev-install: $install_dir now at $sha"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  sync_dev_install_main "$@"
fi
