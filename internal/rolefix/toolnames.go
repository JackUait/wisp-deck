package rolefix

import (
	"encoding/json"
	"strings"
)

// Anthropic's API validates a tool call's name against the tools the request
// declared, so a client never receives one it did not supply. Featherless does
// not: it passes the model's own spelling straight through. Measured 2026-09-02
// on TurboVadim/Qwen3.8-27B-OBLITERATED, which answered a tool declared as
// "Read" with "read" — on the Messages route and on chat/completions alike, and
// not every time, which is what makes it read as a flaky model rather than a
// missing check.
//
// Claude Code answers an unknown name with "No such tool available", so the turn
// is spent on an error for a call the model got right in every way that matters:
// the tool, the arguments, and the intent. Restoring the declared spelling is
// the whole repair — a name matching no declared tool is left exactly as it
// arrived, because that is the model inventing a tool, and the client's own
// error beats running something nobody asked for.

// DeclaredToolNames maps the lowercase form of each declared tool name to its
// declared spelling. A name whose lowercase form is shared by two tools is left
// out: it cannot be resolved, and guessing between them would call the wrong
// tool.
func DeclaredToolNames(body []byte) map[string]string {
	var envelope struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	names := make(map[string]string, len(envelope.Tools))
	ambiguous := map[string]bool{}
	for _, tool := range envelope.Tools {
		if tool.Name == "" {
			continue
		}
		key := strings.ToLower(tool.Name)
		if existing, seen := names[key]; seen && existing != tool.Name {
			ambiguous[key] = true
		}
		names[key] = tool.Name
	}
	for key := range ambiguous {
		delete(names, key)
	}
	return names
}

// declaresTools reports whether a request supplied any tool at all. A turn with
// no tools has no tool call to lose.
func declaresTools(body []byte) bool {
	var envelope struct {
		Tools []json.RawMessage `json:"tools"`
	}
	return json.Unmarshal(body, &envelope) == nil && len(envelope.Tools) > 0
}

// declaredSpelling returns the name a tool call should carry, and whether that
// differs from the name it arrived with.
func declaredSpelling(names map[string]string, called string) (string, bool) {
	declared, ok := names[strings.ToLower(called)]
	if !ok || declared == called {
		return called, false
	}
	return declared, true
}

// repairToolName rewrites the name of a tool call announced by one SSE event.
// Every other event is returned as the bytes that arrived.
func repairToolName(names map[string]string, event []byte) []byte {
	if len(names) == 0 {
		return event
	}
	var parsed struct {
		Type         string `json:"type"`
		ContentBlock struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if !decodeEventData(event, &parsed) ||
		parsed.Type != "content_block_start" || parsed.ContentBlock.Type != "tool_use" {
		return event
	}
	declared, moved := declaredSpelling(names, parsed.ContentBlock.Name)
	if !moved {
		return event
	}
	return rewriteEvent(event, func(payload map[string]json.RawMessage) bool {
		var block map[string]json.RawMessage
		if json.Unmarshal(payload["content_block"], &block) != nil {
			return false
		}
		encoded, err := json.Marshal(declared)
		if err != nil {
			return false
		}
		block["name"] = encoded
		rendered, err := json.Marshal(block)
		if err != nil {
			return false
		}
		payload["content_block"] = rendered
		return true
	})
}

// repairToolNamesInBody rewrites the tool names of a reply that was not
// streamed, leaving every other key of the envelope as it arrived.
func repairToolNamesInBody(names map[string]string, body []byte) ([]byte, bool) {
	if len(names) == 0 {
		return body, false
	}
	envelope, ok := assistantMessage(body)
	if !ok {
		return body, false
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["content"], &blocks); err != nil {
		return body, false
	}
	changed := false
	for _, block := range blocks {
		var kind, called string
		if json.Unmarshal(block["type"], &kind) != nil || kind != "tool_use" {
			continue
		}
		if json.Unmarshal(block["name"], &called) != nil {
			continue
		}
		declared, moved := declaredSpelling(names, called)
		if !moved {
			continue
		}
		encoded, err := json.Marshal(declared)
		if err != nil {
			continue
		}
		block["name"] = encoded
		changed = true
	}
	if !changed {
		return body, false
	}
	content, err := json.Marshal(blocks)
	if err != nil {
		return body, false
	}
	envelope["content"] = content
	out, err := json.Marshal(envelope)
	if err != nil {
		return body, false
	}
	return out, true
}
