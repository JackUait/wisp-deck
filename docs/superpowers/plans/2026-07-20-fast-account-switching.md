# Fast Account and Subscription Switching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Paint account/subscription choices in the resident native ledger with no pre-selection process, then apply the exact choice through the established relaunch machinery.

**Architecture:** Extend the ledger session snapshot with fully resolved switch rows and render them as an in-process Bubble Tea overlay. After confirmation, invoke one argv-safe Bash adapter that dispatches directly into existing account, subscription, or tool relaunch logic. Retain the standalone popup as compatibility fallback and collapse its capability checks to one probe.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, Bash, tmux, Go tests, shellcheck

## Global Constraints

- Work directly on the existing `main` branch; never create branches or worktrees.
- Follow strict TDD: add one failing behavior test, observe the expected failure, then add minimal production code.
- Preserve session-scoped account and subscription identity.
- Preserve unsent drafts, exact conversation resume, attention-generation fencing, shared account state, and cross-agent handoff.
- No external process, tmux query, or filesystem read may run from the chooser click/navigation/render path.
- Run shellcheck on modified scripts, the full test suite, and push before declaring completion.

---

### Task 1: Add the explicit post-selection adapter

**Files:**
- Modify: `internal/ledger/session.go`
- Modify: `internal/ledger/session_test.go`
- Modify: `lib/account-switch.sh`
- Modify: `test/bash/account_switch_subscription_test.go`
- Modify: `test/bash/account_switch_test.go`

**Interfaces:**
- Produces: `SwitchChoice{Kind SwitchKind, Value string}`
- Produces: `AccountSwitcher.Switch(context.Context, SessionContext, SwitchChoice) error`
- Produces: `apply_account_switch_choice <tmux> <relaunch-file> <account|subscription|tool> <value>`

- [x] **Step 1: Write the failing Go adapter test**

Assert that a subscription choice becomes one Bash invocation whose fixed program calls `apply_account_switch_choice`, passes paths/kind/value as argv, and contains neither `open_account_switcher` nor `--help`.

- [x] **Step 2: Run the Go test and verify RED**

Run:

```bash
go test ./internal/ledger -run TestExplicitAccountSwitcherAppliesChoiceWithoutOpeningPopupOrProbingCapabilities -count=1 -v
```

Expected: build failure because `SwitchChoice`, `SwitchSubscription`, and the three-argument switch interface do not exist.

- [x] **Step 3: Add typed choices and the fixed shell program**

Define:

```go
type SwitchKind string

const (
    SwitchAccount SwitchKind = "account"
    SwitchSubscription SwitchKind = "subscription"
    SwitchTool SwitchKind = "tool"
)

type SwitchChoice struct {
    Kind SwitchKind
    Value string
}
```

Change the adapter to validate the kind and execute a fixed program ending in:

```bash
apply_account_switch_choice tmux "$2" "$3" "$4"
```

- [x] **Step 4: Write failing Bash tests for all three choice kinds**

Each test must call `apply_account_switch_choice` directly and assert `respawn-pane` is logged while `display-popup` is absent.

- [x] **Step 5: Run each new Bash test and verify RED**

Run the individual `TestApplyAccountSwitchChoice_...` tests. Expected: exit 127 because the function does not exist.

- [x] **Step 6: Implement direct Bash dispatch**

Read the relaunch context once, resolve the session account/config stamps, and dispatch:

- tool → `relaunch_switch_tool`
- subscription → `_apply_subscription` plus the existing Claude relaunch
- account → persist the pointer, reset an active backend if needed, and use the existing relaunch

- [x] **Step 7: Run the new Go and Bash tests**

Expected: PASS.

### Task 2: Precompute immutable chooser rows

**Files:**
- Modify: `internal/ledger/session.go`
- Modify: `internal/ledger/session_test.go`

**Interfaces:**
- Produces: `SwitchOption{Choice, Label, Color, Glyph, Ready, Active}`
- Produces: `SessionContext.SwitchOptions []SwitchOption`

- [x] **Step 1: Write the failing session-source test**

Create Default, managed accounts, two ready subscriptions, and Codex fixtures. Stamp the live account/config through the fake tmux runner. Assert exact row order, identity fields, and active subscription.

- [x] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./internal/ledger -run TestSessionContextPrecomputesSwitchOptionsWithActiveSubscription -count=1 -v
```

Expected: build failure because `SwitchOption` and `SessionContext.SwitchOptions` do not exist.

- [x] **Step 3: Implement off-loop row resolution**

Parse `configs_dir`, resolve active account/config once, assign account/config colors, filter disabled subscriptions, evaluate readiness, append tool rows, and set exactly one active row.

- [x] **Step 4: Re-run the test**

Expected: PASS.

### Task 3: Render and navigate the chooser in-process

**Files:**
- Create: `internal/tui/ledger_switcher.go`
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`
- Modify: `cmd/wisp-deck-tui/ledger_cmd_test.go`

**Interfaces:**
- Consumes: `SessionContext.SwitchOptions`
- Consumes: `AccountSwitcher.Switch(ctx, session, choice)`
- Produces: immediate in-process overlay state and one asynchronous apply command after confirmation

- [x] **Step 1: Replace the old click test with the desired latency contract**

Assert pill click returns no command, opens the chooser, paints the option labels immediately, and records zero switcher calls.

- [x] **Step 2: Run the test and verify RED**

Expected: compile failure for missing chooser state and old switch interface.

- [x] **Step 3: Add minimal overlay state and rendering**

Add open/cursor fields, route keys/mouse to modal handlers, center a Lip Gloss card over the dimmed ledger, and perform no I/O from those methods.

- [x] **Step 4: Add asynchronous confirmation**

Enter/click on any ready row closes the chooser and returns a Tea command that calls the switch adapter. The adapter revalidates the choice against fresh account/subscription state; a genuinely active choice becomes a no-op in the relaunch layer, while a row that only looked active in the stale snapshot still applies exactly. Escape/outside click cancels.

- [x] **Step 5: Write and run the mouse-hover test**

Assert mouse motion updates the cursor and records zero adapter calls. Observe RED, add motion handling, then observe PASS.

- [x] **Step 6: Update the command-level wiring test**

Assert native ledger pill click paints the chooser without a command, then keyboard confirmation yields the asynchronous command.

- [x] **Step 7: Run affected Go packages**

Run:

```bash
go test ./internal/ledger ./internal/tui ./cmd/wisp-deck-tui -count=1
```

Expected: PASS.

### Task 4: Optimize the standalone compatibility fallback

**Files:**
- Modify: `lib/account-switch.sh`
- Modify: `test/bash/account_switch_test.go`

**Interfaces:**
- Produces: `_probe_switcher_capabilities`, populating all three existing cached verdicts from one help output

- [x] **Step 1: Write the failing one-probe test**

Mock `wisp-deck-tui` to append to a counter and advertise all flags. Call the three capability helpers and assert one counter line.

- [x] **Step 2: Run the test and verify RED**

Expected: FAIL with three probes.

- [x] **Step 3: Consolidate capability detection**

Execute help once, derive session/tool/subscription verdicts together, and keep the existing missing/unparseable-help compatibility semantics.

- [x] **Step 4: Re-run capability and legacy-skew tests**

Expected: PASS.

### Task 5: Verify and land

**Files:**
- Verify all modified Go, Bash, test, and design files.

- [x] **Step 1: Format and inspect**

```bash
gofmt -w cmd/wisp-deck-tui/ledger_cmd_test.go internal/ledger/session.go internal/ledger/session_test.go internal/tui/ledger.go internal/tui/ledger_switcher.go internal/tui/ledger_test.go test/bash/account_switch_subscription_test.go test/bash/account_switch_test.go
git diff --check
```

- [x] **Step 2: Run shellcheck**

```bash
shellcheck lib/account-switch.sh
```

Expected: no output.

- [x] **Step 3: Run focused account-switch regressions**

```bash
go test ./test/bash -run 'Test(ApplyAccountSwitchChoice|OpenAccountSwitcher|SwitcherSupports|SwitcherCapability|CurrentSessionIdentities|CurrentSessionAccount|CurrentSessionConfig|RelaunchAIPane|StashAIDraft|AccountSwitch_endToEnd|NoZshSpecialParameterNames)' -count=1
```

Expected: PASS.

- [x] **Step 4: Run the mandatory full suite**

```bash
./run-tests.sh
```

Expected: PASS.

- [x] **Step 5: Commit, rebase, push, and verify remote state**

```bash
git add cmd/wisp-deck-tui/ledger_cmd_test.go internal/ledger/session.go internal/ledger/session_test.go internal/tui/ledger.go internal/tui/ledger_switcher.go internal/tui/ledger_test.go lib/account-switch.sh test/bash/account_switch_subscription_test.go test/bash/account_switch_test.go docs/superpowers/specs/2026-07-20-fast-account-switching-design.md docs/superpowers/plans/2026-07-20-fast-account-switching.md
git commit -m "perf(switcher): open account choices in-process"
git pull --rebase
git push
git status
```

Expected: clean `main`, up to date with `origin/main`.
