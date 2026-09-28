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
		"Reading engine.go":        callItem("Read", `{"file_path":"/repo/internal/engine.go"}`),
		"Editing proxy.go":         callItem("Edit", `{"file_path":"/repo/proxy.go","old_string":"a","new_string":"b"}`),
		"Writing notes.md":         callItem("Write", `{"file_path":"/tmp/notes.md","content":"x"}`),
		"Editing book.ipynb":       callItem("NotebookEdit", `{"notebook_path":"/r/book.ipynb"}`),
		"Searching for cold start": callItem("Grep", `{"pattern":"cold start"}`),
		"Searching for **/*.go":    callItem("Glob", `{"pattern":"**/*.go"}`),
		"Run the bridge tests":     callItem("Bash", `{"command":"go test","description":"Run the bridge tests"}`),
		"Running a shell command":  callItem("Bash", `{"command":"ls"}`),
		"Using WebFetch":           callItem("WebFetch", `{"url":"https://x"}`),
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
