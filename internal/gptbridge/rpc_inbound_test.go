package gptbridge

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// The bridge chooses how large its own messages may be; it does not get a vote
// on how large the app-server's are. Sizing the receive path with the send
// budget conflated the two, and the overflow was fatal: one long notification
// tore down the shared connection, so every concurrent turn on it died at once
// with "502 inject Claude history: app-server message exceeds 16777216 bytes" —
// blamed on whichever call happened to be in flight, and reported to Claude
// Code as a temporary server-side fault it should retry.
func TestRPCAcceptsAnAppServerMessageLargerThanTheSendBudget(t *testing.T) {
	const sendBudget = 1 << 20
	h := newRPCHarness(t, sendBudget)

	notifications := h.client.Notifications()
	bulk := strings.Repeat("y", 4*sendBudget)
	h.writeLine(t, `{"method":"item/completed","params":{"item":{"id":"big","type":"imageGeneration","result":"`+bulk+`"}}}`)

	select {
	case got := <-notifications:
		if got.Method != "item/completed" {
			t.Fatalf("method = %q", got.Method)
		}
		if !strings.Contains(string(got.Params), bulk) {
			t.Fatalf("params lost the payload (%d bytes)", len(got.Params))
		}
	case <-h.client.Done():
		t.Fatalf("a %d-byte app-server message killed the connection: %v",
			len(bulk), h.client.Err())
	case <-time.After(5 * time.Second):
		t.Fatal("oversized notification was never delivered")
	}

	// The connection must still be usable for the turns sharing it.
	assertRPCStillServes(t, h)
}

// The production scenario, at the production defaults and with nothing
// overridden: a notification just past the 16 MiB send budget. This is the one
// that shipped — a client built exactly like this one died on it, and because a
// single app-server connection is shared by every concurrent turn, it took the
// user's whole session down and blamed the call that happened to be in flight.
func TestRPCDefaultsAcceptAnAppServerMessagePastTheSendBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~17 MiB")
	}
	serverToClientR, serverToClientW := io.Pipe()
	clientToServerR, clientToServerW := io.Pipe()
	client := NewRPCClient(serverToClientR, clientToServerW, RPCOptions{})
	t.Cleanup(func() {
		client.Close()
		_ = serverToClientW.Close()
		_ = clientToServerR.Close()
	})
	go io.Copy(io.Discard, clientToServerR)

	if client.MaxMessageBytes() != defaultRPCMaxMessageBytes {
		t.Fatalf("send budget = %d, want the default", client.MaxMessageBytes())
	}
	payload := strings.Repeat("w", defaultRPCMaxMessageBytes+(1<<20))
	go func() {
		_, _ = io.WriteString(serverToClientW,
			`{"method":"item/completed","params":{"item":{"type":"imageGeneration","result":"`+
				payload+`"}}}`+"\n")
	}()

	select {
	case got := <-client.Notifications():
		if !strings.Contains(string(got.Params), payload) {
			t.Fatalf("a %d-byte notification arrived truncated (%d bytes of params)",
				len(payload), len(got.Params))
		}
	case <-client.Done():
		t.Fatalf("a %d-byte app-server message killed a default client: %v",
			len(payload), client.Err())
	case <-time.After(30 * time.Second):
		t.Fatal("oversized notification was never delivered")
	}
	if dropped := client.overlongMessages(); dropped != 0 {
		t.Fatalf("dropped %d messages; the default ceiling must clear the send budget", dropped)
	}
}

// The inbound ceiling that does exist is a memory backstop, not a kill switch.
// Passing it costs the one message, never the connection — because the
// connection is shared by every concurrent turn, and killing it turns one
// unreadable notification into a session-wide outage.
func TestRPCSurvivesAnAppServerMessageOverTheInboundCeiling(t *testing.T) {
	h := newRPCHarnessWithInbound(t, 1<<20, 64<<10)

	h.writeLine(t, `{"method":"item/completed","params":{"blob":"`+strings.Repeat("z", 256<<10)+`"}}`)

	select {
	case <-h.client.Done():
		t.Fatalf("a message over the inbound ceiling killed the connection: %v", h.client.Err())
	case <-time.After(200 * time.Millisecond):
	}
	if dropped := h.client.overlongMessages(); dropped != 1 {
		t.Fatalf("dropped %d messages, want 1: the ceiling was never reached, "+
			"so this test proves nothing", dropped)
	}
	assertRPCStillServes(t, h)
}

// assertRPCStillServes round-trips one request/response to prove the connection
// is alive and correctly framed after whatever the test just pushed through it.
func assertRPCStillServes(t *testing.T, h *rpcHarness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type reply struct {
		OK bool `json:"ok"`
	}
	done := make(chan error, 1)
	var got reply
	go func() { done <- h.client.Call(ctx, "thread/start", map[string]any{}, &got) }()

	request := h.readObject(t)
	var id json.RawMessage = request["id"]
	if _, err := io.WriteString(h.toClient,
		`{"jsonrpc":"2.0","id":`+string(id)+`,"result":{"ok":true}}`+"\n"); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("connection is unusable after the oversized message: %v", err)
	}
	if !got.OK {
		t.Fatal("response did not round-trip")
	}
}
