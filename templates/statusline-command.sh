#!/bin/bash
input=$(cat)
cwd=$(echo "$input" | sed -n 's/.*"current_dir":"\([^"]*\)".*/\1/p')

# A Nerd Font file-tree glyph (󰙅) symbolizes the worktree, prefixing the project
# name. Literal UTF-8 is embedded directly: this runs under macOS bash 3.2
# (--posix), whose printf has no \u/\U escape support.
printf '\033[01;36m󰙅 %s\033[00m' "$(basename "$cwd")"
