# GPT bridge: stop cold rebuilds from draining the ChatGPT quota

## Problem

Codex scopes the prompt cache to a thread. Measured on 2026-09-28 against a
live app-server (gpt-6-astra, 37K-token history):

| request | thread | cached |
|---|---|---|
| A1, first | A | 0 |
| A2, next turn | A | 36,992 (99.7%) |
| B1, byte-identical to A1 | new thread B | 0 |

The Codex source agrees: `prompt_cache_key` is the session id, and the
ChatGPT backend picks the cache shard from the same id
(`codex-rs/core/src/client.rs`, `prompt_cache_key` / `responses_session_id`).
So every new bridge thread replays its whole history uncached.

The Codex log (`~/.codex/logs_2.sqlite`, 30.4 hours) shows who creates them:

| first input of the thread | threads | context tokens |
|---|---|---|
| Claude Code's subagent progress summary | 5,287 | 630M |
| a message to a working subagent | 418 | 56M |
| a user message | 455 | 23M |
| compaction | 43 | 12.5M |
| everything else | ~600 | ~25M |

The progress summary is Claude Code's `AgentSummary`: every 30 seconds it
forks each running subagent's conversation with the prompt `Describe your most
recent action in 3-5 words using present tense (-ing)…`, `skipTranscript:true`
(so it is invisible in transcripts and Stats). Against Anthropic the fork reads
the cache. Through the bridge it ends in an already-answered tool result, so
`resume` fails, recovery starts a new thread, and ~125K tokens go out uncached
on the session model at the session effort — for 3-5 words.

Claude Code has no user setting that turns it off, and it marks the fork in no
header or body field (captured: only the `anthropic-beta` list differs).
`thread/fork` needs a persisted rollout, and bridge threads are ephemeral.

## Goal

Claude Code's background calls never again drain the ChatGPT quota unseen.
Real work (user messages, messages to subagents, compaction, skills) keeps its
full context.

## Design

### Layer 1: the bridge answers progress summaries itself

At the top of `Engine.Execute`, before `resume` or `start`: when an input text
item starts with `Describe your most recent action in 3-5 words`, the engine
calls no Codex RPC. It answers from the last `function_call` in the history:

| tool | reply |
|---|---|
| `Read` / `Edit` / `Write` / `NotebookEdit` | `Reading` / `Editing` / `Writing` / `Editing` + the file's base name |
| `Grep` / `Glob` | `Searching for` + the pattern |
| `Bash` | its `description`, else `Running a shell command` |
| any other | `Using` + the tool name |
| no tool call yet | `Starting work` |

The reply is one line of at most 100 characters (Claude Code drops a longer or
multi-line summary). It is emitted as an ordinary `end_turn` text message with
zero usage, through the same `emit` path as a model reply.

A live check `WISP_DECK_LIVE_AGENT_SUMMARY_E2E=1` reads the installed Claude
Code binary and fails when the prompt text is gone. It joins the list of
upgrade checks in the root `CLAUDE.md`.

### Layer 2: the thread survives between messages

When a turn ends normally (`end_turn`), the engine parks the thread instead of
deleting it. A parked thread remembers:

- the digests of the Claude history it was built from;
- the digest of the user input of its last turn;
- the model, and a fingerprint of the developer instructions and the tool set.

A new request (no tool results) reuses a parked thread when all hold:

- same model, same instructions fingerprint, same tool-set fingerprint;
- its history starts with the parked digests;
- the items after them are one user message equal to the parked input,
  followed only by assistant messages, `function_call` and
  `function_call_output` items.

Then the engine runs `turn/start` on that thread with the new input. Anything
else starts a new thread, exactly as today. A thread is taken out of the pool
while in use, so two requests never share it. At most 16 threads are parked;
one idle for 30 minutes, or pushed out by the cap, is deleted with
`thread/delete`.

### Layer 3: a warning fuse

Every new thread started with a non-empty history is a cold start. The engine
reports it (the estimated input tokens and the time) to a fuse shared by every
engine of one bridge (it survives `ResilientExecutor` rebuilds).

When cold-start tokens in the last 10 minutes pass 2,000,000, the fuse warns,
at most once an hour:

- a macOS notification through `runHostEffect`, e.g. `GPT bridge: 3.4M tokens
  re-sent without cache in 10 min`;
- one line with the numbers appended to
  `~/.config/wisp-deck/gptbridge-cold.log`.

Both are host effects: off under `WISP_DECK_TESTING`. It never blocks or
changes a request.

The threshold: legitimate cold starts in the log above average ~0.6M per 10
minutes before layer 2, while the summary leak alone was ~3.5M.

## Wiring

Both bridge entry points get the fuse: `NewChatGPTBridge` (All-In,
`cmd/wisp-deck-tui/claude_allin.go`) and `RunAdapter` (the standalone GPT
profile, `cmd/wisp-deck-tui/claude_gpt_adapter.go`).

## Testing

- Layer 1: each tool row of the table; the 100-character cap; no RPC call is
  made; it wins over a continuation whose tool ids are unknown.
- Layer 2: reuse on an exact extension; a new thread on a changed model,
  instructions, tool set, an edited earlier item, or an extra user item; the
  cap and the idle expiry delete threads; an interrupted turn is never parked.
- Layer 3: warns once when the window passes the threshold, not again within
  the hour, never below it; cold starts across an engine rebuild count
  together.
- Live: repeat the Codex-log classification after a real session with
  subagents; progress-summary threads must be gone.

## Not in scope

- The phantom usage rows (`stream.go` `Finish` fallback) that inflate Stats.
- The other Claude Code forks (prompt suggestion, away recap): too rare in the
  log to act on now; the fuse is what catches them if that changes.
