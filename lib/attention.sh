#!/bin/bash
# Private, generation-fenced runtime shared by semantic attention adapters.
# Compatible with the stock Bash 3.2 shipped by macOS.

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
  local version generation tool state rest
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
  _attention_valid_field "$generation" || return 1
  _attention_valid_tool "$tool" || return 1
  _attention_valid_field "$state" || return 1

  _ATTENTION_DESCRIPTOR_GENERATION="$generation"
  _ATTENTION_DESCRIPTOR_TOOL="$tool"
  _ATTENTION_DESCRIPTOR_STATE="$state"
  return 0
}

# attention_session_create <tmp-base>
# Allocate a private root and expose/print its stable descriptor location.
attention_session_create() {
  local tmp_base="${1-}" root
  _attention_valid_field "$tmp_base" || return 1
  [ -d "$tmp_base" ] || return 1

  root="$(umask 077; mktemp -d "$tmp_base/wisp-deck-attention.XXXXXX")" || return 1
  if ! chmod 700 "$root"; then
    rm -rf "$root"
    return 1
  fi

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
  if ! chmod 700 "$generation_dir"; then
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
        rm -rf "$old_dir"
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

# attention_cleanup <root>
# Idempotently remove only a root produced by attention_session_create.
attention_cleanup() {
  local root="${1-}"
  _attention_valid_field "$root" || return 1
  case "${root##*/}" in
    wisp-deck-attention.*) ;;
    *) return 1 ;;
  esac
  rm -rf "$root" || return 1
  if [ "${WISP_DECK_ATTENTION_ROOT-}" = "$root" ]; then
    unset WISP_DECK_ATTENTION_ROOT WISP_DECK_ATTENTION_DESCRIPTOR
    unset WISP_DECK_ATTENTION_GENERATION WISP_DECK_ATTENTION_FILE
  fi
  return 0
}
