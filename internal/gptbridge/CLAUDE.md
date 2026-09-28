# gptbridge — gotchas

Gotchas for the Codex bridge. Loaded when Claude opens a file in this package.

### There are two ways in, and only one of them owns a terminal

`RunAdapter` is the dedicated GPT pane: it owns the app-server, the loopback
API **and** the Claude child, and it runs before Claude exists.
`ChatGPTBridge` (`chatgptbridge.go`) is the middle piece alone, for
`internal/allin`'s router, which serves ChatGPT as one row among many.

Everything that differs between them follows from the terminal:

- **No interactive login.** `RunAdapter` can call `LoginChatGPT` and print an
  auth URL because nothing is painting the pane yet. A bridge started from the
  router is starting inside a live turn, where stdout/stderr are the screen
  Claude Code is drawing on — so `buildAppServer` refuses a signed-out Codex
  with a turn error naming `codex login` instead.
- **A 60s startup timeout, not three minutes.** `RunAdapter`'s budget is paid
  once at pane launch; this one is paid inside a turn, against Claude Code's
  20s stall banner and 180s abort. Measured on this machine under load average
  25: 2.15s through the npm shim, 2.25s for the 220MB native binary on a fresh
  inode, 436ms warm.
- **Lazy, shared, and closed by hand.** Nothing starts until `Endpoint` is
  called; the mutex is held across the whole start so concurrent first turns
  share one app-server; a **failed** start is never cached, because the usual
  cause is fixed between turns. `Close` is called explicitly before `os.Exit`
  by the launch wrapper — see `internal/allin/CLAUDE.md` for the shutdown
  routes, including the measured 7.6ms stdin-EOF exit that covers a SIGKILL.

`buildBundle` is one function used both for the first start and as
`ResilientExecutor`'s rebuild, so a mid-session app-server death is replaced
under the same account check.

### Compaction runs at low effort, and the match must not be anchored

Claude Code reuses the session's thinking budget for the summarization prompt it
generates, so a bridged compaction runs at whatever effort the pane is on.
`isClaudeCompactionInput` downgrades it to `low`, and that downgrade is the only
lever wisp-deck has over how long the model generates in silence — which
matters because **Codex tears down its own upstream connection when a
generation is silent too long, and replays the request from scratch**:

```
13:06:57  message item msg_…eca3ab8da1c7623 starts
   [ 7m55s silent ]
13:14:52  WARN stream disconnected - retrying (1/5 in 210ms)
          sampling_error=… idle timeout waiting for websocket
13:15:23  NEW message item msg_…9698bfc377a8f277     <- replayed from scratch
13:20:37  done
```

That is one real compaction (`~/.codex/logs_2.sqlite`, thread
`01a076d3-88d7-…`): the finished first summary was discarded and the session
was blocked **14.5 minutes**. `stream_max_retries` is 5, so a model slow enough
to trip it every time fails the turn outright.

**That timeout is not configurable.** `stream_idle_timeout_ms` exists on
`ModelProviderInfo`, but `codex app-server --strict-config -c
model_providers.openai.stream_idle_timeout_ms=…` answers *"model_providers
contains reserved built-in provider IDs: `openai`. Built-in providers cannot be
overridden"*, and there is no root-level key (`unknown configuration field`) and
no env var. Effort is the whole lever.

- **Never anchor the match to either end of the message.** It was
  `strings.HasPrefix` against the three template sentences; 2.1.263 began
  building every prompt as `CRITICAL: Respond with TEXT ONLY…` + template +
  `REMINDER: Do NOT call any tools…` (`oPn` and `B0e` in the bundle), so the
  downgrade silently stopped firing and the compaction above ran at
  `codex.turn.reasoning_effort=ultra`. The failure is invisible: nothing errors,
  the session just gets slower once per compaction.
- **The analysis-tag instruction is still required alongside a template**, so an
  ordinary user message asking for a summary is not mistaken for compaction.
- **The effort reaches Codex only through `turn/start`.** `Execute` routes a
  request carrying tool results to `resume`, which never sends `effort` — a
  compaction request is a plain user turn, which
  `TestTranslateClaudeCompactionCarriesNoToolResults` pins.
- **The templates are vendor-controlled, so a test reads the installed client.**
  `TestClaudeCompactionTemplatesStillMatchTheInstalledClient` scans every binary
  under `~/.local/share/claude/versions` for the analysis-tag anchor and fails
  if the surrounding prompt matches no template — so the next rewording goes red
  instead of costing minutes per compaction. It skips where no client is
  installed, which is every CI runner.

Guarded by `internal/gptbridge/compaction_prompt_test.go`.

### A request's `tools[]` holds two different kinds of tool

The GPT bridge originally modelled every entry of an Anthropic request's
`tools[]` as a Claude-hosted function and required each to carry an
`input_schema`. Anthropic's **server tools** break that assumption: they are
identified by a `type` (`web_search_20250305`, `code_execution_*`, …), they run
on Anthropic's side, and they therefore have **no `input_schema` at all**.

That mismatch shipped. Claude Code's WebSearch tool does not search by itself —
it issues a *nested* Messages request whose entire tools array is the one server
tool:

```js
tools:      [{type: "web_search_20250305", name: "web_search", max_uses: 8}]
tool_choice: {type: "tool", name: "web_search"}
```

The bridge rejected it, so **every WebSearch in a GPT-backed session** came back
as `API Error: 400 tools[0]: input_schema must be an object`. The `tools[0]` is
the tell: the main loop's array starts with a real schema'd tool, so an index-0
schema complaint can only be that single-server-tool request.

The rules that keep this working:

- **Partition before validating.** `Tool.IsServerTool()` (empty or `"custom"`
  `type` means Claude-hosted) decides which entries get schema-checked and
  turned into `dynamicTools`. A server tool must never reach Codex as a
  host-provided function — Codex would try to call a function nobody hosts.
- **`tool_choice` can name a server tool.** Resolving `{"type":"tool"}` against
  the Claude-hosted tools alone reported `tool_choice names unknown tool
  "web_search"` — a *second*, distinct 400 hiding behind the first. A forced
  server tool is already satisfied by the thread's own capability, so it yields
  no dynamic tool and no directive. Naming a tool that was never supplied at
  all is still an error.
- **Never silently drop a server tool.** Dropping it lets the model answer
  "from the web" out of its own memory, which is worse than an error. Only
  `web_search*` is supported; anything else fails by name.
- **Web search is answered by Codex's own search**, enabled per-thread via
  `config.web_search`. The app-server's variants are
  `disabled | cached | indexed | live` (verified against a live app-server —
  `enabled`/`auto` are rejected); the bridge asks for `live` and keeps
  `disabled` for every other turn.
- **Codex reports it as a `webSearch` thread item**, which must pass
  `rejectCodexOwnedItem` *only* on turns that asked for it. It stays forbidden
  otherwise, so the guard still catches a genuine capability leak.
- **Domain filters bind in the instructions or nowhere.** `config.web_search`
  accepts only the mode string (an object with `allowed_domains` is rejected),
  so Claude Code's `allowed_domains`/`blocked_domains` are stated to the model
  that runs the search. Dropping them would answer a scoped search with
  unscoped results — which looks like a working search.
- **Prose carries the findings; protocol blocks carry the accounting.** Claude
  Code accepts text commentary as a WebSearch result, but it computes the
  displayed search count from `server_tool_use` / `web_search_tool_result`
  blocks. Reducing Codex's `webSearch` items to prose alone made every successful
  search report `Did 0 searches`. Preserve each item as a paired server-use/result
  block, retain the prose with its inline source URLs, and report
  `usage.server_tool_use.web_search_requests`. A real app-server
  `item/started` can have an empty query while `item/completed` supplies it, so
  emit the pair on completion; if completion still has no display detail (for
  example an `other` action), use a neutral fallback rather than rejecting the
  valid item. Accept these emitted blocks when Claude
  replays assistant history, then omit their lifecycle metadata when injecting
  Codex history because the adjacent prose carries the findings.

Guarded by `TestTranslateAcceptsAnthropicWebSearchServerTool` and its
neighbours in `translate_test.go`, `TestEngineEnablesCodexWebSearchOnlyWhenRequested`,
`TestEngineAcceptsWebSearchItemOnlyOnWebSearchTurns`, and
`TestEngineReportsCodexWebSearchAsAnthropicServerToolUse` in `engine_test.go`,
`TestResponseReducerReportsWebSearchWithEmptyDisplayQuery` in `stream_test.go`,
`TestTranslateReplaysBridgeWebSearchResponse` in `translate_test.go`, plus
`TestHandlerAcceptsClaudeCodeWebSearchRequest`, which replays Claude Code's
exact request body end to end.

### `TaskOutput` is withheld from Codex, because its id dies before the model sees it

A background task's id resolves **only** against Claude Code's in-memory
`appState.tasks`. A task that is terminal and already notified is deleted from
that map by a 1s sweeper: a workflow 30s after it completes (`evictAfter =
endTime + Dye`, `Dye = 30000`, a hardcoded literal with no env override — unlike
`TASK_MAX_OUTPUT_LENGTH`), a background shell immediately. `TaskOutput` has no
disk fallback.

The trap is timing, not the tool. The `<task-notification>` carrying the id is
queued and only delivered when the **current turn ends**, so any turn longer
than the grace window hands the model an id that is already dead. Bridged turns
routinely run minutes: one shipped case completed at 22:36:17Z, was delivered at
22:39:29Z, and failed at 22:39:43Z — reaped three minutes earlier. The trailing
"`. Running background agents: …`" is a hint appended to *every* not-found
message; those agents are unrelated to the id asked for, which is why it reads
as "no task found, yet an agent is clearly running".

Nothing is ever lost — only the handle. The `.output` file survives and is the
path both the launch result and the notification already hand over.

- **Withholding is the lever; guidance is not.** Claude Code already ships
  "DEPRECATED … prefer Read" *in the tool's own description*, and the bridged
  model called it anyway — ~12 times a session against 0.06 for a native pane,
  failing ~12% of the time (100 failures over 41 sessions). Do not downgrade
  `withholdUnreliableTools` back into an instruction; that experiment has run.
- **Yield to `tool_choice`.** A named choice keeps the tool, and `any` keeps it
  when nothing else could satisfy the turn, so withholding can never convert a
  host-forced call into a 400.
- **History replay is unaffected.** Past `tool_use` blocks are injected under
  `block.Name`, which already names undeclared functions every turn for renamed
  MCP tools (`mcp__x` in history vs `wisp_mcp__x` declared).
- **Native panes keep the tool.** wisp-deck is not in their path, and settings
  `disallowedTools` denies at the permission layer rather than removing the tool
  from the model, so applying it there trades one error for another.

Guarded by `TestTranslateWithholdsTaskOutputFromCodex`,
`TestTranslateKeepsTaskOutputWhenToolChoiceForcesIt`, and
`TestTranslateToolChoiceAnySurvivesWithheldTaskOutput` in `translate_test.go`,
plus `TestBaseInstructionsPointAtTheTaskOutputFile` in `engine_test.go`.

### A new thread is an uncached thread

Codex scopes the prompt cache to the thread: `prompt_cache_key` is the session
id, and the ChatGPT backend picks the cache shard from it. Measured on a live
app-server, a byte-identical 37K-token history got 99.7% cached as the next
turn of its own thread, and 0% in a new thread. Every `thread/start` that
replays history is therefore a full-price replay.

Three things follow:

- **Claude Code's subagent progress summary is answered locally**
  (`agentsummary.go`). It forks every running subagent every 30s; through the
  bridge each fork replayed ~125K tokens for 3-5 words, 5,531 times in 30
  hours. `WISP_DECK_LIVE_AGENT_SUMMARY_E2E` checks the prompt after a claude
  upgrade.
- **A finished thread is parked** (`parking.go`) and the next message that
  extends it exactly runs as a new turn on it. Anything that differs starts a
  new thread, so correctness never depends on the pool.
  Claude Code sends skill bodies, attachments and queued messages as text
  beside a tool result. Codex gets that text inside the tool output, and the
  next request shows it as its own user message. The thread keeps its digest
  (`supplement`) so it still matches. Clearing the fingerprint instead left
  every subagent and every skill turn unparkable.
  Inline `role: "system"` messages (task notifications, hook context) join
  the user message before them (`anthropic.go`). Folded into System, each
  notification changed the fingerprint and orphaned every parked thread, and
  a running thread never saw the notification at all.
  A claude.ai MCP server that connects after the first request adds tools and
  instructions, so the first thread cannot match the next message. Codex fixes
  both at `thread/start`; that one cold replay per session is expected.
- **`ColdStartFuse` counts replays** and warns past 2M tokens in 10 minutes,
  through a log line in `gptbridge-cold.log` and one fixed notification an
  hour. A new background fork shows up there instead of in the quota.

### A conversation's transport size is unbounded, and its token count says nothing about it

A request that opens a turn replays the whole conversation into a fresh
ephemeral Codex thread through `thread/inject_items`. (A tool continuation does
not — it resumes the thread that owns the call; see the compaction section
below, which is what that distinction cost.) That message
was built in one piece, and its size is **not** governed by the context guard,
because the guard deliberately prices an image at a flat
`promptGuardImageTokens` (~1600) and never counts its base64 bytes — right for
the model, and blind to the wire. The two measures drift apart without limit.

A real session wedged at exactly that gap: **82 images, 16,766,904 base64 bytes**
— ~131K tokens against a 272K window, so the guard passed it — and the inject
message crossed `defaultRPCMaxMessageBytes` (16 MiB). Three things then made one
oversized message a *permanently dead session*:

- **The refusal is deterministic**, and every later turn resends the same
  history, which only grows.
- **`Call` treated the local size refusal as a connection failure.** Nothing had
  been written, yet `c.fail` tore down a healthy app-server, so each retry also
  paid a full restart. `encodeMessage` (refusal, connection untouched) and
  `sendPayload` (real write, may `fail`) are now separate for that reason.
- **It surfaced as a retryable 502**, so Claude Code burned all ten retries and
  then wedged. It is now `isRefusedOversizedMessage` → 400 prompt-too-long, the
  one shape Claude answers by compacting — the only thing that can actually
  shrink the payload.

So `injectHistory` splits the history across as many calls as
`rpc.MaxMessageBytes()` requires. That is safe because **repeated
`thread/inject_items` calls append to the thread in order** — verified against a
live app-server by `TestLiveInjectItemsAppends` (env-gated; re-run it after a
codex upgrade, because a Codex change to replace-instead-of-append would
silently drop every chunk but the last). Rules that fell out of it:

- **Never rebuild "the whole history in one `Call`".** Chunk size comes from the
  transport's own cap, not a constant of the engine's own, so the two cannot
  drift.
- **One item can't be split**, so an item over the budget is the conversation's
  shape rather than a transport hiccup: it returns `oversizedMessageError`,
  which reaches Claude as prompt-too-long instead of a retry loop.
- **Raising the cap is not the fix.** Whatever the number, a long image-heavy
  session eventually crosses it; only splitting removes the ceiling.

Guarded by `TestATransportCarriesEveryConversationTheContextGuardAdmits` (the
invariant itself, at the real 16 MiB cap through a real `RPCClient`),
`TestEngineInjectsHistoryTooLargeForOneAppServerMessage`,
`TestEngineRejectsASingleHistoryItemLargerThanTheCap`,
`TestRPCRefusedWriteLeavesTheConnectionUsable`, and
`TestOversizedAppServerMessageBecomesPromptTooLong400`.

### The send budget is ours; the app-server's message sizes are not

All of the above is the **write** direction. The read direction had the same
number wired into it — `readLoop` sized its scanner with `MaxMessageBytes` — and
that conflation is a different bug with a worse blast radius. The bridge decides
how large its own messages may be; it gets no vote on how large a notification
Codex sends, and a generated image's base64 or a sub-agent's activity blob is
whatever size Codex makes it.

One long inbound line therefore killed the connection — and **that connection is
shared by every concurrent turn** (one app-server, `e.turns` keyed by thread), so
Claude Code's workflows and background agents all died together. It surfaced as
`502 inject Claude history: app-server message exceeds 16777216 bytes`, blaming
whichever call happened to be in flight rather than the notification that
actually overflowed, and telling the user to retry something deterministic.

- **`MaxInboundBytes` is a separate limit from `MaxMessageBytes`**, and far
  larger (`defaultRPCMaxInboundBytes`, 256 MiB). Never re-derive one from the
  other; that equality *is* the bug.
- **Passing the ceiling costs the message, not the connection.** `readLoop`
  discards the line, counts it (`overlongMessages`), and stays framed on the
  next newline. Killing the connection turns one unreadable notification into a
  session-wide outage; the ceiling is a memory backstop, not a kill switch.
- The read side has no test that an oversized inbound message closes the
  client — that was the behavior, and it was the defect.

Guarded by `TestRPCAcceptsAnAppServerMessageLargerThanTheSendBudget` and
`TestRPCSurvivesAnAppServerMessageOverTheInboundCeiling`.

### `ThreadItem` is a vendor-controlled open enum, so the guard denies by name

`rejectCodexOwnedItem` aborts a turn when Codex reports doing something on this
machine. It used to work the other way round: allow six known-good item types,
abort on everything else. That default made **every Codex release a potential
outage** — the enum already carries 18 variants in 0.146.0 and grows freely, so
each new one became a fatal `502 forbidden Codex-owned tool item` the moment a
user's Codex updated. `contextCompaction` shipped that way, then
`imageGeneration`, then `collabAgentToolCall` and `subAgentActivity` — the last
two ending single turns of 4h20m, 4h48m and 1h17m with "try again in a moment".

An unrecognized item is not evidence of a capability leak; it is evidence that
the vocabulary grew. So the guard names what it forbids
(`codexHostCapabilityItems`: `commandExecution`, `fileChange`, `mcpToolCall`,
`imageView`, `hookPrompt`, plus `webSearch` on turns that never asked for it) and
tolerates everything else, known or not.

- **The real enforcement is the thread config**, not this guard: read-only
  sandbox, no network, `approvalPolicy: "never"`, no MCP servers, a private
  throwaway cwd, shell/unified_exec/hooks/apps off. The guard is the tripwire
  that says the enforcement failed, and those names are the stable part of the
  enum — a shell escape surfaces as `commandExecution` however else Codex grows.
- **Never restore a default-deny branch.** `TestEngineToleratesCodexItemTypesIt
  DoesNotHostItself` asserts the property with an item type that does not exist,
  so it fails on reintroduction rather than on the next specific variant.
- **The reducer already no-ops on item types it does not model**
  (`applyWebSearchItem` returns early), so tolerating an item costs nothing.
- Codex gates collaboration sub-agents behind **four** flags, and `multi_agent`
  alone stopped covering it: `multi_agent_v2`, `enable_fanout` and
  `collaboration_modes` arrived later. The bridge surfaces none of a sub-agent's
  work to Claude, so every token one spends is lost — declare all four rather
  than inherit a new Codex's defaults.

Guarded by `TestEngineToleratesCodexItemTypesItDoesNotHostItself`,
`TestEngineRejectsCodexItemsThatReachThisMachine`, and
`TestEngineDisablesEveryCodexCollaborationFeature`.

### The sandbox governs Codex's tools; saying so to the model breaks Claude's

That read-only sandbox is enforcement the bridge needs, but Codex also **renders
it into the model's prompt**, and the model has no way to know it does not
describe the session it is actually working in:

```
`sandbox_mode` is `read-only`: The sandbox only permits reading files.
Approval policy is currently never. Do not provide the `sandbox_permissions`
for any reason, commands will be rejected.
```

The host's Edit/Write/Bash are Claude Code's tools, not Codex's, and obey
Claude's permission mode. A model reading those two sentences as session-wide
refuses them outright. This shipped: a GPT pane reported *"the repository is
mounted read-only, escalation is disabled … a write-enabled session is
required"* and abandoned four confirmed fixes — while its Claude session sat in
`bypassPermissions` for all 94 recorded `permission-mode` events, with no
transitions. It reads as wisp-deck overriding the user's permission mode, and
it is not: nothing in the launch chain passes `--permission-mode`, and the
launch overlay writes no `permissions` key.

- **Suppress the prose, never the sandbox.**
  `include_permissions_instructions: false` and
  `include_environment_context: false` on the thread config are **prompt
  assembly only** — `sandbox`, `sandboxPolicy`, the `features` map and
  `rejectCodexOwnedItem` are what actually keep Codex off this machine, and
  none of them move. Relaxing the sandbox to make the model feel write-enabled
  would hand it the machine.
- **The environment context is the same misdirection about the cwd.** It
  advertises the private throwaway directory, so a model told to work in the
  user's project is also told it is somewhere else entirely.
- **Codex ignores unrecognized config keys silently**, so a rename would bring
  the prose back with no error and a green unit test. That is why the guard is
  a live one, and why `baseInstructions` also states the fact positively
  ("Codex's own sandbox … governs Codex's tools alone … never refuse or defer
  work on the grounds that the session is read-only"). Belt and braces: the
  instruction is what survives Codex dropping the keys.

Guarded by `TestEngineSuppressesCodexSandboxPromptText` (which also pins the
sandbox and approval policy that must NOT change),
`TestBaseInstructionsSayTheHostToolsWriteForReal`, and
`TestLiveCodexSandboxProseIsSuppressed` — the only check that can see Codex's
side (run it after a codex upgrade; see Commands).

### A generated image is delivered as a path, never as a block

Codex generates images on its own servers, but the app-server **writes every one
to a file** under `$CODEX_HOME/generated_images/<threadId>/<itemId>.png` and
reports the absolute location as `savedPath` on the `imageGeneration` item.
`item/completed` carries `savedPath`, `revisedPrompt`, `status`, and `result` —
the last being the same picture again as ~2MB of base64.

`savedPath` is the deliverable and the item is the only place it is ever
announced. Parsing the item without it shipped as *"[Codex generated an image,
but this bridge cannot return images to Claude Code, so it was discarded]"* —
printed while the finished picture sat on disk, unreferenced. The Messages API
having no assistant-role image block is true and beside the point: the path
travels as text, Claude Code reads the file from it, and a `Read` tool_result
carries the picture back to Codex as `inputImage`, which is what makes
frame-to-frame editing work at all.

- **Never drop `savedPath`,** and never re-word the notice into a claim that the
  image is gone. If it is ever absent, state *that* — report a non-`completed`
  `status` as the failure it is, and never fall back to "discarded".
- **Keep `result` out of the transcript.** It duplicates the file at megabytes
  per image.
- **The path outlives the turn.** Bridge threads are `ephemeral`, so the
  `thread/delete` in `cleanupTurn` answers `-32600 thread is not persisted and
  cannot be deleted` and the file is untouched.
- **The instructions permit image generation.** It has no client-side off
  switch, so the old "never use any Codex-owned … image … tool" line bought
  nothing and cost consistency: the model either obeyed by answering an image
  request with SVG or ignored it and drew anyway — the same prompt behaving
  differently run to run. Do not restore it; the shell/filesystem/MCP
  prohibitions (the ones that would touch this machine) stay.

Guarded by `TestResponseReducerReportsCodexImageSavedPath`,
`TestEngineToleratesCodexImageGenerationItem`, and
`TestBaseInstructionsAllowCodexImageGeneration` — plus
`TestLiveImageEndToEnd`, which is the only check that can catch Codex changing
its side (run it after a codex upgrade; see Commands).

### A compaction rewrites the conversation, so the Codex thread must be rebuilt

Claude Code's reactive autocompact drops the summarized head and **preserves the
trailing group verbatim** — one assistant message plus the `tool_result`s that
follow it. It fires mid-tool-loop almost every time, because a large tool result
is what crosses the threshold, so the request after a compaction is a **tool
continuation**, not a new user turn.

`Execute` routes anything carrying tool results to `resume`, which answers the
app-server's pending request on the thread that owns the call and re-injects
nothing. The summarization request itself does **not** consume that pending
turn — it carries no tool results (`TestTranslateClaudeCompactionCarriesNoToolResults`)
and so goes to `start` on a thread of its own, which is precisely why the
pre-compaction owner is still sitting in `toolIndex` afterwards, waiting to be
resumed. It is also the likeliest explanation for the intermittency: a boundary
drops cleanly when that owner had already gone (an app-server rebuild, the TTL,
or a `/clear`). That thread still holds every message the compaction just dropped. So a
compacted session kept running on its pre-compaction Codex thread: the model was
fed the context Claude Code had thrown away, and Codex reported that thread's
real size back through `thread/tokenUsage/updated`, which `ResponseReducer`
forwards as `input_tokens` + `cache_read_input_tokens`.

Claude Code sums those as the context used, so it compacted again on the next
turn. Measured across three sessions on three days — the transcript's own
`compactMetadata.postTokens` against the very next assistant turn's reported
usage:

| session | `postTokens` | reported one turn later |
|---|---:|---:|
| `093fd228` (09-04, gpt-5.6-sol) | 45,755 | 241,085 |
| `2088e35f` (09-06, gpt-6-astra) | 23,135 | 241,010 |
| `62943587` (09-07, gpt-6-astra) | 43,207 | 243,354 |

Three of those in a row trips Claude Code's rapid-refill breaker
(`jko`/`HIe`/`ist` in the bundle, threshold 3): *"Autocompact is thrashing …"*.
That is **not a warning** — it is built by `Co({content, error:"invalid_request"})`
as an `isApiErrorMessage` assistant message, the turn returns
`{reason:"rapid_refill_breaker"}` **without compacting**, every queued command is
marked cancelled, and an active `/goal` is cleared as "context limit reached".
The message's own advice ("a file being read or a tool output is likely too
large") is wrong here: the largest `tool_result` in any tripping session was
41,410 chars.

- **The signal is the history, never a string.** `engineTurn.history` digests the
  items the thread was built from, and `adoptHistory` requires the incoming
  history to still begin with all of them. A tool continuation only ever
  appends; a compaction replaces the head with the summary, so it cannot extend.
  Matching Claude Code's summary preamble instead would rot exactly the way the
  compaction-effort prefix match already did.
- **The fingerprint is refreshed on every resume.** A turn that opened at the
  start of a conversation was built from an *empty* history, which every later
  history trivially extends — so comparing against the turn's original history
  alone misses a compaction in precisely the longest turns.
- **A diverged thread is rebuilt, not resumed.** The mismatch routes through the
  same `recoverContinuationFromHistory` → `start` path an expired continuation
  already uses, and `cleanupContinuationOwners` interrupts and deletes the stale
  thread rather than leaking it for the 24h TTL.
- **Rebuild-impossible must fail, not fall back.** If the tool result has no
  `function_call` to anchor to, resuming would answer from a conversation the
  client no longer has; it returns `invalidContinuationError` instead, the class
  the client already knows not to retry.
- **Do not "fix" this by reverting the usage report.** Before `87608a5`
  (2026-09-01) `message_delta` carried output tokens only, so Claude Code kept
  the bridge's own byte estimate — which shrinks on compaction, so nothing
  thrashed. That commit is correct: it exposed a defect that had been silently
  feeding the model a stale thread all along. Reverting it restores the silence,
  not the fix.

Guarded by `TestEngineRebuildsTheThreadWhenClaudeCompactsMidToolCall`,
`TestEngineResumesWhenTheHistoryStillExtendsTheThread` (the counterweight — an
ordinary continuation must still resume, or every tool round re-injects the
conversation), and `TestEngineRefusesAStaleThreadItCannotRebuild`.

Not live-verified against a real app-server: reproducing a bridged compaction
costs Codex quota, which is gated until 2026-09-11. The evidence is the recorded
transcripts above, the engine code, and the fake-RPC tests.

### A checklist note that names no status is a silent no-op, so the bridge refuses it

This is what "GPT never finishes its checklists" actually is. `TaskUpdate`
requires only `taskId`; every other field is optional, and Claude Code builds
its acknowledgement as `Updated task #5 description`, reporting a
`statusChange` **only when the call supplied a status**:

```js
return {data:{success:!0,taskId:e,updatedFields:ue,
  statusChange: de.status!==void 0 ? {from:re.status,to:de.status} : void 0}}
…
let E=`Updated task #${o} ${d.join(", ")}`;
```

So a call carrying a note and no status rewrites the task's text, changes no
state, and answers with a success that never mentions the status still
standing. The model reads its own completion report back as confirmation and
never returns.

The bridged model writes exactly that shape. Measured across this machine's
transcripts:

| | TaskUpdate calls | carrying no status | note-only |
|---|---:|---:|---:|
| bridged GPT | 2284 | 1005 (44.0%) | 402 + 244 metadata |
| native Claude | 333 | 36 (10.8%) | **0** |

Claude's 36 are 35 `addBlockedBy` plus a single subject/description rewrite —
wiring and edits that change no status by design. The note-only shape is the
bridge's alone, and its content is unambiguous: *"Implementation and scoped verification finished"*,
*"87 new unit tests passed"*, *"Approved picker implementation is locally
implemented and scoped-verified"*.

Session `62943587` is the shipped case. Records 3853 and 3855 closed tasks #8
and #7 with `status: "completed"`; record 3857 — the next action and the last
checklist write of the session — carried the same kind of report for #5 with no
status, and the pane was left reading `8 tasks (7 done, 1 in progress, 0 open)`
with the parent task stranded. It explains 7 of the 19 unfinished bridged
checklists on this machine, and every one where the model had plainly done the
work.

- **The refusal happens before registration**, in `acceptDynamicTool`, so an
  answered call leaves no `pending` entry and no `toolIndex` row to reap. It is
  returned as `refusedDynamicToolError` and answered with the same
  `success:false` + `contentItems` shape `resume` uses for a real tool result,
  so Codex reads it and keeps generating inside the same turn. Claude never sees
  the call.
- **This is a mechanism, not a reminder.** `7809169` already spent the
  instruction card ("never claim completion while actionable checklist items
  remain") and the shape kept shipping — the same lesson as `TaskOutput`, whose
  own description says DEPRECATED and was called ~12 times a session anyway.
  Leave that instruction alone; it covers a different failure.
- **The refusal names the status the call left standing rather than demanding a
  new one.** A model forced to invent a status would reopen finished work,
  which is worse than the note it replaced.
- **Only a note is refused.** `addBlockedBy`, `addBlocks`, `owner`, `subject`
  and `activeForm` legitimately carry no status; refusing those would break
  ordinary bookkeeping that native panes do too.
- **The batch is never split.** All 744 defect-shaped calls on this machine
  arrived alone in their message, so refusing one inline never strands an
  accepted sibling waiting on the batch timer.
- **There is deliberately no retry bound.** A refusal is an ordinary
  `success:false` tool result, and the bridged model adapts to those: across
  8838 tool errors it re-sent the same tool with changed input 4466 times and
  repeated the identical input 43 times (0.96%). Capping the refusals would only
  re-admit the shape after N tries, which is the bug.

The other 12 of the 19 are mostly not this bug: in **11** the user sent a new
prompt after the last checklist touch, so the stale item reflects a conversation
that moved on rather than a completion claim. **One** (`6c647f80`) is genuinely
abandoned with no note and no redirect, and has no root cause yet — do not fix
it blind. Featherless panes write the same note-only shape and sit behind
`rolefix`, not this bridge.

Not live-verified against a real app-server: Codex quota is gated until
2026-09-11. The refusal answers a server-initiated request mid-turn, which is
shape-identical to what `resume` already does for a real tool result, and the
fake-RPC tests cover it. Re-run a bridged checklist turn after that date and
confirm Codex continues the turn after the refusal rather than stalling.

Guarded by `internal/gptbridge/checklist_test.go`, including
`TestEngineRefusesTheChecklistNoteThatStrandedTaskFive`, which replays record
3857 verbatim and checks that its well-formed sibling from the same breath still
goes through.
