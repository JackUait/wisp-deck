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
resolve_opencode_cmd() {
  if command -v opencode &>/dev/null; then
    echo "opencode"
  elif command -v npx &>/dev/null; then
    echo "npx --prefer-offline opencode-ai@latest"
  fi
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
