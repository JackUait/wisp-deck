#!/bin/bash
# Claude settings.json manipulation helpers.

# Merge statusLine into Claude settings.json (create if missing).
merge_claude_settings() {
  local path="$1"
  mkdir -p "$(dirname "$path")"
  if [ -f "$path" ]; then
    if grep -q '"statusLine"' "$path"; then
      success "Claude status line already configured"
    else
      sed -i '' '$ s/}$/,\n  "statusLine": {\n    "type": "command",\n    "command": "bash ~\/.claude\/statusline-wrapper.sh"\n  }\n}/' "$path"
      success "Added status line to Claude settings"
    fi
  else
    cat > "$path" << 'CSEOF'
{
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/statusline-wrapper.sh"
  }
}
CSEOF
    success "Created Claude settings with status line"
  fi
}

# Add waiting indicator hooks (Stop + PreToolUse + UserPromptSubmit) to settings.json.
# Uses $WISP_DECK_MARKER_FILE env var so hooks are safe outside Wisp Deck.
# Outputs "added", "upgraded", or "exists".
add_waiting_indicator_hooks() {
  local path="$1"
  # Fast path: a python3 cold start (~40ms) runs synchronously on every Claude
  # launch before the AI tool can start. When the file already carries all three
  # distinguishing markers of the current format — the PostToolUse "-cooldown"
  # hook, the AskUserQuestion "-ask" sidecar, and the catch-all negative-lookahead
  # matcher — the python below would only print "exists" and write nothing. These
  # three substrings are exactly the upgrade targets, so their joint presence is a
  # conservative subset of python's "exists" condition: no older format has all of
  # them. Skip the spawn (and the mkdir/dirname subshells) entirely in that common
  # case. Runs before mkdir because the check only reads an existing file.
  if [ -f "$path" ] \
    && grep -q -- '-cooldown' "$path" \
    && grep -q -- '-ask' "$path" \
    && grep -qF '(?!AskUserQuestion$)' "$path"; then
    echo "exists"
    return 0
  fi
  mkdir -p "$(dirname "$path")"
  python3 - "$path" << 'PYEOF'
import json, sys, os

settings_path = sys.argv[1]

if os.path.exists(settings_path):
    try:
        with open(settings_path, "r") as f:
            settings = json.load(f)
    except (json.JSONDecodeError, ValueError):
        settings = {}
else:
    settings = {}

hooks = settings.setdefault("hooks", {})

stop_cmd = 'if [ -n "$WISP_DECK_MARKER_FILE" ]; then touch "$WISP_DECK_MARKER_FILE"; fi'
ask_cmd = 'if [ -n "$WISP_DECK_MARKER_FILE" ]; then touch "$WISP_DECK_MARKER_FILE" "${WISP_DECK_MARKER_FILE}-ask"; fi'
clear_cmd = 'if [ -n "$WISP_DECK_MARKER_FILE" ]; then rm -f "$WISP_DECK_MARKER_FILE" "${WISP_DECK_MARKER_FILE}-ask"; fi'
cooldown_cmd = 'if [ -n "$WISP_DECK_MARKER_FILE" ]; then touch "${WISP_DECK_MARKER_FILE}-cooldown"; fi'

marker = "WISP_DECK_MARKER_FILE"

# Check if current Stop-based format is already installed
stop_list = hooks.get("Stop", [])
stop_exists = any(
    marker in h.get("command", "")
    for entry in stop_list
    for h in entry.get("hooks", [])
)

# Check if old Notification-based format exists (needs upgrade)
notif_list = hooks.get("Notification", [])
notif_exists = any(
    marker in h.get("command", "")
    for entry in notif_list
    for h in entry.get("hooks", [])
)

# Check if old Stop format without matcher exists (needs upgrade)
pre_list = hooks.get("PreToolUse", [])
old_stop_needs_upgrade = stop_exists and not any(
    entry.get("matcher") == "AskUserQuestion"
    for entry in pre_list
    if any(marker in h.get("command", "") for h in entry.get("hooks", []))
)

# Check if PostToolUse cooldown hook exists (needs upgrade if missing)
post_list = hooks.get("PostToolUse", [])
cooldown_exists = any(
    "cooldown" in h.get("command", "")
    for entry in post_list
    for h in entry.get("hooks", [])
)
needs_cooldown_upgrade = stop_exists and not old_stop_needs_upgrade and not cooldown_exists

# Check if catch-all PreToolUse has no matcher (needs upgrade to add negative lookahead)
catchall_needs_fix = stop_exists and not old_stop_needs_upgrade and not needs_cooldown_upgrade and any(
    not entry.get("matcher") and any("rm" in h.get("command", "") and marker in h.get("command", "") for h in entry.get("hooks", []))
    for entry in pre_list
)

# Check if AskUserQuestion hook is missing the -ask sidecar (needs upgrade)
ask_needs_sidecar = stop_exists and not old_stop_needs_upgrade and not needs_cooldown_upgrade and not catchall_needs_fix and any(
    entry.get("matcher") == "AskUserQuestion" and not any("-ask" in h.get("command", "") for h in entry.get("hooks", []))
    for entry in pre_list
)

if stop_exists and not old_stop_needs_upgrade and not needs_cooldown_upgrade and not catchall_needs_fix and not ask_needs_sidecar:
    # Current format already installed (including PostToolUse cooldown)
    print("exists")
    sys.exit(0)
elif notif_exists or old_stop_needs_upgrade or needs_cooldown_upgrade or catchall_needs_fix or ask_needs_sidecar:
    # Old format — remove wisp-deck hooks so they get re-added below
    for event in ["Stop", "Notification", "PreToolUse", "PostToolUse", "UserPromptSubmit"]:
        event_list = hooks.get(event, [])
        new_list = [
            entry for entry in event_list
            if not any(marker in h.get("command", "") for h in entry.get("hooks", []))
        ]
        if new_list:
            hooks[event] = new_list
        elif event in hooks:
            del hooks[event]
    action = "upgraded"
else:
    action = "added"

# Add Stop hook (fires immediately when Claude stops generating)
hooks.setdefault("Stop", []).append({
    "hooks": [{"type": "command", "command": stop_cmd}]
})

# Add PreToolUse hook with matcher for AskUserQuestion (creates marker + -ask sidecar)
hooks.setdefault("PreToolUse", []).append({
    "matcher": "AskUserQuestion",
    "hooks": [{"type": "command", "command": ask_cmd}]
})

# Add PreToolUse catch-all hook (clears marker — Claude is actively working)
hooks.setdefault("PreToolUse", []).append({
    "matcher": "^(?!AskUserQuestion$)",
    "hooks": [{"type": "command", "command": clear_cmd}]
})

# Add PostToolUse catch-all hook (creates cooldown — suppresses subagent noise)
hooks.setdefault("PostToolUse", []).append({
    "hooks": [{"type": "command", "command": cooldown_cmd}]
})

# Add UserPromptSubmit hook (clears marker when user answers)
hooks.setdefault("UserPromptSubmit", []).append({
    "hooks": [{"type": "command", "command": clear_cmd}]
})

with open(settings_path, "w") as f:
    json.dump(settings, f, indent=2)
    f.write("\n")

print(action)
PYEOF
}

# Remove waiting indicator hooks from settings.json.
# Outputs "removed" or "not_found".
remove_waiting_indicator_hooks() {
  local path="$1"
  if [ ! -f "$path" ]; then
    echo "not_found"
    return 0
  fi
  python3 - "$path" << 'PYEOF'
import json, sys, os

settings_path = sys.argv[1]
marker = "WISP_DECK_MARKER_FILE"

try:
    with open(settings_path, "r") as f:
        settings = json.load(f)
except (json.JSONDecodeError, ValueError, FileNotFoundError):
    print("not_found")
    sys.exit(0)

hooks = settings.get("hooks", {})
found = False

for event in ["Stop", "Notification", "PreToolUse", "PostToolUse", "UserPromptSubmit"]:
    event_list = hooks.get(event, [])
    new_list = [
        entry for entry in event_list
        if not any(marker in h.get("command", "") for h in entry.get("hooks", []))
    ]
    if len(new_list) != len(event_list):
        found = True
        if new_list:
            hooks[event] = new_list
        else:
            del hooks[event]

if not found:
    print("not_found")
    sys.exit(0)

if not hooks:
    del settings["hooks"]

with open(settings_path, "w") as f:
    json.dump(settings, f, indent=2)
    f.write("\n")

print("removed")
PYEOF
}
