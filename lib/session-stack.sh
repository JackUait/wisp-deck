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

# stack_adoptable_sessions_for_project <tmux_cmd> <project_dir> [exclude]
# Like stack_sessions_for_project, but only sessions whose wrapper speaks the
# stacking protocol — detected by the WISP_DECK_OWNER_PID launch stamp.
# Sessions launched by pre-stacking wrappers lack it, and their still-running
# wrappers kill their own session unconditionally when their client detaches:
# adopting one turns the consolidation handoff into a kill. Those sessions
# must stay in their own tabs.
stack_adoptable_sessions_for_project() {
  local tmux_cmd="$1" project_dir="$2" exclude="${3:-}"
  local s
  stack_sessions_for_project "$tmux_cmd" "$project_dir" "$exclude" \
    | while IFS= read -r s; do
      [ -n "$s" ] || continue
      "$tmux_cmd" show-environment -t "$s" WISP_DECK_OWNER_PID 2>/dev/null \
        | grep -q '^WISP_DECK_OWNER_PID=[0-9][0-9]*$' || continue
      printf '%s\n' "$s"
    done
  return 0
}

# stack_live_owner_for_project <tmux_cmd> <cfg_root> <project_dir>
# Print "owner<TAB>pid" for the live tab already hosting this project's
# stack: owner = the registry file naming that tab's stack, pid = the owning
# wrapper (checked alive). Prints nothing when no such tab exists —
# pre-stacking sessions (no owner-pid stamp), dead owners, sessions no
# registry lists — and the caller then launches a normal fresh tab. Tmux +
# registry files only: this runs on the post-pick critical path.
stack_live_owner_for_project() {
  local tmux_cmd="$1" cfg="$2" project_dir="$3"
  local s pid f owner
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    pid="$("$tmux_cmd" show-environment -t "$s" WISP_DECK_OWNER_PID 2>/dev/null | cut -d= -f2-)"
    case "$pid" in '' | *[!0-9]*) continue ;; esac
    kill -0 "$pid" 2>/dev/null || continue
    for f in "$cfg"/stacks/*; do
      [ -f "$f" ] || continue
      owner="${f##*/}"
      [ "$owner" = ".reap-marks" ] && continue
      grep -qxF "$s" "$f" 2>/dev/null || continue
      printf '%s\t%s\n' "$owner" "$pid"
      return 0
    done
  done < <(stack_adoptable_sessions_for_project "$tmux_cmd" "$project_dir")
  return 0
}

# Stack registry: <cfg_root>/stacks/<owner_session> lists every session that
# tab owns (including its own). The owning wrapper's cleanup kills exactly
# this list; in-place builds append to it. Single writer per file in practice
# (the owning wrapper and the close/build helpers it spawns), no locking.

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
# The outer status-left for one session of a stack: the project label, one
# chip per session when the stack has ≥2, and always a trailing + button
# that opens a fresh session for this project. The + rides in a named status
# range so the MouseDown1Status bind can identify the click target
# (#{mouse_status_range}). The bar's base style stays
# fg=white,bg=colour236,bold (status-left-style set at launch); chips restore
# it after their own colours.
stack_bar_chips() {
  local project="$1" self="$2" accent="$3"
  shift 3
  local out=" ⬡ ${project} " i=0 s
  local plus="#[range=user|wisp-stack-new]#[fg=colour245] + #[norange]#[default]#[fg=white,bg=colour236,bold] "
  if [ "$#" -le 1 ]; then
    printf '%s' "${out}${plus}"
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
  printf '%s' "${out}${plus}"
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
    # The bar is the stack's only navigation surface and hosts the + button,
    # and the user's own ~/.tmux.conf may hide the status bar globally
    # (`set -g status off`) — painted chips nobody can see make a
    # background-added session look like nothing happened. Always force the
    # session-level bar visible.
    "$tmux_cmd" set-option -t "$s" status on 2>/dev/null || true
  done
  return 0
}

# stack_cycle <tmux_cmd> <current_session> <next|prev>
# Move the pressing client to the neighbouring session of the same project.
# Bound in wrapper.sh with #{session_name} so it always acts on the session
# the key was pressed in (bind-key is server-global — never bake a name).
stack_cycle() {
  local tmux_cmd="$1" current="$2" direction="${3:-next}"
  local proj_dir sessions=() s idx=-1 i n target
  proj_dir="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  [ -n "$proj_dir" ] || return 0
  while IFS= read -r s; do
    [ -n "$s" ] && sessions+=("$s")
  done < <(stack_sessions_for_project "$tmux_cmd" "$proj_dir")
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
  local proj_dir project sessions=() s neighbour="" root f
  proj_dir="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PATH 2>/dev/null | cut -d= -f2-)"
  project="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_PROJECT 2>/dev/null | cut -d= -f2-)"
  root="$("$tmux_cmd" show-environment -t "$current" WISP_DECK_ATTENTION_ROOT 2>/dev/null | cut -d= -f2-)"
  if [ -n "$proj_dir" ]; then
    while IFS= read -r s; do
      [ -n "$s" ] && [ "$s" != "$current" ] && sessions+=("$s")
    done < <(stack_sessions_for_project "$tmux_cmd" "$proj_dir")
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
  [ -n "$neighbour" ] && stack_repaint "$tmux_cmd" "$cfg" "$project" "$proj_dir"
  return 0
}

# stack_adopted_away <tmux_cmd> <session> — was this tab's session taken over
# by a newer tab? Nothing in THIS repo sets WISP_DECK_ADOPTED_BY anymore
# (re-picking an open project now builds into the existing tab instead of
# adopting), but a still-running picker wrapper from the adoption era adopts
# with its in-memory code — this check keeps the new wrapper's cleanup from
# killing a session such a tab took over.
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

# Orphan GC. The wrapper used to end its tmux chain with the SERVER-wide
# `set-option exit-unattached on` — fatal under stacking, where background
# stack sessions legitimately have no attached client. This reaper replaces
# it: every wisp session carries its owning wrapper's pid in session env
# (stamped at launch, restamped on adoption), and a session whose owner died
# without running its trap (SIGKILL, panic) is torn down. Two-strike so a
# launch racing between new-session and the env stamp is never hit.

# stack_reap_orphans <tmux_cmd> <cfg_root>
stack_reap_orphans() {
  local tmux_cmd="$1" cfg="$2"
  local marks="$cfg/stacks/.reap-marks" s env pid f
  mkdir -p "$cfg/stacks" 2>/dev/null || return 0
  while IFS=' ' read -r _ s; do
    [ -n "$s" ] || continue
    env="$("$tmux_cmd" show-environment -t "$s" 2>/dev/null)" || continue
    printf '%s\n' "$env" | grep -qx 'WISP_DECK=1' || continue
    pid="$(printf '%s\n' "$env" | sed -n 's/^WISP_DECK_OWNER_PID=//p' | head -n 1)"
    case "$pid" in '' | *[!0-9]*) continue ;; esac
    if kill -0 "$pid" 2>/dev/null; then
      if grep -qxF "$s" "$marks" 2>/dev/null; then
        grep -vxF "$s" "$marks" > "$marks.tmp.$$" 2>/dev/null || true
        mv "$marks.tmp.$$" "$marks" 2>/dev/null || rm -f "$marks.tmp.$$"
      fi
      continue
    fi
    if grep -qxF "$s" "$marks" 2>/dev/null; then
      cleanup_tmux_session "$s" "" "$tmux_cmd"
      stack_session_files_cleanup "$cfg" "$s"
      grep -vxF "$s" "$marks" > "$marks.tmp.$$" 2>/dev/null || true
      mv "$marks.tmp.$$" "$marks" 2>/dev/null || rm -f "$marks.tmp.$$"
    else
      printf '%s\n' "$s" >> "$marks"
    fi
  done < <("$tmux_cmd" list-sessions -F '#{session_created} #{session_name}' 2>/dev/null)

  # Prune registry files whose owner (the tab that created them) is gone.
  # Nothing else ever cleans up $cfg/stacks/<owner> once the owning wrapper's
  # session dies without running its trap.
  for f in "$cfg"/stacks/*; do
    [ -f "$f" ] || continue
    s="${f##*/}"
    [ "$s" = ".reap-marks" ] && continue
    "$tmux_cmd" has-session -t "$s" 2>/dev/null && continue
    rm -f "$f"
  done
  return 0
}

# stack_reaper_watch <tmux_cmd> <cfg_root> [interval]
stack_reaper_watch() {
  local tmux_cmd="$1" cfg="$2" interval="${3:-30}"
  while "$tmux_cmd" has-session 2>/dev/null; do
    stack_reap_orphans "$tmux_cmd" "$cfg"
    sleep "$interval"
  done
  return 0
}
