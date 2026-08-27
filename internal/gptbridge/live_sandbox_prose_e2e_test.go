package gptbridge

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLiveCodexSandboxProseIsSuppressed checks the one Codex-side behavior the
// fix for read-only refusals depends on: that `include_permissions_instructions`
// and `include_environment_context` still stop the app-server rendering the
// thread's sandbox into the model's prompt.
//
// The bridge runs Codex in a read-only sandbox with `approvalPolicy: "never"` so
// it cannot reach this machine. Codex used to describe that to the model —
// "`sandbox_mode` is `read-only`: The sandbox only permits reading files.",
// "Approval policy is currently never." — and a model reading those as
// session-wide refused the host's Edit/Write/Bash and told the user to start a
// "write-enabled session", while Claude Code was sitting in bypassPermissions.
// One shipped session abandoned four confirmed fixes over it.
//
// The two config keys are vendor-controlled, and Codex ignores config keys it
// does not recognize SILENTLY — so a rename or removal would bring the prose
// back with no error anywhere, and the unit test would still pass because it
// only checks that the bridge sends them. This is the only check that can see
// Codex's side. Run it after a codex upgrade.
//
//	WISP_DECK_LIVE_SANDBOX_PROSE_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexSandboxProseIsSuppressed -v
//
// It drives the real Engine, so the thread parameters cannot drift from the ones
// production sends. It costs one short turn against the signed-in ChatGPT account.
func TestLiveCodexSandboxProseIsSuppressed(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_SANDBOX_PROSE_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_SANDBOX_PROSE_E2E=1 to run the live sandbox-prose check")
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

	// The probe must not contain the strings it searches for: it becomes part of
	// the model's context, so a literal-minded model quoting the question back
	// would fail a working bridge — and this test runs after a codex upgrade,
	// exactly when a spurious red sends someone chasing a phantom regression.
	translation := testTranslation(
		"Quote verbatim every sentence in your context that describes how this " +
			"session is confined: what files may be read or written, and whether " +
			"you may ask for more access. If there are none, reply with exactly " +
			"NONE. Do not summarise; quote.")
	translation.Model = models[0]
	message, err := engine.Execute(ctx, translation, nil)
	if err != nil {
		t.Fatal(err)
	}

	var answer strings.Builder
	for _, block := range message.Content {
		if block.Type == "text" {
			answer.WriteString(block.Text)
		}
	}
	reply := answer.String()
	t.Logf("answer: %q", reply)
	// Match Codex's rendered prose, which neither the probe above nor
	// baseInstructions contain, so only Codex reinstating it can trip this.
	for _, prose := range []string{
		"The sandbox only permits reading files",
		"Approval policy is currently never",
	} {
		if strings.Contains(reply, prose) {
			t.Fatalf("Codex still renders %q into the model's prompt, so a bridged "+
				"session can again refuse edits as read-only: %q", prose, reply)
		}
	}
}
