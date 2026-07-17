# ChatGPT Subscription in Claude Code Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Let Wisp Deck users select an OpenAI GPT subscription for Claude Code and route Claude's Anthropic Messages traffic through their existing `codex login` ChatGPT subscription without exposing credentials or handing tools to Codex.

**Architecture:** Add an auth-aware OpenAI entry to the existing Claude subscription catalog and launch Claude behind a local authenticated HTTP adapter when that entry is active. The adapter translates Anthropic Messages requests into ephemeral `codex app-server` turns, exposes Claude's tools as app-server dynamic tools, suspends turns across `tool_use`/`tool_result`, and translates app-server output back into Anthropic JSON or SSE. Codex owns ChatGPT authentication and model discovery; Claude Code remains the UI, agent loop, permission boundary, and tool executor.

**Tech Stack:** Go 1.24, Bash 3.2-compatible shell, Codex app-server JSON-RPC over stdio, Anthropic Messages HTTP/SSE, Bubble Tea/Lip Gloss, the repository's Go/bash/npx test harnesses.

**Repository constraint:** Execute every task directly on the existing `main` branch. Do not create a branch, worktree, or detached checkout.

**Design reference:** `docs/plans/2026-07-17-chatgpt-subscription-in-claude-code-design.md`

---

### Task 1: Make subscription providers authentication-aware

**Files:**
- Modify: `internal/claudeconfig/catalog.go`
- Modify: `internal/claudeconfig/claudeconfig.go`
- Test: `internal/claudeconfig/claudeconfig_test.go`
- Modify: `internal/opencodeconfig/sync.go`
- Test: `internal/opencodeconfig/sync_test.go`

**Step 1: Write the failing catalog/readiness tests**

Add table-driven tests that require:

- `OpenAI GPT` to resolve to a provider whose auth kind is `codex-chatgpt`.
- The OpenAI aliases to map Opus → `gpt-5.6-sol`, Sonnet → `gpt-5.6-terra`,
  and Haiku/Fable → `gpt-5.6-luna`.
- An OpenAI config to be ready without an API key.
- API-key providers to remain unready without a key and ready with one.
- A config's explicit `WISP_DECK_SUBSCRIPTION_PROVIDER` marker to take
  precedence over display-name heuristics.
- OpenAI GPT to be excluded from generated OpenCode subscriptions.

Use a provider fixture JSON containing:

```json
{
  "env": {
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "openai-chatgpt"
  },
  "model": "gpt-5.6-terra"
}
```

**Step 2: Run the focused tests and confirm failure**

Run:

```bash
go test ./internal/claudeconfig ./internal/opencodeconfig
```

Expected: compilation or assertions fail because provider auth metadata,
provider markers, and OpenCode eligibility do not exist.

**Step 3: Add provider metadata and readiness helpers**

In `catalog.go`, add:

```go
type AuthKind string

const (
    AuthAPIKey      AuthKind = "api-key"
    AuthCodexChatGPT AuthKind = "codex-chatgpt"
)
```

Extend `Provider` with `Auth AuthKind` and `MirrorOpenCode bool`. Add the OpenAI
provider, current ChatGPT-visible models, aliases, and context limits from the
design. Leave per-token costs and unknown output limits at zero: subscription
traffic must never be presented as metered API use.

In `claudeconfig.go`, add helpers that:

- read only the Wisp provider marker from a settings file;
- resolve a provider from that marker, falling back to the existing filename
  and display-name behavior for legacy configs;
- report readiness from provider auth kind rather than always requiring a key.

Keep old call sites source-compatible where practical. Invalid JSON and unknown
markers must fall back to the legacy provider and must not panic.

In `opencodeconfig/sync.go`, skip configs whose provider has
`MirrorOpenCode == false` before reading an API key.

**Step 4: Run the focused tests**

Run:

```bash
go test ./internal/claudeconfig ./internal/opencodeconfig
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/claudeconfig internal/opencodeconfig
git commit -m "feat(config): add ChatGPT auth provider"
```

### Task 2: Seed and render the OpenAI GPT subscription

**Files:**
- Add: `defaults/claude-configs/openai-gpt.json`
- Modify: `defaults/claude-configs.list`
- Modify: `internal/tui/mainmenu.go`
- Modify: `internal/tui/claude_config_panel.go`
- Test: `internal/tui/mainmenu_subscription_focus_test.go`
- Test: `internal/tui/claude_config_panel_test.go`
- Test: `test/npx/distribution_test.go`
- Test: `test/npx/install_e2e_test.go`

**Step 1: Write failing default and TUI tests**

Require the packaged default to contain:

```json
{
  "env": {
    "ANTHROPIC_MODEL": "gpt-5.6-terra",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "gpt-5.6-sol",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.6-terra",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5.6-luna",
    "WISP_DECK_SUBSCRIPTION_PROVIDER": "openai-chatgpt"
  },
  "model": "gpt-5.6-terra"
}
```

Require:

- the config to appear in the subscription ring without an API key;
- API-key configs to preserve the old ring behavior;
- the model panel to render `Authentication  codex login`;
- the OpenAI panel not to expose an API-key editor or accept key-edit input;
- npx packaging and install fixtures to include the new file and list entry.

**Step 2: Run the tests and confirm failure**

Run:

```bash
go test ./internal/tui ./test/npx
```

Expected: FAIL because the default and auth-aware UI do not exist.

**Step 3: Add the default and auth-aware UI**

Create the settings overlay and add `OpenAI GPT:openai-gpt.json` to the defaults
list. Change the main menu's subscription ring/readiness checks to use the
central `claudeconfig` helper. In the config panel, render provider
authentication metadata and make the key row focusable/editable only for
`api-key` providers.

Do not store a placeholder key, base URL, OAuth token, or OpenAI Platform
endpoint in the settings file.

**Step 4: Run the focused tests**

Run:

```bash
go test ./internal/tui ./test/npx
```

Expected: PASS.

**Step 5: Commit**

```bash
git add defaults internal/tui test/npx
git commit -m "feat(tui): expose OpenAI GPT subscription"
```

### Task 3: Build a strict active Codex app-server client

**Files:**
- Add: `internal/gptbridge/rpc.go`
- Add: `internal/gptbridge/rpc_test.go`
- Add: `internal/gptbridge/testdata/fake_app_server.go`

**Step 1: Write failing JSON-RPC client tests**

Build an in-process pipe/fake-process harness. Test:

- monotonically unique request IDs and concurrent calls;
- successful results and structured JSON-RPC errors;
- notifications delivered without blocking the reader;
- server requests delivered to the bridge and answered with the original ID;
- cancellation unblocks all pending calls;
- malformed envelopes, duplicate responses, oversized lines, and unexpected
  EOF fail closed;
- stderr is captured in a bounded diagnostic tail, never mixed with protocol
  stdout.

Add a process test for the initialization sequence:

1. start `codex app-server`;
2. send `initialize` with Wisp Deck client metadata and
   `capabilities.experimentalApi = true`;
3. send `initialized`;
4. call `account/read`;
5. call `model/list`.

The fake app-server program must be compiled only by tests and must never read
real Codex credentials.

**Step 2: Run the test and confirm failure**

Run:

```bash
go test ./internal/gptbridge -run 'TestRPC|TestAppServer'
```

Expected: FAIL because the package does not exist.

**Step 3: Implement the client**

Implement a line-delimited JSON-RPC 2.0 client with a single reader goroutine,
write mutex, pending-call map, bounded server-request and notification queues,
context-aware calls, and deterministic shutdown. Launch app-server with:

```text
codex app-server
```

Pass all behavioral restrictions through `thread/start` configuration rather
than reading or changing the user's Codex config. Keep the child on stdio; do
not expose app-server itself on TCP and do not inspect `~/.codex/auth.json`.

**Step 4: Run tests**

Run:

```bash
go test -race ./internal/gptbridge -run 'TestRPC|TestAppServer'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gptbridge
git commit -m "feat(bridge): add Codex app-server client"
```

### Task 4: Translate Anthropic Messages input into Codex turns

**Files:**
- Add: `internal/gptbridge/anthropic.go`
- Add: `internal/gptbridge/translate.go`
- Add: `internal/gptbridge/translate_test.go`

**Step 1: Write failing request/translation tests**

Use golden fixtures covering:

- string and block-array system prompts;
- user and assistant text history;
- image blocks with base64 and URL sources;
- prior assistant `tool_use` followed by user `tool_result`;
- multiple tool results in one user message;
- Claude tool schemas, descriptions, and `input_schema`;
- `tool_choice` auto, any, none, and named-tool forms;
- alias resolution and direct GPT model IDs;
- `temperature`, `top_p`, `stop_sequences`, and unsupported combinations;
- malformed content, dangling tool results, duplicate tool IDs, excessive body
  size, and unsupported media types.

Expected translation:

- completed Claude history becomes Responses-compatible raw input items on a
  fresh app-server thread;
- the final user content becomes `turn/start.input`;
- Claude tools become app-server `dynamicTools`;
- system instructions become thread/turn developer instructions;
- the requested model and reasoning effort are explicit;
- tool schemas are preserved byte-for-byte semantically.

**Step 2: Run the focused test and confirm failure**

Run:

```bash
go test ./internal/gptbridge -run 'TestTranslate'
```

Expected: FAIL because Anthropic request parsing and translation are absent.

**Step 3: Implement strict protocol types and translation**

Define only the Anthropic fields the adapter supports, while preserving unknown
content-block payloads long enough to return a useful `invalid_request_error`.
Normalize system content, validate tool-call pairing, map images to app-server
input items, and create deterministic dynamic-tool names/IDs.

Never turn Claude's tools into Codex built-ins. Configure each thread to disable
shell/unified execution, file edits, web search, MCP tools, image generation,
skills, and approval prompts. A missing dynamic-tool capability is fatal.

**Step 4: Run tests**

Run:

```bash
go test ./internal/gptbridge -run 'TestTranslate'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gptbridge
git commit -m "feat(bridge): translate Anthropic requests"
```

### Task 5: Translate Codex events into Anthropic responses and SSE

**Files:**
- Add: `internal/gptbridge/stream.go`
- Add: `internal/gptbridge/stream_test.go`

**Step 1: Write failing response tests**

Replay recorded synthetic app-server notifications and require exact Anthropic
event ordering:

```text
message_start
content_block_start
content_block_delta*
content_block_stop
message_delta
message_stop
```

Cover:

- text deltas and final text;
- reasoning deltas mapped only when Claude requested supported thinking output;
- one and multiple dynamic tool calls;
- fragmented JSON arguments emitted as `input_json_delta`;
- text followed by tools;
- stop reasons `end_turn`, `tool_use`, and `max_tokens`;
- input/output/cache token usage;
- non-streaming Messages JSON assembled from the same reducer;
- app-server errors before and after headers;
- disconnect/cancellation behavior;
- UTF-8 boundaries and empty deltas.

**Step 2: Run the test and confirm failure**

Run:

```bash
go test ./internal/gptbridge -run 'TestStream|TestResponse'
```

Expected: FAIL because output reduction and SSE encoding do not exist.

**Step 3: Implement one response reducer with two encoders**

Build a reducer from app-server notifications into Anthropic content blocks and
usage. Use it for both:

- `stream: false`: one Messages JSON response;
- `stream: true`: SSE frames with Anthropic event names and JSON payloads.

Flush after every SSE frame. Once headers are sent, encode failures as
Anthropic-compatible `error` events and terminate the stream. Before headers,
use Anthropic's JSON error envelope and HTTP status mapping.

**Step 4: Run tests**

Run:

```bash
go test -race ./internal/gptbridge -run 'TestStream|TestResponse'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gptbridge
git commit -m "feat(bridge): translate Codex response streams"
```

### Task 6: Suspend dynamic tools across Claude requests

**Files:**
- Add: `internal/gptbridge/engine.go`
- Add: `internal/gptbridge/engine_test.go`

**Step 1: Write failing turn-engine tests**

Drive the engine with a fake app-server and test:

- a text-only turn completes and its ephemeral thread is closed;
- a dynamic `item/tool/call` becomes an Anthropic `tool_use`;
- the corresponding next-request `tool_result` answers the still-pending
  app-server server request and continues that same turn;
- parallel dynamic tool calls wait for all matching results;
- results can arrive in a different order;
- unrelated history cannot satisfy a pending tool;
- missing, duplicate, stale, and cross-client tool IDs fail closed;
- two concurrent Claude subagents cannot see or resume one another's turns;
- client cancellation and idle timeout cancel the app-server turn and reap it;
- completed turns are removed from memory;
- no app-server shell/file/web/MCP tool event is ever accepted.

**Step 2: Run and confirm failure**

Run:

```bash
go test ./internal/gptbridge -run 'TestEngine'
```

Expected: FAIL because cross-request turn state does not exist.

**Step 3: Implement the state machine**

Use unguessable bridge turn IDs embedded in the Anthropic tool-use IDs. Store
only in-memory pending state, scoped by the bridge instance and guarded for
concurrency. Keep the app-server request open while Claude executes tools.
When the next Messages request includes matching tool results, answer each
JSON-RPC server request and continue streaming the original app-server turn.

Start a new app-server thread for every new Claude agent turn. Inject prior
Claude history but never reuse a completed Codex thread as memory.

**Step 4: Run race tests**

Run:

```bash
go test -race ./internal/gptbridge -run 'TestEngine'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gptbridge
git commit -m "feat(bridge): preserve Claude tool turns"
```

### Task 7: Serve an authenticated Anthropic-compatible loopback API

**Files:**
- Add: `internal/gptbridge/server.go`
- Add: `internal/gptbridge/server_test.go`
- Add: `internal/gptbridge/adapter.go`
- Add: `internal/gptbridge/adapter_test.go`

**Step 1: Write failing HTTP and lifecycle tests**

Require:

- binding only to `127.0.0.1` on a random port;
- a random per-launch API key on every request;
- `/health`, `/v1/messages`, and `/v1/messages/count_tokens`;
- Anthropic version and JSON content-type validation;
- bounded request bodies, header timeout, idle timeout, and no proxy trust;
- model validation against live `model/list`;
- explicit errors for missing Codex, incompatible Codex, signed-out state, and
  API-key auth;
- ChatGPT auth accepted without reading any credential file;
- graceful shutdown drains requests, then cancels turns and reaps app-server;
- Claude child exit, signals, and adapter startup failure leave no child
  processes;
- generated environment contains only loopback URL, random bridge key, and
  model aliases.

The count-tokens endpoint must use the selected tokenizer when available;
otherwise test and document a conservative estimate rather than claiming exact
app-server billing.

**Step 2: Run and confirm failure**

Run:

```bash
go test ./internal/gptbridge -run 'TestServer|TestAdapter'
```

Expected: FAIL because no HTTP server or supervisor exists.

**Step 3: Implement server and supervisor**

The supervisor must:

1. locate the explicitly supplied `codex` executable;
2. start and initialize app-server;
3. validate `account/read` is ChatGPT-managed subscription auth;
4. discover models with `model/list`;
5. bind authenticated loopback HTTP;
6. launch the exact Claude argv with:

   ```text
   ANTHROPIC_BASE_URL=http://127.0.0.1:<random-port>
   ANTHROPIC_API_KEY=<random-key>
   ```

7. preserve stdio and exit status;
8. shut down the server and all descendants.

Return actionable stderr such as `Install Codex with wisp-deck, then run codex
login` and never silently fall back to Anthropic or OpenAI API-key billing.

**Step 4: Run race tests**

Run:

```bash
go test -race ./internal/gptbridge -run 'TestServer|TestAdapter'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gptbridge
git commit -m "feat(bridge): serve local Anthropic API"
```

### Task 8: Wire the adapter into Wisp Deck's Claude launch

**Files:**
- Add: `cmd/wisp-deck-tui/claude_gpt_adapter.go`
- Add: `cmd/wisp-deck-tui/claude_gpt_adapter_test.go`
- Modify: `cmd/wisp-deck-tui/main.go`
- Modify: `lib/claude-configs.sh`
- Modify: `lib/tmux-session.sh`
- Modify: `wrapper.sh`
- Test: `test/bash/claude_configs_test.go`
- Add: `test/bash/claude_gpt_adapter_launch_test.go`
- Modify: `test/bash/claude_attention_launch_test.go`
- Modify: `test/bash/tool_switch_pool_test.go`

**Step 1: Write failing CLI and shell tests**

Require:

- `wisp-deck-tui claude-gpt-adapter --codex <path> -- <claude argv...>` to
  reject missing separators/commands and forward child exit status;
- a pure shell helper to read the active settings provider marker with `jq`;
- selecting `openai-chatgpt` to wrap the entire Claude raw resume/fallback chain
  exactly once;
- the existing `claude-attention` supervisor to remain the outermost wrapper;
- ordinary Claude, Zhipu, and MiMo launch strings to remain byte-for-byte
  unchanged;
- quoted prompts, project paths, account directories, screenshot filtering,
  restore mode, and mid-session tool switches to remain argv-safe;
- a missing `codex` command to reach the adapter's actionable error rather than
  selecting another provider;
- relaunch-context persistence to preserve the active provider.

**Step 2: Run and confirm failure**

Run:

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'GPT|Gpt|ClaudeAttention|ClaudeConfig|ToolSwitch'
```

Expected: FAIL because the command and launch wrapper do not exist.

**Step 3: Add the CLI and launch wrapper**

Register the command in `cmd/wisp-deck-tui/main.go`. In
`lib/claude-configs.sh`, add a side-effect-free provider-marker resolver. In
`wrapper.sh`, resolve and export the active provider beside
`WISP_DECK_CLAUDE_SETTINGS`.

In `build_ai_launch_cmd`:

1. build the existing raw Claude command;
2. when the provider marker is `openai-chatgpt`, quote and wrap that whole raw
   command with `wisp-deck-tui claude-gpt-adapter --codex <resolved codex> --
   bash -c <raw>`;
3. apply `claude-attention` outside that adapter as today.

Do not call `ensure_codex` from `wrapper.sh`: it is an interactive installer
belonging to `bin/wisp-deck`. The adapter gives the user the precise recovery
command when Codex is absent.

**Step 4: Run focused tests and shellcheck**

Run:

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'GPT|Gpt|ClaudeAttention|ClaudeConfig|ToolSwitch'
shellcheck wrapper.sh lib/claude-configs.sh lib/tmux-session.sh
```

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/wisp-deck-tui lib wrapper.sh test/bash
git commit -m "feat(launch): route Claude through ChatGPT"
```

### Task 9: Add a real gated Claude-to-ChatGPT end-to-end test

**Files:**
- Add: `test/bash/claude_gpt_live_e2e_test.go`
- Add: `test/bash/testdata/gpt_bridge_tool.sh`

**Step 1: Write the gated test**

Skip unless `WISP_DECK_LIVE_GPT_CLAUDE_E2E=1`. Before running, require:

- `claude` and `codex` on PATH;
- `codex login status` reports ChatGPT auth;
- a supported model is available.

Launch real Claude in non-interactive/print mode through the real adapter with
a temporary config and a harmless Claude-owned test tool. Assert:

- the answer contains a nonce supplied in the prompt;
- the model path is GPT, not Anthropic;
- the tool is requested by GPT, executed by Claude, and its nonce appears in
  the final answer;
- the adapter emits streaming usage and a terminal stop event;
- no Codex built-in execution event occurs;
- app-server and Claude descendants are gone after success and cancellation.

Do not print credentials, auth files, bridge keys, or full environment dumps.

**Step 2: Run once without the gate**

Run:

```bash
go test ./test/bash -run TestLiveGPTSubscriptionInClaude -v
```

Expected: PASS with an explicit skip.

**Step 3: Run the live path**

Run:

```bash
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 \
  go test ./test/bash -run TestLiveGPTSubscriptionInClaude -v -count=1
```

Expected: PASS against the locally logged-in ChatGPT subscription.

**Step 4: Commit**

```bash
git add test/bash
git commit -m "test(bridge): cover live ChatGPT Claude path"
```

### Task 10: Document setup, limitations, and recovery

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Step 1: Add documentation assertions where existing tests cover docs**

Extend the appropriate release/distribution tests to require the user-visible
setup and warning text.

**Step 2: Document the feature**

Explain:

1. install/update Codex with Wisp Deck or npm;
2. run `codex login` and choose ChatGPT sign-in;
3. select `OpenAI GPT` in Wisp Deck's Subscription panel;
4. relaunch existing ledger panes/sessions after updating Wisp Deck.

State that OpenAI Platform API-key login is rejected, OpenCode mirroring is
intentionally absent, dynamic tools require a compatible Codex version, and
the adapter never reads or stores the ChatGPT token.

**Step 3: Run documentation-related tests**

Run:

```bash
go test ./test/bash ./test/npx -run 'Release|Distribution'
```

Expected: PASS.

**Step 4: Commit**

```bash
git add README.md CHANGELOG.md test
git commit -m "docs: explain ChatGPT Claude subscription"
```

### Task 11: Full verification, install, and repository handoff

**Files:**
- Verify all modified files
- Update local installation with `make install`

**Step 1: Audit the complete diff**

Run:

```bash
git status --short
git diff --check
git diff origin/main...HEAD
```

Check specifically for credential access, private OpenAI endpoints, fallback
to API-key billing, Codex-owned tools, unauthenticated listeners, leaked child
processes, and changes to non-GPT launch behavior.

**Step 2: Run formatters and static checks**

Run:

```bash
gofmt -w cmd/wisp-deck-tui internal/gptbridge internal/claudeconfig internal/opencodeconfig internal/tui test
shellcheck wrapper.sh lib/claude-configs.sh lib/tmux-session.sh
go vet ./...
```

Review formatter changes before staging.

**Step 3: Run the full repository suite**

Run:

```bash
./run-tests.sh
```

Expected: all Go, Bash, npx, integrity, and packaging tests PASS.

**Step 4: Re-run the live gated test**

Run:

```bash
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 \
  go test ./test/bash -run TestLiveGPTSubscriptionInClaude -v -count=1
```

Expected: PASS.

**Step 5: Update the local installation**

Run:

```bash
make install
```

Expected: build, ad-hoc signing, copy to `~/.local/bin`, installed signing, and
warmup all succeed.

**Step 6: Verify the required installation invariants**

Run:

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 bin/wisp-deck-tui
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: both `test` commands exit zero and both signatures are valid.

**Step 7: Synchronize and publish**

Run:

```bash
git pull --rebase
./run-tests.sh
git push
git status --short --branch
```

Expected: push succeeds and `main` is clean and up to date with `origin/main`.

**Step 8: Final handoff**

Report:

- OpenAI GPT is selectable and uses `codex login` ChatGPT auth;
- automated and live E2E results;
- installation path, matching SHA-256, and valid signatures;
- existing running ledger panes/sessions must be relaunched to load the new
  binary and provider launch environment.
