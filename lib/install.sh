#!/bin/bash
# Package installation helpers for the installer.

# Detect CPU architecture: outputs "arm64" or "x86_64"
detect_arch() {
  local arch
  arch="$(uname -m)"
  case "$arch" in
    arm64)   echo "arm64" ;;
    x86_64)  echo "x86_64" ;;
    *)
      error "Unsupported architecture: $arch"
      return 1 ;;
  esac
}

# Shared curl hardening for every download the installer performs: retry
# transient failures and bound connection hangs so one network blip doesn't
# fail (or freeze) the whole install. No --max-time: binary downloads on slow
# links may legitimately take minutes.
CURL_RELIABILITY_OPTS=(--retry 3 --retry-delay 1 --connect-timeout 15)

# Get the latest release tag from a GitHub repo (e.g. "v1.2.3")
# Uses the /releases/latest redirect — no API key required.
get_latest_release_tag() {
  local repo="$1" tag
  tag="$(curl -fsSI "${CURL_RELIABILITY_OPTS[@]}" "https://github.com/$repo/releases/latest" 2>/dev/null \
    | grep -i '^location:' \
    | sed 's|.*/tag/||' \
    | tr -d '[:space:]\r')"
  if [[ -z "$tag" ]]; then
    error "Failed to fetch release tag for $repo"
    return 1
  fi
  echo "$tag"
}

# Download a binary from $url to $dest and make it executable.
# The download lands in a temp file and only replaces $dest after it succeeds
# (and passes $verify_cmd, when given), so a failed, truncated, or wrong
# download never clobbers a working binary.
# Usage: install_binary url dest display_name [verify_cmd]
#   verify_cmd is invoked with the downloaded temp path appended; a non-zero
#   exit rejects the download and keeps the existing install untouched.
install_binary() {
  local url="$1" dest="$2" display_name="$3" verify_cmd="${4:-}"
  info "Downloading $display_name..."
  mkdir -p "$(dirname "$dest")"
  local tmp="$dest.download.$$"
  if ! curl -fsSL "${CURL_RELIABILITY_OPTS[@]}" -o "$tmp" "$url"; then
    rm -f "$tmp"
    warn "Failed to download $display_name from $url"
    return 1
  fi
  chmod +x "$tmp"
  if [ -n "$verify_cmd" ] && ! $verify_cmd "$tmp" >/dev/null 2>&1; then
    rm -f "$tmp"
    warn "Downloaded $display_name failed verification — keeping existing install"
    return 1
  fi
  mv -f "$tmp" "$dest"
  success "$display_name installed"
}

# Verifier for install_binary: the downloaded file must at least run.
verify_binary_runs() {
  "$1" --version >/dev/null 2>&1
}

# Verifier for install_binary: wisp-deck-tui must run AND report the version
# we asked for (catches a stale CDN or mis-published release asset).
# Usage (via install_binary): verify_wisp_deck_tui_version <expected> <path>
verify_wisp_deck_tui_version() {
  local expected="$1" bin="$2" reported
  reported="$("$bin" --version 2>/dev/null)" || return 1
  [[ "$reported" == *"$expected"* ]]
}

# Install jq from jqlang/jq GitHub releases.
ensure_jq() {
  if command -v jq &>/dev/null; then
    success "jq already installed"
    return 0
  fi
  local arch jq_arch
  arch="$(detect_arch)" || return 1
  case "$arch" in
    arm64)   jq_arch="macos-arm64" ;;
    x86_64)  jq_arch="macos-amd64" ;;
  esac
  install_binary \
    "https://github.com/jqlang/jq/releases/latest/download/jq-${jq_arch}" \
    "$HOME/.local/bin/jq" \
    "jq" \
    verify_binary_runs
}

# Install tmux from tmux/tmux-builds GitHub releases.
ensure_tmux() {
  if command -v tmux &>/dev/null; then
    success "tmux already installed"
    return 0
  fi
  local arch tag version tmp_dir url
  arch="$(detect_arch)" || return 1
  tag="$(get_latest_release_tag "tmux/tmux-builds")" || return 1
  version="${tag#v}"
  tmp_dir="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_dir'" RETURN
  url="https://github.com/tmux/tmux-builds/releases/download/${tag}/tmux-${version}-macos-${arch}.tar.gz"
  info "Downloading tmux..."
  if curl -fsSL "${CURL_RELIABILITY_OPTS[@]}" -o "$tmp_dir/tmux.tar.gz" "$url" \
    && tar -xzf "$tmp_dir/tmux.tar.gz" -C "$tmp_dir" tmux 2>/dev/null \
    && mkdir -p "$HOME/.local/bin" \
    && mv "$tmp_dir/tmux" "$HOME/.local/bin/tmux" \
    && chmod +x "$HOME/.local/bin/tmux"; then
    success "tmux installed"
  else
    warn "Failed to install tmux"
    return 1
  fi
}

# Install or update wisp-deck-tui by downloading the pre-built binary from the wisp-deck release.
# Args: share_dir (to read VERSION from)
# Checks installed binary version against VERSION file and re-downloads if mismatched.
ensure_wisp_deck_tui() {
  local share_dir="$1"

  local version
  # Grouped so stderr is closed before the open: a missing VERSION is a case this
  # handles below, and it must not also spew the shell's own "No such file" first.
  { version="$(tr -d '[:space:]' < "$share_dir/VERSION")"; } 2>/dev/null || version=""
  if [[ -z "$version" ]]; then
    error "Cannot determine wisp-deck-tui version: VERSION file missing in $share_dir"
    return 1
  fi

  if command -v wisp-deck-tui &>/dev/null; then
    # Check if installed version matches expected version
    local installed_version
    installed_version="$(wisp-deck-tui --version 2>/dev/null | sed 's/.*version //' || echo "")"
    if [[ "$installed_version" == "$version" ]]; then
      success "wisp-deck-tui is up to date ($version)"
      return 0
    fi
    info "Updating wisp-deck-tui ($installed_version -> $version)..."
  fi

  local arch url
  arch="$(detect_arch)" || return 1
  url="https://github.com/JackUait/wisp-deck/releases/download/v${version}/wisp-deck-tui-darwin-${arch}"

  mkdir -p "$HOME/.local/bin"
  install_binary "$url" "$HOME/.local/bin/wisp-deck-tui" "wisp-deck-tui" \
    "verify_wisp_deck_tui_version $version" || return 1
  # Exec the fresh binary once so its first-run Gatekeeper assessment (~1s,
  # more under load) happens now — not on the first modal open in a session.
  "$HOME/.local/bin/wisp-deck-tui" --version >/dev/null 2>&1 || true
}

# Install base CLI requirements. Both are load-bearing at runtime — the
# statusline and settings code shell out to jq, and the session IS a tmux
# session — so a failure must fail the install rather than let setup continue
# and break later, far from the cause. jq's failure used to be swallowed: the
# function returned ensure_tmux's status. Both still run even when the first
# fails, so one run reports every missing dependency.
ensure_base_requirements() {
  local rc=0
  ensure_jq || rc=1
  ensure_tmux || rc=1
  return "$rc"
}

# Install a Homebrew cask if the .app isn't in /Applications.
# Usage: ensure_cask "cask_name" "AppDisplayName"
# Respects APPLICATIONS_DIR env var for testing (defaults to /Applications).
ensure_cask() {
  local cask="$1" app_name="$2"
  local app_dir="${APPLICATIONS_DIR:-/Applications}"
  if [ -d "${app_dir}/${app_name}.app" ]; then
    success "$app_name found"
  else
    info "Installing $app_name..."
    if brew install --cask "$cask"; then
      success "$app_name installed"
    else
      error "$app_name installation failed."
      info "Install manually or run: brew install --cask $cask"
      return 1
    fi
  fi
}

# Install a complete Nerd Font (Hack) so the statusline metric icons (context/
# memory/CPU glyphs) render in terminals that don't ship Nerd Font fallback.
# Ghostty and WezTerm bundle their own symbols; kitty (via symbol_map) and iTerm2
# (via the non-ASCII font) need this font on the system. A COMPLETE font — not a
# symbols-only one — is used so iTerm2, which routes ALL non-ASCII text to the
# non-ASCII font, still has box-drawing and accented glyphs and doesn't have to
# fall back. Non-fatal: a failure only degrades the icons to tofu, so setup
# continues. Respects FONTS_DIR for testing (defaults to ~/Library/Fonts, where
# Homebrew installs font casks).
ensure_nerd_font() {
  local fonts_dir="${FONTS_DIR:-$HOME/Library/Fonts}"
  if ls "$fonts_dir"/*HackNerdFont* >/dev/null 2>&1; then
    success "Nerd Font found"
    return 0
  fi

  info "Installing Nerd Font..."
  if brew install --cask font-hack-nerd-font; then
    success "Nerd Font installed"
  else
    warn "Failed to install Nerd Font — statusline icons may show as boxes in kitty/iTerm2"
  fi
  return 0
}

# Ensure OpenCode is available, preferring a directly-installed binary.
#
# A real `opencode` binary launches ~3x faster than `npx opencode-ai@latest`,
# which revalidates against the npm registry on every launch and reinstalls the
# whole package (~46s) on every version bump. So install it globally via npm when
# possible; fall back to npx (the wrapper uses --prefer-offline) only when a
# global install is unavailable or fails. Any brew-installed copy is removed
# first so npm is the single source of truth.
ensure_opencode() {
  # Remove brew-installed opencode if present
  if brew list opencode &>/dev/null; then
    info "Removing brew-installed OpenCode..."
    brew uninstall opencode &>/dev/null || true
  fi

  if command -v opencode &>/dev/null; then
    success "OpenCode already installed"
    return 0
  fi

  if command -v npm &>/dev/null; then
    info "Installing OpenCode..."
    if npm install -g opencode-ai &>/dev/null; then
      success "OpenCode installed"
      return 0
    fi
    warn "Global OpenCode install failed — falling back to npx (slower launches)"
  fi

  if command -v npx &>/dev/null; then
    success "OpenCode ready (via npx)"
    return 0
  fi

  warn "OpenCode requires Node.js — install from https://nodejs.org"
}

# Ensure the Codex CLI is available.
#
# Unlike OpenCode there is no npx fallback: wrapper.sh detects codex with a plain
# `command -v codex`, so a codex that only exists behind npx would never be
# offered as a tool. Install it globally or not at all.
ensure_codex() {
  if command -v codex &>/dev/null; then
    success "Codex already installed"
    return 0
  fi

  if command -v npm &>/dev/null; then
    info "Installing Codex..."
    if npm install -g @openai/codex &>/dev/null; then
      # Under lazy-nvm setups npm's global prefix bin is off PATH, so the
      # launcher npm just wrote can be invisible to `command -v`. Link it into
      # ~/.local/bin (on PATH wherever wisp-deck runs) before declaring victory.
      if ! command -v codex &>/dev/null; then
        local npm_codex
        npm_codex="$(npm prefix -g 2>/dev/null)/bin/codex"
        if [ -x "$npm_codex" ]; then
          mkdir -p "$HOME/.local/bin"
          ln -sf "$npm_codex" "$HOME/.local/bin/codex"
        fi
      fi
      if command -v codex &>/dev/null; then
        success "Codex installed"
        return 0
      fi
      warn "Codex installed by npm but not reachable on PATH"
      return 1
    fi
    warn "Global Codex install failed"
    return 1
  fi

  warn "Codex requires Node.js — install from https://nodejs.org"
  return 1
}

# Uninstall an npm-installed tool and clean up the ~/.local/bin launcher link
# ensure_* may have created (only when it points into npm's global prefix —
# never delete a launcher the user put there themselves).
_remove_npm_tool() {
  local tool="$1" pkg="$2" display="$3"
  if ! command -v npm &>/dev/null; then
    warn "npm not found — cannot remove $display"
    return 1
  fi
  info "Removing $display..."
  if ! npm uninstall -g "$pkg" &>/dev/null; then
    warn "Failed to remove $display"
    return 1
  fi
  local link="$HOME/.local/bin/$tool" npm_prefix
  npm_prefix="$(npm prefix -g 2>/dev/null)"
  if [ -L "$link" ] && [ -n "$npm_prefix" ]; then
    case "$(readlink "$link")" in
      "$npm_prefix"/*) rm -f "$link" ;;
    esac
  fi
  success "$display removed"
}

remove_opencode() { _remove_npm_tool "opencode" "opencode-ai" "OpenCode"; }
remove_codex()    { _remove_npm_tool "codex" "@openai/codex" "Codex"; }

# Install a command-line tool if not already on PATH.
# Usage: ensure_command "cmd" "install_cmd" "post_msg" "display_name"
ensure_command() {
  local cmd="$1" install_cmd="$2" post_msg="$3" display_name="$4"
  if command -v "$cmd" &>/dev/null; then
    success "$display_name already installed"
  else
    info "Installing $display_name..."
    if eval "$install_cmd"; then
      success "$display_name installed"
      [ -n "$post_msg" ] && info "$post_msg"
    else
      warn "$display_name installation failed — install manually: $install_cmd"
    fi
  fi
}
