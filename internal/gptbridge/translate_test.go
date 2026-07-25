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

func TestTranslateAliasesReservedMCPToolNames(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-sol",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"Fetch the page"}],
		"tools":[{
			"name":"mcp__notion__authenticated_fetch",
			"description":"Fetch a Notion page",
			"input_schema":{"type":"object"}
		}],
		"tool_choice":{"type":"tool","name":"mcp__notion__authenticated_fetch"}
	}`)
	if len(got.DynamicTools) != 1 {
		t.Fatalf("dynamic tools = %#v", got.DynamicTools)
	}
	tool := got.DynamicTools[0]
	if tool.Name != "wisp_mcp__notion__authenticated_fetch" {
		t.Fatalf("app-server tool name = %q", tool.Name)
	}
	if tool.OriginalName != "mcp__notion__authenticated_fetch" {
		t.Fatalf("Claude tool name = %q", tool.OriginalName)
	}
	if !strings.Contains(got.ToolDirective, `must call "wisp_mcp__notion__authenticated_fetch"`) {
		t.Fatalf("tool directive = %q", got.ToolDirective)
	}
}

func TestTranslateReservedMCPAliasDoesNotCollideWithClaudeToolName(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-sol",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"Fetch the page"}],
		"tools":[
			{"name":"mcp__notion__fetch","description":"MCP fetch","input_schema":{"type":"object"}},
			{"name":"wisp_mcp__notion__fetch","description":"Ordinary tool","input_schema":{"type":"object"}}
		]
	}`)
	if len(got.DynamicTools) != 2 {
		t.Fatalf("dynamic tools = %#v", got.DynamicTools)
	}
	if got.DynamicTools[0].Name == got.DynamicTools[1].Name {
		t.Fatalf("app-server tool names collide at %q", got.DynamicTools[0].Name)
	}
	if got.DynamicTools[1].Name != "wisp_mcp__notion__fetch" {
		t.Fatalf("ordinary Claude tool name changed to %q", got.DynamicTools[1].Name)
	}
}

func TestTranslateReservedMCPAliasFitsCodexToolNameLimit(t *testing.T) {
	original := "mcp__" + strings.Repeat("x", 59)
	plan, err := translateTools([]Tool{{
		Name: original, InputSchema: json.RawMessage(`{"type":"object"}`),
	}}, ToolChoice{})
	if err != nil {
		t.Fatalf("translateTools: %v", err)
	}
	tools := plan.DynamicTools
	if len(tools) != 1 {
		t.Fatalf("dynamic tools = %#v", tools)
	}
	if got := len(tools[0].Name); got > 64 {
		t.Fatalf("app-server tool name length = %d, want at most 64: %q", got, tools[0].Name)
	}
	if tools[0].OriginalName != original {
		t.Fatalf("Claude tool name = %q", tools[0].OriginalName)
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

func TestTranslateSkillContinuationMergesSupplementalContent(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"Load the skill"},
			{"role":"assistant","content":[{
				"type":"tool_use",
				"id":"skill_1",
				"name":"Skill",
				"input":{"skill":"superpowers:using-superpowers"}
			}]},
			{"role":"user","content":[
				{
					"type":"tool_result",
					"tool_use_id":"skill_1",
					"content":"Launching skill: superpowers:using-superpowers"
				},
				{"type":"text","text":"Base directory for this skill: /skills/superpowers"}
			]}
		]
	}`)
	if len(got.ToolResults) != 1 || len(got.Input) != 1 {
		t.Fatalf("translation = %+v", got)
	}
	if got.Input[0].Type != "text" ||
		got.Input[0].Text != "Base directory for this skill: /skills/superpowers" {
		t.Fatalf("supplemental input = %+v", got.Input)
	}
	items := got.ToolResults[0].ContentItems
	if len(items) != 2 {
		t.Fatalf("result content = %+v", items)
	}
	if items[0].Type != "inputText" ||
		items[0].Text != "Launching skill: superpowers:using-superpowers" {
		t.Fatalf("first result content = %+v", items[0])
	}
	if items[1].Type != "inputText" ||
		items[1].Text != "Base directory for this skill: /skills/superpowers" {
		t.Fatalf("supplemental result content = %+v", items[1])
	}
}

// Claude Code's compactor appends its plain-text instruction to a pending
// tool result. Recovery must keep that instruction as fresh model input while
// replaying only the real tool output as function_call_output history.
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
	if len(result.ContentItems) != 2 ||
		result.ContentItems[1].Text != "Summarize the conversation. Do not call tools; respond with plain text only." {
		t.Fatalf("live result content = %+v", result.ContentItems)
	}
	if len(result.HistoryContentItems) != 1 || result.HistoryContentItems[0].Text != "Exit code 1" {
		t.Fatalf("history result content = %+v", result.HistoryContentItems)
	}
	if got.Input[0].Type != "text" ||
		got.Input[0].Text != "Summarize the conversation. Do not call tools; respond with plain text only." {
		t.Fatalf("supplemental input = %+v", got.Input)
	}
}

// Claude Code routinely interleaves ordinary text with several tool results in
// the final user message: Skill calls inject their content as text blocks,
// system reminders ride along, and after an API error the user's typed input
// merges into the still-pending tool-result message. Rejecting that shape
// wedges the session permanently, because the client replays the same message
// on every retry.
func TestTranslateMergesTextAcrossMultipleToolResults(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"Run both"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"tool_1","name":"One","input":{}},
				{"type":"tool_use","id":"tool_2","name":"Two","input":{}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"tool_1","content":"first"},
				{"type":"text","text":"Base directory for this skill: /skills/x"},
				{"type":"tool_result","tool_use_id":"tool_2","content":"second"},
				{"type":"text","text":"continue"}
			]}
		]
	}`)
	if len(got.ToolResults) != 2 || len(got.Input) != 2 {
		t.Fatalf("translation = %+v", got)
	}
	if got.Input[0].Text != "Base directory for this skill: /skills/x" ||
		got.Input[1].Text != "continue" {
		t.Fatalf("supplemental input = %+v", got.Input)
	}
	first := got.ToolResults[0]
	if first.ToolUseID != "tool_1" || len(first.ContentItems) != 2 ||
		first.ContentItems[0].Text != "first" ||
		first.ContentItems[1].Text != "Base directory for this skill: /skills/x" {
		t.Fatalf("first result = %+v", first)
	}
	second := got.ToolResults[1]
	if second.ToolUseID != "tool_2" || len(second.ContentItems) != 2 ||
		second.ContentItems[0].Text != "second" ||
		second.ContentItems[1].Text != "continue" {
		t.Fatalf("second result = %+v", second)
	}
}

func TestTranslateMergesLeadingTextIntoFirstToolResult(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":"Run both"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"tool_1","name":"One","input":{}},
				{"type":"tool_use","id":"tool_2","name":"Two","input":{}}
			]},
			{"role":"user","content":[
				{"type":"text","text":"<system-reminder>note</system-reminder>"},
				{"type":"tool_result","tool_use_id":"tool_1","content":"first"},
				{"type":"tool_result","tool_use_id":"tool_2","content":"second"}
			]}
		]
	}`)
	if len(got.ToolResults) != 2 || len(got.Input) != 1 {
		t.Fatalf("translation = %+v", got)
	}
	if got.Input[0].Text != "<system-reminder>note</system-reminder>" {
		t.Fatalf("supplemental input = %+v", got.Input)
	}
	first := got.ToolResults[0]
	if len(first.ContentItems) != 2 ||
		first.ContentItems[0].Text != "<system-reminder>note</system-reminder>" ||
		first.ContentItems[1].Text != "first" {
		t.Fatalf("first result = %+v", first)
	}
	if len(got.ToolResults[1].ContentItems) != 1 ||
		got.ToolResults[1].ContentItems[0].Text != "second" {
		t.Fatalf("second result = %+v", got.ToolResults[1])
	}
}

// Class guard for the 2026-07-20 wedge: Claude Code may compose the final user
// message from ANY interleaving of ordinary blocks and tool results (skill
// text injections, system reminders, post-error typed input, parallel tool
// batches). A translate-path rejection of such a shape is unrecoverable — the
// client replays the identical message on every retry, including after new
// user input — so every producible interleaving must translate, and no block's
// content may be dropped. This tests the property, not the one shipped
// instance; it catches whatever shape the next offender turns out to be.
func TestTranslateNeverRejectsAnyFinalUserBlockInterleaving(t *testing.T) {
	const maxBlocks = 4
	kinds := []string{"text", "image", "tool_result"}
	var cases [][]string
	var build func(prefix []string)
	build = func(prefix []string) {
		if len(prefix) > 0 {
			cases = append(cases, append([]string(nil), prefix...))
		}
		if len(prefix) == maxBlocks {
			return
		}
		for _, kind := range kinds {
			build(append(prefix, kind))
		}
	}
	build(nil)

	const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGBgAAAABQABh6FO1AAAAABJRU5ErkJggg=="
	for _, shape := range cases {
		t.Run(strings.Join(shape, "-"), func(t *testing.T) {
			var uses, finals []string
			var markers []string
			results := 0
			ordinary := 0
			for index, kind := range shape {
				switch kind {
				case "text":
					ordinary++
					marker := "txt_marker_" + strings.Repeat("x", index+1)
					markers = append(markers, marker)
					finals = append(finals, `{"type":"text","text":"`+marker+`"}`)
				case "image":
					ordinary++
					finals = append(finals, `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+pixel+`"}}`)
				case "tool_result":
					results++
					id := "tool_" + strings.Repeat("i", results)
					marker := "result_marker_" + strings.Repeat("y", results)
					markers = append(markers, marker)
					uses = append(uses, `{"type":"tool_use","id":"`+id+`","name":"Run","input":{}}`)
					finals = append(finals, `{"type":"tool_result","tool_use_id":"`+id+`","content":"`+marker+`"}`)
				}
			}
			messages := `[{"role":"user","content":"start"}`
			if len(uses) > 0 {
				messages += `,{"role":"assistant","content":[` + strings.Join(uses, ",") + `]}`
			}
			messages += `,{"role":"user","content":[` + strings.Join(finals, ",") + `]}]`
			body := `{"model":"gpt-5.6-terra","max_tokens":100,"messages":` + messages + `}`

			got := parseAndTranslate(t, body)
			if len(got.ToolResults) != results {
				t.Fatalf("tool results = %d, want %d", len(got.ToolResults), results)
			}
			if results > 0 && len(got.Input) != ordinary {
				t.Fatalf("supplemental input = %d, want %d: %+v", len(got.Input), ordinary, got.Input)
			}
			flattened, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range markers {
				if !strings.Contains(string(flattened), marker) {
					t.Fatalf("content %q was dropped: %s", marker, flattened)
				}
			}
		})
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

// Claude Code's WebSearch tool does not run the search itself. It issues a
// nested Messages request whose entire tools array is Anthropic's server-side
// web_search tool:
//
//	{"type":"web_search_20250305","name":"web_search","max_uses":8}
//
// Server tools carry no input_schema by design — Anthropic hosts them. The
// bridge modelled every tools[] entry as a Claude-hosted custom tool, so this
// shape 400'd with "tools[0]: input_schema must be an object" and every single
// WebSearch call in a GPT session failed.
func TestTranslateAcceptsAnthropicWebSearchServerTool(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tools":[{
			"type":"web_search_20250305",
			"name":"web_search",
			"max_uses":8
		}],
		"messages":[{"role":"user","content":"Perform a web search for the query: encar"}]
	}`)
	if !got.WebSearch {
		t.Fatalf("WebSearch = false, want true for the web_search server tool")
	}
	if len(got.DynamicTools) != 0 {
		t.Fatalf("DynamicTools = %+v, want none (Codex hosts web search itself)", got.DynamicTools)
	}
}

// A server tool must never be forwarded to Codex as a host-provided function,
// and it must not disturb the Claude-hosted tools alongside it.
func TestTranslateKeepsClientToolsAlongsideServerTool(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tools":[
			{"type":"web_search_20250305","name":"web_search","max_uses":8},
			{"name":"Read","description":"read","input_schema":{"type":"object"}},
			{"type":"custom","name":"Bash","description":"bash","input_schema":{"type":"object"}}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	if !got.WebSearch {
		t.Fatalf("WebSearch = false, want true")
	}
	if len(got.DynamicTools) != 2 ||
		got.DynamicTools[0].Name != "Read" || got.DynamicTools[1].Name != "Bash" {
		t.Fatalf("DynamicTools = %+v, want exactly Read and Bash", got.DynamicTools)
	}
}

// Dropping an unknown server tool silently would let the model answer from
// memory as though it had used the capability. Name it instead.
func TestTranslateRejectsUnsupportedServerTool(t *testing.T) {
	request, err := ParseMessagesRequest([]byte(`{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tools":[{"type":"code_execution_20260120","name":"code_execution"}],
		"messages":[{"role":"user","content":"hi"}]
	}`))
	if err != nil {
		t.Fatalf("ParseMessagesRequest: %v", err)
	}
	_, err = TranslateRequest(request)
	if err == nil || !strings.Contains(err.Error(), `code_execution_20260120`) {
		t.Fatalf("error = %v, want it to name the unsupported server tool type", err)
	}
	if err != nil && strings.Contains(err.Error(), "input_schema") {
		t.Fatalf("error = %v, want a server-tool error, not a schema error", err)
	}
}

// The schema requirement still holds for genuinely Claude-hosted tools.
func TestTranslateStillRequiresInputSchemaForClientTools(t *testing.T) {
	request, err := ParseMessagesRequest([]byte(`{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tools":[{"name":"Read","description":"read"}],
		"messages":[{"role":"user","content":"hi"}]
	}`))
	if err != nil {
		t.Fatalf("ParseMessagesRequest: %v", err)
	}
	if _, err := TranslateRequest(request); err == nil ||
		!strings.Contains(err.Error(), "tools[0]: input_schema must be an object") {
		t.Fatalf("error = %v", err)
	}
}

// tool_choice "any" means "use a tool". A request whose only tool is hosted by
// the server still satisfies that, so it must not be rejected for having no
// Claude-hosted tools to force.
func TestTranslateToolChoiceAnyIsSatisfiedByServerTool(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tool_choice":{"type":"any"},
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	if !got.WebSearch || len(got.DynamicTools) != 0 {
		t.Fatalf("translation = %+v", got)
	}
}

// The real Claude Code WebSearch request forces the server tool by name:
// tool_choice {"type":"tool","name":"web_search"}. Resolving that against the
// Claude-hosted tools alone leaves nothing to force, so the request must not be
// rejected as naming an unknown tool.
func TestTranslateToolChoiceMayNameTheWebSearchServerTool(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tool_choice":{"type":"tool","name":"web_search"},
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}],
		"messages":[{"role":"user","content":"Perform a web search for the query: encar"}]
	}`)
	if !got.WebSearch {
		t.Fatalf("WebSearch = false, want true")
	}
	if len(got.DynamicTools) != 0 {
		t.Fatalf("DynamicTools = %+v, want none", got.DynamicTools)
	}
}

// Naming a tool that was never supplied at all is still an error.
func TestTranslateToolChoiceStillRejectsUnknownToolName(t *testing.T) {
	request, err := ParseMessagesRequest([]byte(`{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tool_choice":{"type":"tool","name":"Nope"},
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}],
		"messages":[{"role":"user","content":"hi"}]
	}`))
	if err != nil {
		t.Fatalf("ParseMessagesRequest: %v", err)
	}
	if _, err := TranslateRequest(request); err == nil ||
		!strings.Contains(err.Error(), `tool_choice names unknown tool "Nope"`) {
		t.Fatalf("error = %v", err)
	}
}

// Claude Code's WebSearch exposes allowed_domains/blocked_domains to the user.
// Codex's config.web_search only takes a mode string, so the filters cannot be
// enforced there — but dropping them silently would answer a scoped search with
// unscoped results. Carry them so the turn can state them to the model.
func TestTranslateCarriesWebSearchDomainFilters(t *testing.T) {
	got := parseAndTranslate(t, `{
		"model":"gpt-5.6-terra",
		"max_tokens":100,
		"tools":[{
			"type":"web_search_20250305",
			"name":"web_search",
			"allowed_domains":["encar.com","kbchachacha.com"],
			"blocked_domains":["spam.example"],
			"max_uses":8
		}],
		"messages":[{"role":"user","content":"search"}]
	}`)
	if strings.Join(got.WebSearchAllowedDomains, ",") != "encar.com,kbchachacha.com" {
		t.Fatalf("allowed domains = %v", got.WebSearchAllowedDomains)
	}
	if strings.Join(got.WebSearchBlockedDomains, ",") != "spam.example" {
		t.Fatalf("blocked domains = %v", got.WebSearchBlockedDomains)
	}
}
