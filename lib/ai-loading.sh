#!/bin/bash
# AI pane startup indicator.
#
# claude 2.1.217 (and any AI CLI) can spend seconds — minutes when macOS's
# Security subsystem is contended — in startup work BEFORE it paints its UI. A
# freshly split AI pane is black during that window, which reads as "the agent
# failed to load" even though it is loading normally. This prints a one-line
# "Starting <tool>…" banner into the pane BEFORE the launch command runs; the
# tool's own alt-screen cleanly replaces it once it paints, and it exits with
# the tool if the tool exits. It costs nothing (a single printf) and never
# throttles the launch. See the memory claude-slow-startup-security-xpc.

# ai_pane_loading_prefix <tool> [accent_colour]
# Print a shell prefix (trailing ';') to prepend to the AI launch command. It
# clears the pane and prints the themed banner. <tool> is the AI tool name
# (claude/codex/opencode) — a fixed vocabulary, never user text.
ai_pane_loading_prefix() {
  local tool="$1" accent="${2:-209}"
  [ -n "$tool" ] || return 1
  case "$accent" in '' | *[!0-9]*) accent=209 ;; esac
  # \033[2J\033[H clears and homes; the spinner glyph + tool name sit on the
  # accent colour, then reset. \r\n keeps the cursor column at 0 for the pane's
  # subsequent shell prompt if the tool exits immediately.
  printf 'printf %s;' \
    "'\\033[2J\\033[H\\033[38;5;${accent}m  ◇ Starting ${tool}…\\033[0m\\r\\n'"
}
