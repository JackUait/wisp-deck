package gptbridge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseReducerTextSSESequenceAndMessage(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_test", Model: "gpt-5.6-terra", EstimatedInputTokens: 11,
	})
	var events []StreamEvent
	events = append(events, reducer.Start()...)
	for _, delta := range []string{"hel", "lo"} {
		got, err := reducer.Apply(Notification{
			Method: "item/agentMessage/delta",
			Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","delta":"` + delta + `"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	usage, err := reducer.Apply(Notification{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{
			"threadId":"thread-1","turnId":"turn-1",
			"tokenUsage":{"last":{"inputTokens":14,"cachedInputTokens":3,"outputTokens":2,"reasoningOutputTokens":0,"totalTokens":16}}
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, usage...)
	final, err := reducer.Finish("end_turn")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, final...)

	wantNames := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	if len(events) != len(wantNames) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(wantNames), events)
	}
	for index, want := range wantNames {
		if events[index].Event != want {
			t.Errorf("event[%d] = %q, want %q", index, events[index].Event, want)
		}
	}

	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Type != "text" || message.Content[0].Text != "hello" {
		t.Fatalf("message content = %+v", message.Content)
	}
	if message.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q", message.StopReason)
	}
	if message.Usage.InputTokens != 11 || message.Usage.CacheReadInputTokens != 3 || message.Usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v", message.Usage)
	}

	var sse bytes.Buffer
	if err := WriteSSE(&sse, events); err != nil {
		t.Fatal(err)
	}
	text := sse.String()
	for _, want := range []string{
		"event: message_start\n",
		`"delta":{"text":"hel","type":"text_delta"}`,
		`"stop_reason":"end_turn"`,
		"event: message_stop\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SSE missing %q:\n%s", want, text)
		}
	}
}

func TestResponseReducerToolCalls(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_tools", Model: "gpt-5.6-sol", EstimatedInputTokens: 20,
	})
	events := reducer.Start()
	textEvents, err := reducer.Apply(Notification{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"threadId":"t","turnId":"v","itemId":"text","delta":"Checking."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, textEvents...)
	for _, call := range []DynamicToolCall{
		{ID: "bridge_1", Name: "Read", Arguments: json.RawMessage(`{"path":"a"}`)},
		{ID: "bridge_2", Name: "Glob", Arguments: json.RawMessage(`{"pattern":"*.go"}`)},
	} {
		got, err := reducer.AddToolCall(call)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	final, err := reducer.Finish("tool_use")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, final...)

	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 3 {
		t.Fatalf("content = %+v", message.Content)
	}
	if message.Content[1].Type != "tool_use" || message.Content[1].ID != "bridge_1" || message.Content[1].Name != "Read" {
		t.Fatalf("first tool = %+v", message.Content[1])
	}
	if string(message.Content[2].Input) != `{"pattern":"*.go"}` {
		t.Fatalf("second tool input = %s", message.Content[2].Input)
	}
	if message.StopReason != "tool_use" {
		t.Fatalf("stop reason = %q", message.StopReason)
	}

	var sse bytes.Buffer
	if err := WriteSSE(&sse, events); err != nil {
		t.Fatal(err)
	}
	text := sse.String()
	for _, want := range []string{
		`"content_block":{"id":"bridge_1","input":{},"name":"Read","type":"tool_use"}`,
		`"delta":{"partial_json":"{\"path\":\"a\"}","type":"input_json_delta"}`,
		`"content_block":{"id":"bridge_2","input":{},"name":"Glob","type":"tool_use"}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SSE missing %q:\n%s", want, text)
		}
	}
}

func TestResponseReducerThinkingWhenRequested(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_think", Model: "gpt-5.6-sol",
		IncludeThinking: true, EstimatedInputTokens: 1,
	})
	events := reducer.Start()
	got, err := reducer.Apply(Notification{
		Method: "item/reasoning/summaryTextDelta",
		Params: json.RawMessage(`{"threadId":"t","turnId":"v","itemId":"r","summaryIndex":0,"delta":"Consider."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, got...)
	final, err := reducer.Finish("end_turn")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, final...)

	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Type != "thinking" ||
		message.Content[0].Thinking != "Consider." || message.Content[0].Signature == "" {
		t.Fatalf("thinking content = %+v", message.Content)
	}
	var sse bytes.Buffer
	if err := WriteSSE(&sse, events); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sse.String(), `"delta":{"thinking":"Consider.","type":"thinking_delta"}`) ||
		!strings.Contains(sse.String(), `"type":"signature_delta"`) {
		t.Fatalf("thinking SSE:\n%s", sse.String())
	}
}

func TestResponseReducerIgnoresThinkingWhenNotRequested(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_no_think", Model: "gpt-5.6-terra", EstimatedInputTokens: 1,
	})
	_ = reducer.Start()
	events, err := reducer.Apply(Notification{
		Method: "item/reasoning/summaryTextDelta",
		Params: json.RawMessage(`{"threadId":"t","turnId":"v","itemId":"r","summaryIndex":0,"delta":"hidden"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestResponseReducerTurnFailure(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_error", Model: "gpt-5.6-terra", EstimatedInputTokens: 1,
	})
	_ = reducer.Start()
	_, err := reducer.Apply(Notification{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"t",
			"turn":{"id":"v","status":"failed","items":[],"error":{"message":"upstream failed"}}
		}`),
	})
	if err == nil || !strings.Contains(err.Error(), "upstream failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestResponseReducerRejectsCrossTurnEvents(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_scope", Model: "gpt-5.6-terra", EstimatedInputTokens: 1,
		ThreadID: "thread-a", TurnID: "turn-a",
	})
	_ = reducer.Start()
	_, err := reducer.Apply(Notification{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"threadId":"thread-b","turnId":"turn-a","itemId":"x","delta":"bad"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "different thread") {
		t.Fatalf("error = %v", err)
	}
}

func TestResponseReducerRequiresUsageOrEstimate(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_usage", Model: "gpt-5.6-terra",
	})
	_ = reducer.Start()
	if _, err := reducer.Finish("end_turn"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("Finish error = %v", err)
	}
}

// Codex can report a webSearch action without a display detail, yielding an
// empty query. The search still happened and must remain countable rather than
// aborting the nested Claude Code WebSearch request with a 502.
func TestResponseReducerReportsWebSearchWithEmptyDisplayQuery(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_search", Model: "gpt-5.6-terra",
		ThreadID: "thread-1", TurnID: "turn-1", EstimatedInputTokens: 1,
	})
	for _, method := range []string{"item/started", "item/completed"} {
		_, err := reducer.Apply(Notification{
			Method: method,
			Params: json.RawMessage(`{
				"threadId":"thread-1","turnId":"turn-1",
				"item":{"id":"ws-empty","type":"webSearch","query":"","action":{"type":"other"}}
			}`),
		})
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	if _, err := reducer.Finish("end_turn"); err != nil {
		t.Fatal(err)
	}
	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 2 || message.Content[0].Type != "server_tool_use" ||
		message.Content[1].Type != "web_search_tool_result" {
		t.Fatalf("content = %+v", message.Content)
	}
	var input map[string]string
	if err := json.Unmarshal(message.Content[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if input["query"] != "web search" {
		t.Fatalf("server tool input = %v", input)
	}
	if message.Usage.OutputTokens == 0 {
		t.Fatal("fallback usage omitted server_tool_use output")
	}
}

// Codex saves every image it generates to a file and reports the absolute
// location in the item's savedPath. That path is the deliverable: Claude Code
// can read it, show it, copy it into the project, and hand it back as the input
// image of the next edit turn. Dropping it stranded a picture that exists on
// disk behind the claim that it "was discarded".
func TestResponseReducerReportsCodexImageSavedPath(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_img", Model: "gpt-5.6-terra", EstimatedInputTokens: 5,
	})
	saved := "/Users/dev/.codex/generated_images/019fc333/exec-b2e9851e.png"
	for method, params := range map[string]string{
		"item/started": `{"threadId":"thread-1","turnId":"turn-1","item":{"id":"img",` +
			`"type":"imageGeneration","status":"in_progress","result":""}}`,
		"item/completed": `{"threadId":"thread-1","turnId":"turn-1","item":{"id":"img",` +
			`"type":"imageGeneration","status":"completed",` +
			`"revisedPrompt":"a red apple on wood","result":"success",` +
			`"savedPath":"` + saved + `"}}`,
	} {
		if _, err := reducer.Apply(Notification{
			Method: method, Params: json.RawMessage(params),
		}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	if _, err := reducer.Finish("end_turn"); err != nil {
		t.Fatal(err)
	}
	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	var joined string
	for _, block := range message.Content {
		if block.Type != "text" {
			t.Fatalf("unexpected block type %q: %+v", block.Type, message.Content)
		}
		joined += block.Text
	}
	if !strings.Contains(joined, saved) {
		t.Fatalf("saved image path was not reported: %q", joined)
	}
	if strings.Contains(joined, "discarded") {
		t.Fatalf("an image that exists on disk was reported as discarded: %q", joined)
	}
	if !strings.Contains(joined, "a red apple on wood") {
		t.Fatalf("revised prompt was dropped: %q", joined)
	}
}

// A completed item always names a file; one that ends any other way names none.
// Reporting that as "generated an image" would be the same lie in the other
// direction, so the reducer reports the status the item itself carries.
func TestResponseReducerReportsFailedCodexImageGeneration(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_img", Model: "gpt-5.6-terra", EstimatedInputTokens: 5,
	})
	if _, err := reducer.Apply(Notification{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1",` +
			`"item":{"id":"img","type":"imageGeneration","status":"failed","result":""}}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reducer.Finish("end_turn"); err != nil {
		t.Fatal(err)
	}
	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	var joined string
	for _, block := range message.Content {
		joined += block.Text
	}
	if !strings.Contains(joined, "failed") {
		t.Fatalf("failed image generation was not reported as failed: %q", joined)
	}
	if strings.Contains(joined, "generated an image") {
		t.Fatalf("a failed generation was reported as a produced image: %q", joined)
	}
}

// Without a savedPath there is nothing to hand over, so the reducer must still
// say so rather than let the model's "Generated the image." prose stand with
// nothing behind it.
func TestResponseReducerSurfacesCodexImageGeneration(t *testing.T) {
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: "msg_img", Model: "gpt-5.6-terra", EstimatedInputTokens: 5,
	})
	for _, params := range []string{
		`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"img",` +
			`"type":"imageGeneration","status":"in_progress","result":""}}`,
		`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"img",` +
			`"type":"imageGeneration","status":"completed",` +
			`"revisedPrompt":"a red apple on wood","result":"iVBORw0KGgoAAAANS"}}`,
	} {
		method := "item/started"
		if strings.Contains(params, "completed") {
			method = "item/completed"
		}
		if _, err := reducer.Apply(Notification{
			Method: method, Params: json.RawMessage(params),
		}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	if _, err := reducer.Finish("end_turn"); err != nil {
		t.Fatal(err)
	}
	message, err := reducer.Message()
	if err != nil {
		t.Fatal(err)
	}
	var joined string
	for _, block := range message.Content {
		if block.Type != "text" {
			t.Fatalf("unexpected block type %q: %+v", block.Type, message.Content)
		}
		joined += block.Text
	}
	if !strings.Contains(joined, "image") || !strings.Contains(joined, "a red apple on wood") {
		t.Fatalf("image generation was not surfaced: %q", joined)
	}
	if strings.Contains(joined, "iVBORw0KGgoAAAANS") {
		t.Fatalf("base64 image payload leaked into the transcript: %q", joined)
	}
}

func TestWriteSSEErrorEvent(t *testing.T) {
	var output bytes.Buffer
	err := WriteSSE(&output, []StreamEvent{AnthropicErrorEvent("api_error", "broken")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "event: error\n") ||
		!strings.Contains(output.String(), `"error":{"message":"broken","type":"api_error"}`) {
		t.Fatalf("error SSE:\n%s", output.String())
	}
}
