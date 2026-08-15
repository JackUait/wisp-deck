package gptbridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLiveInjectItemsAppends checks the one Codex-side behavior the bridge's
// chunked history injection depends on: that repeated thread/inject_items calls
// APPEND to the thread in order, rather than replacing what came before.
//
// A conversation's transport size is unbounded with respect to its token cost
// (base64 images are ~1600 tokens and hundreds of kilobytes apiece), so a
// history that fits the model's context window regularly outgrows one
// app-server message and must be split. If a Codex update ever made a second
// inject call replace the first, every bridged session with a large history
// would silently lose all but its last chunk — the unit tests cannot see that,
// because they own both ends of the protocol. Run it after a codex upgrade.
//
//	WISP_DECK_LIVE_INJECT_E2E=1 go test ./internal/gptbridge/ -run TestLiveInjectItemsAppends -v
//
// It costs one short turn against the signed-in ChatGPT account.
func TestLiveInjectItemsAppends(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_INJECT_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_INJECT_E2E=1 to run the live inject_items check")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	server, err := StartAppServer(ctx, AppServerOptions{
		CodexPath: codexPath, ClientVersion: "2.30.0", ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = server.Close(closeCtx)
	}()

	models := subscriptionModelNames(server.Models)
	if len(models) == 0 {
		t.Fatal("no subscription models")
	}

	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := server.RPC.Call(ctx, "thread/start", map[string]any{
		"model": models[0], "cwd": t.TempDir(), "ephemeral": true,
		"approvalPolicy": "never", "sandbox": "read-only",
		"allowProviderModelFallback": false,
		"environments":               []any{}, "runtimeWorkspaceRoots": []string{},
	}, &started); err != nil {
		t.Fatal(err)
	}

	for _, text := range []string{"Remember: SECRET ALPHA is 4711.", "Remember: SECRET BRAVO is 8352."} {
		if err := server.RPC.Call(ctx, "thread/inject_items", map[string]any{
			"threadId": started.Thread.ID,
			"items": []map[string]any{{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": text}},
			}},
		}, &struct{}{}); err != nil {
			t.Fatalf("inject %q: %v", text, err)
		}
	}

	var turn struct{}
	if err := server.RPC.Call(ctx, "turn/start", map[string]any{
		"threadId": started.Thread.ID,
		"input": []map[string]any{{
			"type": "text",
			"text": "Reply with the two secret numbers you were told, in the order " +
				"they were given, separated by one space, and nothing else. If you " +
				"were told only one, reply with just that one.",
		}},
		"model": models[0], "approvalPolicy": "never", "environments": []any{},
		"sandboxPolicy": map[string]any{"type": "readOnly", "networkAccess": false},
	}, &turn); err != nil {
		t.Fatal(err)
	}

	answer := liveTurnAnswer(t, ctx, server.RPC)
	t.Logf("answer: %q", answer)
	if !strings.Contains(answer, "4711 8352") {
		t.Fatalf("answer = %q; a second thread/inject_items no longer appends in "+
			"order after the first, so chunked history injection is unsafe", answer)
	}
}

func liveTurnAnswer(t *testing.T, ctx context.Context, rpc *RPCClient) string {
	t.Helper()
	var answer strings.Builder
	for {
		select {
		case notification := <-rpc.Notifications():
			switch notification.Method {
			case "item/agentMessage/delta":
				var params struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(notification.Params, &params) == nil {
					answer.WriteString(params.Delta)
				}
			case "turn/completed":
				return answer.String()
			case "turn/failed":
				t.Fatalf("turn failed: %s", notification.Params)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for the turn")
		case <-rpc.Done():
			t.Fatalf("app-server connection died: %v", rpc.Err())
		}
	}
}
