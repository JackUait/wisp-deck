#!/bin/bash
# Tmux session helpers — build launch command, cleanup.
# Depends on: process.sh (kill_tree)

# Get the single accent (focus) colour for a tool's tmux chrome — the active pane
# border and the active spare-tab chip. Mirrors the Go theme's Primary: purple
# for OpenCode, orange for claude (the default). Prints a 256-colour number.
get_tool_accent() {
  case "${1:-}" in
    opencode) echo "141" ;;   # #af87ff brand purple
    codex)    echo "36" ;;    # #00af87 brand teal
    *)        echo "209" ;;   # orange (claude default)
  esac
}

# Print an argv-safe OpenCode prefix for the exact command forms emitted by
# resolve_opencode_cmd. No caller-controlled shell prefixes are accepted.
opencode_adapter_prefix() {
  local tool_cmd="$1" executable=""
  case "$tool_cmd" in
    opencode)
      executable="$(command -v opencode 2>/dev/null)" || return 1
      [ -n "$executable" ] || return 1
      printf '%q' "$executable"
      ;;
    /*)
      printf '%q' "$tool_cmd"
      ;;
    'npx --no-install opencode-ai')
      executable="$(command -v npx 2>/dev/null)" || return 1
      [ -n "$executable" ] || return 1
      printf '%q --no-install opencode-ai' "$executable"
      ;;
    'npx --prefer-offline opencode-ai@latest')
      executable="$(command -v npx 2>/dev/null)" || return 1
      [ -n "$executable" ] || return 1
      printf '%q --prefer-offline opencode-ai@latest' "$executable"
      ;;
    *)
      return 1
      ;;
  esac
}

# Build the AI tool launch command string. Claude's complete raw fallback chain
# is supervised once when the generation runtime is present; the screenshot
# filter remains inside that chain as its sole PTY boundary.
# Usage: build_ai_launch_cmd <tool> <tool_cmd> [extra_args_or_project_dir]
build_ai_launch_cmd() {
  local tool="$1" tool_cmd="$2" raw config_root state_q generation_q config_q raw_q

  # OpenCode's strict adapter owns both the authenticated server and the TUI
  # attach. Admit only the exact command forms emitted by resolve_opencode_cmd;
  # arbitrary shell prefixes would otherwise execute outside the adapter's
  # environment and terminal-notification policy.
  if [ "$tool" = "opencode" ] \
     && [ -n "${WISP_DECK_ATTENTION_FILE:-}" ] \
     && [ -n "${WISP_DECK_ATTENTION_GENERATION:-}" ]; then
    local prefix_q="" resume_flag="" prompt_flag=""
    prefix_q="$(opencode_adapter_prefix "$tool_cmd")" || return 1
    printf -v state_q '%q' "$WISP_DECK_ATTENTION_FILE"
    printf -v generation_q '%q' "$WISP_DECK_ATTENTION_GENERATION"
    [ "${WISP_DECK_RESUME:-0}" = "1" ] && resume_flag=" --continue"
    if [ -n "${WISP_DECK_OPENCODE_HANDOFF_PROMPT:-}" ]; then
      printf -v prompt_flag ' --prompt %q' "$WISP_DECK_OPENCODE_HANDOFF_PROMPT"
    fi
    printf 'wisp-deck-tui opencode-adapter --state-file %s --generation %s%s%s -- %s\n' \
      "$state_q" "$generation_q" "$resume_flag" "$prompt_flag" "$prefix_q"
    return 0
  fi

  # Codex's semantic adapter owns the entire remote/embedded resume fallback
  # matrix. Build it as argv-safe shell text before constructing the legacy
  # raw chain so hostile handoff prompts remain one argument and no shell
  # fallback can bypass the generation writer.
  if [ "$tool" = "codex" ] \
     && [ -n "${WISP_DECK_ATTENTION_FILE:-}" ] \
     && [ -n "${WISP_DECK_ATTENTION_GENERATION:-}" ]; then
    local codex_q fallback fallback_q resume_q="" prompt_q="" extra=""
    [ "$#" -ge 3 ] && extra="$3"
    fallback="${WISP_DECK_RESUME_FALLBACK_WINDOW:-10}"
    case "$fallback" in
      *[!0-9]*) ;;
      *) fallback="${fallback}s" ;;
    esac
    printf -v codex_q '%q' "$tool_cmd"
    printf -v state_q '%q' "$WISP_DECK_ATTENTION_FILE"
    printf -v generation_q '%q' "$WISP_DECK_ATTENTION_GENERATION"
    printf -v fallback_q '%q' "$fallback"
    if [ "${WISP_DECK_RESUME:-0}" = "1" ] \
       && [ -n "${WISP_DECK_RESUME_SESSION:-}" ]; then
      printf -v resume_q ' --resume-session %q' "$WISP_DECK_RESUME_SESSION"
      # build_switch_launch_cmd passes project_dir in the legacy extra slot
      # for resumes. It is cwd metadata, not an initial Codex prompt.
      extra=""
    fi
    [ -n "$extra" ] && printf -v prompt_q ' %q' "$extra"
    printf 'wisp-deck-tui codex-adapter --codex %s --state-file %s --generation %s%s --fallback-window %s --%s\n' \
      "$codex_q" "$state_q" "$generation_q" "$resume_q" "$fallback_q" "$prompt_q"
    return 0
  fi

  raw="$(build_ai_launch_cmd_raw "$@")" || return 1
  # The OpenAI subscription uses Claude only as the interactive UI and tool
  # executor. One adapter owns the complete raw resume/screenshot/settings
  # chain and serves it a private Anthropic-compatible loopback API backed by
  # the user's Codex-managed ChatGPT login.
  if [ "$tool" = "claude" ] \
     && [ "${WISP_DECK_CLAUDE_PROVIDER:-}" = "openai-chatgpt" ]; then
    local codex_cmd="${WISP_DECK_CODEX_CMD:-}" codex_q
    case "$codex_cmd" in
      /*) ;;
      *)
        printf 'Error: Codex is required for OpenAI GPT. Install Codex and run codex login.\n' >&2
        return 1
        ;;
    esac
    printf -v codex_q '%q' "$codex_cmd"
    printf -v raw_q '%q' "$raw"
    raw="wisp-deck-tui claude-gpt-adapter --codex ${codex_q} -- bash -c ${raw_q}"
  fi
  if [ "$tool" = "claude" ] \
     && [ -n "${WISP_DECK_ATTENTION_FILE:-}" ] \
     && [ -n "${WISP_DECK_ATTENTION_GENERATION:-}" ]; then
    config_root="${WISP_DECK_CLAUDE_ACCOUNT_DIR:-$HOME/.claude}"
    printf -v state_q '%q' "$WISP_DECK_ATTENTION_FILE"
    printf -v generation_q '%q' "$WISP_DECK_ATTENTION_GENERATION"
    printf -v config_q '%q' "$config_root"
    printf -v raw_q '%q' "$raw"
    printf 'wisp-deck-tui claude-attention --state-file %s --generation %s --config-dir %s -- bash -c %s\n' \
      "$state_q" "$generation_q" "$config_q" "$raw_q"
    return 0
  fi
  printf '%s\n' "$raw"
}

# Construct the tool command without semantic supervision. Keeping the entire
# resume chain in this function ensures one outer adapter owns every fallback.
# Usage: build_ai_launch_cmd_raw <tool> <tool_cmd> [extra_args_or_project_dir]
build_ai_launch_cmd_raw() {
  local tool="$1" tool_cmd="$2"
  shift 2
  local extra="$*"

  # codex takes no flags, no env plumbing and no positional dir (the pane's cwd
  # is already the project dir), so it short-circuits ahead of the claude-only
  # prefixes below. In resume mode a captured session id resumes THAT exact
  # session via a guarded `codex resume <id>` → plain fallback (same startup
  # window contract as claude's chain below); without an id it relaunches
  # fresh — `resume --last` is cwd-filtered but could steal another pane's
  # session.
  if [ "$tool" = "codex" ]; then
    if [ "${WISP_DECK_RESUME:-0}" = "1" ] && [ -n "${WISP_DECK_RESUME_SESSION:-}" ]; then
      local cwin="${WISP_DECK_RESUME_FALLBACK_WINDOW:-10}"
      echo "_wd_t0=\$(date +%s); $tool_cmd resume ${WISP_DECK_RESUME_SESSION}; _wd_rc=\$?; if [ \$_wd_rc -ne 0 ] && [ \$(( \$(date +%s) - _wd_t0 )) -lt $cwin ]; then _wd_t0=\$(date +%s); $tool_cmd; _wd_rc=\$?; fi"
      return 0
    fi
    echo "$tool_cmd"
    return 0
  fi

  # Claude-only: append --settings when a config is active.
  local claude_settings=""
  if [ -n "${WISP_DECK_CLAUDE_SETTINGS:-}" ]; then
    claude_settings=" --settings \"${WISP_DECK_CLAUDE_SETTINGS}\""
  fi

  # Claude-only: when a non-Default native account is active, wrapper.sh exports
  # WISP_DECK_CLAUDE_ACCOUNT_DIR (the account's isolated CLAUDE_CONFIG_DIR). It is
  # prefixed onto the launch as an env assignment so `claude` (and any wrapper it
  # runs behind, which inherits the env) uses that account's login. Default
  # leaves it unset, so Claude falls back to the standard Keychain login.
  local claude_account=""
  if [ -n "${WISP_DECK_CLAUDE_ACCOUNT_DIR:-}" ]; then
    claude_account="CLAUDE_CONFIG_DIR=\"${WISP_DECK_CLAUDE_ACCOUNT_DIR}\" "
  else
    # Default must MEAN the Keychain login: actively shed any CLAUDE_CONFIG_DIR
    # the launching environment carries (a tmux server started from a shell
    # inside another claude session inherits one), or "Default" would silently
    # run that managed login. `env` also accepts the VAR=... assignments the
    # proxy branch below may append.
    claude_account="env -u CLAUDE_CONFIG_DIR "
  fi

  # Claude-only: when the account-rotation proxy is active, wrapper.sh exports the
  # local proxy port (+ key, + CA path in MITM mode). The proxy injects the
  # currently-active pooled account's token and switches accounts as quota is
  # exhausted; claude keeps its own single config dir, so the conversation is
  # continuous across switches. Two routing modes mirror teamclaude:
  #   - MITM (default, CA present): route via HTTPS_PROXY + NODE_EXTRA_CA_CERTS so
  #     even hardcoded api.anthropic.com endpoints get the injected token. Claude
  #     keeps its own token; the proxy accepts it from localhost and rewrites it.
  #   - base-URL (--mitm=false, no CA): ANTHROPIC_BASE_URL + ANTHROPIC_API_KEY.
  if [ -n "${WISP_DECK_PROXY_PORT:-}" ] && [ -n "${WISP_DECK_PROXY_KEY:-}" ]; then
    if [ -n "${WISP_DECK_PROXY_CA:-}" ]; then
      # Embed the key in the proxy URL so claude sends Proxy-Authorization on
      # CONNECT — the proxy authenticates the tunnel rather than trusting
      # loopback (which is not a trust boundary on multi-user hosts).
      local _proxy_url="http://wisp-deck:${WISP_DECK_PROXY_KEY}@127.0.0.1:${WISP_DECK_PROXY_PORT}"
      claude_account="${claude_account}HTTPS_PROXY=\"${_proxy_url}\" HTTP_PROXY=\"${_proxy_url}\" https_proxy=\"${_proxy_url}\" http_proxy=\"${_proxy_url}\" NO_PROXY=\"\" no_proxy=\"\" NODE_EXTRA_CA_CERTS=\"${WISP_DECK_PROXY_CA}\" "
    else
      claude_account="${claude_account}ANTHROPIC_BASE_URL=\"http://127.0.0.1:${WISP_DECK_PROXY_PORT}\" ANTHROPIC_API_KEY=\"${WISP_DECK_PROXY_KEY}\" "
    fi
    # Pass the proxy's active-account state file into claude's env so the status
    # line (a child of claude) can show the pooled account rotation landed on.
    if [ -n "${WISP_DECK_PROXY_ACCOUNT_FILE:-}" ]; then
      claude_account="${claude_account}WISP_DECK_PROXY_ACCOUNT_FILE=\"${WISP_DECK_PROXY_ACCOUNT_FILE}\" "
    fi
  fi

  # Claude-only: a launch prefix that runs Claude behind the screenshot-drag
  # filter. wrapper.sh sets WISP_DECK_CLAUDE_FILTER (to e.g.
  # "wisp-deck-tui screenshot-filter -- ") only after confirming the TUI binary
  # supports it. When a dropped screenshot delivers a screencaptureui temp path,
  # the filter copies the file to a stable location and rewrites the path before
  # Claude reads it (macOS deletes the temp file moments after the drop).
  local claude_filter="${WISP_DECK_CLAUDE_FILTER:-}"

  # Resume mode: reopen this tab's own conversation when its id was captured
  # (WISP_DECK_RESUME_SESSION, stamped by the statusline before the reboot);
  # otherwise fall back to the most recent cwd-scoped conversation. The
  # specific id matters when several tabs of one project are restored — `-c`
  # would open the same conversation in all of them.
  #
  # Each claude step is guarded: `claude --resume`/-c can fail AT STARTUP
  # ("No conversation found" — e.g. a resume bug, a transcript deleted since
  # validation, or a project with no conversations at all), which would dump
  # the restored tab to a bare shell. A step that exits non-zero within the
  # fallback window chains to the next-safest launch: --resume <id> → -c →
  # plain claude. A non-zero exit AFTER the window is a crash or user action —
  # never relaunch then.
  if [ "${WISP_DECK_RESUME:-0}" = "1" ]; then
    if [ "$tool" = "opencode" ]; then
      echo "$tool_cmd --continue"
      return 0
    fi
    local win="${WISP_DECK_RESUME_FALLBACK_WINDOW:-10}"
    local base="${claude_account}${claude_filter}$tool_cmd"
    local steps=("-c" "")
    if [ -n "${WISP_DECK_RESUME_SESSION:-}" ]; then
      steps=("--resume ${WISP_DECK_RESUME_SESSION}" "-c" "")
    fi
    local chain="" step launch
    for step in "${steps[@]}"; do
      launch="${base}${step:+ $step}${claude_settings}"
      if [ -z "$chain" ]; then
        chain="_wd_t0=\$(date +%s); $launch; _wd_rc=\$?"
      else
        chain="$chain; if [ \$_wd_rc -ne 0 ] && [ \$(( \$(date +%s) - _wd_t0 )) -lt $win ]; then _wd_t0=\$(date +%s); $launch; _wd_rc=\$?; fi"
      fi
    done
    # Leave the complete chain's status equal to Claude's final status. The
    # subshell preserves the caller's later `; exec bash` while allowing the
    # outer attention supervisor's `bash -c` to observe the real exit code.
    chain="$chain; (exit \"\$_wd_rc\")"
    echo "$chain"
    return 0
  fi

  case "$tool" in
    opencode)
      echo "$tool_cmd \"$extra\""
      ;;
    *)
      if [ -n "$extra" ]; then
        echo "${claude_account}${claude_filter}$tool_cmd $extra${claude_settings}"
      else
        echo "${claude_account}${claude_filter}$tool_cmd${claude_settings}"
      fi
      ;;
  esac
}

# Layout self-heal. tmux executes every command of the wrapper's launch chain
# even when one fails, so a failed split-window (observed cause: the session
# was sized from a pre-resize tiny pty; a future cause could be any tmux error)
# left the tab attached to a lone full-width ledger pane with nothing to fix
# it. This watcher guards the CLASS: backgrounded by the wrapper around the
# launch, it waits for the window to have real space, rebuilds whatever panes
# are missing (marking the AI pane with @gt_ai exactly like the chain does),
# and exits as soon as the three-pane layout exists. A healthy launch costs it
# one tmux round-trip.
# Usage: gt_ensure_panes_watch <tmux_cmd> <session> <project_dir> <ai_cmd>
#        <spare_cmd> [interval] [max_ticks]
gt_ensure_panes_watch() {
  local tmux_cmd="$1" sess="$2" project_dir="$3" ai_cmd="$4" spare_cmd="$5"
  local interval="${6:-0.25}" max_ticks="${7:-60}"
  local i=0 out panes width height seen=0 healed=0
  local ledger new_pane ai_pane first non_ai p mark
  while [ "$i" -lt "$max_ticks" ]; do
    i=$((i + 1))
    # Empty output is treated like a failed query (a real tmux either errors
    # or prints): not created yet → wait; gone after we saw it → session ended.
    if ! out="$("$tmux_cmd" display-message -p -t "$sess:0" \
      '#{window_panes} #{window_width} #{window_height}' 2>/dev/null)" \
      || [ -z "$out" ]; then
      [ "$seen" -eq 1 ] && return 0
      sleep "$interval"
      continue
    fi
    seen=1
    read -r panes width height <<< "$out"
    case "$panes" in '' | *[!0-9]*) panes=0 ;; esac
    if [ "$panes" -ge 3 ]; then
      [ "$healed" -eq 1 ] && echo "gt_ensure_panes_watch: rebuilt missing panes for $sess" >&2
      return 0
    fi
    # A too-small window is why the splits failed in the first place —
    # retrying now would fail again. Wait for real space.
    if [ "${width:-0}" -lt 40 ] || [ "${height:-0}" -lt 10 ]; then
      sleep "$interval"
      continue
    fi
    if [ "$panes" -eq 1 ]; then
      # The stuck-tab state: only the ledger exists. Rebuild both splits in
      # the chain's order and leave the AI pane focused, as the chain does.
      ledger="$("$tmux_cmd" display-message -p -t "$sess:0" '#{pane_id}' 2>/dev/null)"
      if "$tmux_cmd" split-window -h -p 75 -c "$project_dir" -t "$sess:0" \
        "$ai_cmd; exec bash" 2>/dev/null; then
        new_pane="$("$tmux_cmd" display-message -p -t "$sess:0" '#{pane_id}' 2>/dev/null)"
        "$tmux_cmd" set-option -p -t "$new_pane" @gt_ai 1 2>/dev/null
        "$tmux_cmd" split-window -v -p 45 -c "$project_dir" -t "$ledger" \
          "$spare_cmd" 2>/dev/null
        "$tmux_cmd" select-pane -t "$new_pane" 2>/dev/null
        healed=1
      fi
    elif [ "$panes" -eq 2 ]; then
      # One split failed. The @gt_ai marker says which: no marked pane → the
      # AI split is missing; a marked pane → the spare split is missing.
      ai_pane=""
      first=""
      non_ai=""
      while read -r p mark; do
        [ -n "$p" ] || continue
        [ -z "$first" ] && first="$p"
        if [ "$mark" = "1" ]; then ai_pane="$p"; else non_ai="$p"; fi
      done <<< "$("$tmux_cmd" list-panes -t "$sess:0" -F '#{pane_id} #{@gt_ai}' 2>/dev/null)"
      if [ -z "$ai_pane" ]; then
        if "$tmux_cmd" split-window -h -p 75 -c "$project_dir" -t "$first" \
          "$ai_cmd; exec bash" 2>/dev/null; then
          new_pane="$("$tmux_cmd" display-message -p -t "$sess:0" '#{pane_id}' 2>/dev/null)"
          "$tmux_cmd" set-option -p -t "$new_pane" @gt_ai 1 2>/dev/null
          "$tmux_cmd" select-pane -t "$new_pane" 2>/dev/null
          healed=1
        fi
      else
        if "$tmux_cmd" split-window -v -p 45 -c "$project_dir" -t "$non_ai" \
          "$spare_cmd" 2>/dev/null; then
          "$tmux_cmd" select-pane -t "$ai_pane" 2>/dev/null
          healed=1
        fi
      fi
    fi
    sleep "$interval"
  done
  return 0
}

# Clean up a tmux session: kill watcher, TERM pane trees, KILL survivors, destroy session.
cleanup_tmux_session() {
  local session_name="$1" watcher_pid="$2" tmux_cmd="$3"

  kill "$watcher_pid" 2>/dev/null || true

  # Private key tables live at tmux-server scope, so remove this one before a
  # final pane exit can implicitly destroy the session and its table metadata.
  if command -v ledger_hover_uninstall >/dev/null 2>&1; then
    ledger_hover_uninstall "$tmux_cmd" "$session_name"
  fi

  local pane_pid
  for pane_pid in $("$tmux_cmd" list-panes -s -t "$session_name" -F '#{pane_pid}' 2>/dev/null); do
    kill_tree "$pane_pid" TERM
  done

  sleep 0.3
  for pane_pid in $("$tmux_cmd" list-panes -s -t "$session_name" -F '#{pane_pid}' 2>/dev/null); do
    kill_tree "$pane_pid" KILL
  done

  "$tmux_cmd" kill-session -t "$session_name" 2>/dev/null || true

  # The spare pane's nested tmux is a detached server that reparents away from
  # the pane tree, so the kills above don't reap it. Tear it down explicitly
  # when lib/spare-tabs.sh is loaded.
  if command -v spare_tabs_cleanup >/dev/null 2>&1; then
    spare_tabs_cleanup "$(spare_tabs_socket "$session_name")"
  fi
}
