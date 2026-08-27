package gptbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Codex's ThreadItem type is a vendor-controlled open enum: 0.146.0 already
// carries 18 variants, and every Codex release is free to add more. The bridge
// guard used to allow six of them by name and abort the turn on everything
// else, which made each new variant a fatal "502 forbidden Codex-owned tool
// item" the moment a user's Codex updated.
//
// That default is backwards. An unrecognized item is not evidence that Codex
// ran anything here — it is evidence that Codex's vocabulary grew. The real
// enforcement is the thread's own configuration (read-only sandbox, no network,
// approvalPolicy "never", no MCP servers, a private throwaway cwd, shell and
// unified_exec switched off); the guard is a tripwire for the small, stable set
// of items that would prove that enforcement had failed.
//
// The blast radius makes the direction non-negotiable: aborting kills the whole
// turn, and bridged turns run for hours. Shipped instances of this bug ended
// 4h20m, 4h48m and 1h17m of work with a 502 that told the user to "try again in
// a moment".
func TestEngineToleratesCodexItemTypesItDoesNotHostItself(t *testing.T) {
	// Every non-host variant of Codex 0.146.0's ThreadItem enum, plus a variant
	// that does not exist yet. The invented one is the point of the test: it
	// stands in for whatever the next Codex release adds, and it must not be
	// able to kill a turn.
	for _, itemType := range []string{
		"collabAgentToolCall", // shipped as a 502
		"subAgentActivity",    // shipped as a 502
		"plan",
		"sleep",
		"enteredReviewMode",
		"exitedReviewMode",
		"aVariantCodexHasNotShippedYet",
	} {
		t.Run(itemType, func(t *testing.T) {
			rpc := newFakeEngineRPC()
			rpc.onTurnStart = func(threadID, turnID string) {
				rpc.notifications <- notification(
					"item/started", threadID, turnID,
					`"startedAtMs":1,"item":{"id":"x","type":"`+itemType+`"}`,
				)
				rpc.notifications <- notification(
					"item/completed", threadID, turnID,
					`"item":{"id":"x","type":"`+itemType+`"}`,
				)
				completeTextTurn(rpc, threadID, turnID, "survived")
			}
			engine := newTestEngine(t, rpc)
			message, err := engine.Execute(context.Background(), testTranslation("work"), nil)
			if err != nil {
				t.Fatalf("%q aborted the turn: %v", itemType, err)
			}
			if len(message.Content) != 1 || message.Content[0].Text != "survived" {
				t.Fatalf("message = %+v", message)
			}
		})
	}
}

// The guard keeps its teeth for the items that can only appear if Codex really
// did reach this machine. These names are the stable ones — a shell escape
// shows up as commandExecution whatever else the enum grows.
func TestEngineRejectsCodexItemsThatReachThisMachine(t *testing.T) {
	for _, itemType := range []string{
		"commandExecution",
		"fileChange",
		"mcpToolCall",
		"imageView",
		"hookPrompt",
	} {
		t.Run(itemType, func(t *testing.T) {
			rpc := newFakeEngineRPC()
			rpc.onTurnStart = func(threadID, turnID string) {
				rpc.notifications <- notification(
					"item/started", threadID, turnID,
					`"startedAtMs":1,"item":{"id":"x","type":"`+itemType+`"}`,
				)
				completeTextTurn(rpc, threadID, turnID, "should not get here")
			}
			engine := newTestEngine(t, rpc)
			_, err := engine.Execute(context.Background(), testTranslation("unsafe"), nil)
			if err == nil || !strings.Contains(err.Error(), "forbidden Codex-owned tool") {
				t.Fatalf("%q was tolerated: err = %v", itemType, err)
			}
		})
	}
}

// Codex spawns collaboration sub-agents on its own initiative, and the bridge
// surfaces none of their work to Claude — so the tokens they burn are simply
// lost. `multi_agent` alone stopped covering it: 0.146.0 gates the feature
// behind multi_agent_v2, enable_fanout and collaboration_modes as well.
func TestEngineDisablesEveryCodexCollaborationFeature(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		completeTextTurn(rpc, threadID, turnID, "ok")
	}
	engine := newTestEngine(t, rpc)
	if _, err := engine.Execute(context.Background(), testTranslation("hi"), nil); err != nil {
		t.Fatal(err)
	}

	var params struct {
		Config struct {
			Features map[string]bool `json:"features"`
		} `json:"config"`
	}
	raw := rpc.paramsFor(t, "thread/start")
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode thread/start params: %v", err)
	}
	if params.Config.Features == nil {
		t.Fatalf("thread/start carried no features map: %s", raw)
	}
	for _, flag := range []string{
		"multi_agent", "multi_agent_v2", "enable_fanout", "collaboration_modes",
	} {
		enabled, present := params.Config.Features[flag]
		if !present {
			t.Errorf("feature %q is not declared, so Codex keeps its own default", flag)
			continue
		}
		if enabled {
			t.Errorf("feature %q = true, want false", flag)
		}
	}
}

// The bridge's read-only sandbox and `approvalPolicy: "never"` keep Codex off
// this machine, but the app-server also RENDERS them into the model's prompt —
// "`sandbox_mode` is `read-only`: The sandbox only permits reading files." and
// "Approval policy is currently never." A model that reads those as properties
// of the whole session refuses the host's own Edit/Write/Bash tools and tells
// the user to start a "write-enabled session", while Claude Code sits in
// bypassPermissions. That shipped: a GPT session abandoned four confirmed fixes
// over it. Codex's environment context also advertises the private throwaway
// cwd, which is the same misdirection about a different fact.
//
// Both blocks are gated by config keys, so the sandbox stays and only the prose
// goes: these switch prompt assembly, never enforcement.
func TestEngineSuppressesCodexSandboxPromptText(t *testing.T) {
	rpc := newFakeEngineRPC()
	rpc.onTurnStart = func(threadID, turnID string) {
		completeTextTurn(rpc, threadID, turnID, "ok")
	}
	engine := newTestEngine(t, rpc)
	if _, err := engine.Execute(context.Background(), testTranslation("hi"), nil); err != nil {
		t.Fatal(err)
	}

	var params struct {
		Sandbox        string `json:"sandbox"`
		ApprovalPolicy string `json:"approvalPolicy"`
		Config         struct {
			IncludePermissions *bool `json:"include_permissions_instructions"`
			IncludeEnvironment *bool `json:"include_environment_context"`
		} `json:"config"`
	}
	raw := rpc.paramsFor(t, "thread/start")
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode thread/start params: %v", err)
	}
	for _, key := range []struct {
		name  string
		value *bool
	}{
		{"include_permissions_instructions", params.Config.IncludePermissions},
		{"include_environment_context", params.Config.IncludeEnvironment},
	} {
		if key.value == nil {
			t.Errorf("config.%s is not declared, so Codex keeps describing a "+
				"sandbox that does not govern the host tools", key.name)
			continue
		}
		if *key.value {
			t.Errorf("config.%s = true, want false", key.name)
		}
	}
	// The enforcement these switches must never relax.
	if params.Sandbox != "read-only" {
		t.Errorf("sandbox = %q, want read-only", params.Sandbox)
	}
	if params.ApprovalPolicy != "never" {
		t.Errorf("approvalPolicy = %q, want never", params.ApprovalPolicy)
	}
}
