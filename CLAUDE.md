# CLAUDE.md

Project guidance for Claude Code (claude.ai/code) working with this repository.

---

## Working rules

Write the failing test first, then the code. Run `shellcheck` on the shell files
you changed. Work is not complete until `git push` succeeds.

Run the tests you added, scoped to their files. When a change genuinely needs the
whole suite, run it through `./run-tests.sh` and never as a bare `go test ./...`:
the script exports `WISP_DECK_TESTING=1`, which production code checks before it
touches the host, so a bare run sends notifications, rewrites the install and
kills live sessions. `./test/bash/...` needs `-timeout 20m`; the package runs
close to `go test`'s default 10m, and the kill reads as a hang.

## Commands

Env-gated live checks. Each costs a real turn or API call, so run only the one
named after the upgrade you just did.

```bash
WISP_DECK_LIVE_CLAUDE_E2E=1 go test ./test/bash/ -run TestLiveClaude -v  # After a claude upgrade: verify the real-claude behaviors draft preservation depends on
WISP_DECK_TMUX_WIDTH_E2E=1 go test ./internal/tui/ -run TestCellWidth_matches_a_live_tmux  # After a tmux/go-runewidth/uniseg bump: re-check the diff pager's width model against a real tmux
WISP_DECK_LIVE_IMAGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveImageEndToEnd -v  # After a codex upgrade: verify a generated image still reports a savedPath that exists, and still round-trips back into Codex (costs one real image generation)
WISP_DECK_LIVE_INJECT_E2E=1 go test ./internal/gptbridge/ -run TestLiveInjectItemsAppends -v  # After a codex upgrade: verify repeated thread/inject_items still APPEND in order, which chunked history injection depends on (costs one short turn)
WISP_DECK_LIVE_SANDBOX_PROSE_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexSandboxProseIsSuppressed -v  # After a codex upgrade: verify the include_* config keys still stop Codex describing its read-only sandbox to the model, which is what makes a bridged pane refuse to edit (costs one short turn)
WISP_DECK_LIVE_CODEX_COLD_E2E=1 go test ./internal/codexadapter/ -run TestLiveColdAppServerIsObserved -v  # After a codex upgrade: verify a real app-server still binds its socket and completes the observer handshake inside defaultCodexStartupTimeout (copies the vendored binary to a fresh inode so the exec goes cold, and logs the measured start)
WISP_DECK_LIVE_TOKEN_USAGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexReportsTokenUsage -v  # After a codex upgrade: verify a real app-server still sends thread/tokenUsage/updated, which is the only thing keeping a bridged turn's recorded usage from falling back to the bridge's bytes/4 estimate (costs one short turn)
WISP_DECK_LIVE_CHATGPT_CATALOG_E2E=1 go test ./internal/gptbridge/ -run TestLiveChatGPTCatalogMatchesTheAppServer -v  # After a codex upgrade: verify claudeconfig's openai-chatgpt model ids still equal what a live app-server serves — All-In turns each id into a picker row, and one Codex has dropped resolves and then 400s (costs no quota)
WISP_DECK_LIVE_CHATGPT_BRIDGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveChatGPTBridgeStartsAndServes -v  # After a codex upgrade: measure what All-In's lazy bridge start costs inside a turn, against Claude Code's 20s byte-stall banner (costs no quota)
WISP_DECK_LIVE_CODEX_EOF_E2E=1 go test ./internal/gptbridge/ -run TestLiveCodexAppServerExitsWhenItsParentPipeCloses -v  # After a codex upgrade: verify a real app-server still exits on stdin EOF, which is the only thing that reaps it when claude-allin is killed without running a defer (costs no quota)
WISP_DECK_LIVE_ALLIN_CHATGPT_E2E=1 go test ./cmd/wisp-deck-tui/ -run TestLiveClaudeAllInReachesTheChatGPTEngine -v  # After a codex upgrade: drive a wisp/cfg.openai-chatgpt row through the whole router chain to the engine's own model allowlist (costs no quota — the model it asks for is one no app-server serves)
WISP_DECK_LIVE_FEATHERLESS_E2E=1 FEATHERLESS_API_KEY=... go test ./internal/featherless/ -run TestLiveFeatherless -v  # After a Featherless-side change is suspected: verify /v1/messages still speaks Anthropic with tool_use, and that its ": keep-alive" comments still keep the worst byte silence well under Claude Code's 20s watchdog trigger (costs one short turn)
WISP_DECK_LIVE_LOGIN_MOVE_E2E=1 go test ./internal/allin/ -run TestLiveKeychainLoginsMove -v  # After a claude upgrade or a change to claudeaccount.Reconcile: cross two throwaway Keychain entries and verify the real `security` round trip moves each login back and leaves mcpOAuth in its slot (costs nothing; never reads a real login)
WISP_DECK_LIVE_KEYCHAIN_E2E=1 WISP_DECK_LIVE_KEYCHAIN_DIR=~/.config/wisp-deck/claude-accounts/<login> go test ./internal/allin/ -run TestLiveKeychainRefresh -v  # After a change to the All-In credential path: verify an EXPIRED login's token still refreshes against Anthropic and is written back to the Keychain (name a login whose token has actually expired; a live one is served untouched and proves nothing, and the run spends that login's refresh token a rotation)
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

Use `make release` — never `gh release create` or the GitHub UI. The step-by-step
flow, preflight and post-release verification live in the `releasing` skill
(`.claude/skills/releasing/SKILL.md`). These two constraints stay here, because
they bind ordinary development, not just a release:

**Gotcha (binary warm-up):** the FIRST exec of a freshly built, downloaded, or re-signed `wisp-deck-tui` pays a macOS Gatekeeper/XProtect assessment (~1s idle, multi-second under load). Both the file-list diff modal and the account switcher exec this binary, so a cold binary makes the first modal open stall. **Every code path that writes or re-signs `~/.local/bin/wisp-deck-tui` MUST exec it once afterwards** (`"$bin" --version >/dev/null 2>&1 || true`). Existing warm-up sites: `wrapper.sh` (session launch, via `warm_tui_binary` in `lib/tui.sh`), `scripts/release.sh`, `lib/install.sh` (`ensure_wisp_deck_tui`), and the Makefile `install` target — all guarded by `test/bash/tui_warm_test.go`.

**Gotcha (packaging is invisible locally):** the dev machine once ran wisp-deck from symlinks into the repo (it is copies now, see below), so a file the installer needs but `package.json`'s `files` never publishes still resolves here — and breaks for every `npx` user. This shipped in v2.22.0: `bin/wisp-deck` symlinked `~/.local/bin/wisp-deck` at the unpublished `bin/wisp-deck-config`, `ln -sf` made a dangling link, and setup reported success while the command was dead. **Whenever the installer references a new path, add it to BOTH `copyDistribution` in `bin/npx-wisp-deck.js` AND `files` in `package.json`.** Guarded by `test/npx/` (packaging cross-check + a full install into an empty HOME), which `scripts/release.sh` runs in preflight and the `Install verification` workflow runs in CI. Because the launcher skips the copy when `.version` already matches, a packaging fix only reaches users on a **version bump**.

**Gotcha (the dev install is not an npm install):** on a dev machine the
post-commit hook runs `scripts/sync-dev-install.sh`, which copies HEAD's
distribution into `~/.local/share/wisp-deck` (by rename, never in place) and
writes `.dev-install` with the commit sha. That marker stops `npx wisp-deck` and
the menu's update from installing the older npm package over newer code; the
version check alone only sees "different" and would downgrade it. To go back to
the npm build: `WISP_DECK_FORCE_INSTALL=1 npx wisp-deck`.

## Architecture

Package gotchas live beside the code they guard, and load when a file in that
package is opened. Read the relevant one before changing that package.

| file | covers |
|---|---|
| `lib/CLAUDE.md` | the launch chain, the tab bar, tmux popups, worktree following, shell portability |
| `internal/tui/CLAUDE.md` | diff pager, worktree poll, popup backdrop, image preview, the ledger's account pill and Git backoff |
| `internal/ledger/CLAUDE.md` | discarding an untracked row |
| `internal/claudeconfig/CLAUDE.md` | subscription profiles: context window, output reserve, both stall watchdogs, image denial |
| `internal/featherless/CLAUDE.md` | the Featherless catalog, and the Qwen parser that eats a tool call |
| `internal/rolefix/CLAUDE.md` | the Featherless request/response repair proxy |
| `internal/gptbridge/CLAUDE.md` | the Codex bridge |
| `internal/allin/CLAUDE.md` | the All-In profile |
| `internal/attention/CLAUDE.md` | parked turn, cleared chat |
| `internal/usage/CLAUDE.md` | rollout accounting |

Every file in that table is **audited as production source**:
`isProductionAuditTextPath` matches any tracked path under `internal/`, and
`lib/` is a shipped-text prefix, so `validateShellProductionHostEffectOwnership`
scans every non-blank, non-`#` line of them. A line carrying a bare `say`, a BEL
escape, or an audio marker (`afplay`, `osascript`, `NSSound`,
`AudioServicesPlaySystemSound`, `/System/Library/Sounds`) fails
`TestShellProductionHostEffectOwnershipGuardRejectsBypasses` and
`TestHostEffectOwnershipInventoryRejectsBypasses` — in PROSE, not just in code.
Reword it, or keep that gotcha in this file, which is not an audited path.

### The deck's own overhead outweighed the agents it hosts, and a fork is only half of why

The steady-state tick section below is about forks. Measuring a live
16-session deck end to end
turned up the rest. Over a 181s window, diffing the CPU time of processes alive
at both ends, **wisp-deck's own long-lived processes held 99% of one core
continuously while the 16 Claude agents together held 40%** — the daemons 45%,
the ledgers 26%, the bash watchers 21%, the tmux server 6%.

That is only what survives a sampling interval. Two large costs are short-lived
and invisible to it, measured separately: the statusline at **43% of a core**
(0.433 CPU-s/s across the deck) and tmux's own **client** processes at 49%
(66.6 spawns/s x 7.4ms). Those two overlap each other and the first figure —
the statusline's own `tmux set-environment` calls are counted in both, and
22.5% of the client spawns were the popup backdrop already fixed at HEAD — so
they do not simply add. The safe statement is that overhead exceeded one core
and the agents did not.

Method notes, because getting them wrong sends the work down a blind alley:

- **Measure CPU, never wall clock.** A deck under load sits at 400-500 on 11
  cores, where `go test` ns/op swings 3x for identical work and a 5ms sysctl
  reads as 170ms. Use `getrusage`, bash's `times` builtin, or `ps -o time=`
  deltas.
- **Allocation counts and spawn counts do not lie under load.** Prefer a guard
  that asserts a COUNT over one that asserts a duration; every perf test in
  this repo is shaped that way for this reason.
- **A survivor-diff of the process table misses everything short-lived.** It
  found the daemons and the ledgers and completely missed the statusline,
  which was the second-largest consumer on the machine.
- **The live deck runs older code than HEAD.** The bash lib is a copy synced by
  the installer, resident bash caches functions from session start, and a
  running Go binary is whatever was on disk when its pane launched. Diff the
  installed tree and compare process start times against commit times before
  treating any live reading as a statement about HEAD.

What the fixes had in common is not forking — it is that **a steady-state tick
did work proportional to something it does not own**:

- The supervisor's process-table read rendered an lstart STRING per process on
  the machine (~3700 allocations a call, a quarter of a million a second across
  the deck) for a value only ever compared, and hashed every PID into a set to
  drop a repeat the kernel does not produce.
- The registry poll resolved each process's depth by walking that process's own
  ancestry, re-walking one chain once per process hanging off it — quadratic in
  the supervised tree. Worst case is a deep chain: 1000 processes in a line cost
  61ms a poll, which at 4Hz would be half a core for ONE session. Real trees are
  wide and shallow, where the walk is cheap (1.53ms at 1265 descendants) and the
  cost is instead the registry file it opened for EVERY descendant without
  stopping — ~5ms a tick — although candidates are sorted shallowest first.
- The statusline asked the kernel about one PID at a time, three separate walks
  per render, so a render forked ~7 processes per process in the agent's tree.
- The snapshot heartbeat asked tmux for one session's layout at a time, inside
  a loop over every session, in a tick every session runs.
- keep_awake_tick built a path through a command substitution — a fork with no
  exec, which no PATH-shim spawn guard can see.

**The unit to think in is (per-item cost) x (items) x (tick rate) x (sessions).**
At 2-4Hz across 16 sessions, one item of per-item work is 32-64 of them a
second, and anything per-session is squared across the deck.

End-to-end check on the attention daemon, both binaries run CONCURRENTLY
against private fake sessions so they meet the same load, own CPU over 180s:

  before (4f23c2e)  2.85s  = 1.58% of a core
  after             2.00s  = 1.11% of a core   -> 30% less

That lab daemon supervises one `sleep` and has no registry record to read, so
it isolates the table read alone; a real session pays the tree-shaped costs on
top, which is where the poll fixes land. Per-call figures, which are what the
guards actually pin: the table read went from 3670 allocations and 7.23ms to 21
and 4.81ms, and a poll over a 1000-deep tree from 61ms to 0.94ms.

Known and measured but NOT fixed here, in descending size:

- **Every session's snapshot heartbeat writes a byte-identical file** from the
  same input (tmux state alone), so 15 of 16 are pure waste — ~0.83 of a core.
  Electing one writer is the fix.
- **The installed statusline is not HEAD.** `lib/statusline-setup.sh` only runs
  from `bin/wisp-deck`, so a `git pull` never updates `~/.claude/`; the machine
  measured was still booting `npx` per render (1.048s of CPU against 0.730s for
  the resolved binary).
- **Several sessions on one repository each poll Git independently** — six
  ledgers on the same checkout, ~0.14 of a core of duplicated work.
- **The ledger forks `tmux show-environment` on every tick** to read two
  session-scoped variables (~20% of all tmux client spawns). It cannot simply
  be gated on the relaunch file: the account switch stamps tmux's environment
  WITHOUT rewriting that file, so a stat-gate would miss a switch.
- **`@wd_tab_progress` defeats its own diff-guard.** Progress is rendered at
  second resolution, so a running turn writes a new value every tick, and
  `set-option` is the one tmux verb whose cost scales with the number of
  attached clients (19x at 16 clients).

### A steady-state tick costs (sessions x itself), so nothing in one may fork per session

Every open session runs the same loops forever: the attention watcher at 2Hz,
the Claude supervisor at 4Hz, the ledger at 0.5Hz, the snapshot heartbeat every
10s. On a 17-session deck that is the whole cost model — a single fork added to
a tick is 34 processes a second, and a tick that *enumerates the other sessions*
is quadratic.

Measured on a live 17-session deck (CPU-time deltas, not wall clock, because the
machine sits at load ~380): wisp-deck's own infrastructure burned **60% of one
core continuously — the same as all 17 Claude agents put together**. The rules
that came out of fixing that:

- **The process table is read from the kernel, never from `ps`.**
  `systemProcessTable` (`internal/attention/processtable_darwin.go`) uses
  `unix.SysctlKinfoProcSlice`. The supervisor tick asked for the whole table
  *twice* — once for descendant tracking, once inside the registry poll — and
  each fork cost **80.33 CPU-ms against 5.12**, so 17 sessions kept **16.84 `ps`
  processes resident at all times** and the 250ms poll self-throttled to ~1s.
  Equivalence is proven through the package's own `psSnapshotLine` regex with the
  `ps` snapshot bracketed by two kernel reads: 1835 PIDs, 0 mismatches, and the
  formatted start string is byte-identical to the `procStart` Claude Code writes.
  The kernel reports the kernel task as **PID 0** where `ps -ax` does not, and
  every consumer here rejects a non-positive PID, so it must be skipped.
- **One read per tick, shared.** `refreshTrackedDescendants` returns the table it
  read and `ClaudeRegistryMapper.Processes` takes it. The mapper is built as a
  fresh struct literal inside the poll closure, so nothing can cache across ticks
  by accident — the sharing has to be explicit or it silently doubles again.
- **Never probe per item where one batched probe answers — but the batch is the
  WHOLE TABLE, never `ps -p a,b,c`.** `keep_awake_reap` ran `tr` + `ps -p` *per
  holder file*, and every session reaps every other session's holder twice a
  second: the tick's exec count was literally `28 + 2*(holders+1)`. Batching that
  into one `ps -o pid= -p a,b,c` fixed the exec count and introduced something far
  worse. **The cliff is the NUMBER OF `-p` ARGUMENTS, not the size of the
  machine.** Measured at 805 processes: one pid **3.4ms**, two pids **88ms**,
  three 85ms, four 83ms — flat in list length, and `-p 1,1` (the SAME pid twice)
  costs 188ms. It is a fixed penalty on ps's multi-pid path, almost all of it
  system time. On a box at load 138 with 2002 processes that penalty became
  **7-13s**: at 2Hz per session the probes never drained — 9 `ps` stayed
  resident, one statusline render cost 12-21s (16.89s of it in that single call),
  and opening a session took 10-15s. Every such probe now reads `ps -A` once and
  filters in awk: **22.5ms of CPU against 94ms**, and equivalent on every edge
  (`ps` already deduped a repeated pid, `-A` shows other users' processes and
  zombies, and neither form reports PID 0). Guarded by
  `test/bash/ps_pid_list_probe_test.go`, which tests the SHAPE — it also rejects
  `csv="${pids// /,}"; ps -p "$csv"`, and it scans
  `templates/statusline-wrapper.sh`, whose single-pid fallbacks are the likeliest
  place a list comes back.
  Reading the whole table also retires the old trap: `ps -p` refuses the WHOLE
  list on a single out-of-range PID ("process id too large", verified at 100000
  while 99999 is accepted), which would have reported every other holder dead and
  dropped the sleep veto under every live session. PIDs are still validated, but
  that check is no longer load-bearing — the table probe reports a junk PID as
  absent and the reap removes it anyway. `kill -0` is still not an option
  — it answers EPERM for another user's live process, which reads as dead.
- **A `key=value` file is read in the shell.** `read_settings_value`
  (`lib/tab-title-watcher.sh`) and `keep_awake_enabled` (`lib/keep-awake.sh`) ran
  `grep | head | cut | tr` and `grep` per tick — 13 processes a tick for values
  that almost never change, paid even by users who have the feature off. A
  `while read` loop costs nothing. Reproduce the pipeline exactly when replacing
  one: `head -1` means FIRST match, `tr -d '[:space:]'` means strip the value,
  an anchored regex means a suffixed value does NOT match, and
  `|| [ -n "$line" ]` is required to keep a final line with no trailing newline.
- **A per-session tick must not parse every session.** `write_session_snapshot`
  enumerates every tmux session and used eleven `echo | sed`/`grep` pipelines per
  session to pull its environment apart — 11 processes for one session, 110 for
  ten, in each of 17 sessions every 10 seconds. One `while read` pass over the
  block extracts every field.

Guarded by `internal/attention/processtable_test.go` (including
`TestClaudeRegistryPoll_forks_no_process`, which asserts child CPU time is
*exactly zero* across five polls — a fork always costs some),
`TestClaudeSupervisorTick_reads_the_process_table_once_and_shares_it`,
`test/bash/keep_awake_scaling_test.go`, `test/bash/keep_awake_read_test.go`,
`test/bash/settings_read_forkless_test.go`, and
`test/bash/session_snapshot_scaling_test.go` — the scaling ones compare the spawn
count at one item against twenty, so they fail on the *shape* rather than on a
number that drifts.

### `npx` on a hot path re-resolves the npm graph every time

The statusline wrapper reached ccstatusline through `npx`, and Claude Code
repaints the statusline constantly. Measured, same input, same package:
**5724ms through npx against 2521ms for the binary npx itself resolves to.**
`gt_ccstatusline_cmd` prefers the executable and keeps `npx ccstatusline` as the
fallback for a machine that only has the package. Anything on a repaint or launch
path gets the same treatment — see also the launch critical path in
`lib/CLAUDE.md`.

## Code Conventions

### Avoid Over-Engineering
- Don't add features beyond what's asked
- Don't create helpers for one-time operations
- Three similar lines > premature abstraction
- Only comment where logic isn't self-evident

## Configuration

**DO NOT modify** without explicit request: `run-tests.sh`, `.gitignore`, `VERSION`

