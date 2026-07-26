# GPT Checklist Completion Design

**Date:** 2026-07-26

## Goal

Ensure GPT-backed Claude Code sessions continue through long-running host work and finish actionable checklist items instead of becoming idle with tasks still in progress.

## Root Cause

The GPT bridge replaces Codex app-server's default base instructions with a minimal Claude Code identity and tool-boundary prompt. That prompt says which tools are available but omits the persistence and long-running-command guidance needed by Claude Code's host lifecycle.

The failure is visible in session `5702e2ef-8601-48b3-88bd-666aa7601b0a`:

1. GPT created Task #10 and marked it in progress.
2. GPT launched `./run-tests.sh` as a foreground Bash call with a 600-second tool timeout.
3. Go's default test timeout also fired at 10 minutes.
4. Claude Code moved the command to background tracking only as the child exited.
5. No later task notification was recorded.
6. GPT emitted `end_turn` while Task #10 remained in progress.

The bridge correctly translated the Bash tool result and Codex's deliberate completed turn. The defect is not a hidden app-server failure or an incorrect Anthropic stop reason.

A working comparison in session `6ec5c654-0da7-4241-9ec0-62c7415484cb` launched the same full suite with `run_in_background: true` from the outset. Claude Code registered the task before execution and reliably injected completion notifications, including runs that crossed the 10-minute boundary.

## Design

Extend `baseInstructions` with two provider-scoped rules:

- Continue until the user's request is fully resolved; do not end with actionable checklist items still pending.
- Start host commands expected to approach a tool timeout in background mode from the outset so completion is registered and can resume the session.

These rules belong in the shared instruction prefix so they apply to both ordinary turns and Anthropic web-search turns.

## Non-Goals

- Do not rewrite Bash tool arguments inside the adapter.
- Do not couple the bridge to Claude Code's Bash schema.
- Do not change `run-tests.sh` or Go timeout semantics.
- Do not make the bridge inspect or own Claude Code's task state.
- Do not prevent a turn from stopping when work is genuinely blocked on required user input or an external condition.

## Data Flow

1. Claude Code sends its system prompt and host tool definitions to the local Anthropic-compatible bridge.
2. The bridge creates an ephemeral Codex app-server thread.
3. `baseInstructions` establishes Claude Code identity, persistence, background-registration behavior, and the Codex-owned-tool boundary.
4. GPT chooses host background mode before starting a potentially timeout-length command.
5. Claude Code registers the background task immediately.
6. A completion notification re-invokes the session.
7. GPT consumes the result, completes remaining work, updates the checklist, and only then returns a final response.

## Error Handling

The new guidance does not suppress errors or change protocol responses. Failed background work still returns a host task notification and remains actionable. Existing continuation recovery, context-overflow mapping, web-search handling, and Codex-owned-tool rejection remain unchanged.

## Tests

Add a focused `internal/gptbridge` regression test that checks both normal and web-search `baseInstructions` for:

- persistence until the request is resolved;
- no final response with actionable checklist items pending;
- background registration from the outset for timeout-length host work.

Run the new test first and observe it fail before changing `engine.go`. Then run the focused test, the `internal/gptbridge` package tests, changed-file formatting/vetting, mandatory shellcheck, and the full repository suite before committing and pushing.
