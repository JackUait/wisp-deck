# Subscription Modal Visual Polish Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Give the subscription overlay a clearer terminal-card hierarchy, a useful add-profile state, and polished provider selection without changing configuration behavior.

**Architecture:** Keep the existing modal state, responsive composition, and hit-testing model. Add small render helpers for section headings, status badges, provider rows, and styled help; then compose the browse and lifecycle screens from those helpers while preserving cursor line positions.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing ANSI-aware geometry helpers, table-driven Go rendering and mouse tests.

**Repository constraint:** Work directly on the existing `main` branch. Do not create a branch, worktree, or subagent.

---

### Task 1: Lock the new detail hierarchy with render tests

**Files:**
- Modify: `internal/tui/subscription_modal_render_test.go`
- Modify: `internal/tui/subscription_modal_mouse_test.go`

**Step 1: Write failing detail and add-preview tests**

Add render coverage equivalent to:

```go
func TestSubscriptionModal_detailRenderUsesStructuredSections(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()

	details := stripAnsi(strings.Join(
		m.subscriptionDetailLines(m.subscriptionDetailPaneWidth(), 18), "\n",
	))
	for _, want := range []string{
		"OpenAI GPT", "● READY", "OpenAI / ChatGPT",
		"CONNECTION", "MODEL ROUTING", "ACTIONS",
	} {
		if !strings.Contains(details, want) {
			t.Errorf("details missing %q:\n%s", want, details)
		}
	}
	if strings.Contains(details, "PROFILE DETAILS") {
		t.Errorf("details kept duplicate generic heading:\n%s", details)
	}
}

func TestSubscriptionModal_addPreviewShowsProvidersAndAuth(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.moveSubscriptionProfile(len(m.subscriptionProfiles()))

	preview := stripAnsi(strings.Join(
		m.subscriptionDetailLines(m.subscriptionDetailPaneWidth(), 18), "\n",
	))
	for _, want := range []string{
		"ADD PROFILE", "AVAILABLE PROVIDERS", "Zhipu / GLM",
		"Xiaomi MiMo", "OpenAI / ChatGPT", "API KEY",
		"CODEX LOGIN", "[ Choose provider ]",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("add preview missing %q:\n%s", want, preview)
		}
	}
}
```

Add a mouse test that locates `[ Choose provider ]`, expects
`subscriptionHitAdd`, clicks it, and verifies the modal enters
`subscriptionAddProvider`.

**Step 2: Run the tests to verify RED**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModal_(detailRenderUsesStructuredSections|addPreviewShowsProvidersAndAuth|clickChooseProviderPreview)' -count=1
```

Expected: FAIL because the structured sections, provider preview, and right-pane
add hit target do not exist.

**Step 3: Commit only after implementation is green**

Keep these tests unstaged until Tasks 2 and 3 complete the behavior.

---

### Task 2: Build the structured browse renderer

**Files:**
- Modify: `internal/tui/subscription_modal.go:1079-1360`
- Test: `internal/tui/subscription_modal_render_test.go`

**Step 1: Add focused rendering helpers**

Implement small helpers that:

- render an uppercase section label followed by a dim horizontal rule;
- render a right-aligned `● READY` or `● NEEDS KEY` badge;
- render provider preview rows with the provider name left-aligned and
  `API KEY` or `CODEX LOGIN` right-aligned; and
- preserve ANSI-aware width with `modalTruncate`, `modalPad`, and
  `lipgloss.Width`.

The section helper should produce:

```go
func subscriptionSectionLine(title string, width int, style, rule lipgloss.Style) string {
	label := style.Render(title)
	gap := width - lipgloss.Width(label) - 1
	if gap <= 0 {
		return modalPad(label, width)
	}
	return label + " " + rule.Render(strings.Repeat("─", gap))
}
```

**Step 2: Replace the duplicate detail heading**

Compose each custom profile with the same line positions as the current
renderer:

1. profile identity plus right-aligned readiness badge;
2. provider name;
3. blank;
4. `CONNECTION` section;
5. authentication;
6. endpoint;
7. blank;
8. `MODEL ROUTING` section;
9. four mapping rows;
10. optional API-key/draft/error rows;
11. `ACTIONS` section;
12. existing action rows.

Standard Claude uses the same identity, provider, connection, and action
hierarchy while keeping unsupported actions visible and subdued.

**Step 3: Replace the empty add preview**

Render the add-profile preview from `claudeconfig.Providers`, including each
provider's authentication label and `[ Choose provider ]`. Keep Enter behavior
unchanged.

**Step 4: Run focused tests to verify GREEN**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModal_(detailRenderUsesStructuredSections|addPreviewShowsProvidersAndAuth|wideRenderShowsInventoryAndDetails|standardProfileShowsConsistentActionRow|cardLinesMatchGeometry)' -count=1
```

Expected: PASS.

---

### Task 3: Polish chooser focus and interaction geometry

**Files:**
- Modify: `internal/tui/subscription_modal.go:830-920`
- Modify: `internal/tui/subscription_modal.go:1360-1450`
- Test: `internal/tui/subscription_modal_render_test.go`
- Test: `internal/tui/subscription_modal_mouse_test.go`

**Step 1: Write a failing provider focus test**

Render `subscriptionAddProvider` with true color and assert the ANSI 236
selection background is active at the cursor, provider name, and auth label.
Also assert an unfocused provider row has no selection wash.

**Step 2: Run the focus test to verify RED**

Run:

```bash
go test ./internal/tui -run '^TestSubscriptionModal_providerChooserUsesFullRowFocus$' -count=1
```

Expected: FAIL because chooser focus is currently only a cursor and colored
name.

**Step 3: Render full provider rows**

Reuse the provider-row helper in lifecycle mode. Apply the neutral selection
background to every segment of the focused row so nested foreground colors do
not punch holes in the wash.

**Step 4: Add the preview mouse target**

When browse mode is on the add row, map `[ Choose provider ]` to
`subscriptionHitAdd`. The existing mouse handler then calls
`startSubscriptionAdd`.

**Step 5: Run modal rendering and mouse tests**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModal' -count=1
```

Expected: PASS.

**Step 6: Commit the implementation**

```bash
git add internal/tui/subscription_modal.go \
  internal/tui/subscription_modal_render_test.go \
  internal/tui/subscription_modal_mouse_test.go
git commit -m "style(tui): refine subscription modal"
```

---

### Task 4: Verify, publish, and install

**Files:**
- Verify: repository-wide
- Build/install: `bin/wisp-deck-tui`, `~/.local/bin/wisp-deck-tui`

**Step 1: Run formatting and focused verification**

```bash
gofmt -w internal/tui/subscription_modal.go \
  internal/tui/subscription_modal_render_test.go \
  internal/tui/subscription_modal_mouse_test.go
git diff --check
go test ./internal/tui -count=1
go vet ./...
```

Expected: PASS.

**Step 2: Run repository checks**

```bash
find lib bin ghostty -name '*.sh' -exec shellcheck {} +
./run-tests.sh -p=1 -timeout=20m -count=1
```

Expected: PASS, or record any unchanged environment-sensitive PTY failures
separately with exact output.

**Step 3: Synchronize and push**

```bash
git pull --rebase
git push
git status --short --branch
```

Expected: `main...origin/main` with no local changes.

**Step 4: Install and verify**

```bash
make install
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
cmp -s bin/wisp-deck-tui "$HOME/.local/bin/wisp-deck-tui"
codesign --verify --deep --strict bin/wisp-deck-tui
codesign --verify --deep --strict "$HOME/.local/bin/wisp-deck-tui"
```

Expected: build, path, byte identity, and both signatures pass. Tell the user
to relaunch running Wisp Deck sessions or ledger panes.

---

### Task 5: Add targeted vertical rhythm

**Files:**
- Modify: `internal/tui/subscription_modal.go:960-1055`
- Modify: `internal/tui/subscription_modal.go:1160-1450`
- Test: `internal/tui/subscription_modal_render_test.go`
- Test: `internal/tui/subscription_modal_mouse_test.go`
- Test: `internal/tui/subscription_modal_test.go`

**Step 1: Write failing spacing regressions**

Add a profile-list test that requires `subscriptionProfileLines` line 1 to be
blank and the first profile to start on line 2. Add a detail test that requires
a blank line after `MODEL ROUTING`, a blank line between the final model/API-key
row and `ACTIONS`, and a blank line before Standard Claude's `ACTIONS`.

Use a small line-index helper in the test:

```go
func subscriptionLineIndex(lines []string, text string) int {
	for i, line := range lines {
		if strings.Contains(stripAnsi(line), text) {
			return i
		}
	}
	return -1
}
```

**Step 2: Run the spacing tests and verify RED**

```bash
go test ./internal/tui -run 'TestSubscriptionModal_(profileListHasHeadingBreathingRoom|detailSectionsHaveVerticalRhythm|standardActionsHaveVerticalRhythm)' -count=1
```

Expected: FAIL because profile rows, mappings, and actions currently touch their
adjacent section boundaries.

**Step 3: Add the profile heading gap**

Render one fixed blank line after the `PROFILES` heading. Reduce the inventory
viewport by one additional row in `ensureSubscriptionProfileVisible`, and
change profile mouse-row translation from `cardY - 2` to `cardY - 3`.

**Step 4: Add detail section gaps**

Insert one blank line after `MODEL ROUTING`, one before custom-profile
`ACTIONS`, and one before Standard Claude's `ACTIONS`.

Update `subscriptionDetailCursorLine`:

- mappings begin at `9 + cursor`;
- the first optional row begins at line `13`;
- action focus advances across both the pre-action blank and heading; and
- Standard Claude's action row moves to line `8`.

**Step 5: Run spacing and interaction tests**

```bash
go test ./internal/tui -run 'TestSubscriptionModal' -count=1
```

Expected: PASS, including profile mouse targets, long-list scrolling, compact
save scrolling, card geometry, and action navigation.

**Step 6: Commit**

```bash
git add internal/tui/subscription_modal.go \
  internal/tui/subscription_modal_render_test.go \
  internal/tui/subscription_modal_mouse_test.go \
  internal/tui/subscription_modal_test.go
git commit -m "style(tui): space subscription sections"
```

**Step 7: Verify, push, and reinstall**

Repeat Task 4 against the new source state.
