#!/bin/bash
# Notification setup — sound hooks.
# Depends on: tui.sh (success, warn)

# Play notification sound if enabled for the given AI tool.
# Reads sound preference from features JSON and plays via afplay in background.
# Usage: play_notification_sound <ai_tool> <config_dir>
play_notification_sound() {
  local ai_tool="$1" config_dir="$2"
  local sound_name
  sound_name="$(get_sound_name "$ai_tool" "$config_dir")"
  if [[ -n "$sound_name" ]]; then
    afplay "/System/Library/Sounds/${sound_name}.aiff" &
  fi
}

# Set up sound notification for the given AI tool.
# Manages only the notification channel (to prevent double sounds).
# Sound playback is handled by the tab-title-watcher.
# Usage: setup_sound_notification <config_dir> [settings_path]
setup_sound_notification() {
  local config_dir="$1"
  local settings_path="${2:-$HOME/.claude/settings.json}"
  set_claude_notif_channel "$config_dir" "$settings_path"
}

# Silence Claude Code's own idle notification by setting preferredNotifChannel
# to terminal_bell directly in settings.json (Claude 2.1.x removed the
# `claude config` subcommand). Ghostty has no audible bell, so terminal_bell is
# silent — which leaves wisp-deck's afplay (gated by the sound flag) as the
# single audible source. This is what makes the "off" setting truly silent and
# also prevents double sounds when sound is on.
#
# The prior value is saved to <config_dir>/prev-notif-channel so it can be
# restored when the last session exits. Idempotent and multi-session safe: if
# the channel is already terminal_bell (another live session set it), the saved
# prev value is left untouched so it isn't clobbered with "terminal_bell".
# Usage: set_claude_notif_channel <config_dir> [settings_path]
set_claude_notif_channel() {
  local config_dir="$1"
  local settings_path="${2:-$HOME/.claude/settings.json}"
  # Fast path: when the channel is already terminal_bell (set by this or another
  # live session), the python below immediately sys.exit(0) without writing — but
  # still pays a ~40ms cold start on every Claude launch, ahead of the AI tool
  # starting. Detect the already-silenced state with a cheap grep and skip the
  # spawn (and the mkdir). Mirrors the python's `if current == "terminal_bell":
  # sys.exit(0)`, so the saved prev value is left untouched exactly as before.
  if [ -f "$settings_path" ] \
    && grep -qE '"preferredNotifChannel"[[:space:]]*:[[:space:]]*"terminal_bell"' "$settings_path"; then
    return 0
  fi
  mkdir -p "$config_dir"
  python3 - "$settings_path" "$config_dir/prev-notif-channel" << 'PYEOF'
import json, os, sys

settings_path, prev_path = sys.argv[1], sys.argv[2]

try:
    with open(settings_path) as f:
        settings = json.load(f)
except FileNotFoundError:
    settings = {}
except (OSError, ValueError):
    # settings.json exists but is unreadable/invalid JSON — never clobber it
    # (it holds the user's entire Claude config). Fail safe: leave it untouched
    # and save no prev value. Claude may chime, but we destroy nothing.
    sys.exit(0)

current = settings.get("preferredNotifChannel")

# Already silenced (e.g. by another live wisp-deck session) — don't clobber the
# previously-saved value with "terminal_bell".
if current == "terminal_bell":
    sys.exit(0)

# "__UNSET__" sentinel distinguishes "key absent" from an empty-string value so
# restore can remove the key rather than set it to "".
with open(prev_path, "w") as f:
    f.write("__UNSET__" if current is None else str(current))

settings["preferredNotifChannel"] = "terminal_bell"
os.makedirs(os.path.dirname(settings_path) or ".", exist_ok=True)
with open(settings_path, "w") as f:
    json.dump(settings, f, indent=2)
    f.write("\n")
PYEOF
}

# Restore Claude Code's preferredNotifChannel from the saved value (or remove
# the key when it was previously unset). No-op if no saved value exists.
# Usage: restore_claude_notif_channel <config_dir> [settings_path]
restore_claude_notif_channel() {
  local config_dir="$1"
  local settings_path="${2:-$HOME/.claude/settings.json}"
  local saved_file="$config_dir/prev-notif-channel"
  if [ ! -f "$saved_file" ]; then
    return 0
  fi
  python3 - "$settings_path" "$saved_file" << 'PYEOF'
import json, sys

settings_path, prev_path = sys.argv[1], sys.argv[2]

with open(prev_path) as f:
    prev = f.read().strip("\n")

try:
    with open(settings_path) as f:
        settings = json.load(f)
except FileNotFoundError:
    settings = {}
except (OSError, ValueError):
    # Don't clobber an unreadable/invalid settings.json with just the restored
    # key — leave the user's file intact.
    sys.exit(0)

if prev == "__UNSET__":
    settings.pop("preferredNotifChannel", None)
else:
    settings["preferredNotifChannel"] = prev

with open(settings_path, "w") as f:
    json.dump(settings, f, indent=2)
    f.write("\n")
PYEOF
  rm -f "$saved_file"
}

# Check if sound notifications are enabled for the given AI tool.
# Usage: is_sound_enabled <tool> <config_dir>
# Outputs "true" or "false".
is_sound_enabled() {
  local tool="$1" config_dir="$2"
  local features_file="$config_dir/${tool}-features.json"
  if [ -f "$features_file" ]; then
    local val
    val="$(python3 -c "
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    print('false' if d.get('sound') is False else 'true')
except Exception:
    print('true')
" "$features_file" 2>/dev/null)"
    echo "${val:-true}"
  else
    echo "true"
  fi
}

# Get the sound name for the given AI tool.
# Returns the sound name (e.g. "Bottle") or empty string if sound is disabled.
# Usage: get_sound_name <tool> <config_dir>
get_sound_name() {
  local tool="$1" config_dir="$2"
  local features_file="$config_dir/${tool}-features.json"
  if [ -f "$features_file" ]; then
    python3 -c "
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    if d.get('sound') is False:
        print('')
    else:
        print(d.get('sound_name', 'Bottle'))
except Exception:
    print('Bottle')
" "$features_file" 2>/dev/null
  else
    echo "Bottle"
  fi
}

# Set the sound name for the given AI tool.
# Usage: set_sound_name <tool> <config_dir> <name>
set_sound_name() {
  local tool="$1" config_dir="$2" name="$3"
  local features_file="$config_dir/${tool}-features.json"
  mkdir -p "$config_dir"
  python3 -c "
import json, sys
path = sys.argv[1]
name = sys.argv[2]
try:
    d = json.load(open(path))
except Exception:
    d = {}
d['sound_name'] = name
with open(path, 'w') as f:
    json.dump(d, f)
    f.write('\n')
" "$features_file" "$name"
}

# Set sound feature flag for the given AI tool.
# Usage: set_sound_feature_flag <tool> <config_dir> <true|false>
set_sound_feature_flag() {
  local tool="$1" config_dir="$2" enabled="$3"
  local features_file="$config_dir/${tool}-features.json"
  mkdir -p "$config_dir"
  python3 -c "
import json, sys, os
path = sys.argv[1]
enabled = sys.argv[2] == 'true'
try:
    d = json.load(open(path))
except Exception:
    d = {}
d['sound'] = enabled
with open(path, 'w') as f:
    json.dump(d, f)
    f.write('\n')
" "$features_file" "$enabled"
}

# Remove sound notification setup for the given AI tool.
# Restores the notification channel.
# Usage: remove_sound_notification <config_dir> [settings_path]
remove_sound_notification() {
  local config_dir="$1"
  local settings_path="${2:-$HOME/.claude/settings.json}"
  restore_claude_notif_channel "$config_dir" "$settings_path"
}
