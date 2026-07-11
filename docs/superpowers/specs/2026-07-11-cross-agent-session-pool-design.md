# Cross-agent session preservation + shared session pool

**Date:** 2026-07-11
**Status:** Approved (autonomous goal session — design decisions recorded for review)

## Goal

1. **Preserve each agent's session across mid-session agent switches**, the same
   way account switches already preserve Claude's conversation: switching
   claude → codex → claude resumes Claude's exact conversation (already works),
   and switching codex → claude → codex must resume the *codex* conversation
   too (today codex relaunches fresh, losing the session). Same best-effort for
   OpenCode.
2. **Shared pool of sessions**: the conversation belongs to the *pane*, not to
   the agent it started in. A per-session pool records every agent's native
   session id plus an agent-neutral transcript export (handoff), so the user
   can continue the conversation in any agent — an agent that has no native
   session of its own is seeded with the handoff.

## Background

- The mid-session switcher (`lib/account-switch.sh`) already relaunches the AI
  pane across agents (`relaunch_switch_tool`) and across Claude logins,
  preserving Claude's conversation via the statusline-stamped
  `WISP_DECK_CLAUDE_SESSION` and `build_ai_launch_cmd`'s guarded
  `--resume <id> → -c → plain` chain.
- Codex (0.144.x) supports `codex resume <SESSION_ID>` (UUID) and stores
  rollouts under `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`;
  the first line (`session_meta`) carries the session's `cwd`. Nothing stamps
  a codex session id today, and `build_ai_launch_cmd`'s resume mode returns a
  plain `codex` launch.
- OpenCode resumes the project's most recent session with `--continue` (no
  verifiable per-id resume; the binary is not installed on this machine).
- Both `claude` and `codex` accept a positional initial prompt on launch —
  the injection vector for cross-agent handoff.

## Design

### 1. Per-agent session identity

Live identity lives in the tmux session env, mirroring
`WISP_DECK_CLAUDE_SESSION`:

- `WISP_DECK_CODEX_SESSION` — stamped by `relaunch_switch_tool` when the pane
  *leaves* codex, from `codex_current_session` (below).
- `WISP_DECK_OPENCODE_ACTIVE=1` — stamped when the pane leaves opencode; a
  later switch back launches `opencode --continue` (project-scoped
  most-recent; best-effort, same fidelity `-c` gives Claude).
- `WISP_DECK_CODEX_STARTED_AT=<epoch>` — stamped when the pane switches *to*
  codex, bounding the later capture to this stint. A wrapper-launched codex
  pane has no stamp; capture then falls back to the tmux session's
  `#{session_created}`.

`codex_current_session <sessions_root> <project_dir> <since_epoch>` (new
`lib/session-pool.sh`) prints the UUID of the newest rollout whose
`session_meta` line records `"cwd":"<project_dir>"` and whose mtime ≥
since_epoch; empty otherwise. Root defaults to `~/.codex/sessions`
(overridable for tests via the first arg).

### 2. Codex native resume (`build_ai_launch_cmd`)

In resume mode (`WISP_DECK_RESUME=1`) the codex arm gains the same guarded
fallback chain Claude uses: with `WISP_DECK_RESUME_SESSION` set,
`codex resume <id>` falling back (non-zero exit within the fallback window) to
plain `codex`; without an id it stays a plain launch (codex's `resume --last`
is cwd-filtered but could steal another pane's session — not used).

### 3. Shared session pool (`lib/session-pool.sh`, new)

Pool dir: `<cfg_root>/session-pool/<session-key>/` where session-key is the
relaunch file's `relaunch-` suffix (wrapper names it
`relaunch-dev-<project>-$$`, already per-session unique). Contents:

- `meta` — `key=value` lines: one `<tool>=<sid>` per agent that has a native
  session in this pane (`opencode=1` as a flag), plus `last_tool=` and
  `updated=<epoch>`. Helpers `pool_set`/`pool_get` (read/modify/write, small
  file, no locking needed — single writer per session).
- `handoff.md` — agent-neutral transcript tail, rewritten on every switch away
  from an exportable agent.

Pool dirs older than 30 days are pruned opportunistically when a pool dir is
created.

Exporters (python3, same dependency the draft stash already takes;
fail-open — no python3 ⇒ no handoff, switch behaves as today):

- `export_claude_handoff <project_dir> <sid> <out>` — last 30 user/assistant
  text messages from `~/.claude/projects/<munged>/<sid>.jsonl` (string or
  block-list content; thinking/tool blocks skipped) as
  `**User:** / **Assistant:**` markdown.
- `export_codex_handoff <rollout_file> <out>` — same shape from
  `response_item`/`message` payloads, roles user/assistant only,
  `input_text`/`output_text` items; user texts that are internal context
  wrappers (starting with `<`) are skipped.

### 4. Switch flow (`relaunch_switch_tool` in `lib/account-switch.sh`)

On a switch from tool X to tool Y:

1. **Capture X** (before the respawn): claude — nothing new (statusline owns
   the stamp; draft stash unchanged); codex — resolve
   `codex_current_session`, stamp `WISP_DECK_CODEX_SESSION`, record in pool
   meta; opencode — stamp/record the `opencode` flag.
2. **Export handoff**: claude — export the stamped conversation; codex —
   export the captured rollout. Opencode: none (no readable store contract).
3. **Launch Y**:
   - Y has its own session (stamped env / statusline stamp): native resume —
     claude `--resume <sid>` chain (existing), codex `codex resume <sid>`
     chain (new), opencode `--continue`.
   - Otherwise, if `handoff.md` exists and Y is claude or codex: launch fresh
     with a positional initial prompt: *"You are taking over a conversation
     the user was having with another AI coding agent (<X>). Read
     <handoff.md> for the conversation so far, then continue it seamlessly —
     do not re-introduce yourself."* (quoted with `printf %q` into the
     respawn command). Opencode launches fresh with no injection.
   - Otherwise: fresh (today's behavior).
4. Stamp `WISP_DECK_CODEX_STARTED_AT` when Y is codex; update pool
   `last_tool`. All existing behavior (env stamps, border accent, tool pref,
   relaunch-context rewrite) unchanged.

Every step is fail-open: a missing rollout, absent python3, or unwritable pool
degrades to exactly today's switch.

`lib/compact-view.sh`'s dep list gains `session-pool` so the resident ledger
shell has the helpers; `relaunch_switch_tool` guards each call with
`command -v` for bare unit-test sources.

## Out of scope

- Reboot restore of codex/opencode sessions (snapshot format untouched).
- Handoff injection into opencode (no verified prompt-injection vector).
- Continuous/live handoff export (switch-time only).
- The auto-switch quota rotation (claude-login-only by definition).

## Testing (TDD — failing test first, per repo IRON RULE)

`test/bash/session_pool_test.go` (new):
- `codex_current_session`: newest matching-cwd rollout after since; other-cwd
  and older-than-since rollouts ignored; empty when none.
- `export_codex_handoff` / `export_claude_handoff`: markdown contains
  user+assistant texts, excludes tool/thinking/internal items.
- `pool_set`/`pool_get` round-trip; prune removes >30-day dirs, keeps fresh.
- `handoff_prompt` names the handoff file and the source agent.

`test/bash/codex_launch_test.go` (extend):
- resume mode + sid ⇒ guarded `codex resume <sid>` chain with plain fallback;
  resume mode without sid ⇒ plain codex (regression pin).

`test/bash/account_switch_test.go` (extend):
- leaving codex stamps `WISP_DECK_CODEX_SESSION` and records it in pool meta;
- switching to codex with a stamped sid respawns with `codex resume <sid>`;
- claude→codex with no codex sid but a claude transcript: respawn command
  carries the handoff prompt and `handoff.md` holds the conversation;
- codex→claude with no claude sid: claude launch carries the handoff prompt;
- opencode round-trip: leaving stamps the flag, returning uses `--continue`.

Regression net: existing `account_switch_test.go`,
`account_switch_e2e_test.go`, `draft_restore_test.go`,
`restore_account_test.go` must pass unchanged. Full `./run-tests.sh` +
shellcheck before push.
