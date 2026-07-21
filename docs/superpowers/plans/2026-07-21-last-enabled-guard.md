# Last-Enabled Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refuse to disable the last enabled AI tool and the last enabled managed subscription, with an inline error message.

**Architecture:** Guards in the two TUI toggle handlers (the only writers of the disabled sidecars); persistence helpers unchanged. Enabling is never blocked.

**Tech Stack:** Go, Bubbletea, `go test`.

## Global Constraints

- TDD is mandatory: failing test first, then the minimal fix (repo IRON RULE).
- Run only the new/changed tests while iterating; suite-wide check at the end.
- Shared checkout with concurrent sessions: `git add` only the exact files touched.
- Spec: `docs/superpowers/specs/2026-07-21-last-enabled-guard-design.md`.

---

### Task 1: AI tools panel guard

**Files:**
- Modify: `internal/tui/ai_tools_panel.go` (`toggleFocusedToolDisabled`)
- Test: `internal/tui/ai_tools_disable_test.go`

**Interfaces:**
- Consumes: `m.aiToolRows []models.AITool`, `m.focusedTool()`, `models.ToggleDisabledTool`, `models.LoadDisabledTools`, test helpers `panelMenu`/`writeTempFile`-style setup via `os.WriteFile`.
- Produces: refusal sets `m.aiToolsErr` to `At least one AI tool must stay enabled` (rendered by the panel's existing error line).

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/ai_tools_disable_test.go` (`os` needs importing):

```go
func TestAIToolsPanel_x_refuses_to_disable_the_last_enabled_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true, Disabled: true},
	)
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")
	if err := os.WriteFile(m.disabledToolsFile, []byte("codex\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m.aiToolsCursor = 0 // claude: the last enabled installed tool
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.aiToolRows[0].Disabled {
		t.Error("the last enabled tool must not become disabled")
	}
	if models.LoadDisabledTools(m.disabledToolsFile)["claude"] {
		t.Error("the sidecar file must stay untouched")
	}
	if m.aiToolsErr == nil || !strings.Contains(m.aiToolsErr.Error(), "At least one AI tool must stay enabled") {
		t.Errorf("aiToolsErr = %v, want the last-enabled message", m.aiToolsErr)
	}
}

func TestAIToolsPanel_x_still_disables_with_an_enabled_peer(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex", Installed: true},
	)
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")

	m.aiToolsCursor = 1
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if !m.aiToolRows[1].Disabled {
		t.Error("disabling with another enabled tool present must work")
	}
	if m.aiToolsErr != nil {
		t.Errorf("unexpected error: %v", m.aiToolsErr)
	}
}

func TestAIToolsPanel_x_always_reenables(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true, Disabled: true})
	m.disabledToolsFile = filepath.Join(t.TempDir(), "disabled-tools")
	if err := os.WriteFile(m.disabledToolsFile, []byte("codex\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.aiToolRows[0].Disabled {
		t.Error("re-enabling must never be blocked")
	}
}
```

- [ ] **Step 2: Run them; the first must fail**

Run: `go test ./internal/tui/ -run 'TestAIToolsPanel_x_refuses|TestAIToolsPanel_x_still|TestAIToolsPanel_x_always' -v`
Expected: `..._x_refuses_...` FAILS (tool gets disabled today); the other two PASS.

- [ ] **Step 3: Add the guard**

In `toggleFocusedToolDisabled` (`internal/tui/ai_tools_panel.go`), after the nil/empty-file early return, before `models.ToggleDisabledTool`:

```go
	if !tool.Disabled && tool.Installed {
		enabled := 0
		for _, r := range m.aiToolRows {
			if r.Installed && !r.Disabled {
				enabled++
			}
		}
		if enabled <= 1 {
			m.aiToolsErr = errors.New("At least one AI tool must stay enabled")
			return
		}
	}
```

(`errors` is already imported.)

- [ ] **Step 4: Re-run the three tests — all PASS; then `go test ./internal/tui/ -run 'TestAIToolsPanel'` — all PASS**

- [ ] **Step 5: Commit**

```bash
git add internal/tui/ai_tools_panel.go internal/tui/ai_tools_disable_test.go
git commit -m "feat(tui): refuse to disable the last enabled AI tool"
```

---

### Task 2: Subscription modal guard

**Files:**
- Modify: `internal/tui/subscription_modal.go` (`toggleSubscriptionProfileDisabled`; add `"errors"` to imports)
- Test: `internal/tui/subscription_modal_disable_test.go`

**Interfaces:**
- Consumes: `m.subscriptionProfiles()` (index 0 is Standard Claude; managed profiles follow), `m.subscriptionModal.err`, helpers `newSubscriptionModalMenu` (3 managed profiles), `subscriptionModalKey`, `selectSubscriptionProfile`.
- Produces: refusal sets `m.subscriptionModal.err` to `At least one subscription must stay enabled`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/subscription_modal_disable_test.go`:

```go
func TestSubscriptionModal_x_refuses_to_disable_the_last_enabled_managed_profile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	for _, i := range []int{1, 2} {
		m.selectSubscriptionProfile(i)
		m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	}

	m.selectSubscriptionProfile(3) // last enabled managed profile
	file := m.subscriptionModalProfile().File
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.subscriptionProfiles()[3].Disabled {
		t.Error("the last enabled managed subscription must not become disabled")
	}
	if claudeconfig.LoadDisabled(claudeconfig.DisabledFile(m.claudeConfigsList))[file] {
		t.Error("the sidecar file must stay untouched")
	}
	if m.subscriptionModal.err == nil ||
		!strings.Contains(m.subscriptionModal.err.Error(), "At least one subscription must stay enabled") {
		t.Errorf("err = %v, want the last-enabled message", m.subscriptionModal.err)
	}
}

func TestSubscriptionModal_x_always_reenables_managed_profiles(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	for _, i := range []int{1, 2} {
		m.selectSubscriptionProfile(i)
		m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	}

	m.selectSubscriptionProfile(1)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.subscriptionProfiles()[1].Disabled {
		t.Error("re-enabling must never be blocked")
	}
}
```

- [ ] **Step 2: Run them; the first must fail**

Run: `go test ./internal/tui/ -run 'TestSubscriptionModal_x_refuses|TestSubscriptionModal_x_always' -v`
Expected: `..._x_refuses_...` FAILS; the re-enable test PASSES.

- [ ] **Step 3: Add the guard**

In `toggleSubscriptionProfileDisabled` (`internal/tui/subscription_modal.go`), after the cursor/bounds early return, before `claudeconfig.ToggleDisabled`; add `"errors"` to the file's imports:

```go
	if !profiles[cursor].Disabled {
		enabled := 0
		for _, p := range profiles[1:] { // managed profiles only; Standard can't be disabled
			if !p.Disabled {
				enabled++
			}
		}
		if enabled <= 1 {
			m.subscriptionModal.err = errors.New("At least one subscription must stay enabled")
			return
		}
	}
```

- [ ] **Step 4: Re-run the two tests — all PASS; then `go test ./internal/tui/ -run 'TestSubscriptionModal'` — all PASS**

- [ ] **Step 5: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_disable_test.go
git commit -m "feat(tui): refuse to disable the last enabled managed subscription"
```

---

### Task 3: Verify, push, refresh local binary

- [ ] **Step 1:** `go test ./internal/tui/` — PASS (full suite already covered these surfaces yesterday; rerun `./run-tests.sh` only if other packages were touched — they weren't).
- [ ] **Step 2:** `git push`; `git status` shows up to date with origin.
- [ ] **Step 3:** Rebuild `~/.local/bin/wisp-deck-tui` from `git archive HEAD` in the scratchpad, `go build -ldflags "-X main.Version=$(cat VERSION)"`, `cp` + `codesign --sign - --force` + one `--version` warm-up exec.
