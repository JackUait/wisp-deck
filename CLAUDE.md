# CLAUDE.md

Project guidance for Claude Code (claude.ai/code) working with this repository.

---

## IMMEDIATE COMPLETION CHECKLIST

**STOP! Before saying "done" or "complete", verify ALL of the following:**

### For ANY Code Change (No Exceptions)

```
[ ] 1. Did I write tests FIRST, watch them FAIL, THEN write code? (IRON RULE)
[ ] 2. Did I run shellcheck on modified scripts? (MANDATORY)
[ ] 3. Did I run final verification with full test suite? (MANDATORY)
[ ] 4. Did I `git push` successfully? (Work NOT complete until push succeeds)
```

**If ANY box is unchecked:** Work is NOT complete. Do it NOW.

**No rationalizations:**
- "Chat is too long, instructions are far down" → INVALID. You're reading them right now.
- "User is in a hurry" → INVALID. Half-done work wastes MORE time later.
- "It's just a small change" → INVALID. Small changes break things too.
- "I'll do it in next session" → INVALID. That leaves work stranded.
- "Tests already cover it" → INVALID. Write test FIRST, watch it FAIL.

### For Session End

```
[ ] 1. All code tested (test first → fail → code → pass)
[ ] 2. shellcheck run on modified scripts
[ ] 3. Full test suite run and passing
[ ] 4. `git push` succeeded
[ ] 5. Issues updated/closed
[ ] 6. `git status` shows "up to date with origin"
```

**Work is DEFINITELY NOT complete if:**
- Changes exist only locally (not pushed)
- shellcheck was never run
- Tests were skipped
- No test was written before code

### Bug Fix IRON RULE

```
[ ] 1. Write regression test FIRST
[ ] 2. Run test → watch it FAIL (proves bug exists)
[ ] 3. Fix bug
[ ] 4. Run test → watch it PASS
[ ] 5. Re-run full test suite
```

**Write code before test?** Delete it. Start over.

### Session End Commands

Run full verification:

```bash
# Run shellcheck on all modified scripts
find lib bin -name '*.sh' -exec shellcheck {} + && shellcheck wrapper.sh

# Run full test suite
./run-tests.sh

# Push changes
git pull --rebase
git push
git status  # MUST show "up to date with origin"
```

**This checklist is ALWAYS executed. NO MATTER how long the chat is.**

### Red Flags - You're About to Violate The Rules

If you catch yourself thinking ANY of these, STOP and DO THE CHECKLIST:

- "Chat is too long, I can't find the instructions"
- "User is in a hurry, I'll skip verification this time"
- "It's just a small change, doesn't need full process"
- "I'll do shellcheck in the next session"
- "Tests already exist, I don't need to write one first"
- "I already manually verified it works"
- "The push can wait, user can do it"
- "Full test suite takes too long"

**ALL of these mean: You're rationalizing. Run the checklist NOW.**

---

## Landing the Plane (Session Completion)

**⚠️ CRITICAL: The completion checklist at the TOP of this file MUST be followed.**

Scroll up to "IMMEDIATE COMPLETION CHECKLIST" and verify ALL items before declaring work done.

**If you're reading this section instead of the checklist:** Go to the TOP of the file.

**Summary (detail is at top):**
1. File issues for remaining work
2. Run quality gates (shellcheck, tests)
3. Full test suite verification
4. Update issue status
5. **PUSH TO REMOTE** (MANDATORY)
6. Clean up
7. Verify `git status` shows "up to date with origin"
8. Hand off context

**Remember:** Every code change needs shellcheck → tests → push. No exceptions.

## Project Overview

Wisp Deck is a terminal + tmux wrapper that launches a three-pane dev session with AI coding tools (Claude Code, OpenCode, Codex), a changeset ledger, and a spare terminal. It runs on Ghostty and handles complete process cleanup when windows close (no zombie processes).

**Key Features:**
- Interactive project selector with TUI
- Multi-AI tool support (Claude Code, OpenCode, Codex)
- Custom status lines showing git info and context usage
- Sound notifications on AI idle
- Auto-cleanup of entire process trees

## Commands

```bash
./run-tests.sh                          # Run full Go test suite
go test ./test/bash/... -run TestFoo    # Run specific test group
go test ./test/bash/... -run "test_name" # Filter by name
go test ./... -v -count=1              # Verbose with no caching
shellcheck lib/*.sh lib/terminals/*.sh bin/wisp-deck wrapper.sh  # Lint all scripts
./bin/wisp-deck                         # Run main installer/setup
make release                            # Create a new release
WISP_DECK_LIVE_CLAUDE_E2E=1 go test ./test/bash/ -run TestLiveClaude -v  # After a claude upgrade: verify the real-claude behaviors draft preservation depends on
WISP_DECK_TMUX_WIDTH_E2E=1 go test ./internal/tui/ -run TestCellWidth_matches_a_live_tmux  # After a tmux/go-runewidth/uniseg bump: re-check the diff pager's width model against a real tmux
WISP_DECK_LIVE_IMAGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveImageEndToEnd -v  # After a codex upgrade: verify a generated image still reports a savedPath that exists, and still round-trips back into Codex (costs one real image generation)
WISP_DECK_LIVE_INJECT_E2E=1 go test ./internal/gptbridge/ -run TestLiveInjectItemsAppends -v  # After a codex upgrade: verify repeated thread/inject_items still APPEND in order, which chunked history injection depends on (costs one short turn)
WISP_DECK_LIVE_SANDBOX_PROSE_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexSandboxProseIsSuppressed -v  # After a codex upgrade: verify the include_* config keys still stop Codex describing its read-only sandbox to the model, which is what makes a bridged pane refuse to edit (costs one short turn)
WISP_DECK_LIVE_CODEX_COLD_E2E=1 go test ./internal/codexadapter/ -run TestLiveColdAppServerIsObserved -v  # After a codex upgrade: verify a real app-server still binds its socket and completes the observer handshake inside defaultCodexStartupTimeout (copies the vendored binary to a fresh inode so the exec goes cold, and logs the measured start)
```

### Reading a red CI run

Every workflow job pipes `go test -json` to a file and hands it to `cmd/ci-report`,
which names each failing test, replays its output, annotates the source line, and
writes the GitHub step summary. So a failed job's **last step is the whole story** —
no need to scroll the raw JSON stream. It also catches what a `grep '"Action":"fail"'`
misses: build failures with no test, toolchain errors outside the JSON stream, and a
run that produced no output at all (which used to be reported as success).

Render any local run the same way:

```bash
go test -json ./... 2>&1 | tee out.json >/dev/null
go run ./cmd/ci-report --title "Full suite" out.json
```

### Creating Releases

**EVERY release MUST include fresh `wisp-deck-tui` binaries. NO EXCEPTIONS.**
- The installer downloads binaries from release assets — missing binaries = 404 on install
- The developer's local binary MUST be rebuilt — stale local binary = developer sees old UI
- **NEVER create releases manually via GitHub UI or bare `gh release create`**
- **ALWAYS use `make release`** — it handles binaries (GitHub + local), tagging, and release creation

Run `make release` to automate the full release process. Before running:

1. Bump the version in `VERSION` (semver format: `X.Y.Z`)
2. Commit and push all changes — working tree must be clean
3. Must be on `main` branch
4. `gh` CLI must be installed and authenticated (`brew install gh && gh auth login`)

The script will:
- Run preflight checks (clean tree, main branch, valid version, tag doesn't exist, gh auth, **install verification**)
- Show a confirmation prompt (skip with `--yes` flag)
- Build `wisp-deck-tui` binaries for darwin/arm64 and darwin/amd64
- Create annotated git tag `vX.Y.Z` and push
- Create a GitHub release with binaries attached as assets
- Rebuild the local `~/.local/bin/wisp-deck-tui` binary so the developer sees changes immediately

```bash
make release              # Interactive (with confirmation prompt)
bash scripts/release.sh --yes  # Non-interactive (skip confirmation)
```

**Gotcha (binary warm-up):** the FIRST exec of a freshly built, downloaded, or re-signed `wisp-deck-tui` pays a macOS Gatekeeper/XProtect assessment (~1s idle, multi-second under load). Both the file-list diff modal and the account switcher exec this binary, so a cold binary makes the first modal open stall. **Every code path that writes or re-signs `~/.local/bin/wisp-deck-tui` MUST exec it once afterwards** (`"$bin" --version >/dev/null 2>&1 || true`). Existing warm-up sites: `wrapper.sh` (session launch, via `warm_tui_binary` in `lib/tui.sh`), `scripts/release.sh`, `lib/install.sh` (`ensure_wisp_deck_tui`), and the Makefile `install` target — all guarded by `test/bash/tui_warm_test.go`.

**Gotcha (packaging is invisible locally):** the dev machine runs wisp-deck from symlinks into the repo, so a file the installer needs but `package.json`'s `files` never publishes still resolves here — and breaks for every `npx` user. This shipped in v2.22.0: `bin/wisp-deck` symlinked `~/.local/bin/wisp-deck` at the unpublished `bin/wisp-deck-config`, `ln -sf` made a dangling link, and setup reported success while the command was dead. **Whenever the installer references a new path, add it to BOTH `copyDistribution` in `bin/npx-wisp-deck.js` AND `files` in `package.json`.** Guarded by `test/npx/` (packaging cross-check + a full install into an empty HOME), which `scripts/release.sh` runs in preflight and the `Install verification` workflow runs in CI. Because the launcher skips the copy when `.version` already matches, a packaging fix only reaches users on a **version bump**.

**Gotcha:** `gh release create FILE#LABEL` uses the file's **basename** as the download name (not the label). If you build to a mktemp path, users get assets named `tmp.XXXX`. The release script builds to a temp directory with correct filenames to avoid this.

**Post-release verification (MANDATORY):**
```bash
# Verify binaries are downloadable (users get 404 if this fails)
gh release view v$(cat VERSION) --json assets --jq '.assets[].name'
# Must show: wisp-deck-tui-darwin-arm64, wisp-deck-tui-darwin-amd64
```

## Architecture

Wisp Deck uses a **hybrid architecture** combining Go for interactive TUI and bash for orchestration.

**Layer 1: Go TUI Binary (`wisp-deck-tui`)**
- Interactive terminal UI components built with Bubbletea
- Project selector, AI tool selector, terminal selector, settings menu, input forms
- Outputs structured JSON for bash consumption
- Binary: `~/.local/bin/wisp-deck-tui`
- Source: `cmd/wisp-deck-tui/`, `internal/tui/`, `internal/models/`, `internal/util/`

**Layer 2: Bash Orchestration**

**Entry Points:**
- `bin/wisp-deck` - Main installer script, sources all modules
- `wrapper.sh` - Terminal-agnostic runtime wrapper

**Module System:**
All reusable functionality lives in `lib/` as sourced shell scripts:

- **tui.sh**: Terminal UI helpers (header, success, error, info, warn)
- **install.sh**: Package installation (Homebrew, casks, commands)
- **ai-tools.sh**: AI tool detection and management
- **ai-select-tui.sh**: Interactive AI tool selection wrapper (Go TUI)
- **projects.sh**: Project file parsing and validation
- **project-actions.sh**: Add/delete project operations
- **project-actions-tui.sh**: Interactive project input wrapper (Go TUI)
- **menu-tui.sh**: Project selection wrapper (Go TUI)
- **settings-menu-tui.sh**: Settings menu wrapper (Go TUI)
- **terminals/ghostty.sh**: Ghostty terminal adapter (the only supported terminal)
- **tmux-session.sh**: tmux session creation and pane setup
- **process.sh**: Process tree management and cleanup
- **statusline.sh**: Status line generation logic
- **statusline-setup.sh**: Claude Code status line installation
- **notification-setup.sh**: Sound notification setup
- **settings-json.sh**: JSON manipulation for settings files
- **input.sh**: User input helpers with validation
- **update.sh**: Self-update functionality

**Data Files:**
- `~/.config/wisp-deck/projects` - Project list (name:path format)
- `~/.config/wisp-deck/ai-tool` - Default AI tool preference
- `~/.config/wisp-deck/*-features.json` - AI tool feature flags
- `~/.claude/settings.json` - Claude Code settings
- `~/.config/ghostty/config` - Ghostty terminal config

**Process Hierarchy:**
```
Terminal window (Ghostty)
└─ wrapper.sh (shell command)
   └─ tmux session
      ├─ AI tool (Claude Code/OpenCode)
      ├─ changeset ledger
      └─ spare shell
```

On window close, wrapper recursively kills entire process tree.

### The launch critical path (NEVER block it)

Everything `wrapper.sh` runs **before `select_project_interactive`** is the
launch critical path: the user is staring at the loading splash for exactly
that long. Budget is **~130ms**. The splash is there to make the wait pleasant,
**not** to give you room to do work behind it.

**The rule: no blocking subprocess before the picker.** No `npx`, `npm`, `node`,
`curl`, `brew` — nothing that touches the network or boots a language runtime.

This has already shipped once. `resolve_opencode_cmd` ran
`npx --no-install opencode-ai --version` to decide *which* npx string could
launch OpenCode — eagerly, every launch, for every tool. It cost **3s warm,
6-13s under load**, and every Claude user paid it in full to compute a string
that was then thrown away. It hid behind the splash for releases.

When you need an expensive answer on this path, do one of:

1. **Answer it without the subprocess.** Usually the expensive call is deciding
   more than you need. `opencode_available()` (`lib/ai-tools.sh`) is the
   pattern: a PATH check answers "can we run it?" for free, and the probe that
   answers "*how* do we run it?" is deferred to the one branch that launches it.
2. **Move it after the picker**, to the branch that actually needs it.
3. **Background and disown it**, like `check_for_update` — spawning is fine,
   *blocking* is the defect.

Guarded by `test/bash/launch_critical_path_test.go`, which mocks every expensive
command to sleep 20s and fails if the picker takes over 5s. It tests the
property, not the instance, so it catches whatever the next offender turns out
to be — and it names the command in the failure. `opencode_availability_test.go`
adds a static guard against reintroducing that specific probe.

### The post-pick path and the tmux launch chain (same discipline)

The stretch from project selection to `attach-session` is critical path too:
the splash is stopped and the panes don't exist yet, so every synchronous
spawn here is dead screen time. Three launch invariants, each with a guard:

1. **No blocking subprocess between pick and tmux.** One python3 spawn (the
   Claude launch-settings overlay) is the allowed exception; migrations must
   gate themselves behind a cheap bash check (see
   `remove_waiting_indicator_hooks`'s grep fast path), and capability probes
   must cache by binary signature (see `gt_claude_filter_prefix`,
   `gt_ledger_native_capable`). Guarded by
   `test/bash/launch_post_pick_path_test.go` (property test, names the
   offender).
2. **No foreground `run-shell` inside the launch chain.** tmux runs the
   chain's commands in order; a foreground `run-shell` holds the splits and
   the attach hostage for as long as its script runs (the ledger-hover
   install once blocked on a server-wide 15s lock). Use `run-shell -b` —
   formats like `#{pane_id}` still expand at the command's position in the
   chain. Guarded by `test/bash/ledger_hover_nonblocking_test.go`.
3. **A tab must never end up attached to a broken layout.** tmux executes the
   rest of the chain even when a `split-window` fails (this shipped: a
   pre-resize tiny pty made both splits fail and the tab sat on a lone
   full-width ledger forever). The session size comes from `_sane_term_size`
   (never raw `_detect_term_size` — guarded by
   `test/bash/sane_term_size_test.go`), and `gt_ensure_panes_watch`
   (backgrounded around the launch) rebuilds whatever panes are missing once
   the window has real space — guarded by `test/bash/pane_heal_test.go`.

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

### A large tab chip renders state the agent owns, so the bar sanitizes it

The tab bar has two chip modes (`tab_bar` in the settings file). `compact` is
the original numbered chip; `large` — the **default** — adds the tab's own title
and the elapsed time of the turn running in it. Absent key, absent file and
unknown value all resolve to large, in `tab_view_mode` AND in the Go menu's
`SetTabBar`: disagree and a session opens on a bar the Settings row denies is
selected.

A chip's title is the agent pane's `pane_title`, which is where Claude stamps
its summary of the current turn — so the bar renders a string the model wrote.

- **tmux parses `#[...]` out of the EXPANDED format**, so a turn titled
  `#[fg=red]` repaints the rest of the bar in a colour of the model's choosing.
  Verified live on 3.6a. `#{q:...}` does **not** escape it (only
  `#{s/#/…/:...}` does), so `tab_view_chip_title` deletes every `#` in bash,
  where it is testable. A comma is harmless — a value substituted into
  `#{W:...}` is not re-parsed for the iterator's separator — but a style comma
  written literally in the format still needs `#,`.
- **The ellipsis is what separates a running turn from a finished one.** Claude
  prints `✽ Hyperspacing… (8m 25s · ↓ 16.9k tokens)` while working and replaces
  it in place with `✻ Cooked for 1h 38m 25s` when the turn ends. Matching the
  summary makes a tab claim to be working forever.
- **The inventory is unit-separated (`\037`), never tab-separated.** A tab is an
  IFS *whitespace* character, so bash collapses runs of them: a window with two
  empty fields in a row shifts every later field left, and the pane title lands
  in the wrong variable. This shipped in the first draft of the stamping pass.
- **Only write a value that moved.** `set-option` repaints the client, and this
  pass runs twice a second in every open session on the machine.
- **Both status-left writes take the same mode.** The launch chain writes the
  bar once when it builds the session and again when it realigns to the agent
  pane; disagreeing makes the bar change shape a moment after the tab opens.
- **A card's fill breaks wherever `#[default]` appears.** A large chip is a
  filled card — a rounded cap at each end and one background carried from the
  number through the title to the progress — and `#[default]` restores the
  bar's base style, so reaching for it anywhere between the caps punches a hole
  in that card's own background. The first draft did exactly that after the
  number badge. Every segment between the caps, *including both branches of the
  progress conditional*, carries `bg=` explicitly, and the only `#[default]` in
  a chip sits immediately after the closing cap — which is what
  `TestTabViewStatusLeft_a_cards_fill_is_unbroken` asserts by counting.
- **The caps are octal escapes, not literal glyphs.** They are powerline
  codepoints (U+E0B6/U+E0B4) in the Private Use Area: Ghostty draws them itself
  so no Nerd Font is involved, and tmux accounts each as one cell (probed
  against a live client with `#{pN:}` padding before the format was built). They
  are written `$'\356\202\266'` because `/bin/bash` here is 3.2 — no
  `$'\u'` — and because a literal PUA byte does not reliably survive editing.
- **Card chrome is part of what a chip spends.** `tab_view_title_budget`
  reserves the caps and padding (18 per card, 22 fixed for the label and the
  [+] card). Widening the chrome without widening the reserve does not shrink
  titles — it pushes the right-hand tabs past the window edge, where tmux
  clips them out of existence.

Per-window title and progress are state that becomes valid *later* — a window is
created before its agent has named anything — so, exactly like the ledger's
account pill above, they are re-resolved on every watcher tick rather than
loaded once. Guarded by `test/bash/tab_view_large_test.go` (the pure
title/progress/budget functions against verbatim captures from live panes, the
stamping pass against a spy tmux, the watcher wiring and both launch modes) and
`internal/tui/tab_bar_setting_test.go`.

### The switcher card belongs to the ledger, and a popup's `-y` is its bottom edge

The Switch card is the account pill's menu — the pill in the ledger's footer is
the only thing that opens it — so `open_account_switcher` centers it in the
**ledger pane's** rectangle. It has been centered on the agent pane and on the
whole window before; both float it out in the transcript, detached from the
affordance the user just clicked. Fallbacks are a ledger too narrow for the card
or no ledger pane at all → the window's center (tmux **clips** a popup at the
pane edge, it never shrinks one, so a clipped card is worse than a detached one),
then measure-unavailable → the full-window dimmed-backdrop popup.

Placing it means converting between three coordinate systems that all look alike:

- **`-y` is the popup's BOTTOM boundary; `-x` is its LEFT edge.** The man page
  gives it away by defining position `P` as "the bottom **left** of the pane".
  Handing tmux the wanted top row hoists the card a full card-height too high,
  and tmux then **clamps** it flat against the top of the terminal — so the
  mistake reads as "the popup ignores its position", not as a near miss. Pass
  `top + height`.
- **A popup is positioned in CLIENT rows; panes live in WINDOW rows.** A top
  status line offsets the two by its height (`#{status}` lines when
  `#{status-position}` is `top`), and a popup at `-y 0` paints over that status
  line. Columns need no correction — a status line steals rows, not columns.
- **`_session_side_panes` prints `"<ledger> <spare>"`, so `read -r a b` is a
  trap.** With no ledger the leading field is EMPTY and `read` collapses it,
  handing the spare pane's id over as the ledger and centering the card on the
  wrong pane. Split by prefix (`${side_panes%% *}`).

Guarded by `TestOpenAccountSwitcher_centers_the_card_in_the_ledger_pane`,
`_offsets_the_card_past_a_top_status_line`,
`_falls_back_to_the_window_when_the_card_outgrows_the_ledger`,
`_centers_the_card_in_the_window` (the no-ledger case, which is also the
`read`-collapse regression guard) and `_window_fallback_when_measure_fails`.
To re-check the anchoring against a real tmux, view an attached client through an
outer tmux pane — `capture-pane` on the outer pane renders the inner client's
screen *including* the popup, which is otherwise uncapturable.

### A parked Claude turn runs outside the session, and the session's status freezes

The 🔔 on a tab is `phase=attention` in the attention state file, and for Claude
that phase has ONE source: the `status` field of the account-local record Claude
writes at `<config-dir>/sessions/<pid>.json`. An `idle` following a `busy` means
the turn ended — it rings the bell and plays the sound.

Claude 2.1.220 can **park** a turn: it hands the work to a background job process
claimed from its own daemon pool and stamps `parkedJobId` on the interactive
record. Two consequences, and both broke the bell:

- The job process is a child of the daemon, **never** a descendant of the
  supervised launch root, so `ClaudeRegistryMapper`'s tree-scoped discovery
  cannot reach the work at all.
- Parking writes only `parkedJobId` and unparking only clears it — **neither ever
  touches `status`**. The interactive record's `status` therefore freezes at
  whatever it held when the turn moved away, and stays frozen for every turn the
  job then runs.

Read literally, that reports a working agent as `idle`: the turn is declared
done, the bell rings, and then — because attention is sticky and only a `busy`
observation clears it — nothing ever clears it. This shipped; a session sat at 🔔
through 40+ minutes of continuous work while its record read
`status:"idle", statusUpdatedAt:<40 minutes ago>, parkedJobId:"…"`.

So `resolveParkedJob` follows `parkedJobId` to the `kind:"bg"` record carrying
that `jobId` and takes the status from there. That record is outside the tree, so
it is trusted no further than a launch-tree one: it must name the job, and it
must describe its own live process (PID present in the point-in-time `ps` table,
with a matching `procStart`), so a leftover file for a recycled PID can never
speak for the session. Ambiguity or an unresolvable job is uncertainty
(`found=false`), never a guess.

**Follow the job — don't just suppress the false `idle`.** Parking persists
across turns, so a session that merely ignored a parked `idle` would never ring
when the work actually finished, trading a false bell for no bell at all.

Guarded by `TestClaudeRegistryMapperReportsParkedSessionAsTheJobRunningItsTurn`
and its neighbours in `internal/attention/claude_registry_test.go`.

### A cleared chat retires the attention it threw away

Attention is sticky by design: once `phase=attention` is published, only a `busy`
observation clears it. Clearing the chat (`/new`, `/clear`, picking another
conversation with `/resume`) never produces one — the turn the 🔔 (or the 👀 it
decayed into) was raised about is simply gone, and the tab keeps ringing at a
conversation that no longer exists.

The only thing that moves is the registry record's **`sessionId`**: Claude
replaces it the moment the chat is cleared, while `status` stays `idle`. So
`ClaudeRegistryMapper` reports the conversation alongside the status, and
`ClaudeReducer.observeConversation` retires every pending request — question,
permission, error, and the sticky attention phase itself — when it changes.

- **The interactive session owns the conversation.** A parked session's job
  record speaks for the status only (see above); `SessionID` always comes from
  the launch-tree record.
- **An empty conversation is not a change.** A failed registry read, or a Claude
  too old to report `sessionId`, must leave attention alone — reading absence as
  a change would silence a real request on the next transient miss.
- **Arming survives the change.** A conversation replaced mid-turn is a fork or a
  compaction, not a user clear, and the work still running still owes the user
  its completion bell — so only pending attention is cleared, never `armed`.
- **A present `sessionId` is validated like a job id.** It decides whether
  attention is retired, so a non-string, control-character or oversized value
  rejects the record rather than being guessed at.

Guarded by `TestClaudeReducerClearedConversationRetiresAttention`,
`TestClaudeReducerKeepsAttentionWithoutAConversationChange`,
`TestClaudeReducerClearedConversationKeepsTheRunningTurnArmed`,
`TestClaudeRegistryMapperReportsTheSessionsCurrentConversation`, and
`TestClaudeRegistryObservation_carries_the_conversation`.

### A tab follows its agent's working directory, not the worktrees it creates

An agent that enters a git worktree used to leave the rest of the tab behind:
the ledger kept diffing the checkout nobody was working in, the spare terminal
sat in it, and a crash-restore reopened it. The tab now follows.

**The signal is the agent's cwd, never `git worktree list`.** A bare
`git worktree add` does not move the agent — following it would detach the
ledger from where the agent actually works — and subagent worktrees and this
repo's own test suite create worktrees constantly, so a list-poll would yank the
ledger into a temp checkout mid-test-run. Claude's account-local registry record
carries `cwd`, and it moves in BOTH directions (`EnterWorktree` and
`ExitWorktree`), so one field drives the follow and the snap back out. This is
Claude-only by construction, not by omission: no other agent moves its own
working directory.

- **No second discovery path.** `ClaudeRegistryMapper` already finds and
  validates that record every poll (live PID, `procStart`, launch-tree scope),
  so it reports the cwd and `claude-attention` publishes it to
  `<generation>/cwd`. It is a **sidecar, not a sixth field** of the attention
  state: that record is a fixed five-field versioned protocol every consumer
  pins, and a location is not a semantic phase. Like `SessionID`, the cwd always
  comes from the interactive record — a parked session's job record speaks for
  the status only.
- **The agent pane is never respawned.** `_apply_worktree_switch` relaunches it
  fresh, which is right for the switcher's own row (the user chose it) and
  destructive here: the conversation running in that pane is the one that just
  created the worktree. Its non-agent half is `_retarget_session_context` +
  `_retarget_session_side_panes`, and the follow calls only those.
- **`ExitWorktree` removes the worktree as it leaves,** so the tab is asked to
  follow home FROM a checkout that no longer exists — and `git worktree list`
  from a deleted directory reports nothing, refusing the snap-back and stranding
  the tab on a dead path forever. A dead anchor re-roots at the closest surviving
  ancestor; a Claude worktree lives at `<main>/.claude/worktrees/<name>`, so that
  reaches the repository that owned it while any other repository still fails.
- **The steady state does not fork.** Both watcher reads are builtins, and only a
  directory differing from the session's own reaches the shell that validates it.
  Each distinct directory is attempted **once** — a refused one (the agent cd'd
  out of the project) would otherwise spawn a shell twice a second — and
  convergence clears the memo, so re-entering a worktree still follows.
- **That shell is a fresh `bash`.** The wrapper may run under `bash --posix`
  (Ghostty's `/bin/sh -c` launch), where the process substitution
  `_session_worktrees` reads git through is disabled.
- **A session that exits while still inside a worktree cannot snap back** — the
  agent is gone — so the restore snapshot records the worktree path, and a
  removed one restores nothing.

Guarded by `test/bash/worktree_follow_test.go` (including
`_never_respawns_the_agent_pane`, `_follows_home_after_the_worktree_is_removed`
and `_never_respawns_the_spare_as_a_ledger` — the last a `read -r ledger spare`
field-collapse this extraction fixed), `test/bash/worktree_follow_watcher_test.go`
(`TestAttentionWatcherTick_follows_the_agent_into_a_worktree` is what pins the
tick to the follow at all), `test/bash/worktree_follow_wiring_test.go`, and
`TestClaudeRegistryMapperReportsTheSessionsWorkingDirectory` plus
`TestWorkingDirectoryWriterPublishesBesideTheAttentionState`.

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

Guarded by `internal/tui/mainmenu_worktree_refresh_test.go` — including
`_picksUpAWorktreeGitCreatedAfterTheMenuOpened`, which drives the real loop
against a real repo, and `TestAppModel_deliversAWorktreeRefreshToTheMenuUnderAPushedScreen` —
plus `TestDetectWorktreesFor_*` in `test/internal/models/worktree_refresh_test.go`.

### A self-hosted subscription supplies what the catalog cannot

The `custom` provider is a subscription whose endpoint and model belong to the
user, not to a vendor. Everything the catalog answers for a gateway — base URL,
model ids, context window, price — has no answer here, so the Subscription modal
offers three text fields (Endpoint, Model, Context) where a gateway gets the
alias cycler. The cycler is not merely unhelpful there: `cycleSubscriptionMapping`
returns early on an empty model list, so those rows are inert.

- **It is LAST in `Providers`, and must stay there.** `Providers[0]` is the
  fallback for every profile name matching no alias, so a user-configured
  provider in that slot would claim every stray config on the machine.
- **The save path must skip `WriteModelMappings`.** That function writes the four
  aliases from the draft's model list, which is empty for this provider — so
  running it deletes the model the user just typed, on every save. The regression
  guard is `TestSubscriptionModal_savingACustomProfileKeepsItsModelMapping`.
- **The window is entered, never inferred.** `ContextBudget` cannot size a model
  the catalog has never heard of, and `stampContextBudget` deliberately keeps
  whatever the profile already had in that case — which is exactly what lets a
  hand-typed figure survive `WriteModelMappings` and the `ensure-budget` sweep
  `bin/wisp-deck` runs on every install. It must equal the endpoint's real limit;
  overshooting it is the unrecoverable wedge described above.
- **Every alias names the one model.** `/model` and subagents move freely across
  all four, so a partially mapped profile launches some tiers with no model at
  all. An absent default is written as no key rather than an empty string: a
  blank `ANTHROPIC_DEFAULT_OPUS_MODEL` reaches Claude Code as a real, broken id.
- **A key is still required.** `ConfigReady` gates on it, and these endpoints
  routinely sit on public URLs (a `proxy.runpod.net` host is reachable by anyone
  who guesses it).
- **`MirrorOpenCode` stays off.** OpenCode's catalog cannot size a model nobody
  has published, and `Sync` skips non-mirrored providers outright.

The endpoint has to speak the **Anthropic Messages API** — `/v1/messages`,
streaming, `tool_use`. An OpenAI-compatible server (vLLM, SGLang, Ollama) needs a
translating proxy in front of it; without tool calls Claude Code cannot work at
all.

Guarded by `internal/claudeconfig/custom_provider_test.go` and
`internal/tui/subscription_modal_custom_test.go`.

### A self-hosted pane disarms the byte stall watchdog, because nothing pings it

Claude Code arms a raw-byte stall watchdog on every `text/event-stream` body it
receives. It measures bytes on the wire, not events: 20s of silence paints
`Waiting for API response · will retry in <N> · check your network`, and at the
end of the budget it aborts the stream and replays the whole turn.

Its premise is that a healthy stream is never silent, which holds for Anthropic
and for the vendor gateways, because they forward Anthropic's own `event: ping`.
An endpoint the user supplies promises nothing of the sort — a self-hosted model
prefilling a large prompt sends no bytes at all until its first output token. So
on a `custom` profile the watchdog reports a working model as a broken network,
then kills the turn it was working on.

Decoded from 2.1.247 and then measured against a live pane, because the decode
alone hides the second half:

- **The 20s trigger is a hardcoded interval**, not a budget. No env var moves it,
  and `CLAUDE_ENABLE_STREAM_WATCHDOG` does not cover it — that flag gates the
  abort timers only, while the banner timer is armed either way. The single
  lever is `CLAUDE_ENABLE_BYTE_WATCHDOG`: falsy means the instrumented body is
  never installed, so the banner has nothing to read and cannot fire.
- **A custom base URL is still `firstParty`.** `getAPIProvider()` answers from
  the `CLAUDE_CODE_USE_*` flags alone and never looks at `ANTHROPIC_BASE_URL`,
  so a self-hosted pane draws the **180s** first-party abort budget rather than
  the 300s one — measured, not inferred: the live banner counted down from
  `2m 37s` at the 20s tick.
- **It is not a cosmetic banner.** A mock endpoint byte-silent for 200s was
  aborted at 180s and re-dispatched; the replay is the same prompt, so it takes
  the same time and is killed again — a self-hosted endpoint slower than 180s to
  first token can never complete a turn. The same profile carrying the key ran
  that turn to completion in one attempt, with no banner.
- **Only a user-configured profile is disarmed.** A gateway heartbeats, so
  disarming it there trades a real dead-connection signal for nothing.
- **The provider comes from the `WISP_DECK_SUBSCRIPTION_PROVIDER` marker**, never
  from the filename: an unmatched name resolves to `Providers[0]`, so
  "qwen.json" would read as Zhipu.
- **A declared value is never overwritten,** exactly like `stampContextBudget` —
  the user may have armed it on an endpoint that does keep its stream warm, and
  every launch path may run the sweep.
- **The installer copies a default profile only when the file is absent**, so a
  profile written before this was declared is reachable only by a sweep:
  `claude-config ensure-watchdog`, which `bin/wisp-deck` runs beside
  `ensure-budget`. The modal's own custom-field save runs it too, so a profile
  self-heals the moment the user edits it.

This is the same failure the GPT bridge answers with `event: ping` every 10s —
there wisp-deck owns the server and can keep the socket warm, here it owns only
the profile. What still stands for a self-hosted pane is the separate *event*
watchdog, which aborts a turn after `CLAUDE_STREAM_IDLE_TIMEOUT_MS` (minimum and
default 300s) with no SSE events.

Guarded by `internal/claudeconfig/bytewatchdog_test.go`,
`TestSubscriptionModal_savingACustomProfileDisarmsTheByteWatchdog`, and
`test/bash/byte_watchdog_sweep_test.go`.

### A text-only model is declared by the profile, because Claude Code has no flag for it

Claude Code sends images to whatever `ANTHROPIC_BASE_URL` points at. Decoding
2.1.247 turns up **no** lever to stop it: there is no `supportsVision` on a
model, no vision capability consulted before a block is built, and no env var —
`imageLimits` (from the catalog's `image_limits`) only resizes, and
`CLAUDE_CODE_DISABLE_ATTACHMENTS` governs system-prompt sections, not content.
`appendSystemPrompt` is a managed-settings/SDK key that a user settings file
ignores (verified live: a codeword placed there never reached the model).

A text-only endpoint answers an image with a hard failure — vLLM's
`At most 0 image(s) may be provided in one prompt (parameter=image)`, surfaced
as a 500 that kills the turn, and a subagent with it. So the Subscription
modal's **Images** row (user-configured providers only) writes the one thing a
settings profile can enforce: `permissions.deny` rules on the Reads that produce
a non-text block.

- **`Read(//**/*.png)` — the `//` is load-bearing.** A Read rule's path is
  gitignore-style and relative to the project without it, and the images that
  reach a model live outside the project: a screenshot directory, the Desktop, a
  temp path. Verified against a live pane, not the decode — a rooted rule denies
  `/tmp`, an absolute scratch path and an uppercase `.PNG` alike, **in
  `bypassPermissions` mode**, and a Task subagent inherits the denial.
- **A PDF belongs in the list.** Read returns it as a `document` block (and, on
  the per-page fallback, as image blocks) — just as unsendable. An SVG does
  not: git tracks it as text and Claude Code reads it as text, so denying it
  removes a working capability instead of preventing a failure.
- **This is never stamped for the user**, unlike `stampByteWatchdog` and
  `stampContextBudget`. A self-hosted endpoint may serve a model that sees
  images perfectly well, so there is no sweep and no `EnsureAll` — only the
  toggle, default off.
- **The state is the rules themselves,** with no `WISP_DECK_*` marker to drift.
  *Any* owned rule reads as on, so a profile stamped by an older, shorter list
  still shows the toggle set and is filled in on the next write; turning it off
  removes only the owned strings, never a deny rule the user wrote by hand.
- **The launch overlay is what delivers it.** `write_claude_launch_settings`
  copies the whole settings object, so `permissions` travels untouched — and
  `_apply_subscription_switch` regenerates the overlay, so a mid-session switch
  picks the toggle up too.

Known limits, by construction: this stops the model reading an image. It cannot
stop an image the *user* puts in the prompt — a paste, a drag, or an `@`-mentioned
path Claude Code inlines — nor an MCP tool that returns one. `.ipynb` is
deliberately absent from the list for the same reason as SVG in reverse: only a
cell whose *output* is an image would break the turn, and denying every notebook
read costs far more than that. Wisp's own screenshot-inject binding could
consult the toggle; it does not yet.

Guarded by `internal/claudeconfig/imagereads_test.go`,
`internal/tui/subscription_modal_images_test.go`, and
`test/bash/images_blocked_launch_test.go` (the overlay end of the chain, which
goes red if the overlay is ever narrowed to an allowlist of keys).

### Restore-queue pops are authorized, never ambient

An interactive launch may consume a restore-queue entry only through
`restore_pop_authorized`: it built the queue, it holds the one-shot chain
ticket its spawner issued via `restore_advance`, or it launched inside the
crash-storm grace window of the queue build. Popping without authorization is
the wrong-tab bug: a user's fresh Cmd+T tab silently restores another
project while their intended session opens elsewhere. Never add a
`restore_queue_pop` call site that skips the gate. Guarded by
`test/bash/restore_chain_ticket_test.go`.

Every Wisp session that can run Codex owns a private durable identity path at
`session-identities/<session>.codex`, stamped as
`WISP_DECK_CODEX_SESSION_FILE`. The semantic adapter must persist its exact
current root UUID there, including later `/new` transitions; snapshots,
restore queues, and tool switches must prefer that sidecar over stale tmux
stamps and cwd/rollout guessing. Observer loss before the first identity and
identity-write failure are fatal rather than silently leaving a live,
unrestoreable chat. A restored Codex tab may launch an exact
`codex resume <uuid>` or the interactive `codex resume` selector, but it must
never fall back to plain Codex: a plain launch silently replaces the lost
conversation with an empty one. Guarded by
`test/bash/codex_crash_restore_test.go` and the Codex supervisor tests.

### A Codex conversation is spread over many rollout files, and they replay each other

Claude keeps a conversation in one transcript, so per-file dedup is enough for it.
Codex does not. Forking a subagent, resuming a thread, and compacting each open a
**fresh** rollout file that begins by replaying the ancestor's entire
`token_count` history, re-stamped with the load instant. One real month held 1,684
rollout files that were only **43 session chains** — a single chain spanned 448
files — and summing every `last_token_usage` reported **466B tokens / $309K**
against **14.9B / ~$10.5K** actually spent. A **31x** over-count, shipped and
visible in the Stats tab.

A replayed record is byte-identical to a live one, so nothing about a single line
identifies it. What separates them is **density**: a replay dumps hundreds of
events inside one second, while a real request round-trip takes seconds. So
`ParseCodexRollout` drops every event in a second holding more than
`codexReplayBurstPerSecond` events, and collapses a verbatim re-emission of the
request it just counted (Codex repeats one). Measured against the full corpus,
those two per-file rules reproduce the cross-file **deduplicated** truth to three
decimal places while keeping **100%** of distinct requests — which is what lets
dedup stay per-file and keeps the incremental `Aggregate` cache intact.

Consequences to respect:

- **Never restore a plain "sum every `last_token_usage`" reading**, and never
  reach for `total_token_usage`: it is cumulative *including* the replays, so a
  forked rollout's final value hit 2.42B for one thread.
- **Changing this parser requires bumping `cacheVersion`** (`internal/usage/cache.go`).
  The per-file cache *and* the append-only journal both store parsed months
  tagged with a parser version, and only a higher version supersedes them —
  without the bump the old inflated numbers are simply reloaded.
- Guarded by the `TestParseCodexRollout_dropsReplayedHistoryBurst`,
  `_collapsesConsecutiveDuplicateTokenCounts`, and `_keepsBurstFreeRapidRequests`
  tests in `internal/usage/codex_test.go` — the last one is the counterweight, so
  a future tightening cannot start eating genuinely busy seconds.

### A request's `tools[]` holds two different kinds of tool

The GPT bridge originally modelled every entry of an Anthropic request's
`tools[]` as a Claude-hosted function and required each to carry an
`input_schema`. Anthropic's **server tools** break that assumption: they are
identified by a `type` (`web_search_20250305`, `code_execution_*`, …), they run
on Anthropic's side, and they therefore have **no `input_schema` at all**.

That mismatch shipped. Claude Code's WebSearch tool does not search by itself —
it issues a *nested* Messages request whose entire tools array is the one server
tool:

```js
tools:      [{type: "web_search_20250305", name: "web_search", max_uses: 8}]
tool_choice: {type: "tool", name: "web_search"}
```

The bridge rejected it, so **every WebSearch in a GPT-backed session** came back
as `API Error: 400 tools[0]: input_schema must be an object`. The `tools[0]` is
the tell: the main loop's array starts with a real schema'd tool, so an index-0
schema complaint can only be that single-server-tool request.

The rules that keep this working:

- **Partition before validating.** `Tool.IsServerTool()` (empty or `"custom"`
  `type` means Claude-hosted) decides which entries get schema-checked and
  turned into `dynamicTools`. A server tool must never reach Codex as a
  host-provided function — Codex would try to call a function nobody hosts.
- **`tool_choice` can name a server tool.** Resolving `{"type":"tool"}` against
  the Claude-hosted tools alone reported `tool_choice names unknown tool
  "web_search"` — a *second*, distinct 400 hiding behind the first. A forced
  server tool is already satisfied by the thread's own capability, so it yields
  no dynamic tool and no directive. Naming a tool that was never supplied at
  all is still an error.
- **Never silently drop a server tool.** Dropping it lets the model answer
  "from the web" out of its own memory, which is worse than an error. Only
  `web_search*` is supported; anything else fails by name.
- **Web search is answered by Codex's own search**, enabled per-thread via
  `config.web_search`. The app-server's variants are
  `disabled | cached | indexed | live` (verified against a live app-server —
  `enabled`/`auto` are rejected); the bridge asks for `live` and keeps
  `disabled` for every other turn.
- **Codex reports it as a `webSearch` thread item**, which must pass
  `rejectCodexOwnedItem` *only* on turns that asked for it. It stays forbidden
  otherwise, so the guard still catches a genuine capability leak.
- **Domain filters bind in the instructions or nowhere.** `config.web_search`
  accepts only the mode string (an object with `allowed_domains` is rejected),
  so Claude Code's `allowed_domains`/`blocked_domains` are stated to the model
  that runs the search. Dropping them would answer a scoped search with
  unscoped results — which looks like a working search.
- **Prose carries the findings; protocol blocks carry the accounting.** Claude
  Code accepts text commentary as a WebSearch result, but it computes the
  displayed search count from `server_tool_use` / `web_search_tool_result`
  blocks. Reducing Codex's `webSearch` items to prose alone made every successful
  search say `Did 0 searches`. Preserve each item as a paired server-use/result
  block, retain the prose with its inline source URLs, and report
  `usage.server_tool_use.web_search_requests`. A real app-server
  `item/started` can have an empty query while `item/completed` supplies it, so
  emit the pair on completion; if completion still has no display detail (for
  example an `other` action), use a neutral fallback rather than rejecting the
  valid item. Accept these emitted blocks when Claude
  replays assistant history, then omit their lifecycle metadata when injecting
  Codex history because the adjacent prose carries the findings.

Guarded by `TestTranslateAcceptsAnthropicWebSearchServerTool` and its
neighbours in `translate_test.go`, `TestEngineEnablesCodexWebSearchOnlyWhenRequested`,
`TestEngineAcceptsWebSearchItemOnlyOnWebSearchTurns`, and
`TestEngineReportsCodexWebSearchAsAnthropicServerToolUse` in `engine_test.go`,
`TestResponseReducerReportsWebSearchWithEmptyDisplayQuery` in `stream_test.go`,
`TestTranslateReplaysBridgeWebSearchResponse` in `translate_test.go`, plus
`TestHandlerAcceptsClaudeCodeWebSearchRequest`, which replays Claude Code's
exact request body end to end.

### `TaskOutput` is withheld from Codex, because its id dies before the model sees it

A background task's id resolves **only** against Claude Code's in-memory
`appState.tasks`. A task that is terminal and already notified is deleted from
that map by a 1s sweeper: a workflow 30s after it completes (`evictAfter =
endTime + Dye`, `Dye = 30000`, a hardcoded literal with no env override — unlike
`TASK_MAX_OUTPUT_LENGTH`), a background shell immediately. `TaskOutput` has no
disk fallback.

The trap is timing, not the tool. The `<task-notification>` carrying the id is
queued and only delivered when the **current turn ends**, so any turn longer
than the grace window hands the model an id that is already dead. Bridged turns
routinely run minutes: one shipped case completed at 22:36:17Z, was delivered at
22:39:29Z, and failed at 22:39:43Z — reaped three minutes earlier. The trailing
"`. Running background agents: …`" is a hint appended to *every* not-found
message; those agents are unrelated to the id asked for, which is why it reads
as "no task found, yet an agent is clearly running".

Nothing is ever lost — only the handle. The `.output` file survives and is the
path both the launch result and the notification already hand over.

- **Withholding is the lever; guidance is not.** Claude Code already ships
  "DEPRECATED … prefer Read" *in the tool's own description*, and the bridged
  model called it anyway — ~12 times a session against 0.06 for a native pane,
  failing ~12% of the time (100 failures over 41 sessions). Do not downgrade
  `withholdUnreliableTools` back into an instruction; that experiment has run.
- **Yield to `tool_choice`.** A named choice keeps the tool, and `any` keeps it
  when nothing else could satisfy the turn, so withholding can never convert a
  host-forced call into a 400.
- **History replay is unaffected.** Past `tool_use` blocks are injected under
  `block.Name`, which already names undeclared functions every turn for renamed
  MCP tools (`mcp__x` in history vs `wisp_mcp__x` declared).
- **Native panes keep the tool.** wisp-deck is not in their path, and settings
  `disallowedTools` denies at the permission layer rather than removing the tool
  from the model, so applying it there trades one error for another.

Guarded by `TestTranslateWithholdsTaskOutputFromCodex`,
`TestTranslateKeepsTaskOutputWhenToolChoiceForcesIt`, and
`TestTranslateToolChoiceAnySurvivesWithheldTaskOutput` in `translate_test.go`,
plus `TestBaseInstructionsPointAtTheTaskOutputFile` in `engine_test.go`.

### A conversation's transport size is unbounded, and its token count says nothing about it

The bridge is stateless per turn: every request replays the whole conversation
into a fresh ephemeral Codex thread through `thread/inject_items`. That message
was built in one piece, and its size is **not** governed by the context guard,
because the guard deliberately prices an image at a flat
`promptGuardImageTokens` (~1600) and never counts its base64 bytes — right for
the model, and blind to the wire. The two measures drift apart without limit.

A real session wedged at exactly that gap: **82 images, 16,766,904 base64 bytes**
— ~131K tokens against a 272K window, so the guard passed it — and the inject
message crossed `defaultRPCMaxMessageBytes` (16 MiB). Three things then made one
oversized message a *permanently dead session*:

- **The refusal is deterministic**, and every later turn resends the same
  history, which only grows.
- **`Call` treated the local size refusal as a connection failure.** Nothing had
  been written, yet `c.fail` tore down a healthy app-server, so each retry also
  paid a full restart. `encodeMessage` (refusal, connection untouched) and
  `sendPayload` (real write, may `fail`) are now separate for that reason.
- **It surfaced as a retryable 502**, so Claude Code burned all ten retries and
  then wedged. It is now `isRefusedOversizedMessage` → 400 prompt-too-long, the
  one shape Claude answers by compacting — the only thing that can actually
  shrink the payload.

So `injectHistory` splits the history across as many calls as
`rpc.MaxMessageBytes()` requires. That is safe because **repeated
`thread/inject_items` calls append to the thread in order** — verified against a
live app-server by `TestLiveInjectItemsAppends` (env-gated; re-run it after a
codex upgrade, because a Codex change to replace-instead-of-append would
silently drop every chunk but the last). Rules that fell out of it:

- **Never rebuild "the whole history in one `Call`".** Chunk size comes from the
  transport's own cap, not a constant of the engine's own, so the two cannot
  drift.
- **One item can't be split**, so an item over the budget is the conversation's
  shape rather than a transport hiccup: it returns `oversizedMessageError`,
  which reaches Claude as prompt-too-long instead of a retry loop.
- **Raising the cap is not the fix.** Whatever the number, a long image-heavy
  session eventually crosses it; only splitting removes the ceiling.

Guarded by `TestATransportCarriesEveryConversationTheContextGuardAdmits` (the
invariant itself, at the real 16 MiB cap through a real `RPCClient`),
`TestEngineInjectsHistoryTooLargeForOneAppServerMessage`,
`TestEngineRejectsASingleHistoryItemLargerThanTheCap`,
`TestRPCRefusedWriteLeavesTheConnectionUsable`, and
`TestOversizedAppServerMessageBecomesPromptTooLong400`.

### The send budget is ours; the app-server's message sizes are not

All of the above is the **write** direction. The read direction had the same
number wired into it — `readLoop` sized its scanner with `MaxMessageBytes` — and
that conflation is a different bug with a worse blast radius. The bridge decides
how large its own messages may be; it gets no vote on how large a notification
Codex sends, and a generated image's base64 or a sub-agent's activity blob is
whatever size Codex makes it.

One long inbound line therefore killed the connection — and **that connection is
shared by every concurrent turn** (one app-server, `e.turns` keyed by thread), so
Claude Code's workflows and background agents all died together. It surfaced as
`502 inject Claude history: app-server message exceeds 16777216 bytes`, blaming
whichever call happened to be in flight rather than the notification that
actually overflowed, and telling the user to retry something deterministic.

- **`MaxInboundBytes` is a separate limit from `MaxMessageBytes`**, and far
  larger (`defaultRPCMaxInboundBytes`, 256 MiB). Never re-derive one from the
  other; that equality *is* the bug.
- **Passing the ceiling costs the message, not the connection.** `readLoop`
  discards the line, counts it (`overlongMessages`), and stays framed on the
  next newline. Killing the connection turns one unreadable notification into a
  session-wide outage; the ceiling is a memory backstop, not a kill switch.
- The read side has no test that an oversized inbound message closes the
  client — that was the behavior, and it was the defect.

Guarded by `TestRPCAcceptsAnAppServerMessageLargerThanTheSendBudget` and
`TestRPCSurvivesAnAppServerMessageOverTheInboundCeiling`.

### `ThreadItem` is a vendor-controlled open enum, so the guard denies by name

`rejectCodexOwnedItem` aborts a turn when Codex reports doing something on this
machine. It used to work the other way round: allow six known-good item types,
abort on everything else. That default made **every Codex release a potential
outage** — the enum already carries 18 variants in 0.146.0 and grows freely, so
each new one became a fatal `502 forbidden Codex-owned tool item` the moment a
user's Codex updated. `contextCompaction` shipped that way, then
`imageGeneration`, then `collabAgentToolCall` and `subAgentActivity` — the last
two ending single turns of 4h20m, 4h48m and 1h17m with "try again in a moment".

An unrecognized item is not evidence of a capability leak; it is evidence that
the vocabulary grew. So the guard names what it forbids
(`codexHostCapabilityItems`: `commandExecution`, `fileChange`, `mcpToolCall`,
`imageView`, `hookPrompt`, plus `webSearch` on turns that never asked for it) and
tolerates everything else, known or not.

- **The real enforcement is the thread config**, not this guard: read-only
  sandbox, no network, `approvalPolicy: "never"`, no MCP servers, a private
  throwaway cwd, shell/unified_exec/hooks/apps off. The guard is the tripwire
  that says the enforcement failed, and those names are the stable part of the
  enum — a shell escape surfaces as `commandExecution` however else Codex grows.
- **Never restore a default-deny branch.** `TestEngineToleratesCodexItemTypesIt
  DoesNotHostItself` asserts the property with an item type that does not exist,
  so it fails on reintroduction rather than on the next specific variant.
- **The reducer already no-ops on item types it does not model**
  (`applyWebSearchItem` returns early), so tolerating an item costs nothing.
- Codex gates collaboration sub-agents behind **four** flags, and `multi_agent`
  alone stopped covering it: `multi_agent_v2`, `enable_fanout` and
  `collaboration_modes` arrived later. The bridge surfaces none of a sub-agent's
  work to Claude, so every token one spends is lost — declare all four rather
  than inherit a new Codex's defaults.

Guarded by `TestEngineToleratesCodexItemTypesItDoesNotHostItself`,
`TestEngineRejectsCodexItemsThatReachThisMachine`, and
`TestEngineDisablesEveryCodexCollaborationFeature`.

### The sandbox governs Codex's tools; saying so to the model breaks Claude's

That read-only sandbox is enforcement the bridge needs, but Codex also **renders
it into the model's prompt**, and the model has no way to know it does not
describe the session it is actually working in:

```
`sandbox_mode` is `read-only`: The sandbox only permits reading files.
Approval policy is currently never. Do not provide the `sandbox_permissions`
for any reason, commands will be rejected.
```

The host's Edit/Write/Bash are Claude Code's tools, not Codex's, and obey
Claude's permission mode. A model reading those two sentences as session-wide
refuses them outright. This shipped: a GPT pane reported *"the repository is
mounted read-only, escalation is disabled … a write-enabled session is
required"* and abandoned four confirmed fixes — while its Claude session sat in
`bypassPermissions` for all 94 recorded `permission-mode` events, with no
transitions. It reads as wisp-deck overriding the user's permission mode, and
it is not: nothing in the launch chain passes `--permission-mode`, and the
launch overlay writes no `permissions` key.

- **Suppress the prose, never the sandbox.**
  `include_permissions_instructions: false` and
  `include_environment_context: false` on the thread config are **prompt
  assembly only** — `sandbox`, `sandboxPolicy`, the `features` map and
  `rejectCodexOwnedItem` are what actually keep Codex off this machine, and
  none of them move. Relaxing the sandbox to make the model feel write-enabled
  would hand it the machine.
- **The environment context is the same misdirection about the cwd.** It
  advertises the private throwaway directory, so a model told to work in the
  user's project is also told it is somewhere else entirely.
- **Codex ignores unrecognized config keys silently**, so a rename would bring
  the prose back with no error and a green unit test. That is why the guard is
  a live one, and why `baseInstructions` also states the fact positively
  ("Codex's own sandbox … governs Codex's tools alone … never refuse or defer
  work on the grounds that the session is read-only"). Belt and braces: the
  instruction is what survives Codex dropping the keys.

Guarded by `TestEngineSuppressesCodexSandboxPromptText` (which also pins the
sandbox and approval policy that must NOT change),
`TestBaseInstructionsSayTheHostToolsWriteForReal`, and
`TestLiveCodexSandboxProseIsSuppressed` — the only check that can see Codex's
side (run it after a codex upgrade; see Commands).

### A generated image is delivered as a path, never as a block

Codex generates images on its own servers, but the app-server **writes every one
to a file** under `$CODEX_HOME/generated_images/<threadId>/<itemId>.png` and
reports the absolute location as `savedPath` on the `imageGeneration` item.
`item/completed` carries `savedPath`, `revisedPrompt`, `status`, and `result` —
the last being the same picture again as ~2MB of base64.

`savedPath` is the deliverable and the item is the only place it is ever
announced. Parsing the item without it shipped as *"[Codex generated an image,
but this bridge cannot return images to Claude Code, so it was discarded]"* —
printed while the finished picture sat on disk, unreferenced. The Messages API
having no assistant-role image block is true and beside the point: the path
travels as text, Claude Code reads the file from it, and a `Read` tool_result
carries the picture back to Codex as `inputImage`, which is what makes
frame-to-frame editing work at all.

- **Never drop `savedPath`,** and never re-word the notice into a claim that the
  image is gone. If it is ever absent, say *that* — report a non-`completed`
  `status` as the failure it is, and never fall back to "discarded".
- **Keep `result` out of the transcript.** It duplicates the file at megabytes
  per image.
- **The path outlives the turn.** Bridge threads are `ephemeral`, so the
  `thread/delete` in `cleanupTurn` answers `-32600 thread is not persisted and
  cannot be deleted` and the file is untouched.
- **The instructions permit image generation.** It has no client-side off
  switch, so the old "never use any Codex-owned … image … tool" line bought
  nothing and cost consistency: the model either obeyed by answering an image
  request with SVG or ignored it and drew anyway — the same prompt behaving
  differently run to run. Do not restore it; the shell/filesystem/MCP
  prohibitions (the ones that would touch this machine) stay.

Guarded by `TestResponseReducerReportsCodexImageSavedPath`,
`TestEngineToleratesCodexImageGenerationItem`, and
`TestBaseInstructionsAllowCodexImageGeneration` — plus
`TestLiveImageEndToEnd`, which is the only check that can catch Codex changing
its side (run it after a codex upgrade; see Commands).

### A subscription pane declares its provider's context window, or gets stranded

Claude Code budgets auto-compaction from **its own model catalog**, which knows
nothing about the server `ANTHROPIC_BASE_URL` points at. Decoded from the 2.1.x
bundle, the window is picked in this order:

```js
function sae(){ return Q.CLAUDE_CODE_DISABLE_1M_CONTEXT }        // unset → false
function Ov(e){ if(sae()) return false; return /\[1m\]/i.test(e) }
                                             // ^ a regex on the model STRING
if (Ov(model))                       return 1e6;
if (betas.includes(1m) && EW(model)) return 1e6;
if (L2(model))                       return 1e6;
let n = Q.CLAUDE_CODE_MAX_CONTEXT_TOKENS;
if (n > 0 && !Bo(ls(model)).startsWith("claude-")) return n;
return 200000;                                                  // Xbr
```

Two consequences, both shipped:

- An unrecognized subscription model lands on the flat **200000**, which is
  *wrong in both directions*: it strands `glm-4.5-air` (real window 131072) and
  it silently costs a Kimi user a quarter of the 262144 they pay for.
- A session model still carrying Anthropic's `[1m]` marker gets **1000000**
  regardless of provider — `Ov()` never looks past the string.

Overshooting a provider's cap is **unrecoverable**: `/compact` must itself send
the oversized transcript, so it fails with the same 400 as every other turn (and
is *larger* than the turn that already failed — it appends the summarization
prompt). A real session sat at 253,954 tokens under `claude-fable-5[1m]`, was
switched to Kimi (`k3`, cap 262144), and the next tool result killed it for good.

Every subscription profile therefore declares its real window with
`CLAUDE_CODE_MAX_CONTEXT_TOKENS`, taken from the catalog that already knows
each model's limit (`claudeconfig.ContextBudget`) or entered by the user for a
custom endpoint. A window below 1M also coordinates two safeguards:
`CLAUDE_CODE_AUTO_COMPACT_WINDOW` directly caps current Claude versions, and
`CLAUDE_CODE_DISABLE_1M_CONTEXT=1` keeps the inherited marker from winning in
older ones. Rules that fell out of building it:

- **The budget is the MINIMUM across all four `ANTHROPIC_DEFAULT_*_MODEL`
  mappings.** One env var governs the whole session — `/model` and subagents
  move freely between the aliases — so anything larger lets the session grow
  past whichever mapped model has the tightest cap.
- **A provider-native mapping does not remove a global `[1m]` selection.**
  With global `model: "opus[1m]"`, a custom Qwen mapping rendered as
  `Qwen-3.8-Uncensored[1m]`; Claude ignored the max-context key, never
  compacted, then sent 230145 input plus 32000 output tokens to a 262144-token
  endpoint. The auto-compact override is the direct guard, while disabling the
  marker preserves the same result on older Claude versions.
- **The installer copies a default profile only when the file is absent**, so a
  profile created before these keys were declared can never be repaired by
  shipping a new default. `claude-config ensure-budget` sweeps existing ones,
  including a custom profile whose user-entered max window is the only size
  available, and `bin/wisp-deck` runs it.
- **A custom window is always user-owned.** Never infer one for a custom
  profile or replace its declared value just because its model id also appears
  in the catalog; the self-hosted endpoint may enforce a different limit. The
  profile stays unavailable until its endpoint, model, positive context window,
  and API key are all present.
- **A switch re-checks that the conversation still fits.** Retargeting replays
  the WHOLE transcript to the new provider, so `_guard_subscription_context`
  measures the live conversation against the target's declared window and
  refuses while the roomier backend can still run `/compact`. Unknown is not
  too big: an absent transcript, no `jq`, or a target declaring no window all
  allow the switch — blocking on missing data is a worse trade.

Verify a change against a live pane, not the decode: launch `claude --settings
<profile>` on an isolated tmux server and run `/context`, which prints
`Auto-compact window: <N> tokens` outright. Guarded by
`internal/claudeconfig/contextbudget_test.go` (including a check that every
shipped default declares its provider's window) and
`test/bash/subscription_context_guard_test.go`.

## Code Conventions

### Avoid Over-Engineering
- Don't add features beyond what's asked
- Don't create helpers for one-time operations
- Three similar lines > premature abstraction
- Only comment where logic isn't self-evident

### Shell Scripting Best Practices

**Strict Mode:**
```bash
set -e  # Exit on error (use in scripts)
set -u  # Exit on undefined variable (optional, use carefully)
set -o pipefail  # Pipe failures propagate (optional)
```

**Quoting:**
```bash
# ✅ CORRECT - Always quote variables
"$var"
"${array[@]}"
mkdir -p "$dir/subdir"

# ❌ WRONG - Unquoted (word splitting, glob expansion)
$var
${array[@]}
mkdir -p $dir/subdir
```

**Command Substitution:**
```bash
# ✅ CORRECT - Use $() for nesting and readability
result="$(command)"
outer="$(inner "$(innermost)")"

# ❌ WRONG - Backticks are legacy
result=`command`
```

**Conditionals:**
```bash
# ✅ CORRECT - Use [[ ]] for advanced features
if [[ "$var" == "value" ]]; then
  # Supports &&, ||, =~, <, >
  # No word splitting inside [[ ]]
fi

# ✅ CORRECT - Use [ ] for POSIX compatibility
if [ "$var" = "value" ]; then
  # More portable
fi

# ❌ WRONG - Don't use `test` command directly
if test "$var" = "value"; then
  # Verbose, no benefit
fi
```

**Error Handling:**
```bash
# ✅ CORRECT - Check command success
if command_that_might_fail; then
  success "Operation completed"
else
  error "Operation failed"
  return 1
fi

# ✅ CORRECT - Use || for fallback
result="$(brew --prefix 2>/dev/null || echo "/usr/local")"

# ❌ WRONG - Ignoring errors
command_that_might_fail  # What if it fails?
```

**shellcheck Compliance:**
- **ALWAYS** run `shellcheck` before committing
- Fix ALL warnings (SC1091 source directive is OK if verified)
- Use `# shellcheck disable=SCXXXX` ONLY when necessary with comment explaining why

**File Operations:**
```bash
# ✅ CORRECT - Check file existence
if [ -f "$file" ]; then
  # File exists and is regular file
fi

if [ -d "$dir" ]; then
  # Directory exists
fi

# ✅ CORRECT - Safe file reading
while IFS=: read -r name path; do
  echo "$name -> $path"
done < "$projects_file"

# ❌ WRONG - Cat abuse (useless use of cat)
cat file | grep pattern  # Use: grep pattern file
```

**Functions:**
```bash
# ✅ CORRECT - Clear function declarations
function_name() {
  local var1="$1"
  local var2="$2"

  # Always use local for function variables
  # Return 0 for success, non-zero for failure
  return 0
}

# ❌ WRONG - Global variables in functions
bad_function() {
  result="$1"  # Pollutes global scope
}
```

**Array Handling:**
```bash
# ✅ CORRECT - Proper array operations
array=("item1" "item2" "item3")
echo "${array[0]}"  # First element
echo "${array[@]}"  # All elements
echo "${#array[@]}"  # Length

# Iterate over array
for item in "${array[@]}"; do
  echo "$item"
done

# ❌ WRONG - Word splitting
for item in ${array[@]}; do  # Missing quotes
  echo "$item"
done
```

### Go Code Conventions

**Project Structure:**
```
cmd/wisp-deck-tui/     # CLI entry point and subcommands
internal/tui/          # Bubbletea UI components
internal/models/       # Data types (Project, Config)
internal/util/         # Utilities (path, JSON)
```

**Testing:**
- Unit tests alongside implementation: `*_test.go`
- Run with: `go test ./...`
- Mock external dependencies in tests

**Bubbletea Patterns:**
- Each TUI component implements tea.Model interface
- Init() for initialization
- Update() for message handling
- View() for rendering

**JSON Output:**
- All subcommands output JSON to stdout
- Errors go to stderr
- Use util.OutputJSON() helper for consistency

### Project-Specific Patterns

**TUI Output:**
```bash
# Use standardized TUI functions from tui.sh
header "Section Title"
success "Operation succeeded"
error "Something failed"
info "FYI message"
warn "Warning message"
```

**Configuration Files:**
```bash
# Read project file (name:path format)
while IFS=: read -r name path; do
  [[ "$name" =~ ^#.*$ ]] && continue  # Skip comments
  [[ -z "$name" ]] && continue  # Skip empty
  # Process $name and $path
done < "$PROJECTS_FILE"
```

**AI Tool Integration:**
```bash
# Check if command exists
if command -v claude &>/dev/null; then
  # claude is available
fi

# Install with verification
ensure_command "claude" \
  "curl -fsSL https://claude.ai/install.sh | bash" \
  "Run 'claude' to authenticate" \
  "Claude Code"
```

**Process Management:**
```bash
# Get process tree recursively
get_process_tree() {
  local pid="$1"
  local children
  children=$(pgrep -P "$pid" 2>/dev/null || true)

  echo "$pid"
  for child in $children; do
    get_process_tree "$child"
  done
}

# Kill with grace period then force
kill -TERM "$pid" 2>/dev/null || true
sleep 0.5
kill -KILL "$pid" 2>/dev/null || true
```

## Testing

### IRON RULE: No Code Without Tests

**⚠️ This is also in the completion checklist at the TOP of this file.**

**ALL code changes require behavior tests.**

**Bug fixes MUST follow this exact order:**
1. Write regression test
2. Run it → watch it FAIL (proves bug exists)
3. Fix the bug
4. Run it → watch it PASS
5. Only THEN is the fix complete

**No exceptions. No "I'll test later". No "it's obvious".**

Write test first. If you write code before test, delete it and start over.

**See "IMMEDIATE COMPLETION CHECKLIST" at TOP of file for the full workflow.**

### Commands
```bash
./run-tests.sh                               # Full suite
go test ./test/bash/... -run TestFoo -v       # Single test group
go test ./test/bash/... -run "test_name" -v   # Filter by name
```

### Go Test Structure

**Test Files:**
- Go unit tests: `internal/**/*_test.go`, `test/internal/**/*_test.go`
- Bash integration tests: `test/bash/*_test.go` (call bash functions via `os/exec`)

**Bash Integration Test (test/bash/):**
```go
package bash_test

func TestLoadProjects_reads_name_path_lines(t *testing.T) {
    dir := t.TempDir()
    writeTempFile(t, dir, "projects", "app:/path/to/app\nweb:~/code/web\n")
    out, code := runBashFunc(t, "lib/projects.sh", "load_projects",
        []string{filepath.Join(dir, "projects")}, nil)
    assertExitCode(t, code, 0)
    assertContains(t, out, "app:/path/to/app")
}
```

**Shared helpers** in `test/bash/helpers_test.go`:
- `runBashFunc(t, module, funcName, args, env)` — source module, call function
- `runBashFuncWithStdin(t, module, funcName, args, env, stdin)` — with stdin
- `runBashSnippet(t, script, env)` — run arbitrary bash
- `runBashScript(t, scriptPath, args, env)` — run script directly
- `mockCommand(t, dir, name, body)` — create mock executable in dir/bin/
- `writeTempFile(t, dir, name, content)` — create temp file
- `buildEnv(t, mockDirs, extra...)` — build env with PATH prepended
- `assertContains/assertNotContains/assertExitCode` — assertion helpers

**Critical Rules:**

**Setup/Cleanup:**
- Use `t.TempDir()` for auto-cleaned temp directories
- Use `t.Cleanup()` for deferred cleanup
- Use `t.Setenv()` for environment variable isolation

**Mocking External Commands:**
```go
// Create mock brew that reports "already installed"
dir := t.TempDir()
binDir := mockCommand(t, dir, "brew", `echo "already installed"`)
env := buildEnv(t, []string{binDir})
out, code := runBashFunc(t, "lib/install.sh", "ensure_brew_pkg",
    []string{"pkg"}, env)
```

**Table-Driven Tests:**
```go
func TestParseEscSequence(t *testing.T) {
    tests := []struct {
        name  string
        stdin string
        want  string
    }{
        {"up arrow", "[A", "A"},
        {"down arrow", "[B", "B"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            out, _ := runBashFuncWithStdin(t, "lib/input.sh",
                "parse_esc_sequence", nil, nil, tt.stdin)
            if strings.TrimSpace(out) != tt.want {
                t.Errorf("got %q, want %q", out, tt.want)
            }
        })
    }
}
```

### What to Test vs Not

**DO Test:**
- Public function contracts
- User-facing behavior
- File operations (create, modify, delete)
- Error conditions (bad input, missing files)
- Integration between modules
- Edge cases (empty input, special chars)

**DO NOT Test:**
- Private helper functions (unless complex)
- Third-party commands (brew, tmux, etc)
- Obvious shell behavior
- Visual formatting (unless critical)

### Common Pitfalls

| Pitfall | Solution |
|---------|----------|
| Temp files leak between tests | Use `t.TempDir()` for auto-cleanup |
| Tests depend on order | Each test should be independent |
| Missing assertions | Every test needs assertion checks |
| Not testing error paths | Test both success and failure |
| Assuming clean environment | Set up everything in test setup |
| Not mocking external commands | Use `mockCommand` + `buildEnv` for PATH isolation |
| Hardcoded HOME paths | Use `t.TempDir()` and env overrides |

### Red Flags - You're About to Violate The Rules

**⚠️ More red flags are in the completion checklist at the TOP of this file.**

If you catch yourself thinking ANY of these, STOP:
- "This is too simple to test"
- "I'll test it after"
- "Tests would just duplicate the code"
- "It's about the spirit, not the letter"
- "This case is different"
- "I already verified it manually"

**These thoughts mean you're rationalizing. Write the test first.**

## Configuration

**DO NOT modify** without explicit request: `run-tests.sh`, `.gitignore`, `VERSION`

## Important Patterns

1. **Modularity**: Each `lib/*.sh` file is independently sourceable
2. **Error Propagation**: Use `set -e` and proper exit codes
3. **User Feedback**: Consistent TUI output (header/success/error/info/warn)
4. **Graceful Degradation**: Detect and adapt to missing optional features
5. **Process Cleanup**: Recursive tree killing with grace period
6. **Config Management**: Support both merge and replace for existing configs
7. **Cross-Shell Compatibility**: Source user's profile (bash/zsh) for environment
8. **Symlink Management**: Use `ln -sf` for idempotent linking
9. **Path Expansion**: Always expand `~` to `$HOME` for validation
10. **Sound Notification**: Pluggable hook system for AI idle events

## JSON Interface Schemas

### select-project
```json
{"name": "wisp-deck", "path": "/path/to/wisp-deck", "selected": true}
{"selected": false}  // Cancelled
```

### select-ai-tool
```json
{"tool": "claude", "command": "claude", "selected": true}
{"selected": false}  // Cancelled
```

### add-project
```json
{"name": "new-project", "path": "/path/to/project", "confirmed": true}
{"confirmed": false}  // Cancelled
```

### confirm
```json
{"confirmed": true}
{"confirmed": false}
```

### settings-menu
```json
{"action": "toggle-ghost"}
{"action": "quit"}
```
