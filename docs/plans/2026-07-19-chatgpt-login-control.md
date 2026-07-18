# Visible ChatGPT Login Control Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a persistent ChatGPT sign-in/switch action and live Codex account state to the subscription modal, with automatic browser login when a signed-out ChatGPT profile is activated.

**Architecture:** Reuse Codex app-server's managed `account/read` and `account/login/*` protocol through small exported helpers in `internal/gptbridge`. Run every account check and login as a Bubble Tea command, stream the authentication URL into the modal before login completes, and reject stale results with an operation generation. Pass the already-resolved Codex executable from the shell launcher into the main-menu model.

**Tech Stack:** Go, Bubble Tea, Codex app-server JSONL protocol, Cobra, Bash, Go tests.

---

### Task 1: Reusable ChatGPT authentication operations

**Files:**
- Modify: `internal/gptbridge/adapter.go`
- Modify: `internal/gptbridge/rpc.go`
- Test: `internal/gptbridge/adapter_test.go`

**Step 1: Write failing helper tests**

Add tests that use the existing fake Codex app-server fixture to require:

```go
account, err := ReadChatGPTAccount(ctx, ChatGPTAuthOptions{
    CodexPath: fakeCodex,
})
```

to return signed-out, ChatGPT, and API-key account results, and:

```go
account, err := AuthenticateChatGPT(ctx, ChatGPTAuthOptions{
    CodexPath: fakeCodex,
    OpenURL: func(url string) error {
        opened = url
        return openErr
    },
}, func(event ChatGPTAuthEvent) {
    events = append(events, event)
})
```

to start managed browser login even when an account already exists, surface the
validated HTTPS URL and browser-open error before completion, return the
refreshed ChatGPT account, and reap app-server on success, error, or
cancellation.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/gptbridge -run 'Test(Read|Authenticate)ChatGPT' -count=1 -v
```

Expected: FAIL because the reusable helpers and event type do not exist.

**Step 3: Implement the helpers**

Add:

```go
type ChatGPTAuthOptions struct {
    CodexPath       string
    ClientVersion   string
    StartupTimeout  time.Duration
    LoginTimeout    time.Duration
    ShutdownTimeout time.Duration
    OpenURL         func(string) error
}

type ChatGPTAuthEvent struct {
    URL     string
    OpenErr error
}

func ReadChatGPTAccount(
    ctx context.Context,
    options ChatGPTAuthOptions,
) (AccountReadResult, error)

func AuthenticateChatGPT(
    ctx context.Context,
    options ChatGPTAuthOptions,
    present func(ChatGPTAuthEvent),
) (AccountReadResult, error)
```

Both helpers start `StartAppServer` with bounded startup/shutdown contexts and
always close it. `AuthenticateChatGPT` always invokes `LoginChatGPT`, including
for an already-authenticated user, so the visible action can switch accounts.
Its callback opens only the HTTPS URL already validated by `LoginChatGPT`, then
reports both the URL and any opener failure. After completion it calls
`ValidateChatGPTSubscription`.

Export the existing opener as:

```go
func OpenChatGPTAuthURL(authURL string) error
```

and keep `RunAdapter` using the same function.

**Step 4: Run focused and race tests**

Run:

```bash
go test -race ./internal/gptbridge -run 'Test(Read|Authenticate|RunAdapter|AppServerLogin)ChatGPT' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gptbridge/adapter.go internal/gptbridge/rpc.go internal/gptbridge/adapter_test.go
git commit -m "feat(bridge): expose ChatGPT auth flow"
```

### Task 2: Add asynchronous modal authentication state

**Files:**
- Modify: `internal/tui/mainmenu.go`
- Modify: `internal/tui/subscription_modal.go`
- Create: `internal/tui/subscription_modal_auth_test.go`

**Step 1: Write failing state-machine tests**

Define injected test functions for account checking and login, then cover:

```go
cmd := m.openSubscriptionModal()
msg := cmd()
updated, next := m.Update(msg)
```

Require the following transitions:

- opening the modal with a ChatGPT profile: `Checking…` → `Signed in` or
  `Signed out`;
- missing Codex or API-key auth: `Unavailable` plus an inline error;
- activating the login action while signed in or signed out starts one login;
- an authentication URL event updates the modal immediately and schedules the
  completion wait;
- successful completion becomes `Signed in`;
- cancellation/failure becomes `Signed out` or `Unavailable` and permits retry;
- repeated Enter while pending does not start another login;
- closing/reopening the modal cancels work and ignores an old generation.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModalChatGPTAuth' -count=1 -v
```

Expected: FAIL because the authentication state and commands do not exist.

**Step 3: Implement the state machine**

Add a modal-local authentication enum:

```go
type subscriptionAuthStatus int

const (
    subscriptionAuthChecking subscriptionAuthStatus = iota
    subscriptionAuthSignedOut
    subscriptionAuthSignedIn
    subscriptionAuthUnavailable
)
```

Store status, pending flag, fallback URL, opener error, operation generation,
and cancel function in `subscriptionModalState`. Add injected check/login
functions to `MainMenuModel`, with production defaults calling
`gptbridge.ReadChatGPTAccount` and `gptbridge.AuthenticateChatGPT`.

Use typed Bubble Tea messages for check results, URL events, and login
completion. The login command owns a buffered event channel. The first URL
event returns immediately to Bubble Tea; handling it schedules another command
that waits for completion. Every message includes its operation generation.

Change:

```go
func (m *MainMenuModel) openSubscriptionModal() tea.Cmd
```

to initialize `Checking…` and return the async account check. Propagate that
command from `focusEnter` and `settingsEnter`. Cancel pending authentication on
modal close. Keep the existing launch-time adapter login gate unchanged.

**Step 4: Run focused and race tests**

Run:

```bash
go test -race ./internal/tui -run 'TestSubscriptionModal(ChatGPTAuth|_open|_Esc|_CtrlC)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui/mainmenu.go internal/tui/subscription_modal.go internal/tui/subscription_modal_auth_test.go
git commit -m "feat(tui): track ChatGPT login state"
```

### Task 3: Render and activate the persistent login action

**Files:**
- Modify: `internal/tui/subscription_modal.go`
- Modify: `internal/tui/subscription_modal_render_test.go`
- Modify: `internal/tui/subscription_modal_mouse_test.go`
- Modify: `internal/tui/subscription_modal_test.go`

**Step 1: Write failing rendering and interaction tests**

Require every ChatGPT profile to render:

```text
Authentication  Checking…
[ Sign in / switch account ]
```

and later `Signed in`, `Signed out`, or `Unavailable`. While pending, require
`[ Waiting for browser… ]`; after receiving a URL, require the fallback URL and
any opener error to remain visible.

Require keyboard Enter and mouse click on the action to start login. Require
API-key rows to retain their editor behavior and Standard Claude to remain
unchanged.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModal.*(ChatGPT|Login|Auth)' -count=1 -v
```

Expected: FAIL on the missing action/status labels and hit target.

**Step 3: Implement rendering and input**

Include `subscriptionDetailAuth` in ChatGPT detail navigation. Render the
status in the connection section and the persistent action in the actions
section. Make `activateSubscriptionDetail` dispatch ChatGPT login for
`AuthCodexChatGPT`, while retaining API-key edit for `AuthAPIKey`.

Teach `subscriptionModalTarget` to return `subscriptionHitAuth` for both action
labels, and make `handleSubscriptionModalMouse` dispatch the same login command
as keyboard Enter. Disable duplicate activation while pending.

When `useSubscriptionProfile` successfully activates a ChatGPT profile and the
known state is signed out, return the login command automatically.

**Step 4: Run modal tests**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModal' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_render_test.go internal/tui/subscription_modal_mouse_test.go internal/tui/subscription_modal_test.go
git commit -m "feat(tui): add ChatGPT sign-in action"
```

### Task 4: Pass the Codex executable into the main menu

**Files:**
- Modify: `cmd/wisp-deck-tui/main_menu.go`
- Modify: `cmd/wisp-deck-tui/main_menu_test.go`
- Modify: `lib/menu-tui.sh`
- Modify: `test/bash/wrapper_claude_config_test.go`

**Step 1: Write failing CLI and shell tests**

Require `main-menu --codex /absolute/path/codex` to configure the model, reject
a non-absolute path, and require `select_project_interactive` to forward the
already-resolved `CODEX_CMD` without an additional resolver process.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'MainMenu.*Codex|Wrapper.*Codex|ClaudeConfig' -count=1 -v
```

Expected: FAIL because `main-menu` has no `--codex` flag.

**Step 3: Implement path wiring**

Add the optional absolute `--codex` flag, validate it in
`buildMainMenuModel`, and call:

```go
model.SetCodexPath(mainMenuCodexPath)
```

In `lib/menu-tui.sh`, append:

```bash
if [[ "${CODEX_CMD:-}" = /* ]]; then
  cmd_args+=("--codex" "$CODEX_CMD")
fi
```

The missing-path state is handled in the modal as `Unavailable`; no credentials
or environment secrets are forwarded.

**Step 4: Run focused tests**

Run:

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'MainMenu.*Codex|Wrapper.*Codex|ClaudeConfig' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/wisp-deck-tui/main_menu.go cmd/wisp-deck-tui/main_menu_test.go lib/menu-tui.sh test/bash/wrapper_claude_config_test.go
git commit -m "feat(menu): wire Codex login client"
```

### Task 5: Update user documentation

**Files:**
- Modify: `README.md`
- Modify: `test/bash/claude_gpt_docs_test.go`

**Step 1: Write a failing documentation assertion**

Require the ChatGPT subscription section to mention the persistent
`Sign in / switch account` control, automatic signed-out activation flow,
browser authentication, and the fact that profile deletion does not log Codex
out.

**Step 2: Run the assertion**

Run:

```bash
go test ./test/bash -run TestChatGPTSubscriptionDocs -count=1 -v
```

Expected: FAIL on the new wording.

**Step 3: Update the documentation**

Explain the modal status/action and retain the security boundary: Codex owns
credentials; Wisp Deck never reads or stores them.

**Step 4: Run the assertion**

Run:

```bash
go test ./test/bash -run TestChatGPTSubscriptionDocs -count=1 -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add README.md test/bash/claude_gpt_docs_test.go
git commit -m "docs: explain visible ChatGPT login"
```

### Task 6: Verify, install, and validate the local binary

**Files:**
- No source changes expected.

**Step 1: Run focused race tests**

```bash
go test -race ./internal/gptbridge ./internal/tui -count=1
```

Expected: PASS.

**Step 2: Run command and shell integration tests**

```bash
go test ./cmd/wisp-deck-tui ./test/bash -run 'GPT|Gpt|ChatGPT|MainMenu.*Codex|ClaudeConfig' -count=1
```

Expected: PASS.

**Step 3: Run the full repository suite**

```bash
go test ./... -count=1
./run-tests.sh
```

Expected: PASS. If unrelated pre-existing failures remain, record the exact
tests and confirm every changed package passes independently.

**Step 4: Run the live subscription bridge check**

```bash
WISP_DECK_LIVE_GPT_CLAUDE_E2E=1 go test ./test/bash -run TestLiveGPTSubscriptionInClaude -count=1 -v
```

Expected: PASS using the current ChatGPT subscription.

**Step 5: Install and validate**

```bash
make install
command -v wisp-deck-tui
shasum -a 256 bin/wisp-deck-tui ~/.local/bin/wisp-deck-tui
codesign --verify --verbose=2 ~/.local/bin/wisp-deck-tui
```

Expected:

- path is `~/.local/bin/wisp-deck-tui`;
- source and installed SHA-256 values match; and
- code signature is valid and satisfies its designated requirement.

**Step 6: Record relaunch requirement**

The final handoff must state that every running ledger pane/session must be
relaunched to load the new binary and provider environment.
