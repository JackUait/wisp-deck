package gptbridge

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestLiveCodexReportsTokenUsage checks the one Codex-side behavior the Stats
// tab's cost figures depend on: that a real app-server still sends
// `thread/tokenUsage/updated` for a bridged turn, so the streamed usage carries
// Codex's own accounting instead of the bridge's byte estimate.
//
// message_start is emitted before the turn runs, so it can only carry
// estimatePromptTokens' bytes/4 guess with no cache split. message_delta is the
// only chance to correct it. While the delta reported output alone, every
// bridged turn was recorded as a fully uncached prompt: one month showed
// 46,044 of 46,066 GPT assistant messages with cache_read_input_tokens 0 and
// 20.4B tokens billed as fresh input, roughly 5x the real API-equivalent.
//
// Forwarding the real usage is worth nothing if Codex stops sending it, and it
// would fail silently — the reducer falls back to the estimate, which looks
// like a plausible number rather than an error. Run this after a codex upgrade.
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
	// A sentinel estimate no real prompt can produce: the turn carries the base
	// instructions and a tool schema, so anything the estimate could return is
	// thousands of tokens. If the streamed usage is still 1, the delta is the
	// fallback and Codex sent no accounting at all.
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
		t.Fatalf("streamed usage is still the estimate, so Codex sent no "+
			"thread/tokenUsage/updated and every bridged turn will be priced as an "+
			"uncached byte guess: %+v", streamed)
	}
	if streamed != message.Usage {
		t.Errorf("streamed usage %+v differs from the non-streaming message usage %+v; "+
			"Claude Code records the streamed one", streamed, message.Usage)
	}
}
