# Add-Project Form Polish — Design

Date: 2026-07-15

## Goal

Make the add-project form look and feel great. The current form (screenshot
review) has four concrete problems:

1. **No focus indication.** Two fields, but nothing marks which one is active
   besides the blinking cursor.
2. **The GitHub tip crowds the form.** It renders between the Path and Name
   rows in the theme's dim-orange, reading like a warning, and it appears/
   disappears as you type, so the box height jumps.
3. **No destination feedback for GitHub URLs.** Pasting a URL gives no clue
   where the repo will be cloned until an error appears after submit.
4. **The cloning state is a static text line.** No motion, so a long clone
   looks frozen.

## Design

All changes live in `MainMenuModel` (`internal/tui/mainmenu.go`), almost
entirely inside `renderInputBox`.

### 1. Focus markers

The focused field's label renders as `▸ Path: ` / `▸ Name: ` in the theme
Primary color, bold. The unfocused label keeps its current dim styling with a
two-space indent. Both forms are exactly 8 cells, so layout is unchanged.

### 2. A single stable status slot under the Path row

One row directly under the path input shows exactly one of, in priority
order:

1. Path error — `✗ <message>` in red.
2. GitHub repo detected (`util.ParseGitHubRepo` succeeds on the field value)
   — a live destination preview in the theme Accent color:
   `⬡ github.com/owner/repo → ~/Projects/<name>`
   The destination is the same computation `startGitHubClone` uses
   (projects root, else `$HOME`, joined with the current name-field value),
   shown with `$HOME` abbreviated to `~`. If the destination directory
   already exists or is already a registered project, the slot shows
   `✗ already exists: <dest>` in red instead — the collision the user would
   otherwise only discover on submit.
3. The tip, reworded and dim: `Tip: paste a GitHub repo URL to clone it` —
   only while the path field is focused and no autocomplete suggestions are
   open.
4. Otherwise a blank row.

Because the slot always renders, the box height no longer jumps while
typing (autocomplete suggestion rows remain the one exception).

The destination computation is extracted into a `cloneDestDir()` helper
shared by `startGitHubClone` and the preview so they can never disagree.

### 3. Error prefix

Name-field errors gain the same `✗ ` prefix as path errors (red, two-space
indent). Soft-warn duplicate-name message included.

### 4. Clone spinner

While `m.cloning`, the status area shows an animated braille spinner
(`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) in the Accent color:
`⠋ Cloning github.com/owner/repo → ~/Projects/<name>`
driven by a dedicated `cloneTickMsg` ticker (80ms, mirroring
`installTickMsg` — bob ticks only run when the mascot is animated, so the
spinner gets its own ticker). The ticker re-arms only while `m.cloning`, so
it dies with the clone. The footer help while cloning reads `Ctrl+C quit`
(Esc and Enter are ignored in that state, so advertising them would lie).

The old static "Cloning repository…" row under the name field is replaced by
this spinner row in the status slot.

## Out of scope

- Any behavioral change to validation, clone, or submit flows.
- Autocomplete rendering.
- The open-once variant keeps the same improvements where they apply (focus
  marker, error prefix); it has no name field or GitHub flow.

## Testing

TDD, red first. Render tests live in `internal/tui` (white-box, ANSI
stripped) next to `mainmenu_render_test.go`; behavioral clone-spinner tests
extend `test/internal/tui/mainmenu_github_test.go` via the existing exported
test seams.

- Focused path field renders `▸ Path:`; name label unmarked — and flipped
  after Tab.
- GitHub URL in path → status slot shows `→ <root>/<repo>` preview; tip
  hidden.
- GitHub URL whose destination exists → status slot shows `✗ already
  exists`.
- No URL, path focused → tip rendered; with an error → `✗` row rendered and
  tip hidden.
- Name error renders with `✗ ` prefix.
- Cloning: View contains a braille spinner frame and the destination;
  successive `cloneTickMsg`s advance the frame; ticker stops (returns no
  cmd) once cloning is done; footer shows `Ctrl+C quit`.
- Box height identical with tip visible vs. hidden (stable slot).
