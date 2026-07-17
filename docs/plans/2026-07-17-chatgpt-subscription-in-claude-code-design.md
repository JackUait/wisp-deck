# ChatGPT Subscription in Claude Code — Design

**Date:** 2026-07-17
**Status:** Approved for implementation

## Summary

Add an **OpenAI GPT** entry to Wisp Deck's existing Subscription selector. When
that subscription is active, Wisp Deck still launches the ordinary Claude Code
client and preserves its UI, permissions, tools, hooks, transcripts, resume
behavior, and status line. A local Wisp Deck adapter translates Claude Code's
Anthropic Messages API traffic into the public Codex app-server protocol and
uses the ChatGPT subscription already authenticated by `codex login`.

The implementation must never turn this into metered OpenAI API-key usage,
copy OAuth tokens out of Codex, or replace Claude Code with a nested
`codex exec` agent.

## Goals

- Make a ChatGPT subscription selectable beside Zhipu GLM and Xiaomi MiMo.
- Reuse the user's existing ChatGPT authentication from `codex login`.
- Keep Claude Code as the visible and controlling agent client.
- Preserve Claude Code's tool execution and permission boundaries.
- Support text, images, streaming, sequential and parallel tool calls, resume,
  concurrent Claude subagents, cancellation, and token-usage reporting.
- Fail explicitly when Codex is missing, signed out, authenticated with an API
  key, too old for the required protocol, or missing a configured model.
- Verify the real Claude Code → Wisp bridge → Codex app-server → ChatGPT path
  with a gated live end-to-end test.

## Non-goals

- Calling ChatGPT's private backend directly.
- Reading, copying, parsing, refreshing, or persisting Codex OAuth tokens.
- Adding OpenAI Platform API-key support.
- Making the ChatGPT subscription available to OpenCode.
- Replacing Claude Code with `codex exec`, the Codex SDK, or a second coding
  agent that owns filesystem and shell tools.
- Sharing a long-lived Codex thread across completed Claude turns.
- Supporting arbitrary third-party Responses-compatible providers in this
  change.

## Decisions

### Authentication

The adapter starts `codex app-server` and lets Codex own authentication and
token refresh. It checks app-server's account state before accepting Claude
traffic:

- ChatGPT authentication: accepted.
- Signed out: rejected with an instruction to run `codex login`.
- API-key authentication: rejected so subscription usage never silently becomes
  metered API usage.
- Enterprise access-token authentication: out of scope for the first version
  unless app-server reports it as ChatGPT-managed subscription access.

The bridge never opens `~/.codex/auth.json`, queries the macOS credential store,
or receives an OAuth token.

### Public integration boundary

Use `codex app-server`, the documented interface for deep product integrations.
Its JSON-RPC protocol provides authentication state, model discovery, streamed
agent events, ephemeral threads, raw history injection, and host-provided
dynamic tools.

`dynamicTools` is currently experimental. The adapter opts into
`experimentalApi` and treats rejection of that field or the
`item/tool/call` method as a protocol incompatibility with a clear Codex upgrade
message. No fallback may give Codex its own shell or filesystem tools.

### Claude remains the agent client

Codex app-server is configured per bridge thread with these capabilities
disabled:

- shell and unified execution;
- file editing and image generation;
- web search and browser/computer use;
- apps, plugins, MCP servers, skills, and hooks;
- multi-agent delegation;
- persistent memories.

The model sees only the dynamic tools supplied in Claude Code's Messages API
request. The adapter supplies Claude's system prompt as the thread's base
instructions and a small developer instruction that defines the translation
contract. Tool execution always returns to Claude Code.

## User Experience

### Seeded subscription

Installation seeds:

```text
OpenAI GPT:openai-gpt.json
```

The settings file contains no secret and no fixed localhost port. Its `env`
section carries:

```json
{
  "WISP_DECK_SUBSCRIPTION_PROVIDER": "openai-chatgpt",
  "ANTHROPIC_DEFAULT_OPUS_MODEL": "gpt-5.6-sol",
  "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.6-terra",
  "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5.6-luna",
  "ANTHROPIC_DEFAULT_FABLE_MODEL": "gpt-5.6-luna"
}
```

The marker is metadata read by Wisp Deck. The adapter overrides routing and
local authentication in the child Claude process; no base URL or fake
credential is persisted in the profile.

### Subscription selector

Provider metadata gains an authentication kind:

- `api-key` for Zhipu and MiMo;
- `codex-chatgpt` for OpenAI GPT.

API-key subscriptions remain selectable only after a key is present. The
OpenAI GPT profile is selectable without a key. In its model-mapping panel, the
API-key editor is replaced with:

```text
Authentication   codex login
```

The TUI does not synchronously probe Codex authentication while rendering.
Authentication is verified by the adapter at launch.

### Model mapping

The OpenAI provider catalog initially exposes the visible models advertised by
the supported Codex release:

- `gpt-5.6-sol`
- `gpt-5.6-terra`
- `gpt-5.6-luna`
- `gpt-5.5`
- `gpt-5.4`
- `gpt-5.4-mini`
- `gpt-5.3-codex-spark`

The bridge queries app-server's live model catalog during startup. The model
sent by Claude must appear in that catalog. An unavailable mapping produces an
actionable error listing available visible models; it is never silently routed
to a different model.

Claude thinking budgets map deterministically to the nearest supported Codex
reasoning effort. When thinking is absent, the model's advertised default
effort is used. Unsupported Anthropic sampling controls are ignored only when
Claude Code sends their defaults; a non-default unsupported control returns an
explicit validation error.

### Installation and launch

The active settings file's provider marker is resolved after project selection.
For OpenAI GPT:

1. Ensure `codex` exists using the repository's existing `ensure_codex` path.
2. Build the normal Claude launch/resume fallback chain.
3. Wrap that chain in `wisp-deck-tui claude-gpt-adapter`.
4. The adapter starts app-server, validates ChatGPT auth and the selected model,
   listens on an ephemeral loopback port, and launches Claude with:
   - `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>`;
   - a random per-launch local API key;
   - the selected `--settings` file and existing account/statusline environment.
5. The outer existing `claude-attention` supervisor continues to own semantic
   attention state.

Standard Claude, Zhipu, and MiMo commands remain byte-for-byte equivalent to
their current launch behavior.

## Process Architecture

```text
tmux AI pane
└─ wisp-deck-tui claude-attention
   └─ wisp-deck-tui claude-gpt-adapter
      ├─ codex app-server (JSON-RPC over stdio)
      ├─ loopback Anthropic Messages server
      └─ Claude Code
         └─ tools execute under Claude's existing permission model
```

The adapter is the process supervisor for its two children. Claude exit,
app-server exit, pane cancellation, or adapter cancellation closes the
listener, interrupts active app-server turns, resolves pending requests with an
error, and terminates both children. No daemon or cross-session bridge remains.

## Request and Conversation Model

### Why threads are per Claude turn

The Anthropic Messages API is stateless: every request includes conversation
history. Codex app-server is stateful: a thread owns turns and tool calls.
Claude can also issue concurrent requests for subagents.

The bridge therefore creates one ephemeral Codex thread per new Claude agent
turn, not one thread per Claude process:

- A new Messages request without a recognized pending `tool_result` creates a
  new ephemeral thread.
- Completed Claude history is reconstructed in that thread.
- The final user message starts one Codex turn.
- A dynamic tool call keeps that Codex turn alive across the next HTTP request.
- When Codex produces its final text, the ephemeral thread is discarded.

This makes completed calls stateless, isolates concurrent subagents, and keeps
only active tool loops in memory.

### History injection

For a new thread, all request messages except the final user message are mapped
to Responses-compatible items and injected before `turn/start`:

- user text → user message input text;
- assistant text → assistant message output text;
- assistant `tool_use` → function/custom tool call;
- user `tool_result` → matching tool-call output;
- base64 image → input image data URL.

The final user message is sent as the app-server turn input. If an installed
Codex protocol cannot inject a required item shape, the adapter rejects the
request rather than flattening history into a lossy text transcript.

### Dynamic tool loop

Claude tool definitions become app-server dynamic function tools with the same
name, description, and JSON input schema.

When app-server sends `item/tool/call`:

1. Record the JSON-RPC request ID, Codex call ID, thread, and turn.
2. Return an Anthropic `tool_use` content block using a bridge-generated ID.
3. Keep the JSON-RPC request unanswered.
4. On the next Messages request, match each `tool_result` by bridge ID.
5. Reply to app-server with `inputText` and/or inline `inputImage` content items.
6. Set `success: false` when Claude reports an error result.
7. Continue consuming the same app-server turn until another tool batch or the
   final response.

All tool calls emitted in the same model step are returned in one Anthropic
response. Parallel results may arrive in any order and are delivered to their
matching JSON-RPC requests.

Unknown, duplicate, expired, or cross-thread tool-result IDs are invalid
requests and never start a new thread.

### Streaming

Claude Code normally requests SSE streaming. The bridge emits a valid
Anthropic sequence:

1. `message_start`;
2. `content_block_start`;
3. text or input-JSON deltas;
4. `content_block_stop`;
5. `message_delta` with stop reason and usage;
6. `message_stop`.

App-server `item/agentMessage/delta` events become text deltas. Dynamic tool
arguments arrive atomically and are emitted as one valid `input_json_delta`.
Text preceding a tool call is preserved as an earlier text block.

Non-streaming Messages requests return the equivalent JSON message. A client
disconnect interrupts a turn only when no pending tool call has deliberately
crossed the HTTP boundary.

### Stop reasons and usage

- final agent text → `end_turn`;
- one or more dynamic calls → `tool_use`;
- app-server interruption caused by client cancellation → an error;
- model/context limit → the closest Anthropic stop reason available.

Use app-server's latest turn token-usage notification. Input, cached input, and
output totals are translated where Anthropic exposes matching fields. Missing
usage is zero only for an error response; successful responses must carry a
known or explicitly estimated value.

### Additional endpoints

The loopback server supports the endpoints Claude Code exercises:

- `POST /v1/messages`;
- `POST /v1/messages/count_tokens`, using the selected GPT tokenizer or a
  documented conservative estimate until app-server exposes exact counting;
- a small authenticated health endpoint used only by the adapter.

Any other path returns an Anthropic-style not-found error. Every endpoint
requires the random local key even though the listener is loopback-only.

## Components

### `internal/claudeconfig`

- Add provider authentication/mirroring metadata.
- Add the OpenAI model catalog and aliases.
- Read `WISP_DECK_SUBSCRIPTION_PROVIDER` from a settings file.
- Define `ConfigReady`: API-key providers require a key; ChatGPT does not.
- Keep OpenAI GPT out of OpenCode mirroring and API-price enrichment.

### `internal/gptbridge`

A new package containing:

- strict Anthropic request/response/content types;
- pure history and tool translation;
- Anthropic JSON and SSE writers;
- the authenticated loopback HTTP handler;
- pending-turn and pending-tool state;
- app-server JSON-RPC client and subprocess lifecycle;
- model/auth capability validation;
- the high-level adapter runner.

The pure translator must not depend on processes, sockets, globals, or the real
clock. Interfaces around app-server and process launch allow deterministic
integration tests.

### `cmd/wisp-deck-tui`

Add a hidden `claude-gpt-adapter` command with argv-safe flags for:

- absolute Claude and Codex executable paths;
- the selected settings path;
- the already-built Claude argument/resume chain;
- project working directory;
- test-only startup and shutdown timeouts where needed.

The command does not evaluate arbitrary shell text. Existing fallback chains
must be represented as validated argv attempts or confined to the same
allowlisted command forms already emitted by Wisp Deck.

### Bash launch/install plumbing

- Detect the provider marker for the active settings file.
- Ensure Codex only for the OpenAI GPT subscription.
- Insert the adapter inside the existing Claude attention supervisor.
- Preserve screenshot filtering, account isolation, resume fallback, session
  restore, and process cleanup.
- Add the new default profile to local and npm distributions.

### TUI

- Treat ChatGPT-authenticated configs as ready without an API key.
- Render authentication state instead of an API-key editor.
- Keep model mappings editable.
- Leave existing subscription cycling and Standard behavior unchanged.

## Security

- Bind only `127.0.0.1` on an ephemeral port.
- Generate a high-entropy per-launch key and compare it in constant time.
- Never log request authorization, prompts, tool results, or app-server auth
  payloads by default.
- Cap request/header sizes high enough for Claude's supported context but reject
  unbounded input.
- Do not expose app-server on TCP; use stdio owned by the adapter.
- Disable every Codex-hosted execution or external-context tool.
- Use ephemeral app-server threads so Claude transcripts remain the durable
  conversation source.
- Keep credential-store and `auth.json` access exclusively inside Codex.

## Error Behavior

All HTTP failures use Anthropic's error envelope and an appropriate status:

| Condition | Behavior |
| --- | --- |
| Invalid local key | 401 `authentication_error` |
| Malformed/unsupported Messages request | 400 `invalid_request_error` |
| Signed out of Codex | startup error: run `codex login` |
| Codex using API key | startup error: ChatGPT login required |
| Missing Codex executable | install through `ensure_codex`, then fail clearly if unavailable |
| Requested model unavailable | 400 with supported visible model IDs |
| `dynamicTools` rejected | 502 protocol error with Codex upgrade instruction |
| App-server exits or returns RPC error | 502 `api_error`; terminate affected turns |
| Unknown/duplicate tool result | 400 `invalid_request_error` |
| Every active request cancelled | interrupt and discard its ephemeral thread |
| Adapter or Claude exits | cancel all state and terminate both children |

No failure falls back to Anthropic, OpenAI Platform API-key billing, a different
GPT model, `codex exec`, or Codex-owned tools.

## Testing

All behavior is implemented test-first: add one failing test, run it and confirm
the expected failure, implement the minimum behavior, then rerun green.

### Pure unit tests

- Provider resolution, authentication kind, readiness, and OpenCode exclusion.
- Seeded profile fields and default model aliases.
- Anthropic request decoding and validation.
- System prompt, text, image, history, and final-user translation.
- Tool-schema translation.
- Dynamic call → `tool_use` and result → JSON-RPC response.
- Error tool results and mixed text/image results.
- Sequential and parallel tool batches.
- Unknown, duplicate, expired, and cross-thread tool IDs.
- Thinking-budget → reasoning-effort mapping.
- Model catalog validation.
- Non-streaming response shape and stop reasons.
- Exact SSE ordering, text deltas, tool JSON, usage, and message stop.
- Anthropic error envelopes and HTTP statuses.
- Token-count endpoint behavior.

### App-server integration tests

Use a controllable fake JSON-RPC subprocess/transport to cover:

- initialize/initialized negotiation and experimental capability;
- ChatGPT, signed-out, and API-key account states;
- model-list discovery;
- thread start, history injection, and turn start;
- delayed tool response across two HTTP requests;
- parallel calls/results in different orders;
- app-server error/exit and adapter cancellation;
- no built-in execution request is accepted;
- multiple simultaneous Claude turns remain isolated.

### Command and Bash tests

- `claude-gpt-adapter` validates absolute executable/settings paths.
- OpenAI GPT launch uses the adapter for fresh and every resume fallback.
- Standard, Zhipu, and MiMo launch strings remain unchanged.
- Screenshot filtering, account config, attention generation, and settings path
  survive adapter wrapping.
- Missing Codex invokes only the existing safe installer path.
- Provider marker parsing cannot inject shell text.
- Package manifests and npx copies include the new default.

### TUI tests

- OpenAI GPT appears in the main Subscription ring without an API key.
- API-key configs still require keys.
- The GPT model panel shows `codex login` and has no key-edit hit target.
- Model cycling and persistence use the OpenAI catalog.
- OpenCode sync ignores the ChatGPT provider and preserves unrelated config.

### Live end-to-end verification

Add an opt-in test, for example:

```sh
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 \
  go test ./test/bash -run TestLiveGPTSubscriptionInClaude -v -count=1
```

It requires real `claude`, a current `codex`, and ChatGPT authentication. It
launches a bounded non-interactive Claude prompt through the adapter and proves:

1. the response came from a configured GPT model;
2. one harmless Claude-owned tool completes through the dynamic-tool round trip;
3. no Codex-owned command/file tool ran;
4. streaming completed with usage;
5. every adapter child exited.

The live test is never part of ordinary CI because it consumes subscription
quota and requires interactive account state, but it is mandatory on the
development machine before this feature is declared complete.

### Final gates

- `gofmt` on modified Go files.
- `shellcheck` on every modified shell script.
- Targeted red/green evidence for every behavior group.
- `./run-tests.sh`.
- `git pull --rebase`, `git push`, and clean/up-to-date `git status`.
- `make install`.
- `command -v wisp-deck-tui` equals `~/.local/bin/wisp-deck-tui`.
- SHA-256 of `~/.local/bin/wisp-deck-tui` equals `bin/wisp-deck-tui`.
- `codesign --verify --verbose ~/.local/bin/wisp-deck-tui`.
- Relaunch any running ledger pane/session to load the installed binary.

## Authoritative References

- OpenAI Codex authentication:
  <https://learn.chatgpt.com/docs/auth>
- OpenAI Codex app-server:
  <https://learn.chatgpt.com/docs/app-server>
- OpenAI Codex app-server source/protocol:
  <https://github.com/openai/codex/tree/main/codex-rs/app-server>
- Anthropic Messages API:
  <https://platform.claude.com/docs/en/api/messages>
- Anthropic streaming Messages:
  <https://platform.claude.com/docs/en/api/messages-streaming>
- Anthropic tool use:
  <https://platform.claude.com/docs/en/agents-and-tools/tool-use/implement-tool-use>
