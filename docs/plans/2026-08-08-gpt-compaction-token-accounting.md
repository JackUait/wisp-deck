# GPT Compaction Token Accounting Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Stop Claude Code from rejecting valid GPT compaction turns before Wisp Deck can apply GPT's real context window, and stop the bridge from counting serialized JSON escaping as model tokens.

**Architecture:** Disable Claude Code's assumed window enforcement only inside the GPT adapter so inherited sessions reach Wisp Deck's existing model-aware guard. Compute one semantic input estimate from the normalized Anthropic request, carry it through `Translation`, and use it at every boundary that reports or checks prompt size. Preserve the flat image allowance; remove the independent raw-body and re-marshaled-translation estimates.

**Tech Stack:** Go, `net/http`, Anthropic Messages-compatible SSE, Codex app-server.

---

### Task 1: Make token counting semantic and consistent

**Files:**
- Modify: `internal/gptbridge/contextguard.go`
- Modify: `internal/gptbridge/contextguard_test.go`
- Modify: `internal/gptbridge/translate.go`
- Modify: `internal/gptbridge/translate_test.go`
- Modify: `internal/gptbridge/server.go`
- Modify: `internal/gptbridge/server_test.go`

**Step 1: Write failing regression tests**

Add a quote- and backslash-heavy Messages request. Assert that
`estimatePromptTokens` counts its normalized semantic content once and returns
at least one token. Assert that `TranslateRequest` records exactly that estimate
on the returned `Translation`. Send the same request to `/count_tokens` and
assert the endpoint returns the same estimate rather than `len(payload)/3`.

**Step 2: Run the focused tests and verify RED**

Run:

```bash
go test -count=1 ./internal/gptbridge -run 'TestEstimatePromptTokensCountsSemanticContentNotJSONEscapes|TestTranslateRecordsSemanticInputEstimate|TestHandlerCountTokensUsesSemanticPromptEstimate' -v
```

Expected: FAIL because `Translation` has no semantic estimate and
`/count_tokens` still counts raw JSON bytes.

**Step 3: Implement the minimal request-level change**

Add `EstimatedInputTokens int64` to `Translation`. Make
`estimatePromptTokens` return at least one. Set the field in `TranslateRequest`.
In both `/v1/messages` and `/v1/messages/count_tokens`, parse the normalized
request once and use that semantic estimate for window admission and the count
response.

**Step 4: Run the focused tests and verify GREEN**

Run the command from Step 2. Expected: PASS.

### Task 2: Use the request estimate in streamed usage

**Files:**
- Modify: `internal/gptbridge/engine.go`
- Modify: `internal/gptbridge/engine_test.go`

**Step 1: Write a failing engine regression test**

Start a fake engine turn with `EstimatedInputTokens: 12345`, complete it with
the existing fake app-server notifications, and inspect the emitted
`message_start`. Assert `usage.input_tokens` is `12345`, even when the
translation contains escape-heavy input whose re-marshaled size would produce
a different number.

**Step 2: Run the focused test and verify RED**

Run:

```bash
go test -count=1 ./internal/gptbridge -run TestEngineReportsRequestSemanticEstimate -v
```

Expected: FAIL because the engine calls `estimateTranslationTokens`.

**Step 3: Implement the minimal engine change**

Pass `translation.EstimatedInputTokens` to `ResponseOptions` and remove
`estimateTranslationTokens`, eliminating the second JSON serialization.

**Step 4: Run the focused test and verify GREEN**

Run the command from Step 2. Expected: PASS.

### Task 3: Verify the complete compaction path and install

**Files:**
- Verify: `internal/gptbridge/*.go`
- Install: `bin/wisp-deck-tui`, `~/.local/bin/wisp-deck-tui`

**Step 1: Format and run scoped regressions**

Run:

```bash
gofmt -w internal/gptbridge/contextguard.go internal/gptbridge/contextguard_test.go internal/gptbridge/translate.go internal/gptbridge/translate_test.go internal/gptbridge/server.go internal/gptbridge/server_test.go internal/gptbridge/engine.go internal/gptbridge/engine_test.go
go test -count=1 ./internal/gptbridge
git diff --check
```

Expected: PASS with no formatting or whitespace errors.

**Step 2: Re-run the controlled Claude Code boundary probe**

Use a local fake Anthropic endpoint to show that the old inflated streamed
usage makes `claude --resume` fail locally with `Prompt is too long`, while the
new semantic estimate permits the next `/v1/messages` request. Do not touch a
running ledger pane.

**Step 3: Update and verify the required local installation**

Run:

```bash
make install
command -v wisp-deck-tui
shasum -a 256 bin/wisp-deck-tui ~/.local/bin/wisp-deck-tui
codesign --verify --verbose=2 ~/.local/bin/wisp-deck-tui
```

Expected: command resolves to `~/.local/bin/wisp-deck-tui`, hashes match, and
code-signature verification succeeds.

**Step 4: Commit only the compaction fix**

Stage the token-accounting files without including unrelated pre-existing
adapter-timeout changes, then commit with:

```bash
git commit -m "fix(gptbridge): count semantic compaction input"
```

### Task 4: Let the GPT bridge own unknown-model window admission

**Files:**
- Modify: `internal/gptbridge/adapter.go`
- Create: `internal/gptbridge/adapter_compaction_test.go`

**Step 1: Reproduce the process boundary**

Resume a fake 190K-token Claude Code session under an unrecognized model ID.
Confirm Claude Code returns `Prompt is too long` without sending
`/v1/messages`. Repeat with
`CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1` and confirm the
request reaches the endpoint.

**Step 2: Write and run the failing adapter regression**

Assert `BuildClaudeEnvironment` replaces any inherited value with exactly one
`CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1` entry. Run the focused
test and confirm it fails because the adapter currently preserves the inherited
value.

**Step 3: Implement and verify the minimal override**

Add the variable to the adapter's existing environment override boundary. Run
the focused test and the complete `internal/gptbridge` package, run
`make install`, then run the installed adapter environment probe. Do not add
the variable to global Claude settings; native Claude sessions must retain
Claude Code's own window enforcement.
