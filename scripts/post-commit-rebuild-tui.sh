#!/usr/bin/env bash
# Keep the install in sync with HEAD after each commit. The install is copies,
# not symlinks, so nothing committed reaches live panes on its own.
#
# - A commit touching the Go TUI's inputs rebuilds + installs
#   ~/.local/bin/wisp-deck-tui. Ledger panes only ever exec the installed
#   binary (bac1be4 sat uninstalled for 19 hours this way).
# - A commit touching a distribution entry runs scripts/sync-dev-install.sh,
#   which copies HEAD's bash into ~/.local/share/wisp-deck and marks it
#   .dev-install so an npm update cannot downgrade it.
#
# Both export HEAD with `git archive`: the shared checkout churns under
# concurrent sessions and must never be used dirty. Each job re-checks HEAD
# when done and runs again if another commit landed, so the newest one wins.
#
# Invoked by .githooks/post-commit. Runs in the background by default so
# commits don't stall; WISP_DECK_HOOK_SYNC=1 runs inline (tests).
set -euo pipefail

# shellcheck source=scripts/sync-dev-install.sh
source "$(dirname "${BASH_SOURCE[0]}")/sync-dev-install.sh"

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

# dev_install_commit_touches_dist <sha> — true iff the commit changes a file
# the npm package ships.
dev_install_commit_touches_dist() {
  git diff-tree --no-commit-id --name-only -r "$1" -- "${DIST_ENTRIES[@]}" | grep -q .
}

# dev_install_sync_loop <lock_dir> — sync the current HEAD; repeat if HEAD
# moved meanwhile. A failed sync (no install yet) is logged, not retried.
dev_install_sync_loop() {
  local lock_dir="$1" sha
  # shellcheck disable=SC2064  # expand now, same reason as above
  trap "rm -rf '$lock_dir'" EXIT
  while :; do
    sha="$(git rev-parse HEAD)"
    sync_dev_install_main || break
    [ "$(git rev-parse HEAD)" = "$sha" ] && break
  done
}

# hook_run_locked <name> <loop_fn> — run <loop_fn> <lock_dir> unless another
# run of <name> is alive; that one re-checks HEAD, so this commit is covered.
hook_run_locked() {
  local name="$1" loop_fn="$2" git_dir lock_dir owner
  git_dir="$(git rev-parse --git-dir)"
  lock_dir="$git_dir/$name.lock"

  if ! mkdir "$lock_dir" 2>/dev/null; then
    # Take over only if the owner died mid-run.
    owner="$(cat "$lock_dir/pid" 2>/dev/null || true)"
    if [ -n "$owner" ] && kill -0 "$owner" 2>/dev/null; then
      return 0
    fi
    rm -rf "$lock_dir"
    mkdir "$lock_dir" 2>/dev/null || return 0
  fi

  if [ "${WISP_DECK_HOOK_SYNC:-}" = "1" ]; then
    echo "$$" > "$lock_dir/pid"
    "$loop_fn" "$lock_dir"
  else
    # Background so the commit returns immediately; both streams must be
    # dropped (the hook's stderr can be a live AI pane).
    (
      echo "$BASHPID" > "$lock_dir/pid"
      "$loop_fn" "$lock_dir"
    ) >>"$git_dir/$name.log" 2>&1 &
    disown
  fi
}

post_commit_main() {
  if tui_commit_touches_binary HEAD; then
    hook_run_locked wisp-tui-rebuild tui_rebuild_loop
  fi
  if dev_install_commit_touches_dist HEAD; then
    hook_run_locked wisp-dev-install-sync dev_install_sync_loop
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  post_commit_main "$@"
fi
