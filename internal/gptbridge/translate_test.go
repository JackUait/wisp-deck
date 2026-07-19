package gptbridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseAndTranslate(t *testing.T, body string) Translation {
	t.Helper()
	request, err := ParseMessagesRequest([]byte(body))
	if err != nil {
		t.Fatalf("ParseMessagesRequest: %v", err)
	}
	translated, err := TranslateRequest(request)
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	return translated
}

func TestTranslateSimpleMessagesRequest(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":4096,
		"system":[{"type":"text","text":"Be exact."}],
		"messages":[
			{"role":"user","content":"Earlier question"},
			{"role":"assistant","content":[{"type":"text","text":"Earlier answer"}]},
			{"role":"user","content":[{"type":"text","text":"Current question"}]}
		],
		"stream":true
	}`)
	if got.Model != "gpt-5.6-terra" || got.MaxTokens != 4096 || !got.Stream {
		t.Fatalf("request metadata = %+v", got)
	}
	if got.System != "Be exact." {
		t.Fatalf("system = %q", got.System)
	}
	if len(got.History) != 2 {
		t.Fatalf("history = %#v", got.History)
	}
	history, _ := json.Marshal(got.History)
	for _, want := range []string{
		`"role":"user"`,
		`"type":"input_text"`,
		`"role":"assistant"`,
		`"type":"output_text"`,
	} {
		if !strings.Contains(string(history), want) {
			t.Errorf("history missing %q: %s", want, history)
		}
	}
	if len(got.Input) != 1 || got.Input[0].Type != "text" || got.Input[0].Text != "Current question" {
		t.Fatalf("input = %#v", got.Input)
	}
}

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

func TestTranslateToolsPreservesSchemaAndChoice(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-sol",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"Inspect it"}],
		"tools":[{
			"name":"Read",
			"description":"Read one file",
			"input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
		}],
		"tool_choice":{"type":"tool","name":"Read"}
	}`)
	if len(got.DynamicTools) != 1 {
		t.Fatalf("dynamic tools = %#v", got.DynamicTools)
	}
	tool := got.DynamicTools[0]
	if tool.Type != "function" || tool.Name != "Read" || tool.Description != "Read one file" {
		t.Fatalf("tool = %+v", tool)
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema = %v", schema)
	}
	if !strings.Contains(got.ToolDirective, `must call "Read"`) {
		t.Fatalf("tool directive = %q", got.ToolDirective)
	}
}

func TestTranslateToolChoiceNoneExposesNoTools(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[{"role":"user","content":"Answer directly"}],
		"tools":[{"name":"Bash","description":"","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"none"}
	}`)
	if len(got.DynamicTools) != 0 {
		t.Fatalf("dynamic tools = %#v, want none", got.DynamicTools)
	}
}

func TestTranslateImages(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-luna",
		"max_tokens":100,
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"Describe"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},
				{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}
			]
		}]
	}`)
	if len(got.Input) != 3 {
		t.Fatalf("input = %#v", got.Input)
	}
	if got.Input[1].Type != "image" || got.Input[1].URL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("base64 image = %+v", got.Input[1])
	}
	if got.Input[2].URL != "https://example.com/image.png" {
		t.Fatalf("URL image = %+v", got.Input[2])
	}
}

func TestTranslateCompletedToolHistory(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"Read x"},
			{"role":"assistant","content":[
				{"type":"text","text":"Checking."},
				{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"x"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"contents"}
			]},
			{"role":"assistant","content":"It says contents."},
			{"role":"user","content":"Continue"}
		]
	}`)
	history, _ := json.Marshal(got.History)
	for _, want := range []string{
		`"type":"function_call"`,
		`"call_id":"toolu_1"`,
		`"arguments":"{\"path\":\"x\"}"`,
		`"type":"function_call_output"`,
		`"output":"contents"`,
	} {
		if !strings.Contains(string(history), want) {
			t.Errorf("history missing %q: %s", want, history)
		}
	}
}

func TestTranslatePendingToolResults(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"Run both"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"bridge_a","name":"One","input":{}},
				{"type":"tool_use","id":"bridge_b","name":"Two","input":{}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"bridge_b","content":"second"},
				{"type":"tool_result","tool_use_id":"bridge_a","content":[{"type":"text","text":"first"}],"is_error":true}
			]}
		]
	}`)
	if len(got.ToolResults) != 2 || len(got.Input) != 0 {
		t.Fatalf("translation = %+v", got)
	}
	if got.ToolResults[0].ToolUseID != "bridge_b" || !got.ToolResults[0].Success {
		t.Fatalf("first result = %+v", got.ToolResults[0])
	}
	if got.ToolResults[1].ToolUseID != "bridge_a" || got.ToolResults[1].Success {
		t.Fatalf("second result = %+v", got.ToolResults[1])
	}
	if len(got.ToolResults[1].ContentItems) != 1 || got.ToolResults[1].ContentItems[0].Text != "first" {
		t.Fatalf("result content = %+v", got.ToolResults[1].ContentItems)
	}
}

func TestTranslateThinkingBudgetToReasoningEffort(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-sol",
		"max_tokens":50000,
		"thinking":{"type":"enabled","budget_tokens":20000},
		"messages":[{"role":"user","content":"Think"}]
	}`)
	if got.Effort != "xhigh" {
		t.Fatalf("effort = %q, want xhigh", got.Effort)
	}
}

func TestTranslateRejectsUnsupportedSamplingControls(t *testing.T) {
	tests := []string{
		`"temperature":0.2`,
		`"top_p":0.5`,
		`"stop_sequences":["STOP"]`,
	}
	for _, field := range tests {
		t.Run(field, func(t *testing.T) {
			body := `{"model":"gpt-5.6-terra","max_tokens":100,` + field +
				`,"messages":[{"role":"user","content":"hi"}]}`
			request, err := ParseMessagesRequest([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := TranslateRequest(request); err == nil || !strings.Contains(err.Error(), "not supported") {
				t.Fatalf("TranslateRequest error = %v", err)
			}
		})
	}
}

func TestTranslateRejectsMalformedToolHistory(t *testing.T) {
	tests := map[string]string{
		"dangling result": `[
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"x"}]}
		]`,
		"duplicate tool id": `[
			{"role":"user","content":"x"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"same","name":"A","input":{}},
				{"type":"tool_use","id":"same","name":"B","input":{}}
			]},
			{"role":"user","content":"next"}
		]`,
	}
	for name, messages := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"gpt-5.6-terra","max_tokens":100,"messages":` + messages + `}`
			request, err := ParseMessagesRequest([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := TranslateRequest(request); err == nil {
				t.Fatal("expected invalid tool history")
			}
		})
	}
}

func TestParseMessagesRequestRejectsUnknownContentBlock(t *testing.T) {
	_, err := ParseMessagesRequest([]byte(`{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[{"role":"user","content":[{"type":"document","source":{}}]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), `unsupported content block "document"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestParseMessagesRequestRejectsNonTextInlineSystem(t *testing.T) {
	_, err := ParseMessagesRequest([]byte(`{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"test"},
			{"role":"system","content":[{
				"type":"image",
				"source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}
			}]}
		]
	}`))
	if err == nil ||
		!strings.Contains(err.Error(), `messages[1]: system message has unsupported content block "image"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestParseMessagesRequestRejectsSystemOnlyMessages(t *testing.T) {
	_, err := ParseMessagesRequest([]byte(`{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[{"role":"system","content":"No user input."}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "messages must contain a user or assistant message") {
		t.Fatalf("error = %v", err)
	}
}
