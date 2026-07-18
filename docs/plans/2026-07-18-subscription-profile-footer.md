# Subscription Profile Footer Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove the profile-pane heading and pin Add profile to the bottom of the subscription inventory.

**Architecture:** Split `subscriptionProfileLines` into a top gutter, a scrolling profile-only viewport, and a fixed add-action row. Update profile visibility and mouse-row translation to match the new geometry without changing profile lifecycle behavior.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing ANSI-aware render and mouse tests.

**Repository constraint:** Work directly on the existing `main` branch. Do not create a branch, worktree, or subagent.

---

### Task 1: Pin the add action beneath the profile viewport

**Files:**
- Modify: `internal/tui/subscription_modal.go:1056-1078`
- Modify: `internal/tui/subscription_modal.go:1231-1322`
- Modify: `internal/tui/subscription_modal.go:1615-1627`
- Test: `internal/tui/subscription_modal_render_test.go`
- Test: `internal/tui/subscription_modal_mouse_test.go`
- Test: `internal/tui/subscription_modal_test.go`

**Step 1: Write failing layout regressions**

Replace the heading-breathing-room test with assertions that:

```go
lines := m.subscriptionProfileLines(subscriptionListWidth, 10)
if strings.Contains(stripAnsi(strings.Join(lines, "\n")), "PROFILES") {
	t.Fatal("profile pane kept its heading")
}
if strings.TrimSpace(stripAnsi(lines[0])) != "" {
	t.Fatal("profile pane lost its top gutter")
}
if !strings.Contains(stripAnsi(lines[1]), "Standard Claude") {
	t.Fatal("first profile does not follow the gutter")
}
if !strings.Contains(stripAnsi(lines[len(lines)-1]), "+ Add profile") {
	t.Fatal("Add profile is not pinned to the bottom")
}
```

Add a focused-add assertion proving the final row keeps the selection wash and
right inset. Keep the existing card target test so the fixed row must remain
mouse-actionable.

**Step 2: Run the focused tests and verify RED**

```bash
go test ./internal/tui -run \
  'TestSubscriptionModal_(profilePaneOmitsHeading|addProfileIsPinnedToBottom|profileRowsReserveRightInset|profileCursorStaysInScrolledViewport|Hit_profilesMappingsAndActions)' \
  -count=1
```

Expected: FAIL because `PROFILES` still renders and Add profile currently
scrolls directly after the final subscription.

**Step 3: Split the profile renderer**

Remove heading styles and heading output from `subscriptionProfileLines`.
Build `items` from subscription profiles only. Render one top-gutter row,
`height - 2` scrolling profile rows, and one fixed add row. For a one-row
viewport, return only the add action.

Preserve the existing row width, focus wash, readiness status, and right inset.

**Step 4: Align scrolling and hit-testing**

In `ensureSubscriptionProfileVisible`, make the viewport `bodyHeight - 2` and
the scrollable item count equal only the profile count. When Add profile is
focused, use the final profile as the visibility target.

In `subscriptionModalTarget`, detect `+ Add profile` before translating a
profile row. Translate profile rows with `cardY - 2 + profileOffset` because
only the top gutter precedes them.

**Step 5: Run subscription verification**

```bash
go test ./internal/tui -run 'TestSubscriptionModal' -count=1
go vet ./internal/tui
```

Expected: PASS.

**Step 6: Commit, verify, push, and install**

Commit the profile renderer, tests, design, and plan. Run the repository’s full
test suite, push `main`, run `make install`, and verify installed path, SHA-256
identity, and both code signatures.
