#!/bin/bash
# Notification setup — sound hooks.
# Depends on: tui.sh (success, warn)

# Play notification sound if enabled for the given AI tool.
# Reads sound preference from features JSON and plays via afplay in background.
# Usage: play_notification_sound <ai_tool> <config_dir>
play_notification_sound() {
  local ai_tool="$1" config_dir="$2"
  local lock_file="$config_dir/.${ai_tool}-features.json.lock"
  local module="${BASH_SOURCE[0]}"

  # Writers use this same advisory lock. Holding it through playback makes a
  # successful Off action a completion boundary: old-authorized audio has
  # finished, and later playback must re-read the new value.
  (
    /usr/bin/lockf -k "$lock_file" /bin/bash -c '
      source "$1"
      sound_name="$(get_sound_name "$2" "$3")"
      if [[ -n "$sound_name" ]]; then
        afplay "/System/Library/Sounds/${sound_name}.aiff"
      fi
    ' _ "$module" "$ai_tool" "$config_dir"
  ) >/dev/null 2>&1 &
}

# Print two lines: explicit enabled state, then the validated sound name. A
# nonblocking descriptor and fstat avoid hanging on FIFOs/devices; bounded,
# duplicate-rejecting JSON parsing makes every ambiguous document silent.
_read_sound_preference() {
  python3 -c '
import json, os, stat, sys

def reject_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate key")
        result[key] = value
    return result

def reject_constant(_value):
    raise ValueError("non-standard JSON constant")

def silent():
    print("false")
    print("")

fd = None
try:
    flags = os.O_RDONLY | os.O_NONBLOCK | getattr(os, "O_CLOEXEC", 0)
    fd = os.open(sys.argv[1], flags)
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode) or info.st_size < 0 or info.st_size > 65536:
        raise ValueError("invalid preference file")
    chunks = []
    total = 0
    while True:
        chunk = os.read(fd, min(8192, 65537 - total))
        if not chunk:
            break
        chunks.append(chunk)
        total += len(chunk)
        if total > 65536:
            raise ValueError("oversized preference file")
    data = b"".join(chunks)
    document = json.loads(data.decode("utf-8"),
                          object_pairs_hook=reject_duplicates,
                          parse_constant=reject_constant)
    case_variant = isinstance(document, dict) and any(
        (key != "sound" and key.casefold() == "sound") or
        (key != "sound_name" and key.casefold() == "sound_name")
        for key in document)
    if not isinstance(document, dict) or case_variant or document.get("sound") is not True:
        silent()
    elif "sound_name" in document and not isinstance(document["sound_name"], str):
        silent()
    else:
        name = document.get("sound_name") or "Bottle"
        allowed = {"Basso", "Blow", "Bottle", "Frog", "Funk", "Glass", "Hero",
                   "Morse", "Ping", "Pop", "Purr", "Sosumi", "Submarine", "Tink"}
        print("true")
        print(name if name in allowed else "Bottle")
except Exception:
    silent()
finally:
    if fd is not None:
        os.close(fd)
' "$1" 2>/dev/null
}

# Check if sound notifications are enabled for the given AI tool.
# Usage: is_sound_enabled <tool> <config_dir>
# Outputs "true" or "false".
is_sound_enabled() {
  local tool="$1" config_dir="$2"
  _read_sound_preference "$config_dir/${tool}-features.json" | sed -n '1p'
}

# Get the sound name for the given AI tool.
# Returns the sound name (e.g. "Bottle") or empty string if sound is disabled.
# Usage: get_sound_name <tool> <config_dir>
get_sound_name() {
  local tool="$1" config_dir="$2"
  _read_sound_preference "$config_dir/${tool}-features.json" | sed -n '2p'
}

# Update one sound preference while preserving unrelated keys only from a
# bounded, strict, regular JSON document. Invalid input is replaced with a
# fresh document, never normalized into a stale enabled preference.
_write_sound_preference() {
  python3 -c '
import json, os, stat, sys, tempfile

def reject_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate key")
        result[key] = value
    return result

def reject_constant(_value):
    raise ValueError("non-standard JSON constant")

def load_existing(path):
    fd = None
    try:
        flags = os.O_RDONLY | os.O_NONBLOCK | getattr(os, "O_CLOEXEC", 0)
        fd = os.open(path, flags)
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size < 0 or info.st_size > 65536:
            raise ValueError("invalid preference file")
        chunks = []
        total = 0
        while True:
            chunk = os.read(fd, min(8192, 65537 - total))
            if not chunk:
                break
            chunks.append(chunk)
            total += len(chunk)
            if total > 65536:
                raise ValueError("oversized preference file")
        document = json.loads(b"".join(chunks).decode("utf-8"),
                              object_pairs_hook=reject_duplicates,
                              parse_constant=reject_constant)
        case_variant = isinstance(document, dict) and any(
            (key != "sound" and key.casefold() == "sound") or
            (key != "sound_name" and key.casefold() == "sound_name")
            for key in document)
        if not isinstance(document, dict) or case_variant:
            raise ValueError("ambiguous preference document")
        return document
    except Exception:
        return {}
    finally:
        if fd is not None:
            os.close(fd)

path, operation, value = sys.argv[1:4]
document = load_existing(path)
if operation == "name":
    document["sound_name"] = value
elif operation == "enabled":
    document["sound"] = value == "true"
else:
    raise ValueError("unknown preference operation")

fd, temp_path = tempfile.mkstemp(prefix="." + os.path.basename(path) + ".",
                                 dir=os.path.dirname(path))
try:
    with os.fdopen(fd, "w") as output:
        json.dump(document, output)
        output.write("\n")
    os.chmod(temp_path, 0o644)
    os.replace(temp_path, path)
except Exception:
    try:
        os.unlink(temp_path)
    except OSError:
        pass
    raise
' "$1" "$2" "$3"
}

# Set the sound name for the given AI tool.
# Usage: set_sound_name <tool> <config_dir> <name>
set_sound_name() {
  local tool="$1" config_dir="$2" name="$3"
  local features_file="$config_dir/${tool}-features.json"
  local lock_file="$config_dir/.${tool}-features.json.lock"
  local module="${BASH_SOURCE[0]}"
  mkdir -p "$config_dir"
  /usr/bin/lockf -k "$lock_file" /bin/bash -c '
    source "$1"
    _write_sound_preference "$2" name "$3"
  ' _ "$module" "$features_file" "$name"
}

# Set sound feature flag for the given AI tool.
# Usage: set_sound_feature_flag <tool> <config_dir> <true|false>
set_sound_feature_flag() {
  local tool="$1" config_dir="$2" enabled="$3"
  local features_file="$config_dir/${tool}-features.json"
  local lock_file="$config_dir/.${tool}-features.json.lock"
  local module="${BASH_SOURCE[0]}"
  mkdir -p "$config_dir"
  /usr/bin/lockf -k "$lock_file" /bin/bash -c '
    source "$1"
    _write_sound_preference "$2" enabled "$3"
  ' _ "$module" "$features_file" "$enabled"
}
