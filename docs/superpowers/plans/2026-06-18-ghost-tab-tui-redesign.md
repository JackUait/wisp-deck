# Ghost Tab Tabbed TUI Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Ghost Tab's single cramped main-menu box with a tabbed UI (Projects · Settings · Stats), a contextual action bar, and refined retro-terminal styling — without changing the bash-facing JSON contract.

**Architecture:** Introduce an `activeTab` enum on `MainMenuModel` as the single source of truth for which screen renders. Tab routing replaces the `settingsMode` boolean and the pushed Stats screen. Rendering is split out of the 3294-line `mainmenu.go` into focused `render_*.go` files. The model, persistence, and result-emitting logic are reused verbatim; only framing changes.

**Tech Stack:** Go, Bubbletea (`tea.Model`), Lipgloss (styling), existing test helpers in `internal/tui/*_test.go`.

## Global Constraints

- Unicode-block / geometric glyphs only — **no Nerd Font glyphs** (must render without a patched font).
- `MainMenuResult` JSON fields and `actionNames` action strings stay byte-for-byte identical: `select-project`, `add-project`, `delete-project`, `open-once`, `plain-terminal`, `quit`.
- Per-AI palette unchanged (`internal/tui/theme.go`); all colors come from `m.theme`.
- Box geometry constants unchanged: `menuInnerWidth`, `menuPadding`, `menuContentWidth` (=54).
- Run `go test ./...` after every task; run `./run-tests.sh` + `shellcheck` (if any `.sh` touched) before final push.
- Every code change is test-first: write the test, watch it fail, implement, watch it pass, commit.

---

### Task 1: Tab state model

Introduces the `activeTab` enum and its accessors/cyclers as pure model state. No rendering or key-routing change yet — keeps the build green and isolates the state contract later tasks consume.

**Files:**
- Modify: `internal/tui/mainmenu.go` (add type + field + methods near the `MainMenuModel` struct, ~line 145)
- Test: `internal/tui/mainmenu_tabs_test.go` (create)

**Interfaces:**
- Produces:
  - `type MenuTab int` with `const (TabProjects MenuTab = iota; TabSettings; TabStats)`
  - `func (m *MainMenuModel) ActiveTab() MenuTab`
  - `func (m *MainMenuModel) SetActiveTab(t MenuTab)`
  - `func (m *MainMenuModel) CycleTab(direction string)` — `"next"` / `"prev"`, wraps across the 3 tabs
  - field `activeTab MenuTab` on `MainMenuModel` (zero value `TabProjects` = correct default)

- [ ] **Step 1: Write the failing test**

```go
package tui

import "testing"

func TestActiveTab_defaultsToProjects(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	if m.ActiveTab() != TabProjects {
		t.Errorf("default tab = %v, want TabProjects", m.ActiveTab())
	}
}

func TestSetActiveTab(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	if m.ActiveTab() != TabStats {
		t.Errorf("after SetActiveTab(TabStats) = %v, want TabStats", m.ActiveTab())
	}
}

func TestCycleTab_wraps(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.CycleTab("next") // Projects -> Settings
	if m.ActiveTab() != TabSettings {
		t.Fatalf("next from Projects = %v, want Settings", m.ActiveTab())
	}
	m.CycleTab("next") // Settings -> Stats
	m.CycleTab("next") // Stats -> Projects (wrap)
	if m.ActiveTab() != TabProjects {
		t.Fatalf("3x next = %v, want Projects (wrap)", m.ActiveTab())
	}
	m.CycleTab("prev") // Projects -> Stats (wrap back)
	if m.ActiveTab() != TabStats {
		t.Fatalf("prev from Projects = %v, want Stats (wrap)", m.ActiveTab())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestActiveTab|TestSetActiveTab|TestCycleTab' -v`
Expected: FAIL — `undefined: TabProjects`, `m.ActiveTab undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/mainmenu.go`, add after the `MainMenuResult` type (~line 91):

```go
// MenuTab identifies which top-level tab is active.
type MenuTab int

const (
	TabProjects MenuTab = iota
	TabSettings
	TabStats
)

const menuTabCount = 3
```

Add to the `MainMenuModel` struct (anywhere in the field block, e.g. near `settingsMode`):

```go
	// activeTab is the source of truth for which top-level tab renders.
	activeTab MenuTab
```

Add these methods (e.g. just after `InSettingsMode`):

```go
// ActiveTab returns the currently selected top-level tab.
func (m *MainMenuModel) ActiveTab() MenuTab { return m.activeTab }

// SetActiveTab switches to the given tab.
func (m *MainMenuModel) SetActiveTab(t MenuTab) { m.activeTab = t }

// CycleTab moves to the next/prev tab, wrapping across all tabs.
func (m *MainMenuModel) CycleTab(direction string) {
	if direction == "prev" {
		m.activeTab = (m.activeTab - 1 + menuTabCount) % menuTabCount
	} else {
		m.activeTab = (m.activeTab + 1) % menuTabCount
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestActiveTab|TestSetActiveTab|TestCycleTab' -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/mainmenu.go internal/tui/mainmenu_tabs_test.go
git commit -m "feat(tui): add activeTab state model"
```

---

### Task 2: Route tab navigation keys

Wire `Tab`/`Shift+Tab` and `S`/`T`/`P` into the Projects-tab key handler so the tab can change. Settings/Stats stop being a sub-mode / pushed screen at the *routing* level. Rendering still goes through the old path (Task 5 swaps it) — but `View()` is updated to dispatch on `activeTab` here so the new state is observable end-to-end.

**Files:**
- Modify: `internal/tui/mainmenu.go` — `handleRune` (~1516), `Update` `tea.KeyMsg` block (~1419), `View` (~3183)
- Test: `internal/tui/mainmenu_tabs_test.go`

**Interfaces:**
- Consumes: `MenuTab`, `ActiveTab`, `SetActiveTab`, `CycleTab` (Task 1)
- Produces: behavior — `Tab` cycles forward, `Shift+Tab` back; `s`/`S` → `TabSettings`; `t`/`T` → `TabStats`; on Settings/Stats tabs, `Tab`/`Shift+Tab` keep cycling. The `'t'` handler no longer emits `PushScreenMsg`. The `'s'` handler no longer sets `settingsMode`.

- [ ] **Step 1: Write the failing test**

```go
func TestHandleRune_sSwitchesToSettingsTab(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.handleRune('s')
	if m.ActiveTab() != TabSettings {
		t.Errorf("after 's' tab = %v, want Settings", m.ActiveTab())
	}
}

func TestHandleRune_tSwitchesToStatsTab(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	_, cmd := m.handleRune('t')
	if m.ActiveTab() != TabStats {
		t.Errorf("after 't' tab = %v, want Stats", m.ActiveTab())
	}
	if cmd != nil {
		t.Errorf("'t' should not emit a navigation cmd (no pushed screen), got %v", cmd)
	}
}

func TestUpdate_tabKeyCycles(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.ActiveTab() != TabSettings {
		t.Errorf("Tab from Projects = %v, want Settings", m.ActiveTab())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.ActiveTab() != TabProjects {
		t.Errorf("Shift+Tab back = %v, want Projects", m.ActiveTab())
	}
}
```

(`tea` is already imported by the package's other test files; if `mainmenu_tabs_test.go` lacks it, add `tea "github.com/charmbracelet/bubbletea"`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestHandleRune_s|TestHandleRune_t|TestUpdate_tabKeyCycles' -v`
Expected: FAIL — `'s'` still sets `settingsMode`, `'t'` returns a Push cmd, Tab key unhandled.

- [ ] **Step 3: Write minimal implementation**

In `handleRune` (~1543), replace the `'s'` and `'t'` cases:

```go
	case 's', 'S':
		m.SetActiveTab(TabSettings)
		m.settingsSelected = 0
		return m, nil
	case 't', 'T':
		m.SetActiveTab(TabStats)
		return m, nil
```

In `Update`, in the `tea.KeyMsg` switch on `msg.Type` (after the `tea.KeyShiftDown` case, ~1474), add:

```go
		case tea.KeyTab:
			m.CycleTab("next")
			return m, nil
		case tea.KeyShiftTab:
			m.CycleTab("prev")
			return m, nil
```

In `Update`, the settings-mode interception (~1424) currently reads `if m.settingsMode`. Change it to route on the tab:

```go
		// Settings tab intercepts all key handling
		if m.activeTab == TabSettings {
			return m.updateSettings(msg)
		}
		// Stats tab: Tab/Shift+Tab/S/T navigate; Esc returns to Projects.
		if m.activeTab == TabStats {
			return m.updateStatsTab(msg)
		}
```

Add a minimal `updateStatsTab` (full body refined in Task 6) near `updateSettings`:

```go
// updateStatsTab handles key events while the Stats tab is active.
func (m *MainMenuModel) updateStatsTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyTab:
		m.CycleTab("next")
		return m, nil
	case tea.KeyShiftTab:
		m.CycleTab("prev")
		return m, nil
	case tea.KeyEsc:
		m.SetActiveTab(TabProjects)
		return m, nil
	case tea.KeyCtrlC:
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch TranslateRune(msg.Runes[0]) {
			case 's', 'S':
				m.SetActiveTab(TabSettings)
				return m, nil
			case 't', 'T':
				m.SetActiveTab(TabProjects) // 't' from Stats toggles back
				return m, nil
			}
		}
	}
	return m, nil
}
```

In `updateSettings` (~1574), the `tea.KeyEsc` case currently does `m.settingsMode = false`. Change to:

```go
	case tea.KeyEsc:
		m.SetActiveTab(TabProjects)
		return m, nil
```

Also add `Tab`/`Shift+Tab` to `updateSettings` so they cycle from Settings (insert as new cases in its `switch msg.Type`):

```go
	case tea.KeyTab:
		m.CycleTab("next")
		return m, nil
	case tea.KeyShiftTab:
		m.CycleTab("prev")
		return m, nil
```

In `View` (~3189), change the dispatch from `settingsMode` to `activeTab` (rendering bodies still old; Task 5/6 replace them):

```go
	var menuBox string
	switch {
	case m.inputMode != "":
		menuBox = m.renderInputBox()
	case m.activeTab == TabSettings:
		if m.modelMapOpen {
			menuBox = m.renderSettingsBox() + "\n" + m.renderModelMapPanel()
		} else {
			menuBox = m.renderSettingsBox()
		}
	case m.activeTab == TabStats:
		menuBox = m.renderStatsBox()
	default:
		menuBox = m.renderMenuBox()
	}
```

Add a temporary `renderStatsBox` that defers to the existing stats renderer so the build stays green (replaced properly in Task 6):

```go
// renderStatsBox renders the Stats tab body. Replaced with the in-box
// version in Task 6; for now it reuses the standalone stats view.
func (m *MainMenuModel) renderStatsBox() string {
	s := NewStatsModel()
	s.SetSize(m.width, m.height)
	return s.View()
}
```

Update `WantsEsc` (~964) so Settings/Stats tabs claim Esc (return to Projects instead of quitting):

```go
func (m *MainMenuModel) WantsEsc() bool {
	return m.activeTab != TabProjects || m.inputMode != "" || m.deleteMode || m.settingsInputMode
}
```

Update `InSettingsMode` (~826) and `EnterSettings`/`ExitSettings`/`SetSettingsMode` to delegate to the tab so existing callers/tests keep working:

```go
func (m *MainMenuModel) InSettingsMode() bool { return m.activeTab == TabSettings }
func (m *MainMenuModel) EnterSettings()       { m.activeTab = TabSettings; m.settingsSelected = 0 }
func (m *MainMenuModel) ExitSettings()        { m.activeTab = TabProjects }
func (m *MainMenuModel) SetSettingsMode(v bool) {
	if v {
		m.activeTab = TabSettings
	} else {
		m.activeTab = TabProjects
	}
}
```

Remove the now-unused `settingsMode` field and any remaining references (grep `settingsMode`); the only writers were the methods above and `handleRune`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestHandleRune_s|TestHandleRune_t|TestUpdate_tabKeyCycles' -v`
Expected: PASS. Then `go test ./internal/tui/ -v` — fix any test still referencing `settingsMode` directly by switching it to `SetActiveTab`/`ActiveTab`.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/mainmenu.go internal/tui/mainmenu_tabs_test.go
git commit -m "feat(tui): route tab navigation keys, drop settingsMode flag"
```

---

### Task 3: Contract regression guard

Lock the bash-facing JSON contract with explicit tests *before* the render rewrite, so any later regression is caught immediately. This is the safety net the spec calls out.

**Files:**
- Test: `internal/tui/mainmenu_contract_test.go` (create)

**Interfaces:**
- Consumes: `handleRune`, `selectCurrent`, `Result()`, `MainMenuResult` (existing)

- [ ] **Step 1: Write the failing test**

```go
package tui

import "testing"

func TestContract_actionStringsUnchanged(t *testing.T) {
	want := []string{"add-project", "delete-project", "open-once", "plain-terminal"}
	if len(actionNames) != len(want) {
		t.Fatalf("actionNames len = %d, want %d", len(actionNames), len(want))
	}
	for i, a := range want {
		if actionNames[i] != a {
			t.Errorf("actionNames[%d] = %q, want %q", i, actionNames[i], a)
		}
	}
}

func TestContract_selectProjectEmitsAction(t *testing.T) {
	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.selectCurrent()
	r := m.Result()
	if r == nil || r.Action != "select-project" || r.Name != "blok" {
		t.Fatalf("select result = %+v, want action=select-project name=blok", r)
	}
}

func TestContract_plainTerminalAction(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.handleRune('p')
	r := m.Result()
	if r == nil || r.Action != "plain-terminal" {
		t.Fatalf("plain result = %+v, want action=plain-terminal", r)
	}
}
```

Add `"github.com/jackuait/ghost-tab/internal/models"` to the test imports.

- [ ] **Step 2: Run test to verify it fails (or passes — confirm green baseline)**

Run: `go test ./internal/tui/ -run TestContract -v`
Expected: PASS (this guards existing behavior). If any FAIL, a prior task broke the contract — fix before continuing.

- [ ] **Step 3: No implementation needed**

These tests pin existing behavior; no code change.

- [ ] **Step 4: Re-run to confirm**

Run: `go test ./internal/tui/ -run TestContract -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/mainmenu_contract_test.go
git commit -m "test(tui): pin bash JSON action contract"
```

---

### Task 4: Extract chrome rendering (header + tab bar + footer)

Move shared box chrome into a focused file and add the new tab bar row. This is the first visible piece of the redesign: the `Projects · Settings · Stats` bar under the title.

**Files:**
- Create: `internal/tui/render_chrome.go`
- Modify: `internal/tui/mainmenu.go` — `renderMenuBox` calls the new helpers
- Test: `internal/tui/render_chrome_test.go` (create)

**Interfaces:**
- Produces:
  - `func (m *MainMenuModel) renderTabBar(leftBorder, rightBorder string) string` — one box row showing all three tab labels; the active one styled with the block accent.
  - `func (m *MainMenuModel) boxBorders() (top, separator, bottom, leftBorder, rightBorder string)` — the existing border strings, extracted so every render_*.go reuses them.
- Consumes: `m.theme`, `menuInnerWidth`, `menuPadding`, `menuContentWidth`, `ActiveTab`

- [ ] **Step 1: Write the failing test**

```go
package tui

import (
	"strings"
	"testing"
)

func TestRenderTabBar_showsAllTabs(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	for _, label := range []string{"Projects", "Settings", "Stats"} {
		if !strings.Contains(bar, label) {
			t.Errorf("tab bar missing %q: %q", label, bar)
		}
	}
}

func TestRenderTabBar_activeTabAccented(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	// The active tab is wrapped with the block accent glyph U+258C.
	if !strings.Contains(bar, "▌") {
		t.Errorf("active tab bar missing block accent: %q", bar)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRenderTabBar -v`
Expected: FAIL — `m.boxBorders undefined`, `m.renderTabBar undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tui/render_chrome.go`:

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// boxBorders returns the rounded-box border strings shared by every tab body.
func (m *MainMenuModel) boxBorders() (top, separator, bottom, leftBorder, rightBorder string) {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	hLine := strings.Repeat("─", menuInnerWidth)
	top = dimStyle.Render("╭" + hLine + "╮")
	separator = dimStyle.Render("├" + hLine + "┤")
	bottom = dimStyle.Render("╰" + hLine + "╯")
	leftBorder = dimStyle.Render("│")
	rightBorder = strings.Repeat(" ", menuPadding) + dimStyle.Render("│")
	return
}

// menuTabLabels is the ordered list of top-level tab labels.
var menuTabLabels = []string{"Projects", "Settings", "Stats"}

// renderTabBar renders the Projects · Settings · Stats row. The active tab is
// wrapped in block accents and styled bold; inactive tabs are dimmed.
func (m *MainMenuModel) renderTabBar(leftBorder, rightBorder string) string {
	activeStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)

	var parts []string
	for i, label := range menuTabLabels {
		if MenuTab(i) == m.activeTab {
			parts = append(parts, activeStyle.Render("▌"+label+"▐"))
		} else {
			parts = append(parts, inactiveStyle.Render(" "+label+" "))
		}
	}
	content := strings.Join(parts, "  ")
	gap := menuContentWidth - lipgloss.Width(content) - 1
	if gap < 0 {
		gap = 0
	}
	return leftBorder + " " + content + strings.Repeat(" ", gap) + rightBorder
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestRenderTabBar -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/render_chrome.go internal/tui/render_chrome_test.go
git commit -m "feat(tui): add tab bar chrome rendering"
```

---

### Task 5: Projects tab body + contextual action bar

Rebuild the Projects view: title row, tab bar, project list (reusing the existing row-rendering logic), the `+ Add project` row, and a contextual action bar that reflects the selected row. The old action-list rows (`A/D/O/P/S/T`) are removed.

**Files:**
- Create: `internal/tui/render_projects.go` (new `renderMenuBox` + action bar helper moved here)
- Modify: `internal/tui/mainmenu.go` — remove the old `renderMenuBox`; remove `actionLabels` action-row rendering; `TotalItems`/`ResolveItem` gain an `add-project` pseudo-row (see below)
- Test: `internal/tui/render_projects_test.go` (create); update `internal/tui/mainmenu_render_test.go`

**Interfaces:**
- Consumes: `boxBorders`, `renderTabBar` (Task 4); `m.projects`, `m.selectedItem`, `ResolveItem`, worktree helpers
- Produces:
  - `func (m *MainMenuModel) renderMenuBox() string` — full Projects-tab box
  - `func (m *MainMenuModel) renderActionBar(leftBorder, rightBorder string) string` — contextual action line for the selected row
  - `actionBarFor(itemType string) string` — pure helper returning the action label text for a row type (`"project"` → `"▸ Open    ⟕ Worktrees    ✕ Delete"`, `"worktree"` → `"▸ Open    ✕ Delete"`, `"add-project"` → `"+ Add project"`, else `""`)

**Note on the `+ Add project` row:** it becomes the last selectable item in the Projects list. Extend `TotalItems` to `len(projects) + expandedWorktreeCount() + 1` (the add row; the old 4 action rows are gone) and extend `ResolveItem` to return `("add-project", -1, -1)` for the final index. `selectCurrent` maps `add-project` → `enterInputMode("add-project")`. Reorder/jump/delete logic is unaffected because they already operate on `"project"`/`"worktree"` types only.

- [ ] **Step 1: Write the failing test**

```go
package tui

import (
	"strings"
	"testing"

	"github.com/jackuait/ghost-tab/internal/models"
)

func TestActionBarFor_byRowType(t *testing.T) {
	cases := map[string][]string{
		"project":     {"Open", "Worktrees", "Delete"},
		"worktree":    {"Open", "Delete"},
		"add-project": {"Add project"},
	}
	for rowType, wantWords := range cases {
		got := actionBarFor(rowType)
		for _, w := range wantWords {
			if !strings.Contains(got, w) {
				t.Errorf("actionBarFor(%q)=%q missing %q", rowType, got, w)
			}
		}
	}
	if actionBarFor("action") != "" {
		t.Errorf("actionBarFor(action) should be empty")
	}
}

func TestRenderMenuBox_hasTabBarAndAddRow(t *testing.T) {
	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	out := m.renderMenuBox()
	if !strings.Contains(out, "Projects") {
		t.Errorf("menu box missing tab bar")
	}
	if !strings.Contains(out, "Add project") {
		t.Errorf("menu box missing + Add project row")
	}
	// The old action stack labels must be gone from the projects body.
	if strings.Contains(out, "Plain terminal") || strings.Contains(out, "Open once") {
		t.Errorf("old action rows should not render in projects body: %q", out)
	}
}

func TestAddProjectRow_isSelectable(t *testing.T) {
	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	last := m.TotalItems() - 1
	itemType, _, _ := m.ResolveItem(last)
	if itemType != "add-project" {
		t.Errorf("last item type = %q, want add-project", itemType)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestActionBarFor|TestRenderMenuBox_hasTabBar|TestAddProjectRow' -v`
Expected: FAIL — `actionBarFor` undefined; `ResolveItem` returns `"action"` for the last index.

- [ ] **Step 3: Write minimal implementation**

In `mainmenu.go`, update `TotalItems`:

```go
func (m *MainMenuModel) TotalItems() int {
	return len(m.projects) + m.expandedWorktreeCount() + 1 // +1 for the add-project row
}
```

Update `ResolveItem`'s tail (after the project/worktree loop, replacing the `"action"` fallthrough):

```go
	// Final selectable item is the add-project row.
	return "add-project", -1, -1
```

Update `resolveToFlatIndex` to handle `"add-project"`:

```go
	case "add-project":
		return len(m.projects) + m.expandedWorktreeCount()
```

Update `selectCurrent`'s switch — replace the `case "action":` block with:

```go
	case "add-project":
		// handled by Enter routing; selectCurrent only sets results.
```

Update the `tea.KeyEnter` handler in `Update` (~1481) — replace the action dispatch with:

```go
		case tea.KeyEnter:
			itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
			switch itemType {
			case "add-project":
				return m.enterInputMode("add-project")
			case "add-worktree":
				if cmd := m.selectCurrent(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			_ = projectIdx
			if cmd := m.selectCurrent(); cmd != nil {
				return m, cmd
			}
			return m, tea.Quit
```

Delete the old `renderMenuBox` from `mainmenu.go` and the now-unused `actionNames`-as-rows rendering. Keep the `actionNames` and `actionLabels` *variables* (still referenced by the contract and by `enterInputMode`/result strings).

Create `internal/tui/render_projects.go`:

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// actionBarFor returns the contextual action label text for a selected row type.
func actionBarFor(itemType string) string {
	switch itemType {
	case "project":
		return "▸ Open    ⟕ Worktrees    ✕ Delete"
	case "worktree":
		return "▸ Open    ✕ Delete"
	case "add-project":
		return "+ Add project"
	default:
		return ""
	}
}

// renderActionBar renders the contextual action line for the current selection.
func (m *MainMenuModel) renderActionBar(leftBorder, rightBorder string) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Accent)
	itemType, _, _ := m.ResolveItem(m.selectedItem)
	text := actionBarFor(itemType)
	rendered := style.Render(text)
	gap := menuContentWidth - lipgloss.Width(rendered) - 2
	if gap < 0 {
		gap = 0
	}
	return leftBorder + "  " + rendered + strings.Repeat(" ", gap) + rightBorder
}
```

Add the new `renderMenuBox` to `render_projects.go`. Reuse the exact project/worktree row rendering from the old function (title row, update-notification row, the per-project 2-line rows with selection accent `▎`, worktree expansion, badges, stale styling). Assemble as:

```go
func (m *MainMenuModel) renderMenuBox() string {
	top, separator, bottom, leftBorder, rightBorder := m.boxBorders()
	var lines []string
	lines = append(lines, top)
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder)) // extracted from old renderMenuBox title block
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)
	if m.updateVersion != "" {
		lines = append(lines, m.renderUpdateRow(leftBorder, rightBorder)) // extracted update-notification row
	}
	lines = append(lines, m.renderProjectRows(leftBorder, rightBorder)...) // extracted project/worktree rows + blank line + add-project row
	lines = append(lines, separator)
	lines = append(lines, m.renderActionBar(leftBorder, rightBorder))
	lines = append(lines, bottom)
	lines = append(lines, m.renderHelpRow()) // extracted footer hints incl. "O once · P plain"
	return strings.Join(lines, "\n")
}
```

Extract `renderTitleRow`, `renderUpdateRow`, `renderProjectRows`, and `renderHelpRow` into `render_projects.go` by lifting the corresponding blocks verbatim from the old `renderMenuBox` body (the title block ~2509-2528, the update block ~2534-2545, the project/worktree loop, and the help row). In `renderProjectRows`, after the project/worktree loop, append one blank content row then the `+ Add project` row:

```go
	addStyle := lipgloss.NewStyle().Foreground(m.theme.Text)
	addSel := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	label := "+  Add project"
	prefix := "   " + label
	if itemType, _, _ := m.ResolveItem(m.selectedItem); itemType == "add-project" {
		prefix = "  " + addSel.Render("▎") + " " + addSel.Render(label)
	} else {
		prefix = "   " + addStyle.Render(label)
	}
	gap := menuContentWidth - lipgloss.Width(prefix)
	if gap < 0 {
		gap = 0
	}
	rows = append(rows, leftBorder+prefix+strings.Repeat(" ", gap)+rightBorder)
```

In `renderHelpRow`, set the hint text to the new footer (still uses `helpStyle` color `247`):

```go
	hint := "↑↓ move · ⇧↑↓ reorder · ←→ AI · ↵ open · O once · P plain"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestActionBarFor|TestRenderMenuBox_hasTabBar|TestAddProjectRow' -v`
Expected: PASS. Then `go test ./internal/tui/ -v` and update `mainmenu_render_test.go` assertions that referenced old action rows (replace expectations of `Add new project`/`Settings`/`Stats` rows with the tab bar + add-project row).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/render_projects.go internal/tui/render_projects_test.go internal/tui/mainmenu.go internal/tui/mainmenu_render_test.go
git commit -m "feat(tui): rebuild projects tab with contextual action bar"
```

---

### Task 6: Settings & Stats tab bodies

Give Settings and Stats the tab-bar chrome so they read as tabs, and render Stats inside the box body. Settings rendering moves to its own file; the existing settings rows are reused, just prefixed with the title + tab bar.

**Files:**
- Create: `internal/tui/render_settings.go` (move `renderSettingsBox`, `renderSettingsItem`, `renderModelMapPanel` here)
- Create: `internal/tui/render_stats.go` (new in-box `renderStatsBox`)
- Modify: `internal/tui/mainmenu.go` — remove the temporary `renderStatsBox` from Task 2; delete moved functions
- Test: `internal/tui/render_settings_test.go`, `internal/tui/render_stats_test.go` (create)

**Interfaces:**
- Consumes: `boxBorders`, `renderTabBar`, `renderTitleRow`; existing `StatsModel` data accessors in `stats.go`
- Produces: `renderSettingsBox` and `renderStatsBox` both begin with top border + title row + tab bar + separator.

- [ ] **Step 1: Write the failing test**

```go
package tui

import (
	"strings"
	"testing"
)

func TestRenderSettingsBox_hasTabBar(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if !strings.Contains(out, "Settings") || !strings.Contains(out, "Projects") {
		t.Errorf("settings box missing tab bar: %q", out)
	}
	if !strings.Contains(out, "▌") {
		t.Errorf("settings tab should be accented as active")
	}
}

func TestRenderStatsBox_hasTabBar(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabStats)
	out := m.renderStatsBox()
	if !strings.Contains(out, "Stats") {
		t.Errorf("stats box missing tab bar: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestRenderSettingsBox_hasTabBar|TestRenderStatsBox_hasTabBar' -v`
Expected: FAIL — settings box has no tab bar; stats box is the standalone view.

- [ ] **Step 3: Write minimal implementation**

Move `renderSettingsBox`, `renderSettingsItem`, and `renderModelMapPanel` from `mainmenu.go` into `render_settings.go` (cut/paste, same package). At the start of `renderSettingsBox`, replace the standalone top-border/title assembly so it shares chrome:

```go
func (m *MainMenuModel) renderSettingsBox() string {
	top, separator, _, leftBorder, rightBorder := m.boxBorders()
	var lines []string
	lines = append(lines, top)
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)
	// ... existing settings item rows appended here (unchanged) ...
	// ... existing help row ...
	return strings.Join(lines, "\n")
}
```

(Preserve the existing settings item rows, model-map gating, and help row exactly — only the header lines change.)

Delete the temporary `renderStatsBox` added in Task 2 from `mainmenu.go`. Create `internal/tui/render_stats.go`:

```go
package tui

import (
	"strings"
)

// renderStatsBox renders the Stats tab: shared chrome + the stats content body.
func (m *MainMenuModel) renderStatsBox() string {
	top, separator, bottom, leftBorder, rightBorder := m.boxBorders()
	var lines []string
	lines = append(lines, top)
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)
	lines = append(lines, m.renderStatsRows(leftBorder, rightBorder)...)
	lines = append(lines, bottom)
	lines = append(lines, m.renderHelpRow())
	return strings.Join(lines, "\n")
}
```

Implement `renderStatsRows` by formatting the data already exposed by `StatsModel`/`stats.go` (token counts, costs, history) into box rows using `leftBorder`/`rightBorder` and `menuContentWidth`, mirroring `renderSettingsItem`'s label-left/value-right layout. (Reuse `NewStatsModel()` to fetch the numbers; render them as box rows rather than calling its standalone `View()`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestRenderSettingsBox_hasTabBar|TestRenderStatsBox_hasTabBar' -v`
Expected: PASS. Then `go test ./internal/tui/ -v`; update `mainmenu_stats_test.go` / `stats_test.go` if they asserted the old standalone stats layout.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/render_settings.go internal/tui/render_stats.go internal/tui/mainmenu.go internal/tui/render_settings_test.go internal/tui/render_stats_test.go internal/tui/mainmenu_stats_test.go
git commit -m "feat(tui): render settings and stats as tabs"
```

---

### Task 7: Empty state + layout polish

Add the empty-projects state (big ghost + prompt) and verify the layout-height calculation accounts for the new tab-bar row and action bar so the ghost stays vertically centered.

**Files:**
- Modify: `internal/tui/render_projects.go` (`renderProjectRows` empty branch)
- Modify: `internal/tui/mainmenu.go` (`CalculateLayout` height: add the tab-bar row and action-bar row)
- Test: `internal/tui/render_projects_test.go`, `internal/tui/mainmenu_worktree_test.go` (layout)

**Interfaces:**
- Consumes: `m.projects`
- Produces: empty-state row text "No projects yet · press A to add"; corrected `MenuLayout.MenuHeight`.

- [ ] **Step 1: Write the failing test**

```go
func TestRenderMenuBox_emptyState(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	out := m.renderMenuBox()
	if !strings.Contains(out, "No projects yet") {
		t.Errorf("empty state missing prompt: %q", out)
	}
	if !strings.Contains(out, "Add project") {
		t.Errorf("empty state should still offer add row")
	}
}

func TestCalculateLayout_accountsForTabBar(t *testing.T) {
	projects := []models.Project{{Name: "a", Path: "/tmp/a"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	layout := m.CalculateLayout(120, 40)
	// 2 chrome rows added vs old: tab bar + action bar.
	if layout.MenuHeight < 9 {
		t.Errorf("MenuHeight = %d, too small to include tab+action bars", layout.MenuHeight)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestRenderMenuBox_emptyState|TestCalculateLayout_accountsForTabBar' -v`
Expected: FAIL — no empty-state text; `MenuHeight` understates the new rows.

- [ ] **Step 3: Write minimal implementation**

In `renderProjectRows`, when `len(m.projects) == 0`, emit a centered prompt row before the add-project row:

```go
	if len(m.projects) == 0 {
		msg := lipgloss.NewStyle().Foreground(m.theme.Dim).
			Render("No projects yet · press A to add")
		pad := (menuContentWidth - lipgloss.Width(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		gap := menuContentWidth - pad - lipgloss.Width(msg)
		if gap < 0 {
			gap = 0
		}
		rows = append(rows, leftBorder+strings.Repeat(" ", pad)+msg+strings.Repeat(" ", gap)+rightBorder)
	}
```

In `CalculateLayout` (~1124), add the two new chrome rows to `menuHeight`:

```go
	// 7 = top + title + separator + blank + separator + action bar + bottom;
	// +1 for the tab-bar row, +1 for the add-project row.
	menuHeight := 7 + 1 + 1 + projectRows + worktreeRows + addWorktreeRows + numSeparators
```

(Adjust the constant so the rendered line count matches `renderMenuBox`; verify by counting `\n` in the output for a known fixture.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestRenderMenuBox_emptyState|TestCalculateLayout_accountsForTabBar' -v`
Expected: PASS. Then `go test ./internal/tui/ -v`.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/render_projects.go internal/tui/mainmenu.go internal/tui/render_projects_test.go internal/tui/mainmenu_worktree_test.go
git commit -m "feat(tui): add empty state and fix tabbed layout height"
```

---

### Task 8: Full verification & push

Final gate per repo rules.

**Files:** none (verification only)

- [ ] **Step 1: Run the full Go suite**

Run: `go test ./... -count=1`
Expected: PASS. Fix any remaining test that asserts the old layout.

- [ ] **Step 2: Run the project test runner**

Run: `./run-tests.sh`
Expected: PASS.

- [ ] **Step 3: shellcheck (only if any .sh changed)**

Run: `shellcheck lib/*.sh lib/terminals/*.sh bin/ghost-tab wrapper.sh`
Expected: clean. (No shell files are expected to change in this plan; skip if `git diff --name-only` shows none.)

- [ ] **Step 4: Manual smoke (optional but recommended)**

Run: `go run ./cmd/ghost-tab-tui main-menu` (or the existing dev entry) and confirm: Tab cycles Projects/Settings/Stats, action bar updates with selection, `+ Add project` works, ghost still renders.

- [ ] **Step 5: Commit any fixups and push**

```bash
git add -A
git commit -m "test(tui): finalize tabbed redesign verification" --allow-empty
git pull --rebase
git push
git status   # MUST show "up to date with origin"
```

---

## Self-Review

**Spec coverage:**
- Tabbed model (Projects/Settings/Stats) → Tasks 1, 2, 4, 6 ✓
- Big ghost retained → unchanged `View()` ghost composition; verified Task 7 layout ✓
- Refined retro aesthetic (rounded borders, block accents, per-AI palette) → Tasks 4, 5, 6 ✓
- Unicode-block icons only → all glyphs are box-drawing / geometric (`▌▐ ▸ ✕ ⟕ +`); no Nerd Font codepoints ✓
- Contextual action bar → Task 5 ✓
- Add project promoted to visible row → Task 5 ✓
- Worktrees inline (unchanged) → preserved; `ResolveItem`/`TotalItems` extended without disturbing worktree resolution ✓
- Settings rows + model map preserved → Task 6 ✓
- Stats as tab body → Tasks 2 (temp), 6 (real) ✓
- JSON contract unchanged → Task 3 guard + preserved `actionNames`/result emitters ✓
- Render-layer split into focused files → Tasks 4, 5, 6 ✓
- Empty/narrow/idle states → Task 7 (empty); narrow + idle reuse existing `CalculateLayout`/ghost-sleep paths ✓

**Placeholder scan:** Task 5 and Task 6 reference "lift verbatim from the old `renderMenuBox`" / "format the StatsModel data" rather than re-printing ~200 lines of existing render code; the exact source line ranges and the assembled-line order are given, plus the new/changed code is shown in full. The `⟕` glyph (U+27D5) in the action bar is a geometric symbol — if it renders inconsistently in testing, substitute `▸` or `~`; the test asserts the word "Worktrees", not the glyph, so either passes.

**Type consistency:** `MenuTab`, `actionBarFor`, `boxBorders`, `renderTabBar`, `renderTitleRow`, `renderUpdateRow`, `renderProjectRows`, `renderHelpRow`, `renderActionBar`, `renderStatsRows`, `renderStatsBox`, `renderSettingsBox` are named identically across all tasks. `boxBorders` returns 5 values `(top, separator, bottom, leftBorder, rightBorder)` and every caller uses that order.
