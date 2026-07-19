# Subscription Sign-In Action Placement Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move the OpenAI / ChatGPT sign-in control into a dedicated first row under ACTIONS while preserving authentication behavior and responsive profile-management actions.

**Architecture:** Keep `subscriptionDetailAuth` and `subscriptionHitAuth` as the existing keyboard and mouse identities. Move only their rendered line and adjacent authentication feedback, then update the cursor-line geometry so the viewport follows the new visual order.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, table-driven TUI tests, macOS code signing

---

### Task 1: Lock the Visual and Cursor Order

**Files:**
- Modify: `internal/tui/subscription_modal_render_test.go`
- Modify: `internal/tui/subscription_modal_test.go`
- Modify: `internal/tui/subscription_modal_auth_test.go`
- Test: `internal/tui/subscription_modal_render_test.go`
- Test: `internal/tui/subscription_modal_test.go`
- Test: `internal/tui/subscription_modal_auth_test.go`

**Step 1: Add the failing render-order test**

Add a test in `subscription_modal_render_test.go` that opens the active OpenAI
GPT profile, renders enough detail height to avoid clipping, and finds these
line indexes with `subscriptionLineIndex`:

```go
connection := subscriptionLineIndex(lines, "CONNECTION")
modelRouting := subscriptionLineIndex(lines, "MODEL ROUTING")
actions := subscriptionLineIndex(lines, "ACTIONS")
signIn := subscriptionLineIndex(lines, "[ Sign in / switch account ]")
rename := subscriptionLineIndex(lines, "[ Rename ]")
```

Require all indexes to exist and:

```go
if !(connection < modelRouting &&
	modelRouting < actions &&
	actions < signIn &&
	signIn < rename) {
	t.Fatalf(
		"detail order connection=%d model=%d actions=%d sign-in=%d rename=%d:\n%s",
		connection, modelRouting, actions, signIn, rename,
		stripAnsi(strings.Join(lines, "\n")),
	)
}
```

This proves the button left CONNECTION, follows MODEL ROUTING, and occupies a
dedicated row above profile management.

**Step 2: Add the failing cursor-line test**

In `subscription_modal_test.go`, open the OpenAI GPT profile in the details
pane and set `detailCursor` to `subscriptionDetailAuth`.

Render the full detail lines, locate the sign-in line, and require:

```go
if got, want := m.subscriptionDetailCursorLine(), signIn; got != want {
	t.Fatalf("auth cursor line = %d, want rendered sign-in line %d", got, want)
}
```

Then set a manual URL, browser-open error, and auth error; focus Rename and
compare `subscriptionDetailCursorLine()` to the rendered Rename line. This
locks the cursor geometry across variable feedback rows.

**Step 3: Strengthen the pending-feedback order test**

In `TestSubscriptionModalChatGPTAuthRendersPersistentActionAndStatus`, also
set:

```go
m.subscriptionModal.auth.err = errors.New("login failed")
```

After rendering, find the indexes for:

```text
[ Waiting for browser… ]
Open manually:
browser unavailable
login failed
[ Rename ]
```

Require them to be strictly increasing. This keeps all authentication feedback
adjacent to sign-in and above the secondary management controls.

**Step 4: Format the tests**

Run:

```bash
gofmt -w \
  internal/tui/subscription_modal_render_test.go \
  internal/tui/subscription_modal_test.go \
  internal/tui/subscription_modal_auth_test.go
```

Expected: no unrelated formatting changes.

**Step 5: Run focused tests to verify RED**

Run:

```bash
go test ./internal/tui \
  -run 'TestSubscriptionModal_(signInActionFollowsModelRouting|chatGPTActionCursorLinesMatchRenderedRows|ChatGPTAuthRendersPersistentActionAndStatus)' \
  -count=1 -v
```

Expected: FAIL because sign-in still renders inside CONNECTION and cursor
geometry does not match its visible line.

### Task 2: Move Sign-In and Correct Viewport Geometry

**Files:**
- Modify: `internal/tui/subscription_modal.go`
- Test: `internal/tui/subscription_modal_render_test.go`
- Test: `internal/tui/subscription_modal_test.go`
- Test: `internal/tui/subscription_modal_auth_test.go`
- Test: `internal/tui/subscription_modal_mouse_test.go`

**Step 1: Remove sign-in from CONNECTION**

In `subscriptionDetailLines`, keep the Authentication and Endpoint rows but
remove the ChatGPT block that currently appends:

```go
[ Sign in / switch account ]
```

and its URL/error feedback before MODEL ROUTING.

Do not change the computed authentication status or endpoint.

**Step 2: Render sign-in first under ACTIONS**

Immediately after appending the ACTIONS section heading, add the ChatGPT-only
block:

```go
if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
	loginLabel := "[ Sign in / switch account ]"
	if m.subscriptionModal.auth.pending {
		loginLabel = "[ Waiting for browser… ]"
	}
	lines = append(lines, m.subscriptionActionLabel(
		subscriptionHitAuth,
		subscriptionDetailAuth,
		loginLabel,
		accent,
		label,
	))
	// Append the existing manual URL, browser-open error, and auth error rows.
}
```

Append Rename, Delete, and Save afterward through the unchanged
`subscriptionActionLines` helper. This guarantees a dedicated sign-in row at
every width while retaining existing wrapping for the three management
actions.

**Step 3: Update cursor-line geometry**

In `subscriptionDetailCursorLine`, retain the API-key row handling. After the
existing dirty/error adjustments and:

```go
line += 2 // blank line and heading before actions
```

handle ChatGPT before calculating management-action wrapping:

```go
if profile.Provider.Auth == claudeconfig.AuthCodexChatGPT {
	if cursor == subscriptionDetailAuth {
		return line
	}
	line++ // dedicated sign-in row
	if m.subscriptionModal.auth.url != "" {
		line++
	}
	if m.subscriptionModal.auth.openErr != nil {
		line++
	}
	if m.subscriptionModal.auth.err != nil {
		line++
	}
}
```

The existing one-line/pair/stacked action calculations then operate from the
first Rename/Delete/Save row.

**Step 4: Format the implementation**

Run:

```bash
gofmt -w internal/tui/subscription_modal.go
```

Expected: no unrelated formatting changes.

**Step 5: Run focused tests to verify GREEN**

Run:

```bash
go test ./internal/tui \
  -run 'TestSubscriptionModal_(signInActionFollowsModelRouting|chatGPTActionCursorLinesMatchRenderedRows|ChatGPTAuthRendersPersistentActionAndStatus)' \
  -count=1 -v
```

Expected: PASS.

**Step 6: Run keyboard, mouse, and render regression tests**

Run:

```bash
go test ./internal/tui \
  -run 'TestSubscriptionModal_(chatGPTNavigationIncludesLoginAction|actionRowUsesHorizontalKeyboardNavigation|actionRowCanReachSaveWithKeyboard|wideRenderShowsInventoryAndDetails|detailRenderUsesStructuredSections|ChatGPTAuthEnterStartsLogin|Mouse_clickChatGPTLoginStartsAuth)' \
  -count=1 -v
```

Expected: PASS.

**Step 7: Run the full TUI package**

Run:

```bash
go test ./internal/tui -count=1
```

Expected: PASS.

**Step 8: Commit**

Commit only:

```text
internal/tui/subscription_modal.go
internal/tui/subscription_modal_render_test.go
internal/tui/subscription_modal_test.go
internal/tui/subscription_modal_auth_test.go
```

Commit message:

```text
fix(tui): move ChatGPT sign-in into actions
```

### Task 3: Install and Verify the Local Modal

**Files:**
- No source changes expected.

**Step 1: Run adjacent command and TUI tests**

Run:

```bash
go test ./cmd/wisp-deck-tui ./internal/tui -count=1
```

Expected: PASS.

**Step 2: Install**

Run:

```bash
make install
```

Expected: build, signing, and local installation succeed.

**Step 3: Verify the installed artifact**

Run:

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: correct path, matching SHA-256, and a valid signature.

**Step 4: Inspect the rendered modal**

Launch Wisp Deck with the OpenAI / ChatGPT profile selected and open the
Subscriptions detail pane.

Expected visual hierarchy:

```text
CONNECTION
Authentication ...
Endpoint       Local Codex bridge

MODEL ROUTING
...

ACTIONS
[ Sign in / switch account ]
[ Rename ]  [ Delete ]  [ Save changes ]
```

Use keyboard Down from Fable and mouse click on sign-in to verify both target
the moved control.

**Step 5: Audit final scope**

Run:

```bash
git diff --check HEAD^
git status --short
```

Expected: no whitespace errors and only the pre-existing unrelated working
changes remain.
