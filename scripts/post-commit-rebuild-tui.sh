#!/usr/bin/env bash
# Rebuild + install ~/.local/bin/wisp-deck-tui when a commit touches the Go
# TUI's inputs. lib/ deploys itself through the live symlinks, but ledger
# panes only ever exec the installed binary — a committed-but-not-installed
# TUI change leaves every new tab on the old UI until someone happens to run
# a build (bac1be4 sat uninstalled for 19 hours this way).
#
# The build exports HEAD with `git archive` into a temp dir: the shared
# checkout churns under concurrent sessions and must never be built dirty.
# After installing, HEAD is re-checked — if another commit landed mid-build,
# the loop builds again so the newest commit always wins.
#
# Invoked by .githooks/post-commit. Runs in the background by default so
# commits don't stall; WISP_DECK_HOOK_SYNC=1 runs inline (tests).
set -euo pipefail

# tui_commit_touches_binary <sha> — true iff the commit changes anything the
# wisp-deck-tui binary is built from.
tui_commit_touches_binary() {
  git diff-tree --no-commit-id --name-only -r "$1" -- \
    "cmd/wisp-deck-tui" "internal" "go.mod" "go.sum" "VERSION" | grep -q .
}

# tui_build_and_install <sha> — export <sha> clean, build, install, sign, warm.
tui_build_and_install() {
  local sha="$1" work version
  work="$(mktemp -d "${TMPDIR:-/tmp}/wisp-tui-hook-build.XXXXXX")"
  # shellcheck disable=SC2064  # expand $work now; it is gone at trap time otherwise
  trap "rm -rf '$work'" RETURN
  git archive "$sha" | tar -x -C "$work"
  version="$(cat "$work/VERSION")"
  (
    cd "$work"
    go build -ldflags "-X main.Version=$version -X main.HostEffectsCapability=enabled -X main.SoundPreviewCapability=enabled" \
      -o "$work/wisp-deck-tui" ./cmd/wisp-deck-tui
  )
  mkdir -p "$HOME/.local/bin"
  cp "$work/wisp-deck-tui" "$HOME/.local/bin/wisp-deck-tui"
  codesign --sign - --force "$HOME/.local/bin/wisp-deck-tui"
  # Re-signing resets the Gatekeeper first-run assessment — pay it now, not
  # on the next modal open in a live session.
  "$HOME/.local/bin/wisp-deck-tui" --version >/dev/null 2>&1 || true
}

# tui_rebuild_loop <lock_dir> — build the current HEAD; repeat if HEAD moved
# while building. The lock dir is removed on exit however the loop ends.
tui_rebuild_loop() {
  local lock_dir="$1" sha
  # shellcheck disable=SC2064  # expand now, same reason as above
  trap "rm -rf '$lock_dir'" EXIT
  while :; do
    sha="$(git rev-parse HEAD)"
    tui_build_and_install "$sha"
    [ "$(git rev-parse HEAD)" = "$sha" ] && break
  done
}

tui_hook_main() {
  tui_commit_touches_binary HEAD || return 0

  local git_dir lock_dir owner
  git_dir="$(git rev-parse --git-dir)"
  lock_dir="$git_dir/wisp-tui-rebuild.lock"

  if ! mkdir "$lock_dir" 2>/dev/null; then
    # A builder is already running; it re-checks HEAD after each build, so
    # this commit is covered. Take over only if the owner died mid-build.
    owner="$(cat "$lock_dir/pid" 2>/dev/null || true)"
    if [ -n "$owner" ] && kill -0 "$owner" 2>/dev/null; then
      return 0
    fi
    rm -rf "$lock_dir"
    mkdir "$lock_dir" 2>/dev/null || return 0
  fi

  if [ "${WISP_DECK_HOOK_SYNC:-}" = "1" ]; then
    echo "$$" > "$lock_dir/pid"
    tui_rebuild_loop "$lock_dir"
  else
    # Background so the commit returns immediately; both streams must be
    # dropped (the hook's stderr can be a live AI pane).
    (
      echo "$BASHPID" > "$lock_dir/pid"
      tui_rebuild_loop "$lock_dir"
    ) >>"$git_dir/wisp-tui-rebuild.log" 2>&1 &
    disown
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  tui_hook_main "$@"
fi
