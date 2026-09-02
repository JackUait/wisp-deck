package rolefix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// emptyReplyStream is what Featherless sends when its tool parser eats the whole
// reply: a well-formed message envelope carrying no content block at all.
const emptyReplyStream = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":19838,\"output_tokens\":0}}}\n\n" +
	"event: message_delta\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":181}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// An assistant turn with no content at all is not something Anthropic's API can
// produce, and it is not the model declining to act: measured 2026-09-02 on
// Qwen/Qwen3.5-397B-A17B, which billed 181 completion tokens for 0 bytes of
// reply, and on Qwen/Qwen3-VL-30B-A3B-Instruct, which billed 181 for 22 tokens
// of prose — Featherless's Qwen tool parser strips the model's tool call and
// then emits nothing in its place.
//
// Delivered as it arrives, that is a turn where nothing happens and nothing is
// said. It is what a /goal Stop hook loops on forever, and it is why a pane can
// spin for 40 minutes with no error on screen. The proxy says so instead.
func TestNewHandler_says_so_when_the_endpoint_returns_an_empty_reply(t *testing.T) {
	upstream := sseServer(t, emptyReplyStream)
	_, body := through(t, upstream, toolCallBody)
	text := streamedText(t, body)
	if text == "" {
		t.Fatal("an empty reply reached the client as an empty reply")
	}
	if !strings.Contains(text, "empty reply") {
		t.Errorf("notice = %q, want it to name what happened", text)
	}
	// The turn must still end the way the endpoint ended it.
	if !strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Errorf("the stream lost its stop_reason:\n%s", body)
	}
}

// A turn that declared no tools has no tool call to lose, so an empty reply
// there is the endpoint's own business and is delivered as it arrived.
func TestNewHandler_leaves_an_empty_reply_to_a_toolless_turn_alone(t *testing.T) {
	upstream := sseServer(t, emptyReplyStream)
	_, body := through(t, upstream, strings.Replace(hookEvaluatorBody, `"tools": [],`, "", 1))
	if got := streamedText(t, body); got != "" {
		t.Errorf("text = %q, want the empty reply untouched", got)
	}
}

// A reply that carries anything at all is the model's, and inventing commentary
// on top of it would put words in its mouth.
func TestNewHandler_never_comments_on_a_reply_that_has_content(t *testing.T) {
	upstream := sseServer(t, sseToolUseResponse("Read"))
	_, body := through(t, upstream, toolCallBody)
	if got := streamedText(t, body); got != "" {
		t.Errorf("a tool call drew a notice: %q", got)
	}
}

// The same reply, not streamed.
func TestNewHandler_says_so_when_an_unstreamed_reply_is_empty(t *testing.T) {
	payload := map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content": []any{}, "stop_reason": "end_turn",
		"usage": map[string]any{"input_tokens": 19838, "output_tokens": 181},
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
	if len(got.Content) != 1 {
		t.Fatalf("content = %v, want one explanatory block", got.Content)
	}
	text, _ := got.Content[0]["text"].(string)
	if !strings.Contains(text, "empty reply") {
		t.Errorf("notice = %q, want it to name what happened", text)
	}
}
