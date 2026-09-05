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
WISP_DECK_LIVE_TOKEN_USAGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexReportsTokenUsage -v  # After a codex upgrade: verify a real app-server still sends thread/tokenUsage/updated, which is the only thing keeping a bridged turn's recorded usage from falling back to the bridge's bytes/4 estimate (costs one short turn)
WISP_DECK_LIVE_FEATHERLESS_E2E=1 FEATHERLESS_API_KEY=... go test ./internal/featherless/ -run TestLiveFeatherless -v  # After a Featherless-side change is suspected: verify /v1/messages still speaks Anthropic with tool_use, and that its ": keep-alive" comments still keep the worst byte silence well under Claude Code's 20s watchdog trigger (costs one short turn)
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

### An untracked ledger row is not always a file, so discard needs `clean -ffd`

`git ls-files --others` — the query BOTH renderers build the "new" group from —
does not descend into a nested repository. A subagent's
`.claude/worktrees/<name>` checkout therefore arrives as **one row whose path is
`"<dir>/"`**, not as its files. Both renderers already knew that at *display*
time (`inspectWorktreeFile`, `untracked_numstat`) and neither knew it at
*discard* time.

`git clean -f` removes no directory at all, and refuses a nested repository even
with `-d` — **exiting 0 and printing nothing either way**. So discarding those
rows reported success, cleared the selection, refreshed, and left every one of
them exactly where it was. It shipped as "discard doesn't work" on a tab full of
agent worktrees.

- **The flags are `-ffdq`, unconditionally.** `-d` reaches a directory and the
  second `-f` reaches a nested repository; both are no-ops on a plain file, so a
  trailing-slash gate would only add a branch. This does delete a nested
  repository's own history — which is what discarding a row the ledger listed
  means, and strictly better than the silent no-op it replaced.
- **Removing the checkout is half of it.** The registration in `.git/worktrees/`
  survives, and the project menu polls `git worktree list` every ~2s
  (`DetectWorktreesFor`) without filtering `prunable` — so a discarded worktree
  came back as a menu row launching into a path that no longer exists.
  `git worktree prune` follows the removal; it only drops registrations whose
  directory is already gone, so it can never unregister a live worktree.
- **Both renderers, as always.** A pane picks between the Go ledger and
  `lib/compact-view.sh` by binary capability, so a fix in one is a fix for half
  the users.

Guarded by `TestDiscardRemovesAnUntrackedNestedWorktree` and
`TestDiscardUnregistersTheWorktreeItRemoved`
(`internal/ledger/actions_test.go`, which takes the path from a real
`Source.Load` so the test exercises the `"<dir>/"` shape production produces),
plus `TestDiscardWorktreeFile_deletes_an_untracked_nested_worktree`
(`test/bash/compact_view_test.go`).

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

### A tab in trouble says so, and says how bad it is

The tab title is the session's status at a glance, and it used to have only one
thing to say about a turn that ended: 🔔. A turn that **died** rendered exactly
the same bell as one that finished, so the tab strip could not distinguish work
completed from work lost. `reason` already travelled the protocol — the tick
simply rendered the phase alone.

Two cues, split by how certain the trouble is:

- **❌ — a confirmed failure**: `phase=attention, reason=error`. All three
  adapters already produce it (Claude's error status, Codex's system-error,
  OpenCode's error).
- **⚠️ — a possible one**: the session reported fine and then stopped reporting.

**Certainty picks the cue, not severity alone.** Sustained silence has a
documented benign cause — Claude serializing on macOS Security XPC publishes
nothing for *minutes* under concurrency while working perfectly — so the
uncertain signal gets the softer glyph. Putting ❌ on it would cry wolf on
healthy sessions and teach the user to ignore the one cue that means something
died.

- **The error cue is checked BEFORE the seen swap, and survives it.** A bell
  decays to 👀 once the user looks, because looking is what a bell asks for.
  Looking at a failure does not fix it, so ❌ is not swapped out — otherwise a
  glance at the tab strip erases the only record that the turn died.
- **A warning needs a valid read first, and a sustained silence after it.**
  `_ATTENTION_WATCH_EVER_VALID` gates it: a session that has NEVER reported is a
  launch in progress, not a fault, and warning through every cold start would
  make the cue meaningless. `_ATTENTION_WATCH_QUIET_LIMIT` (default 60 ticks,
  ~30s at the 0.5s interval) is the sustain — one missed read is routine.
- **The warning never covers a cue the phase earned.** A tab waiting on the user
  has something specific to say; the silence is only a guess.
- **It is a live reading, not a latch.** Any valid snapshot resets the counter,
  so a session that starts reporting again clears the cue without a relaunch.
- **There are TWO rendering sites.** `apply_tab_title` returns early in `model`
  mode — the agent named the tab itself — so the per-tick model re-emit renders
  the cue separately. Both `case` statements default to the plain title, so
  missing one fails *soft*: model-title users would simply never see a cue, and
  a manual check of the other mode looks green.

Guarded by `test/bash/tab_title_trouble_test.go` — including
`_a_failed_turn_shows_a_cross_focused_or_not` (the seen-swap precedence),
`_model_title_mode_carries_the_trouble_cue` (the second site),
`_never_warns_before_the_session_has_ever_reported` and
`_a_session_that_reports_again_clears_the_warning`.

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

### A here-document is a pipe under bash 5.3, and a pipe holds 512 bytes

Bash 5.3 writes a here-document — and a here-string, which is one — into a
**pipe** rather than a temp file whenever it is under a hardcoded 64KB, and it
writes the whole body *before* the reader starts. So the body has to fit the
pipe, and a pipe is only as big as the kernel granted it. On a Mac running a
deck of long-lived stands it grants the **512-byte minimum**: ~22,000 open pipe
fds exhaust the pipe budget and capacity never grows from there. Measured
2026-09-05: 500B fine, 512B hangs forever, 70KB fine again (the temp-file
fallback). `/bin/bash` (3.2) and zsh are unaffected, and TMPDIR is not involved.

Nothing about this looks like a bug in the script. The shell blocks in
`heredoc_write` with no error, no timeout and no output, so the caller simply
waits. It shipped as **"the model switcher does nothing"**:
`_current_session_identities` reads `tmux show-environment` (PATH alone runs
past 512 bytes) through `done <<< "$session_env"`, so the first click on the
account pill deadlocked its bash — and because `LedgerModel.openAccountSwitch`
guards on `switchingAccount` until `OpenSwitcher` returns, *every later click
was a silent no-op* for the life of the pane. The same deadlock sat on the
launch path in `get_loading_art` (704B of ASCII art), so a new tab hung on the
splash.

- **Never feed a here-document anything that can outgrow 512 bytes.** Fixed
  text goes through `printf '%s\n' 'line' 'line'`; an embedded script goes
  through `python3 -c "$script"` (`-c` puts `sys.argv[1:]` exactly where
  `python3 -` did); a variable read line by line goes through
  `< <(printf '%s\n' "$var")`.
- **Process substitution is NOT available everywhere.** tmux runs `run-shell`
  under `/bin/sh`, which is bash 3.2 in POSIX mode, where `< <(...)` is a
  **syntax error** — the file fails to parse and the click dies before dispatch.
  Ten lib files parse there today and must keep parsing there; in those, split
  on newline instead (`IFS=$'\n'; set -f; for line in $var`), which also keeps
  the loop in the current shell where a pipe would subshell it.
  `TestShellCodeThatParsesUnderBinSh_keepsParsingThere` compares each file
  against its own HEAD version, so it fails only on a file that *lost* the
  property.
- **A here-string carrying small, bounded data is left alone** — a terminal
  size, an 8-entry palette, one window's pane list. Rewriting those in the
  POSIX-safe form buys nothing and costs legibility.
- **Only the literal body is decidable**, so
  `TestShippedShellCode_hasNoHereDocumentThePipeCannotHold` caps that at 400
  bytes; a here-string's size is whatever the variable holds at runtime, which
  is why the deadlock is also pinned behaviorally by
  `TestCurrentSessionIdentities_survives_an_environment_past_the_pipe_buffer`.

Reducing the number of open stands restores normal pipe sizes and hides all of
this again — which is exactly why it presents as intermittent.

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

### Featherless speaks Anthropic natively, and keeps its own socket warm

Featherless is documented as an OpenAI-compatible provider, which by the rule
above would put it behind a translating proxy. It does not need one:
`POST /v1/messages` is a real Anthropic Messages route, undocumented and verified
live — it answers unauthenticated with Anthropic's error envelope where
`/v1/chat/completions` answers with OpenAI's and an unknown path answers with a
fastify 404, and with a key it streams `content_block_start{tool_use}` →
`input_json_delta` → `stop_reason:"tool_use"`. So the provider is an ordinary
API-key gateway at `https://api.featherless.ai`, and ~15,500 models are reachable
from the Subscription modal's picker.

- **The credential is `ANTHROPIC_AUTH_TOKEN`, never `ANTHROPIC_API_KEY`.**
  Featherless answers `x-api-key` with a 401 and `Authorization: Bearer` with a
  200.
- **The byte watchdog stays ARMED here**, unlike a self-hosted profile.
  Featherless fills the wait before the first token with
  `: keep-alive (awaiting first token)` SSE **comments** — the watchdog counts
  bytes, not events, so a comment is as good as a token. Measured on a cold 14B
  with a 22k-token prompt: comments every ~1.2s across a 12s model load, worst
  byte silence 4.8s against the 20s trigger. Disarming would trade a real
  dead-connection signal for nothing.
- **It sends no `event: ping` at all**, so those comments are the whole
  mechanism, and they appear **only when there is a wait to fill** — a small
  prompt to a hot model emits none. That is why the live guard sends a large
  prompt and asserts the measured byte silence rather than the comments'
  presence: the property is "never silent long enough to trip the watchdog", and
  a keep-alive count is only how Featherless currently achieves it.
- **Only tool-calling models are offered.** 15,571 of the 21,908 report
  `features.tool_use`; the rest produce a pane that cannot read or edit a single
  file, so `Parse` drops them — along with any model declaring no
  `context_length`, because an undeclared window falls back to the flat 200000
  that strands a 32768 model permanently.
- **`available_on_current_plan` is absent on an unauthenticated listing**, and
  absent must read as available, or the picker is empty until a key is typed.
- **`is_gated` is about HuggingFace, not Featherless.**
  `meta-llama/Llama-3.3-70B-Instruct` is gated and serves normally, so it is
  never shown and never blocks a pick.
- **`RemoteCatalog` shares `UserConfigured`'s save path** via
  `Provider.SuppliesOwnModel()`: both must skip `WriteModelMappings`, which
  writes the four aliases from an empty model list and so deletes the picked
  model on every save. What it does **not** share is the watchdog disarm, which
  stays keyed to `UserConfigured` alone.
- **The launch wraps a Featherless pane in a repair proxy** — see the section
  below. That makes `featherless` the second key `get_claude_config_provider`
  must report (it allowlists only the gateways whose marker changes a launch
  decision; `custom` still resolves to the empty string because nothing branches
  on it). Pinned by `TestGetClaudeConfigProviderReportsFeatherless` and
  `TestGetClaudeConfigProvider_never_reports_featherless_as_the_gpt_bridge`.
- **Alias resolution takes the LONGEST matching alias, not the first in slice
  order.** Profiles are named after the picked model, so "Featherless GLM-5.2"
  contains zhipu's `glm` and "Featherless Kimi-K3" contains moonshot's `kimi` —
  and zhipu is `Providers[0]`, so no placement could fix it. Ties keep slice
  order, so "kimi for coding" still beats "kimi".

Guarded by `internal/claudeconfig/featherless_provider_test.go`,
`internal/featherless/*_test.go`, `internal/tui/subscription_model_picker_test.go`,
and `internal/tui/subscription_modal_featherless_test.go`.

### A Featherless pane runs behind a request repair proxy

Featherless serves the Anthropic Messages API, but it validates the **published
schema**, where a message role is only `user` or `assistant`. Claude Code puts
its capability listings — the agent-type roster and the skills roster — into
`messages[]` as entries with `role: "system"`. Anthropic's own API accepts them;
Featherless answers the whole request with
`400 messages.1.role: Invalid enum value ... received 'system'`, which kills the
turn before the model ever sees it.

Measured, not decoded: a request captured from a live pane was replayed as sent
(**400**) and with that one role rewritten to `"user"` (**200**, normal
completion).

The proxy repairs a second thing, and it is the reason a Featherless pane could
look alive and still do nothing. **A request that declares `thinking` turns
Featherless's tool-call parser off.** Extended thinking is on by default, so
Claude Code puts `thinking: {"type":"adaptive","display":"omitted"}` on every
request; the model still emits a tool call, but the endpoint stops converting
it, so the raw Qwen `<tool_call><function=…><parameter=…>` XML arrives as
assistant **text**. The pane renders the markup, no tool runs, and the turn ends
`end_turn` — so nothing errors and the session simply spins.

Measured 2026-09-02, same prompt and model, both arms, on
`TurboVadim/Qwen3.8-27B-OBLITERATED` and
`huihui-ai/Huihui-Qwen3.8-27B-abliterated`: without the field `stop_reason` is
`tool_use` and a `tool_use` block arrives; with it `stop_reason` is `end_turn`
and the XML sits in a text block. Confirmed end to end through the launch chain
— the pre-fix binary printed the bare XML and called nothing, the fixed one ran
`Read` and answered.

- **Dropping `thinking` costs no reasoning.** Featherless returns a `thinking`
  block whether or not the request asks for one, so the field buys the turn
  nothing and breaks its tool calling. It is the *presence* of the key that does
  it — `{"type":"enabled","budget_tokens":N}` fails the same way, and
  `output_config.effort` is innocent. So is the header: both arms sent the same
  `Anthropic-Beta: …,interleaved-thinking-2025-05-14,…`, and the repaired one
  called tools with it still there. The body field is the whole lever.
- **The strip is safe for a model the bug never touched.** Not every class
  mis-parses: `zai-org/GLM-5.3-Flash` and `GLM-4.7-Flash` answer `tool_use` with
  a `thinking` block in BOTH arms, identically. So stripping unconditionally for
  every Featherless pane repairs the broken classes and takes nothing from the
  working ones — which is why this needs no per-model probe.
- **The repair is one pass, and `changed` gates the rewrite.** `Rewrite` returns
  the body untouched when nothing moved, so a deletion recorded after that guard
  would be silently thrown away.
- **There is no settings-level escape.** `--disallowedTools Task` removes the
  agent roster and the skills roster takes its place; both are Claude Code's own
  emissions. Disabling enough tools to silence them costs more than the proxy.
- **The settings file beats the process environment.** Verified live: launching
  with `ANTHROPIC_BASE_URL` exported and a profile declaring its own, the profile
  won. So the proxy cannot be delivered by env override the way the GPT bridge
  does it — the session's **settings overlay** is what gets pointed at the proxy.
- **The overlay is the session's own copy.** `write_claude_launch_settings` never
  modifies the stored profile, so `PointSettingsAt` rewrites the overlay in place
  and every other key in it — the API key the proxy forwards but never holds, the
  picked model, the declared window, the image deny rules — travels untouched.
  The stored profile keeps naming the real endpoint, so `ConfigReady`, the
  budget sweep and the modal all keep working on the truth.
- **`FlushInterval: -1` is load-bearing.** Buffering the response would swallow
  the `: keep-alive` comments Featherless sends while awaiting its first token,
  which is the whole reason the byte watchdog stays armed for this provider.
- **Nothing here may cost a session.** An overlay that cannot be read, declares
  no endpoint, or already points at loopback runs the child exactly as it would
  have run anyway.
- **A model that refuses to call tools is the model, not the proxy.** Verified
  end to end: `zai-org/GLM-5.3-Flash` through this proxy called Read and quoted
  the file back, while `moonshotai/Kimi-K3` on the same setup insisted "tool use
  has been temporarily disabled for this turn" with all 29 tools present in the
  request.

Guarded by `internal/rolefix/*_test.go`,
`cmd/wisp-deck-tui/claude_rolefix_test.go`, and
`test/bash/claude_rolefix_launch_test.go`.

### That proxy also repairs the response, because a JSON schema is only advice here

`/goal`, a prompt hook, memory selection and auto-mode setup all ask the model
for a JSON object and then run a plain `JSON.parse` over the reply — Claude Code
strips a markdown fence (`$x`) and nothing else. What makes that safe on
Anthropic's API is `output_config.format`: the server constrains the decode, so
the reply cannot be anything but the object.

Featherless accepts that field and ignores it. Measured 2026-09-02 on
`TurboVadim/Qwen3.8-27B-OBLITERATED`: under a schema requiring
`{capital_city, population_millions}`, a system prompt asking for one sentence of
prose answered `Paris`. Every other lever an OpenAI-compatible server usually
offers — `response_format`, `guided_json`, `extra_body.guided_json` — is likewise
accepted and unhonoured, and there is no client-side off switch either:
`s3o`/`hEt` add `format` for any model whose name is not an old Claude, so a
Featherless pane sends the schema and the `structured-outputs-2025-12-15` beta on
every one of these side queries (confirmed on the wire).

So the contract degrades to a suggestion in the prompt, and the model breaks it
whenever it feels like explaining itself first. A `/goal` Stop hook came back as
a paragraph followed by a perfectly good verdict object; Claude Code reported
**`Stop hook error: JSON validation failed`** and the goal never held. The proxy
therefore delivers what the endpoint dropped: a text block that does not parse is
replaced by the JSON object inside it.

- **Only a request that declared a schema is touched.** `output_config` carries
  `effort` on every ordinary turn, so the trigger is `format`, never the object
  holding it. Extracting an object out of a normal reply would replace the answer
  with a fragment of itself.
- **A conforming block is replayed verbatim, delta for delta.** Repairing what is
  not broken is how a proxy invents bugs, and a byte-identical passthrough is
  what the test asserts.
- **Nothing is invented.** Text holding no complete object is forwarded as it
  arrived: the client's own error beats a fabricated verdict.
- **The outermost object wins, and the schema breaks the tie.** A member object
  decodes at its own opening brace too, so the scan skips past each match; among
  the outermost ones the last carrying every `required` key is the verdict, which
  is what stops a trailing `the schema is {…}` aside from being read as one.
- **Holding a block back is silence, so the proxy fills it.** It writes
  `: keep-alive` comments while buffering — bytes are all the byte-stall watchdog
  counts, the same mechanism Featherless uses before a first token.
- **A body past the repair budget is delivered, not truncated.** An
  `io.LimitReader` alone would silently cut it; the read goes one byte past the
  budget and hands the rest on unread.
- **The request asks for `identity`.** A compressed body is bytes the repair
  cannot read, and `ReverseProxy` does not decode one.

Verified live before and after on the real endpoint: the same request direct to
Featherless returned prose then JSON (a hook failure), and through the proxy
returned the bare verdict.

Guarded by `internal/rolefix/structured_test.go`.

### Claude Code's floor is ~20,000 tokens, so a 32K model is not a small model

Everything Claude Code sends before the conversation starts was measured on the
wire on 2026-09-02, by pointing a bare headless pane (`--strict-mcp-config`, an
empty `CLAUDE_CONFIG_DIR`, a two-file project) at a logging proxy and posting the
captured request to Featherless's `/v1/chat/completions`, which reports the
prompt cost its `/v1/messages` route does not:

| part | bytes |
|---|---|
| 26 tool schemas | 65,526 |
| system prompt | 7,037 |
| agent + skill rosters (a `role: "system"` message) | 7,390 |
| **charged by the endpoint** | **19,838 tokens** |

A profile also reserves a quarter of the window for the reply, so a
32,768-token model has `32768 - 8192 - 19838` = **4,738 tokens** for the whole
conversation. One file read spends that. The model is not "small for long
sessions" — it cannot finish one task.

That is **88% of Featherless's tool-calling catalog**: of 15,573 models
declaring `tool_use` and a context length, 13,712 are exactly 32768, another
1,835 are 4096 or 8192, and **26** are 65536 or wider. So `featherless.Parse`
drops anything under `MinContext`, for the same reason it already drops a model
without tool calling: it produces a pane that cannot do the work.

- **`MinContext` is derived, not chosen.** The room left for the conversation
  (`window - window/4 - floor`) has to be at least as large as the floor itself,
  which puts the bar at 53,334; 65536 is the next power of two, and the catalog
  holds nothing between 32768 and 131072 anyway.
- **`ClaudeCodeFloorTokens` and the proxy's estimator are pinned to one
  recording.** `test/internal/rolefix/testdata/claude-code-first-turn.json` is that
  captured request, and `TestEstimateInputTokens_matches_what_the_endpoint_charged`
  holds the estimate to what Featherless billed for it. It lives under `test/`
  because the host-effect audit reads every tracked text file under `internal/`
  as production source, and this one is a verbatim capture of Claude Code's own
  tool descriptions. Re-capture it when
  Claude Code's tool set changes shape; both numbers move together.

### Featherless reports no usage at all, so nothing ever compacts

`/v1/messages` answers **every** turn with `usage: {input_tokens: 0,
output_tokens: 0}` — streamed and not, on every model tried. The count exists:
the same conversation through `/v1/chat/completions` reports 19,838 prompt
tokens. Only the Anthropic adapter drops it.

Claude Code sizes auto-compaction from that figure, so a permanent zero is not a
cosmetic statusline bug. The transcript never compacts, `/context` reads empty,
the cost line reads 0.0%, and the conversation grows until the endpoint starts
rejecting every turn — at which point there is no way back, because `/compact`
must itself send the oversized transcript and is larger than the turn that
already failed. On a narrow model that arrives within a couple of turns.

So `internal/rolefix/usage.go` supplies what the endpoint dropped:
`message_start` gets the request's own estimate, `message_delta` gets the reply's
streamed bytes, and a body that was not streamed gets both.

- **A figure the endpoint reported is never replaced.** A gateway that counts
  knows better than an estimate.
- **The estimate counts the JSON whole, not the strings inside it.** A tool
  schema reaches the model as the schema — braces, keys and all — and counting
  only its string values read 17,023 for a request charged 19,838.
- **It leans high, deliberately.** `bytesPerTokenDenominator` is 3.8 against a
  measured 3.98, because reading low ends a session and reading high only
  compacts a little early — the same direction `cellWidth` rounds.
- **An image is priced flat** (`imageTokens`, the GPT bridge's own figure). Its
  base64 is orders of magnitude larger than its token cost, so counting those
  bytes would report one screenshot as larger than the window.

### Featherless's Qwen tool parser destroys the tool call it strips

This is what "I can't use Qwen from Featherless" actually is. On the captured
first request above, `Qwen/Qwen3-VL-30B-A3B-Instruct` billed **181 completion
tokens** and returned **22 tokens of prose and no tool call** — 159 generated
tokens discarded. `Qwen/Qwen3.5-397B-A17B` billed the same and returned an
**empty** reply. The parser lifts the model's `<tool_call>` markup out of the
text and then emits nothing in its place; `finish_reason: null` marks it on the
chat-completions route, and the Anthropic route normalizes that to
`stop_reason: "end_turn"`, so the pane sees a model that announces work and
stops. Forever — a `/goal` Stop hook loops on it until the block cap fires.

It is Qwen-specific, not endpoint-wide. Replaying that same request across all
26 models wide enough to run Claude Code: **21 call tools normally** (every GLM,
every Kimi, DeepSeek V3.1/V3.2/V4, MiniMax M2/M2.5/M2.7/M3, Step-3.5-Flash,
Laguna-S-2.1), most of them with a prose preamble in the same message. The
failures are `Qwen/Qwen3.5-397B-A17B`, `Qwen/Qwen3-VL-30B-A3B-Instruct`, the two
oldest DeepSeeks, and one MiniMax that 400s. **There is currently no Qwen on
Featherless that can run a pane**: the ones that call tools are all 32768.

- **The discarded bytes cannot be recovered**, so the proxy does the one thing
  left: it refuses to pass the silence on. A reply that declares tools and
  arrives with no content block at all — a shape Anthropic's API cannot produce
  — is given a text block naming what happened. A reply carrying anything at all
  is the model's, and commenting on it would put words in its mouth.
- **`thinking` is not a way out.** It disables the parser for the 27B class (the
  raw markup then arrives as text, recoverable in principle), and does nothing
  for Qwen3-VL, whose call is destroyed either way. Stripping `thinking` stays
  right.
- **Neither is prompting.** A rule telling the model to emit the call with no
  preamble changed nothing across 12 runs on both models.

### The endpoint passes through a tool name the model mis-spelled

Anthropic's API validates a tool call's name against the tools the request
declared, so a client never receives one it did not supply. Featherless passes
the model's own spelling through: `TurboVadim/Qwen3.8-27B-OBLITERATED` answered
a tool declared as `Read` with `read`, on both routes, and not every time —
which is what makes it read as a flaky model rather than a missing check. Claude
Code answers that with `No such tool available`, spending the turn on an error
for a call that was right in every way that matters.

`internal/rolefix/toolnames.go` restores the declared spelling. A name matching
no declared tool is left exactly as it arrived — that is the model inventing a
tool (`read_file` was observed), and the client's own error beats running
something nobody asked for. Two tools whose names differ only by case resolve to
neither.

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

### The window holds the reply too, so the reply's room comes out of it

Declaring the window is only half of fitting inside it. Claude Code also picks
`max_tokens` from its own catalog, which has never heard of a subscription
model, and settles on **32000** — and an inference server enforces
`input + max_tokens <= context`, not `input <= context`. On a 32768-token model
that leaves ~768 tokens for the system prompt, the tool schemas and the
conversation, so *every* real turn is rejected before the model reads a word:
`API Error: 400 The request was rejected as invalid. Please check your request
parameters.`

Measured against api.featherless.ai on 2026-09-02 by replaying a request
captured from a live pane through a logging proxy: `max_tokens` 32000 and 30000
both answered 400, 28000 and below answered 200, and adding ~4000 tokens of
input moved that boundary down by the same amount. The identical profile
carrying an 8192 reserve ran the turn to completion.

Claude Code's own accounting says the same thing outright. `/context` on a live
pane carrying the pre-fix profile reported `Autocompact buffer: 33k tokens
(100.7%)` and **no free space at all** — the room it holds back for the reply is
larger than the entire window. The same pane with the reserve declared reported
`Autocompact buffer: 21.2k (64.7%)` and `Free space: 9.3k (28.3%)`.

So a declared window also declares `CLAUDE_CODE_MAX_OUTPUT_TOKENS`. That key is
the whole of the fix.

- **Do not also take the reserve out of `CLAUDE_CODE_AUTO_COMPACT_WINDOW`.**
  Claude Code sizes its own auto-compact buffer from the reserve, so once the
  reserve is right the reply's room is already carved out — the buffer figures
  above are that happening. Shrinking the compact key on top of it was measured
  and changes nothing: 32768, 24576, 20000 and 10000 in a launch overlay all
  produced byte-identical `/context` accounting. It is also actively risky,
  because Claude Code's own parser documents the accepted range as
  `'auto' or 100k-1M tokens`, so a window minus its reserve can land below 100k
  and be rejected — `131072 - 32000 = 99072` does.
- **Both window keys keep naming the endpoint's real limit.**
  `_guard_subscription_context`, the statusline and the modal all read
  `CLAUDE_CODE_MAX_CONTEXT_TOKENS` as the truth about the endpoint.
- **The reserve never rises above 32000.** That is what Claude Code would have
  asked for unprompted; this exists to fit a small window, not to ask a provider
  for more than it was already going to be asked for. A quarter of the window,
  capped there — no cataloged model's real max output falls below that, so
  consulting `Model.Output` would never lower it.
- **A declared reserve is the user's own figure and is kept**, exactly like
  `stampByteWatchdog`'s key: they may know their endpoint's real output cap.
  Only `WriteCustomContextWindow` re-derives one, because there the user is
  changing the size of the thing being divided.
- **`EnsureContextBudget`'s change-check must compare the reserve.** It
  enumerates its keys explicitly, and every profile that has this bug already
  declares all three window keys correctly — a check that omits the fourth
  reports "unchanged" and never writes the file, silently skipping exactly the
  broken profiles the sweep exists to repair.
- **A window of 1M or more is left alone**, the same branch that already ships
  no compaction cap there. The overflow exists at 1M too, just far rarer.

Known limit, by construction: a single huge tool result can still carry one turn
past the threshold in one step and be rejected once. That is inherent to a 32K
window, not something a profile can prevent.

Guarded by `internal/claudeconfig/outputreserve_test.go` (including the sweep's
backfill of a window-current profile, whose change-check is the trap above, and
a check that every shipped sub-1M default declares the reserve its window
implies) and
`TestApplyPendingSubscriptionModel_reserves_output_room_for_a_small_window`.

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
