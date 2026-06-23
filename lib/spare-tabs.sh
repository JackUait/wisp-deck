#!/bin/bash
# Spare-pane tabbed terminal.
#
# The spare bottom-left pane runs its own *nested* tmux server (one per Ghost
# Tab window, on a dedicated -L socket). That inner tmux's status bar is pinned
# to the top of the pane and acts as a tab bar: each inner window is a terminal
# tab, every tab is labelled by its number (the first included), and clickable
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
  local dir="$2" lib="$3" label="$4"

  # Tab bar styling — deliberately minimal. The selected tab is the only thing
  # with colour: its number on the orange colour209 accent (the app's focus
  # colour, matching the pane-active border). Inactive tabs are plain bracketed
  # labels — [index] for every tab, the first included — no chip, no
  # decoration. The bar itself is transparent (bg=default), so the tabs and the
  # + button float on the terminal background. Closing is keyboard-only
  # (prefix+w); there is no per-tab ✕.
  cat <<EOF
set -g mouse on
set -g status-position top
set -g exit-unattached on
set -g remain-on-exit on
set -g base-index 1
set -g status-justify left
set -g status-style "fg=colour250,bg=default"
set -g window-status-style "bg=default"
# The whole tab list lives in status-left via #{W:...} so the + add button can
# sit immediately after the last tab (status-right is pinned far-right, which is
# why it isn't used). #{?window_active,...} picks the active vs inactive look;
# commas inside #[...] within that conditional are escaped as #, so they aren't
# read as argument separators. The auto window list is blanked out to avoid a
# duplicate. status-left-length is raised so the list is never truncated.
set -g status-left-length 1000
set -g status-left "#{W:#[range=user|sel:#{window_id}]#{?window_active,#[fg=colour235#,bg=colour209#,bold]  #{window_index}  #[nobold]#[norange]#[bg=default],#[default fg=colour245][ #{window_index} ]#[norange]} }#[range=user|new]#[fg=colour209,bg=colour236,bold]  +  #[nobold]#[norange]"
set -g status-right ""
set -g window-status-separator ""
set -g window-status-format ""
set -g window-status-current-format ""
set -g @gt_dir "$dir"
bind -n MouseDown1Status run-shell ". \"$lib\" && spare_tabs_dispatch \"$label\" \"#{mouse_status_range}\""
bind -n MouseDown1StatusLeft run-shell ". \"$lib\" && spare_tabs_dispatch \"$label\" \"#{mouse_status_range}\""
bind -n MouseDown1StatusRight run-shell ". \"$lib\" && spare_tabs_dispatch \"$label\" \"#{mouse_status_range}\""
set-hook -g pane-died "if -F \"#{==:#{session_windows},1}\" \"respawn-pane -k\" \"kill-window\""
EOF
}

# The command the spare pane runs. Sheds the parent $TMUX env so tmux allows
# nesting, then execs the inner server; falls back to a plain shell on failure.
# A non-empty zdotdir is pinned onto the inner tmux env so the spare shell (and
# every tab it spawns) loads the minimal-prompt config from spare_prompt_zdotdir.
# Args: <socket_label> <config_path> <project_dir> [zdotdir]
spare_tabs_launch_cmd() {
  local label="$1" conf="$2" dir="$3" zdotdir="${4:-}"
  local envpfx="env -u TMUX -u TMUX_PANE"
  [ -n "$zdotdir" ] && envpfx="$envpfx ZDOTDIR=$(printf '%q' "$zdotdir")"
  printf '%s tmux -L %q -f %q new-session -c %q || exec bash' \
    "$envpfx" "$label" "$conf" "$dir"
}

# Build a throwaway ZDOTDIR that sources the user's real zsh config and then
# pins a minimal, cwd-only prompt for the spare pane — dropping the system
# user@host prefix and conda's "(base)" so only the directory shows. Only zsh is
# handled; any other login shell is left with its default prompt (returns empty,
# writes nothing). Echoes the generated ZDOTDIR path.
# Args: <share_dir> <session_name> <login_shell> <real_zdotdir>
spare_prompt_zdotdir() {
  local share="$1" session="$2" shell="$3" real="$4"
  case "$shell" in
    */zsh | zsh) ;;
    *) return 0 ;;
  esac

  local target="$share/spare-zdotdir-$session"
  mkdir -p "$target"

  # .zshenv runs first on every shell; re-pin ZDOTDIR so our .zshrc still wins
  # even if the user's .zshenv repoints it.
  cat > "$target/.zshenv" <<EOF
[ -f "$real/.zshenv" ] && . "$real/.zshenv"
ZDOTDIR="$target"
EOF
  cat > "$target/.zprofile" <<EOF
[ -f "$real/.zprofile" ] && . "$real/.zprofile"
EOF
  # Source the real interactive config (conda, PATH, aliases...) then override
  # the prompt last so only the cwd is shown.
  cat > "$target/.zshrc" <<EOF
[ -f "$real/.zshrc" ] && . "$real/.zshrc"
PROMPT='%1~ %# '
EOF
  cat > "$target/.zlogin" <<EOF
[ -f "$real/.zlogin" ] && . "$real/.zlogin"
EOF

  printf '%s\n' "$target"
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
