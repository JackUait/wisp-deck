# Folder Browser Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** "Add Project" opens a floating overlay-modal directory browser (navigate, filter, choose folder) instead of a free-text Path field, then hands off to the existing Name stage / GitHub-clone flow.

**Architecture:** New `DirBrowserModel` (pure state + FS reads via `os.ReadDir`) embedded in `MainMenuModel`; rendered as a floating card composited over the dimmed screen by a shared `overlayCard` helper extracted from `overlayAbout`. Choosing a folder feeds the existing `pathInput`/`advanceToNameField` machinery so name auto-derive, dup-warn, persistence, and GitHub cloning stay untouched.

**Tech Stack:** Go, Bubbletea, lipgloss. Tests with `go test`, real temp dirs.

## Global Constraints

- TDD, iron rule: failing test first for every behavior change.
- Work directly on `main`, no branches/worktrees.
- Stage only files this feature touches (concurrent sessions share the checkout).
- `open-once` input mode keeps its current text-field behavior.
- Directories only, dotfolders hidden unless the filter starts with `.`.
- Full suite `./run-tests.sh` + push at the end.

---

### Task 1: Extract shared `overlayCard` compositor

**Files:**
- Modify: `internal/tui/about.go` (overlayAbout → thin wrapper)
- Test: `test/internal/tui/overlay_card_test.go`

**Interfaces:**
- Produces: `func (m *MainMenuModel) overlayCard(placed string, cardLines []string, left, top, width int) string` — dims the placed screen faint-gray and lays the card lines at (left, top).

- [x] Step 1: Failing test — `overlayCard` places arbitrary card lines at given origin over a dimmed backdrop; `overlayAbout` output is unchanged (byte-identical to composing via overlayCard with the About card's layout).
- [x] Step 2: Run, watch fail (undefined `overlayCard`).
- [x] Step 3: Move the compositor loop from `overlayAbout` into `overlayCard(placed, cardLines, left, top, width)`; `overlayAbout` calls it with `renderAboutCard()` lines + `aboutCardLayout()`.
- [x] Step 4: Tests pass; existing about tests pass.
- [x] Step 5: Commit `refactor(tui): extract overlayCard compositor from overlayAbout`.

### Task 2: DirBrowserModel core

**Files:**
- Create: `internal/tui/dirbrowser.go`
- Test: `test/internal/tui/dirbrowser_test.go`

**Interfaces (produces):**
```go
type DirBrowserModel struct { /* cwd, entries, filter, selected, scroll, errMsg */ }
func NewDirBrowser(startDir string) DirBrowserModel
func (b *DirBrowserModel) Cwd() string
func (b *DirBrowserModel) Filter() string
func (b *DirBrowserModel) VisibleRows() []string   // row 0 = chooseThisFolderRow sentinel, then filtered subdir names
func (b *DirBrowserModel) Selected() int
func (b *DirBrowserModel) MoveUp() / MoveDown()
func (b *DirBrowserModel) Descend() bool           // enter highlighted dir (row>0); false on row 0
func (b *DirBrowserModel) GoUp()
func (b *DirBrowserModel) ChooseHighlighted() (path string, ok bool) // row 0 → cwd
func (b *DirBrowserModel) TypeRune(r rune) / BackspaceFilter() bool  // false when filter empty
func (b *DirBrowserModel) ClearFilter()
func (b *DirBrowserModel) GitHubURL() (cloneURL string, ok bool)     // filter parses as GH URL
func (b *DirBrowserModel) Err() string
const chooseThisFolderRow = "⏎ choose this folder"
```

- [x] Step 1: Failing tests: new browser lists sorted subdirs of startDir, dotdirs hidden, files excluded; filter narrows case-insensitively; `.`-prefixed filter reveals dotdirs; Descend resets filter+selection; GoUp goes to parent and stops at `/`; ChooseHighlighted on row 0 returns cwd, on row N returns subdir path; unreadable dir on Descend sets Err and stays; GitHubURL detects `https://github.com/o/r`; selection clamps when filter shrinks list.
- [x] Step 2: Run, watch fail (package has no DirBrowserModel).
- [x] Step 3: Implement minimal model (readDir on cwd change, recompute visible rows on filter change).
- [x] Step 4: Tests pass.
- [x] Step 5: Commit `feat(tui): DirBrowserModel directory navigation core`.

### Task 3: Browser card rendering + overlay

**Files:**
- Modify: `internal/tui/dirbrowser.go` (card lines), `internal/tui/mainmenu.go` (View hook)
- Test: `test/internal/tui/dirbrowser_render_test.go`

**Interfaces:**
- Produces: `func (m *MainMenuModel) renderBrowserCard() string`, `func (m *MainMenuModel) browserCardLayout() (left, top, w, h int)`, `m.browser *DirBrowserModel` field; View composites via `overlayCard` when `m.browser != nil`.

- [x] Step 1: Failing render tests: card shows border title "Add Project — choose folder", home-abbreviated cwd, filter line, highlighted row marker, footer hints; fixed row count (maxVisible window) so height is stable; scrolled window shows correct slice; GitHub-URL filter replaces list with clone hint; View() output contains card when browser open.
- [x] Step 2: Watch fail.
- [x] Step 3: Implement rendering (About-card chrome: rounded border 245, embedded title via `embedAboutBorderTitle`-style helper, fixed `browserMaxVisible = 10` rows) and hook into `View()` before the aboutOpen check.
- [x] Step 4: Pass.
- [x] Step 5: Commit `feat(tui): floating folder-browser card rendering`.

### Task 4: Wire browser into add-project flow

**Files:**
- Modify: `internal/tui/mainmenu.go` (enterInputMode, key routing, name-stage Esc, renderInputBox path row, footer)
- Test: `test/internal/tui/mainmenu_browser_test.go`

**Interfaces:**
- Consumes: DirBrowserModel API from Task 2; `advanceToNameField`, `submitInputMode` (existing).

- [x] Step 1: Failing integration tests: triggering add-project opens the browser (browser non-nil, no focused path input); Esc closes back to menu; arrow+Enter descends; choosing folder → name stage, `pathInput` holds chosen path, name auto-derived, path row rendered static (no cursor); Enter in name stage appends project to file; Esc from name stage reopens browser; GitHub URL typed into filter + Enter → name stage → submit runs clone flow (`SetGitCloneForTest`); Ctrl+C quits.
- [x] Step 2: Watch fail.
- [x] Step 3: Implement: `enterInputMode("add-project")` builds `NewDirBrowser(root-or-home)` and sets `m.browser`; key routing branch `if m.browser != nil` ahead of inputMode handling; choose → `pathInput.SetValue`, `browser=nil`, `advanceToNameField()`; name-stage Esc/Shift+Tab reopens browser; `renderInputBox` renders static path text in add-project name stage; footer variants updated.
- [x] Step 4: Pass.
- [x] Step 5: Commit `feat(tui): add-project opens folder-browser overlay modal`.

### Task 5: Migrate existing tests + full verification

**Files:**
- Modify: `internal/tui/addproject_render_test.go`, `test/internal/tui/mainmenu_github_test.go` (+ any other add-project-path-driven tests found by the suite)

- [x] Step 1: Run full suite; list failures caused by the new flow.
- [x] Step 2: Update tests to drive the browser (filter for URL flow; choose-folder for path flow) while preserving each test's original assertion intent.
- [x] Step 3: `./run-tests.sh` green; `shellcheck` untouched scripts only if shell files changed (none expected).
- [x] Step 4: Commit `test(tui): migrate add-project tests to folder-browser flow`, push, verify `git status` up to date.
