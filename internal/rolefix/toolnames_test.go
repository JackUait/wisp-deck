package rolefix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// toolCallBody is a turn that declares the tools Claude Code declares, in the
// spelling it declares them.
const toolCallBody = `{
  "model": "TurboVadim/Qwen3.8-27B-OBLITERATED",
  "max_tokens": 1024,
  "stream": true,
  "system": [{"type":"text","text":"You are Claude Code."}],
  "messages": [{"role":"user","content":[{"type":"text","text":"read /etc/hosts"}]}],
  "tools": [
    {"name":"Read","description":"Read a file.","input_schema":{"type":"object"}},
    {"name":"Bash","description":"Run a command.","input_schema":{"type":"object"}}
  ]
}`

// sseToolUseResponse renders one assistant tool call the way Featherless streams
// it, under whatever name the model emitted.
func sseToolUseResponse(name string) string {
	start, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": name, "input": map[string]any{}},
	})
	var b strings.Builder
	b.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n")
	b.WriteString("event: content_block_start\ndata: " + string(start) + "\n\n")
	b.WriteString("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"file_path\\\":\\\"/etc/hosts\\\"}\"}}\n\n")
	b.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	b.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":8}}\n\n")
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

func streamedToolNames(body string) []string {
	var names []string
	for _, line := range strings.Split(body, "\n") {
		data, found := strings.CutPrefix(line, "data: ")
		if !found {
			continue
		}
		var event struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		if event.Type == "content_block_start" && event.ContentBlock.Type == "tool_use" {
			names = append(names, event.ContentBlock.Name)
		}
	}
	return names
}

func sseServer(t *testing.T, stream string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	t.Cleanup(server.Close)
	return server
}

// Anthropic's API validates a tool call's name against the tools the request
// declared, so a client never sees one it did not supply. Featherless passes the
// model's own spelling straight through: measured 2026-09-02 on
// TurboVadim/Qwen3.8-27B-OBLITERATED, which returned "read" for a tool declared
// as "Read" on both the Messages and the chat-completions route. Claude Code
// answers that with "No such tool available", so the turn is spent on an error
// for a call the model got right in every way that matters.
func TestNewHandler_restores_the_declared_spelling_of_a_tool_name(t *testing.T) {
	upstream := sseServer(t, sseToolUseResponse("read"))
	_, body := through(t, upstream, toolCallBody)
	if got := streamedToolNames(body); len(got) != 1 || got[0] != "Read" {
		t.Errorf("tool names = %v, want [Read]", got)
	}
}

// A name that matches no declared tool is the model inventing one, and the
// client's own "no such tool" is the honest answer — snapping it to the nearest
// spelling would run a tool nobody asked for.
func TestNewHandler_leaves_a_tool_name_it_cannot_match_alone(t *testing.T) {
	upstream := sseServer(t, sseToolUseResponse("read_file"))
	_, body := through(t, upstream, toolCallBody)
	if got := streamedToolNames(body); len(got) != 1 || got[0] != "read_file" {
		t.Errorf("tool names = %v, want [read_file] untouched", got)
	}
}

// A stream whose names are already right must reach the client byte for byte.
func TestNewHandler_leaves_a_correctly_named_tool_call_untouched(t *testing.T) {
	stream := sseToolUseResponse("Read")
	upstream := sseServer(t, stream)
	_, body := through(t, upstream, toolCallBody)
	if body != stream {
		t.Errorf("a correct tool call was rewritten:\n%s", body)
	}
}

// The same repair on a reply that was not streamed.
func TestNewHandler_restores_a_tool_name_on_a_response_that_was_not_streamed(t *testing.T) {
	payload := map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content": []any{
			map[string]any{"type": "text", "text": "reading it"},
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "bASH", "input": map[string]any{}},
		},
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 9, "output_tokens": 8},
	}
	encoded, _ := json.Marshal(payload)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(upstream.Close)

	_, body := through(t, upstream, strings.Replace(toolCallBody, `"stream": true,`, "", 1))
	var got struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("parse: %v (%s)", err, body)
	}
	if len(got.Content) != 2 || got.Content[1]["name"] != "Bash" {
		t.Errorf("content = %v, want the tool renamed to Bash", got.Content)
	}
	if got.Content[0]["text"] != "reading it" {
		t.Errorf("the reply's text moved: %v", got.Content[0])
	}
}

func TestDeclaredToolNames(t *testing.T) {
	names := DeclaredToolNames([]byte(toolCallBody))
	if len(names) != 2 || names["read"] != "Read" || names["bash"] != "Bash" {
		t.Errorf("DeclaredToolNames() = %v, want read->Read and bash->Bash", names)
	}
	// Two tools differing only by case cannot both be the answer, so neither is
	// offered: guessing between them would call the wrong one.
	ambiguous := `{"tools":[{"name":"Read"},{"name":"read"}],"messages":[]}`
	if names := DeclaredToolNames([]byte(ambiguous)); len(names) != 0 {
		t.Errorf("DeclaredToolNames(ambiguous) = %v, want nothing to snap to", names)
	}
	if names := DeclaredToolNames([]byte("{not json")); len(names) != 0 {
		t.Errorf("an unparseable body must declare no tools, got %v", names)
	}
}
