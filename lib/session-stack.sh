#!/bin/bash
# Per-project session stacking. One Ghostty tab per project, holding a stack
# of full wisp sessions switchable via keybindings; the outer status bar shows
# the stack. See docs/superpowers/specs/2026-07-21-per-project-session-stacking-design.md.
#
# Everything here is fail-open: helpers exit 0 and print nothing when the tmux
# server is gone or data is missing, and callers treat that as "no stack".
# Session names may contain spaces — iterate line-wise, never word-split.

# stack_sessions_for_project <tmux_cmd> <project_dir> [exclude_session]
# Print the names of live wisp sessions whose WISP_DECK_PATH equals
# <project_dir>, one per line, oldest first. Tmux-only: this runs on the
# launch critical path.
stack_sessions_for_project() {
  local tmux_cmd="$1" project_dir="$2" exclude="${3:-}"
  local created s
  # shellcheck disable=SC2034
  "$tmux_cmd" list-sessions -F '#{session_created} #{session_name}' 2>/dev/null \
    | sort -n | while IFS=' ' read -r created s; do
      [ -n "$s" ] || continue
      [ "$s" = "$exclude" ] && continue
      "$tmux_cmd" show-environment -t "$s" WISP_DECK_PATH 2>/dev/null \
        | grep -qxF "WISP_DECK_PATH=$project_dir" || continue
      printf '%s\n' "$s"
    done
  return 0
}
