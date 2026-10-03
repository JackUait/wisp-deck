# tui — gotchas

Gotchas for the Go TUI. Loaded when Claude opens a file in this package.

### The diff pager measures text in cells, never in runes

Everything the file preview lays out — `fitColumn`, `wrapColumns`, `tintColumn`,
`truncatePath`, the popup frame — measures through `cellWidth`/`forEachCell` in
`internal/tui/diffview.go`. **Never reach for `len([]rune(s))`,
`runewidth.RuneWidth` per rune, `runewidth.StringWidth` over a whole string, or
`lipgloss.Width` on text that came out of a file.** Each of those has already
shipped a broken preview:

- Per-rune `RuneWidth` counts a Bengali vowel sign as its own cell. A locale
  file read ~50% wider than the terminal drew it, so every line truncated and
  wrapped early and the side-by-side divider walked off its column.
- Whole-string `runewidth.StringWidth` disagrees the other way: this repo's
  Unicode tables are newer than tmux's, so an Indic conjunct reads one cell
  narrower than tmux paints it.
- `lipgloss.Width` (and therefore `lipgloss`'s `Width()` padding, `Border()` and
  `Place()`) uses a third table. Letting lipgloss frame the popup re-padded rows
  that were already exact and produced 122- and 124-cell rows inside a 120-cell
  box — hence `framePopup` and `placeBox` draw the chrome by hand.
- Segmenting each colored run separately splits a letter from its accent,
  because the highlighter wraps every rune in its own SGR pair. Escapes are
  transparent to the terminal's composition, so `forEachCell` walks the
  escape-free projection of the line.

tmux owns the cell grid the popup is painted into, so tmux is the authority —
not Unicode, not a library. The model is pinned to it by
`internal/tui/testdata/tmux_widths.json`, a recording of what a real tmux
painted for 555 strings (real shipped-locale lines across ~60 languages, the
constructs that segment strangely, and every codepoint class where the tables
were ever found to disagree). `TestCellWidth_matches_a_live_tmux` re-derives it
from a live tmux on demand — run it after bumping tmux, go-runewidth or uniseg.

Where the model cannot be exact it **rounds up, never down**: over-counting
leaves a blank cell, while under-counting writes past the column edge and shoves
everything after it sideways. `TestCellWidth_never_under_counts` enforces that
direction, and it holds for all 154,996 assigned codepoints.

### The project menu polls for worktrees, because nothing tells it

`git worktree list` used to run exactly once, when the menu was built, plus on
the four changes the menu made itself (created a worktree, removed one, added a
project, deleted one). Nothing watched the filesystem and there was no refresh
key, so a worktree created in a terminal while the menu sat open simply never
appeared — the only cure was quitting and relaunching.

So `worktreeRefreshCmd` re-detects every project's worktrees on a ~2s loop and
`initCmds` arms it **unconditionally** — the ghost tickers beside it are gated
on `ghostDisplay == "animated"`, and copying that gate would leave a static-ghost
session with the original bug. Rules that fell out of building it:

- **`AppModel` delegates to `a.top()`, so the loop needs its own route.** The
  poll reschedules itself from the menu's own `Update`; delivered to the topmost
  screen it would be swallowed by the branch picker and the chain would be dead
  for the rest of the session. `worktreesRefreshedMsg` is routed to `stack[0]`.
- **Detection never touches `m.projects`.** `PopulateWorktrees` writes through
  the slice `View` is reading — a data race the moment it runs off the Update
  loop. `models.DetectWorktreesFor` returns fresh data keyed by path instead,
  and it spawns one git process per project, so it must stay in a `tea.Cmd`.
- **Results are applied by path, never by index.** A project can be added or
  deleted between the spawn and the delivery, and an index would write one
  project's worktrees onto another. A path the round did not report keeps what
  it had: absent means "not measured", not "none".
- **The cursor is anchored to the worktree's path, not its row.** Enter launches
  whatever the cursor is on; a worktree appearing above it shifts every row
  below, and re-anchoring by index would silently move the cursor onto a
  different worktree.
- **A refresh is held back mid-flow** (`inputMode`, `deleteMode`, `cloning`, a
  pending branch pick). `deleteSelected` is a flat index, so a row moving under
  an open delete confirm is how someone removes the wrong worktree.
- **An emptied project stays expanded.** `ToggleWorktrees` deliberately expands
  a worktree-less project to its lone add-worktree row, so collapsing on the
  poll would close a row the user opened on purpose —
  `reloadAfterWorktreeRemoval` prunes because it ends a removal flow, not
  because expansion implies worktrees.

The poll surfaces ephemeral worktrees too (subagent `.claude/worktrees/*`,
this suite's own temp checkouts) for as long as they exist; that is the list
being true, not a defect.

The poll also sweeps (`models.SweepDetachedWorktrees`), at most once per
`worktreeSweepInterval` and never mid-flow. It removes a detached worktree only
when that loses nothing: it must be in a throwaway root (a Claude scratchpad, or
`.claude/worktrees/`), unlocked, idle for `worktreeSweepMinIdle`, have its HEAD
on some ref, have no process with its cwd inside, and pass a non-force
`git worktree remove`, which refuses uncommitted and untracked files. Ignored
files go with it. Then `git worktree prune` drops registrations whose folder is
gone.

- **Throwaway roots only.** A detached checkout elsewhere can be deliberate.
  blok keeps a base checkout in `.blok-undo/wt/` that is clean and on `main`.
- **The idle wait is what protects a fresh worktree.** Claude Code's Bash tool
  starts a new process for each command, so between commands no cwd is inside
  the worktree and lsof cannot see it is about to be used.
- **lsof runs last.** It costs about 0.25 CPU-s, so it runs only when a
  candidate survives the cheaper checks.

Guarded by `internal/tui/mainmenu_worktree_refresh_test.go` — including
`_picksUpAWorktreeGitCreatedAfterTheMenuOpened`, which drives the real loop
against a real repo, and `TestAppModel_deliversAWorktreeRefreshToTheMenuUnderAPushedScreen` —
plus `TestDetectWorktreesFor_*` in `test/internal/models/worktree_refresh_test.go`.

### The popup backdrop is built for a user, not for a timer

`refreshBackdrop` ran on every 2s ledger refresh, spawning `tmux
display-message`, `tmux list-panes` and one `tmux capture-pane` **per pane**,
then writing and renaming a temp file — 220-247ms, forever, in every session,
maintaining the dimmed screen behind a popup that only ever opens on a click.
It is now armed by interaction, which also makes it *fresher* than a 2s timer.

- **Throttled to one rebuild per 750ms**: a mouse crossing the pane emits a
  motion event per cell, and un-throttled that costs more than the timer did.
- **An interaction that OPENED a popup is excluded**, preserving the existing
  contract that a click never waits on or starts a refresh
  (`TestLedgerOpenClickStartsPopupOffInputLoopOnCacheMiss`). The hover that
  necessarily preceded the click is what armed it.

### An image preview decodes in Go first, and falls back to macOS ImageIO

Clicking an image in the ledger opens a PREVIEW popup instead of the useless
"Binary files differ" diff. Which files that covers is one list kept in two
places — `previewableImageExts` (`internal/tui/imageformats.go`) and the
`is_image_file` case glob (`lib/compact-view.sh`) — because a pane picks its
renderer by binary capability, so a format added to one and not the other
previews for half the users. `TestIsImageFile_matches_the_Go_renderers_list`
pins them together, and `TestPreviewableImageExtensions_all_decode` refuses an
extension that has no fixture in `internal/tui/testdata/img` proving it decodes.

`decodeImage` (`internal/tui/imagedecode.go`) tries the registered Go decoders
first — stdlib PNG/JPEG/GIF plus `x/image`'s WebP/BMP/TIFF — so the formats a
repo is mostly made of cost nothing but the decode. AVIF, HEIC/HEIF, ICO/ICNS
and SVG have no pure-Go decoder here, and each shells out to `sips`, which is
ImageIO and reads all of them. Rules that fell out of building it:

- **The extension travels with the bytes.** ImageIO sniffs every container from
  its magic number except SVG, which is plain text and is recognized only by the
  scratch file's extension. `NewImageView` takes it from the title (the path).
- **Convert at `previewRasterMaxSide`, but only downward.** A 24-megapixel phone
  photo converted at full size cost **9 seconds** of popup-open latency for
  pixels nothing keeps (the cap is `kittyMaxSide`); converting at the ceiling is
  1.4s. `--resampleHeightWidthMax` resizes in BOTH directions, so a cheap
  `sips -g pixelWidth` probe (~30ms) gates it — passing it unconditionally would
  blow an 8px icon up to 2048 and sneak a blurry enlargement past the renderer's
  deliberate no-upscale rule.
- **A vector is the one thing to enlarge.** An SVG has no pixels of its own, so
  it is rasterized AT the ceiling rather than at its declared size — otherwise a
  16px icon previews as a speck.
- **An SVG previews but is not a byte-delta row.** git tracks it as text: it has
  real line counts and no hydrated byte size, so `is_binary_image_file` (and, on
  the Go side, `row.Binary`) keeps it on the `+N −N` row while `is_image_file`
  still routes its click to the preview. Sizing it would print "±0" on every
  edit.
- **The gate is extension + presence, not `NewBytes`.** `opensImagePreview`
  stats the file, exactly like the shell's `[ -f ]`: byte sizes exist only for
  binary changes, and a deleted image must still fall back to the diff rather
  than cat a path that is gone.

### The ledger's account pill is re-resolved every refresh, never once

The ledger pane always races its own relaunch context. tmux `new-session`
stamps `WISP_DECK_RELAUNCH_FILE` into the pane's env and creates the pane in
the same batch, while `wrapper.sh` writes the file itself in the launch tail —
and it **must stay there**: `test/bash/launch_post_pick_path_test.go` keeps
every millisecond of tail work behind `new-session` so the agent's boot
overlaps it. Whether the pane wins the race is decided by how warm the TUI
binary is, which is why the symptom was "the pill sometimes doesn't show".

So the pill's context is **state that becomes valid later**, and the ledger
must treat it that way:

- `LedgerModel` reloads the session context on **every refresh tick**
  (`internal/tui/ledger.go`). A one-shot load in `Init()` turned any transient
  miss — absent file, partial read, tmux hiccup — into a pane with no pill for
  its entire life.
- The shell fallback renderer re-reads the context each build tick until it
  resolves (`lib/compact-view.sh`). It recomputed the pill per tick but read the
  context *once* before the loop, so a pane that won the race kept empty account
  paths forever. **Both renderers must self-heal** — the pane picks between them
  by binary capability, so a fix in one is a fix for half the users.
- A failed reload **keeps the last good context**. Blanking it makes the pill
  drop out of the footer until the next tick.
- `write_relaunch_context` publishes by **rename** (`lib/account-switch.sh`).
  A truncated prefix parses cleanly into a context with no accounts —
  indistinguishable from "nothing to switch to" — so a mid-write reader would
  silently drop the pill instead of failing and being retried. The mid-session
  switch rewrites this same file under a live pane, so the window is not
  confined to launch.
- An action error **shares** the footer with the pill rather than replacing it.
  `actionError` is sticky until some later action succeeds, so taking the row
  over hid the pane's identity — and its only switch affordance — indefinitely.

Guarded end-to-end by `test/bash/ledger_pill_race_test.go` and
`test/bash/compact_view_pill_late_context_test.go` (both drive a real renderer
over a pty with the context published late), plus the model-level tests in
`internal/tui/ledger_session_reload_test.go` and the atomic-publish test in
`test/bash/relaunch_context_ready_test.go`.

### The ledger polls Git for a repository that is almost never changing

Sampling every repository a live ledger was watching, at the ledger's own 2s
cadence, for two minutes while agents were working: **320 polls, 3 of which found
anything changed — 99.1% waste**. Five concurrent git processes each time, which
came to **~63% of one core continuously**, on a machine where all 17 Claude
agents together used 59%. The ledger cost more than the agents it was watching.

So an unchanged load slows the next one (2s → 4s → 8s) and anything that moves
resets it to 2s: a changed snapshot, or the user touching the pane. Measured over
a dormant 600s: 76 loads instead of 300.

- **The tick keeps firing at the base interval; only the LOAD backs off.** A
  timer is free and five git processes are not, so resetting the cadence takes
  effect within one tick rather than waiting out the long one.
- **The Git load backs off. The session context does NOT.** It is the account
  pill's only source, `wrapper.sh` writes it *after* the tmux batch that spawns
  the pane, and a mid-session switch rewrites it under a live pane. The first
  draft skipped it on a backed-off tick and broke
  `TestLedgerAccountPillRecoversWhenSessionContextArrivesLate` — see the pill
  section above, which is the same class of bug.
- **`SameContent` must ignore `Generation`.** It counts loads, so including it
  makes every comparison unequal and the backoff dead code. It must NOT ignore
  line counts: a file edited twice stays "modified" while only its numbers move.
- **Both renderers, as always.** `lib/compact-view.sh` carries the same backoff
  (skip 0 → 1 → 3 timeouts, the same 4x ratio). There, `need_build` is the trap:
  nothing on the timer path ever cleared it, so the loop rebuilt on every pass
  and the first draft bought exactly nothing — the skip must clear it.
- **The shell guard is RELATIVE, deliberately.** It runs a dormant and a busy
  repository concurrently and compares their poll counts, because a shell build
  tick is essentially pure fork cost and an absolute polls-per-second assertion
  measures the machine, not the code. Without the backoff the arms are level (12
  vs 11); with it the dormant arm runs at ~0.68 of the busy one.
- **A pty test of `compact_view` exercises the GO renderer** unless
  `WISP_DECK_LEDGER_SHELL_FALLBACK=1` is set — `[ -t 0 ]` makes it native-eligible.
