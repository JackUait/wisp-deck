# Context-Sensitive Enable/Disable Help Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Teach the user that `x` re-enables a disabled AI tool / subscription by swapping the static `x disable` help label to `x enable` when the focused row is disabled.

**Architecture:** Pure render-layer change in the Go TUI (`wisp-deck-tui`). The `x` key already toggles disabled state on both surfaces; only the two help-footer strings become cursor-aware. No new state, files, or persistence.

**Tech Stack:** Go, Bubbletea/lipgloss, standard `go test`.

## Global Constraints

- TDD is mandatory: write the failing test, watch it fail, then implement (repo IRON RULE).
- Run ONLY the new/changed tests while iterating (user rule); the full suite runs once at session end.
- No behavior change to the `x` toggle itself — labels only.
- Other sessions commit to this same checkout: `git add` only the exact files you touched, never `git add -A`.
- Spec: `docs/superpowers/specs/2026-07-20-enable-disabled-agent-help-design.md`.

---

### Task 1: AI tools panel help label

**Files:**
- Modify: `internal/tui/ai_tools_panel.go` (help footer at the end of `renderAIToolsPanel`, currently ~lines 497-500)
- Test: `internal/tui/ai_tools_disable_test.go`

**Interfaces:**
- Consumes: `m.focusedTool() *models.AITool` (existing; returns the row under `m.aiToolsCursor`, nil when out of range), `panelMenu(t, tools...)` and `stripAnsi(s)` test helpers (both exist in package `tui` tests).
- Produces: nothing new — render output only.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/ai_tools_disable_test.go` (after `TestAIToolsPanel_render_shows_disabled_tag`):

```go
func TestAIToolsPanel_help_shows_enable_when_focused_tool_disabled(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true, Disabled: true})
	out := stripAnsi(m.renderAIToolsPanel())
	if !strings.Contains(out, "x enable") {
		t.Errorf("help must offer 'x enable' on a disabled focused tool, got:\n%s", out)
	}
	if strings.Contains(out, "x disable") {
		t.Errorf("help must not still say 'x disable', got:\n%s", out)
	}
}

func TestAIToolsPanel_help_shows_disable_when_focused_tool_enabled(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	out := stripAnsi(m.renderAIToolsPanel())
	if !strings.Contains(out, "x disable") {
		t.Errorf("help must offer 'x disable' on an enabled focused tool, got:\n%s", out)
	}
}
```

(`strings`, `models`, and `panelMenu` are already imported/defined in this file's package.)

- [ ] **Step 2: Run the new tests to verify the first fails**

Run: `go test ./internal/tui/ -run 'TestAIToolsPanel_help_shows' -v`
Expected: `TestAIToolsPanel_help_shows_enable_when_focused_tool_disabled` FAILS (help still says `x disable`); the second test PASSES.

- [ ] **Step 3: Make the help label cursor-aware**

In `internal/tui/ai_tools_panel.go`, replace the footer construction at the end of `renderAIToolsPanel`:

```go
	lines = append(lines, separator)
	help := helpStyle.Render("⏎ install") + sep + helpStyle.Render("d default") + sep +
		helpStyle.Render("x disable") + sep + helpStyle.Render("r remove") + sep + helpStyle.Render("esc close")
```

with:

```go
	lines = append(lines, separator)
	xLabel := "x disable"
	if tool := m.focusedTool(); tool != nil && tool.Disabled {
		xLabel = "x enable"
	}
	help := helpStyle.Render("⏎ install") + sep + helpStyle.Render("d default") + sep +
		helpStyle.Render(xLabel) + sep + helpStyle.Render("r remove") + sep + helpStyle.Render("esc close")
```

- [ ] **Step 4: Run the new tests to verify both pass**

Run: `go test ./internal/tui/ -run 'TestAIToolsPanel_help_shows' -v`
Expected: both PASS.

- [ ] **Step 5: Run the panel's existing tests (regression on this surface)**

Run: `go test ./internal/tui/ -run 'TestAIToolsPanel' -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/ai_tools_panel.go internal/tui/ai_tools_disable_test.go
git commit -m "feat(tui): AI tools help shows 'x enable' on a disabled focused tool"
```

---

### Task 2: Subscription modal help label

**Files:**
- Modify: `internal/tui/subscription_modal.go` (help string in `renderSubscriptionModalCard`, currently line ~2417)
- Test: `internal/tui/subscription_modal_disable_test.go`

**Interfaces:**
- Consumes: `m.subscriptionProfiles() []subscriptionProfile` and `m.subscriptionModal.profileCursor` (the label guard mirrors `toggleSubscriptionProfileDisabled`'s own bounds check — do NOT use `subscriptionModalProfile()`, which clamps out-of-range cursors to the last profile and would mislabel login/add rows), `newSubscriptionModalMenu(t)`, `subscriptionModalKey(t, m, msg)`, `m.selectSubscriptionProfile(i)`, `stripAnsi(s)` (all exist). `newSubscriptionModalMenu` sets width 100, so the modal renders the wide (non-compact) browse help line this task edits.
- Produces: nothing new — render output only.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/subscription_modal_disable_test.go`:

```go
func TestSubscriptionModal_help_shows_enable_when_focused_profile_disabled(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	out := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(out, "x enable") {
		t.Errorf("help must offer 'x enable' on a disabled focused profile, got:\n%s", out)
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	out = stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(out, "x disable") {
		t.Errorf("help must return to 'x disable' after re-enabling, got:\n%s", out)
	}
}

func TestSubscriptionModal_help_keeps_disable_label_on_standard_row(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.selectSubscriptionProfile(0) // Standard Claude — x is a no-op here

	out := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(out, "x disable") {
		t.Errorf("help on the Standard row must keep 'x disable', got:\n%s", out)
	}
	if strings.Contains(out, "x enable") {
		t.Errorf("help on the Standard row must not say 'x enable', got:\n%s", out)
	}
}

func TestSubscriptionModal_help_keeps_disable_label_on_login_rows(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(len(m.subscriptionProfiles()) - 1) // last managed profile
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.selectSubscriptionProfile(m.subscriptionLoginRowStart()) // x is a no-op here

	out := stripAnsi(m.renderSubscriptionModalCard())
	if strings.Contains(out, "x enable") {
		t.Errorf("help on a login row must not say 'x enable' (last profile is disabled but not focused), got:\n%s", out)
	}
}
```

(`strings`, `tea`, and the helpers are already imported/defined for this file's package.)

- [ ] **Step 2: Run the new tests to verify the first fails**

Run: `go test ./internal/tui/ -run 'TestSubscriptionModal_help' -v`
Expected: `TestSubscriptionModal_help_shows_enable_when_focused_profile_disabled` FAILS on the `x enable` assertion; the standard-row and login-row tests PASS.

- [ ] **Step 3: Make the help string cursor-aware**

In `internal/tui/subscription_modal.go`, `renderSubscriptionModalCard`, replace:

```go
	help := "↑↓ profile · → details · x disable · Tab pane · Enter action · Esc close"
```

with:

```go
	xLabel := "x disable"
	if profiles, cursor := m.subscriptionProfiles(), m.subscriptionModal.profileCursor; cursor > 0 &&
		cursor < len(profiles) && profiles[cursor].Disabled {
		xLabel = "x enable"
	}
	help := "↑↓ profile · → details · " + xLabel + " · Tab pane · Enter action · Esc close"
```

(The bounds check mirrors `toggleSubscriptionProfileDisabled`: cursor 0 is Standard Claude and cursors past the profiles are add/login rows — `x` is a no-op on all of them, so the label must stay `x disable`. `subscriptionModalProfile()` is unsuitable here: it clamps an out-of-range cursor to the last profile.)

The later `if/else if` chain that overwrites `help` for the add-provider, action, compact, and details-pane modes stays exactly as is — those modes never mention `x`.

- [ ] **Step 4: Run the new tests to verify both pass**

Run: `go test ./internal/tui/ -run 'TestSubscriptionModal_help' -v`
Expected: both PASS.

- [ ] **Step 5: Run the modal's existing tests (regression on this surface)**

Run: `go test ./internal/tui/ -run 'TestSubscriptionModal' -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_disable_test.go
git commit -m "feat(tui): subscription help shows 'x enable' on a disabled focused profile"
```

---

### Task 3: Final verification and push

**Files:** none new — verification only.

- [ ] **Step 1: Full test suite**

Run: `./run-tests.sh`
Expected: PASS. (Known caveat: timing-sensitive tests can flake under parallel load — rerun a flaky one in isolation before treating it as a real failure.)

- [ ] **Step 2: Push**

```bash
git pull --rebase
git push
git status
```

Expected: `git status` reports "up to date with 'origin/main'". (No shell scripts were modified, so shellcheck has nothing in scope.)
