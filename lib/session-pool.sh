#!/bin/bash
# Shared cross-agent session pool. Each wisp-deck session gets a pool dir that
# records every agent's native conversation id (`meta`) plus an agent-neutral
# transcript export (`handoff.md`), written when the pane switches agents. The
# pool is what lets a conversation started under one agent be continued under
# another: the target agent either resumes its OWN recorded session natively,
# or is seeded with the handoff via an initial prompt (see
# lib/account-switch.sh relaunch_switch_tool).
#
# Everything here is fail-open by contract: callers treat an empty result or a
# non-zero exit as "no pool data" and fall back to today's fresh-launch switch.
# Runs inside the compact-view pane's zsh as well as bash — keep expansions
# zsh-safe (no unquoted word-splitting, no bare globs that can nomatch).

# pool_dir <cfg_root> <session_key> — print (and create) the pool directory for
# one wisp-deck session; session_key is the relaunch file's per-session suffix.
# Pool dirs of long-gone sessions accumulate, so creation opportunistically
# prunes siblings untouched for 30+ days.
pool_dir() {
  local cfg_root="$1" key="$2"
  local root="$cfg_root/session-pool"
  find "$root" -mindepth 1 -maxdepth 1 -type d -mtime +30 \
    -exec rm -rf {} + 2>/dev/null || true
  mkdir -p "$root/$key" 2>/dev/null || return 1
  printf '%s\n' "$root/$key"
}

# pool_set <meta_file> <key> <value> — upsert one key=value line. Single writer
# per session (the switch flow), so read-filter-rewrite needs no locking.
pool_set() {
  local meta="$1" key="$2" value="$3" tmp
  tmp="$meta.tmp.$$"
  { [ -f "$meta" ] && grep -v "^$key=" "$meta"; true; } > "$tmp" 2>/dev/null
  printf '%s=%s\n' "$key" "$value" >> "$tmp"
  mv "$tmp" "$meta"
}

# pool_get <meta_file> <key> — print the key's value, empty when absent.
pool_get() {
  local meta="$1" key="$2"
  [ -f "$meta" ] || return 0
  sed -n "s/^$key=//p" "$meta" | head -n 1
}

# codex_current_session <sessions_root> <project_dir> <since_epoch> — print the
# session UUID of the newest codex rollout that belongs to <project_dir>
# (session_meta cwd match) and was touched at/after <since_epoch>. The time
# bound keeps the capture on THIS pane's codex stint — another wisp session on
# the same project may have older rollouts. Empty when nothing matches.
codex_current_session() {
  local root="$1" proj="$2" since="${3:-0}"
  local f m best="" best_m=0 sid
  [ -d "$root" ] || return 0
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    m="$(stat -f %m "$f" 2>/dev/null || echo 0)"
    [ "$m" -ge "$since" ] || continue
    head -n 1 "$f" 2>/dev/null | grep -qF "\"cwd\":\"$proj\"" || continue
    if [ "$m" -gt "$best_m" ]; then
      best="$f"
      best_m="$m"
    fi
  done < <(find "$root" -type f -name 'rollout-*.jsonl' 2>/dev/null)
  [ -n "$best" ] || return 0
  # rollout-<timestamp>-<uuid>.jsonl — the UUID is the basename's last 36 chars.
  sid="${best##*/}"
  sid="${sid%.jsonl}"
  printf '%s\n' "${sid:${#sid}-36}"
}

# codex_rollout_for <sessions_root> <uuid> — print the rollout file recording
# session <uuid>; empty when it does not exist.
codex_rollout_for() {
  local root="$1" uuid="$2"
  [ -d "$root" ] || return 0
  find "$root" -type f -name "rollout-*-$uuid.jsonl" 2>/dev/null | head -n 1
}

# pool_claude_transcript <project_dir> <sid> — path of claude's transcript for
# <sid>: the project path with every non-alphanumeric byte replaced by '-',
# under ~/.claude/projects/ (same munge lib/session-restore.sh uses).
pool_claude_transcript() {
  local proj="$1" sid="$2"
  printf '%s\n' "$HOME/.claude/projects/${proj//[^A-Za-z0-9]/-}/$sid.jsonl"
}

# export_claude_handoff <transcript_jsonl> <out_md> [max_msgs] — write the
# transcript's last max_msgs (default 30) user/assistant TEXT messages as
# markdown. String content passes through; block-list content keeps only text
# blocks (thinking/tool blocks are model plumbing, not conversation). Non-zero
# (and no file) when the transcript is missing/unreadable or python3 is absent.
export_claude_handoff() {
  local transcript="$1" out="$2" max="${3:-30}"
  [ -f "$transcript" ] || return 1
  command -v python3 >/dev/null 2>&1 || return 1
  python3 - "$transcript" "$out" "$max" <<'PYEOF'
import json, sys
transcript, out, max_msgs = sys.argv[1], sys.argv[2], int(sys.argv[3])
msgs = []
with open(transcript, "rb") as f:
    for line in f:
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if e.get("type") not in ("user", "assistant"):
            continue
        content = e.get("message", {}).get("content")
        if isinstance(content, str):
            text = content
        elif isinstance(content, list):
            text = "\n".join(b.get("text", "") for b in content
                             if isinstance(b, dict) and b.get("type") == "text")
        else:
            continue
        text = text.strip()
        if text:
            msgs.append((e["type"], text))
with open(out, "w") as f:
    f.write("# Conversation so far\n\n")
    for role, text in msgs[-max_msgs:]:
        f.write("**%s:** %s\n\n" % ("User" if role == "user" else "Assistant", text))
PYEOF
}

# export_codex_handoff <rollout_jsonl> <out_md> [max_msgs] — same markdown
# shape from a codex rollout: response_item/message payloads, roles
# user/assistant only, input_text/output_text items. User texts that are
# injected context wrappers (they start with '<') are codex plumbing, not the
# user's words — skipped.
export_codex_handoff() {
  local rollout="$1" out="$2" max="${3:-30}"
  [ -f "$rollout" ] || return 1
  command -v python3 >/dev/null 2>&1 || return 1
  python3 - "$rollout" "$out" "$max" <<'PYEOF'
import json, sys
rollout, out, max_msgs = sys.argv[1], sys.argv[2], int(sys.argv[3])
msgs = []
with open(rollout, "rb") as f:
    for line in f:
        try:
            e = json.loads(line)
        except ValueError:
            continue
        p = e.get("payload", {})
        if e.get("type") != "response_item" or p.get("type") != "message":
            continue
        role = p.get("role")
        if role not in ("user", "assistant"):
            continue
        text = "\n".join(b.get("text", "") for b in p.get("content", [])
                         if isinstance(b, dict)
                         and b.get("type") in ("input_text", "output_text"))
        text = text.strip()
        if not text or (role == "user" and text.startswith("<")):
            continue
        msgs.append((role, text))
with open(out, "w") as f:
    f.write("# Conversation so far\n\n")
    for role, text in msgs[-max_msgs:]:
        f.write("**%s:** %s\n\n" % ("User" if role == "user" else "Assistant", text))
PYEOF
}

# handoff_prompt <handoff_md> <from_tool> — the initial prompt that seeds a
# freshly launched agent with another agent's conversation.
handoff_prompt() {
  local handoff="$1" from="$2"
  printf 'You are taking over a conversation the user was having with another AI coding agent (%s). Read %s for the conversation so far, then continue it seamlessly — do not re-introduce yourself, just pick up where it left off.\n' \
    "$from" "$handoff"
}
