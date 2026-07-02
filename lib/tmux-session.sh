#!/bin/bash
# Tmux session helpers — build launch command, cleanup.
# Depends on: process.sh (kill_tree)

# Get the single accent (focus) colour for a tool's tmux chrome — the active pane
# border and the active spare-tab chip. Mirrors the Go theme's Primary: purple
# for OpenCode, orange for claude (the default). Prints a 256-colour number.
get_tool_accent() {
  case "${1:-}" in
    opencode) echo "141" ;;   # #af87ff brand purple
    *)        echo "209" ;;   # orange (claude default)
  esac
}

# Build the AI tool launch command string.
# Usage: build_ai_launch_cmd <tool> <claude_cmd> <opencode_cmd> [extra_args_or_project_dir]
build_ai_launch_cmd() {
  local tool="$1" claude_cmd="$2" opencode_cmd="$3"
  shift 3
  local extra="$*"

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
      echo "$opencode_cmd --continue"
      return 0
    fi
    local win="${WISP_DECK_RESUME_FALLBACK_WINDOW:-10}"
    local base="${claude_account}${claude_filter}$claude_cmd"
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
    echo "$chain"
    return 0
  fi

  case "$tool" in
    opencode)
      echo "$opencode_cmd \"$extra\""
      ;;
    *)
      if [ -n "$extra" ]; then
        echo "${claude_account}${claude_filter}$claude_cmd $extra${claude_settings}"
      else
        echo "${claude_account}${claude_filter}$claude_cmd${claude_settings}"
      fi
      ;;
  esac
}

# Clean up a tmux session: kill watcher, TERM pane trees, KILL survivors, destroy session.
cleanup_tmux_session() {
  local session_name="$1" watcher_pid="$2" tmux_cmd="$3"

  kill "$watcher_pid" 2>/dev/null || true

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
