# Subscription Detail Simplification Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove the duplicated detail identity/readiness header and the visible Use profile action while keeping every remaining custom-profile action keyboard and mouse accessible.

**Architecture:** Make the provider row the first rendered detail row, remove Use from render, hit-test, and focus models, and make Rename the entry point to the custom action row. Preserve the existing `u` shortcut and activation implementation so this visual simplification does not change profile activation semantics.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing ANSI-aware render, keyboard, and mouse tests.

**Repository constraint:** Work directly on the existing `main` branch. Do not create a branch, worktree, or subagent.

---

### Task 1: Specify the simplified detail header and actions

**Files:**
- Modify: `internal/tui/subscription_modal_render_test.go`
- Modify: `internal/tui/subscription_modal_test.go`
- Modify: `internal/tui/subscription_modal_mouse_test.go`

**Step 1: Write the failing rendering tests**

Update `TestSubscriptionModal_detailRenderUsesStructuredSections` so the first
nonblank detail row starts with `PROVIDER`, and assert that the detail-only
output contains neither `OpenAI GPT` nor `● READY`.

Update the action-row tests to require this visible custom row:

```text
[ Rename ]  [ Delete ]  [ Save changes ]
```

Assert that `[ Use profile ]` is absent. Replace the Standard action test with
an assertion that Standard details contain neither `ACTIONS` nor any profile
action button.

**Step 2: Write the failing keyboard tests**

Change the final-setting navigation assertion to:

```go
if m.subscriptionModal.detailCursor != subscriptionDetailRename {
	t.Fatalf("Down from Fable selected %d, want Rename", m.subscriptionModal.detailCursor)
}
```

Start horizontal action tests from Rename: zero Right presses activate Rename,
one activates Delete, and two reach Save. Change Left from Rename to expect an
immediate return to the profiles pane.

**Step 3: Update the mouse contract**

Remove `[ Use profile ]` from the action hit-target table while retaining
Rename, Delete, and Save assertions.

**Step 4: Run the focused tests and verify RED**

```bash
go test ./internal/tui -run \
  'TestSubscriptionModal_(detailRenderUsesStructuredSections|wideActionsShareOneLine|standardProfileOmitsActions|leftFromRenameReturnsToProfiles|actionRowUsesHorizontalKeyboardNavigation|actionRowCanReachSaveWithKeyboard|chatGPTNavigationSkipsMissingAPIKeyRow|Hit_profilesMappingsAndActions)' \
  -count=1
```

Expected: FAIL because the old header and Use button still render, and the
action focus model still starts on Use.

### Task 2: Simplify rendering and interaction

**Files:**
- Modify: `internal/tui/subscription_modal.go`

**Step 1: Remove Use from focus and hit-test enums**

Delete `subscriptionDetailUse` and `subscriptionHitUse`. Keep
`useSubscriptionProfile` and the `u` key handler unchanged.

Make `subscriptionDetailRows` return no focusable rows for Standard Claude and
append `subscriptionDetailRename` after the editable custom-profile settings.
Make `moveSubscriptionAction` traverse only Rename, Delete, and Save.

When Up moves from Delete or Save, normalize the cursor to Rename before moving
back to the final editable setting. Initialize Standard details with no
focusable cursor.

**Step 2: Remove the duplicate identity header**

Delete `subscriptionIdentityLine` and build existing-profile detail output with
the provider row as line zero:

```go
lines := []string{
	providerLabel + green.Render(modalTruncate(profile.Provider.Name, providerWidth)),
}
```

Keep add-profile and lifecycle titles unchanged.

**Step 3: Render only supported actions**

Remove the Standard `ACTIONS` section. Change `subscriptionActionLines` to
accept and lay out Rename, Delete, and Save only. Remove the Use action label,
mouse target, and mouse dispatch branch.

**Step 4: Realign cursor scrolling**

Shift model and API-key cursor line calculations upward by one. Treat the
single-line custom action row as shared by Rename, Delete, and Save, with
narrow wrapping handled only for those visible controls.

**Step 5: Run the focused tests and verify GREEN**

```bash
go test ./internal/tui -run 'TestSubscriptionModal' -count=1
```

Expected: PASS.

**Step 6: Run package quality checks**

```bash
gofmt -w internal/tui/subscription_modal.go \
  internal/tui/subscription_modal_test.go \
  internal/tui/subscription_modal_render_test.go \
  internal/tui/subscription_modal_mouse_test.go
go vet ./internal/tui
git diff --check
```

Expected: all commands exit successfully.

### Task 3: Verify, land, and install

**Files:**
- Verify only

**Step 1: Run the complete test suite**

```bash
./run-tests.sh -p=1 -timeout=20m -count=1
```

If the known timing-sensitive compact-view idle-frame test flakes, run it
alone and run the deterministic remainder with that one test skipped.

**Step 2: Commit only the subscription detail files**

```bash
git add internal/tui/subscription_modal.go \
  internal/tui/subscription_modal_test.go \
  internal/tui/subscription_modal_render_test.go \
  internal/tui/subscription_modal_mouse_test.go \
  docs/plans/2026-07-19-subscription-detail-simplification.md
git commit -m "style(tui): simplify subscription details"
```

Preserve unrelated working-tree changes.

**Step 3: Synchronize and push main**

Fetch `origin/main`, verify the local branch contains it, then push without
rebasing unrelated dirty files.

**Step 4: Install and verify**

```bash
make install
command -v wisp-deck-tui
shasum -a 256 bin/wisp-deck-tui ~/.local/bin/wisp-deck-tui
codesign --verify --verbose=2 bin/wisp-deck-tui
codesign --verify --verbose=2 ~/.local/bin/wisp-deck-tui
```

Expected: the command resolves to `~/.local/bin/wisp-deck-tui`, both SHA-256
values match, and both signatures verify.
