package gptbridge

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The bridge's client is Claude Code, which believes these gpt models have
// Anthropic-sized context windows (200K/1M) and therefore never auto-compacts
// before Codex's real window overflows. "prompt is too long" is the one error
// shape Claude Code answers with compaction instead of a blind retry, so every
// context-window overflow — predicted or reported by Codex — must surface as a
// 400 invalid_request_error carrying that phrase, never a retryable 502.

func TestContextOverflowMessageDetection(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		{"Codex ran out of room in the model's context window. Start a new thread or clear earlier history before retrying.", true},
		{"Your input exceeds the context window of this model.", true},
		{"context_length_exceeded", true},
		{"The prompt is too long for this model", true},
		{"upstream broke", false},
		{"model gpt-test is not available", false},
		{"Codex turn was interrupted", false},
	}
	for _, tt := range tests {
		if got := isContextOverflowMessage(tt.message); got != tt.want {
			t.Errorf("isContextOverflowMessage(%q) = %v, want %v", tt.message, got, tt.want)
		}
	}
}

func TestEstimatePromptTokensCountsImagesFlatNotByBase64Size(t *testing.T) {
	text := strings.Repeat("a", 4000)
	textOnly, err := ParseMessagesRequest([]byte(fmt.Sprintf(
		`{"model":"gpt-test","max_tokens":10,"messages":[{"role":"user","content":%q}]}`, text)))
	if err != nil {
		t.Fatal(err)
	}
	got := estimatePromptTokens(textOnly)
	if got < 900 || got > 1100 {
		t.Errorf("text-only estimate = %d, want ~1000 (bytes/4)", got)
	}

	image := strings.Repeat("A", 1<<20) // 1 MiB of base64 must not count as ~260K tokens
	withImage, err := ParseMessagesRequest([]byte(fmt.Sprintf(
		`{"model":"gpt-test","max_tokens":10,"messages":[{"role":"user","content":[`+
			`{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}},`+
			`{"type":"text","text":"look"}]}]}`, image)))
	if err != nil {
		t.Fatal(err)
	}
	got = estimatePromptTokens(withImage)
	if got > 5000 {
		t.Errorf("image estimate = %d, want flat per-image cost, not base64 length", got)
	}
	if got < promptGuardImageTokens {
		t.Errorf("image estimate = %d, want at least the flat image cost %d", got, promptGuardImageTokens)
	}
}

func oversizedMessagesBody(model string, bytes int) string {
	return fmt.Sprintf(
		`{"model":%q,"max_tokens":100,"messages":[{"role":"user","content":%q}]}`,
		model, strings.Repeat("x", bytes))
}

func TestHandlerRejectsPromptOverRealWindowBeforeCodex(t *testing.T) {
	executor := &fakeMessageExecutor{}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	// gpt-5.3-codex-spark's real window is 128000 tokens; ~600KB of text
	// estimates to ~150K tokens and must be rejected before reaching Codex.
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", oversizedMessagesBody("gpt-5.3-codex-spark", 600_000))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	var body AnthropicErrorResponse
	decodeBody(t, response, &body)
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("error type = %q, want invalid_request_error", body.Error.Type)
	}
	if !strings.Contains(body.Error.Message, "prompt is too long") ||
		!strings.Contains(body.Error.Message, "128000 maximum") {
		t.Errorf("message = %q, want canonical prompt-too-long with window", body.Error.Message)
	}
	if executor.translation.Model != "" {
		t.Errorf("executor was invoked for an over-window prompt: %+v", executor.translation)
	}

	// A prompt inside the window must still reach Codex.
	response = requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", oversizedMessagesBody("gpt-5.3-codex-spark", 10_000))
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("in-window status = %d, want 200", response.StatusCode)
	}
	if executor.translation.Model != "gpt-5.3-codex-spark" {
		t.Errorf("executor not invoked for in-window prompt: %+v", executor.translation)
	}
}

func TestHandlerSkipsWindowGuardForUnknownModels(t *testing.T) {
	executor := &fakeMessageExecutor{}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", oversizedMessagesBody("gpt-test", 600_000))
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a model with no catalog window", response.StatusCode)
	}
}

func TestCodexOverflowErrorBecomesPromptTooLong400(t *testing.T) {
	executor := &fakeMessageExecutor{err: errors.New(
		"Codex ran out of room in the model's context window. Start a new thread or clear earlier history before retrying.")}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(false))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	var body AnthropicErrorResponse
	decodeBody(t, response, &body)
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("error type = %q, want invalid_request_error", body.Error.Type)
	}
	if !strings.Contains(body.Error.Message, "prompt is too long") ||
		!strings.Contains(body.Error.Message, "ran out of room") {
		t.Errorf("message = %q, want prompt-too-long prefix with Codex cause", body.Error.Message)
	}
}

func TestCodexOverflowMidStreamEmitsInvalidRequestErrorEvent(t *testing.T) {
	executor := &fakeMessageExecutor{
		events: []StreamEvent{{Event: "message_start", Data: map[string]any{"type": "message_start"}}},
		err: errors.New(
			"Codex ran out of room in the model's context window. Start a new thread or clear earlier history before retrying."),
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(true))
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	text := string(data)
	if response.StatusCode != http.StatusOK || !strings.Contains(text, "event: error\n") {
		t.Fatalf("status=%d stream=%s", response.StatusCode, text)
	}
	if !strings.Contains(text, "invalid_request_error") || !strings.Contains(text, "prompt is too long") {
		t.Errorf("stream error event should carry invalid_request_error prompt-too-long, got: %s", text)
	}
}
