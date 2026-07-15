#!/usr/bin/env bash

# Exact cross-pane hover routing for the changeset ledger.
#
# tmux identifies the pane under each mouse event before looking up its key
# binding. MouseMovePane itself is not a bindable key, but tmux falls back to
# the active table's Any binding for otherwise-unbound motion. A private table
# provides that fallback for each Wisp session. A constant root-table wrapper
# catches specifically bound pane mouse keys and delegates their original work.

ledger_hover_table_name() {
  local session_name="$2" checksum safe_name
  checksum=$(printf '%s' "$session_name" | cksum) || return 1
  checksum="${checksum%% *}"
  safe_name="${session_name//[^[:alnum:]_]/_}"
  printf 'wisp-ledger-hover-%s-%s\n' "$checksum" "$safe_name"
}

ledger_hover_clone_table() {
  local tmux_cmd="$1" from="$2" to="$3" bindings binding needle replacement config
  local expected got
  bindings=$("$tmux_cmd" list-keys -T "$from" 2>/dev/null) || return 1
  config=$(mktemp "${TMPDIR:-/tmp}/wisp-ledger-hover.XXXXXX") || return 1
  needle="-T $from"
  replacement="-T $to"
  while IFS= read -r binding; do
    printf '%s\n' "${binding/$needle/$replacement}"
  done <<< "$bindings" > "$config"
  "$tmux_cmd" unbind-key -a -T "$to" 2>/dev/null || true
  if ! "$tmux_cmd" source-file -n "$config" >/dev/null 2>&1 ||
     ! "$tmux_cmd" source-file "$config" >/dev/null 2>&1; then
    rm -f "$config"
    "$tmux_cmd" unbind-key -a -T "$to" 2>/dev/null || true
    return 1
  fi
  rm -f "$config"
  expected=$(
    while IFS= read -r binding; do
      printf '%s\n' "${binding/-T $from/-T __table__}"
    done <<< "$bindings"
  )
  got=$(
    while IFS= read -r binding; do
      printf '%s\n' "${binding/-T $to/-T __table__}"
    done < <("$tmux_cmd" list-keys -T "$to" 2>/dev/null)
  )
  if [ "$got" != "$expected" ]; then
    "$tmux_cmd" unbind-key -a -T "$to" 2>/dev/null || true
    return 1
  fi
}

ledger_hover_table_pane_keys() {
  local tmux_cmd="$1" table="$2" binding marker rest key
  marker=" -T $table "
  while IFS= read -r binding; do
    [[ "$binding" == *"$marker"* ]] || continue
    rest="${binding#*"$marker"}"
    key="${rest%% *}"
    [[ "$key" == *Pane ]] && printf '%s\n' "$key"
  done < <("$tmux_cmd" list-keys -T "$table" 2>/dev/null)
}

ledger_hover_normalized_binding() {
  local tmux_cmd="$1" table="$2" key="$3" binding
  binding=$("$tmux_cmd" list-keys -T "$table" "$key" 2>/dev/null || true)
  [ -n "$binding" ] && printf '%s\n' "${binding/-T $table/-T __table__}"
}

ledger_hover_copy_binding() {
  local tmux_cmd="$1" from="$2" to="$3" key="$4" binding config expected got
  binding=$("$tmux_cmd" list-keys -T "$from" "$key" 2>/dev/null || true)
  if [ -z "$binding" ]; then
    "$tmux_cmd" unbind-key -T "$to" "$key" 2>/dev/null || true
    return 0
  fi
  config=$(mktemp "${TMPDIR:-/tmp}/wisp-ledger-hover.XXXXXX") || return 1
  printf '%s\n' "${binding/-T $from/-T $to}" > "$config"
  if ! "$tmux_cmd" source-file -n "$config" >/dev/null 2>&1 ||
     ! "$tmux_cmd" source-file "$config" >/dev/null 2>&1; then
    rm -f "$config"
    return 1
  fi
  rm -f "$config"
  expected=$(ledger_hover_normalized_binding "$tmux_cmd" "$from" "$key")
  got=$(ledger_hover_normalized_binding "$tmux_cmd" "$to" "$key")
  [ "$got" = "$expected" ]
}

ledger_hover_sync_root_changes() {
  local tmux_cmd="$1" original_table="$2" active_table="$3" keys key current active
  keys=$({
    ledger_hover_table_pane_keys "$tmux_cmd" root
    ledger_hover_table_pane_keys "$tmux_cmd" "$active_table"
  } | sort -u)
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    current=$(ledger_hover_normalized_binding "$tmux_cmd" root "$key")
    active=$(ledger_hover_normalized_binding "$tmux_cmd" "$active_table" "$key")
    if [ "$current" != "$active" ]; then
      ledger_hover_copy_binding "$tmux_cmd" root "$original_table" "$key" || return 1
    fi
  done <<< "$keys"
}

ledger_hover_restore_root_pane_bindings() {
  local tmux_cmd="$1" original_table="$2" active_table="$3" keys key
  keys=$({
    ledger_hover_table_pane_keys "$tmux_cmd" root
    ledger_hover_table_pane_keys "$tmux_cmd" "$original_table"
    ledger_hover_table_pane_keys "$tmux_cmd" "$active_table"
  } | sort -u)
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    "$tmux_cmd" unbind-key -T root "$key" 2>/dev/null || true
    ledger_hover_copy_binding "$tmux_cmd" "$original_table" root "$key" || return 1
  done <<< "$keys"
}

ledger_hover_wrap_root_pane_bindings() {
  local tmux_cmd="$1" original_table="$2" active_table="$3"
  local binding table_marker rest key original_command has_ledger is_ledger was_inside
  local leave_route inside_route outside_route route config keys got

  config=$(mktemp "${TMPDIR:-/tmp}/wisp-ledger-hover.XXXXXX") || return 1
  table_marker=" -T $original_table "
  has_ledger='#{@wisp_ledger_hover_pane}'
  is_ledger='#{==:#{pane_id},#{@wisp_ledger_hover_pane}}'
  was_inside='#{==:#{@wisp_ledger_hover_inside},1}'
  leave_route="run-shell -C -t = 'send-keys -t \"#{@wisp_ledger_hover_pane}\" -H 1b 5b 3c 33 35 3b 39 39 39 39 3b 39 39 39 39 4d'"
  inside_route="if-shell -F '$was_inside' '' { set-option -t = @wisp_ledger_hover_inside 1 }"
  outside_route="if-shell -F '$was_inside' { set-option -t = @wisp_ledger_hover_inside 0 ; $leave_route } ''"
  while IFS= read -r binding; do
    [[ "$binding" == *"$table_marker"* ]] || continue
    rest="${binding#*"$table_marker"}"
    key="${rest%% *}"
    [[ "$key" == *Pane ]] || continue
    original_command="${rest#"$key"}"
    original_command="${original_command# }"
    route="if-shell -F -t = '$has_ledger' { if-shell -F -t = '$is_ledger' { $inside_route } { $outside_route } } ''"
    if [[ "$binding" == "bind-key -r "* ]]; then
      printf 'bind-key -r -T root %s %s \\; %s\n' "$key" "$route" "$original_command"
    else
      printf 'bind-key -T root %s %s \\; %s\n' "$key" "$route" "$original_command"
    fi
  done < <("$tmux_cmd" list-keys -T "$original_table" 2>/dev/null) > "$config"

  if ! "$tmux_cmd" source-file -n "$config" >/dev/null 2>&1; then
    rm -f "$config"
    return 1
  fi
  keys=$({
    ledger_hover_table_pane_keys "$tmux_cmd" "$original_table"
    ledger_hover_table_pane_keys "$tmux_cmd" "$active_table"
  } | sort -u)
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    "$tmux_cmd" unbind-key -T root "$key" 2>/dev/null || true
  done <<< "$keys"
  if ! "$tmux_cmd" source-file "$config" >/dev/null 2>&1; then
    rm -f "$config"
    ledger_hover_restore_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table" || true
    return 1
  fi
  rm -f "$config"

  keys=$(ledger_hover_table_pane_keys "$tmux_cmd" "$original_table")
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    got=$("$tmux_cmd" list-keys -T root "$key" 2>/dev/null || true)
    if [[ "$got" != *'@wisp_ledger_hover_inside'* ]]; then
      ledger_hover_restore_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table" || true
      return 1
    fi
  done <<< "$keys"
  if ! ledger_hover_clone_table "$tmux_cmd" root "$active_table"; then
    ledger_hover_restore_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table" || true
    return 1
  fi
}

# tmux checks specifically bound clicks/wheels outside Any in some key-table
# states. Install one constant-size root wrapper which reads the target
# session's options dynamically, then delegates to the original command.
ledger_hover_reconcile_root_mouse_unlocked() {
  local tmux_cmd="$1" ensure="${2:-0}"
  local original_table active_table marker live

  original_table='wisp-ledger-hover-root-original'
  active_table='wisp-ledger-hover-root-active'
  marker=$("$tmux_cmd" show-options -gqv '@wisp_ledger_hover_root_original' 2>/dev/null || true)
  if [ -z "$marker" ]; then
    [ "$ensure" = "1" ] || return 0
    ledger_hover_clone_table "$tmux_cmd" root "$original_table" || return 1
    if ! ledger_hover_wrap_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table"; then
      ledger_hover_restore_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table" || true
      "$tmux_cmd" unbind-key -a -T "$original_table" 2>/dev/null || true
      "$tmux_cmd" unbind-key -a -T "$active_table" 2>/dev/null || true
      return 1
    fi
    if ! "$tmux_cmd" set-option -g '@wisp_ledger_hover_root_original' "$original_table"; then
      ledger_hover_restore_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table" || true
      "$tmux_cmd" unbind-key -a -T "$original_table" 2>/dev/null || true
      "$tmux_cmd" unbind-key -a -T "$active_table" 2>/dev/null || true
      return 1
    fi
    return 0
  fi

  ledger_hover_sync_root_changes "$tmux_cmd" "$original_table" "$active_table" || return 1
  live=$("$tmux_cmd" list-sessions -F '#{@wisp_ledger_hover_pane}' 2>/dev/null || true)
  if printf '%s\n' "$live" | awk 'NF { found=1 } END { exit !found }'; then
    ledger_hover_wrap_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table"
    return $?
  fi

  ledger_hover_restore_root_pane_bindings "$tmux_cmd" "$original_table" "$active_table" || return 1
  "$tmux_cmd" unbind-key -a -T "$original_table" 2>/dev/null || true
  "$tmux_cmd" unbind-key -a -T "$active_table" 2>/dev/null || true
  "$tmux_cmd" set-option -gqu '@wisp_ledger_hover_root_original' 2>/dev/null || true
}

ledger_hover_acquire_root_lock() {
  local tmux_cmd="$1" socket checksum
  socket=$("$tmux_cmd" display-message -p '#{socket_path}' 2>/dev/null) || return 1
  checksum=$(printf '%s' "$socket" | cksum) || return 1
  checksum="${checksum%% *}"
  _ledger_hover_lock_path="${TMPDIR:-/tmp}/wisp-ledger-hover-root-$checksum.lock"
  exec 9>"$_ledger_hover_lock_path" || return 1
  if command -v lockf >/dev/null 2>&1; then
    lockf -t 15 9 || {
      exec 9>&-
      return 1
    }
  elif command -v flock >/dev/null 2>&1; then
    flock -w 15 9 || {
      exec 9>&-
      return 1
    }
  else
    exec 9>&-
    return 1
  fi
}

ledger_hover_release_root_lock() {
  [ -n "${_ledger_hover_lock_path:-}" ] || return 0
  exec 9>&-
  _ledger_hover_lock_path=''
}

ledger_hover_rebuild_root_mouse() (
  local tmux_cmd="$1" ensure="${2:-0}"
  ledger_hover_acquire_root_lock "$tmux_cmd" || return 1
  trap ledger_hover_release_root_lock EXIT
  trap 'exit 130' HUP INT TERM
  ledger_hover_reconcile_root_mouse_unlocked "$tmux_cmd" "$ensure"
)

# ledger_hover_uninstall <tmux-command> <session>
# Restore the session's original default table and remove its private clone.
ledger_hover_uninstall() {
  local tmux_cmd="$1" session_name="$2" table base base_mouse current

  table=$("$tmux_cmd" show-options -qv -t "$session_name" '@wisp_ledger_hover_table' 2>/dev/null || true)
  base=$("$tmux_cmd" show-options -qv -t "$session_name" '@wisp_ledger_hover_base' 2>/dev/null || true)
  base_mouse=$("$tmux_cmd" show-options -qv -t "$session_name" '@wisp_ledger_hover_mouse' 2>/dev/null || true)
  current=$("$tmux_cmd" show-options -Aqv -t "$session_name" key-table 2>/dev/null || true)
  if [ -z "$table" ]; then
    table=$(ledger_hover_table_name "$tmux_cmd" "$session_name") || return 1
  fi

  if [ -n "$table" ] && [ "$current" = "$table" ]; then
    "$tmux_cmd" set-option -t "$session_name" key-table "${base:-root}" 2>/dev/null || true
  fi
  if [ -n "$base_mouse" ]; then
    "$tmux_cmd" set-option -t "$session_name" mouse "$base_mouse" 2>/dev/null || true
  fi
  if [ -n "$table" ]; then
    "$tmux_cmd" unbind-key -a -T "$table" 2>/dev/null || true
  fi

  "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_table' 2>/dev/null || true
  "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_base' 2>/dev/null || true
  "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_mouse' 2>/dev/null || true
  "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_inside' 2>/dev/null || true
  "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_pane' 2>/dev/null || true
  ledger_hover_rebuild_root_mouse "$tmux_cmd" 0 || true
}

# ledger_hover_install <tmux-command> <session> <ledger-pane-id>
# Clone the session's effective root table, add exact pane-leave routing, then
# activate the clone for this session only. The synthetic out-of-bounds motion
# is understood by both native and retained shell ledger renderers.
ledger_hover_install() {
  local tmux_cmd="$1" session_name="$2" ledger_pane="$3"
  local base base_mouse table bindings binding config keys got
  local original_any original_any_binding inside_condition was_inside leave_route mouse_handler
  local table_marker rest key original_command inside_handler outside_handler wrapped_binding

  [ -n "$tmux_cmd" ] && [ -n "$session_name" ] && [ -n "$ledger_pane" ] || return 1
  "$tmux_cmd" has-session -t "$session_name" 2>/dev/null || return 1

  # Reinstallation must start from the user's real base table, not recursively
  # clone a stale Wisp table left by an earlier setup attempt.
  ledger_hover_uninstall "$tmux_cmd" "$session_name"
  base=$("$tmux_cmd" show-options -Aqv -t "$session_name" key-table 2>/dev/null || true)
  base="${base:-root}"
  base_mouse=$("$tmux_cmd" show-options -Aqv -t "$session_name" mouse 2>/dev/null || true)
  base_mouse="${base_mouse:-off}"
  table=$(ledger_hover_table_name "$tmux_cmd" "$session_name") || return 1
  bindings=$("$tmux_cmd" list-keys -T "$base" 2>/dev/null) || return 1
  original_any_binding=$("$tmux_cmd" list-keys -T "$base" Any 2>/dev/null || true)
  original_any="send-keys"
  if [[ "$original_any_binding" == *" Any "* ]]; then
    original_any="${original_any_binding#* Any }"
    original_any="${original_any//\\;/;}"
  fi

  ledger_hover_clone_table "$tmux_cmd" "$base" "$table" || return 1

  # Remember whether the last pane-motion event was inside the ledger. The
  # first event in a neighbour emits one leave; further motion there is only
  # forwarded to its real target and does not keep waking the ledger.
  inside_condition='#{==:#{mouse_pane},#{@wisp_ledger_hover_pane}}'
  was_inside='#{==:#{@wisp_ledger_hover_inside},1}'
  leave_route="run-shell -C -t = 'send-keys -t \"#{@wisp_ledger_hover_pane}\" -H 1b 5b 3c 33 35 3b 39 39 39 39 3b 39 39 39 39 4d'"
  inside_handler="if-shell -F '$was_inside' { send-keys -M } { set-option -t = @wisp_ledger_hover_inside 1 ; send-keys -M }"
  outside_handler="if-shell -F '$was_inside' { set-option -t = @wisp_ledger_hover_inside 0 ; $leave_route ; send-keys -M } { send-keys -M }"
  mouse_handler="if-shell -F '$inside_condition' { $inside_handler } { $outside_handler }"
  if ! "$tmux_cmd" bind-key -T "$table" Any \
    if-shell -F '#{mouse_pane}' "$mouse_handler" "$original_any"; then
    "$tmux_cmd" unbind-key -a -T "$table" 2>/dev/null || true
    return 1
  fi

  # Specifically bound clicks/wheels do not reach Any. Wrap their copies in
  # this session table as well; the root wrappers below cover tmux's temporary
  # fallback to root (for example while changing key tables).
  config=$(mktemp "${TMPDIR:-/tmp}/wisp-ledger-hover.XXXXXX") || return 1
  table_marker=" -T $base "
  while IFS= read -r binding; do
    [[ "$binding" == *"$table_marker"* ]] || continue
    rest="${binding#*"$table_marker"}"
    key="${rest%% *}"
    [[ "$key" == *Pane ]] || continue
    original_command="${rest#"$key"}"
    original_command="${original_command# }"
    original_command="${original_command//\\;/;}"
    [ -n "$original_command" ] || continue
    if [[ "$binding" == *'@wisp_ledger_hover_inside'* ]]; then
      continue
    fi
    inside_handler="if-shell -F '$was_inside' '' { set-option -t = @wisp_ledger_hover_inside 1 }"
    outside_handler="if-shell -F '$was_inside' { set-option -t = @wisp_ledger_hover_inside 0 ; $leave_route } ''"
    wrapped_binding="if-shell -F -t = '#{==:#{pane_id},#{@wisp_ledger_hover_pane}}' { $inside_handler ; $original_command } { $outside_handler ; $original_command }"
    if [[ "$binding" == "bind-key -r "* ]]; then
      printf 'bind-key -r -T %s %s %s\n' "$table" "$key" "$wrapped_binding"
    else
      printf 'bind-key -T %s %s %s\n' "$table" "$key" "$wrapped_binding"
    fi
  done <<< "$bindings" > "$config"
  if ! "$tmux_cmd" source-file -n "$config" >/dev/null 2>&1 ||
     ! "$tmux_cmd" source-file "$config" >/dev/null 2>&1; then
    rm -f "$config"
    "$tmux_cmd" unbind-key -a -T "$table" 2>/dev/null || true
    return 1
  fi
  rm -f "$config"
  keys=$(ledger_hover_table_pane_keys "$tmux_cmd" "$base")
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    got=$("$tmux_cmd" list-keys -T "$table" "$key" 2>/dev/null || true)
    if [[ "$got" != *'@wisp_ledger_hover_inside'* ]]; then
      "$tmux_cmd" unbind-key -a -T "$table" 2>/dev/null || true
      return 1
    fi
  done <<< "$keys"

  if ! "$tmux_cmd" set-option -t "$session_name" '@wisp_ledger_hover_base' "$base" \; \
    set-option -t "$session_name" '@wisp_ledger_hover_mouse' "$base_mouse" \; \
    set-option -t "$session_name" '@wisp_ledger_hover_table' "$table" \; \
    set-option -t "$session_name" '@wisp_ledger_hover_inside' 0 \; \
    set-option -t "$session_name" '@wisp_ledger_hover_pane' "$ledger_pane" \; \
    set-option -t "$session_name" key-table "$table" \; \
    set-option -t "$session_name" mouse on; then
    "$tmux_cmd" unbind-key -a -T "$table" 2>/dev/null || true
    "$tmux_cmd" set-option -t "$session_name" mouse "$base_mouse" 2>/dev/null || true
    "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_base' 2>/dev/null || true
    "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_mouse' 2>/dev/null || true
    "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_table' 2>/dev/null || true
    "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_inside' 2>/dev/null || true
    "$tmux_cmd" set-option -qu -t "$session_name" '@wisp_ledger_hover_pane' 2>/dev/null || true
    return 1
  fi
  if ! ledger_hover_rebuild_root_mouse "$tmux_cmd" 1; then
    ledger_hover_uninstall "$tmux_cmd" "$session_name"
    return 1
  fi
}
