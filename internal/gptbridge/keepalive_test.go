package gptbridge

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// blockingExecutor emits an opening event, then stalls exactly like Codex does
// while it reasons: no further events until released.
type blockingExecutor struct {
	release chan struct{}
	opening []StreamEvent
}

func (b *blockingExecutor) Execute(
	ctx context.Context,
	_ Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	if emit != nil && len(b.opening) > 0 {
		if err := emit(b.opening); err != nil {
			return AnthropicMessage{}, err
		}
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return AnthropicMessage{}, ctx.Err()
	}
	if emit != nil {
		if err := emit([]StreamEvent{
			{Event: "message_stop", Data: map[string]any{"type": "message_stop"}},
		}); err != nil {
			return AnthropicMessage{}, err
		}
	}
	return AnthropicMessage{ID: "msg_done", StopReason: "end_turn"}, nil
}

// Claude Code arms a byte-level stall watchdog on any text/event-stream body:
// 20s of raw-byte silence paints "Waiting for API response … check your
// network", and 300s aborts the stream and replays the whole turn. A reasoning
// Codex turn emits nothing the bridge forwards, so the bridge itself must keep
// bytes moving.
func TestStreamingTurnKeepsBytesFlowingWhileCodexIsSilent(t *testing.T) {
	executor := &blockingExecutor{
		release: make(chan struct{}),
		opening: []StreamEvent{{Event: "message_start", Data: map[string]any{"type": "message_start"}}},
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{KeepAliveInterval: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	defer close(executor.release)

	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(true))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	// Read in a goroutine: with no keepalive the body simply never yields a
	// line, so a blocking read would hang the test instead of failing it.
	pings := make(chan int, 1)
	go func() {
		reader := bufio.NewReader(response.Body)
		seen := 0
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				pings <- seen
				return
			}
			if strings.HasPrefix(line, "event: ping") {
				seen++
				if seen >= 2 {
					pings <- seen
					return
				}
			}
		}
	}()

	select {
	case seen := <-pings:
		if seen < 2 {
			t.Fatalf("stream ended after %d keepalive frames while Codex was still working", seen)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bridge emitted no keepalive frames while Codex was silent; " +
			"Claude Code paints its stalled banner after 20s of byte silence " +
			"and aborts the turn at 300s")
	}
}

// A quota exhaustion is deterministic: every retry fails identically until the
// window resets days later. Mapping it to a 5xx makes Claude Code retry ~11
// times over ~3 minutes and then report it as "usually temporary".
func TestCodexUsageLimitIsNotRetryable(t *testing.T) {
	executor := &fakeMessageExecutor{
		err: errors.New("You've hit your usage limit. Visit " +
			"https://chatgpt.com/codex/settings/usage to purchase more credits " +
			"or try again at Aug 20th, 2026 9:54 AM."),
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(false))
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 so Claude Code surfaces it instead of retrying",
			response.StatusCode)
	}
}

// Codex signing out mid-session is equally deterministic.
func TestCodexAuthFailureIsNotRetryable(t *testing.T) {
	executor := &fakeMessageExecutor{
		err: errors.New("restart ChatGPT subscription bridge: Codex is still " +
			"signed out after ChatGPT sign-in; run `codex login` and relaunch"),
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(false))
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

// A local model provider that is not listening is as deterministic as quota or
// sign-out: no retry can start a process that is not running. This is the
// codex-chatgpt-web setup, where ~/.codex/config.toml points openai_base_url at
// a loopback port served by a separate app; when that app exits, every turn
// fails identically until a human starts it again.
func TestCodexLocalModelProviderDownIsNotRetryable(t *testing.T) {
	executor := &fakeMessageExecutor{
		err: errors.New("stream disconnected before completion: error sending " +
			"request for url (http://127.0.0.1:17841/v1/responses)"),
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(false))
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 so Claude Code surfaces it instead of "+
			"retrying ~11 times against a dead loopback port", response.StatusCode)
	}
	data, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(data), "openai_base_url") {
		t.Fatalf("body = %s, want the remedy naming the setting that points "+
			"Codex at the local provider", data)
	}
}

// The counterweight: the same reqwest wording against the real upstream is a
// transient blip (the ChatGPT backend resets streams routinely), and a retry is
// exactly what should happen.
func TestCodexUpstreamSendFailureStaysRetryable(t *testing.T) {
	executor := &fakeMessageExecutor{
		err: errors.New("stream disconnected before completion: error sending " +
			"request for url (https://chatgpt.com/backend-api/codex/responses)"),
	}
	handler, err := NewHandler(executor, "secret", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	response := requestBridge(t, server.Client(), http.MethodPost,
		server.URL+"/v1/messages", "secret", validMessagesBody(false))
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 so a transient upstream failure is retried",
			response.StatusCode)
	}
}

func TestLocalModelProviderIsUnreachable(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{"loopback ipv4", "error sending request for url (http://127.0.0.1:17841/v1/responses)", true},
		{"localhost", "error sending request for url (http://localhost:17841/v1/responses)", true},
		{"loopback ipv6", "error sending request for url (http://[::1]:17841/v1/responses)", true},
		{"remote upstream", "error sending request for url (https://chatgpt.com/backend-api/codex/responses)", false},
		{"loopback named but no send failure", "http://127.0.0.1:17841 returned 500", false},
		{"unrelated failure", "You've hit your usage limit.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalModelProviderUnreachable(strings.ToLower(tt.message)); got != tt.want {
				t.Errorf("isLocalModelProviderUnreachable(%q) = %v, want %v",
					tt.message, got, tt.want)
			}
		})
	}
}
