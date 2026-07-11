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
resolve_opencode_cmd() {
  if command -v opencode &>/dev/null; then
    echo "opencode"
  elif command -v npx &>/dev/null; then
    # A cached copy wins over @latest: the registry's advertised latest can be
    # uninstallable (observed live: opencode-ai@latest -> 1.17.18, ETARGET),
    # and then every @latest launch dies at npm and dumps the pane to a bare
    # shell while a working cached copy sits unused.
    if npx --no-install opencode-ai --version >/dev/null 2>&1; then
      echo "npx --no-install opencode-ai"
    else
      echo "npx --prefer-offline opencode-ai@latest"
    fi
  fi
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
