# Claude Bridge Auth Token Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent Claude Code's custom API-key confirmation by authenticating the private OpenAI GPT subscription bridge exclusively through `ANTHROPIC_AUTH_TOKEN`.

**Architecture:** Keep stripping all inherited Anthropic routes and credentials at the Claude child-process boundary, but emit only the loopback base URL and Bearer-token credential. The existing bridge server already accepts `Authorization: Bearer`, so the implementation is limited to environment construction and its boundary tests.

**Tech Stack:** Go, Claude Code 2.1.214, authenticated loopback HTTP, Codex app-server, macOS PTY and code signing

---

### Task 1: Lock the Child Environment Contract

**Files:**
- Modify: `internal/gptbridge/adapter_test.go`
- Test: `internal/gptbridge/adapter_test.go`

**Step 1: Update the environment unit test**

Change `TestBuildClaudeEnvironmentOverridesOnlyBridgeRouting` so its expected
environment still contains:

```text
ANTHROPIC_BASE_URL=http://127.0.0.1:4321
ANTHROPIC_AUTH_TOKEN=bridge-secret
NO_PROXY=example.com,127.0.0.1,localhost
```

Remove `ANTHROPIC_API_KEY=bridge-secret` from the expected values and add:

```go
if countEnv(got, "ANTHROPIC_API_KEY") != 0 {
	t.Fatalf("ANTHROPIC_API_KEY leaked into Claude environment: %q", got)
}
```

Continue asserting that the base URL, auth token, and `NO_PROXY` each occur
exactly once. The input fixture must keep an inherited
`ANTHROPIC_API_KEY=old` so the test proves it is stripped rather than merely
omitted from a clean environment.

**Step 2: Update the adapter boundary fixture**

In `TestRunAdapterLaunchesClaudeWithPrivateBridgeEnvironment`, change the fake
Claude script to record whether `ANTHROPIC_API_KEY` exists without expanding
an unset variable:

```sh
if env | grep '^ANTHROPIC_API_KEY=' >/dev/null; then
  printf 'KEY_SET=yes\n'
else
  printf 'KEY_SET=no\n'
fi
printf 'TOKEN=%s\n' "$ANTHROPIC_AUTH_TOKEN"
```

Replace the equal-key assertion with assertions that `KEY_SET=no`, the token
is non-empty, and the token is not the inherited value.

**Step 3: Run the focused tests to verify RED**

Run:

```bash
go test ./internal/gptbridge \
  -run 'Test(BuildClaudeEnvironment|RunAdapterLaunchesClaudeWithPrivateBridgeEnvironment)' \
  -count=1 -v
```

Expected: FAIL because the current implementation injects
`ANTHROPIC_API_KEY`.

### Task 2: Emit Only the Gateway Auth Token

**Files:**
- Modify: `internal/gptbridge/adapter.go:64-98`
- Test: `internal/gptbridge/adapter_test.go`

**Step 1: Make the minimal environment change**

Keep `ANTHROPIC_API_KEY` in the override-key set so inherited values are
removed:

```go
overrides := map[string]string{
	"ANTHROPIC_BASE_URL":   bridgeURL,
	"ANTHROPIC_API_KEY":    "",
	"ANTHROPIC_AUTH_TOKEN": bridgeKey,
}
```

Append only:

```go
result = append(result,
	"ANTHROPIC_BASE_URL="+bridgeURL,
	"ANTHROPIC_AUTH_TOKEN="+bridgeKey,
	"NO_PROXY="+noProxy,
)
```

Update the function comment to describe a gateway auth token rather than a
generic credential.

**Step 2: Format the changed Go files**

Run:

```bash
gofmt -w internal/gptbridge/adapter.go internal/gptbridge/adapter_test.go
```

Expected: no unrelated formatting changes.

**Step 3: Run the focused tests to verify GREEN**

Run:

```bash
go test ./internal/gptbridge \
  -run 'Test(BuildClaudeEnvironment|RunAdapterLaunchesClaudeWithPrivateBridgeEnvironment|HandlerUsesBearerKeyForSDKCompatibility)' \
  -count=1 -v
```

Expected: PASS.

**Step 4: Run package and race suites**

Run:

```bash
go test ./internal/gptbridge -count=1
go test -race ./internal/gptbridge -count=1
```

Expected: PASS.

**Step 5: Commit the implementation**

Stage only the two bridge files:

```bash
git add internal/gptbridge/adapter.go internal/gptbridge/adapter_test.go
git commit -m "fix(bridge): use token-only Claude auth"
```

### Task 3: Verify Claude and Install

**Files:**
- No source changes expected.

**Step 1: Run affected integration tests**

Run:

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'GPT|Gpt|ChatGPT' -count=1
```

Expected: PASS.

**Step 2: Run the gated live subscription test**

Run:

```bash
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 \
  go test ./test/bash -run TestLiveGPTSubscriptionInClaude -count=1 -v
```

Expected: PASS, proving Claude's Bearer token authenticates to the bridge and
the Bash tool continuation completes.

**Step 3: Verify interactive startup**

Launch Claude through the source adapter in a PTY with a temporary
`CLAUDE_CONFIG_DIR`. Complete only the theme selection.

Expected: Claude proceeds to its security/onboarding screen without rendering:

```text
Detected a custom API key in your environment
```

Terminate the temporary session and remove its config directory.

**Step 4: Install**

Run:

```bash
make install
```

Expected: build, signing, and installation succeed.

**Step 5: Verify the installed artifact**

Run:

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: correct path, matching hashes, and a valid signature.

**Step 6: Run an installed Claude continuation**

Use the installed adapter for a non-bare Claude print request that loads the
SessionStart skill and returns an exact marker.

Expected: the marker is returned successfully without a dual-credential
warning or API-key confirmation.

