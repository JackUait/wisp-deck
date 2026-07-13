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
    # Both streams dropped: this fires from the watcher loop on a terminal the AI
    # tool is drawing on, and a missing sound file is not worth corrupting it
    # over. The watcher already drops both, but this must not depend on where it
    # is called from — it is one line either way.
    afplay "/System/Library/Sounds/${sound_name}.aiff" >/dev/null 2>&1 &
  fi
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
