package gptbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeMessageExecutor struct {
	message     AnthropicMessage
	events      []StreamEvent
	err         error
	translation Translation
}

func (f *fakeMessageExecutor) Execute(
	_ context.Context,
	translation Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	f.translation = translation
	if emit != nil && len(f.events) > 0 {
		if err := emit(f.events); err != nil {
			return AnthropicMessage{}, err
		}
	}
	return f.message, f.err
}

func validMessagesBody(stream bool) string {
	return `{
		"model":"gpt-test",
		"max_tokens":100,
		"stream":` + map[bool]string{true: "true", false: "false"}[stream] + `,
		"messages":[{"role":"user","content":"hello"}]
	}`
}

func requestBridge(t *testing.T, client *http.Client, method, url, key, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		request.Header.Set("x-api-key", key)
	}
	if body != "" {
		request.Header.Set("content-type", "application/json")
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerRejectsMissingOrWrongBridgeKey(t *testing.T) {
	handler, err := NewHandler(&fakeMessageExecutor{}, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, key := range []string{"", "wrong"} {
		response := requestBridge(t, server.Client(), http.MethodPost, server.URL+"/v1/messages", key, validMessagesBody(false))
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("key %q: status = %d, want 401", key, response.StatusCode)
		}
		var body AnthropicErrorResponse
		decodeBody(t, response, &body)
		if body.Error.Type != "authentication_error" {
			t.Errorf("key %q: error = %+v", key, body)
		}
	}
}

func TestHandlerNonStreamingMessages(t *testing.T) {
	executor := &fakeMessageExecutor{message: AnthropicMessage{
		ID: "msg_test", Type: "message", Role: "assistant", Model: "gpt-test",
		Content:    []ResponseContentBlock{{Type: "text", Text: "hello back"}},
		StopReason: "end_turn", Usage: Usage{InputTokens: 2, OutputTokens: 3},
	}}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost, server.URL+"/v1/messages", "secret", validMessagesBody(false))
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d: %s", response.StatusCode, data)
	}
	if got := response.Header.Get("content-type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q", got)
	}
	var message AnthropicMessage
	decodeBody(t, response, &message)
	if message.Content[0].Text != "hello back" {
		t.Fatalf("message = %+v", message)
	}
	if executor.translation.Model != "gpt-test" || len(executor.translation.Input) != 1 {
		t.Fatalf("translation = %+v", executor.translation)
	}
}

func TestHandlerStreamingMessages(t *testing.T) {
	executor := &fakeMessageExecutor{
		message: AnthropicMessage{ID: "msg_stream", StopReason: "end_turn"},
		events: []StreamEvent{
			{Event: "message_start", Data: map[string]any{"type": "message_start"}},
			{Event: "message_stop", Data: map[string]any{"type": "message_stop"}},
		},
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost, server.URL+"/v1/messages", "secret", validMessagesBody(true))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("content-type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q", got)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "event: message_start\n") || !strings.Contains(text, "event: message_stop\n") {
		t.Fatalf("SSE = %s", text)
	}
}

func TestHandlerStreamingErrorAfterHeadersUsesErrorEvent(t *testing.T) {
	executor := &fakeMessageExecutor{
		events: []StreamEvent{{Event: "message_start", Data: map[string]any{"type": "message_start"}}},
		err:    errors.New("upstream broke"),
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost, server.URL+"/v1/messages", "secret", validMessagesBody(true))
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "event: error\n") ||
		!strings.Contains(string(data), "upstream broke") {
		t.Fatalf("status=%d stream=%s", response.StatusCode, data)
	}
}

func TestHandlerCountTokensAndHealthRequireAuth(t *testing.T) {
	handler, err := NewHandler(&fakeMessageExecutor{}, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	countBody := `{"model":"gpt-test","messages":[{"role":"user","content":"count these words"}]}`
	response := requestBridge(t, server.Client(), http.MethodPost, server.URL+"/v1/messages/count_tokens", "secret", countBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("count status = %d", response.StatusCode)
	}
	var count struct {
		InputTokens int64 `json:"input_tokens"`
	}
	decodeBody(t, response, &count)
	if count.InputTokens <= 0 {
		t.Fatalf("count = %+v", count)
	}

	response = requestBridge(t, server.Client(), http.MethodGet, server.URL+"/health", "secret", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	var health map[string]string
	decodeBody(t, response, &health)
	if health["status"] != "ok" {
		t.Fatalf("health = %v", health)
	}
}

func TestHandlerValidatesHeadersMethodPathAndBodyLimit(t *testing.T) {
	handler, err := NewHandler(&fakeMessageExecutor{}, "secret", ServerOptions{MaxBodyBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		mutate func(*http.Request)
		status int
	}{
		{"not found", http.MethodPost, "/unknown", `{}`, nil, http.StatusNotFound},
		{"wrong method", http.MethodGet, "/v1/messages", "", nil, http.StatusMethodNotAllowed},
		{"wrong content type", http.MethodPost, "/v1/messages", `{}`, func(r *http.Request) {
			r.Header.Set("content-type", "text/plain")
		}, http.StatusUnsupportedMediaType},
		{"missing version", http.MethodPost, "/v1/messages", `{}`, func(r *http.Request) {
			r.Header.Del("anthropic-version")
		}, http.StatusBadRequest},
		{"too large", http.MethodPost, "/v1/messages", strings.Repeat("x", 100), nil, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(tt.method, server.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("x-api-key", "secret")
			if tt.body != "" {
				request.Header.Set("content-type", "application/json")
				request.Header.Set("anthropic-version", "2023-06-01")
			}
			if tt.mutate != nil {
				tt.mutate(request)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tt.status {
				data, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, tt.status, data)
			}
		})
	}
}

func TestStartLoopbackServerBindsIPv4Loopback(t *testing.T) {
	bridge, err := StartLoopbackServer(&fakeMessageExecutor{}, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Shutdown(context.Background())
	address, err := net.ResolveTCPAddr("tcp", bridge.Address())
	if err != nil {
		t.Fatal(err)
	}
	if !address.IP.IsLoopback() || address.IP.String() != "127.0.0.1" {
		t.Fatalf("listener address = %s", bridge.Address())
	}
	response := requestBridge(t, bridge.Client(), http.MethodGet, bridge.URL()+"/health", "secret", "")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
}

func TestHandlerRejectsInvalidConstruction(t *testing.T) {
	if _, err := NewHandler(nil, "secret", ServerOptions{}); err == nil {
		t.Fatal("expected nil executor error")
	}
	if _, err := NewHandler(&fakeMessageExecutor{}, "", ServerOptions{}); err == nil {
		t.Fatal("expected empty key error")
	}
}

func TestHandlerUsesBearerKeyForSDKCompatibility(t *testing.T) {
	handler, err := NewHandler(&fakeMessageExecutor{message: AnthropicMessage{}}, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(validMessagesBody(false)))
	request.Header.Set("authorization", "Bearer secret")
	request.Header.Set("content-type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
}
