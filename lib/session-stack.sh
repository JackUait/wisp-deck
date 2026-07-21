#!/bin/bash
# Per-project session stacking. One Ghostty tab per project, holding a stack
# of full wisp sessions switchable via keybindings; the outer status bar shows
# the stack. See docs/superpowers/specs/2026-07-21-per-project-session-stacking-design.md.
#
# Everything here is fail-open: helpers exit 0 and print nothing when the tmux
# server is gone or data is missing, and callers treat that as "no stack".
# Session names may contain spaces — iterate line-wise, never word-split.

# stack_sessions_for_project <tmux_cmd> <project_dir> [exclude_session]
# Print the names of live wisp sessions whose WISP_DECK_PATH equals
# <project_dir>, one per line, oldest first. Tmux-only: this runs on the
# launch critical path.
stack_sessions_for_project() {
  local tmux_cmd="$1" project_dir="$2" exclude="${3:-}"
  local created s
  # shellcheck disable=SC2034
  "$tmux_cmd" list-sessions -F '#{session_created} #{session_name}' 2>/dev/null \
    | sort -n | while IFS=' ' read -r created s; do
      [ -n "$s" ] || continue
      [ "$s" = "$exclude" ] && continue
      "$tmux_cmd" show-environment -t "$s" WISP_DECK_PATH 2>/dev/null \
        | grep -qxF "WISP_DECK_PATH=$project_dir" || continue
      printf '%s\n' "$s"
    done
  return 0
}

# Stack registry: <cfg_root>/stacks/<owner_session> lists every session that
# tab owns (including its own). The owning wrapper's cleanup kills exactly
# this list; adoption edits it. Single writer per file in practice (the
# owning wrapper and the close/adopt helpers it spawns), no locking.

stack_add() {
  local cfg="$1" owner="$2" session="$3" f
  mkdir -p "$cfg/stacks" 2>/dev/null || return 1
  f="$cfg/stacks/$owner"
  grep -qxF "$session" "$f" 2>/dev/null && return 0
  printf '%s\n' "$session" >> "$f"
}

stack_remove_entry() {
  local cfg="$1" owner="$2" session="$3" f tmp
  f="$cfg/stacks/$owner"
  [ -f "$f" ] || return 0
  tmp="$f.tmp.$$"
  grep -vxF "$session" "$f" > "$tmp" 2>/dev/null || true
  mv "$tmp" "$f"
}

stack_list() {
  cat "$1/stacks/$2" 2>/dev/null
  return 0
}

# Remove the SHARE_DIR files wrapper.sh creates per session (mirrors the rm
# lines in its cleanup()). Used for adopted sessions, whose original wrapper
# is gone by the time they die.
stack_session_files_cleanup() {
  local cfg="$1" s="$2"
  rm -f "$cfg/spare-${s}.conf" "$cfg/relaunch-${s}" \
    "$cfg/proxy-${s}.log" "$cfg/proxy-account-${s}"
  rm -rf "$cfg/spare-zdotdir-${s}"
}

# stack_bar_chips <project_name> <self_session> <accent> <session>...
# The outer status-left for one session of a stack. With a single session the
# bar is exactly today's " ⬡ project " so the common case looks unchanged.
# The bar's base style stays fg=white,bg=colour236,bold (status-left-style set
# at launch); chips restore it after their own colours.
stack_bar_chips() {
  local project="$1" self="$2" accent="$3"
  shift 3
  local out=" ⬡ ${project} " i=0 s
  if [ "$#" -le 1 ]; then
    printf '%s' "$out"
    return 0
  fi
  for s in "$@"; do
    i=$((i + 1))
    if [ "$s" = "$self" ]; then
      out="${out}#[fg=colour235,bg=colour${accent},bold] ${i} #[default]#[fg=white,bg=colour236,bold] "
    else
      out="${out}#[fg=colour245] ${i} #[default]#[fg=white,bg=colour236,bold] "
    fi
  done
  printf '%s' "$out"
}

# stack_repaint <tmux_cmd> <cfg_root> <project_name> <project_dir>
# Rebuild every project session's status-left. Each session's bar marks its
# OWN chip active (the visible bar always belongs to the current session), in
# the accent of that session's tool — honouring the user theme preset when
# lib/theme.sh is loaded.
stack_repaint() {
  local tmux_cmd="$1" cfg="$2" project="$3" dir="$4"
  local sessions=() s tool pref accent chips
  while IFS= read -r s; do
    [ -n "$s" ] && sessions+=("$s")
  done < <(stack_sessions_for_project "$tmux_cmd" "$dir")
  [ "${#sessions[@]}" -gt 0 ] || return 0
  pref="$(grep '^theme=' "$cfg/settings" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
  for s in "${sessions[@]}"; do
    tool="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_TOOL 2>/dev/null | cut -d= -f2-)"
    accent=209
    if command -v get_theme_accent >/dev/null 2>&1 \
      && command -v gt_resolve_theme >/dev/null 2>&1; then
      accent="$(get_theme_accent "$(gt_resolve_theme "$pref" "$tool")")"
    fi
    chips="$(stack_bar_chips "$project" "$s" "$accent" "${sessions[@]}")"
    "$tmux_cmd" set-option -t "$s" status-left "$chips" 2>/dev/null || true
  done
  return 0
}

# stack_cycle <tmux_cmd> <current_session> <next|prev>
# Move the pressing client to the neighbouring session of the same project.
# Bound in wrapper.sh with #{session_name} so it always acts on the session
# the key was pressed in (bind-key is server-global — never bake a name).
stack_cycle() {
  local tmux_cmd="$1" current="$2" direction="${3:-next}"
  local path sessions=() s idx=-1 i n target
  path="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  [ -n "$path" ] || return 0
  while IFS= read -r s; do
    [ -n "$s" ] && sessions+=("$s")
  done < <(stack_sessions_for_project "$tmux_cmd" "$path")
  n=${#sessions[@]}
  [ "$n" -gt 1 ] || return 0
  for ((i = 0; i < n; i++)); do
    [ "${sessions[$i]}" = "$current" ] && idx=$i
  done
  [ "$idx" -ge 0 ] || return 0
  if [ "$direction" = "prev" ]; then
    target="${sessions[$(((idx - 1 + n) % n))]}"
  else
    target="${sessions[$(((idx + 1) % n))]}"
  fi
  "$tmux_cmd" switch-client -t "$target"
}

# stack_close_current <tmux_cmd> <cfg_root> <current_session>
# Close ONLY the current stack session: move the client to a neighbour first
# (killing the session under the client would end the whole tab), then full
# per-session teardown, then deregister from whichever stack file holds it.
# Closing the LAST session skips the switch — the client dies with the
# session and the owning wrapper's cleanup unwinds the tab.
stack_close_current() {
  local tmux_cmd="$1" cfg="$2" current="$3"
  local path project sessions=() s neighbour="" root f
  path="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  project="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PROJECT 2>/dev/null | cut -d= -f2-)"
  root="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_ATTENTION_ROOT 2>/dev/null | cut -d= -f2-)"
  if [ -n "$path" ]; then
    while IFS= read -r s; do
      [ -n "$s" ] && [ "$s" != "$current" ] && sessions+=("$s")
    done < <(stack_sessions_for_project "$tmux_cmd" "$path")
  fi
  [ "${#sessions[@]}" -gt 0 ] && neighbour="${sessions[0]}"
  [ -n "$neighbour" ] && "$tmux_cmd" switch-client -t "$neighbour" 2>/dev/null
  cleanup_tmux_session "$current" "" "$tmux_cmd"
  command -v attention_cleanup >/dev/null 2>&1 && attention_cleanup "$root" 2>/dev/null
  command -v keep_awake_drop >/dev/null 2>&1 && keep_awake_drop "$cfg" "$current" 2>/dev/null
  stack_session_files_cleanup "$cfg" "$current"
  for f in "$cfg"/stacks/*; do
    [ -f "$f" ] || continue
    stack_remove_entry "$cfg" "${f##*/}" "$current"
  done
  [ -n "$neighbour" ] && stack_repaint "$tmux_cmd" "$cfg" "$project" "$path"
  return 0
}

# One-shot "open a fresh stack session for this project" request — the same
# ticket pattern as the restore chain (see restore_issue_chain_ticket): the
# hotkey writes the request and simulates Cmd+T; the fresh tab's wrapper
# mv-claims it and skips the picker. Stale (>60s) requests are never claimed,
# so a broken trigger can't hijack a tab the user opens later.

# stack_request_new <tmux_cmd> <cfg_root> <session>
stack_request_new() {
  local tmux_cmd="$1" cfg="$2" session="$3" dir
  dir="$("$tmux_cmd" show-environment -t "$session" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  [ -n "$dir" ] || return 1
  printf '%s|%s\n' "$(date +%s)" "$dir" > "$cfg/stack-request" 2>/dev/null || return 1
  if ! restore_trigger_tab; then
    rm -f "$cfg/stack-request"
    return 1
  fi
}

# stack_request_claim <cfg_root> — prints the requested project dir.
stack_request_claim() {
  local cfg="$1" req="$1/stack-request" claimed stamp dir now
  [ -f "$req" ] || return 1
  claimed="$req.claimed.$$"
  mv "$req" "$claimed" 2>/dev/null || return 1
  IFS='|' read -r stamp dir < "$claimed" || true
  rm -f "$claimed"
  case "$stamp" in '' | *[!0-9]*) return 1 ;; esac
  now="$(date +%s)"
  [ $((now - stamp)) -le 60 ] || return 1
  [ -d "$dir" ] || return 1
  printf '%s\n' "$dir"
}

# stack_adopt_all <tmux_cmd> <cfg_root> <new_session> <owner_pid> <old>...
# ORDER IS THE NO-ZOMBIE INVARIANT: a session is appended to the NEW owner's
# stack file (so the new wrapper's cleanup will kill it) BEFORE its
# adopted-by marker is set (which makes the OLD wrapper skip killing it).
# A crash between the two leaves the session doubly covered, never orphaned.
stack_adopt_all() {
  local tmux_cmd="$1" cfg="$2" new_session="$3" owner_pid="$4"
  shift 4
  local s
  for s in "$@"; do
    "$tmux_cmd" has-session -t "$s" 2>/dev/null || continue
    stack_add "$cfg" "$new_session" "$s" || continue
    "$tmux_cmd" set-environment -t "$s" WISP_DECK_ADOPTED_BY "$new_session" 2>/dev/null || continue
    "$tmux_cmd" set-environment -t "$s" WISP_DECK_OWNER_PID "$owner_pid" 2>/dev/null || true
  done
  return 0
}

# stack_finalize_adoption <tmux_cmd> <new_session> <old>...
# Wait for the adopting tab's client to attach, then detach the old tabs'
# clients so their wrappers unwind in adopted-away mode. Backgrounded by
# wrapper.sh — must never block the launch. The server-wide exit-unattached
# option no longer exists (removed with stacking), so the detach itself can
# not take the server down.
stack_finalize_adoption() {
  local tmux_cmd="$1" new_session="$2"
  shift 2
  local i s
  for i in $(seq 1 100); do
    [ -n "$("$tmux_cmd" list-clients -t "$new_session" 2>/dev/null)" ] && break
    sleep 0.2
  done
  for s in "$@"; do
    "$tmux_cmd" detach-client -s "$s" 2>/dev/null || true
  done
  return 0
}

# stack_adopted_away <tmux_cmd> <session> — was this tab's session taken over
# by a newer tab? (Its wrapper must then exit without killing anything.)
stack_adopted_away() {
  local tmux_cmd="$1" s="$2" v
  "$tmux_cmd" has-session -t "$s" 2>/dev/null || return 1
  v="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_ADOPTED_BY 2>/dev/null | cut -d= -f2-)"
  [ -n "$v" ]
}

# stack_owner_teardown <tmux_cmd> <cfg_root> <owner_session>
# Kill every session this tab owns except its own (the wrapper's existing
# cleanup lines handle that one) and except sessions adopted away since.
stack_owner_teardown() {
  local tmux_cmd="$1" cfg="$2" owner="$3"
  local s adopted_by root
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    [ "$s" = "$owner" ] && continue
    "$tmux_cmd" has-session -t "$s" 2>/dev/null || continue
    adopted_by="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_ADOPTED_BY 2>/dev/null | cut -d= -f2-)"
    if [ -n "$adopted_by" ] && [ "$adopted_by" != "$owner" ]; then
      continue
    fi
    root="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_ATTENTION_ROOT 2>/dev/null | cut -d= -f2-)"
    cleanup_tmux_session "$s" "" "$tmux_cmd"
    command -v attention_cleanup >/dev/null 2>&1 && attention_cleanup "$root" 2>/dev/null
    command -v keep_awake_drop >/dev/null 2>&1 && keep_awake_drop "$cfg" "$s" 2>/dev/null
    stack_session_files_cleanup "$cfg" "$s"
  done < <(stack_list "$cfg" "$owner")
  rm -f "$cfg/stacks/$owner"
  return 0
}
