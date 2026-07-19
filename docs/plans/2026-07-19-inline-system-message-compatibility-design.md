# Claude Inline System Message Compatibility

## Problem

Claude Code 2.1.214 can append SessionStart-hook context to an Anthropic
Messages request as an entry in `messages` with role `system`. A captured
request used this order:

1. a `user` message containing the current prompt and system reminders;
2. a text-only `system` message containing SessionStart-hook context.

The ChatGPT subscription bridge currently accepts system instructions only in
the top-level Anthropic `system` field. Its parser therefore rejects the live
request with:

```text
messages[1]: unsupported role "system"
```

The rejection happens inside Wisp Deck before a Codex thread or turn starts.

After normalizing that request, installed non-bare verification exposed a
second Claude 2.1.214 shape. When Claude invokes its `Skill` host tool, the
continuation contains one `tool_result` block followed by an ordinary text
block containing the loaded skill:

```text
user:
  tool_result: "Launching skill: superpowers:using-superpowers"
  text: "Base directory for this skill: …"
```

The bridge currently rejects that request with:

```text
final tool_result message cannot contain ordinary user content
```

## Decision

Normalize text-only inline system messages into the request's existing system
instruction collection during Anthropic request parsing. Do not emit inline
system messages into Codex conversation history.

Top-level system blocks remain first. Inline system blocks are appended in
their original message order. The existing translation then joins all system
blocks and passes them through `developerInstructions` on `thread/start`.

After normalization, only `user` and `assistant` messages remain in the
conversation transcript. The captured `user → system` request therefore
produces a normal final user turn while retaining the SessionStart-hook
instructions at the correct developer-instruction priority.

For a final continuation containing exactly one `tool_result`, merge any
supplemental user text or image blocks into that result's content before
translating it to Codex dynamic-tool `contentItems`. This preserves Claude's
loaded Skill text through the only continuation channel Codex exposes while a
turn is paused for a dynamic-tool response.

If ordinary content accompanies multiple tool results, reject the request as
ambiguous rather than guessing which tool output owns it. Tool-result-only
continuations and ordinary final user turns retain their existing behavior.

## Alternatives

### Convert inline system messages to user messages

This would satisfy Codex history validation, but it would demote trusted
Claude-host context to user priority and could make system instructions appear
as user conversation content.

### Drop inline system messages

This would unblock the request but silently discard SessionStart hooks and
other host-supplied instructions.

### Reject inline system messages

This is the current behavior and is incompatible with current Claude Code.

### Drop supplemental continuation text

This would allow the turn to continue but silently discard the skill body
Claude loaded for the model.

### Inject a new user history item into the active Codex turn

Codex app-server does not provide a supported extra-user-input operation while
the turn is paused for a dynamic-tool response. Injecting history concurrently
with the active turn would rely on undefined ordering.

### Attach supplemental content to every parallel tool result

This would duplicate instructions and falsely attribute the same content to
unrelated tools. Ambiguous multi-result continuations remain rejected.

## Validation and Errors

Inline system messages may contain a string or an array of text blocks, matching
the top-level system representation already supported by the bridge. Any
non-text inline system block is rejected with a message-indexed validation
error. A request containing only system messages remains invalid because no
model input exists.

Supplemental content on a single-result continuation uses the existing
tool-result content validation: text and images are accepted, and other block
types remain invalid. A mixed continuation with more than one tool result
returns an explicit ambiguity error.

The loopback bridge remains authenticated with its private per-process key, so
normalizing this role does not expand access beyond the Claude child already
trusted to construct the Anthropic request.

## Testing

Add a parser-and-translation regression using the captured ordering:

- top-level system text;
- a final user message;
- an inline text-only system message.

The test must fail under the current parser, then prove that:

- both system sources reach `Translation.System` in deterministic order;
- inline system content is absent from Codex history;
- the user content remains the turn input.

Add validation coverage for a non-text inline system block and a system-only
message list. Run the complete `internal/gptbridge` tests, race tests, and the
opt-in live GPT-through-Claude end-to-end test.

Add a translation regression using the captured Skill continuation and prove
that:

- the original tool output remains the first Codex content item;
- the loaded skill text becomes a second content item on the same result;
- no separate user input is created.

Add an ambiguity regression for supplemental content accompanying two tool
results. Finally, run a non-bare installed Claude prompt with default tools so
Claude loads `superpowers:using-superpowers` and completes through the bridge.
