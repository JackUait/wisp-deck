#!/bin/bash
# AI tool helper functions — pure, no side effects on source.

# Resolve the command used to launch OpenCode, optimized for launch speed.
#
# `npx opencode-ai@latest` revalidates against the npm registry on every launch
# (~6s warm) and reinstalls the whole package whenever a new version is published
# (~46s). A directly-installed `opencode` binary launches in ~2s with no npm work,
# so it is preferred whenever present. When only npx is available, the fallback
# adds --prefer-offline so the existing npx cache is reused (skipping the registry
# round-trip and the periodic reinstall) instead of `@latest` re-fetching.
#
# Echoes the launch command, or empty when neither opencode nor npx is on PATH.
# opencode_available — exit 0 when OpenCode can be launched at all, WITHOUT
# deciding how. Deliberately cheap: a PATH lookup, no subprocess.
#
# This exists because resolve_opencode_cmd's npx branch spawns node (6-13s
# measured, warm cache) and wrapper.sh used to call it before the project picker
# painted — every launch, every tool, paying seconds to answer a question only an
# OpenCode launch asks. The probe never decided availability anyway: it only
# picks BETWEEN the two npx strings, both non-empty. So availability is exactly
# "opencode or npx on PATH", which costs nothing. Pinned by
# TestOpencodeAvailable_agrees_with_resolve_opencode_cmd.
opencode_available() {
  command -v opencode &>/dev/null || command -v npx &>/dev/null
}

# resolve_opencode_cmd — the launch command itself. The npx branch is the
# expensive one; call it only when OpenCode is actually being launched, never on
# the path to the picker.
#
# The --no-install probe spawns node (6-13s measured, warm cache), so a
# successful verdict is cached: the probe passes exactly when npx holds a
# cached opencode-ai copy, so the verdict is recorded alongside the
# package.json of that copy. While the file still exists later launches skip
# the probe; once it is gone (npm cache cleared, copy replaced) the record is
# stale and the probe runs again — never launch a command whose backing copy
# has vanished. Only the success verdict is cached: a failed probe means
# nothing is installed yet, and the prefer-offline launch that follows will
# install a copy, changing the right answer for the very next launch.
resolve_opencode_cmd() {
  # May run under zsh (mid-session tool switch from the compact-view pane),
  # where an unmatched glob is fatal by default. Confined to this function.
  [ -n "${ZSH_VERSION:-}" ] && setopt local_options no_nomatch 2>/dev/null
  if command -v opencode &>/dev/null; then
    echo "opencode"
    return 0
  fi
  command -v npx &>/dev/null || return 0
  local verdict_file="${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck/opencode-npx-verdict"
  local pkg=""
  if [ -f "$verdict_file" ]; then
    { IFS= read -r pkg < "$verdict_file"; } 2>/dev/null || pkg=""
    if [ -n "$pkg" ] && [ -f "$pkg" ]; then
      echo "npx --no-install opencode-ai"
      return 0
    fi
    rm -f "$verdict_file" 2>/dev/null
  fi
  # A cached copy wins over @latest: the registry's advertised latest can be
  # uninstallable (observed live: opencode-ai@latest -> 1.17.18, ETARGET),
  # and then every @latest launch dies at npm and dumps the pane to a bare
  # shell while a working cached copy sits unused.
  if npx --no-install opencode-ai --version >/dev/null 2>&1; then
    for pkg in "${NPM_CONFIG_CACHE:-$HOME/.npm}"/_npx/*/node_modules/opencode-ai/package.json; do
      [ -f "$pkg" ] || continue
      mkdir -p "${verdict_file%/*}" 2>/dev/null || break
      printf '%s\n' "$pkg" > "$verdict_file" 2>/dev/null || true
      break
    done
    echo "npx --no-install opencode-ai"
  else
    echo "npx --prefer-offline opencode-ai@latest"
  fi
}

# Ghostty launches wrapper.sh through `bash -l`, which never reads a zsh
# user's ~/.zshrc — so a claude that lives on a PATH built there (an npm
# install under nvm/volta/asdf, the legacy ~/.claude/local alias install, a
# custom npm prefix) is invisible to `command -v` here even though it runs
# fine in the user's own terminal. That shipped as "Claude Code is not
# installed", with no way out, on a machine where it was installed.
#
# resolve_claude_cmd answers "where is claude?" without ever spawning a
# language runtime (this runs on the launch critical path): PATH first, then
# the location setup cached from the user's real shell, then the well-known
# install homes.
# Usage: resolve_claude_cmd [cache_file]
# Echoes the absolute path; exit 1 when claude is nowhere to be found.
resolve_claude_cmd() {
  # May run under zsh, where an unmatched glob is fatal by default.
  [ -n "${ZSH_VERSION:-}" ] && setopt local_options no_nomatch 2>/dev/null
  local _c
  if _c="$(command -v claude 2>/dev/null)" && [ -n "$_c" ]; then
    echo "$_c"
    return 0
  fi
  # Setup (bin/wisp-deck) runs in the user's real shell and records what
  # `command -v claude` said there. Trust it only while it still points at an
  # executable — an nvm claude vanishes with its node version.
  local cache_file="${1:-}"
  if [ -n "$cache_file" ] && [ -f "$cache_file" ]; then
    { IFS= read -r _c < "$cache_file"; } 2>/dev/null || _c=""
    if [ -n "$_c" ] && [ -x "$_c" ]; then
      echo "$_c"
      return 0
    fi
  fi
  for _c in "$HOME/.claude/local/claude" "$HOME/.volta/bin/claude" "$HOME/.asdf/shims/claude"; do
    if [ -x "$_c" ]; then
      echo "$_c"
      return 0
    fi
  done
  # nvm keeps one global bin per node version; prefer the newest version that
  # has claude (version order — lexically v9 sorts after v22).
  local _best
  _best="$(for _c in "${NVM_DIR:-$HOME/.nvm}/versions/node"/*/bin/claude; do
    [ -x "$_c" ] && printf '%s\n' "$_c"
  done | sort -V | tail -n 1)"
  if [ -n "$_best" ]; then
    echo "$_best"
    return 0
  fi
  return 1
}

# activate_claude_cmd — resolve claude AND make the find launchable: sets
# CLAUDE_CMD, and when claude was found off-PATH prepends its bin dir so
# `claude` resolves for everything downstream — the Go menu's own detection
# (exec.LookPath), the tmux panes that exec claude, and, for npm installs,
# the sibling `node` its shebang needs.
# Usage: activate_claude_cmd [cache_file]
activate_claude_cmd() {
  CLAUDE_CMD="$(resolve_claude_cmd "${1:-}")" || CLAUDE_CMD=""
  if [ -n "$CLAUDE_CMD" ] && ! command -v claude &>/dev/null; then
    PATH="${CLAUDE_CMD%/*}:$PATH"
    export PATH
  fi
}

# cache_claude_cmd — record where the user's own shell finds claude. Setup is
# the only wisp-deck process that runs in that shell (the only place an
# nvm/volta PATH is loaded), so this is the one moment the answer is knowable.
# No claude on PATH writes nothing: a wrong cache is worse than none.
# Usage: cache_claude_cmd <cache_file>
cache_claude_cmd() {
  local cache_file="$1" _c
  _c="$(command -v claude 2>/dev/null)" || return 0
  [ -n "$_c" ] || return 0
  mkdir -p "$(dirname "$cache_file")" 2>/dev/null || return 0
  printf '%s\n' "$_c" > "$cache_file" 2>/dev/null || true
}

# Map a tool identifier onto the command that launches it.
# Usage: resolve_ai_tool_cmd <tool> <claude_cmd> <opencode_cmd> <codex_cmd>
# Unknown identifiers resolve to claude, matching every other per-tool switch.
resolve_ai_tool_cmd() {
  case "${1:-}" in
    opencode) echo "${3:-}" ;;
    codex)    echo "${4:-}" ;;
    *)        echo "${2:-}" ;;
  esac
}

# Filter a tool list against the disabled-tools file (one name per line).
# Usage: filter_disabled_ai_tools <disabled_file> <tool>...
# Echoes surviving tools one per line. If filtering would leave nothing,
# the disable list is ignored so a launch can never end up tool-less.
filter_disabled_ai_tools() {
  local disabled_file="$1"; shift
  local survivors=() _t
  for _t in "$@"; do
    if [ -f "$disabled_file" ] && grep -qx "$_t" "$disabled_file" 2>/dev/null; then
      continue
    fi
    survivors+=("$_t")
  done
  if [ ${#survivors[@]} -eq 0 ] && [ $# -gt 0 ]; then
    echo "wisp-deck: all AI tools disabled — ignoring disable list" >&2
    printf '%s\n' "$@"
    return 0
  fi
  printf '%s\n' "${survivors[@]}"
}

# Validates SELECTED_AI_TOOL against AI_TOOLS_AVAILABLE.
# Falls back to first available if current selection is invalid.
# Optional arg $1: path to preference file (writes corrected value if provided).
validate_ai_tool() {
  local _valid=0 _t
  for _t in "${AI_TOOLS_AVAILABLE[@]}"; do
    [ "$_t" == "$SELECTED_AI_TOOL" ] && _valid=1
  done
  if [ "$_valid" -eq 0 ] && [ ${#AI_TOOLS_AVAILABLE[@]} -gt 0 ]; then
    SELECTED_AI_TOOL="${AI_TOOLS_AVAILABLE[0]}"
    if [ -n "${1:-}" ]; then
      mkdir -p "$(dirname "$1")"
      echo "$SELECTED_AI_TOOL" > "$1"
    fi
  fi
}
