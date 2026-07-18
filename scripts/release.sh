#!/bin/bash
set -euo pipefail

# --- Preflight check functions ---

check_clean_tree() {
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "Error: Working tree is not clean. Commit or stash changes first." >&2
    return 1
  fi
}

check_main_branch() {
  local branch
  branch="$(git rev-parse --abbrev-ref HEAD)"
  if [[ "$branch" != "main" ]]; then
    echo "Error: Must be on main branch (currently on '$branch')." >&2
    return 1
  fi
}

read_version() {
  local version_file="$1"
  if [[ ! -f "$version_file" ]]; then
    echo "Error: VERSION file not found at $version_file" >&2
    return 1
  fi
  local version
  version="$(tr -d '[:space:]' < "$version_file")"
  if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: VERSION '$version' is not valid semver (expected X.Y.Z)" >&2
    return 1
  fi
  echo "$version"
}

check_tag_not_exists() {
  local tag="$1"
  if git rev-parse "$tag" &>/dev/null; then
    echo "Error: Tag $tag already exists." >&2
    return 1
  fi
}

check_gh_auth() {
  if ! command -v gh &>/dev/null; then
    echo "Error: gh CLI is not installed. Install with: brew install gh" >&2
    return 1
  fi
  if ! gh auth status &>/dev/null; then
    echo "Error: gh CLI is not authenticated. Run: gh auth login" >&2
    return 1
  fi
}

# Prove the package we are about to publish can actually be installed. v2.22.0
# shipped an installer that symlinked ~/.local/bin/wisp-deck at a file npm never
# published: `ln -sf` created a dangling link, so setup reported success while
# the command was dead. These tests pack the tarball, install it into an empty
# HOME, and fail on exactly that. Set RELEASE_SKIP_INSTALL_CHECK=1 to bypass.
check_install_verified() {
  local project_dir="$1" out
  [[ "${RELEASE_SKIP_INSTALL_CHECK:-}" == "1" ]] && return 0
  if ! out="$(cd "$project_dir" && go test ./test/npx/... -count=1 2>&1)"; then
    echo "$out" >&2
    return 1
  fi
}

# --- Main orchestration ---

# Script-level variable so the EXIT trap can clean up after main() returns.
build_dir=""
trap '[[ -n "$build_dir" ]] && rm -rf "$build_dir"' EXIT

main() {
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  local project_dir="$script_dir/.."
  local version_file="${RELEASE_VERSION_FILE:-$project_dir/VERSION}"
  local yes_flag=false

  # Parse args
  for arg in "$@"; do
    case "$arg" in
      --yes|-y) yes_flag=true ;;
    esac
  done

  # Preflight checks
  echo "Running preflight checks..."

  check_clean_tree
  echo "  ✓ Working tree is clean"

  check_main_branch
  echo "  ✓ On main branch"

  local version
  version="$(read_version "$version_file")"
  echo "  ✓ Version: $version"

  local tag="v$version"
  check_tag_not_exists "$tag"
  echo "  ✓ Tag $tag does not exist"

  check_gh_auth
  echo "  ✓ gh CLI authenticated"

  if check_install_verified "$project_dir"; then
    echo "  ✓ Install verification passed"
  else
    echo "Error: install verification failed — refusing to publish a package users cannot install" >&2
    exit 1
  fi

  echo ""

  # Confirmation
  if [[ "$yes_flag" != true ]]; then
    echo "Release $tag?"
    echo "  - Create annotated tag $tag"
    echo "  - Push to origin"
    echo "  - Create GitHub release with binaries attached"
    echo ""
    read -rp "Proceed? [y/N] " confirm
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
      echo "Aborted."
      exit 0
    fi
  fi

  # Build wisp-deck-tui binaries into a temp directory with correct filenames.
  # gh release create uses the file's basename as the asset download name,
  # so files must be named wisp-deck-tui-darwin-{arch} (not mktemp names).
  echo "Building wisp-deck-tui binaries..."
  build_dir="$(mktemp -d)"

  local ldflags="-X main.Version=$version -X main.HostEffectsCapability=enabled -X main.SoundPreviewCapability=enabled"
  (cd "$project_dir" && GOOS=darwin GOARCH=arm64 go build -ldflags "$ldflags" -o "$build_dir/wisp-deck-tui-darwin-arm64" ./cmd/wisp-deck-tui) || {
    echo "Error: failed to build wisp-deck-tui for arm64" >&2; exit 1
  }
  (cd "$project_dir" && GOOS=darwin GOARCH=amd64 go build -ldflags "$ldflags" -o "$build_dir/wisp-deck-tui-darwin-amd64" ./cmd/wisp-deck-tui) || {
    echo "Error: failed to build wisp-deck-tui for amd64" >&2; exit 1
  }
  codesign --sign - --force "$build_dir/wisp-deck-tui-darwin-arm64"
  codesign --sign - --force "$build_dir/wisp-deck-tui-darwin-amd64"
  echo "  ✓ Built wisp-deck-tui for darwin/arm64 and darwin/amd64"

  # Tag and push
  echo ""
  echo "Creating tag $tag..."
  git tag -a "$tag" -m "Release $tag"
  echo "Pushing to origin..."
  git push origin main --tags

  # Create GitHub release
  echo "Creating GitHub release..."
  local prev_tag
  prev_tag="$(git describe --tags --abbrev=0 "${tag}^" 2>/dev/null || echo "")"
  local notes
  notes="$(bash "$script_dir/generate-release-notes.sh" "$prev_tag" "$tag")"
  gh release create "$tag" --notes "$notes" \
    "$build_dir/wisp-deck-tui-darwin-arm64" \
    "$build_dir/wisp-deck-tui-darwin-amd64"

  # Sync version to package.json and publish to npm
  if [[ -f "$project_dir/package.json" ]] && command -v npm &>/dev/null; then
    echo "Publishing to npm..."
    local npm_token=""
    if [[ -f "$project_dir/.env" ]]; then
      npm_token="$(grep '^NPM_PUBLISH_TOKEN=' "$project_dir/.env" | cut -d= -f2- | tr -d '[:space:]' || true)"
    fi
    local publish_cmd="npm publish"
    if [[ -n "$npm_token" ]]; then
      publish_cmd="npm publish --//registry.npmjs.org/:_authToken=$npm_token"
    fi
    (cd "$project_dir" && npm version "$version" --no-git-tag-version --allow-same-version && $publish_cmd) && \
      echo "  ✓ Published wisp-deck@$version to npm" || \
      echo "  ⚠ npm publish failed (GitHub release still succeeded)"
  fi

  # Update local binary so the developer sees changes immediately
  echo "Updating local binary..."
  local local_bin="$HOME/.local/bin/wisp-deck-tui"
  if [[ -d "$(dirname "$local_bin")" ]]; then
    (cd "$project_dir" && go build -ldflags "$ldflags" -o "$local_bin" ./cmd/wisp-deck-tui) && \
      codesign --sign - --force "$local_bin" && \
      echo "  ✓ Updated $local_bin" || \
      echo "  ⚠ Failed to update local binary (release still succeeded)"
    # Re-signing resets the Gatekeeper first-run assessment (~1s stall on the
    # next exec) — pay it here so the next modal open in a live session is warm.
    "$local_bin" --version >/dev/null 2>&1 || true
  fi

  echo ""
  echo "✓ Release $tag complete!"
  echo "  GitHub: https://github.com/JackUait/wisp-deck/releases/tag/$tag"
  echo "  Binaries: wisp-deck-tui-darwin-arm64, wisp-deck-tui-darwin-amd64"
}

# Only run main when executed directly (not sourced for testing)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
