# GPT Compaction Token Accounting Design

## Problems

The failing session crossed two independent context-window boundaries.

First, it had grown to 351,736 tokens under native Claude Opus before switching
to GPT. Claude Code 2.1.223 began keeping unrecognized model IDs inside an
assumed context window. On resume it rejected that inherited session locally as
`Prompt is too long`, before sending `/v1/messages` to Wisp Deck. The bridge
therefore had no opportunity to return its model-aware overflow error and
trigger compaction.

Second, once requests reach the bridge, Claude Code decides when a transcript
is safe to continue or compact from the Anthropic-compatible token counts Wisp
Deck returns. The GPT bridge derived those counts from serialized request JSON:

- `/v1/messages/count_tokens` divides the raw HTTP body size by three.
- streamed `message_start` usage re-marshals the translated request and divides
  that JSON size by four.

JSON escaping is transport representation, not model input. Code-heavy history
contains enough quotes and backslashes for both estimates to substantially
overstate GPT's actual prompt size. Codex measured a successful compaction
retry at 202,912 total tokens inside its 258,400-token window.

## Decision

Make Wisp Deck the single owner of GPT context-window admission. The adapter
sets Claude Code's documented
`CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1` override only for GPT
bridge children. Oversized inherited sessions then reach Wisp Deck, whose
existing model-aware guard uses Codex's advertised context window and returns
the Anthropic-compatible overflow error that Claude Code uses to compact.

Within that bridge, use one semantic prompt estimate everywhere Claude Code
observes or Wisp Deck checks input size. Compute it from the normalized
`MessagesRequest`, counting the actual text, tool names, tool inputs, and
schemas once, plus the existing flat image allowance. Store that estimate on
`Translation` and reuse it for:

1. the real-model-window overflow guard;
2. `/v1/messages/count_tokens`;
3. streamed `message_start` input usage.

This fixes both ownership and accounting without recognizing compaction prompt
text, lowering GPT reasoning effort, capping summaries, or changing normal
tool-turn behavior. Codex app-server does not expose a pre-generation exact
token count, so the estimate remains approximate, but it no longer counts JSON
escaping and all bridge boundaries agree on the same value.

## Data Flow

`BuildClaudeEnvironment` pins the documented unknown-model enforcement opt-out
when it launches Claude behind the GPT adapter. `ParseMessagesRequest` then
normalizes every Anthropic payload that reaches the bridge.
`estimatePromptTokens` walks that normalized structure. `TranslateRequest`
records the result as `Translation.EstimatedInputTokens`. The HTTP handler uses
the recorded value for its window guard and count-token response, while the
engine passes it to the response reducer for `message_start` usage.

The estimator returns at least one token for every valid request. Images retain
their flat estimate so base64 transport bytes never dominate accounting.

## Verification

Regression coverage will use quote- and backslash-heavy text where serialized
JSON is much larger than semantic content. Tests must first demonstrate that:

- `/count_tokens` overcounts the normalized prompt;
- translation does not carry the semantic estimate;
- the engine reports a separately re-marshaled estimate instead of the
  translation's estimate.

After implementation, focused tests must prove all three boundaries return the
same semantic count. Existing overflow, translation, streaming, continuation,
and compaction regressions must remain green. A controlled Claude Code endpoint
test must also prove that an inherited 190K-token unknown-model session is
rejected before `/v1/messages` without the adapter override and reaches the
endpoint with it.
