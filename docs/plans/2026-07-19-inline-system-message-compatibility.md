# Inline System Message Compatibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Accept Claude Code 2.1.214 inline system messages and Skill continuations without losing instructions or emitting invalid Codex history.

**Architecture:** Normalize inline `system` messages at the Anthropic parsing boundary by appending their text blocks to `MessagesRequest.System` and omitting them from `MessagesRequest.Messages`. At the translation boundary, merge supplemental text or images from a single-result continuation into that dynamic tool result's content items; reject ambiguous mixed content beside multiple results. Reuse the existing Codex developer-instruction and dynamic-tool response channels.

**Tech Stack:** Go, Anthropic Messages JSON, Codex app-server RPC, Go testing and race detector.

---

### Task 1: Normalize Inline System Messages

**Files:**
- Modify: `internal/gptbridge/anthropic.go:154-166`
- Test: `internal/gptbridge/translate_test.go`

**Step 1: Write the failing regression test**

Add a test using the live Claude 2.1.214 ordering:

```go
func TestTranslateInlineSystemMessageIntoDeveloperInstructions(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"system":"Top-level instructions.",
		"messages":[
			{"role":"user","content":"test"},
			{"role":"system","content":[
				{"type":"text","text":"SessionStart hook context."}
			]}
		]
	}`)
	if got.System != "Top-level instructions.\n\nSessionStart hook context." {
		t.Fatalf("system = %q", got.System)
	}
	if len(got.History) != 0 {
		t.Fatalf("history = %#v, want no inline system item", got.History)
	}
	if len(got.Input) != 1 || got.Input[0].Text != "test" {
		t.Fatalf("input = %#v", got.Input)
	}
}
```

**Step 2: Run the test and verify RED**

Run:

```bash
go test ./internal/gptbridge -run TestTranslateInlineSystemMessageIntoDeveloperInstructions -count=1 -v
```

Expected: FAIL with `messages[1]: unsupported role "system"`.

**Step 3: Implement the minimal parser normalization**

In the `wire.Messages` loop, handle `system` before the existing user/assistant
role check:

```go
if message.Role == "system" {
	content, err := parseContent(message.Content, message.Role)
	if err != nil {
		return MessagesRequest{}, fmt.Errorf("messages[%d]: %w", index, err)
	}
	for _, block := range content {
		if block.Type != "text" {
			return MessagesRequest{}, fmt.Errorf(
				"messages[%d]: system message has unsupported content block %q",
				index, block.Type,
			)
		}
	}
	request.System = append(request.System, content...)
	continue
}
```

Do not alter downstream history translation.

**Step 4: Run the test and verify GREEN**

Run:

```bash
go test ./internal/gptbridge -run TestTranslateInlineSystemMessageIntoDeveloperInstructions -count=1 -v
```

Expected: PASS.

### Task 2: Pin Invalid Inline System Inputs

**Files:**
- Modify: `internal/gptbridge/anthropic.go`
- Test: `internal/gptbridge/translate_test.go`

**Step 1: Write failing validation tests**

Add table cases proving:

- an inline system image is rejected with a message-indexed error;
- a request containing only inline system messages is rejected because no
  user/assistant model input remains.

**Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/gptbridge -run 'TestParseMessagesRequestRejects(NonTextInlineSystem|SystemOnlyMessages)' -count=1 -v
```

Expected: at least one test FAILS because the parser does not yet enforce the
post-normalization message invariant.

**Step 3: Implement the remaining validation**

After normalizing all wire messages, reject an empty normalized transcript:

```go
if len(request.Messages) == 0 {
	return MessagesRequest{}, errors.New("messages must contain a user or assistant message")
}
```

Keep the inline system content validation in the parser loop from Task 1.

**Step 4: Run focused and package tests**

Run:

```bash
go test ./internal/gptbridge -run 'Test(Parse|Translate).*System' -count=1 -v
go test ./internal/gptbridge -count=1
go test -race ./internal/gptbridge -count=1
```

Expected: PASS.

**Step 5: Commit the implementation**

```bash
git add internal/gptbridge/anthropic.go internal/gptbridge/translate_test.go
git commit -m "fix(bridge): accept inline system messages"
```

### Task 3: Preserve Skill Continuation Content

**Files:**
- Modify: `internal/gptbridge/translate.go:174-207`
- Test: `internal/gptbridge/translate_test.go`

**Step 1: Write the failing captured-shape regression**

Add:

```go
func TestTranslateSkillContinuationMergesSupplementalContent(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"Load the skill"},
			{"role":"assistant","content":[{
				"type":"tool_use","id":"skill_1","name":"Skill",
				"input":{"skill":"superpowers:using-superpowers"}
			}]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"skill_1",
				 "content":"Launching skill: superpowers:using-superpowers"},
				{"type":"text","text":"Base directory for this skill: /skills/superpowers"}
			]}
		]
	}`)
	if len(got.ToolResults) != 1 || len(got.Input) != 0 {
		t.Fatalf("translation = %+v", got)
	}
	items := got.ToolResults[0].ContentItems
	if len(items) != 2 ||
		items[0].Text != "Launching skill: superpowers:using-superpowers" ||
		items[1].Text != "Base directory for this skill: /skills/superpowers" {
		t.Fatalf("content items = %#v", items)
	}
}
```

**Step 2: Write the ambiguity regression**

Add a request with two pending tool uses, two final tool results, and one
ordinary text block. Assert that translation returns:

```text
final user content alongside multiple tool results is ambiguous
```

**Step 3: Run both tests and verify RED**

Run:

```bash
go test ./internal/gptbridge -run 'TestTranslate(SkillContinuation|RejectsAmbiguousMixedToolResults)' -count=1 -v
```

Expected: both FAIL under the current blanket
`final tool_result message cannot contain ordinary user content` rejection.

**Step 4: Implement the minimal continuation normalizer**

Add a helper:

```go
func normalizedFinalToolResults(blocks []ContentBlock) ([]ContentBlock, error) {
	var results []ContentBlock
	hasOrdinary := false
	for _, block := range blocks {
		if block.Type == "tool_result" {
			results = append(results, block)
		} else {
			hasOrdinary = true
		}
	}
	if !hasOrdinary {
		return results, nil
	}
	if len(results) != 1 {
		return nil, errors.New(
			"final user content alongside multiple tool results is ambiguous",
		)
	}
	merged := make([]ContentBlock, 0, len(results[0].ToolContent)+len(blocks)-1)
	for _, block := range blocks {
		if block.Type == "tool_result" {
			merged = append(merged, block.ToolContent...)
		} else {
			merged = append(merged, block)
		}
	}
	results[0].ToolContent = merged
	return results, nil
}
```

In the final tool-result branch, call this helper and translate only its
returned result blocks. Do not alter tool-result-only or ordinary user turns.

**Step 5: Run focused and bridge tests**

Run:

```bash
go test ./internal/gptbridge -run 'TestTranslate(SkillContinuation|RejectsAmbiguousMixedToolResults)' -count=1 -v
go test ./internal/gptbridge -count=1
go test -race ./internal/gptbridge -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/gptbridge/translate.go internal/gptbridge/translate_test.go
git commit -m "fix(bridge): preserve Skill continuation text"
```

### Task 4: Verify the Live Bridge and Install

**Files:**
- No source changes expected.

**Step 1: Run the affected integration tests**

Run:

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'GPT|Gpt|ChatGPT' -count=1
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 go test ./test/bash -run TestLiveGPTSubscriptionInClaude -count=1 -v
```

Expected: PASS.

**Step 2: Reproduce the original Claude request**

Launch a fresh OpenAI GPT session through Wisp Deck and send a short prompt.
Confirm the request no longer returns `messages[1]: unsupported role "system"`.

Run a second non-bare prompt with Claude's default tools so its SessionStart
hook loads `superpowers:using-superpowers`. Confirm the Skill continuation
completes without:

```text
final tool_result message cannot contain ordinary user content
```

**Step 3: Install**

Run:

```bash
make install
```

Expected: build and install succeed.

**Step 4: Verify the installed artifact**

Verify:

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: path and hashes match; code signature is valid.
