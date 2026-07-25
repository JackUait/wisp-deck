# GPT Compaction Continuation Design

## Problem

Claude Code may append ordinary user text to a final user message that also contains a pending `tool_result`. The GPT bridge currently folds every ordinary block into the adjacent tool result. When the suspended Codex turn cannot be resumed and the bridge recovers from history, it replays that folded content as `function_call_output` and replaces the model input with its generic continuation notice.

Claude Code's reactive-compaction instruction is one such ordinary block. It therefore becomes hidden tool output instead of the fresh instruction to produce a plain-text summary. GPT resumes the prior task and may call another tool; Claude Code then reports `summarization produced empty response` because the compaction response contains no assistant text.

## Design

Preserve the two semantic channels of a mixed final user message:

1. The original `tool_result` content remains the response to the pending tool call.
2. Ordinary text and images remain supplemental model input in their original order.

Normal live continuation still returns each tool result through the app-server request that owns it. Supplemental content is included with that response so Skill injections, reminders, and user text continue to reach the suspended turn.

If the live continuation cannot be resumed and the bridge reconstructs the turn from history, recovery must:

- replay only the original tool-result payload as `function_call_output`;
- send the generic no-repeat recovery notice as fresh input;
- append the preserved supplemental blocks as fresh input.

This makes Claude Code's compaction instruction authoritative fresh model input without recognizing or depending on its wording. The same mechanism correctly preserves future mixed-message instructions.

## Data Model

`TranslatedToolResult` keeps two representations:

- `ContentItems`: live response content, including supplemental blocks assigned to that result;
- `HistoryContentItems`: original tool-result content only, used when reconstructing `function_call_output` history.

`Translation` keeps the ordinary final-message blocks as supplemental `Input` in source order. A final message containing tool results therefore may populate both `ToolResults` and `Input`; this is intentional rather than ambiguous.

## Recovery Flow

1. Translate and validate every final `tool_result` against its preceding `tool_use`.
2. Preserve each tool result's original content for history replay.
3. Continue assigning ordinary blocks to adjacent live results, retaining current continuation behavior.
4. Also translate ordinary blocks into supplemental `Translation.Input` in original order.
5. On recovery, append original tool-result outputs to history and construct fresh input as:
   - bridge recovery notice;
   - preserved supplemental input.
6. Start the fresh Codex thread with that input.

## Error Handling

Unsupported supplemental blocks remain translation errors. Missing or mismatched tool-use IDs remain invalid continuations. The change does not weaken the tool ownership or continuation validation boundaries.

If no supplemental input exists, recovery behavior remains unchanged.

## Tests

Follow TDD with a regression test that reproduces the shipped failure:

- history ends with an assistant tool call;
- the final user message contains its tool result plus a compaction-style plain-text instruction;
- the continuation owner is unavailable, forcing history recovery.

The test must fail before implementation and then prove that:

- recovered history contains only the original tool output;
- recovered `turn/start` input contains both the bridge notice and compaction instruction;
- the instruction is not embedded in `function_call_output`;
- a plain-text model completion is returned.

Existing tests for Skill continuations, arbitrary block interleavings, exact live responses, and replay recovery must remain green. Run the new test first, then the changed-package tests, changed-file lint/format checks, the full suite, and the repository completion checklist before push.
