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
		if item.Type == "text" && strings.Contains(item.Text, agentSummaryPrompt) {
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
