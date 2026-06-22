#!/bin/bash
# Spare-pane tabbed terminal.
#
# The spare bottom-left pane runs its own *nested* tmux server (one per Ghost
# Tab window, on a dedicated -L socket). That inner tmux's status bar is pinned
# to the top of the pane and acts as a tab bar: each inner window is a terminal
# tab, the project name labels the first tab, extras are numbered, and clickable
# user-ranges give a [ + ] add button and a per-tab close (×).
#
# The outer tmux keeps mouse OFF so clicks fall through to this inner tmux.
# All click/exit logic routes through the helpers below so it stays testable.

# Deterministic, filesystem-safe inner tmux -L label derived from the outer
# session name. The launcher, the outer keybindings, and cleanup all recompute
# it so they address the same inner server.
spare_tabs_socket() {
  local session_name="$1"
  printf 'gtspare_%s' "$(printf '%s' "$session_name" | tr -c 'A-Za-z0-9_-' '_')"
}

# Emit the inner tmux config (consumed via `tmux -f`).
# Args: <project_name> <project_dir> <lib_path> <socket_label>
# Note: project_dir/lib_path/label are baked in as literals; #{...} stay as
# tmux formats. The mouse handler's \" are intentional — tmux unescapes them.
spare_tabs_config() {
  local project="$1" dir="$2" lib="$3" label="$4"

  # Powerline half-circle caps that round each pill's ends. Built via octal
  # printf so they survive bash 3.2 --posix (no \u escapes): U+E0B6 (left) and
  # U+E0B4 (right).
  local lcap rcap
  lcap="$(printf '\356\202\266')"
  rcap="$(printf '\356\202\264')"

  # Tab bar styling. Each tab is a rounded "pill" built from Powerline
  # half-circle caps ($lcap body $rcap): a neutral grey pill for inactive tabs,
  # the orange brand accent (tied to the active pane border) reserved for the
  # selected tab, and a matching dark pill for the [ + ] add button. The
  # half-circles and ✕ are Nerd Font glyphs (Ghostty renders them natively).
  # Palette: 235 bar bg · 238 idle pill · 209 accent · 250/243 idle text/×.
  cat <<EOF
set -g mouse on
set -g status-position top
set -g exit-unattached on
set -g remain-on-exit on
set -g base-index 1
set -g status-justify left
set -g status-style "bg=colour235"
set -g status-left " "
set -g status-right "#[range=user|new]#[fg=colour238,bg=colour235]$lcap#[fg=colour209,bg=colour238,bold] + #[nobold]#[norange]#[fg=colour238,bg=colour235]$rcap "
set -g window-status-separator " "
set -g @gt_dir "$dir"
set -g window-status-format "#[range=user|sel:#{window_id}]#[fg=colour238,bg=colour235]$lcap#[fg=colour250,bg=colour238] #{?#{==:#{window_index},1},$project,#{window_index}} #[norange]#[range=user|close:#{window_id}]#[fg=colour243,bg=colour238]✕ #[norange]#[fg=colour238,bg=colour235]$rcap"
set -g window-status-current-format "#[range=user|sel:#{window_id}]#[fg=colour209,bg=colour235]$lcap#[fg=colour235,bg=colour209,bold] #{?#{==:#{window_index},1},$project,#{window_index}} #[nobold]#[norange]#[range=user|close:#{window_id}]#[fg=colour237,bg=colour209]✕ #[norange]#[fg=colour209,bg=colour235]$rcap"
bind -n MouseDown1Status run-shell ". \"$lib\" && spare_tabs_dispatch \"$label\" \"#{mouse_status_range}\""
bind -n MouseDown1StatusLeft run-shell ". \"$lib\" && spare_tabs_dispatch \"$label\" \"#{mouse_status_range}\""
bind -n MouseDown1StatusRight run-shell ". \"$lib\" && spare_tabs_dispatch \"$label\" \"#{mouse_status_range}\""
set-hook -g pane-died "if -F \"#{==:#{session_windows},1}\" \"respawn-pane -k\" \"kill-window\""
EOF
}

# The command the spare pane runs. Sheds the parent $TMUX env so tmux allows
# nesting, then execs the inner server; falls back to a plain shell on failure.
# Args: <socket_label> <config_path> <project_dir>
spare_tabs_launch_cmd() {
  local label="$1" conf="$2" dir="$3"
  printf 'env -u TMUX -u TMUX_PANE tmux -L %q -f %q new-session -c %q || exec bash' \
    "$label" "$conf" "$dir"
}

# Close one tab, but never empty the bar: the last remaining tab is respawned
# (fresh shell) instead of killed, so the tab bar always survives.
# Args: <socket_label> <window_id>
spare_tabs_close() {
  local label="$1" win="$2" count dir
  count="$(tmux -L "$label" list-windows -F '#{window_id}' 2>/dev/null | grep -c .)"
  if [ "${count:-0}" -le 1 ]; then
    dir="$(tmux -L "$label" show -gv @gt_dir 2>/dev/null)"
    tmux -L "$label" respawn-pane -k -t "$win" ${dir:+-c "$dir"} 2>/dev/null || true
  else
    tmux -L "$label" kill-window -t "$win" 2>/dev/null || true
  fi
}

# Close whichever tab is currently active (used by the keyboard shortcut).
# Args: <socket_label>
spare_tabs_close_current() {
  local label="$1" win
  win="$(tmux -L "$label" display-message -p '#{window_id}' 2>/dev/null)"
  [ -n "$win" ] && spare_tabs_close "$label" "$win"
}

# Route a status-bar click to its action by the clicked user-range tag.
# Args: <socket_label> <mouse_status_range>
spare_tabs_dispatch() {
  local label="$1" range="$2" dir
  case "$range" in
    new)
      dir="$(tmux -L "$label" show -gv @gt_dir 2>/dev/null)"
      tmux -L "$label" new-window ${dir:+-c "$dir"} 2>/dev/null || true
      ;;
    sel:*)
      tmux -L "$label" select-window -t "${range#sel:}" 2>/dev/null || true
      ;;
    close:*)
      spare_tabs_close "$label" "${range#close:}"
      ;;
  esac
}

# Tear down the detached inner tmux server (it reparents away from the pane, so
# killing the pane tree alone would leak it).
# Args: <socket_label>
spare_tabs_cleanup() {
  local label="$1"
  command -v tmux >/dev/null 2>&1 && tmux -L "$label" kill-server 2>/dev/null || true
}
