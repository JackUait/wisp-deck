package gptbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// wireRecorder is the app-server side of a real RPCClient: it answers the calls
// one bridged turn makes and records the exact byte size of every line the
// bridge wrote. The wire is the only place the per-message cap can actually be
// observed, so a chunking bug cannot hide behind a stub that never serializes.
type wireRecorder struct {
	mu       sync.Mutex
	methods  []string
	sizes    []int
	injected []json.RawMessage
}

func (r *wireRecorder) record(method string, size int, items []json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods = append(r.methods, method)
	r.sizes = append(r.sizes, size)
	if method == "thread/inject_items" {
		r.injected = append(r.injected, items...)
	}
}

func (r *wireRecorder) snapshot() ([]string, []int, []json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.methods...),
		append([]int(nil), r.sizes...),
		append([]json.RawMessage(nil), r.injected...)
}

func (r *wireRecorder) count(method string) int {
	methods, _, _ := r.snapshot()
	total := 0
	for _, got := range methods {
		if got == method {
			total++
		}
	}
	return total
}

func startWiredAppServer(t *testing.T, maxBytes int) (*RPCClient, *wireRecorder) {
	t.Helper()
	serverToClientR, serverToClientW := io.Pipe()
	clientToServerR, clientToServerW := io.Pipe()
	client := NewRPCClient(serverToClientR, clientToServerW, RPCOptions{MaxMessageBytes: maxBytes})
	recorder := &wireRecorder{}

	go func() {
		defer serverToClientW.Close()
		scanner := bufio.NewScanner(clientToServerR)
		scanner.Buffer(make([]byte, 64<<10), 256<<20)
		reply := func(format string, args ...any) bool {
			_, err := fmt.Fprintf(serverToClientW, format+"\n", args...)
			return err == nil
		}
		for scanner.Scan() {
			line := scanner.Bytes()
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Items []json.RawMessage `json:"items"`
				} `json:"params"`
			}
			if json.Unmarshal(line, &request) != nil {
				continue
			}
			recorder.record(request.Method, len(line)+1, request.Params.Items)
			switch request.Method {
			case "thread/start":
				if !reply(`{"id":%s,"result":{"thread":{"id":"thread-1"}}}`, request.ID) {
					return
				}
			case "turn/start":
				if !reply(`{"id":%s,"result":{"turn":{"id":"turn-1","status":"inProgress","items":[]}}}`, request.ID) {
					return
				}
				if !reply(`{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"agent","delta":"ok"}}`) {
					return
				}
				if !reply(`{"method":"thread/tokenUsage/updated","params":{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"last":{"inputTokens":10,"cachedInputTokens":2,"outputTokens":3,"reasoningOutputTokens":0,"totalTokens":13}}}}`) {
					return
				}
				if !reply(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}}`) {
					return
				}
			default:
				if !reply(`{"id":%s,"result":{}}`, request.ID) {
					return
				}
			}
		}
	}()

	t.Cleanup(func() {
		client.Close()
		_ = serverToClientW.Close()
		_ = clientToServerR.Close()
	})
	return client, recorder
}

// historyItems builds count message items of roughly itemBytes each, standing
// in for the base64 image payloads that dominate a real conversation's
// transport size.
func historyItems(count, itemBytes int) []map[string]any {
	items := make([]map[string]any, 0, count)
	for index := range count {
		items = append(items, map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": fmt.Sprintf("item-%d-", index) + strings.Repeat("x", itemBytes),
			}},
		})
	}
	return items
}

// A conversation's transport size is dominated by base64 image data, which
// costs a flat ~1600 tokens per image but hundreds of kilobytes apiece. A
// history that sits comfortably inside the model's context window therefore
// routinely exceeds the app-server's per-message cap — 82 images and 16.0 MB of
// base64 wedged a real session permanently, because every retry re-sent the
// same oversized message. app-server appends each thread/inject_items call's
// items to the thread in order, so the history must be split across as many
// calls as the cap requires.
func TestEngineInjectsHistoryTooLargeForOneAppServerMessage(t *testing.T) {
	const maxBytes = 256 << 10
	client, recorder := startWiredAppServer(t, maxBytes)
	engine, err := NewEngine(client, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL: time.Second, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	translation := testTranslation("hi")
	translation.History = historyItems(24, 64<<10)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := engine.Execute(ctx, translation, nil); err != nil {
		t.Fatalf("oversized history failed to inject: %v", err)
	}

	methods, sizes, injected := recorder.snapshot()
	if injections := recorder.count("thread/inject_items"); injections < 2 {
		t.Fatalf("history was sent in %d inject calls; it must be split: %v", injections, methods)
	}
	for index, size := range sizes {
		if size > maxBytes {
			t.Errorf("%s message is %d bytes on the wire, over the %d cap", methods[index], size, maxBytes)
		}
	}
	if len(injected) != len(translation.History) {
		t.Fatalf("injected %d items, want %d", len(injected), len(translation.History))
	}
	for index, item := range injected {
		want, err := json.Marshal(translation.History[index])
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(item) || string(item) != string(want) {
			t.Fatalf("item %d changed or moved during chunking", index)
		}
	}
	select {
	case <-client.Done():
		t.Fatalf("connection died on an oversized history: %v", client.Err())
	default:
	}
}

// imageHeavyRequest is a conversation of `images` screenshots read back as tool
// results — the shape that wedged a real session.
func imageHeavyRequest(images, decodedBytes int) MessagesRequest {
	pixels := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("wisp"), decodedBytes/4))
	messages := make([]Message, 0, images*2+1)
	for index := range images {
		id := fmt.Sprintf("toolu_read_%d", index)
		messages = append(messages,
			Message{Role: "assistant", Content: []ContentBlock{{
				Type: "tool_use", ID: id, Name: "Read",
				Input: json.RawMessage(fmt.Sprintf(`{"file_path":"/frames/%d.png"}`, index)),
			}}},
			Message{Role: "user", Content: []ContentBlock{{
				Type: "tool_result", ToolUseID: id, ToolContent: []ContentBlock{{
					Type:  "image",
					Image: &ImageSource{Type: "base64", MediaType: "image/png", Data: pixels},
				}},
			}}},
		)
	}
	return MessagesRequest{
		Model: "gpt-5.6-sol", MaxTokens: 4096,
		Messages: append(messages, Message{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "What changed between the frames?"},
		}}),
	}
}

// The context guard prices an image at a flat ~1600 tokens because its base64
// payload is orders of magnitude larger than its token cost. That is right for
// the model and irrelevant to the wire, so the two measures drift apart without
// limit: the session that wedged carried 82 images and 16.0 MB of base64 inside
// a ~131K-token conversation — comfortably within the model's 272K window, and
// just past the app-server's 16 MiB per-message cap. Whatever the guard admits,
// the transport has to be able to carry.
func TestATransportCarriesEveryConversationTheContextGuardAdmits(t *testing.T) {
	request := imageHeavyRequest(34, 400<<10)
	translation, err := TranslateRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	window, known := modelContextWindow(translation.Model)
	if !known {
		t.Fatalf("model %q has no catalog window, so the guard cannot admit it", translation.Model)
	}
	if translation.EstimatedInputTokens > int64(window) {
		t.Fatalf("estimate %d exceeds the %d window: the guard rejects this "+
			"conversation, so it never reaches the transport",
			translation.EstimatedInputTokens, window)
	}

	client, recorder := startWiredAppServer(t, defaultRPCMaxMessageBytes)
	engine, err := NewEngine(client, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL: time.Second, Models: []string{translation.Model},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := engine.Execute(ctx, translation, nil); err != nil {
		t.Fatalf("a conversation inside the model's window could not be sent: %v", err)
	}
	methods, sizes, injected := recorder.snapshot()
	for index, size := range sizes {
		if size > defaultRPCMaxMessageBytes {
			t.Errorf("%s message is %d bytes, over the %d cap",
				methods[index], size, defaultRPCMaxMessageBytes)
		}
	}
	if len(injected) != len(translation.History) {
		t.Errorf("injected %d items, want %d", len(injected), len(translation.History))
	}
	// Keeps the fixture honest: a smaller one would fit in a single message and
	// pass without exercising the split at all.
	if injections := recorder.count("thread/inject_items"); injections < 2 {
		t.Fatalf("the fixture fit in %d message(s); it no longer reaches the cap", injections)
	}
}

// A single item larger than the cap cannot be split, so the bridge must say so
// as a request-shaped failure rather than hand the transport a message it will
// refuse and let the turn look like a retryable transport fault.
func TestEngineRejectsASingleHistoryItemLargerThanTheCap(t *testing.T) {
	const maxBytes = 64 << 10
	client, _ := startWiredAppServer(t, maxBytes)
	engine, err := NewEngine(client, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL: time.Second, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	translation := testTranslation("hi")
	translation.History = historyItems(1, maxBytes*2)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = engine.Execute(ctx, translation, nil)
	var oversized oversizedMessageError
	if !errors.As(err, &oversized) {
		t.Fatalf("error = %v, want an oversized-message error", err)
	}
	select {
	case <-client.Done():
		t.Fatalf("connection died on an unsendable item: %v", client.Err())
	default:
	}
}

// The final user message is one turn's input and cannot be split either, so an
// oversized paste has to fail the same way: as the request's own shape, on a
// connection that stays alive for the next turn.
func TestEngineRejectsTurnInputLargerThanTheCap(t *testing.T) {
	const maxBytes = 64 << 10
	client, _ := startWiredAppServer(t, maxBytes)
	engine, err := NewEngine(client, EngineOptions{
		PrivateCWD: t.TempDir(), ToolBatchWindow: 5 * time.Millisecond,
		PendingTTL: time.Second, Models: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = engine.Execute(ctx, testTranslation(strings.Repeat("x", maxBytes*2)), nil)
	var oversized oversizedMessageError
	if !errors.As(err, &oversized) {
		t.Fatalf("error = %v, want an oversized-message error", err)
	}
	select {
	case <-client.Done():
		t.Fatalf("connection died on an unsendable turn input: %v", client.Err())
	default:
	}
}
