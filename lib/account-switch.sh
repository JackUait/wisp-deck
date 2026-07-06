#!/bin/bash
# shellcheck disable=SC2059  # Intentional: ANSI escape variables in printf format strings
# Mid-session Claude account switching — the clickable ledger pill and the AI-pane
# relaunch behind it. Lets the user change the active native Claude login WHILE a
# session runs: click the account pill in the compact-view ledger, pick a login in
# the popup, and the running `claude` is relaunched under that login's isolated
# CLAUDE_CONFIG_DIR in continue (`-c`) mode. History is shared across accounts
# (symlinked into ~/.claude), so the conversation carries over.
#
# Depends on (sourced alongside by the caller): statusline.sh (gt_* helpers),
# claude-accounts.sh (pointer helpers), tmux-session.sh (build_ai_launch_cmd),
# and — for the relaunch's shared-state sync — claude-shared-settings.sh.

# reload_switcher_lib <lib_dir> — re-source THIS module from disk so a
# long-running ledger picks up on-disk edits to the switcher (popup dimensions,
# flags, backdrop) without the whole pane having to restart. Called right before
# opening the switcher. No-op when the file is missing (keeps the resident copy).
reload_switcher_lib() {
  local lib_dir="$1"
  [ -n "$lib_dir" ] && [ -f "$lib_dir/account-switch.sh" ] || return 0
  # shellcheck source=/dev/null
  source "$lib_dir/account-switch.sh"
}

# switcher_supports_session_flags — exit 0 when the installed wisp-deck-tui
# accepts --active/--result-file on claude-account-switch. The lib is installed
# as a live symlink while the binary is a copy, so a newer lib can face an older
# binary that would REJECT the new flags (cobra errors out before the UI runs)
# and the switcher would silently stop switching. Legacy is only assumed when
# the help output positively shows the command WITHOUT --result-file; a missing
# binary or unreadable help counts as supported (the popup can't run either
# way, and the current binary is the norm). Cached per process ($$-keyed would
# be overkill: reload_switcher_lib re-sources this file, so keep the cache in a
# var this file does not reset).
switcher_supports_session_flags() {
  if [ -z "${_GT_SWITCHER_FLAGS_PROBE:-}" ]; then
    local help
    help="$(wisp-deck-tui claude-account-switch --help 2>&1)" || help=""
    if printf '%s' "$help" | grep -q 'claude-account-switch' \
       && ! printf '%s' "$help" | grep -q -- '--result-file'; then
      _GT_SWITCHER_FLAGS_PROBE=legacy
    else
      _GT_SWITCHER_FLAGS_PROBE=ok
    fi
  fi
  [ "$_GT_SWITCHER_FLAGS_PROBE" = ok ]
}

# account_pill_enabled <relaunch_file> <list_file> — exit 0 when the ledger should
# show the switch pill. The tool/proxy eligibility gate lives in wrapper.sh, which
# only writes the relaunch-context file for a claude session with the rotation
# proxy OFF; so here it is enough that the relaunch file exists AND there are 2+
# accounts to switch between (a single managed login + the implicit Default).
account_pill_enabled() {
  local relaunch_file="$1" list_file="$2"
  [ -n "$relaunch_file" ] && [ -f "$relaunch_file" ] || return 1
  gt_multiple_claude_accounts "$list_file"
}

# account_pill <label> <color> [hover] — render the account pill for the ledger
# bottom bar. Line 1 is the drawable string (a pad space + the 󰀄 glyph + a space +
# the label + a trailing pad space, the glyph/label in the account's 256-color);
# line 2 is its VISIBLE click width so the click handler can bound the hit region.
# Width = pad space + glyph + space + label + trailing pad space. When hover is 1
# the pill gains a background bar (48;5;238, matching the file-row hover) so the
# pointer target reads as pressable; the background+fg SGR open at the very start
# and the reset closes after the trailing pad, so BOTH pad spaces are highlighted —
# a small margin of breathing room on the left and right of the text. The two pad
# spaces are reserved in the plain pill too, so the visible width is identical
# either way and the bottom bar never shifts on hover.
account_pill() {
  local label="$1" color="$2" hover="${3:-0}"
  if [ "$hover" = 1 ]; then
    printf '\033[48;5;238;38;5;%sm \xF3\xB0\x80\x84 %s \033[0m\n' "$color" "$label"
  else
    printf ' \033[38;5;%sm\xF3\xB0\x80\x84 %s\033[0m \n' "$color" "$label"
  fi
  printf '%s\n' "$((4 + ${#label}))"
}

# current_session_account <tmux_cmd> <pointer_file> — print the account dir name
# THIS session's AI pane is running. The active-account POINTER is global (any
# session's switch or the launcher rewrites it), so a mid-session switch stamps
# the pane's own account into the tmux session env (WISP_DECK_CLAUDE_ACCOUNT,
# set at launch by wrapper.sh and updated by relaunch_ai_pane). A stamped empty
# value means the Default login and must NOT fall back; only an UNSTAMPED
# session (pre-stamp launch: tmux prints `-NAME`) falls back to the pointer.
current_session_account() {
  local tmux_cmd="$1" pointer_file="$2" line
  line="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_ACCOUNT 2>/dev/null)" || line=""
  case "$line" in
    WISP_DECK_CLAUDE_ACCOUNT=*)
      printf '%s\n' "${line#WISP_DECK_CLAUDE_ACCOUNT=}"
      return 0
      ;;
  esac
  get_active_claude_account "$pointer_file"
}

# account_current <pointer_file> <list_file> <default_label_file> <colors_file> \
#   [tmux_cmd] — print "<label>\t<color>" for the account to show on the pill.
# With tmux_cmd, that is the account THIS session's pane is running (see
# current_session_account) — the global pointer alone would make the pill
# "switch back" whenever another session or the launcher rewrites it. Without
# tmux_cmd (legacy callers), the pointer is used. The label mirrors the
# statusline (gt_claude_account_label reads the dir off the tail of its first
# arg, so the bare dir name works); the color is the persisted per-account hue.
account_current() {
  local pointer_file="$1" list_file="$2" default_label_file="$3" colors_file="$4" tmux_cmd="${5:-}"
  local dir label color
  if [ -n "$tmux_cmd" ]; then
    dir="$(current_session_account "$tmux_cmd" "$pointer_file")"
  else
    dir="$(get_active_claude_account "$pointer_file")"
  fi
  label="$(gt_claude_account_label "$dir" "$list_file" "$default_label_file")"
  color="$(gt_account_color "$colors_file" "$dir")"
  printf '%s\t%s\n' "$label" "$color"
}

# find_ai_pane <tmux_cmd> — print the pane id of the AI pane. wrapper.sh tags it
# with the per-pane option @gt_ai=1; list every session pane's id + flag and pick
# the tagged one. Empty when none is tagged (e.g. the harness).
find_ai_pane() {
  local tmux_cmd="$1" id flag
  while read -r id flag; do
    [ "$flag" = "1" ] && { printf '%s\n' "$id"; return 0; }
  done < <("$tmux_cmd" list-panes -s -F '#{pane_id} #{@gt_ai}' 2>/dev/null)
  return 0
}

# current_ai_session <tmux_cmd> — print the resume-validated conversation id the
# statusline stamped into the session env (gt_stamp_claude_session sets
# WISP_DECK_CLAUDE_SESSION only once the transcript is durable). tmux prints
# `NAME=value` when set, or `-NAME` (leading dash) when unset — only the former
# yields an id. Empty when unstamped, so the relaunch falls back to `-c`.
current_ai_session() {
  local tmux_cmd="$1" line
  line="$("$tmux_cmd" show-environment WISP_DECK_CLAUDE_SESSION 2>/dev/null)" || return 0
  case "$line" in
    WISP_DECK_CLAUDE_SESSION=*) printf '%s\n' "${line#WISP_DECK_CLAUDE_SESSION=}" ;;
  esac
  return 0
}

# _ai_input_line_empty <tmux_cmd> <pane> — exit 0 when the pane's input line is
# EMPTY. The input box renders below the transcript, so the input line is the
# LAST ❯-prefixed row of the frame (a bare ❯ higher up is transcript content;
# dialog rows like "❯ 2. No, exit" carry text and read as non-empty). Same
# empty-prompt heuristic wait_ai_pane_ready trusts, incl. the U+00A0 pad. A
# frame with no ❯ at all is "can't tell" — exit 1 so the caller stays on the
# slow, fail-open path.
_ai_input_line_empty() {
  local tmux_cmd="$1" pane="$2" nbsp line last=""
  nbsp="$(printf '\302\240')"
  while IFS= read -r line; do
    case "$line" in "❯"*) last="$line" ;; esac
  done < <("$tmux_cmd" capture-pane -p -t "$pane" 2>/dev/null)
  [ -n "$last" ] || return 1
  printf '%s\n' "$last" | grep -qE "^❯[ ${nbsp}]*$"
}

# stash_ai_draft <tmux_cmd> <pane> <history_file> [project_dir] — extract the
# AI pane's unsent draft by making claude itself persist it: Esc Esc with a
# non-empty input appends the draft (full text, newlines, [Image #N]/[Pasted
# text #N] markers) to the shared prompt history. The lone Escape first
# interrupts a streaming turn (no-op when idle). History growth within the
# poll window is the "there WAS a draft" signal — an empty input appends
# nothing. The history is shared by EVERY live claude session, so a foreign
# session's entry can land in the same window: with project_dir given, only
# appended entries whose "project" matches count (never replay another
# session's prompt into this pane). Prints the stashed draft text; exit 0 iff
# stashed. Fail-open: any miss just means the switch behaves as before this
# feature.
stash_ai_draft() {
  local tmux_cmd="$1" pane="$2" hist="$3" project="${4:-}"
  command -v python3 >/dev/null 2>&1 || return 1
  # Fast path: an empty input box has nothing to stash — skip the Esc-Esc
  # dance and its several-second no-growth poll entirely. This is the common
  # case, so it is most of the mid-session switch's latency.
  if _ai_input_line_empty "$tmux_cmd" "$pane"; then
    return 1
  fi
  local before=0 after out
  [ -f "$hist" ] && before="$(wc -l < "$hist")"
  "$tmux_cmd" send-keys -t "$pane" Escape 2>/dev/null || return 1
  sleep 0.2
  "$tmux_cmd" send-keys -t "$pane" Escape Escape 2>/dev/null || return 1
  for _ in $(seq 1 15); do
    after=0
    [ -f "$hist" ] && after="$(wc -l < "$hist")"
    if [ "$after" -gt "$before" ]; then
      out="$(python3 - "$hist" "$before" "$project" <<'PYEOF'
import json, sys
path, skip, project = sys.argv[1], int(sys.argv[2]), sys.argv[3]
with open(path, "rb") as f:
    lines = [l for l in f.read().splitlines() if l.strip()][skip:]
for line in reversed(lines):
    try:
        entry = json.loads(line)
    except ValueError:
        continue
    if project and entry.get("project") != project:
        continue
    print(entry.get("display", ""), end="")
    break
PYEOF
)" || out=""
      if [ -n "$out" ]; then
        printf '%s' "$out"
        return 0
      fi
      # Growth was only foreign sessions' entries — keep waiting for ours.
    fi
    sleep 0.1
  done
  return 1
}

# draft_cache_root <accounts_dir> <session_acct> — config root of the account
# the pane WAS running before the switch: its image-cache/ holds the draft's
# pasted images (written at paste time). Empty acct = the Default (Keychain)
# login, whose root is ~/.claude. The NEW login only needs to READ this path,
# so no cache sharing across accounts is required.
draft_cache_root() {
  local accounts_dir="$1" acct="$2"
  if [ -n "$acct" ]; then
    printf '%s/%s\n' "$accounts_dir" "$acct"
  else
    printf '%s/.claude\n' "$HOME"
  fi
}

# wait_ai_pane_ready <tmux_cmd> <pane> [iters] — poll (iters × 0.5s, default
# ~30s) until the relaunched claude shows an EMPTY ready input line: "❯"
# alone on its line. Trust/login/update dialogs also render "❯"-prefixed rows
# but always with text after it, so this match keeps the replay's pastes away
# from dialogs. Timeout is fail-open: the draft stays one Up-press away in
# prompt history.
wait_ai_pane_ready() {
  local tmux_cmd="$1" pane="$2" iters="${3:-60}" nbsp
  # claude pads the empty prompt with a NO-BREAK space (U+00A0), not an ASCII
  # space — accept both (printf keeps this bash-3.2/zsh safe, no $'\u..').
  nbsp="$(printf '\302\240')"
  for _ in $(seq 1 "$iters"); do
    if "$tmux_cmd" capture-pane -p -t "$pane" 2>/dev/null | grep -qE "^❯[ ${nbsp}]*$"; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

# send_continue_message <tmux_cmd> <pane> — type "continue" into the pane's
# input and submit it. Used by the auto-switch relaunch so the conversation
# picks up in the new login without the user re-prompting. The literal text
# (-l) and the Enter are separate sends with a beat in between so claude
# registers the text before the submit. Best-effort.
send_continue_message() {
  local tmux_cmd="$1" pane="$2"
  "$tmux_cmd" send-keys -t "$pane" -l "continue" 2>/dev/null || return 0
  sleep 0.2
  "$tmux_cmd" send-keys -t "$pane" Enter 2>/dev/null || true
  return 0
}

# _draft_paste <tmux_cmd> <pane> <text> — bracketed-paste literal text into
# the pane via a named tmux buffer. Bracketed (-p) is load-bearing twice
# over: embedded newlines must not submit, and a pasted image PATH is only
# re-attached as a live image chip when it arrives as a paste (the same
# mechanism the screenshot drop uses). Best-effort: a failed paste degrades
# to "draft stays in history".
_draft_paste() {
  local tmux_cmd="$1" pane="$2" text="$3"
  printf '%s' "$text" | "$tmux_cmd" load-buffer -b wispdraft - 2>/dev/null || return 0
  "$tmux_cmd" paste-buffer -p -b wispdraft -t "$pane" 2>/dev/null || true
  return 0
}

# replay_ai_draft <tmux_cmd> <pane> <draft> <cache_root> <sid> — rebuild the
# input field from the stashed draft text. Split at [Image #N] markers; text
# segments paste verbatim, and each marker whose cached PNG exists
# (<cache_root>/image-cache/<sid>/<N>.png, written by the OLD claude at paste
# time) pastes as that absolute path — the new claude re-attaches it as a
# live image. Missing file, unstamped sid, or a malformed marker degrade to
# the literal marker text. [Pasted text #N] markers are never split on: their
# bytes died with the old process (memory-only), so they ride along inside
# text segments. Nothing here ever submits.
replay_ai_draft() {
  local tmux_cmd="$1" pane="$2" draft="$3" cache_root="$4" sid="$5"
  local rest="$draft" pre marker n img
  while [ -n "$rest" ]; do
    pre="${rest%%\[Image #*}"
    if [ "$pre" = "$rest" ]; then
      _draft_paste "$tmux_cmd" "$pane" "$rest"
      break
    fi
    [ -n "$pre" ] && _draft_paste "$tmux_cmd" "$pane" "$pre"
    rest="${rest#"$pre"}"
    if [ "${rest#*\]}" = "$rest" ]; then
      # No closing bracket: not a real marker — paste the remainder literally.
      _draft_paste "$tmux_cmd" "$pane" "$rest"
      break
    fi
    marker="${rest%%\]*}]"
    rest="${rest#*\]}"
    n="${marker#\[Image #}"
    n="${n%\]}"
    img=""
    case "$n" in
      *[!0-9]* | '') ;; # non-numeric: leave img empty -> literal
      *) [ -n "$sid" ] && img="$cache_root/image-cache/$sid/$n.png" ;;
    esac
    if [ -n "$img" ] && [ -f "$img" ]; then
      _draft_paste "$tmux_cmd" "$pane" "$img"
    else
      _draft_paste "$tmux_cmd" "$pane" "$marker"
    fi
    sleep 0.3 # let the image chip render before the next segment lands
  done
  return 0
}

# build_switch_launch_cmd <tool> <claude_cmd> <opencode_cmd> <settings> <filter> \
#   <project_dir> <new_account_dir> [resume_session] — build the launch command that
# respawns the AI pane under new_account_dir.
#
# resume_session is the id of THIS pane's active conversation (the statusline
# stamps it only once the transcript is durable — see current_ai_session). Its
# presence is the "the previous account HAD an active session" signal:
#   - non-empty: reopen THAT exact conversation via build_ai_launch_cmd's resume
#     chain (`--resume <id>` → `-c` → plain claude). The stamped id keeps a
#     multi-tab/window project's pane on ITS own conversation, which bare `-c`
#     could not guarantee.
#   - empty: the pane had NO active session, so do NOT resume — launching `-c`
#     would restore the cwd's most-recent conversation, one that was never this
#     pane's. Build a plain, fresh claude under the new login instead.
# new_account_dir empty = the Default (Keychain) login, so CLAUDE_CONFIG_DIR is
# left unset.
build_switch_launch_cmd() {
  local tool="$1" claude_cmd="$2" opencode_cmd="$3" settings="$4" filter="$5" \
    project_dir="$6" new_account_dir="$7" resume_session="${8:-}"
  if [ -n "$resume_session" ]; then
    WISP_DECK_RESUME=1 \
    WISP_DECK_RESUME_SESSION="$resume_session" \
    WISP_DECK_CLAUDE_ACCOUNT_DIR="$new_account_dir" \
    WISP_DECK_CLAUDE_SETTINGS="$settings" \
    WISP_DECK_CLAUDE_FILTER="$filter" \
      build_ai_launch_cmd "$tool" "$claude_cmd" "$opencode_cmd" "$project_dir"
    return 0
  fi
  # Fresh launch: no resume. claude takes no positional dir (its cwd is set by
  # respawn-pane's -c); opencode does, so only it gets project_dir as the extra.
  local extra=""
  [ "$tool" = "opencode" ] && extra="$project_dir"
  WISP_DECK_CLAUDE_ACCOUNT_DIR="$new_account_dir" \
  WISP_DECK_CLAUDE_SETTINGS="$settings" \
  WISP_DECK_CLAUDE_FILTER="$filter" \
    build_ai_launch_cmd "$tool" "$claude_cmd" "$opencode_cmd" "$extra"
}

# _read_relaunch_ctx <relaunch_file> — load the key=value relaunch context into the
# caller's _rc_* locals (declared by the caller). IFS='=' keeps values verbatim,
# including any trailing space in the screenshot filter prefix.
_read_relaunch_ctx() {
  local file="$1" k v
  while IFS='=' read -r k v; do
    case "$k" in
      tool) _rc_tool="$v" ;;
      claude_cmd) _rc_claude_cmd="$v" ;;
      opencode_cmd) _rc_opencode_cmd="$v" ;;
      settings) _rc_settings="$v" ;;
      filter) _rc_filter="$v" ;;
      project_dir) _rc_project_dir="$v" ;;
      accounts_dir) _rc_accounts_dir="$v" ;;
      pointer) _rc_pointer="$v" ;;
      list) _rc_list="$v" ;;
      colors) _rc_colors="$v" ;;
      default_label) _rc_default_label="$v" ;;
    esac
  done < "$file"
}

# write_relaunch_context <out_file> <tool> <claude_cmd> <opencode_cmd> <settings> \
#   <filter> <project_dir> <cfg_root> — persist everything the mid-session switch
# needs to rebuild the AI launch and locate the account files. wrapper.sh writes it
# once per launch (for an eligible claude session) and passes its path to the pane
# as WISP_DECK_RELAUNCH_FILE. key=value, one per line — read back by
# _read_relaunch_ctx with IFS='=' so a value's spaces (the filter prefix) survive.
write_relaunch_context() {
  local out="$1" tool="$2" claude_cmd="$3" opencode_cmd="$4" settings="$5" \
    filter="$6" project_dir="$7" cfg_root="$8"
  mkdir -p "$(dirname "$out")" 2>/dev/null
  {
    printf 'tool=%s\n' "$tool"
    printf 'claude_cmd=%s\n' "$claude_cmd"
    printf 'opencode_cmd=%s\n' "$opencode_cmd"
    printf 'settings=%s\n' "$settings"
    printf 'filter=%s\n' "$filter"
    printf 'project_dir=%s\n' "$project_dir"
    printf 'accounts_dir=%s\n' "$cfg_root/claude-accounts"
    printf 'pointer=%s\n' "$cfg_root/claude-account"
    printf 'list=%s\n' "$cfg_root/claude-accounts.list"
    printf 'colors=%s\n' "$cfg_root/claude-account-colors"
    printf 'default_label=%s\n' "$cfg_root/claude-account-default-label"
  } > "$out"
}

# relaunch_ai_pane <tmux_cmd> <relaunch_file> [chosen] — respawn the AI pane
# under a new login. With the optional 3rd arg, that is the login the switcher
# popup reported through its result file ("" or "default" = the Default login,
# a dir name = that managed login, resolved only while its dir still exists).
# Without it (legacy callers), the global pointer decides — but the pointer is
# shared mutable state: between the popup's write and this respawn another
# session's switch (or the launcher) can rewrite it, and resolving it here
# would respawn THIS pane under a login the user never picked. Resolves the
# account's isolated config dir, brings its shared conversation state/settings
# up to date (so a login not synced this boot still sees the shared history),
# builds the continue-mode launch, and respawn-panes the tagged AI pane. No-op
# when the AI pane can't be found.
relaunch_ai_pane() {
  local tmux_cmd="$1" relaunch_file="$2"
  local have_chosen=0 chosen=""
  if [ "$#" -ge 3 ]; then
    have_chosen=1
    chosen="$3"
  fi
  local _rc_tool="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_settings="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  local new_dir pane cmd
  if [ "$have_chosen" = 1 ]; then
    new_dir=""
    if [ -n "$chosen" ] && [ "$chosen" != "default" ] && [ -d "$_rc_accounts_dir/$chosen" ]; then
      new_dir="$_rc_accounts_dir/$chosen"
    fi
  else
    new_dir="$(resolve_claude_account_dir "$_rc_accounts_dir" "$_rc_pointer")"
  fi

  # Bring the target login's shared conversation state + settings up to date, so
  # `claude -c` under it resumes the same history (mirrors wrapper.sh's per-account
  # sync at launch). Best-effort — helpers may be absent in a bare test source.
  if [ -n "$new_dir" ]; then
    command -v sync_claude_shared_state >/dev/null 2>&1 \
      && sync_claude_shared_state "$HOME/.claude" "$new_dir"
    command -v sync_claude_shared_settings >/dev/null 2>&1 \
      && sync_claude_shared_settings "$HOME/.claude" "$new_dir"
  fi

  pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$pane" ] || return 0

  # Resume the exact conversation this pane was on (the statusline stamped its id),
  # so the switch carries over THIS session rather than the cwd's most-recent one.
  # An empty sid means the previous account had no active session — the switch
  # then launches a fresh claude rather than resuming (see build_switch_launch_cmd).
  local sid
  sid="$(current_ai_session "$tmux_cmd")"
  cmd="$(build_switch_launch_cmd "$_rc_tool" "$_rc_claude_cmd" "$_rc_opencode_cmd" \
    "$_rc_settings" "$_rc_filter" "$_rc_project_dir" "$new_dir" "$sid")"

  "$tmux_cmd" respawn-pane -k -t "$pane" -c "$_rc_project_dir" "$cmd; exec bash"

  # Stamp what this session's pane NOW runs into the tmux session env, so the
  # pill and the next switch decision read this pane's actual account rather
  # than the global pointer. Default stamps an EMPTY value (set, not unset) —
  # an unset var means "pre-stamp session" and falls back to the pointer.
  "$tmux_cmd" set-environment WISP_DECK_CLAUDE_ACCOUNT "${new_dir##*/}" 2>/dev/null
  return 0
}

# _relaunch_preserving_draft <tmux_cmd> <relaunch_file> <session_acct> [chosen]
# — the "switch is happening" path shared by the result-file and legacy flows:
# stash the pane's unsent draft (claude only — opencode has no Esc-Esc
# stash), relaunch under the new login (the explicit popup choice when given,
# else the pointer — see relaunch_ai_pane), then hand the stashed text to a
# DISOWNED background waiter that replays it once the new claude shows its
# empty prompt. Reads _rc_tool/_rc_accounts_dir from the caller's scope (the
# same dynamic-scoping contract _read_relaunch_ctx uses). When the caller
# scope sets _gt_send_continue=1 (the auto-switch path), the waiter also
# submits a "continue" message the moment the new claude is ready — BEFORE
# the draft replay, so the continued turn streams while the unsent draft
# lands back in the input box. Every step is fail-open: a missed stash or a
# never-ready pane leaves the switch exactly as it behaved before this
# feature (worst case the draft sits in prompt history, one Up away).
_relaunch_preserving_draft() {
  local tmux_cmd="$1" relaunch_file="$2" session_acct="$3"
  local draft="" sid="" pane send_continue="${_gt_send_continue:-}"
  if [ "$_rc_tool" = "claude" ]; then
    sid="$(current_ai_session "$tmux_cmd")"
    pane="$(find_ai_pane "$tmux_cmd")"
    if [ -n "$pane" ]; then
      draft="$(stash_ai_draft "$tmux_cmd" "$pane" \
        "${WISP_DECK_HISTORY_FILE:-$HOME/.claude/history.jsonl}" \
        "$_rc_project_dir")" || draft=""
    fi
  fi
  if [ "$#" -ge 4 ]; then
    relaunch_ai_pane "$tmux_cmd" "$relaunch_file" "$4"
  else
    relaunch_ai_pane "$tmux_cmd" "$relaunch_file"
  fi
  [ -n "$draft" ] || [ -n "$send_continue" ] || return 0
  local cache_root new_pane
  cache_root="$(draft_cache_root "$_rc_accounts_dir" "$session_acct")"
  new_pane="$(find_ai_pane "$tmux_cmd")"
  [ -n "$new_pane" ] || return 0
  (
    if wait_ai_pane_ready "$tmux_cmd" "$new_pane"; then
      [ -n "$send_continue" ] && send_continue_message "$tmux_cmd" "$new_pane"
      [ -n "$draft" ] && replay_ai_draft "$tmux_cmd" "$new_pane" "$draft" "$cache_root" "$sid"
    fi
  ) >/dev/null 2>&1 &
  disown 2>/dev/null || true
  return 0
}

# auto_switch_relaunch <tmux_cmd> <relaunch_file> <target> — the automatic
# quota-rotation entry point (fired by auto_switch_maybe_trigger via `tmux
# run-shell -b`). Reuses the exact mid-session switch the ledger pill drives —
# stash the unsent draft, respawn the pane under the target login resuming the
# same conversation — and additionally auto-submits "continue" once the new
# claude is ready, so the interrupted turn picks up without the user typing.
# No-op when the pane already runs the target (a stale trigger racing a manual
# switch).
auto_switch_relaunch() {
  local tmux_cmd="$1" relaunch_file="$2" target="$3"
  local _rc_tool="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_settings="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"
  local session_acct want
  session_acct="$(current_session_account "$tmux_cmd" "$_rc_pointer")"
  # The rotation names the Default login "default"; the session env stamps it
  # as the empty string — normalize before comparing so a stale trigger can't
  # bounce a pane already sitting on the Default.
  want="$target"
  [ "$want" = "default" ] && want=""
  [ "$want" = "$session_acct" ] && return 0
  local _gt_send_continue=1
  _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct" "$target"
  return 0
}

# open_account_switcher <tmux_cmd> <relaunch_file> — the click handler entry point.
# Float the account switcher popup (which writes the global pointer on select),
# then relaunch the AI pane only if the choice differs from the account THIS
# pane is running. The popup reports the choice through a result file (its
# stdout is swallowed by display-popup); no file = cancelled. Comparing against
# the SESSION's account — not the pointer before/after — is what makes the
# switch stick: the pointer is global, so another session's switch (or the
# launcher) can already have flipped it to the login the user now picks here,
# and a pointer-diff would silently skip the relaunch, leaving this pane on the
# old login (the account appeared to "switch back").
open_account_switcher() {
  local tmux_cmd="$1" relaunch_file="$2"
  local _rc_tool="" _rc_claude_cmd="" _rc_opencode_cmd="" _rc_settings="" \
    _rc_filter="" _rc_project_dir="" _rc_accounts_dir="" _rc_pointer="" \
    _rc_list="" _rc_colors="" _rc_default_label=""
  [ -f "$relaunch_file" ] || return 0
  _read_relaunch_ctx "$relaunch_file"

  # The account this pane runs (stamped in the tmux session env), and the file
  # the popup reports the user's choice through. The result path is minted but
  # NOT created — its existence after the popup is the "user picked something"
  # signal. An older binary rejects the new flags (see
  # switcher_supports_session_flags); it gets the legacy pointer-diff flow.
  local session_acct result_file session_flags=""
  session_acct="$(current_session_account "$tmux_cmd" "$_rc_pointer")"
  result_file=""
  if switcher_supports_session_flags; then
    result_file=$(mktemp "${TMPDIR:-/tmp}/gtswitchsel.XXXXXX" 2>/dev/null) || result_file=""
    [ -n "$result_file" ] && rm -f "$result_file"
    [ -n "$result_file" ] && session_flags="--active $(printf '%q' "$session_acct") \
--result-file $(printf '%q' "$result_file") "
  fi

  # Snapshot the screen behind the popup so the switcher can show it DIMMED in the
  # margin around the card. tmux freezes the panes under a popup, so this snapshot
  # (taken just before opening it) matches what's behind. Serialized as a "W H"
  # header then one "PANE <left> <top>" + captured-lines + "ENDPANE" block per
  # pane; ParseBackdrop composites it. Best-effort: any failure yields a blank
  # margin. This mirrors the diff pager's backdrop (lib/compact-view.sh).
  local backdrop backdrop_arg=""
  backdrop=$(mktemp "${TMPDIR:-/tmp}/gtswitch.XXXXXX" 2>/dev/null) || backdrop=""
  if [ -n "$backdrop" ]; then
    {
      "$tmux_cmd" display-message -p -t "${TMUX_PANE:-}" '#{client_width} #{client_height}'
      "$tmux_cmd" list-panes -t "${TMUX_PANE:-}" -F '#{pane_id} #{pane_left} #{pane_top}' 2>/dev/null |
        while read -r pid pleft ptop; do
          printf 'PANE %s %s\n' "$pleft" "$ptop"
          "$tmux_cmd" capture-pane -p -t "$pid" 2>/dev/null
          printf 'ENDPANE\n'
        done
    } >"$backdrop" 2>/dev/null
    backdrop_arg="--backdrop-file $(printf '%q' "$backdrop")"
  fi

  # Full-screen (-w/-h 100%) and borderless (-B) so the switcher owns the whole
  # window: it draws its own rounded card over the dimmed snapshot and closes when
  # a click lands outside the card (tmux ignores clicks outside a smaller popup).
  local before
  before="$(get_active_claude_account "$_rc_pointer")"
  "$tmux_cmd" display-popup -E -B -w 100% -h 100% \
    "wisp-deck-tui claude-account-switch --list $(printf '%q' "$_rc_list") \
--accounts-dir $(printf '%q' "$_rc_accounts_dir") \
--pointer $(printf '%q' "$_rc_pointer") \
--colors $(printf '%q' "$_rc_colors") \
--default-label $(printf '%q' "$_rc_default_label") \
${session_flags}${backdrop_arg}" 2>/dev/null || true
  [ -n "$backdrop" ] && rm -f "$backdrop"

  # No result file = the popup was cancelled (or never ran) — a clean no-op.
  # Otherwise relaunch iff the chosen login differs from what this pane runs.
  local chosen after
  if [ -n "$result_file" ]; then
    if [ -f "$result_file" ]; then
      IFS= read -r chosen < "$result_file" || chosen=""
      rm -f "$result_file"
      # Hand the CHOICE itself to the relaunch: re-resolving the global
      # pointer there would race another session's concurrent switch.
      [ "$chosen" != "$session_acct" ] && _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct" "$chosen"
    fi
  else
    # Legacy binary (no result-file contract): fall back to the pointer-diff
    # decision — it can't fix a pointer that already matches the choice, but it
    # keeps the switcher working until the binary is updated.
    after="$(get_active_claude_account "$_rc_pointer")"
    [ "$before" != "$after" ] && _relaunch_preserving_draft "$tmux_cmd" "$relaunch_file" "$session_acct"
  fi
  return 0
}
