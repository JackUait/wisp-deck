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

So every subscription profile ships `CLAUDE_CODE_MAX_CONTEXT_TOKENS`, taken from
the catalog that already knows each model's real window
(`claudeconfig.ContextBudget`). Rules that fell out of building it:

- **The budget is the MINIMUM across all four `ANTHROPIC_DEFAULT_*_MODEL`
  mappings.** One env var governs the whole session — `/model` and subagents
  move freely between the aliases — so anything larger lets the session grow
  past whichever mapped model has the tightest cap.
- **The override is only honored for non-`claude-*` model ids**, and only
  *after* the three 1M branches have had their say. It therefore cannot cap a
  `[1m]` model at all; what makes it reachable is that subscription profiles
  map every alias to a provider-native id (`k3`, `kimi-for-coding`, `glm-4.7`).
- **The installer copies a default profile only when the file is absent**, so a
  profile created before the window was declared can never be repaired by
  shipping a new default. `claude-config ensure-budget` sweeps existing ones,
  and `bin/wisp-deck` runs it.
- **Never invent a window for a provider the catalog cannot size** — capping a
  session at a limit nobody enforces is its own bug.
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
