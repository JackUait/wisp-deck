# ChatGPT Inline Login Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make a signed-out OpenAI GPT launch authenticate the user through Codex app-server and continue into Claude automatically.

**Architecture:** Extend the existing app-server client with a managed ChatGPT login transaction that owns start, browser handoff, completion matching, cancellation, and post-login discovery. Call it from the adapter only when `account/read` reports no account; keep existing ChatGPT and API-key behavior unchanged.

**Tech Stack:** Go 1.24, Codex app-server JSON-RPC over stdio, macOS `open`, the existing fake-process/RPC test harnesses.

**Repository constraint:** Execute directly on the existing `main` branch. Do not create a branch, worktree, or detached checkout.

**Design reference:** `docs/plans/2026-07-19-chatgpt-inline-login-design.md`

---

### Task 1: Add a managed app-server login transaction

**Files:**
- Modify: `internal/gptbridge/rpc.go`
- Test: `internal/gptbridge/rpc_test.go`

**Step 1: Write the failing success-path test**

Add an RPC-harness test that starts with a signed-out `AppServer`, calls the new
login method, and requires this wire sequence:

```text
account/login/start
account/login/completed (matching notification)
account/read
model/list
```

Assert the start parameters contain:

```json
{
  "type": "chatgpt",
  "useHostedLoginSuccessPage": true,
  "appBrand": "chatgpt"
}
```

Assert the returned `authUrl` reaches the injected opener and the refreshed
account/model data is stored on `AppServer`.

**Step 2: Run the test to verify RED**

Run:

```bash
go test ./internal/gptbridge -run TestAppServerLoginChatGPT -count=1 -v
```

Expected: FAIL because the login transaction does not exist.

**Step 3: Implement the minimal login transaction**

Add strict protocol types for the login start response and completion
notification. Implement an `AppServer` method that starts managed ChatGPT
login, validates the login ID and HTTPS authentication URL, invokes an injected
URL opener, waits for the matching completion notification, then re-runs
`account/read` and `model/list`.

On context cancellation, issue a bounded best-effort
`account/login/cancel` request for the active login ID.

**Step 4: Run the focused test to verify GREEN**

Run:

```bash
go test ./internal/gptbridge -run TestAppServerLoginChatGPT -count=1 -v
```

Expected: PASS.

**Step 5: Add error and cancellation tests**

Cover:

- unrelated login completion notifications are ignored;
- browser-open failure is returned separately so callers can warn and keep
  waiting;
- a failed matching notification returns its error;
- cancellation sends `account/login/cancel`;
- malformed or non-HTTPS login results fail closed.

Run:

```bash
go test -race ./internal/gptbridge -run TestAppServerLoginChatGPT -count=1 -v
```

Expected: PASS.

### Task 2: Authenticate signed-out adapter launches

**Files:**
- Modify: `internal/gptbridge/adapter.go`
- Test: `internal/gptbridge/adapter_test.go`
- Verify: `cmd/wisp-deck-tui/claude_gpt_adapter_test.go`

**Step 1: Write the failing adapter regression test**

Create a fake Codex app-server script that initially returns `account: null`,
accepts `account/login/start`, emits a successful completion notification, then
returns a ChatGPT account and visible model on the refresh calls. Inject a URL
opener and assert Claude starts only after authentication succeeds.

**Step 2: Run the test to verify RED**

Run:

```bash
go test ./internal/gptbridge -run TestRunAdapterLogsInSignedOutChatGPTUser -count=1 -v
```

Expected: FAIL with the current signed-out `codex login` error.

**Step 3: Implement the adapter login gate**

Extend `AdapterOptions` with an injectable URL opener and login timeout. For a
signed-out account, print the authentication URL, attempt the opener, wait for
managed login, then validate the refreshed ChatGPT account and model catalog.
Use `open <url>` as the production opener. Do not invoke login for an existing
account and do not alter API-key rejection.

**Step 4: Run focused tests**

Run:

```bash
go test -race ./internal/gptbridge ./cmd/wisp-deck-tui \
  -run 'TestRunAdapter|TestValidateChatGPTSubscription|TestClaudeGPTAdapter' \
  -count=1
```

Expected: PASS.

### Task 3: Align live verification and user documentation

**Files:**
- Modify: `test/bash/claude_gpt_live_e2e_test.go`
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Test: `test/bash/chatgpt_subscription_docs_test.go`

**Step 1: Write failing documentation assertions**

Require the README and changelog to explain that selecting OpenAI GPT opens
ChatGPT login automatically when needed and that an API-key account remains
rejected.

Run:

```bash
go test ./test/bash -run 'TestChatGPTSubscriptionDocs|TestLiveGPTSubscriptionInClaude' -count=1 -v
```

Expected: FAIL on the old manual `codex login` instructions.

**Step 2: Update live preflight and docs**

Let the gated live test reach the adapter when Codex is signed out instead of
failing in preflight. Keep rejecting a known API-key login. Update setup and
recovery instructions to describe the automatic browser flow and copyable URL.

**Step 3: Run focused verification**

Run:

```bash
go test ./test/bash -run 'TestChatGPTSubscriptionDocs|TestLiveGPTSubscriptionInClaude' -count=1 -v
```

Expected: PASS (the live test skips unless explicitly enabled).

### Task 4: Full verification and local installation

**Files:**
- Verify all changed files.
- Update the local binary with `make install`.

**Step 1: Format and inspect**

Run:

```bash
gofmt -w internal/gptbridge/rpc.go internal/gptbridge/rpc_test.go \
  internal/gptbridge/adapter.go internal/gptbridge/adapter_test.go \
  test/bash/claude_gpt_live_e2e_test.go
git diff --check
```

Expected: no formatting or whitespace errors.

**Step 2: Run the full test suite**

Run:

```bash
WISP_DECK_TESTING=1 go test ./...
WISP_DECK_TESTING=1 ./run-tests.sh
```

Expected: PASS.

**Step 3: Run the gated live bridge test**

With the development machine's existing ChatGPT login:

```bash
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 \
  go test ./test/bash -run TestLiveGPTSubscriptionInClaude -count=1 -v
```

Expected: PASS through real Claude, Codex app-server, and ChatGPT subscription.
This verifies the already-authenticated path; the signed-out browser ceremony
is covered without touching real credentials by the fake app-server tests.

**Step 4: Install and verify the local binary**

Run:

```bash
make install
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
  "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose "$HOME/.local/bin/wisp-deck-tui"
```

Expected: install succeeds, command path matches, hashes match, and code
signature verification exits zero.
