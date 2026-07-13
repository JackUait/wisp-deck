#!/bin/bash
# Claude settings.json manipulation helpers.

# Merge statusLine into Claude settings.json (create if missing).
merge_claude_settings() {
  local filepath="$1"
  mkdir -p "$(dirname "$filepath")"
  if [ -f "$filepath" ]; then
    if grep -q '"statusLine"' "$filepath"; then
      success "Claude status line already configured"
    else
      sed -i '' '$ s/}$/,\n  "statusLine": {\n    "type": "command",\n    "command": "bash ~\/.claude\/statusline-wrapper.sh"\n  }\n}/' "$filepath"
      success "Added status line to Claude settings"
    fi
  else
    cat > "$filepath" << 'CSEOF'
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

# Merge subagentStatusLine into Claude settings.json (create if missing).
# Claude renders one agent-panel row per subagent through this command, so the
# row for whichever subagent the user is on shows that subagent's own info.
# Mirrors merge_claude_settings: idempotent, appends before the final brace.
merge_subagent_statusline() {
  local filepath="$1"
  mkdir -p "$(dirname "$filepath")"
  if [ -f "$filepath" ]; then
    if grep -q '"subagentStatusLine"' "$filepath"; then
      success "Claude subagent status line already configured"
    else
      sed -i '' '$ s/}$/,\n  "subagentStatusLine": {\n    "type": "command",\n    "command": "bash ~\/.claude\/subagent-statusline.sh"\n  }\n}/' "$filepath"
      success "Added subagent status line to Claude settings"
    fi
  else
    cat > "$filepath" << 'CSEOF'
{
  "subagentStatusLine": {
    "type": "command",
    "command": "bash ~/.claude/subagent-statusline.sh"
  }
}
CSEOF
    success "Created Claude settings with subagent status line"
  fi
}

# write_claude_launch_settings <generation-dir> [active-wisp-settings]
#
# Build the additional settings overlay passed to Claude for one attention
# generation. The selected Wisp config is copied when present, then only
# preferredNotifChannel is forced so Claude's native sound stays silent while
# Wisp Deck owns notification playback. The source is never modified. A sibling
# temporary file is renamed over the target so readers cannot observe partial
# JSON. Prints the generated path on success.
write_claude_launch_settings() {
  local generation_dir="${1:-}" source_settings="${2:-}"
  local generation suffix target tmp

  [ -d "$generation_dir" ] || return 1
  generation="${generation_dir##*/}"
  case "$generation" in
    generation.*) suffix="${generation#generation.}" ;;
    *) return 1 ;;
  esac
  case "$suffix" in
    ''|*[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789]*) return 1 ;;
  esac

  target="$generation_dir/claude-settings.json"
  tmp="$(umask 077; mktemp "$generation_dir/.claude-settings.XXXXXX")" || return 1
  if ! chmod 600 "$tmp" \
     || ! python3 - "$source_settings" "$tmp" << 'PYEOF'
import json
import sys

source_path, output_path = sys.argv[1], sys.argv[2]
settings = {}

if source_path:
    with open(source_path, "r", encoding="utf-8") as source:
        settings = json.load(source)
    if not isinstance(settings, dict):
        raise ValueError("Claude settings overlay must be a JSON object")

settings["preferredNotifChannel"] = "terminal_bell"
with open(output_path, "w", encoding="utf-8") as output:
    json.dump(settings, output, indent=2)
    output.write("\n")
PYEOF
  then
    rm -f "$tmp"
    return 1
  fi
  if ! mv -f "$tmp" "$target"; then
    rm -f "$tmp"
    return 1
  fi
  printf '%s\n' "$target"
}

# Remove every historical Wisp Deck/Ghost Tab marker command from a Claude
# settings file. Unrelated commands are retained even when they share the same
# hook entry. This is an upgrade-only migration helper; semantic state is now
# read from Claude's process registry instead of hooks. When a Wisp config root
# is supplied, this mutation shares a process lock with the legacy notification
# migration so concurrent upgraded wrappers cannot overwrite each other.
# Outputs "removed" or "not_found".
remove_waiting_indicator_hooks() {
  local filepath="$1"
  local config_dir="${2:-}"
  if [ ! -f "$filepath" ]; then
    echo "not_found"
    return 0
  fi
  python3 - "$filepath" "$config_dir" << 'PYEOF'
import fcntl
import json
import os
import stat
import sys
import tempfile

settings_path, config_dir = sys.argv[1], sys.argv[2]
markers = ("WISP_DECK_MARKER_FILE", "GHOST_TAB_MARKER_FILE")

if config_dir:
    try:
        flags = os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0)
        lock_fd = os.open(os.path.join(config_dir, ".settings-migration.lock"), flags, 0o600)
        os.fchmod(lock_fd, 0o600)
        fcntl.flock(lock_fd, fcntl.LOCK_EX)
    except OSError:
        print("not_found")
        sys.exit(0)

try:
    with open(settings_path, "r", encoding="utf-8") as source:
        source_stat = os.fstat(source.fileno())
        settings = json.load(source)
except (OSError, ValueError):
    print("not_found")
    sys.exit(0)

if not isinstance(settings, dict):
    print("not_found")
    sys.exit(0)

hooks = settings.get("hooks")
if not isinstance(hooks, dict):
    print("not_found")
    sys.exit(0)

found = False
for event, event_list in list(hooks.items()):
    if not isinstance(event_list, list):
        continue
    kept_entries = []
    for entry in event_list:
        if not isinstance(entry, dict) or not isinstance(entry.get("hooks"), list):
            kept_entries.append(entry)
            continue

        entry_found = False
        kept_commands = []
        for hook in entry["hooks"]:
            command = hook.get("command", "") if isinstance(hook, dict) else ""
            if isinstance(command, str) and any(marker in command for marker in markers):
                found = True
                entry_found = True
            else:
                kept_commands.append(hook)

        if not entry_found:
            kept_entries.append(entry)
        elif kept_commands:
            kept_entry = dict(entry)
            kept_entry["hooks"] = kept_commands
            kept_entries.append(kept_entry)

    if kept_entries:
        hooks[event] = kept_entries
    else:
        del hooks[event]

if not found:
    print("not_found")
    sys.exit(0)

if not hooks:
    del settings["hooks"]

# Follow a settings symlink rather than replacing the link itself. The managed
# Claude accounts intentionally share the standard login's settings this way.
write_path = os.path.realpath(settings_path)
directory = os.path.dirname(write_path) or "."
try:
    target_stat = os.stat(write_path)
except OSError:
    print("not_found")
    sys.exit(0)
if not stat.S_ISREG(target_stat.st_mode) or not os.path.samestat(source_stat, target_stat):
    print("not_found")
    sys.exit(0)

def same_source_version(before, after):
    return (
        os.path.samestat(before, after)
        and before.st_size == after.st_size
        and before.st_mtime_ns == after.st_mtime_ns
        and before.st_ctime_ns == after.st_ctime_ns
    )

mode = stat.S_IMODE(target_stat.st_mode)
fd, temporary = tempfile.mkstemp(prefix=".wisp-hooks.", dir=directory)
try:
    os.fchmod(fd, mode)
    with os.fdopen(fd, "w", encoding="utf-8") as output:
        json.dump(settings, output, indent=2)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())
    try:
        current_path_stat = os.stat(settings_path)
        current_target_stat = os.stat(write_path)
    except OSError:
        current_path_stat = current_target_stat = None
    if (
        current_path_stat is None
        or current_target_stat is None
        or not same_source_version(source_stat, current_path_stat)
        or not same_source_version(source_stat, current_target_stat)
    ):
        os.unlink(temporary)
        print("not_found")
        sys.exit(0)
    os.replace(temporary, write_path)
except Exception:
    try:
        os.close(fd)
    except OSError:
        pass
    try:
        os.unlink(temporary)
    except OSError:
        pass
    raise

print("removed")
PYEOF
}

# migrate_legacy_claude_notif_channel <settings-file> <wisp-config-root>
#
# Old releases globally forced preferredNotifChannel=terminal_bell and saved the
# displaced value in prev-notif-channel. A crash could strand both. Consume that
# legacy lease once: restore it only while the global value is still the exact
# value Wisp wrote, otherwise preserve the user's newer choice. Invalid settings
# are left byte-for-byte intact and retain the lease for a later retry.
#
# The same permanent private lock used by hook removal serializes concurrent
# upgraded wrappers. Writes follow a managed-account settings symlink and replace
# its target atomically, leaving the symlink itself intact.
migrate_legacy_claude_notif_channel() {
  local settings_path="${1:-}" config_dir="${2:-}"
  [ -n "$settings_path" ] && [ -d "$config_dir" ] || return 0
  [ -f "$config_dir/prev-notif-channel" ] || return 0

  python3 - "$settings_path" "$config_dir" << 'PYEOF'
import fcntl
import json
import os
import stat
import sys
import tempfile

settings_path, config_dir = sys.argv[1], sys.argv[2]
legacy_path = os.path.join(config_dir, "prev-notif-channel")
lock_path = os.path.join(config_dir, ".settings-migration.lock")

try:
    flags = os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0)
    lock_fd = os.open(lock_path, flags, 0o600)
    os.fchmod(lock_fd, 0o600)
    fcntl.flock(lock_fd, fcntl.LOCK_EX)
except OSError:
    sys.exit(0)

try:
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    legacy_fd = os.open(legacy_path, flags)
except FileNotFoundError:
    sys.exit(0)
except OSError:
    sys.exit(0)

try:
    legacy_stat = os.fstat(legacy_fd)
    if not stat.S_ISREG(legacy_stat.st_mode) or legacy_stat.st_size > 4096:
        sys.exit(0)
    with os.fdopen(legacy_fd, "rb") as legacy_file:
        raw_saved = legacy_file.read(4097)
    if len(raw_saved) > 4096:
        sys.exit(0)
    saved = raw_saved.decode("utf-8").strip("\n")
except (OSError, UnicodeError):
    try:
        os.close(legacy_fd)
    except OSError:
        pass
    sys.exit(0)

try:
    with open(settings_path, "r", encoding="utf-8") as source:
        source_stat = os.fstat(source.fileno())
        settings = json.load(source)
except FileNotFoundError:
    # The user removed the global settings after the legacy lease was written.
    # Preserve that newer choice and discard only the stale lease.
    try:
        os.unlink(legacy_path)
    except FileNotFoundError:
        pass
    sys.exit(0)
except (OSError, ValueError):
    sys.exit(0)

if not isinstance(settings, dict):
    sys.exit(0)

if settings.get("preferredNotifChannel") != "terminal_bell":
    try:
        os.unlink(legacy_path)
    except FileNotFoundError:
        pass
    sys.exit(0)

if saved == "__UNSET__":
    settings.pop("preferredNotifChannel", None)
else:
    settings["preferredNotifChannel"] = saved

write_path = os.path.realpath(settings_path)
try:
    target_stat = os.stat(write_path)
except OSError:
    sys.exit(0)
if not stat.S_ISREG(target_stat.st_mode) or not os.path.samestat(source_stat, target_stat):
    sys.exit(0)

def same_source_version(before, after):
    return (
        os.path.samestat(before, after)
        and before.st_size == after.st_size
        and before.st_mtime_ns == after.st_mtime_ns
        and before.st_ctime_ns == after.st_ctime_ns
    )

directory = os.path.dirname(write_path) or "."
fd, temporary = tempfile.mkstemp(prefix=".wisp-settings-migration.", dir=directory)
try:
    os.fchmod(fd, stat.S_IMODE(target_stat.st_mode))
    with os.fdopen(fd, "w", encoding="utf-8") as output:
        json.dump(settings, output, indent=2)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())
    try:
        current_path_stat = os.stat(settings_path)
        current_target_stat = os.stat(write_path)
    except OSError:
        current_path_stat = current_target_stat = None
    if (
        current_path_stat is None
        or current_target_stat is None
        or not same_source_version(source_stat, current_path_stat)
        or not same_source_version(source_stat, current_target_stat)
    ):
        os.unlink(temporary)
        sys.exit(0)
    os.replace(temporary, write_path)
    try:
        directory_fd = os.open(directory, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    except OSError:
        pass
except Exception:
    try:
        os.close(fd)
    except OSError:
        pass
    try:
        os.unlink(temporary)
    except OSError:
        pass
    sys.exit(0)

try:
    os.unlink(legacy_path)
except FileNotFoundError:
    pass
PYEOF
}
