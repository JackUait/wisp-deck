# GPT Checklist Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep GPT-backed Claude Code sessions resumable through long host work so actionable checklist items are finished instead of abandoned.

**Architecture:** Extend the GPT bridge's shared `baseInstructions` prefix with persistence and up-front background-registration guidance. The bridge continues to pass Claude Code's host tools through unchanged; normal turns and web-search turns receive the same lifecycle rules.

**Tech Stack:** Go, Codex app-server RPC, Anthropic Messages compatibility, Go testing.

## Global Constraints

- Work directly on the existing `main` branch; never create branches or worktrees.
- Modify only `internal/gptbridge/engine.go` and `internal/gptbridge/engine_test.go` for runtime behavior.
- Do not rewrite Bash arguments or modify `run-tests.sh`.
- Follow strict TDD: add and run the failing regression test before editing implementation code.
- Preserve all current Codex-owned-tool, web-search, continuation, and error behavior.
- Run mandatory shellcheck, the full suite, push, and verify synchronization before completion.

## File Structure

- `internal/gptbridge/engine.go`: builds provider-scoped Codex base instructions for GPT-backed Claude Code turns.
- `internal/gptbridge/engine_test.go`: guards lifecycle guidance for normal and web-search turns.

---

### Task 1: Restore GPT Checklist Persistence Guidance

**Files:**
- Modify: `internal/gptbridge/engine_test.go:799-835`
- Modify: `internal/gptbridge/engine.go:574-600`

**Interfaces:**
- Consumes: `baseInstructions(translation Translation) string`
- Produces: the same function signature with shared persistence/background-registration text in both normal and web-search variants.

- [x] **Step 1: Write the failing regression test**

Add this test before `TestEngineEnablesCodexWebSearchOnlyWhenRequested`:

```go
func TestBaseInstructionsKeepClaudeChecklistWorkResumable(t *testing.T) {
	for _, webSearch := range []bool{false, true} {
		t.Run(fmt.Sprintf("web_search_%t", webSearch), func(t *testing.T) {
			instructions := baseInstructions(Translation{WebSearch: webSearch})
			for _, want := range []string{
				"fully resolved",
				"actionable checklist items remain",
				"background mode from the outset",
				"registers their completion and automatically resumes you",
			} {
				if !strings.Contains(instructions, want) {
					t.Fatalf("baseInstructions omit %q: %q", want, instructions)
				}
			}
		})
	}
}
```

`engine_test.go` already imports `fmt` and `strings`; add no dependencies.

- [x] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./internal/gptbridge -run TestBaseInstructionsKeepClaudeChecklistWorkResumable -count=1 -v
```

Expected: FAIL because the current base instructions omit `fully resolved` and the other lifecycle phrases.

- [x] **Step 3: Add the minimal shared guidance**

Extend the initial `lines` slice in `baseInstructions` so it reads:

```go
lines := []string{
	"You are the language model inside Claude Code.",
	"Follow the supplied developer and user instructions.",
	"Continue working until the user's request is fully resolved; never claim completion while actionable checklist items remain.",
	"Start host commands that may approach their timeout in background mode from the outset, so Claude Code registers their completion and automatically resumes you.",
}
```

Do not change the normal/web-search branch structure or any tool permissions.

- [x] **Step 4: Format and verify GREEN**

Run:

```bash
gofmt -w internal/gptbridge/engine.go internal/gptbridge/engine_test.go
go test ./internal/gptbridge -run TestBaseInstructionsKeepClaudeChecklistWorkResumable -count=1 -v
```

Expected: both subtests PASS.

- [x] **Step 5: Run bridge regression coverage**

Run:

```bash
go test ./internal/gptbridge -count=1
go vet ./internal/gptbridge
git diff --check -- internal/gptbridge/engine.go internal/gptbridge/engine_test.go
```

Expected: all commands exit 0.

### Task 2: Verify, Commit, Push, and Synchronize

**Files:**
- Verify: `internal/gptbridge/engine.go`
- Verify: `internal/gptbridge/engine_test.go`
- Verify: `docs/superpowers/specs/2026-07-26-gpt-checklist-completion-design.md`
- Verify: `docs/superpowers/plans/2026-07-26-gpt-checklist-completion.md`

**Interfaces:**
- Consumes: the Task 1 implementation and repository completion checklist.
- Produces: a tested commit on `main`, pushed to `origin/main`, with unrelated README/logo work left untouched.

- [x] **Step 1: Check the changed-file shellcheck scope**

Run:

```bash
changed=(${(f)"$(git diff --name-only -- '*.sh' wrapper.sh)"})
if (( ${#changed[@]} == 0 )); then
  printf 'No modified shell scripts; shellcheck scope is empty.\n'
else
  shellcheck "${changed[@]}"
fi
```

Expected: `No modified shell scripts; shellcheck scope is empty.` The global lint rule forbids linting unchanged files.

- [x] **Step 2: Run the full suite as registered background work**

Launch from the outset with host background mode and remove inherited session state:

```bash
unset_args=()
while IFS='=' read -r name _; do
  [[ "$name" == WISP_DECK* ]] && unset_args+=(-u "$name")
done < <(env)
env "${unset_args[@]}" -u CLAUDE_CONFIG_DIR -u TMUX -u TMUX_PANE \
  ./run-tests.sh -timeout=40m -p=1 -parallel=4 -count=1
```

Expected: every package passes. Removing every `WISP_DECK_*` variable is required because live session stamps such as `WISP_DECK_RELAUNCH_FILE` can override the isolated values a test supplies.

- [x] **Step 3: Review owned changes only**

Run:

```bash
git status --short
git diff --check
git diff -- internal/gptbridge/engine.go internal/gptbridge/engine_test.go docs/superpowers/plans/2026-07-26-gpt-checklist-completion.md
```

Expected: only the two bridge files and this plan are owned by this implementation; existing README/logo files remain unstaged.

- [x] **Step 4: Commit only owned implementation files**

Run:

```bash
git add internal/gptbridge/engine.go internal/gptbridge/engine_test.go docs/superpowers/plans/2026-07-26-gpt-checklist-completion.md
git commit -m "fix(gptbridge): finish Claude checklists" -m "Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

Expected: commit succeeds without staging `README.md` or `docs/wisp-deck-logo.png`.

- [x] **Step 5: Rebase, push, and verify synchronization**

Run:

```bash
git pull --rebase
git push
git status --short --branch
git rev-parse HEAD
git rev-parse origin/main
```

Expected: push succeeds; `HEAD` equals `origin/main`; status reports the branch is up to date while preserving any unrelated working-tree files.
