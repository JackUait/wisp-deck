# GPT Bridge Cold Rebuilds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Claude Code's background forks stop replaying whole conversations uncached through the GPT bridge, and any future leak of that kind raises a warning.

**Architecture:** Three independent layers in `internal/gptbridge`, wired from `cmd/wisp-deck-tui`:
- Layer 1: `Engine.Execute` answers the subagent progress-summary prompt locally.
- Layer 2: the engine parks a finished thread and reuses it for the next matching request.
- Layer 3: a `ColdStartFuse` counts fresh threads started with history and calls a warn hook past a threshold. `cmd` turns that into one log line plus a fixed macOS notification through `runHostEffect`.

**Tech Stack:** Go, the Codex app-server JSON-RPC (`thread/start`, `thread/inject_items`, `turn/start`, `thread/delete`).

**Spec:** `docs/superpowers/specs/2026-09-28-gptbridge-cold-rebuilds-design.md`

## Global Constraints

- Summary prompt prefix, exactly: `Describe your most recent action in 3-5 words`.
- Summary reply: one line, at most 100 characters, stop reason `end_turn`, usage all zero, no Codex RPC call.
- Parking pool: at most 16 threads; a thread idle 30 minutes is deleted.
- Fuse: window 10 minutes, threshold 2,000,000 estimated tokens, at most one warning per hour.
- Warning log: `~/.config/wisp-deck/gptbridge-cold.log`. Notification text is fixed: title `GPT bridge`, body `Context re-sent without cache — see gptbridge-cold.log`.
- The warning never blocks or changes a request: the fuse calls the hook on its own goroutine.
- Host effects only through `runHostEffect`; `cmd/wisp-deck-tui/host_effects.go` keeps exactly one `"/usr/bin/osascript"` literal and one `display notification …` literal.
- Tests: scoped runs with `WISP_DECK_TESTING=1`. Never a bare `go test ./...`. `./test/bash/...` needs `-timeout 20m`.
- Comments: short, only what silently breaks if changed. `internal/**/CLAUDE.md` is audited prose: no bare `say`, no BEL, no audio marker.
- Work on `main`, stage only your own files. Every commit message ends with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.

## Review Focus

1. A summary fork whose tool ids are unknown (the real shape) → answered locally, never recovered into a new thread.
2. A request that extends a parked thread but whose earlier item changed (compaction) → new thread, never reuse.
3. Two requests racing for one parked thread → only one gets it.
4. A parked thread gets a stray app-server notification → dropped; it must not surface in the next turn on that thread.
5. The fuse under an engine rebuild (`ResilientExecutor`) → one fuse per bridge counts every engine's cold starts.

Each has a test in the task that owns it.

---

### Task 1: Answer progress summaries locally

**Files:**
- Create: `internal/gptbridge/agentsummary.go`
- Test: `internal/gptbridge/agentsummary_test.go`
- Modify: `internal/gptbridge/engine.go` (`Execute`)

**Interfaces:**
- Produces:
  - `const agentSummaryPrompt = "Describe your most recent action in 3-5 words"`
  - `func isAgentSummaryInput(input []UserInput) bool`
  - `func agentSummaryReply(history []map[string]any) string`
  - `func localTextResponse(model, text string, emit func([]StreamEvent) error) (AnthropicMessage, error)`

- [ ] **Step 1: Write the failing tests** (`agentsummary_test.go`)

```go
package gptbridge

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func summaryTranslation(history []map[string]any) Translation {
	translation := testTranslation("Describe your most recent action in 3-5 words using present tense (-ing). Name the file or function, not the branch. Do not use tools.")
	translation.History = history
	return translation
}

func callItem(name, arguments string) map[string]any {
	return map[string]any{"type": "function_call", "call_id": "toolu_1", "name": name, "arguments": arguments}
}

func TestAgentSummaryReplyNamesTheLastToolCall(t *testing.T) {
	cases := map[string]map[string]any{
		"Reading engine.go":                callItem("Read", `{"file_path":"/repo/internal/engine.go"}`),
		"Editing proxy.go":                 callItem("Edit", `{"file_path":"/repo/proxy.go","old_string":"a","new_string":"b"}`),
		"Writing notes.md":                 callItem("Write", `{"file_path":"/tmp/notes.md","content":"x"}`),
		"Editing book.ipynb":               callItem("NotebookEdit", `{"notebook_path":"/r/book.ipynb"}`),
		"Searching for cold start":         callItem("Grep", `{"pattern":"cold start"}`),
		"Searching for **/*.go":            callItem("Glob", `{"pattern":"**/*.go"}`),
		"Run the bridge tests":             callItem("Bash", `{"command":"go test","description":"Run the bridge tests"}`),
		"Running a shell command":          callItem("Bash", `{"command":"ls"}`),
		"Using WebFetch":                   callItem("WebFetch", `{"url":"https://x"}`),
	}
	for want, item := range cases {
		history := []map[string]any{
			callItem("Read", `{"file_path":"/old.go"}`),
			{"type": "function_call_output", "call_id": "toolu_0", "output": "x"},
			item,
		}
		if got := agentSummaryReply(history); got != want {
			t.Errorf("agentSummaryReply(%v) = %q, want %q", item["name"], got, want)
		}
	}
	if got := agentSummaryReply(nil); got != "Starting work" {
		t.Errorf("no tool call gave %q", got)
	}
}

func TestAgentSummaryReplyIsOneShortLine(t *testing.T) {
	long := strings.Repeat("very long description ", 20) + "\nsecond line"
	got := agentSummaryReply([]map[string]any{callItem("Bash", `{"command":"x","description":`+strconv.Quote(long)+`}`)})
	if len([]rune(got)) > 100 || strings.ContainsAny(got, "\r\n") || got == "" {
		t.Fatalf("reply %q is not one line of at most 100 characters", got)
	}
}

func TestEngineAnswersAProgressSummaryWithoutCodex(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := newTestEngine(t, rpc)
	translation := summaryTranslation([]map[string]any{callItem("Read", `{"file_path":"/r/engine.go"}`)})
	// The real fork ends in a tool result the live thread already consumed.
	translation.ToolResults = []TranslatedToolResult{{ToolUseID: "toolu_gone", Success: true}}

	var events []StreamEvent
	message, err := engine.Execute(context.Background(), translation, func(got []StreamEvent) error {
		events = append(events, got...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.StopReason != "end_turn" || len(message.Content) != 1 || message.Content[0].Text != "Reading engine.go" {
		t.Fatalf("message = %+v", message)
	}
	if message.Usage != (Usage{}) {
		t.Fatalf("usage = %+v, want zero", message.Usage)
	}
	if len(events) == 0 || events[0].Event != "message_start" || events[len(events)-1].Event != "message_stop" {
		t.Fatalf("events = %+v", events)
	}
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	if len(rpc.calls) != 0 {
		t.Fatalf("a progress summary reached Codex: %v", rpc.calls)
	}
}

func TestIsAgentSummaryInputIgnoresOrdinaryText(t *testing.T) {
	if isAgentSummaryInput([]UserInput{{Type: "text", Text: "please describe the ledger"}}) {
		t.Fatal("ordinary text matched")
	}
	if !isAgentSummaryInput([]UserInput{{Type: "text", Text: agentSummaryPrompt + " using present tense"}}) {
		t.Fatal("the summary prompt did not match")
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/gptbridge/ -run 'TestAgentSummary|TestEngineAnswersAProgressSummary|TestIsAgentSummaryInput'`
Expected: build failure, `undefined: agentSummaryReply`.

- [ ] **Step 3: Implement** (`agentsummary.go`)

```go
package gptbridge

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// agentSummaryPrompt opens Claude Code's AgentSummary fork: every 30s, per
// running subagent, the whole conversation plus this prompt. Through the
// bridge each one replays ~125K tokens into a fresh, uncached thread.
// WISP_DECK_LIVE_AGENT_SUMMARY_E2E checks the installed Claude still sends it.
const agentSummaryPrompt = "Describe your most recent action in 3-5 words"

// Claude Code drops a longer or multi-line summary.
const agentSummaryMaxRunes = 100

func isAgentSummaryInput(input []UserInput) bool {
	for _, item := range input {
		if item.Type == "text" && strings.HasPrefix(strings.TrimSpace(item.Text), agentSummaryPrompt) {
			return true
		}
	}
	return false
}

// agentSummaryReply names the conversation's last tool call.
func agentSummaryReply(history []map[string]any) string {
	for index := len(history) - 1; index >= 0; index-- {
		item := history[index]
		if item["type"] != "function_call" {
			continue
		}
		name, _ := item["name"].(string)
		raw, _ := item["arguments"].(string)
		var arguments map[string]any
		_ = json.Unmarshal([]byte(raw), &arguments)
		text := func(key string) string {
			value, _ := arguments[key].(string)
			return strings.TrimSpace(value)
		}
		reply := "Using " + name
		switch name {
		case "Read":
			reply = "Reading " + filepath.Base(text("file_path"))
		case "Edit", "MultiEdit":
			reply = "Editing " + filepath.Base(text("file_path"))
		case "Write":
			reply = "Writing " + filepath.Base(text("file_path"))
		case "NotebookEdit":
			reply = "Editing " + filepath.Base(text("notebook_path"))
		case "Grep", "Glob":
			reply = "Searching for " + text("pattern")
		case "Bash":
			reply = "Running a shell command"
			if description := text("description"); description != "" {
				reply = description
			}
		}
		return oneShortLine(reply)
	}
	return "Starting work"
}

func oneShortLine(text string) string {
	if cut := strings.IndexAny(text, "\r\n"); cut >= 0 {
		text = text[:cut]
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > agentSummaryMaxRunes {
		runes = runes[:agentSummaryMaxRunes]
	}
	return strings.TrimSpace(string(runes))
}

// localTextResponse is a complete end_turn reply the bridge wrote itself, so
// its usage is zero: no model ran.
func localTextResponse(model, text string, emit func([]StreamEvent) error) (AnthropicMessage, error) {
	messageID, err := randomBridgeID("msg_")
	if err != nil {
		return AnthropicMessage{}, err
	}
	events := []StreamEvent{
		{Event: "message_start", Data: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": messageID, "type": "message", "role": "assistant",
				"model": model, "content": []any{},
				"stop_reason": nil, "stop_sequence": nil, "usage": Usage{},
			},
		}},
		contentStart(0, map[string]any{"type": "text", "text": ""}),
		{Event: "content_block_delta", Data: map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": text},
		}},
		contentStop(0),
		{Event: "message_delta", Data: map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": Usage{},
		}},
		{Event: "message_stop", Data: map[string]any{"type": "message_stop"}},
	}
	if err := emitEvents(emit, events); err != nil {
		return AnthropicMessage{}, err
	}
	return AnthropicMessage{
		ID: messageID, Type: "message", Role: "assistant", Model: model,
		Content:    []ResponseContentBlock{{Type: "text", Text: text}},
		StopReason: "end_turn",
	}, nil
}
```

In `engine.go` `Execute`, right after the model check:

```go
	if isAgentSummaryInput(translation.Input) {
		return localTextResponse(translation.Model, agentSummaryReply(translation.History), emit)
	}
```

- [ ] **Step 4: Run them and see them pass, plus the engine suite**

Run: `WISP_DECK_TESTING=1 go test ./internal/gptbridge/ -run 'TestAgentSummary|TestEngine|TestIsAgentSummaryInput'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gptbridge/agentsummary.go internal/gptbridge/agentsummary_test.go internal/gptbridge/engine.go
git commit -m "fix(gptbridge): answer Claude's subagent progress summaries without Codex"
```

---

### Task 2: Live check that Claude still sends that prompt

**Files:**
- Create: `internal/gptbridge/live_agent_summary_e2e_test.go`
- Modify: `CLAUDE.md` (root, the env-gated commands block)

**Interfaces:**
- Consumes: `agentSummaryPrompt`.

- [ ] **Step 1: Write the check**

```go
package gptbridge

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// After a claude upgrade: the local progress-summary answer only works while
// Claude Code's AgentSummary prompt still starts with agentSummaryPrompt. If
// it changes, every summary goes back to replaying the whole conversation.
func TestLiveClaudeStillSendsTheAgentSummaryPrompt(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_AGENT_SUMMARY_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_AGENT_SUMMARY_E2E=1 to check the installed Claude Code")
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(binary, []byte(agentSummaryPrompt)) {
		t.Fatalf("%s no longer contains %q: update agentSummaryPrompt, or summaries replay whole conversations again", resolved, agentSummaryPrompt)
	}
}
```

- [ ] **Step 2: Run it live and see it pass**

Run: `WISP_DECK_LIVE_AGENT_SUMMARY_E2E=1 go test ./internal/gptbridge/ -run TestLiveClaudeStillSendsTheAgentSummaryPrompt -v`
Expected: PASS against the installed 2.1.283.

- [ ] **Step 3: Prove it can fail**

Temporarily change the constant's last word (`words` → `wordz`), run the same command, expect FAIL, then revert the constant.

- [ ] **Step 4: Register it in the root `CLAUDE.md`** command block, after `TestLiveClaude`:

```bash
WISP_DECK_LIVE_AGENT_SUMMARY_E2E=1 go test ./internal/gptbridge/ -run TestLiveClaudeStillSendsTheAgentSummaryPrompt -v  # After a claude upgrade: verify Claude Code's subagent progress-summary prompt still starts with agentSummaryPrompt, or the GPT bridge replays a whole conversation uncached every 30s per subagent (costs nothing)
```

- [ ] **Step 5: Commit**

```bash
git add internal/gptbridge/live_agent_summary_e2e_test.go CLAUDE.md
git commit -m "test(gptbridge): live check that Claude still sends the progress-summary prompt"
```

---

### Task 3: The cold-start fuse

**Files:**
- Create: `internal/gptbridge/coldfuse.go`
- Test: `internal/gptbridge/coldfuse_test.go`
- Modify: `internal/gptbridge/engine.go` (`EngineOptions`, `start`)

**Interfaces:**
- Produces:
  - `type ColdStartWarning struct { Tokens int64; Starts int; Window time.Duration }`
  - `type ColdStartFuse struct` with `func NewColdStartFuse(warn func(ColdStartWarning)) *ColdStartFuse` and `func (f *ColdStartFuse) Record(tokens int64)`
  - unexported test hooks: fields `now func() time.Time`, `async bool`
  - `EngineOptions.ColdStarts *ColdStartFuse` (nil means off)

- [ ] **Step 1: Write the failing tests** (`coldfuse_test.go`)

```go
package gptbridge

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fuseClock struct{ at time.Time }

func (c *fuseClock) now() time.Time { return c.at }

func newTestFuse(clock *fuseClock) (*ColdStartFuse, *[]ColdStartWarning) {
	var warnings []ColdStartWarning
	fuse := NewColdStartFuse(func(w ColdStartWarning) { warnings = append(warnings, w) })
	fuse.now = clock.now
	fuse.async = false
	return fuse, &warnings
}

func TestColdStartFuseWarnsOnceWhenTheWindowPassesTheThreshold(t *testing.T) {
	clock := &fuseClock{at: time.Unix(1_000_000, 0)}
	fuse, warnings := newTestFuse(clock)
	for i := 0; i < 16; i++ {
		fuse.Record(125_000)
		clock.at = clock.at.Add(30 * time.Second)
	}
	if len(*warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one", *warnings)
	}
	w := (*warnings)[0]
	if w.Tokens <= 2_000_000 || w.Starts < 16 || w.Window != 10*time.Minute {
		t.Fatalf("warning = %+v", w)
	}
}

func TestColdStartFuseStaysQuietBelowTheThreshold(t *testing.T) {
	clock := &fuseClock{at: time.Unix(1_000_000, 0)}
	fuse, warnings := newTestFuse(clock)
	for i := 0; i < 100; i++ {
		fuse.Record(150_000)
		clock.at = clock.at.Add(time.Minute)
	}
	if len(*warnings) != 0 {
		t.Fatalf("1.5M per 10 min warned: %+v", *warnings)
	}
}

func TestColdStartFuseWarnsAgainOnlyAfterAnHour(t *testing.T) {
	clock := &fuseClock{at: time.Unix(1_000_000, 0)}
	fuse, warnings := newTestFuse(clock)
	burst := func() {
		for i := 0; i < 5; i++ {
			fuse.Record(1_000_000)
		}
	}
	burst()
	clock.at = clock.at.Add(30 * time.Minute)
	burst()
	if len(*warnings) != 1 {
		t.Fatalf("warned %d times within the hour", len(*warnings))
	}
	clock.at = clock.at.Add(31 * time.Minute)
	burst()
	if len(*warnings) != 2 {
		t.Fatalf("warned %d times after the hour, want 2", len(*warnings))
	}
}

func TestEngineRecordsAColdStartOnlyWhenItReplaysHistory(t *testing.T) {
	var mu sync.Mutex
	var recorded []int64
	fuse := NewColdStartFuse(func(ColdStartWarning) {})
	fuse.record = func(tokens int64) { mu.Lock(); recorded = append(recorded, tokens); mu.Unlock() }

	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) { completeTextTurn(rpc, threadID, turnID, "ok") }
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), Models: []string{"gpt-test"}, ColdStarts: fuse,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)

	fresh := testTranslation("hi")
	fresh.EstimatedInputTokens = 10
	if _, err := engine.Execute(context.Background(), fresh, nil); err != nil {
		t.Fatal(err)
	}
	replay := testTranslation("again")
	replay.EstimatedInputTokens = 90_000
	replay.History = []map[string]any{{"type": "message", "role": "user",
		"content": []map[string]any{{"type": "input_text", "text": "old"}}}}
	if _, err := engine.Execute(context.Background(), replay, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 || recorded[0] != 90_000 {
		t.Fatalf("recorded = %v, want one cold start of 90000", recorded)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/gptbridge/ -run 'TestColdStartFuse|TestEngineRecordsAColdStart'`
Expected: build failure, `undefined: NewColdStartFuse`.

- [ ] **Step 3: Implement** (`coldfuse.go`)

```go
package gptbridge

import (
	"sync"
	"time"
)

const (
	coldStartWindow    = 10 * time.Minute
	coldStartThreshold = 2_000_000
	coldStartQuiet     = time.Hour
)

// ColdStartWarning reports uncached replays inside one window.
type ColdStartWarning struct {
	Tokens int64
	Starts int
	Window time.Duration
}

// ColdStartFuse counts fresh threads that replay history: each one is sent
// uncached, because Codex scopes the prompt cache to the thread. Legitimate
// traffic stays well under the threshold; a background fork that replays a
// conversation on a timer does not. It only warns, never blocks.
type ColdStartFuse struct {
	mu       sync.Mutex
	warn     func(ColdStartWarning)
	events   []coldStart
	lastWarn time.Time

	now    func() time.Time
	async  bool
	record func(int64) // test hook
}

type coldStart struct {
	at     time.Time
	tokens int64
}

func NewColdStartFuse(warn func(ColdStartWarning)) *ColdStartFuse {
	return &ColdStartFuse{warn: warn, now: time.Now, async: true}
}

// Record is safe on a nil fuse.
func (f *ColdStartFuse) Record(tokens int64) {
	if f == nil {
		return
	}
	if f.record != nil {
		f.record(tokens)
	}
	f.mu.Lock()
	now := f.now()
	f.events = append(f.events, coldStart{at: now, tokens: tokens})
	cut := 0
	for cut < len(f.events) && now.Sub(f.events[cut].at) > coldStartWindow {
		cut++
	}
	f.events = f.events[cut:]
	var total int64
	for _, event := range f.events {
		total += event.tokens
	}
	fire := total > coldStartThreshold && f.warn != nil &&
		(f.lastWarn.IsZero() || now.Sub(f.lastWarn) >= coldStartQuiet)
	warning := ColdStartWarning{Tokens: total, Starts: len(f.events), Window: coldStartWindow}
	if fire {
		f.lastWarn = now
	}
	f.mu.Unlock()
	if !fire {
		return
	}
	if f.async {
		go f.warn(warning)
		return
	}
	f.warn(warning)
}
```

In `engine.go`: add `ColdStarts *ColdStartFuse` to `EngineOptions`. In `start`, inside `if len(translation.History) > 0 {`, before `injectHistory`:

```go
		e.options.ColdStarts.Record(translation.EstimatedInputTokens)
```

- [ ] **Step 4: Run them and see them pass**

Run: `WISP_DECK_TESTING=1 go test ./internal/gptbridge/ -run 'TestColdStartFuse|TestEngineRecordsAColdStart|TestEngine'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gptbridge/coldfuse.go internal/gptbridge/coldfuse_test.go internal/gptbridge/engine.go
git commit -m "feat(gptbridge): count uncached thread replays and warn past a threshold"
```

---

### Task 4: Wire the fuse to a log line and a notification

**Files:**
- Modify: `internal/gptbridge/adapter.go` (`AdapterOptions`, `buildAppServerBundle`, `finishAppServerBundle`, `RunAdapter`)
- Modify: `internal/gptbridge/chatgptbridge.go` (`ChatGPTBridgeOptions`, its bundle builder)
- Modify: `cmd/wisp-deck-tui/host_effects.go`, `cmd/wisp-deck-tui/host_effects_test.go`
- Create: `cmd/wisp-deck-tui/gptbridge_cold.go`, `cmd/wisp-deck-tui/gptbridge_cold_test.go`
- Modify: `cmd/wisp-deck-tui/claude_allin.go`, `cmd/wisp-deck-tui/claude_gpt_adapter.go`

**Interfaces:**
- Consumes: `gptbridge.NewColdStartFuse`, `gptbridge.ColdStartWarning`.
- Produces:
  - `AdapterOptions.ColdStarts` / `ChatGPTBridgeOptions.ColdStarts *ColdStartFuse`; `finishAppServerBundle(server, privateCWD, shutdownTimeout, coldStarts)`.
  - `claudeBackgroundNotificationKind` value `gptBridgeColdRebuildNotification`; `func newGPTBridgeColdWarningHostEffect() hostEffect`.
  - `func newGPTBridgeColdStartFuse(logPath string) *gptbridge.ColdStartFuse`; `func formatGPTBridgeColdLine(at time.Time, w gptbridge.ColdStartWarning) string`.

- [ ] **Step 1: Write the failing tests**

In `host_effects_test.go`:

```go
func TestHostEffectGPTBridgeColdWarningIsFixed(t *testing.T) {
	plan, ok := planHostEffect(newGPTBridgeColdWarningHostEffect(), []string{"HOME=/tmp/home"})
	if !ok || plan.executable != "/usr/bin/osascript" {
		t.Fatalf("plan = %+v, ok = %v", plan, ok)
	}
	environment := strings.Join(plan.environment, "\n")
	for _, required := range []string{
		"WISP_DECK_NOTIFICATION_TITLE=GPT bridge",
		"WISP_DECK_NOTIFICATION_BODY=Context re-sent without cache — see gptbridge-cold.log",
	} {
		if strings.Count(environment, required) != 1 {
			t.Fatalf("environment %q lacks %q", environment, required)
		}
	}
}
```

`gptbridge_cold_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

func TestFormatGPTBridgeColdLineCarriesTheNumbers(t *testing.T) {
	line := formatGPTBridgeColdLine(time.Date(2026, 9, 28, 4, 5, 0, 0, time.UTC),
		gptbridge.ColdStartWarning{Tokens: 3_400_000, Starts: 27, Window: 10 * time.Minute})
	for _, want := range []string{"2026-09-28T04:05:00Z", "3400000", "27", "10m0s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("line %q is not newline-terminated", line)
	}
}

func TestGPTBridgeColdWarningWritesNothingUnderTest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gptbridge-cold.log")
	warnGPTBridgeCold(path, gptbridge.ColdStartWarning{Tokens: 3_000_000, Starts: 20, Window: 10 * time.Minute})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a test binary wrote the host log: %v", err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `WISP_DECK_TESTING=1 go test ./cmd/wisp-deck-tui/ -run 'TestHostEffectGPTBridgeColdWarningIsFixed|TestFormatGPTBridgeColdLine|TestGPTBridgeColdWarning'`
Expected: build failure, `undefined: newGPTBridgeColdWarningHostEffect`.

- [ ] **Step 3: Implement**

`host_effects.go`:
- Add `gptBridgeColdRebuildNotification` as the last `claudeBackgroundNotificationKind` const.
- Add:

```go
func newGPTBridgeColdWarningHostEffect() hostEffect {
	return hostEffect{
		kind:             hostEffectClaudeBackgroundNotification,
		notificationKind: gptBridgeColdRebuildNotification,
	}
}
```

- In `claudeBackgroundNotificationBody` add `case gptBridgeColdRebuildNotification: return "Context re-sent without cache — see gptbridge-cold.log", true`.
- In `planHostEffect`'s notification case, pick the title by kind instead of the literal:

```go
		title := "Claude background"
		if effect.notificationKind == gptBridgeColdRebuildNotification {
			title = "GPT bridge"
		}
```

  Pass `title` to `hostEffectEnvironment`. Leave the osascript and `display notification` literals as they are, once each.

`gptbridge_cold.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

const gptBridgeColdNotifyTimeout = 5 * time.Second

// gptBridgeColdLog lives beside the rest of wisp-deck's config.
func gptBridgeColdLog(configDir string) string {
	return filepath.Join(configDir, "gptbridge-cold.log")
}

func newGPTBridgeColdStartFuse(logPath string) *gptbridge.ColdStartFuse {
	return gptbridge.NewColdStartFuse(func(w gptbridge.ColdStartWarning) {
		warnGPTBridgeCold(logPath, w)
	})
}

// warnGPTBridgeCold writes one log line and shows the fixed notification.
// Both are host effects, so a test binary does neither.
func warnGPTBridgeCold(logPath string, w gptbridge.ColdStartWarning) {
	if !currentHostEffectsDecision().Allowed {
		return
	}
	if file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = file.WriteString(formatGPTBridgeColdLine(time.Now().UTC(), w))
		_ = file.Close()
	}
	if runtime.GOOS != "darwin" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gptBridgeColdNotifyTimeout)
	defer cancel()
	_ = runHostEffect(ctx, newGPTBridgeColdWarningHostEffect())
}

func formatGPTBridgeColdLine(at time.Time, w gptbridge.ColdStartWarning) string {
	return fmt.Sprintf("%s uncached_tokens=%d cold_starts=%d window=%s\n",
		at.Format(time.RFC3339), w.Tokens, w.Starts, w.Window)
}
```

`internal/gptbridge`:
- Add `ColdStarts *ColdStartFuse` to `AdapterOptions` and `ChatGPTBridgeOptions`.
- Give `finishAppServerBundle` a `coldStarts *ColdStartFuse` parameter and pass it into `EngineOptions{…, ColdStarts: coldStarts}`.
- Update its three callers: `buildAppServerBundle` (`options.ColdStarts`), `RunAdapter` (`options.ColdStarts`), and `chatgptbridge.go` (`b.options.ColdStarts`). The fuse lives in the options, so every engine a `ResilientExecutor` rebuilds shares it.

`cmd` wiring:
- `claude_allin.go`, in `newChatGPTBridge`: the function takes `codexPath` only. Change it to `newChatGPTBridge(codexPath string, coldStarts *gptbridge.ColdStartFuse)`. Build the fuse in `RunE` as `newGPTBridgeColdStartFuse(gptBridgeColdLog(filepath.Dir(env.AccountsList)))` and pass it in. Update the `newBridge` function type and its test fakes to the new signature.
- `claude_gpt_adapter.go`: set `ColdStarts: newGPTBridgeColdStartFuse(gptBridgeColdLog(filepath.Join(home, ".config", "wisp-deck")))` in the `AdapterOptions` literal, with `home, _ := os.UserHomeDir()`.

- [ ] **Step 4: Run tests, the host-effect audits and the build**

```bash
WISP_DECK_TESTING=1 go test ./internal/gptbridge/ ./cmd/wisp-deck-tui/ 2>&1 | tail -5
go build -o /dev/null ./cmd/wisp-deck-tui/
WISP_DECK_TESTING=1 go test ./test/bash/ -timeout 20m -run 'HostEffect|IdleSoundRuntimeSitesUseSharedLiveGate|MainMenuSoundPreviewOwnershipGuardRejectsBypasses'
```

Expected: all PASS. If an audit names a rule, satisfy that rule rather than edit the audit; if it must change, record a Ruling.

- [ ] **Step 5: Commit**

```bash
git add internal/gptbridge/adapter.go internal/gptbridge/chatgptbridge.go cmd/wisp-deck-tui/host_effects.go cmd/wisp-deck-tui/host_effects_test.go cmd/wisp-deck-tui/gptbridge_cold.go cmd/wisp-deck-tui/gptbridge_cold_test.go cmd/wisp-deck-tui/claude_allin.go cmd/wisp-deck-tui/claude_gpt_adapter.go
git add <any updated *_test.go fakes>
git commit -m "feat(gptbridge): warn once an hour when replays go uncached"
```

---

### Task 5: Keep a finished thread for the next message

**Files:**
- Create: `internal/gptbridge/parking.go`
- Test: `internal/gptbridge/parking_test.go`
- Modify: `internal/gptbridge/engine.go` (`engineTurn`, `EngineOptions`, `Engine`, `start`, `resume`, `runTurnBoundary`, `Close`)
- Modify: `internal/gptbridge/engine_test.go` (`TestEngineCompletesTextTurnAndDeletesThread`)
- Modify: `internal/gptbridge/CLAUDE.md` (new section)

**Interfaces:**
- Produces:
  - `EngineOptions.ParkLimit int` (default 16), `EngineOptions.ParkIdle time.Duration` (default 30m)
  - `engineTurn` fields: `fingerprint string`, `lastInput [sha256.Size]byte`, `expectInput bool`, `parkTimer *time.Timer`
  - `Engine` field: `parked []*engineTurn` (oldest first)
  - `func threadFingerprint(t Translation) string`, `func inputHistoryItem(input []UserInput) map[string]any`
  - `func (e *Engine) park(state *engineTurn)`, `func (e *Engine) takeParked(t Translation) *engineTurn`, `func (e *Engine) evictParked(state *engineTurn)`

Reuse rule (spec, Layer 2), for a request without tool results:
1. `threadFingerprint` equal (model + system + tool directive + dynamic tools JSON);
2. incoming history digests start with `state.history`;
3. if `state.expectInput`, the next item's digest equals `state.lastInput` (the digest of `inputHistoryItem` of the parked turn's input);
4. every remaining item is `function_call`, `function_call_output`, or a `message` with role `assistant`.

- [ ] **Step 1: Write the failing tests** (`parking_test.go`)

```go
package gptbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func userItem(text string) map[string]any {
	return inputHistoryItem([]UserInput{{Type: "text", Text: text}})
}

func assistantItem(text string) map[string]any {
	return map[string]any{"type": "message", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text}}}
}

func parkingEngine(t *testing.T, rpc *fakeEngineRPC, limit int, idle time.Duration) *Engine {
	t.Helper()
	rpc.onTurnStart = func(threadID, turnID string) { completeTextTurn(rpc, threadID, turnID, "ok") }
	engine, err := NewEngine(rpc, EngineOptions{
		PrivateCWD: t.TempDir(), Models: []string{"gpt-test"},
		ToolBatchWindow: 5 * time.Millisecond, ParkLimit: limit, ParkIdle: idle,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	return engine
}

func parkCalls(rpc *fakeEngineRPC, method string) int {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	n := 0
	for _, call := range rpc.calls {
		if call == method {
			n++
		}
	}
	return n
}

func parkRun(t *testing.T, engine *Engine, translation Translation) {
	t.Helper()
	if _, err := engine.Execute(context.Background(), translation, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEngineReusesAParkedThreadForTheNextMessage(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, time.Hour)
	parkRun(t, engine, testTranslation("first"))
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	parkRun(t, engine, next)
	if got := parkCalls(rpc, "thread/start"); got != 1 {
		t.Fatalf("thread/start = %d, want the thread reused", got)
	}
	if got := parkCalls(rpc, "thread/inject_items"); got != 0 {
		t.Fatalf("a reused thread replayed history %d times", got)
	}
	if got := parkCalls(rpc, "thread/delete"); got != 0 {
		t.Fatalf("thread/delete = %d, want the thread kept", got)
	}
}

func TestEngineStartsANewThreadWhenAnythingDiffers(t *testing.T) {
	cases := map[string]func(next *Translation){
		"model":        func(n *Translation) { n.Model = "gpt-other" },
		"system":       func(n *Translation) { n.System = "changed" },
		"tools":        func(n *Translation) { n.DynamicTools = nil },
		"edited input": func(n *Translation) { n.History[0] = userItem("rewritten") },
		"extra user":   func(n *Translation) { n.History = append(n.History, userItem("queued")) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rpc := newFakeEngineRPC()
			engine := parkingEngine(t, rpc, 16, time.Hour)
			engine.models["gpt-other"] = true
			parkRun(t, engine, testTranslation("first"))
			next := testTranslation("second")
			next.History = []map[string]any{userItem("first"), assistantItem("ok")}
			mutate(&next)
			parkRun(t, engine, next)
			if got := parkCalls(rpc, "thread/start"); got != 2 {
				t.Fatalf("thread/start = %d, want a new thread", got)
			}
		})
	}
}

func TestEngineDeletesParkedThreadsPastTheLimit(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 2, time.Hour)
	for _, text := range []string{"a", "b", "c"} {
		parkRun(t, engine, testTranslation(text))
	}
	if got := parkCalls(rpc, "thread/delete"); got != 1 {
		t.Fatalf("thread/delete = %d, want the oldest of three dropped", got)
	}
}

func TestEngineDeletesAnIdleParkedThread(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, 20*time.Millisecond)
	parkRun(t, engine, testTranslation("a"))
	deadline := time.Now().Add(2 * time.Second)
	for parkCalls(rpc, "thread/delete") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("an idle parked thread was never deleted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEngineHandsAParkedThreadToOneRequestOnly(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, time.Hour)
	parkRun(t, engine, testTranslation("first"))
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	a := engine.takeParked(next)
	b := engine.takeParked(next)
	if a == nil || b != nil {
		t.Fatalf("takeParked twice gave %v and %v, want one thread then none", a, b)
	}
	engine.cleanupTurn(a, false)
}

func TestEngineDropsAStrayEventForAParkedThread(t *testing.T) {
	rpc := newFakeEngineRPC()
	engine := parkingEngine(t, rpc, 16, time.Hour)
	parkRun(t, engine, testTranslation("first"))
	rpc.notifications <- notification("item/agentMessage/delta", "thread-1", "turn-stale",
		`"itemId":"late","delta":"stale"`)
	time.Sleep(20 * time.Millisecond)
	next := testTranslation("second")
	next.History = []map[string]any{userItem("first"), assistantItem("ok")}
	message, err := engine.Execute(context.Background(), next, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(message.Content)
	if string(raw) != `[{"type":"text","text":"ok"}]` {
		t.Fatalf("content = %s, want only the new turn's text", raw)
	}
}

func TestEngineNeverParksAFailedTurn(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		rpc.notifications <- Notification{Method: "turn/completed", Params: json.RawMessage(
			`{"threadId":"` + threadID + `","turn":{"id":"` + turnID + `","status":"failed","error":{"message":"boom"},"items":[]}}`)}
	}
	engine, err := NewEngine(rpc, EngineOptions{PrivateCWD: t.TempDir(), Models: []string{"gpt-test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	_, _ = engine.Execute(context.Background(), testTranslation("a"), nil)
	engine.mu.Lock()
	parked := len(engine.parked)
	engine.mu.Unlock()
	if parked != 0 {
		t.Fatalf("a failed turn was parked")
	}
}
```

Change `TestEngineCompletesTextTurnAndDeletesThread` in `engine_test.go`: rename it `TestEngineCompletesTextTurnAndParksThread`. Replace its final-call check with: no `thread/delete` yet, and `len(engine.parked) == 1` under `engine.mu`. Ledger this as a Ruling: the spec replaces delete-on-end_turn with park.

- [ ] **Step 2: Run them and see them fail**

Run: `WISP_DECK_TESTING=1 go test ./internal/gptbridge/ -run 'TestEngineReusesAParked|TestEngineStartsANewThread|TestEngineDeletesParked|TestEngineDeletesAnIdle|TestEngineHandsAParked|TestEngineDropsAStray|TestEngineNeverParks|TestEngineCompletesTextTurnAndParks'`
Expected: build failure, `undefined: inputHistoryItem` / `unknown field ParkLimit`.

- [ ] **Step 3: Implement**

`parking.go`:

```go
package gptbridge

import (
	"crypto/sha256"
	"encoding/json"
	"time"
)

const (
	defaultParkLimit = 16
	defaultParkIdle  = 30 * time.Minute
)

// inputHistoryItem is the history item translate builds from this same user
// message on the next request, so its digest can be compared with that one.
func inputHistoryItem(input []UserInput) map[string]any {
	content := make([]map[string]any, 0, len(input))
	for _, item := range input {
		switch item.Type {
		case "text":
			content = append(content, map[string]any{"type": "input_text", "text": item.Text})
		case "image":
			content = append(content, map[string]any{"type": "input_image", "image_url": item.URL, "detail": "auto"})
		}
	}
	return map[string]any{"type": "message", "role": "user", "content": content}
}

// threadFingerprint covers everything fixed at thread/start: a thread started
// with other instructions or tools cannot serve the request.
func threadFingerprint(t Translation) string {
	tools, _ := json.Marshal(t.DynamicTools)
	sum := sha256.Sum256([]byte(t.Model + "\x00" + t.System + "\x00" + t.ToolDirective + "\x00" + string(tools)))
	return string(sum[:])
}

// park keeps a finished thread for the next message: Codex scopes the prompt
// cache to the thread, so a new one replays the whole history uncached.
// Caller holds state.mu.
func (e *Engine) park(state *engineTurn) {
	e.mu.Lock()
	delete(e.turns, state.threadID)
	e.parked = append(e.parked, state)
	var evict *engineTurn
	if len(e.parked) > e.options.ParkLimit {
		evict, e.parked = e.parked[0], e.parked[1:]
	}
	state.parkTimer = time.AfterFunc(e.options.ParkIdle, func() { e.evictParked(state) })
	e.mu.Unlock()
	if evict != nil {
		e.evictParked(evict)
	}
}

// takeParked removes and returns the parked thread this request extends.
func (e *Engine) takeParked(t Translation) *engineTurn {
	if len(t.ToolResults) > 0 {
		return nil
	}
	fingerprint := threadFingerprint(t)
	digests := historyDigests(t.History)
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := len(e.parked) - 1; index >= 0; index-- {
		state := e.parked[index]
		if state.fingerprint != fingerprint || !extendsParked(state, t.History, digests) {
			continue
		}
		e.parked = append(e.parked[:index], e.parked[index+1:]...)
		if state.parkTimer != nil {
			state.parkTimer.Stop()
			state.parkTimer = nil
		}
		return state
	}
	return nil
}

func extendsParked(state *engineTurn, history []map[string]any, digests [][sha256.Size]byte) bool {
	if len(digests) < len(state.history) {
		return false
	}
	for index, digest := range state.history {
		if digests[index] != digest {
			return false
		}
	}
	rest := len(state.history)
	if state.expectInput {
		if rest >= len(digests) || digests[rest] != state.lastInput {
			return false
		}
		rest++
	}
	for _, item := range history[rest:] {
		switch item["type"] {
		case "function_call", "function_call_output":
		case "message":
			if item["role"] != "assistant" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// evictParked deletes a parked thread. It does nothing for a thread that is
// no longer in the pool: an idle timer that fires just as takeParked hands the
// thread out must not delete a thread that is now running a turn.
func (e *Engine) evictParked(state *engineTurn) {
	e.mu.Lock()
	found := false
	for index, parked := range e.parked {
		if parked == state {
			e.parked = append(e.parked[:index], e.parked[index+1:]...)
			found = true
			break
		}
	}
	if found && state.parkTimer != nil {
		state.parkTimer.Stop()
		state.parkTimer = nil
	}
	e.mu.Unlock()
	if found {
		e.cleanupTurn(state, false)
	}
}
```

`Close` drops the pool with `evictParked`. A test that takes a thread with `takeParked` and does not run it gives it back with `e.cleanupTurn(state, false)`.

`engine.go`:
- `EngineOptions`: add `ParkLimit int` and `ParkIdle time.Duration`; default them in `NewEngine` like `PendingTTL` (`<= 0` → `defaultParkLimit` / `defaultParkIdle`).
- `Engine`: add `parked []*engineTurn`.
- `engineTurn`: add `fingerprint string`, `lastInput [sha256.Size]byte`, `expectInput bool`, `parkTimer *time.Timer`.
- `start`: at the very top, before `threadParams`:

```go
	if state := e.takeParked(translation); state != nil {
		return e.continueParked(ctx, state, translation, emit)
	}
```

  After creating `state`, set `state.fingerprint = threadFingerprint(translation)`, `state.lastInput = historyDigests([]map[string]any{inputHistoryItem(translation.Input)})[0]`, `state.expectInput = true`.
- New method in `parking.go`:

```go
// continueParked runs the next turn on a parked thread: only the new input
// is sent, and the prefix is already in this thread's cache.
func (e *Engine) continueParked(ctx context.Context, state *engineTurn, translation Translation, emit func([]StreamEvent) error) (AnthropicMessage, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	// Fresh channels: anything the app-server sent while the thread was
	// parked belongs to no turn of ours.
	state.events = make(chan Notification, 256)
	state.requests = make(chan ServerRequest, 64)
	state.errors = make(chan error, 1)
	state.pending = make(map[string]*pendingDynamicTool)
	state.history = historyDigests(translation.History)
	state.lastInput = historyDigests([]map[string]any{inputHistoryItem(translation.Input)})[0]
	state.expectInput = true
	e.mu.Lock()
	e.turns[state.threadID] = state
	e.mu.Unlock()
	turnParams := map[string]any{
		"threadId": state.threadID, "input": translation.Input, "model": translation.Model,
		"approvalPolicy": "never", "environments": []any{},
		"sandboxPolicy": map[string]any{"type": "readOnly", "networkAccess": false},
	}
	if translation.Effort != "" {
		turnParams["effort"] = translation.Effort
	}
	var turnStarted struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := e.rpc.Call(ctx, "turn/start", turnParams, &turnStarted); err != nil {
		e.cleanupTurn(state, true)
		return AnthropicMessage{}, fmt.Errorf("start Codex turn: %w", err)
	}
	if turnStarted.Turn.ID == "" {
		e.cleanupTurn(state, true)
		return AnthropicMessage{}, errors.New("turn/start response is missing turn id")
	}
	state.turnID = turnStarted.Turn.ID
	return e.runTurnBoundary(ctx, state, translation, emit)
}
```

  (`parking.go` then imports `context`, `errors`, `fmt`.)
- `resume`: after `adoptHistory` succeeds, set `state.expectInput = false`. A continuation's own supplemental input would be a user item after the prefix; mark such a turn unparkable with `if len(translation.Input) > 0 { state.fingerprint = "" }` (an empty fingerprint never matches).
- `runTurnBoundary`, the end-of-turn branch: replace

```go
			if message, err := reducer.Message(); err == nil {
				e.cleanupTurn(state, false)
				return message, nil
			}
```

  with

```go
			if message, err := reducer.Message(); err == nil {
				if message.StopReason == "end_turn" && state.fingerprint != "" {
					e.park(state)
				} else {
					e.cleanupTurn(state, false)
				}
				return message, nil
			}
```

- `Close`: after cleaning `e.turns`, take a copy of `e.parked` under `e.mu`, and call `e.evictParked` for each.

Add a section to `internal/gptbridge/CLAUDE.md`:

```markdown
### A new thread is an uncached thread

Codex scopes the prompt cache to the thread: `prompt_cache_key` is the session
id, and the ChatGPT backend picks the cache shard from it. Measured on a live
app-server, a byte-identical 37K-token history got 99.7% cached as the next
turn of its own thread, and 0% in a new thread. Every `thread/start` that
replays history is therefore a full-price replay.

Three things follow:

- **Claude Code's subagent progress summary is answered locally**
  (`agentsummary.go`). It forks every running subagent every 30s; through the
  bridge each fork replayed ~125K tokens for 3-5 words, 5,531 times in 30
  hours. `WISP_DECK_LIVE_AGENT_SUMMARY_E2E` checks the prompt after a claude
  upgrade.
- **A finished thread is parked** (`parking.go`) and the next message that
  extends it exactly runs as a new turn on it. Anything that differs starts a
  new thread, so correctness never depends on the pool.
- **`ColdStartFuse` counts replays** and warns past 2M tokens in 10 minutes,
  through a log line in `gptbridge-cold.log` and one fixed notification an
  hour. A new background fork shows up there instead of in the quota.
```

- [ ] **Step 4: Run the new tests, the whole package, and vet**

```bash
WISP_DECK_TESTING=1 go test -race ./internal/gptbridge/ 2>&1 | tail -5
go vet ./internal/gptbridge/
WISP_DECK_TESTING=1 go test ./test/bash/ -timeout 20m -run 'HostEffectOwnership'
```

Expected: PASS (the last line checks the audited CLAUDE.md prose).

- [ ] **Step 5: Commit**

```bash
git add internal/gptbridge/parking.go internal/gptbridge/parking_test.go internal/gptbridge/engine.go internal/gptbridge/engine_test.go internal/gptbridge/CLAUDE.md
git commit -m "fix(gptbridge): keep a finished thread so the next message reads the cache"
```

---

### Task 6: Live verification and push

- [ ] **Step 1: Build and check the whole touched surface**

```bash
go build -o /dev/null ./cmd/wisp-deck-tui/
WISP_DECK_TESTING=1 go test ./internal/gptbridge/ ./cmd/wisp-deck-tui/ 2>&1 | tail -3
WISP_DECK_LIVE_AGENT_SUMMARY_E2E=1 go test ./internal/gptbridge/ -run TestLiveClaudeStillSendsTheAgentSummaryPrompt -v
```

- [ ] **Step 2: Live parking check (costs 2 short turns)**

Write a throwaway env-gated test (not committed) that starts a real app-server through `NewEngine` with a fuse. Run `Execute` twice: the second request's history is the first input plus the first reply. Log both turns' usage. Expected: the second turn reports a high `cache_read_input_tokens` and no second `thread/start`. Delete the throwaway file.

- [ ] **Step 3: Push**

```bash
git pull --rebase && git push
```

- [ ] **Step 4: Tell the user how to take it live:** the running panes use the installed `wisp-deck-tui`, so the fix reaches them after a local install or a release plus a pane relaunch. After that, a session with subagents on a GPT row should show no progress-summary threads in `~/.codex/logs_2.sqlite` (the classification script from the investigation).
