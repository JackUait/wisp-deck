# GPT Compaction Token Accounting Design

## Problem

Claude Code decides when a transcript is safe to continue or compact from the
Anthropic-compatible token counts returned by Wisp Deck. The GPT bridge
currently derives those counts from serialized request JSON:

- `/v1/messages/count_tokens` divides the raw HTTP body size by three.
- streamed `message_start` usage re-marshals the translated request and divides
  that JSON size by four.

JSON escaping is transport representation, not model input. Code-heavy history
contains enough quotes and backslashes for both estimates to substantially
overstate GPT's actual prompt size. In the observed failure Claude Code was
told the transcript occupied 351,736 tokens and rejected the next turn as
`Prompt is too long`; Codex measured the successful retry at 202,912 total
tokens inside its 258,400-token window.

## Decision

Use one semantic prompt estimate everywhere Claude Code observes or Wisp Deck
checks input size. Compute it from the normalized `MessagesRequest`, counting
the actual text, tool names, tool inputs, and schemas once, plus the existing
flat image allowance. Store that estimate on `Translation` and reuse it for:

1. the real-model-window overflow guard;
2. `/v1/messages/count_tokens`;
3. streamed `message_start` input usage.

This fixes the accounting mismatch without recognizing compaction prompt text,
lowering GPT reasoning effort, capping summaries, or changing normal tool-turn
behavior. Codex app-server does not expose a pre-generation exact token count,
so the estimate remains approximate, but it no longer counts JSON escaping and
all bridge boundaries agree on the same value.

## Data Flow

`ParseMessagesRequest` normalizes the Anthropic payload. `estimatePromptTokens`
walks that normalized structure. `TranslateRequest` records the result as
`Translation.EstimatedInputTokens`. The HTTP handler uses the recorded value
for its window guard and count-token response, while the engine passes it to
the response reducer for `message_start` usage.

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
test will then verify that corrected usage permits the next turn where the old
inflated usage causes a local `Prompt is too long` rejection.
