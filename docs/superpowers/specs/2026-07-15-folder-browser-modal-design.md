# Folder Browser Modal for Add Project

**Date:** 2026-07-15
**Status:** Approved

## Problem

Choosing "Add Project" drops the user into a free-text Path field. Even with
tab-completion, typing a filesystem path is slow and error-prone. The user
wants to *navigate* to the folder instead: opening "Add Project" should show
an overlay modal file browser in which they can freely walk the filesystem
and pick the target folder.

## Goals

- "Add Project" opens a floating overlay modal directory browser instead of
  the text Path field.
- The user can descend into folders, go back up, filter by typing, and choose
  any directory as the project folder.
- Feature parity is preserved: pasting a GitHub URL still triggers the clone
  flow, and the existing Name stage (auto-derive, duplicate warning) still
  runs after a folder is chosen.

## Non-Goals

- The `open-once` input mode keeps its current text field (out of scope).
- No file selection — directories only, matching today's autocomplete.
- No hidden-dotfolder toggle; dotfolders stay hidden (matching autocomplete),
  but a typed filter beginning with `.` reveals matching dotfolders.
- No mouse-driven list interaction beyond click-outside-to-dismiss.

## Approaches Considered

1. **Embedded browser model + floating overlay card (chosen).** A new
   `DirBrowserModel` lives inside `MainMenuModel`, rendered with the same
   composited-overlay chrome as the About card / account-switch popup.
   Pros: reuses the established modal pattern, keeps clone/name/persistence
   logic in-process, no bash changes. Cons: `mainmenu.go` grows another mode.
2. **Separate subprocess modal emitting JSON** (like `confirm`). Rejected:
   add-project already mutates the projects file inside the main-menu
   process; round-tripping through bash would fork the clone and name logic.
3. **Keep the text field, add the browser as an optional popup.** Rejected:
   the request is to *replace* the typing flow, and two entry paths doubles
   the surface to test.

## Design

### Flow

```
menu ── "add project" ──▶ [folder browser overlay]
                             │  Esc ─────────────▶ back to menu
                             │  choose folder ───▶ [name stage]  (existing form,
                             │                      path locked, focus on Name)
                             │  GitHub URL + ⏎ ──▶ [name stage] ─▶ clone flow
                             ◀── Esc / Shift+Tab from Name reopens the browser
```

### DirBrowserModel (`internal/tui/dirbrowser.go`)

State: `cwd` (absolute path), `entries` (subdirectory names of `cwd`, sorted,
dotfolders excluded), `filter` (typed text), `selected` index over the
*visible* rows, and a scroll window (`maxVisible` rows, about 10).

Visible rows = pinned **"⏎ choose this folder"** row (row 0, selecting `cwd`)
followed by the entries whose names case-insensitively contain `filter`.
When `filter` starts with `.`, dotfolders are included in the match pool.

Keys (handled by `MainMenuModel` routing to the browser while it is open):

| Key | Action |
|-----|--------|
| ↑ / ↓ | move highlight (clamped, window scrolls) |
| Enter / → | on a folder row: descend into it, reset filter; on row 0: choose `cwd` |
| Tab | choose the highlighted folder directly (row 0 → `cwd`) |
| ← / Backspace (empty filter) | go up to parent (stops at `/`) |
| Backspace (non-empty filter) | delete last filter rune |
| printable runes | append to filter, reset highlight to first match |
| Esc | non-empty filter: clear it; else close the browser, back to menu |
| Ctrl+C | quit the app (action result "quit", matching the form) |

Start directory: the configured projects-root (`readProjectsRoot`), falling
back to `$HOME`. Unreadable directories keep the user where they are and show
the error in the card's status line.

**GitHub URLs:** the filter doubles as the URL slot. When the filter parses
via `util.ParseGitHubRepo`, the list is replaced by a status line
("Clone <owner/repo> — press ⏎") and Enter hands the URL off exactly as the
old path field did. This keeps paste-a-URL parity.

### Card rendering

The browser renders as a floating card using the About-card chrome: rounded
gray border, title `Add Project — choose folder` embedded in the top border,
faint-gray dimmed backdrop, centered. The About-specific compositor
(`overlayAbout`) is generalized into a shared `overlayCard(placed, cardLines,
left, top, width)` helper in `about.go`, used by both overlays — a targeted
refactor, not a rewrite.

Card body, fixed height so the card never jumps:

```
╭─ Add Project — choose folder ──────────────╮
│  ~/Packages                                │   ← cwd (home-abbreviated)
│  Filter: wi▌                               │   ← filter line (always shown)
│  ▸ ⏎ choose this folder                    │
│    wisp-deck/                              │
│    wisp-web/                               │
│    …                                       │
│                                            │   ← status slot (error / clone hint / blank)
│  ⏎ open · tab choose · ← up · esc cancel   │
╰────────────────────────────────────────────╯
```

### Integration with the existing form

- `enterInputMode("add-project")` now opens the browser
  (`m.browser = NewDirBrowser(startDir)`) instead of focusing the path text
  input. `inputMode` stays `"add-project"`; a non-nil `m.browser` means the
  browser stage is active.
- Choosing a folder sets `m.pathInput.SetValue(chosen)` and calls the
  existing `advanceToNameField()`; the browser closes and the familiar
  add-project box appears with the Path row rendered as static text (no
  prompt/cursor — it is no longer editable) and focus on Name. All existing
  name logic (auto-derive, empty check, duplicate warning, clone dispatch on
  GitHub URL) is untouched.
- Esc / Shift+Tab in the Name stage reopens the browser at the previously
  chosen folder's parent (or the URL case reopens it with the URL back in
  the filter) instead of focusing the path field.
- `open-once` mode is untouched and keeps the text field + autocomplete.

### Bash / JSON contract

Unchanged. Add-project still persists via `AppendProject` inside the Go
process; `lib/menu-tui.sh` needs no changes.

## Error Handling

- Unreadable directory on descend: stay in place, show the error in the
  status slot.
- Filter with no matches: list shows only the "choose this folder" row.
- All post-choose validation (duplicate project/name, clone destination
  collisions, save failures) is the existing form's and is unchanged.

## Testing

TDD throughout; real temp dirs (`t.TempDir()`), no FS mocking.

- `test/internal/tui/dirbrowser_test.go` — model behavior: listing (sorted,
  dotfolders hidden), filter narrowing (and `.`-prefix revealing dotfolders),
  descend/up navigation, choose-cwd and choose-highlighted, scroll clamping,
  unreadable-dir error, GitHub URL detection.
- `test/internal/tui/mainmenu_browser_test.go` — integration: "add project"
  opens the browser; Esc returns to menu; choosing a folder lands in the name
  stage with the path set and name auto-derived; submit appends the project;
  Esc from name reopens the browser; URL-in-filter reaches the clone flow
  (via `SetGitCloneForTest`).
- `internal/tui/addproject_render_test.go` — updated: name-stage renders the
  chosen path as static text; browser card renders title, cwd, rows, footer;
  stable card height across states.
- `test/internal/tui/mainmenu_github_test.go` — updated to drive the URL
  through the browser filter instead of the path input.
