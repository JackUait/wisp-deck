package gptbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

type rpcHarness struct {
	client     *RPCClient
	fromClient *bufio.Reader
	toClient   io.WriteCloser
}

func newRPCHarness(t *testing.T, maxBytes int) *rpcHarness {
	t.Helper()
	serverToClientR, serverToClientW := io.Pipe()
	clientToServerR, clientToServerW := io.Pipe()
	client := NewRPCClient(serverToClientR, clientToServerW, RPCOptions{
		MaxMessageBytes: maxBytes,
	})
	t.Cleanup(func() {
		client.Close()
		_ = serverToClientW.Close()
		_ = clientToServerR.Close()
	})
	return &rpcHarness{
		client:     client,
		fromClient: bufio.NewReader(clientToServerR),
		toClient:   serverToClientW,
	}
}

func (h *rpcHarness) readObject(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	line, err := h.fromClient.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read client message: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("decode client message %q: %v", line, err)
	}
	return got
}

func (h *rpcHarness) writeLine(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(h.toClient, line+"\n"); err != nil {
		t.Fatalf("write server message: %v", err)
	}
}

func TestRPCCallRoutesConcurrentResponsesByID(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	type result struct {
		Value string `json:"value"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var first, second result
	errs := make(chan error, 2)
	go func() { errs <- h.client.Call(ctx, "first", map[string]int{"n": 1}, &first) }()
	go func() { errs <- h.client.Call(ctx, "second", map[string]int{"n": 2}, &second) }()

	requests := []map[string]json.RawMessage{h.readObject(t), h.readObject(t)}
	byMethod := make(map[string]map[string]json.RawMessage)
	for _, request := range requests {
		var method string
		if err := json.Unmarshal(request["method"], &method); err != nil {
			t.Fatal(err)
		}
		byMethod[method] = request
		if _, ok := request["jsonrpc"]; ok {
			t.Fatal("Codex app-server messages must omit jsonrpc")
		}
	}

	h.writeLine(t, fmt.Sprintf(`{"id":%s,"result":{"value":"two"}}`, byMethod["second"]["id"]))
	h.writeLine(t, fmt.Sprintf(`{"id":%s,"result":{"value":"one"}}`, byMethod["first"]["id"]))
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if first.Value != "one" || second.Value != "two" {
		t.Fatalf("results = %#v, %#v", first, second)
	}
}

func TestRPCDispatchesNotificationAndAnswersServerRequest(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	h.writeLine(t, `{"method":"item/agentMessage/delta","params":{"delta":"hi"}}`)
	select {
	case got := <-h.client.Notifications():
		if got.Method != "item/agentMessage/delta" || !strings.Contains(string(got.Params), `"hi"`) {
			t.Fatalf("notification = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not delivered")
	}

	h.writeLine(t, `{"id":"tool-7","method":"item/tool/call","params":{"tool":"echo"}}`)
	var request ServerRequest
	select {
	case request = <-h.client.ServerRequests():
		if request.Method != "item/tool/call" || request.ID.String() != "tool-7" {
			t.Fatalf("server request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("server request was not delivered")
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.client.Respond(request.ID, map[string]any{
			"success": true,
			"contentItems": []map[string]string{
				{"type": "inputText", "text": "ok"},
			},
		})
	}()
	response := h.readObject(t)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if string(response["id"]) != `"tool-7"` || !strings.Contains(string(response["result"]), `"success":true`) {
		t.Fatalf("response = %v", response)
	}
}

func TestRPCReturnsStructuredServerError(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		var result any
		errCh <- h.client.Call(ctx, "bad/method", map[string]any{}, &result)
	}()
	request := h.readObject(t)
	h.writeLine(t, fmt.Sprintf(
		`{"id":%s,"error":{"code":-32601,"message":"not found","data":{"method":"bad/method"}}}`,
		request["id"],
	))
	err := <-errCh
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T %v, want *RPCError", err, err)
	}
	if rpcErr.Code != -32601 || rpcErr.Message != "not found" {
		t.Fatalf("RPC error = %+v", rpcErr)
	}
}

func TestRPCCloseUnblocksPendingCall(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	errCh := make(chan error, 1)
	go func() {
		var result any
		errCh <- h.client.Call(context.Background(), "wait", map[string]any{}, &result)
	}()
	_ = h.readObject(t)
	h.client.Close()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrRPCClosed) {
			t.Fatalf("Call error = %v, want ErrRPCClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending call remained blocked")
	}
}

func TestRPCFailsClosedOnDuplicateResponse(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		var result any
		errCh <- h.client.Call(ctx, "once", map[string]any{}, &result)
	}()
	request := h.readObject(t)
	line := fmt.Sprintf(`{"id":%s,"result":{}}`, request["id"])
	h.writeLine(t, line)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	h.writeLine(t, line)
	select {
	case <-h.client.Done():
		if err := h.client.Err(); err == nil || !strings.Contains(err.Error(), "unknown response id") {
			t.Fatalf("client error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("duplicate response did not close client")
	}
}

func TestRPCRejectsOversizedMessage(t *testing.T) {
	h := newRPCHarness(t, 64)
	_, _ = io.WriteString(h.toClient, `{"method":"event","params":{"value":"`+strings.Repeat("x", 128)+`"}}`+"\n")
	select {
	case <-h.client.Done():
		if err := h.client.Err(); err == nil || !strings.Contains(err.Error(), "message exceeds") {
			t.Fatalf("client error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("oversized message did not close client")
	}
}

func TestRPCRejectsMalformedEnvelope(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	h.writeLine(t, `{"jsonrpc":"2.0","method":"event"}`)
	select {
	case <-h.client.Done():
		if err := h.client.Err(); err == nil || !strings.Contains(err.Error(), "jsonrpc") {
			t.Fatalf("client error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("malformed envelope did not close client")
	}
}

func TestStartAppServerInitializesAndDiscoversAccountAndModels(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "wire.log")
	script := filepath.Join(dir, "fake-codex")
	body := `#!/bin/sh
set -eu
test "$1" = app-server
read first
printf '%s\n' "$first" >> "$FAKE_CODEX_LOG"
printf '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp/codex","platformFamily":"unix","platformOs":"macos"}}\n'
read initialized
printf '%s\n' "$initialized" >> "$FAKE_CODEX_LOG"
read account
printf '%s\n' "$account" >> "$FAKE_CODEX_LOG"
printf '{"id":2,"result":{"account":{"type":"chatgpt","email":null,"planType":"plus"},"requiresOpenaiAuth":true}}\n'
read models
printf '%s\n' "$models" >> "$FAKE_CODEX_LOG"
printf '{"id":3,"result":{"data":[{"id":"gpt-test","model":"gpt-test","displayName":"GPT Test","description":"test","hidden":false,"isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[]}]}}\n'
while read line; do :; done
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_LOG", logPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server, err := StartAppServer(ctx, AppServerOptions{
		CodexPath:     script,
		ClientVersion: "test-version",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())

	if server.Account.Account == nil || server.Account.Account.Type != "chatgpt" {
		t.Fatalf("account = %+v", server.Account)
	}
	if len(server.Models) != 1 || server.Models[0].ID != "gpt-test" {
		t.Fatalf("models = %+v", server.Models)
	}
	wire, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	for _, want := range []string{
		`"method":"initialize"`,
		`"experimentalApi":true`,
		`"method":"initialized"`,
		`"method":"account/read"`,
		`"method":"model/list"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wire log missing %q:\n%s", want, text)
		}
	}
}

func TestStartAppServerHandshakeFailureReapsUncooperativeChild(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "fake-codex")
	body := `#!/bin/sh
set -eu
echo "$$" > "$FAKE_CODEX_PID"
read first
printf '{"id":1,"result":{}}\n'
trap '' HUP TERM
while :; do sleep 1; done
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX_PID", pidFile)

	started := time.Now()
	_, err := StartAppServer(context.Background(), AppServerOptions{
		CodexPath:       script,
		ClientVersion:   "test-version",
		ShutdownTimeout: 100 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "missing userAgent") {
		t.Fatalf("StartAppServer error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("handshake cleanup took %s, want under 2s", elapsed)
	}
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &pid); err != nil {
		t.Fatal(err)
	}
	if processExists(pid) {
		t.Fatalf("uncooperative app-server process %d still exists", pid)
	}
}

func TestRPCConcurrentCallsUseUniqueIDs(t *testing.T) {
	h := newRPCHarness(t, 1<<20)
	const count = 32
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errs := make(chan error, count)
	var wg sync.WaitGroup
	wg.Add(count)
	for range count {
		go func() {
			defer wg.Done()
			var result struct{}
			errs <- h.client.Call(ctx, "same", map[string]any{}, &result)
		}()
	}
	ids := make(map[string]bool, count)
	for range count {
		request := h.readObject(t)
		id := string(request["id"])
		if ids[id] {
			t.Fatalf("duplicate request id %s", id)
		}
		ids[id] = true
		h.writeLine(t, fmt.Sprintf(`{"id":%s,"result":{}}`, id))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
