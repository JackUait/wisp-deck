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

## Validation and Errors

Inline system messages may contain a string or an array of text blocks, matching
the top-level system representation already supported by the bridge. Any
non-text inline system block is rejected with a message-indexed validation
error. A request containing only system messages remains invalid because no
model input exists.

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
