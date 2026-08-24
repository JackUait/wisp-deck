package gptbridge

import (
	"bufio"
	"context"
	"errors"
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
