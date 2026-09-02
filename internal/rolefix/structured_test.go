package rolefix

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hookEvaluatorBody is the shape Claude Code 2.1.258 sends for a /goal Stop
// hook, captured off the wire from a live Featherless pane on 2026-09-02. The
// verdict's JSON contract rides in output_config.format, and the request carries
// the structured-outputs beta that is supposed to enforce it.
const hookEvaluatorBody = `{
  "model": "TurboVadim/Qwen3.8-27B-OBLITERATED",
  "max_tokens": 8192,
  "stream": true,
  "tools": [],
  "output_config": {
    "effort": "high",
    "format": {
      "type": "json_schema",
      "schema": {
        "type": "object",
        "properties": {"ok":{"type":"boolean"},"reason":{"type":"string"},"impossible":{"type":"boolean"}},
        "required": ["ok", "reason"],
        "additionalProperties": false
      }
    }
  },
  "system": [{"type":"text","text":"You are evaluating a stop-condition hook in Claude Code."}],
  "messages": [{"role":"user","content":[{"type":"text","text":"Condition: ship it"}]}]
}`

// proseThenJSON is the verbatim text Featherless returned for a Stop hook on
// 2026-09-02, recorded in the session transcript as the hook's stdout beside
// "JSON validation failed". Claude Code parses the whole response text, so the
// paragraph in front of the object is what breaks the hook.
const proseThenJSON = "The stop condition is not satisfied. The transcript only contains the initial goal " +
	"statement: \"Goal set: get VINs for every car on every marketplace we support.\" No work has been done, " +
	"no subagents dispatched, and no VINs obtained.\n\n" +
	`{"ok": false, "reason": "No subagents have been dispatched and no VINs have been obtained yet."}`

const verdictJSON = `{"ok": false, "reason": "No subagents have been dispatched and no VINs have been obtained yet."}`

// sseTextResponse renders one assistant text block the way Featherless streams
// it: a delta per fragment, no ping events of its own.
func sseTextResponse(text string) string {
	var b strings.Builder
	b.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n")
	b.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	for _, chunk := range chunked(text, 7) {
		payload, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": chunk},
		})
		b.WriteString("event: content_block_delta\ndata: " + string(payload) + "\n\n")
	}
	b.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	b.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":0}}\n\n")
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

func chunked(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// streamedText reassembles an assistant turn the way Claude Code does: it
// concatenates the text deltas of every text block.
func streamedText(t *testing.T, body string) string {
	t.Helper()
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			continue
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "text" {
				out.WriteString(event.ContentBlock.Text)
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				out.WriteString(event.Delta.Text)
			}
		}
	}
	return out.String()
}

// through posts body to a handler proxying upstream and returns the response the
// client sees.
func through(t *testing.T, upstream *httptest.Server, body string) (*http.Response, string) {
	t.Helper()
	front := httptest.NewServer(NewHandler(upstream.URL))
	t.Cleanup(front.Close)
	resp, err := http.Post(front.URL+"/v1/messages?beta=true", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp, string(raw)
}

func sseUpstream(t *testing.T, text string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextResponse(text))
	}))
	t.Cleanup(server.Close)
	return server
}

// Featherless accepts output_config.format and ignores it — proved live on
// 2026-09-02 by asking, under a schema demanding {capital_city, population},
// for one sentence of prose and getting "Paris". So the JSON contract Claude
// Code's prompt-hook evaluator depends on is unenforced, the model prefixes a
// paragraph whenever it feels like explaining, and the evaluator's parse of the
// whole response text fails with "JSON validation failed". The proxy owes the
// client the contract the endpoint dropped.
func TestNewHandler_repairs_a_prose_preamble_the_request_asked_the_endpoint_to_prevent(t *testing.T) {
	upstream := sseUpstream(t, proseThenJSON)
	_, body := through(t, upstream, hookEvaluatorBody)
	if got := streamedText(t, body); got != verdictJSON {
		t.Fatalf("client text = %q, want %q", got, verdictJSON)
	}
}

// A response that already honours the contract must reach the client exactly as
// the endpoint sent it: repairing what is not broken is how a proxy invents bugs.
func TestNewHandler_leaves_a_response_that_already_answers_in_json_alone(t *testing.T) {
	upstream := sseUpstream(t, verdictJSON)
	_, body := through(t, upstream, hookEvaluatorBody)
	if body != sseTextResponse(verdictJSON) {
		t.Fatalf("a conforming response was rewritten:\n%s", body)
	}
}

// Only a request that declared a JSON schema is repaired. A normal turn's prose
// is the answer, and extracting an object out of it would replace the reply with
// a fragment of itself.
func TestNewHandler_leaves_a_turn_that_declared_no_schema_alone(t *testing.T) {
	upstream := sseUpstream(t, proseThenJSON)
	_, body := through(t, upstream, claudeCodeBody)
	if got := streamedText(t, body); got != proseThenJSON {
		t.Fatalf("client text = %q, want it untouched", got)
	}
}

// The side queries that select memories use the same contract without streaming.
func TestNewHandler_repairs_a_response_that_was_not_streamed(t *testing.T) {
	payload := map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content":     []any{map[string]any{"type": "text", "text": proseThenJSON}},
		"stop_reason": "end_turn",
	}
	encoded, _ := json.Marshal(payload)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(upstream.Close)

	resp, body := through(t, upstream, strings.ReplaceAll(hookEvaluatorBody, `"stream": true`, `"stream": false`))
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("parse repaired body: %v (%s)", err, body)
	}
	if len(parsed.Content) != 1 || parsed.Content[0].Text != verdictJSON {
		t.Fatalf("content = %+v, want the bare verdict", parsed.Content)
	}
	if got := resp.Header.Get("Content-Length"); got != "" && got != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length %s does not describe the %d byte body", got, len(body))
	}
}

// Nothing may be invented. Text holding no JSON object at all is the endpoint's
// answer, and the client's own error is a better outcome than a fabricated one.
func TestNewHandler_leaves_text_holding_no_object_alone(t *testing.T) {
	upstream := sseUpstream(t, "I am unable to judge this condition.")
	_, body := through(t, upstream, hookEvaluatorBody)
	if got := streamedText(t, body); got != "I am unable to judge this condition." {
		t.Fatalf("client text = %q, want it untouched", got)
	}
}

// Buffering the text to repair it is silence on the wire, and Claude Code arms a
// 20s byte-stall watchdog on every stream it reads. The proxy fills its own wait
// the way Featherless fills the wait before a first token: with SSE comments.
func TestNewHandler_keeps_the_stream_warm_while_it_buffers(t *testing.T) {
	restore := keepAliveInterval
	keepAliveInterval = 40 * time.Millisecond
	t.Cleanup(func() { keepAliveInterval = restore })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		full := sseTextResponse(proseThenJSON)
		head, tail, _ := strings.Cut(full, "event: content_block_stop")
		_, _ = io.WriteString(w, head)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(3 * keepAliveInterval)
		_, _ = io.WriteString(w, "event: content_block_stop"+tail)
	}))
	t.Cleanup(upstream.Close)

	front := httptest.NewServer(NewHandler(upstream.URL))
	t.Cleanup(front.Close)
	resp, err := http.Post(front.URL+"/v1/messages?beta=true", "application/json", strings.NewReader(hookEvaluatorBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	seen := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var got bytes.Buffer
		for {
			line, err := reader.ReadString('\n')
			got.WriteString(line)
			if strings.HasPrefix(line, ":") {
				seen <- got.String()
				return
			}
			if err != nil {
				seen <- got.String()
				return
			}
		}
	}()
	select {
	case got := <-seen:
		if !strings.Contains(got, "\n: ") && !strings.HasPrefix(got, ": ") {
			t.Fatalf("no keep-alive reached the client while the proxy buffered:\n%s", got)
		}
	case <-time.After(3 * keepAliveInterval):
		t.Fatal("the proxy stayed silent through the whole buffered block")
	}
}

func TestRepairJSONText(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		required []string
		want     string
		changed  bool
	}{
		{name: "bare object is already the contract", text: verdictJSON, required: []string{"ok", "reason"}, want: verdictJSON},
		{
			name: "prose in front of the object", text: proseThenJSON,
			required: []string{"ok", "reason"}, want: verdictJSON, changed: true,
		},
		{
			name: "a fenced object is what Claude Code already strips",
			text: "```json\n" + verdictJSON + "\n```", required: []string{"ok", "reason"},
			want: "```json\n" + verdictJSON + "\n```",
		},
		{
			name: "prose around a fenced object", text: "Here is my verdict:\n```json\n" + verdictJSON + "\n```\nHope that helps.",
			required: []string{"ok", "reason"}, want: verdictJSON, changed: true,
		},
		{
			name: "the outermost object wins over its own members",
			text: `Verdict: {"ok": false, "reason": "no", "detail": {"ok": true, "reason": "inner"}}`,
			// A nested member parses on its own, so scanning for the last "{"
			// that decodes would answer with the wrong object.
			required: []string{"ok", "reason"},
			want:     `{"ok": false, "reason": "no", "detail": {"ok": true, "reason": "inner"}}`, changed: true,
		},
		{
			name: "the object that satisfies the schema wins",
			text: `Shape: {"note": "verdicts look like this"} then {"ok": true, "reason": "done"}`,
			// Both are outermost, so the required keys are the tiebreak.
			required: []string{"ok", "reason"},
			want:     `{"ok": true, "reason": "done"}`, changed: true,
		},
		{
			name:     "an earlier conforming object beats a trailing stray one",
			text:     `{"ok": true, "reason": "done"} — for reference the schema is {"type": "object"}`,
			required: []string{"ok", "reason"},
			want:     `{"ok": true, "reason": "done"}`, changed: true,
		},
		{name: "no object at all", text: "I cannot judge this.", required: []string{"ok"}, want: "I cannot judge this."},
		{name: "an unterminated object is not an object", text: `thinking: {"ok": fal`, required: []string{"ok"}, want: `thinking: {"ok": fal`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := RepairJSONText(tc.text, tc.required)
			if got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if changed != tc.changed {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

func TestJSONSchemaContract(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		want     []string
		declared bool
	}{
		{name: "output_config.format", body: hookEvaluatorBody, want: []string{"ok", "reason"}, declared: true},
		{
			name:     "the deprecated top-level key",
			body:     `{"output_format":{"type":"json_schema","schema":{"required":["selected_memories"]}},"messages":[]}`,
			want:     []string{"selected_memories"},
			declared: true,
		},
		{
			name: "effort alone is every ordinary turn",
			body: `{"output_config":{"effort":"high"},"messages":[]}`,
		},
		{name: "no output config", body: claudeCodeBody},
		{
			name: "a format that is not a json schema",
			body: `{"output_config":{"format":{"type":"text"}},"messages":[]}`,
		},
		{name: "not a request at all", body: `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, declared := JSONSchemaContract([]byte(tc.body))
			if declared != tc.declared {
				t.Fatalf("declared = %v, want %v", declared, tc.declared)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("required = %v, want %v", got, tc.want)
			}
		})
	}
}

// The endpoint may compress a body it is not asked to leave alone, and a
// compressed body is bytes the repair cannot read. A request whose response the
// proxy must inspect therefore asks for identity.
func TestNewHandler_asks_for_an_unencoded_body_when_it_must_read_one(t *testing.T) {
	var encodings []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encodings = append(encodings, r.Header.Get("Accept-Encoding"))
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextResponse(verdictJSON))
	}))
	t.Cleanup(upstream.Close)

	through(t, upstream, hookEvaluatorBody)
	if len(encodings) != 1 || encodings[0] != "identity" {
		t.Fatalf("Accept-Encoding = %v, want [identity]", encodings)
	}
}

// A body too large to hold is the endpoint's to deliver: truncating it to the
// repair budget would turn an unrepairable reply into a corrupt one.
func TestNewHandler_passes_a_body_too_large_to_repair_through_intact(t *testing.T) {
	restore := maxRepairBytes
	maxRepairBytes = 512
	t.Cleanup(func() { maxRepairBytes = restore })

	payload, _ := json.Marshal(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 4096)}},
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(upstream.Close)

	_, body := through(t, upstream, strings.ReplaceAll(hookEvaluatorBody, `"stream": true`, `"stream": false`))
	if body != string(payload) {
		t.Fatalf("body was truncated: %d bytes reached the client, want %d", len(body), len(payload))
	}
}

// The same for a stream: a text block past the budget is released as it
// arrived, so the client still sees every delta the endpoint sent.
func TestNewHandler_releases_a_text_block_too_large_to_hold(t *testing.T) {
	restore := maxRepairBytes
	maxRepairBytes = 512
	t.Cleanup(func() { maxRepairBytes = restore })

	long := "Reasoning: " + strings.Repeat("thinking out loud. ", 200) + verdictJSON
	upstream := sseUpstream(t, long)
	_, body := through(t, upstream, hookEvaluatorBody)
	if got := streamedText(t, body); got != long {
		t.Fatalf("client text = %d bytes, want the %d the endpoint sent", len(got), len(long))
	}
}
