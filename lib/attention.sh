#!/bin/bash
# Private, generation-fenced runtime shared by semantic attention adapters.
# Compatible with the stock Bash 3.2 shipped by macOS.

# Cleanup authority is process-local: only roots allocated by this sourced
# runtime may be recursively removed by the same shell.
_ATTENTION_OWNED_ROOTS=$'\n'
_ATTENTION_RELAUNCH_LOCK_ROOT=""
_ATTENTION_RELAUNCH_LOCK_TOKEN=""

_attention_has_record_delimiter() {
  case "${1-}" in
    *$'\t'*|*$'\r'*|*$'\n'*) return 0 ;;
  esac
  return 1
}

_attention_valid_field() {
  [ -n "${1-}" ] && ! _attention_has_record_delimiter "$1"
}

_attention_valid_tool() {
  case "${1-}" in
    claude|codex|opencode) return 0 ;;
  esac
  return 1
}

_attention_valid_generation() {
  local generation="${1-}" suffix
  case "$generation" in
    generation.*) suffix="${generation#generation.}" ;;
    *) return 1 ;;
  esac
  case "$suffix" in
    ''|*[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789]*) return 1 ;;
  esac
  return 0
}

_attention_track_root() {
  local root="${1-}"
  case "$_ATTENTION_OWNED_ROOTS" in
    *$'\n'"$root"$'\n'*) return 0 ;;
  esac
  _ATTENTION_OWNED_ROOTS="${_ATTENTION_OWNED_ROOTS}${root}"$'\n'
}

_attention_root_is_owned() {
  local root="${1-}"
  case "$_ATTENTION_OWNED_ROOTS" in
    *$'\n'"$root"$'\n'*) return 0 ;;
  esac
  return 1
}

_attention_forget_root() {
  local root="${1-}" line rebuilt=$'\n'
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    [ "$line" = "$root" ] || rebuilt="${rebuilt}${line}"$'\n'
  done <<EOF
$_ATTENTION_OWNED_ROOTS
EOF
  _ATTENTION_OWNED_ROOTS="$rebuilt"
}

# _attention_atomic_replace <target> <record-without-newline>
# The target's parent must already exist. A sibling temporary file is renamed
# over the target so readers observe only a complete old or complete new line.
_attention_atomic_replace() {
  local target="${1-}" record="${2-}" parent tmp
  _attention_valid_field "$target" || return 1
  [ -n "$record" ] || return 1
  case "$record" in
    *$'\r'*|*$'\n'*) return 1 ;;
  esac
  parent="${target%/*}"
  [ -n "$parent" ] && [ -d "$parent" ] || return 1
  tmp="$(umask 077; mktemp "$parent/.attention.XXXXXX")" || return 1
  if ! chmod 600 "$tmp" \
     || ! printf '%s\n' "$record" >"$tmp" \
     || ! mv -f "$tmp" "$target"; then
    rm -f "$tmp"
    return 1
  fi
  return 0
}

# _attention_parse_descriptor_file <descriptor>
# On success, populate private descriptor fields without printing them.
_attention_parse_descriptor_file() {
  local descriptor="${1-}" bytes line extra second_status tab
  local version generation tool state rest root
  _attention_valid_field "$descriptor" || return 1
  [ -f "$descriptor" ] || return 1

  bytes="$(wc -c <"$descriptor" 2>/dev/null | tr -d '[:space:]')" || return 1
  case "$bytes" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$bytes" -le 4096 ] || return 1

  line=""
  extra=""
  second_status=0
  {
    IFS= read -r line || return 1
    if IFS= read -r extra; then
      second_status=0
    else
      second_status=$?
    fi
  } <"$descriptor"
  [ "$second_status" -ne 0 ] && [ -z "$extra" ] || return 1
  case "$line" in
    *$'\r'*) return 1 ;;
  esac

  tab=$'\t'
  version=${line%%"$tab"*}
  [ "$version" != "$line" ] || return 1
  rest=${line#*"$tab"}
  generation=${rest%%"$tab"*}
  [ "$generation" != "$rest" ] || return 1
  rest=${rest#*"$tab"}
  tool=${rest%%"$tab"*}
  [ "$tool" != "$rest" ] || return 1
  state=${rest#*"$tab"}
  case "$state" in
    *"$tab"*) return 1 ;;
  esac

  [ "$version" = "1" ] || return 1
  _attention_valid_generation "$generation" || return 1
  _attention_valid_tool "$tool" || return 1
  _attention_valid_field "$state" || return 1
  root="${descriptor%/*}"
  [ "$state" = "$root/$generation/state" ] || return 1

  _ATTENTION_DESCRIPTOR_GENERATION="$generation"
  _ATTENTION_DESCRIPTOR_TOOL="$tool"
  _ATTENTION_DESCRIPTOR_STATE="$state"
  return 0
}

# attention_session_create <tmp-base>
# Allocate a private root and expose/print its stable descriptor location.
attention_session_create() {
  local tmp_base="${1-}" root owner_start owner_record
  _attention_valid_field "$tmp_base" || return 1
  [ -d "$tmp_base" ] || return 1

  root="$(umask 077; mktemp -d "$tmp_base/wisp-deck-attention.XXXXXX")" || return 1
  if ! chmod 700 "$root"; then
    rm -rf "$root"
    return 1
  fi

  # Background adapters outlive the short account-switch shells that may
  # launch them. Record the exact wrapper process identity once in the stable
  # session root so every generation and switch can validate the same owner;
  # PID alone is unsafe after reuse.
  owner_start="$(LC_ALL=C TZ=UTC /bin/ps -p "$$" -o lstart= 2>/dev/null \
    | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  if ! _attention_valid_field "$owner_start"; then
    rm -rf "$root"
    return 1
  fi
  owner_record="1"$'\t'"$$"$'\t'"$owner_start"
  if ! _attention_atomic_replace "$root/owner" "$owner_record"; then
    rm -rf "$root"
    return 1
  fi

  _attention_track_root "$root"

  WISP_DECK_ATTENTION_ROOT="$root"
  WISP_DECK_ATTENTION_DESCRIPTOR="$root/descriptor"
  unset WISP_DECK_ATTENTION_GENERATION WISP_DECK_ATTENTION_FILE
  export WISP_DECK_ATTENTION_ROOT WISP_DECK_ATTENTION_DESCRIPTOR
  printf '%s\n' "$root"
}

# attention_begin_generation <root> <claude|codex|opencode>
# Publish a fresh unknown state and descriptor, then remove the superseded
# generation directory so late writers cannot recreate or mutate current state.
attention_begin_generation() {
  local root="${1-}" tool="${2-}" descriptor
  local old_generation="" old_state="" old_dir=""
  local generation_dir generation state state_record descriptor_record

  _attention_valid_field "$root" || return 1
  _attention_valid_tool "$tool" || return 1
  [ -d "$root" ] || return 1
  descriptor="$root/descriptor"

  if [ -e "$descriptor" ]; then
    _attention_parse_descriptor_file "$descriptor" || return 1
    old_generation="$_ATTENTION_DESCRIPTOR_GENERATION"
    old_state="$_ATTENTION_DESCRIPTOR_STATE"
  fi

  generation_dir="$(umask 077; mktemp -d "$root/generation.XXXXXX")" || return 1
  generation="${generation_dir##*/}"
  state="$generation_dir/state"
  if ! _attention_valid_generation "$generation" \
     || ! chmod 700 "$generation_dir"; then
    rm -rf "$generation_dir"
    return 1
  fi

  state_record="1"$'\t'"$generation"$'\t'"0"$'\t'"unknown"$'\t'"-"
  if ! _attention_atomic_replace "$state" "$state_record"; then
    rm -rf "$generation_dir"
    return 1
  fi

  descriptor_record="1"$'\t'"$generation"$'\t'"$tool"$'\t'"$state"
  if ! _attention_atomic_replace "$descriptor" "$descriptor_record"; then
    rm -rf "$generation_dir"
    return 1
  fi

  WISP_DECK_ATTENTION_ROOT="$root"
  WISP_DECK_ATTENTION_DESCRIPTOR="$descriptor"
  WISP_DECK_ATTENTION_GENERATION="$generation"
  WISP_DECK_ATTENTION_FILE="$state"
  export WISP_DECK_ATTENTION_ROOT WISP_DECK_ATTENTION_DESCRIPTOR
  export WISP_DECK_ATTENTION_GENERATION WISP_DECK_ATTENTION_FILE

  # Delete only a runtime-owned path that exactly matches its descriptor.
  case "$old_generation" in
    generation.*)
      old_dir="$root/$old_generation"
      if [ "$old_generation" != "$generation" ] \
         && [ "$old_state" = "$old_dir/state" ]; then
        rm -rf "$old_dir" || return 1
      fi
      ;;
  esac

  printf '%s\t%s\t%s\n' "$generation" "$state" "$descriptor"
}

# attention_read_descriptor <descriptor>
# Print generation, tool, and state path only for one strict complete record.
attention_read_descriptor() {
  _attention_parse_descriptor_file "${1-}" || return 1
  printf '%s\t%s\t%s\n' \
    "$_ATTENTION_DESCRIPTOR_GENERATION" \
    "$_ATTENTION_DESCRIPTOR_TOOL" \
    "$_ATTENTION_DESCRIPTOR_STATE"
}

# attention_relaunch_lock_acquire <root>
# Serialize the rotate/stamp/build/respawn transaction across the independent
# ledger and auto-switch shells. Dead recorded owners are reclaimed. An
# ownerless lock is never reclaimed: its creator may be paused between mkdir
# and publishing ownership, so removing it could admit two concurrent owners.
attention_relaunch_lock_acquire() {
  local root="${1-}" lock owner owner_pid owner_token
  local attempts=0 token
  _attention_valid_field "$root" || return 1
  [ -d "$root" ] || return 1
  [ -z "$_ATTENTION_RELAUNCH_LOCK_ROOT" ] || return 1
  lock="$root/.relaunch-lock"
  owner="$lock/owner"

  while ! mkdir "$lock" 2>/dev/null; do
    if [ -f "$owner" ]; then
      owner_pid=""
      owner_token=""
      IFS=$'\t' read -r owner_pid owner_token <"$owner" || true
      case "$owner_pid" in
        ''|*[!0-9]*) ;;
        *)
          if ! kill -0 "$owner_pid" 2>/dev/null; then
            rm -rf "$lock" 2>/dev/null || true
          fi
          ;;
      esac
    fi
    attempts=$((attempts + 1))
    [ "$attempts" -lt 400 ] || return 1
    sleep 0.05
  done

  if ! chmod 700 "$lock"; then
    rmdir "$lock" 2>/dev/null || true
    return 1
  fi
  token="$$.${RANDOM:-0}.${RANDOM:-0}"
  if ! printf '%s\t%s\n' "$$" "$token" >"$owner" \
     || ! chmod 600 "$owner"; then
    rm -f "$owner"
    rmdir "$lock" 2>/dev/null || true
    return 1
  fi

  _ATTENTION_RELAUNCH_LOCK_ROOT="$root"
  _ATTENTION_RELAUNCH_LOCK_TOKEN="$token"
  return 0
}

# attention_relaunch_lock_release <root>
attention_relaunch_lock_release() {
  local root="${1-}" lock owner owner_pid="" owner_token=""
  [ -n "$root" ] && [ "$root" = "$_ATTENTION_RELAUNCH_LOCK_ROOT" ] || return 1
  lock="$root/.relaunch-lock"
  owner="$lock/owner"
  IFS=$'\t' read -r owner_pid owner_token <"$owner" || return 1
  [ "$owner_pid" = "$$" ] \
    && [ "$owner_token" = "$_ATTENTION_RELAUNCH_LOCK_TOKEN" ] || return 1
  rm -rf "$lock" || return 1
  _ATTENTION_RELAUNCH_LOCK_ROOT=""
  _ATTENTION_RELAUNCH_LOCK_TOKEN=""
  return 0
}

# attention_start_claude_background_candidate <claude> <config-root>
#   <wisp-config-root> <owner-root> <default|isolated> [error-log]
#
# Start a terminal-detached candidate for one exact Claude account. The Go
# broker uses owner-root/owner for wrapper PID+start validation, keeps one local
# claim per owner/root, and elects the account-global leader. This helper is
# deliberately best-effort: an unavailable broker must never prevent Claude or
# an account/tool switch from launching.
attention_start_claude_background_candidate() {
  local claude="${1-}" config_root="${2-}" wisp_config="${3-}"
  local owner_root="${4-}" config_mode="${5-}"
  local error_log="${6:-/dev/null}" candidate_dir
  local -a candidate_args

  case "$claude" in /*) ;; *) return 0 ;; esac
  case "$config_root" in /*) ;; *) return 0 ;; esac
  case "$wisp_config" in /*) ;; *) return 0 ;; esac
  case "$owner_root" in /*) ;; *) return 0 ;; esac
  case "$config_mode" in default|isolated) ;; *) return 0 ;; esac
  [ -d "$config_root" ] || return 0
  [ -d "$wisp_config" ] || return 0
  [ -d "$owner_root" ] && [ -f "$owner_root/owner" ] || return 0

  candidate_dir="$owner_root/claude-background-candidates"
  if ! (umask 077; mkdir -p "$candidate_dir") || ! chmod 700 "$candidate_dir"; then
    return 0
  fi
  if ! : >>"$error_log" 2>/dev/null; then
    error_log=/dev/null
  fi

  candidate_args=(
    claude-background
    --claude "$claude"
    --config-dir "$config_root"
    --wisp-config-dir "$wisp_config"
    --owner-root "$owner_root"
  )
  [ "$config_mode" = default ] && candidate_args+=(--default-config)
  (
    exec 3>&-
    exec nohup wisp-deck-tui "${candidate_args[@]}"
  ) </dev/null >/dev/null 2>>"$error_log" &
  return 0
}

# attention_cleanup <root>
# Idempotently remove only a root produced by attention_session_create.
attention_cleanup() {
  local root="${1-}"
  _attention_valid_field "$root" || return 1
  [ -e "$root" ] || return 0
  _attention_root_is_owned "$root" || return 1
  case "${root##*/}" in
    wisp-deck-attention.*) ;;
    *) return 1 ;;
  esac
  rm -rf "$root" || return 1
  _attention_forget_root "$root"
  if [ "${WISP_DECK_ATTENTION_ROOT-}" = "$root" ]; then
    unset WISP_DECK_ATTENTION_ROOT WISP_DECK_ATTENTION_DESCRIPTOR
    unset WISP_DECK_ATTENTION_GENERATION WISP_DECK_ATTENTION_FILE
  fi
  return 0
}
