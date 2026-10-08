package gptbridge

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

// TestLiveCodexReportsTokenUsage checks the one Codex-side behavior the Stats
// tab's cost figures depend on: that a real app-server still sends
// `thread/tokenUsage/updated` for a bridged turn, so the streamed usage carries
// Codex's own accounting instead of the bridge's byte estimate.
//
// Missing Codex accounting leaves zero billable usage. Run after a codex upgrade.
//
//	WISP_DECK_LIVE_TOKEN_USAGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexReportsTokenUsage -v
//
// It drives the real Engine, so the thread parameters cannot drift from the ones
// production sends. It costs one short turn against the signed-in ChatGPT account.
func TestLiveCodexReportsTokenUsage(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_TOKEN_USAGE_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_TOKEN_USAGE_E2E=1 to run the live token-usage check")
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
	engine, err := NewEngine(server.RPC, EngineOptions{
		PrivateCWD: t.TempDir(), Models: models,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	translation := testTranslation("Reply with exactly: ok")
	translation.Model = models[0]
	translation.EstimatedInputTokens = 1

	var events []StreamEvent
	message, err := engine.Execute(ctx, translation, func(batch []StreamEvent) error {
		events = append(events, batch...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	streamed := messageDeltaUsage(t, events)
	t.Logf("streamed usage: %+v", streamed)
	t.Logf("message usage:  %+v", message.Usage)

	if streamed.InputTokens+streamed.CacheReadInputTokens <= 1 {
		t.Fatalf("Codex sent no billable thread/tokenUsage/updated: %+v", streamed)
	}
	if !reflect.DeepEqual(streamed, message.Usage) {
		t.Errorf("streamed usage %+v differs from the non-streaming message usage %+v; "+
			"Claude Code records the streamed one", streamed, message.Usage)
	}
}
