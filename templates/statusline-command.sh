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

# Upstream divergence rides HERE, right of the branch name (it moved off the
# ledger's bottom bar, where it competed with the account pill for the row):
# ↑N commits to push, ↓M to pull. Both are omitted when the branch is in sync,
# and the whole marker is absent when the branch tracks no upstream at all.
divergence=""
if git -C "$cwd" rev-parse '@{u}' >/dev/null 2>&1; then
  counts=$(git -C "$cwd" rev-list --left-right --count 'HEAD...@{u}' 2>/dev/null)
  ahead=$(echo "$counts" | cut -f1)
  behind=$(echo "$counts" | cut -f2)
  if [ -n "$ahead" ] && [ "$ahead" -gt 0 ] 2>/dev/null; then
    divergence=$(printf ' \033[36m↑%s\033[00m' "$ahead")
  fi
  if [ -n "$behind" ] && [ "$behind" -gt 0 ] 2>/dev/null; then
    divergence="${divergence}$(printf ' \033[33m↓%s\033[00m' "$behind")"
  fi
fi

# A Nerd Font file-tree glyph (󰙅) symbolizes the worktree, prefixing the label.
# Literal UTF-8 is embedded directly: this runs under macOS bash 3.2 (--posix),
# whose printf has no \u/\U escape support.
printf '\033[01;36m󰙅 %s\033[00m%s' "$label" "$divergence"
