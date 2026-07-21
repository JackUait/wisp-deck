#!/bin/bash
input=$(cat)
cwd=$(echo "$input" | sed -n 's/.*"current_dir":"\([^"]*\)".*/\1/p')

# The segment names the checked-out BRANCH (it moved here from the ledger
# header); the project name is already on the tab title. Outside a repo — or on
# a detached HEAD with no reachable SHA — fall back to the directory name so the
# segment is never empty.
label=$(git -C "$cwd" symbolic-ref --short -q HEAD 2>/dev/null)
if [ -z "$label" ]; then
  label=$(git -C "$cwd" rev-parse --short HEAD 2>/dev/null)
fi
if [ -z "$label" ]; then
  label=$(basename "$cwd")
fi

# A Nerd Font file-tree glyph (󰙅) symbolizes the worktree, prefixing the label.
# Literal UTF-8 is embedded directly: this runs under macOS bash 3.2 (--posix),
# whose printf has no \u/\U escape support.
printf '\033[01;36m󰙅 %s\033[00m' "$label"
