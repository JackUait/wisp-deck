# Author Credits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Attribute Wisp Deck to Evgeniy Pyatkov in a credits file, project metadata, and the Settings screen.

**Architecture:** A dim non-selectable credit line is appended to the Settings box render (no positional-row changes). `package.json` author becomes a structured object. `CREDITS.md` + a README section carry the attribution in docs.

**Tech Stack:** Go (Bubbletea/lipgloss TUI), Go tests, Markdown, JSON.

## Global Constraints

- Canonical credit text: `Made by Evgeniy Pyatkov (@jackuait) · Telegram: @that_ai_guy` with link `https://t.me/that_ai_guy`.
- TDD: write test → watch it FAIL → implement → watch it PASS → commit. No code before its test.
- Run `gofmt`/`shellcheck` as applicable; no branches — commit on `main`.
- The Settings credit line MUST NOT be a selectable settings item: `settingsItemCount()` must stay `rowKeepAwake + 1`.

---

### Task 1: Settings credit line (interface)

**Files:**
- Modify: `internal/tui/render_settings.go` (in `renderSettingsBox`, after the section loop, before the trailing `emptyRow`/separator/help block near the end of the function)
- Test: `internal/tui/render_settings_test.go`

**Interfaces:**
- Consumes: `renderSettingsBox() string`, `settingsItemCount() int` (returns `rowKeepAwake + 1`), package vars `dimStyle`, `menuContentWidth`, and the local `leftBorder`/`rightBorder`/`emptyRow` built inside `renderSettingsBox`.
- Produces: nothing new consumed by later tasks.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/render_settings_test.go`:

```go
func TestRenderSettingsBox_showsAuthorCredit(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()

	for _, want := range []string{"Made by Evgeniy Pyatkov (@jackuait)", "@that_ai_guy"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings box missing credit %q:\n%s", want, out)
		}
	}
	// The credit is decorative, not a selectable row.
	if got := m.settingsItemCount(); got != rowKeepAwake+1 {
		t.Errorf("settingsItemCount changed to %d; credit must not be a selectable row", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRenderSettingsBox_showsAuthorCredit -v`
Expected: FAIL — output does not contain "Made by Evgeniy Pyatkov (@jackuait)".

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/render_settings.go`, inside `renderSettingsBox`, immediately after the `for _, section := range ...` loop that appends section headers/items and BEFORE the `// Empty row` / `lines = append(lines, emptyRow)` that precedes the separator, insert:

```go
	// Decorative author credit — a dim, non-selectable line (not a settings
	// item, so settingsItemCount / row indices are untouched).
	lines = append(lines, emptyRow)
	creditText := "Made by Evgeniy Pyatkov (@jackuait) · @that_ai_guy"
	creditPrefix := " " + dimStyle.Render(creditText)
	creditGap := menuContentWidth - lipgloss.Width(dimStyle.Render(creditText)) - 1
	if creditGap < 0 {
		creditGap = 0
	}
	lines = append(lines, leftBorder+creditPrefix+strings.Repeat(" ", creditGap)+rightBorder)
```

(`lipgloss` and `strings` are already imported in this file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestRenderSettingsBox_showsAuthorCredit -v`
Expected: PASS.

- [ ] **Step 5: Guard against layout-height regressions**

Run: `go test ./internal/tui/ -run TestRenderSettingsBox -v`
Expected: PASS for all `TestRenderSettingsBox_*`. If a fixed-height/row-count assertion fails, adjust that expectation to account for the 2 added lines (blank + credit) — the credit is intended chrome. Then re-run.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/tui/render_settings.go
git add internal/tui/render_settings.go internal/tui/render_settings_test.go
git commit -m "feat(settings): show author credit line"
```

---

### Task 2: package.json author metadata

**Files:**
- Modify: `package.json:30`
- Test: `test/npx/version_sync_test.go` (add a new test; reuse the existing `projectRoot(t)` helper)

**Interfaces:**
- Consumes: `projectRoot(t) string` (test helper in the `npx_test` package).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the failing test**

Add to `test/npx/version_sync_test.go`:

```go
func TestPackageJSON_author_is_structured(t *testing.T) {
	root := projectRoot(t)
	pkgBytes, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Author struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"author"`
	}
	if err := json.Unmarshal(pkgBytes, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Author.Name != "Evgeniy Pyatkov" {
		t.Errorf("author.name = %q, want %q", pkg.Author.Name, "Evgeniy Pyatkov")
	}
	if pkg.Author.URL != "https://t.me/that_ai_guy" {
		t.Errorf("author.url = %q, want %q", pkg.Author.URL, "https://t.me/that_ai_guy")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/npx/ -run TestPackageJSON_author_is_structured -v`
Expected: FAIL — `author.name` is empty (author is currently the string `"JackUait"`, which unmarshals into the struct as zero values).

- [ ] **Step 3: Write minimal implementation**

In `package.json`, replace line 30:

```json
  "author": "JackUait",
```

with:

```json
  "author": {
    "name": "Evgeniy Pyatkov",
    "url": "https://t.me/that_ai_guy"
  },
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./test/npx/ -run TestPackageJSON -v`
Expected: PASS (both the new author test and the existing version-sync test).

- [ ] **Step 5: Commit**

```bash
git add package.json test/npx/version_sync_test.go
git commit -m "chore: structured author metadata in package.json"
```

---

### Task 3: CREDITS.md and README section (docs)

**Files:**
- Create: `CREDITS.md`
- Modify: `README.md` (add a `## Credits` section near the bottom, before any final license/footer content)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing (static docs; no behavior test per spec).

- [ ] **Step 1: Create `CREDITS.md`**

```markdown
# Credits

Made by **Evgeniy Pyatkov** ([@jackuait](https://github.com/JackUait)).

Telegram: [@that_ai_guy](https://t.me/that_ai_guy)
```

- [ ] **Step 2: Add a Credits section to `README.md`**

Append near the bottom of `README.md`:

```markdown
---

## Credits

Made by **Evgeniy Pyatkov** ([@jackuait](https://github.com/JackUait)) — Telegram: [@that_ai_guy](https://t.me/that_ai_guy).

See [CREDITS.md](CREDITS.md).
```

- [ ] **Step 3: Verify rendering**

Run: `grep -c "that_ai_guy" CREDITS.md README.md`
Expected: each file reports `1`.

- [ ] **Step 4: Commit**

```bash
git add CREDITS.md README.md
git commit -m "docs: add author credits"
```

---

### Task 4: Full verification

- [ ] **Step 1: Run the full suite**

Run: `./run-tests.sh`
Expected: PASS.

- [ ] **Step 2: Lint any touched shell (none expected)**

No shell scripts were modified in this plan; skip `shellcheck` unless `git diff --name-only` shows a `.sh` change.

- [ ] **Step 3: Push**

```bash
git pull --rebase
git push
git status   # MUST show "up to date with origin"
```

---

## Self-Review

- **Spec coverage:** Interface credit (Task 1, Approach A, non-selectable) ✓; CREDITS.md + README section (Task 3) ✓; package.json structured author (Task 2) ✓; testing requirements — settings render test asserting text + unchanged item count (Task 1), package.json parse test (Task 2) ✓.
- **Placeholder scan:** No TBD/TODO; every code step shows real code.
- **Type consistency:** `settingsItemCount()`/`rowKeepAwake` used as defined in `mainmenu.go`; `projectRoot(t)` used as defined in `test/npx`; `dimStyle`/`menuContentWidth`/`leftBorder`/`rightBorder`/`emptyRow` are the same identifiers used elsewhere in `renderSettingsBox`.
