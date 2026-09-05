#!/bin/bash
set -euo pipefail

generate_release_notes() {
  local from_tag="${1:-}"
  local to_tag="$2"

  # Get commit messages between tags (or all commits up to to_tag)
  local log_range
  if [[ -n "$from_tag" ]]; then
    log_range="${from_tag}..${to_tag}"
  else
    log_range="$to_tag"
  fi

  local commits
  commits="$(git log --oneline --no-merges "$log_range" | sed 's/^[a-f0-9]* //')"

  # Arrays for each category
  local -a feats=()
  local -a fixes=()
  local -a refactors=()
  local -a others=()

  while IFS= read -r msg; do
    [[ -z "$msg" ]] && continue

    # Filter out version bump commits
    if [[ "$msg" =~ ^Bump\ version\ to ]]; then
      continue
    fi

    # Filter out merge commits (shouldn't appear with --no-merges, but safety)
    if [[ "$msg" =~ ^Merge\ pull\ request ]]; then
      continue
    fi

    # Filter out revert commits
    if [[ "$msg" =~ ^Revert\  ]]; then
      continue
    fi

    # Filter out design docs and implementation plans (unprefixed)
    if [[ "$msg" =~ (design\ doc|implementation\ plan|design\ for) ]]; then
      continue
    fi

    # Parse conventional commit prefix: type(scope): message  OR  type: message
    # Store regex in variable to avoid bash parsing issues with brackets/parens
    local pattern='^(feat|fix|refactor|test|docs|chore|style|ci|build|cleanup)(\([^)]*\))?: (.+)$'
    if [[ "$msg" =~ $pattern ]]; then
      local type="${BASH_REMATCH[1]}"
      local description="${BASH_REMATCH[3]}"

      # Filter scaffolding commits (module init, directory structure, .gitkeep, missing deps)
      if [[ "$description" =~ (initialize .* module|directory structure|\.gitkeep|missing .* dependencies) ]]; then
        continue
      fi

      # Capitalize first letter
      description="$(echo "${description:0:1}" | tr '[:lower:]' '[:upper:]')${description:1}"

      case "$type" in
        feat) feats+=("$description") ;;
        fix) fixes+=("$description") ;;
        refactor) refactors+=("$description") ;;
        # test, docs, chore, style, ci, build, cleanup → skip
        *) ;;
      esac
    else
      # Unprefixed commit — goes to Other Changes
      # Capitalize first letter for consistency
      local capitalized
      capitalized="$(echo "${msg:0:1}" | tr '[:lower:]' '[:upper:]')${msg:1}"
      others+=("$capitalized")
    fi
  done < <(printf '%s\n' "$commits")

  # Output sections in order: Features, Bug Fixes, Refactoring, Other Changes
  local has_output=false

  if [[ ${#feats[@]} -gt 0 ]]; then
    [[ "$has_output" == true ]] && echo ""
    echo "## Features"
    for item in "${feats[@]}"; do
      echo "- $item"
    done
    has_output=true
  fi

  if [[ ${#fixes[@]} -gt 0 ]]; then
    [[ "$has_output" == true ]] && echo ""
    echo "## Bug Fixes"
    for item in "${fixes[@]}"; do
      echo "- $item"
    done
    has_output=true
  fi

  if [[ ${#refactors[@]} -gt 0 ]]; then
    [[ "$has_output" == true ]] && echo ""
    echo "## Refactoring"
    for item in "${refactors[@]}"; do
      echo "- $item"
    done
    has_output=true
  fi

  if [[ ${#others[@]} -gt 0 ]]; then
    [[ "$has_output" == true ]] && echo ""
    echo "## Other Changes"
    for item in "${others[@]}"; do
      echo "- $item"
    done
    has_output=true
  fi
}

# Only run when executed directly (not sourced for testing)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  generate_release_notes "${1:-}" "${2:-}"
fi
