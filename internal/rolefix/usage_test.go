package rolefix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamedUsage returns the input and output token counts a client reads out of
// an Anthropic SSE stream: message_start carries the prompt's cost and
// message_delta the reply's.
func streamedUsage(t *testing.T, body string) (input, output int) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		data, found := strings.CutPrefix(line, "data: ")
		if !found {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					Input  int `json:"input_tokens"`
					Output int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "message_start":
			input = event.Message.Usage.Input
			output = event.Message.Usage.Output
		case "message_delta":
			if event.Usage.Output > 0 {
				output = event.Usage.Output
			}
			if event.Usage.Input > 0 {
				input = event.Usage.Input
			}
		}
	}
	return input, output
}

// Featherless answers /v1/messages with usage {input_tokens: 0, output_tokens:
// 0} on every turn, streamed and not — measured 2026-09-02 against the live
// endpoint, where the same conversation through /v1/chat/completions reported
// 19,838 prompt tokens. Claude Code sizes auto-compaction from that figure, so
// a permanent zero means the transcript never compacts, /context reads empty,
// and the conversation grows until the endpoint rejects every turn — with no
// warning and no way back, because /compact must itself send the oversized
// transcript.
func TestNewHandler_reports_the_usage_the_endpoint_left_at_zero(t *testing.T) {
	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextResponse(proseThenJSON))
	}))
	t.Cleanup(upstream.Close)

	_, body := through(t, upstream, claudeCodeBody)
	input, output := streamedUsage(t, body)
	if input <= 0 {
		t.Errorf("input_tokens = %d; a zero leaves Claude Code unable to compact", input)
	}
	if output <= 0 {
		t.Errorf("output_tokens = %d; the reply's own cost is part of the transcript", output)
	}
	// The count is of the body the endpoint was actually sent, not of the one
	// Claude Code wrote: the repairs in front of it change what the model reads.
	if want := EstimateInputTokens(forwarded); input != want {
		t.Errorf("input_tokens = %d, want the forwarded request's estimate %d", input, want)
	}
}

// A gateway that does report usage knows better than an estimate, so a figure
// the endpoint supplied is never replaced.
func TestNewHandler_never_overwrites_usage_the_endpoint_reported(t *testing.T) {
	stream := strings.ReplaceAll(sseTextResponse("hello"),
		`"usage":{"input_tokens":0,"output_tokens":0}`, `"usage":{"input_tokens":4242,"output_tokens":7}`)
	stream = strings.ReplaceAll(stream, `"usage":{"output_tokens":0}`, `"usage":{"output_tokens":99}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	t.Cleanup(upstream.Close)

	_, body := through(t, upstream, claudeCodeBody)
	if input, output := streamedUsage(t, body); input != 4242 || output != 99 {
		t.Errorf("usage = %d/%d, want the endpoint's own 4242/99", input, output)
	}
}

// The side queries that select memories and evaluate hooks do not stream, and
// their cost counts against the same window.
func TestNewHandler_reports_usage_on_a_response_that_was_not_streamed(t *testing.T) {
	payload := map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m",
		"content":     []any{map[string]any{"type": "text", "text": "done"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
	}
	encoded, _ := json.Marshal(payload)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(upstream.Close)

	_, body := through(t, upstream, claudeCodeBody)
	var got struct {
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("parse: %v (%s)", err, body)
	}
	if got.Usage.Input <= 0 || got.Usage.Output <= 0 {
		t.Errorf("usage = %d/%d, want both filled in", got.Usage.Input, got.Usage.Output)
	}
	if len(got.Content) != 1 || got.Content[0]["text"] != "done" {
		t.Errorf("the reply itself moved: %v", got.Content)
	}
}

// The estimate covers everything the endpoint turns into prompt. Tool schemas
// are 16,000 of a bare pane's 20,000 tokens, so leaving them out would report a
// fifth of the truth.
func TestEstimateInputTokens_counts_the_whole_prompt(t *testing.T) {
	filler := strings.Repeat("x", 4000)
	body := func(parts ...string) []byte {
		return []byte(`{"model":"m","max_tokens":8192,` + strings.Join(parts, ",") + `}`)
	}
	bare := EstimateInputTokens(body(`"messages":[{"role":"user","content":"hi"}]`))
	withSystem := EstimateInputTokens(body(
		`"system":[{"type":"text","text":"`+filler+`"}]`,
		`"messages":[{"role":"user","content":"hi"}]`))
	withTools := EstimateInputTokens(body(
		`"messages":[{"role":"user","content":"hi"}]`,
		`"tools":[{"name":"Read","description":"`+filler+`","input_schema":{"type":"object"}}]`))

	if withSystem-bare < 900 {
		t.Errorf("4000 bytes of system prompt moved the estimate by %d, want ~1000", withSystem-bare)
	}
	if withTools-bare < 900 {
		t.Errorf("4000 bytes of tool schema moved the estimate by %d, want ~1000", withTools-bare)
	}
}

// An image's base64 is orders of magnitude larger than its token cost, so
// counting those bytes would report a conversation with one screenshot as
// larger than the window and compact on every turn. Same flat price the GPT
// bridge's context guard uses.
func TestEstimateInputTokens_prices_an_image_flat(t *testing.T) {
	image := strings.Repeat("A", 2<<20)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + image + `"}},` +
		`{"type":"text","text":"what is this"}]}]}`)
	got := EstimateInputTokens(body)
	if got > 4*imageTokens {
		t.Errorf("estimate = %d for one image; its base64 is dominating the count", got)
	}
	if got < imageTokens {
		t.Errorf("estimate = %d, want at least the flat image cost %d", got, imageTokens)
	}
}

// A body the estimator cannot parse is still a prompt, and reporting zero for it
// is the very failure this repair exists to remove.
func TestEstimateInputTokens_never_reports_zero(t *testing.T) {
	for _, body := range []string{"", "{", `{"messages":[]}`} {
		if got := EstimateInputTokens([]byte(body)); got < 1 {
			t.Errorf("EstimateInputTokens(%q) = %d, want at least 1", body, got)
		}
	}
}

// Only an assistant message has usage to report. An error body, or anything
// else the endpoint answers a POST with, must reach the client as it arrived —
// stamping a token count onto it would be the proxy inventing data.
func TestNewHandler_leaves_a_response_that_is_not_a_message_alone(t *testing.T) {
	const errorBody = `{"type":"error","error":{"type":"api_error","message":"model is at capacity"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, errorBody)
	}))
	t.Cleanup(upstream.Close)

	resp, body := through(t, upstream, claudeCodeBody)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if body != errorBody {
		t.Errorf("body = %s, want it untouched", body)
	}
}
