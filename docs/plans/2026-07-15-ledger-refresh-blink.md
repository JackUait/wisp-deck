# Ledger Refresh Blink Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Keep the native ledger's clean empty state visually stable during periodic background refreshes.

**Architecture:** Preserve the last accepted snapshot while the asynchronous refresh command runs. Restrict the existing `loading` flag to initial startup rather than setting it for every refresh generation.

**Tech Stack:** Go, Bubble Tea, standard `testing` package

---

### Task 1: Lock the stable-refresh behavior with a regression test

**Files:**
- Modify: `internal/tui/ledger_test.go`

**Step 1: Write the failing test**

Add a test that creates a clean, already-loaded ledger model, triggers
`ledgerRefreshTickMsg`, and checks that `View` contains `no changes` and does
not contain `loading changes` while the returned load command is pending.

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui -run TestLedgerRefreshTickKeepsLoadedEmptyStateVisible -count=1`

Expected: FAIL because `startLoad` currently sets `loading = true`.

### Task 2: Keep background refreshes invisible

**Files:**
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`

**Step 1: Write the minimal implementation**

Remove the unconditional `m.loading = true` assignment from `startLoad`.
Initial loading remains controlled by `LedgerOptions.Loading`, and accepted
results/errors continue to clear it.

Update the existing refresh-tick state assertion to expect that an already
loaded model remains non-loading.

**Step 2: Run focused tests**

Run: `go test ./internal/tui -run 'TestLedgerRefresh(TickKeepsLoadedEmptyStateVisible|TickStartsNewGeneration|InitLoadsSnapshotAsynchronously|AcceptsLatestAndSchedulesNextTick|ErrorRetainsSnapshotAndSchedulesRetry)$' -count=1`

Expected: PASS.

**Step 3: Run package and PTY regression tests**

Run: `go test ./internal/tui ./cmd/wisp-deck-tui -count=1`

Run: `go test ./test/bash -run 'TestNativeLedgerPTY|TestCompactView_uses_native|TestCompactView_forwards_native' -count=1`

Expected: PASS.

