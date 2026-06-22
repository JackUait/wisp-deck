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

# Get the latest release tag from a GitHub repo (e.g. "v1.2.3")
# Uses the /releases/latest redirect — no API key required.
get_latest_release_tag() {
  local repo="$1" tag
  tag="$(curl -fsSI "https://github.com/$repo/releases/latest" 2>/dev/null \
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
install_binary() {
  local url="$1" dest="$2" display_name="$3"
  info "Downloading $display_name..."
  mkdir -p "$(dirname "$dest")"
  if curl -fsSL -o "$dest" "$url"; then
    chmod +x "$dest"
    success "$display_name installed"
  else
    warn "Failed to download $display_name from $url"
    return 1
  fi
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
    "jq"
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
  if curl -fsSL -o "$tmp_dir/tmux.tar.gz" "$url"; then
    tar -xzf "$tmp_dir/tmux.tar.gz" -C "$tmp_dir" tmux
    mkdir -p "$HOME/.local/bin"
    mv "$tmp_dir/tmux" "$HOME/.local/bin/tmux"
    chmod +x "$HOME/.local/bin/tmux"
    success "tmux installed"
  else
    warn "Failed to install tmux"
    return 1
  fi
}

# Install lazygit from jesseduffield/lazygit GitHub releases.
ensure_lazygit() {
  if command -v lazygit &>/dev/null; then
    success "lazygit already installed"
    return 0
  fi
  local arch tag version tmp_dir url
  arch="$(detect_arch)" || return 1
  tag="$(get_latest_release_tag "jesseduffield/lazygit")" || return 1
  version="${tag#v}"
  tmp_dir="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_dir'" RETURN
  url="https://github.com/jesseduffield/lazygit/releases/download/${tag}/lazygit_${version}_darwin_${arch}.tar.gz"
  info "Downloading lazygit..."
  if curl -fsSL -o "$tmp_dir/lazygit.tar.gz" "$url"; then
    tar -xzf "$tmp_dir/lazygit.tar.gz" -C "$tmp_dir" lazygit
    mkdir -p "$HOME/.local/bin"
    mv "$tmp_dir/lazygit" "$HOME/.local/bin/lazygit"
    chmod +x "$HOME/.local/bin/lazygit"
    success "lazygit installed"
  else
    warn "Failed to install lazygit"
    return 1
  fi
}

# Install or update ghost-tab-tui by downloading the pre-built binary from the ghost-tab release.
# Args: share_dir (to read VERSION from)
# Checks installed binary version against VERSION file and re-downloads if mismatched.
ensure_ghost_tab_tui() {
  local share_dir="$1"

  local version
  version="$(tr -d '[:space:]' < "$share_dir/VERSION" 2>/dev/null)"
  if [[ -z "$version" ]]; then
    error "Cannot determine ghost-tab-tui version: VERSION file missing in $share_dir"
    return 1
  fi

  if command -v ghost-tab-tui &>/dev/null; then
    # Check if installed version matches expected version
    local installed_version
    installed_version="$(ghost-tab-tui --version 2>/dev/null | sed 's/.*version //' || echo "")"
    if [[ "$installed_version" == "$version" ]]; then
      success "ghost-tab-tui is up to date ($version)"
      return 0
    fi
    info "Updating ghost-tab-tui ($installed_version -> $version)..."
  fi

  local arch url
  arch="$(detect_arch)" || return 1
  url="https://github.com/JackUait/ghost-tab/releases/download/v${version}/ghost-tab-tui-darwin-${arch}"

  mkdir -p "$HOME/.local/bin"
  install_binary "$url" "$HOME/.local/bin/ghost-tab-tui" "ghost-tab-tui" || return 1
}

# Install base CLI requirements.
ensure_base_requirements() {
  ensure_jq
  ensure_tmux
  ensure_lazygit
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

# Ensure OpenCode is available via npx, removing any brew-installed version first.
ensure_opencode() {
  # Remove brew-installed opencode if present
  if brew list opencode &>/dev/null; then
    info "Removing brew-installed OpenCode..."
    brew uninstall opencode &>/dev/null || true
  fi

  if command -v npx &>/dev/null; then
    success "OpenCode ready (via npx)"
    return 0
  fi

  warn "OpenCode requires Node.js — install from https://nodejs.org"
}

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
