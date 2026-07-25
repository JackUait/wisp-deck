# GPT Compaction Continuation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve Claude Code compaction instructions as fresh GPT input when a mixed tool-result continuation is reconstructed.

**Architecture:** Translation records mixed final-message content in two channels: the live tool response retains the current merged payload, while recovery history retains only the original tool output and supplemental ordinary blocks become `Translation.Input`. Continuation recovery replays the clean tool output and appends the supplemental input after its existing no-repeat notice.

**Tech Stack:** Go 1.24, standard library JSON, Codex app-server JSON-RPC, Anthropic Messages compatibility layer.

## Global Constraints

- Work directly on the existing `main` branch; never create branches or worktrees.
- Follow TDD: write each regression test, run it and observe failure, then implement the minimum behavior.
- Do not recognize compaction by prompt text; preserve mixed-message semantics generically.
- Keep live Skill/system-reminder continuation behavior unchanged.
- Lint only changed files during development; run the full repository suite for final verification.
- Run shellcheck on modified scripts; this change should modify no shell scripts, so explicitly record that shellcheck is not applicable if that remains true.
- Push all commits and verify `git status` reports the branch up to date with `origin/main` before completion.

---

### Task 1: Preserve both mixed-message channels during translation

**Files:**
- Modify: `internal/gptbridge/translate.go:34-54,163-263,363-383`
- Test: `internal/gptbridge/translate_test.go:211-394`

**Interfaces:**
- Consumes: normalized `Message` and `ContentBlock` values from `ParseMessagesRequest`.
- Produces: `TranslatedToolResult.HistoryContentItems []ToolOutputItem` and supplemental `Translation.Input []UserInput` for recovery; `ContentItems` remains the live app-server response payload.

- [ ] **Step 1: Write the failing translation regression test**

Add a test that models a tool result followed by Claude Code's plain-text compaction instruction:

```go
func TestTranslateMixedToolResultPreservesSupplementalRecoveryInput(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-sol",
		"max_tokens":32000,
		"messages":[
			{"role":"user","content":"Run lint"},
			{"role":"assistant","content":[{
				"type":"tool_use","id":"lint_1","name":"Bash","input":{"command":"yarn lint"}
			}]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"lint_1","content":"Exit code 1"},
				{"type":"text","text":"Summarize the conversation. Do not call tools; respond with plain text only."}
			]}
		]
	}`)

	if len(got.ToolResults) != 1 || len(got.Input) != 1 {
		t.Fatalf("translation = %+v", got)
	}
	result := got.ToolResults[0]
	if len(result.ContentItems) != 2 || result.ContentItems[1].Text != "Summarize the conversation. Do not call tools; respond with plain text only." {
		t.Fatalf("live result content = %+v", result.ContentItems)
	}
	if len(result.HistoryContentItems) != 1 || result.HistoryContentItems[0].Text != "Exit code 1" {
		t.Fatalf("history result content = %+v", result.HistoryContentItems)
	}
	if got.Input[0].Type != "text" || got.Input[0].Text != "Summarize the conversation. Do not call tools; respond with plain text only." {
		t.Fatalf("supplemental input = %+v", got.Input)
	}
}
```

Update the existing mixed-content tests to expect their ordinary text/image blocks in `Translation.Input` as well as in live `ContentItems`. Keep pure tool-result tests expecting no input.

- [ ] **Step 2: Run the focused test and observe failure**

Run:

```bash
go test ./internal/gptbridge -run TestTranslateMixedToolResultPreservesSupplementalRecoveryInput -count=1 -v
```

Expected: FAIL to compile because `TranslatedToolResult.HistoryContentItems` does not exist, proving the recovery representation is absent.

- [ ] **Step 3: Implement dual-channel normalization**

Extend the result type:

```go
type TranslatedToolResult struct {
	ToolUseID          string
	Success            bool
	ContentItems       []ToolOutputItem
	HistoryContentItems []ToolOutputItem
}
```

Introduce a focused internal representation:

```go
type normalizedToolResult struct {
	Live    ContentBlock
	History ContentBlock
}
```

Change `normalizedFinalToolResults` to return `([]normalizedToolResult, []ContentBlock, error)`. For each `tool_result`, copy the unmodified block into `History`, retain the current adjacent merge behavior in `Live`, and append every ordinary block to a `supplemental` slice in source order. Translate `supplemental` through `translateUserInput` into `translation.Input`.

For each normalized result, translate both blocks and set:

```go
live, err := translateToolResult(block.Live)
if err != nil {
	return err
}
history, err := translateToolResult(block.History)
if err != nil {
	return err
}
live.HistoryContentItems = history.ContentItems
translation.ToolResults = append(translation.ToolResults, live)
```

- [ ] **Step 4: Run translation tests and format changed Go files**

Run:

```bash
gofmt -w internal/gptbridge/translate.go internal/gptbridge/translate_test.go
go test ./internal/gptbridge -run 'TestTranslate(PendingToolResults|SkillContinuationMergesSupplementalContent|MergesTextAcrossMultipleToolResults|MergesLeadingTextIntoFirstToolResult|NeverRejectsAnyFinalUserBlockInterleaving|MixedToolResultPreservesSupplementalRecoveryInput)' -count=1 -v
```

Expected: PASS. Pure tool results still have empty `Input`; every ordinary mixed block appears in both the live result assignment and supplemental input without loss.

- [ ] **Step 5: Commit translation behavior**

```bash
git add internal/gptbridge/translate.go internal/gptbridge/translate_test.go
git commit -m "fix(gptbridge): preserve mixed continuation input" -m "Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Feed preserved instructions into recovery

**Files:**
- Modify: `internal/gptbridge/engine.go:155-190`
- Test: `internal/gptbridge/engine_test.go:372-465`

**Interfaces:**
- Consumes: `Translation.ToolResults[*].HistoryContentItems` and `Translation.Input` from Task 1.
- Produces: recovery `Translation.History` containing clean `function_call_output` items and recovery `Translation.Input` containing the bridge notice followed by supplemental model input.

- [ ] **Step 1: Write the failing end-to-end engine regression test**

Add a recovery test using an expired pending tool turn. Construct its continuation with:

```go
continuation.Input = []UserInput{{
	Type: "text",
	Text: "Summarize the conversation. Do not call tools; respond with plain text only.",
}}
continuation.ToolResults = []TranslatedToolResult{{
	ToolUseID: id,
	Success:   true,
	ContentItems: []ToolOutputItem{
		{Type: "inputText", Text: "Exit code 1"},
		{Type: "inputText", Text: "Summarize the conversation. Do not call tools; respond with plain text only."},
	},
	HistoryContentItems: []ToolOutputItem{{Type: "inputText", Text: "Exit code 1"}},
}}
```

Have the recovered fake turn complete with `"summary text"`. Assert:

```go
if strings.Contains(injected, "Summarize the conversation") {
	t.Fatalf("supplemental instruction leaked into function output history: %s", injected)
}
if !strings.Contains(turnInputs, "tool results above are complete") ||
	!strings.Contains(turnInputs, "Summarize the conversation") {
	t.Fatalf("recovery input = %s", turnInputs)
}
if len(message.Content) != 1 || message.Content[0].Text != "summary text" {
	t.Fatalf("recovered response = %+v", message)
}
```

- [ ] **Step 2: Run the focused engine test and observe failure**

Run:

```bash
go test ./internal/gptbridge -run TestEngineRecoveryPreservesCompactionInstructionAsFreshInput -count=1 -v
```

Expected: FAIL because recovery injects the merged compaction instruction into `function_call_output` and overwrites `Translation.Input` with only the generic notice.

- [ ] **Step 3: Implement clean history and appended recovery input**

In `recoverContinuationFromHistory`, use `HistoryContentItems` when present, with `ContentItems` as the compatibility fallback for programmatically built translations:

```go
content := result.HistoryContentItems
if content == nil {
	content = result.ContentItems
}
output, ok := translatedToolHistoryOutput(content)
```

Preserve supplemental input before resetting recovery input:

```go
supplemental := append([]UserInput(nil), translation.Input...)
recovery.Input = []UserInput{{
	Type: "text",
	Text: "[bridge] The tool results above are complete; continue the interrupted response. " +
		"Do not repeat tool calls that already have results above, and do not " +
		"re-ask questions whose answers already appear above.",
}}
recovery.Input = append(recovery.Input, supplemental...)
```

Do not change live `resume`; it continues to answer the suspended app-server request with merged `ContentItems` and intentionally ignores `Translation.Input`.

- [ ] **Step 4: Run the engine regression and package tests**

Run:

```bash
gofmt -w internal/gptbridge/engine.go internal/gptbridge/engine_test.go
go test ./internal/gptbridge -run 'TestEngine(RecoveryPreservesCompactionInstructionAsFreshInput|RecoversExpiredContinuationFromValidatedHistory|RecoveryCleansUpLiveTurnFromMixedContinuation)' -count=1 -v
go test ./internal/gptbridge -count=1
```

Expected: PASS. Recovery history contains only true tool output, fresh input contains the no-repeat notice and supplemental instruction, and existing continuation recovery remains green.

- [ ] **Step 5: Commit recovery behavior**

```bash
git add internal/gptbridge/engine.go internal/gptbridge/engine_test.go
git commit -m "fix(gptbridge): keep compaction prompt fresh" -m "Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Verify and land the root-cause fix

**Files:**
- Verify: `internal/gptbridge/translate.go`
- Verify: `internal/gptbridge/translate_test.go`
- Verify: `internal/gptbridge/engine.go`
- Verify: `internal/gptbridge/engine_test.go`

**Interfaces:**
- Consumes: the completed translation and recovery changes.
- Produces: a pushed `main` branch whose focused regression and full repository suite pass.

- [ ] **Step 1: Run changed-file formatting and static checks**

```bash
gofmt -w internal/gptbridge/translate.go internal/gptbridge/translate_test.go internal/gptbridge/engine.go internal/gptbridge/engine_test.go
git diff --check HEAD~2 -- internal/gptbridge/translate.go internal/gptbridge/translate_test.go internal/gptbridge/engine.go internal/gptbridge/engine_test.go
go vet ./internal/gptbridge
```

Expected: all commands succeed. No shell scripts changed, so shellcheck is not applicable.

- [ ] **Step 2: Run the exact regression and package tests uncached**

```bash
go test ./internal/gptbridge -run 'TestTranslateMixedToolResultPreservesSupplementalRecoveryInput|TestEngineRecoveryPreservesCompactionInstructionAsFreshInput' -count=1 -v
go test ./internal/gptbridge -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the mandatory full suite**

```bash
./run-tests.sh
```

Expected: PASS. If a timing test fails under parallel system load, rerun that named test in isolation and report both results without hiding the full-suite failure.

- [ ] **Step 4: Review the final diff and repository state**

```bash
git diff origin/main...HEAD --check
git status --short --branch
git log --oneline --decorate -5
```

Expected: only the design, plan, translation, and recovery commits are present; the worktree is clean.

- [ ] **Step 5: Push and verify remote synchronization**

```bash
git pull --rebase
git push
git status --short --branch
```

Expected: push succeeds and status reports `main...origin/main` with no ahead/behind marker or local changes.
