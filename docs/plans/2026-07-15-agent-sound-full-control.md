# Agent Sound Full-Control Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Work directly on the repository's existing branch; this repository explicitly forbids worktrees and additional branches.

**Goal:** Make Wisp Deck the only component allowed to cause automatic attention audio for every Claude, Codex, and OpenCode launch, resume, restore, or switch it manages.

**Architecture:** Apply fail-closed, generation-local shutdown controls at each agent launch; run every agent behind a shared bounded terminal egress filter; replace OpenCode's auto-loaded filesystem plugin with an authenticated loopback `--pure` server/attach supervisor that consumes native SSE events. Preserve Wisp's existing generation-fenced attention protocol and playback locks as the sole automatic sound path.

**Tech Stack:** Go 1.25, Cobra, `creack/pty`, POSIX shell/Bash, `httptest`, tmux integration tests, macOS `codesign`/SHA-256 verification.

**Design:** `docs/plans/2026-07-15-agent-sound-full-control-design.md`

---

## Guardrails

- Do not create a branch, worktree, detached checkout, or isolated Git workflow.
- Preserve unrelated dirty files and stage only task-owned hunks.
- Add every behavior test first, run it red for the intended reason, then implement the smallest passing change.
- Use `apply_patch` for edits.
- Commit each completed task independently.
- If a repository-wide test fails in a user-modified area, prove whether the failure reproduces without the task-owned patch before changing unrelated work.
- Before handoff, run `make install`, verify the installed path/hash/signature, and say that running ledger panes/sessions must be relaunched.

## Task 1: Disable all Claude hooks in the private launch overlay

**Files:**

- Modify: `test/bash/claude_launch_settings_test.go`
- Modify: `lib/settings-json.sh`

**Step 1: Write the failing overlay tests**

Extend the active-config test so the source contains `"disableAllHooks": false` and representative `hooks`, `enabledPlugins`, model, and permission settings. Assert that the generated file:

- forces `preferredNotifChannel` to `notifications_disabled`;
- forces `disableAllHooks` to boolean `true`;
- preserves the hook/plugin objects byte-semantically for settings compatibility, because Claude ignores them under `disableAllHooks`;
- preserves every unrelated selected setting;
- leaves the durable source byte-identical;
- remains mode `0600`.

Extend the no-active-config test to require exactly the two forced keys. Keep the existing atomic-failure test.

**Step 2: Run the tests and confirm red**

Run: `go test ./test/bash -run 'TestSettingsJsonClaudeLaunchSettings' -count=1`

Expected: failure because `disableAllHooks` is absent or remains false.

**Step 3: Implement the strict override**

In `write_claude_launch_settings`, assign `settings["disableAllHooks"] = True` immediately beside the notification-channel override. Do not mutate or strip the source settings.

**Step 4: Run the focused tests**

Run: `go test ./test/bash -run 'TestSettingsJsonClaudeLaunchSettings' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add lib/settings-json.sh test/bash/claude_launch_settings_test.go
git commit -m "fix(claude): disable launch hooks"
```

## Task 2: Prove every Claude relaunch uses the same strict overlay

**Files:**

- Modify: `test/bash/claude_launch_settings_test.go`
- Modify only if a failing test proves a gap: `wrapper.sh`
- Modify only if a failing test proves a gap: `lib/account-switch.sh`
- Modify only if a failing test proves a gap: `lib/tmux-session.sh`

**Step 1: Add launch-path invariant tests**

Add source and executable tests covering:

- initial launch creates the overlay before building the command;
- exact resume and `-c`/fresh fallback steps all reference the same generated `--settings` path;
- account relaunch stages the overlay before generation rotation and publishes it into the new generation;
- agent switch to Claude uses a newly generated overlay, never a deleted prior-generation path;
- failures to parse, stage, validate, move, or chmod the overlay abort before `respawn-pane`;
- the relaunch-context durable source field is kept separately from the ephemeral generated path.

Assert both forced keys by reading the staged/published JSON, not only by source scanning.

**Step 2: Run the tests**

Run: `go test ./test/bash -run 'ClaudeLaunchSettings|Claude.*Relaunch|Relaunch.*Claude|BuildAILaunchCmd.*settings' -count=1`

Expected: PASS if the audited choke point is complete; otherwise fail at the uncovered path.

**Step 3: Close only proven gaps**

If any path fails, route it through the existing `stage_claude_relaunch_settings` / `prepare_claude_relaunch_settings` transaction and `build_ai_launch_cmd`. Do not add a second overlay implementation.

**Step 4: Re-run the focused tests**

Run the command from Step 2.

Expected: PASS.

**Step 5: Commit if files changed**

```bash
git add test/bash/claude_launch_settings_test.go lib/account-switch.sh lib/tmux-session.sh wrapper.sh
git commit -m "test(claude): cover strict relaunch settings"
```

Stage only relevant hunks if shared shell files contain unrelated user edits.

## Task 3: Disable Codex hooks in app-server and TUI argv

**Files:**

- Modify: `internal/codexadapter/supervisor_test.go`
- Modify: `internal/codexadapter/supervisor.go`

**Step 1: Write exact failing argv tests**

Update the server and TUI expected argument arrays so every process receives separate exact pairs for:

```text
-c notify=[]
-c features.hooks=false
```

Keep TUI's private completion discriminator:

```text
-c tui.notifications=["agent-turn-complete"]
-c tui.notification_method="osc9"
-c tui.notification_condition="always"
```

Add negative assertions that neither embedded nor remote/fallback argv omits hook shutdown or enables another notification method.

**Step 2: Run the tests and confirm red**

Run: `go test ./internal/codexadapter -run 'Argv|Supervisor' -count=1`

Expected: exact-argv failures because `features.hooks=false` is missing.

**Step 3: Implement one shared strict config set**

Add a named `features.hooks=false` constant. Use it in both `buildCodexServerArgv` and the TUI config list. Keep each `-c` and value as separate argv entries.

**Step 4: Run focused and race tests**

Run:

```bash
go test ./internal/codexadapter -count=1
go test -race ./internal/codexadapter -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/codexadapter/supervisor.go internal/codexadapter/supervisor_test.go
git commit -m "fix(codex): disable lifecycle hooks"
```

## Task 4: Extract a shared bounded terminal notification filter

**Files:**

- Create: `internal/terminalcontrol/filter.go`
- Create: `internal/terminalcontrol/filter_test.go`
- Modify: `internal/codexadapter/osc9.go`
- Modify: `internal/codexadapter/osc9_filter.go`
- Modify: `internal/codexadapter/osc9_test.go`

**Step 1: Write the shared filter contract tests**

The zero-value or `NewFilter` stream must suppress:

- standalone BEL in ground state;
- plain OSC 9 terminated by BEL;
- plain OSC 9 terminated by ST;
- tmux passthrough-wrapped OSC 9 with BEL or doubled-ST inner terminators;
- the same controls split at every possible boundary and one byte at a time.

It must preserve byte-for-byte:

- printable and UTF-8 text;
- CSI and unrelated ESC sequences;
- OSC 0/2/8 and their BEL terminators;
- non-tmux DCS;
- tmux passthrough of non-OSC-9 controls;
- BEL used strictly as an unrelated OSC terminator.

Add malformed, nested/doubled escape, oversize, recovery-in-same-chunk, retained-memory-bound, and EOF cases. Confirmed incomplete notifications are discarded at EOF; ambiguous prefixes are preserved. Standalone BEL at EOF remains discarded.

Expose filtered notification events to an optional callback or return slice so Codex can still interpret its private `agent-turn-complete` payload. Do not expose ground-state BEL as a semantic event.

**Step 2: Run the new package tests and confirm red**

Run: `go test ./internal/terminalcontrol -count=1`

Expected: package/files do not exist yet.

**Step 3: Move and generalize the existing OSC 9 state machine**

Move the bounded parser/filter implementation from `internal/codexadapter` into `internal/terminalcontrol`. Add ground-state BEL suppression without treating BEL terminators inside unrelated OSC sequences as standalone BEL. Keep a 64 KiB payload bound and bounded candidate state.

Export only the small API adapters need, for example:

```go
type Event struct { Kind Kind; Message string }
type Filter struct { /* bounded state */ }
func (f *Filter) Feed([]byte) (output []byte, events []Event)
func (f *Filter) Flush() []byte
func (f *Filter) RetainedBytes() int
```

**Step 4: Keep Codex compatibility as a thin wrapper**

Either alias the shared event/filter types or adapt them inside Codex so existing reducer/supervisor APIs remain stable. Delete duplicate parsing logic; there must be one output policy implementation.

**Step 5: Run focused tests**

Run:

```bash
go test ./internal/terminalcontrol ./internal/codexadapter -count=1
go test -race ./internal/terminalcontrol ./internal/codexadapter -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/terminalcontrol internal/codexadapter/osc9.go internal/codexadapter/osc9_filter.go internal/codexadapter/osc9_test.go
git commit -m "refactor(terminal): share notification filter"
```

## Task 5: Put Claude behind the shared egress policy

**Files:**

- Modify: `cmd/wisp-deck-tui/screenshot_filter.go`
- Create or modify: `cmd/wisp-deck-tui/screenshot_filter_test.go`
- Modify: `test/bash/tmux_session_settings_test.go`

**Step 1: Add failing unit and real-PTY tests**

Extract the child-output pump so it can be tested with arbitrary readers/writers. Assert that it:

- removes standalone BEL and plain/tmux OSC 9 across tiny reads;
- preserves unrelated terminal bytes;
- flushes valid non-notification prefixes at EOF;
- propagates non-EOF read/write failures.

Add a real PTY/subprocess fixture that writes notification controls plus ordinary text and proves only ordinary text reaches the outer writer. Add a shell invariant that every managed Claude command with an attention generation retains `wisp-deck-tui screenshot-filter --` as its innermost PTY boundary across fresh and resume fallback commands.

**Step 2: Run and confirm red**

Run:

```bash
go test ./cmd/wisp-deck-tui -run 'Screenshot|TerminalNotification' -count=1
go test ./test/bash -run 'BuildAILaunchCmd.*settings|Claude.*Filter' -count=1
```

Expected: output pump test fails because child output is copied raw.

**Step 3: Apply the shared filter**

Replace `io.Copy(os.Stdout, ptmx)` with the tested bounded output pump using `terminalcontrol.Filter`. Preserve raw mode, resizing, signals, child exit status, and the input screenshot rewrite exactly.

For non-interactive stdin, do not bypass the output policy: start the child with stdout/stderr pipes or a PTY-compatible pump so managed agent output still cannot reach the outer terminal raw. Preserve stdout/stderr ordering expectations where practical and document the chosen behavior in tests.

**Step 4: Run focused and race tests**

Run:

```bash
go test ./cmd/wisp-deck-tui ./internal/terminalcontrol -count=1
go test -race ./cmd/wisp-deck-tui ./internal/terminalcontrol -count=1
go test ./test/bash -run 'BuildAILaunchCmd.*settings|Claude.*Filter' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/wisp-deck-tui/screenshot_filter.go cmd/wisp-deck-tui/screenshot_filter_test.go test/bash/tmux_session_settings_test.go
git commit -m "fix(claude): filter terminal notifications"
```

## Task 6: Define OpenCode's strict launch contract and private TUI config

**Files:**

- Create: `internal/opencodeadapter/config.go`
- Create: `internal/opencodeadapter/config_test.go`
- Create: `internal/opencodeadapter/argv.go`
- Create: `internal/opencodeadapter/argv_test.go`
- Create: `cmd/wisp-deck-tui/opencode_adapter.go`
- Create: `cmd/wisp-deck-tui/opencode_adapter_test.go`

**Step 1: Write failing config and argv tests**

Require an atomic mode-`0600` `opencode-tui.json` inside the current generation directory with a minimal attention object that forces:

```json
{
  "attention": {
    "enabled": false,
    "notifications": false,
    "sound": false
  }
}
```

Test replacement, temp cleanup, parent/generation validation, symlink/non-directory rejection, and fail-closed write errors.

Define exact argv builders with one validated OpenCode command prefix (`opencode`, or the Wisp-resolved fixed `npx` prefix):

```text
<prefix> --pure serve --hostname 127.0.0.1 --port <port>
<prefix> --pure attach http://127.0.0.1:<port> --dir <physical-project> --password <secret>
```

Attach must append exactly one of fresh, `--continue`, `--session <id>`, and an optional single initial prompt according to the existing Wisp resume/handoff contract. The attach environment must set `OPENCODE_TUI_CONFIG` to the private file and must not inherit an attacker-supplied replacement value.

Validate absolute state path, generation ownership, physical cwd, positive bounded port, non-empty high-entropy password, and a whitelisted executable/prefix representation. Reject shell strings that require evaluation.

**Step 2: Run and confirm red**

Run: `go test ./internal/opencodeadapter ./cmd/wisp-deck-tui -run 'OpenCode|Opencode' -count=1`

Expected: package/command absent.

**Step 3: Implement config/argv/CLI validation only**

Create the Cobra subcommand and option struct, but inject the runner so supervisor implementation can follow in later tasks. Keep all command execution as exact argv; never invoke `bash -c` for the OpenCode prefix.

**Step 4: Run focused tests**

Run: `go test ./internal/opencodeadapter ./cmd/wisp-deck-tui -run 'OpenCode|Opencode' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/opencodeadapter/config.go internal/opencodeadapter/config_test.go internal/opencodeadapter/argv.go internal/opencodeadapter/argv_test.go cmd/wisp-deck-tui/opencode_adapter.go cmd/wisp-deck-tui/opencode_adapter_test.go
git commit -m "feat(opencode): define silent adapter launch"
```

## Task 7: Port the OpenCode semantic reducer to Go

**Files:**

- Create: `internal/opencodeadapter/event.go`
- Create: `internal/opencodeadapter/event_test.go`
- Create: `internal/opencodeadapter/reducer.go`
- Create: `internal/opencodeadapter/reducer_test.go`
- Reference during port, then retire later: `templates/opencode-plugin.ts`

**Step 1: Translate the reducer fixtures before implementation**

Create table/sequence tests matching the current plugin contract for:

- session create/update/delete and parent/root correlation;
- busy/retry/idle completion arming;
- root versus child-session completion;
- question asked/replied/rejected;
- permission asked/replied;
- supported errors, duplicate event IDs, error-before-session correlation, and idle suppression after an error;
- question priority over permission, then error, completion, working, ready, unknown;
- unannounced identity handling so repeated waiting state does not replay sound;
- malformed schemas, cycles, unresolved parents, unknown event types, excessive identifiers/strings/nesting/model counts, and serial/error-identity overflow.

Use representative JSON envelopes taken from the current TypeScript tests/fixtures and assert exact `attention.State` phase, reason, and identity transitions.

**Step 2: Run and confirm red**

Run: `go test ./internal/opencodeadapter -run 'Event|Reducer' -count=1`

Expected: missing implementation.

**Step 3: Implement bounded decoding and reduction**

Port the approved `templates/opencode-plugin.ts` model without semantic shortcuts. Decode with a bounded reader, reject trailing JSON, validate all discriminated event schemas, and cap all maps/slices. Feed accepted states to the existing `attention.AtomicWriter`; do not add any playback or plugin call.

**Step 4: Differential-check important sequences**

Use the existing Node plugin harness while it still exists to confirm the Go reducer yields identical protocol records for a compact corpus of success, question, permission, error, child-session, duplicate, and malformed sequences.

**Step 5: Run focused and race tests**

Run:

```bash
go test ./internal/opencodeadapter -count=1
go test -race ./internal/opencodeadapter -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/opencodeadapter/event.go internal/opencodeadapter/event_test.go internal/opencodeadapter/reducer.go internal/opencodeadapter/reducer_test.go
git commit -m "feat(opencode): reduce native attention events"
```

## Task 8: Implement the authenticated bounded SSE observer

**Files:**

- Create: `internal/opencodeadapter/observer.go`
- Create: `internal/opencodeadapter/observer_test.go`

**Step 1: Write failing `httptest` coverage**

Test that the observer:

- requests only loopback `/event` with the per-generation authentication secret;
- requires a successful event-stream response and rejects redirects/wrong content types;
- parses `data:` records across every read split, CRLF/LF framing, comments, and multi-line data according to the server's actual SSE form;
- sends each complete JSON event to the reducer in wire order;
- enforces response-header, line, event, and total retained-byte limits;
- rejects invalid UTF-8/JSON, trailing data, oversized events, and unsupported redirects;
- has setup/read/idle cancellation behavior that cannot hang launch or teardown;
- closes promptly on generation cancellation/server exit.

**Step 2: Run and confirm red**

Run: `go test ./internal/opencodeadapter -run 'Observer|SSE' -count=1`

Expected: missing implementation.

**Step 3: Implement the observer**

Use a private `http.Client` with no proxy, no redirect following, bounded headers, explicit timeouts, and a loopback-only URL constructed by the supervisor. Authenticate exactly as OpenCode's `--password` server requires. Parse incrementally without `bufio.Scanner`'s implicit/default limits.

**Step 4: Run focused and race tests**

Run:

```bash
go test ./internal/opencodeadapter -run 'Observer|SSE' -count=1
go test -race ./internal/opencodeadapter -run 'Observer|SSE' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/opencodeadapter/observer.go internal/opencodeadapter/observer_test.go
git commit -m "feat(opencode): observe authenticated events"
```

## Task 9: Supervise OpenCode server, observer, and attach PTY as one generation

**Files:**

- Create: `internal/opencodeadapter/supervisor.go`
- Create: `internal/opencodeadapter/supervisor_test.go`
- Modify: `cmd/wisp-deck-tui/opencode_adapter.go`
- Modify: `cmd/wisp-deck-tui/opencode_adapter_test.go`

**Step 1: Write lifecycle tests with injected fakes**

Cover:

- cryptographically random secret per run;
- loopback port reservation/retry without accepting port 0 fallback behavior;
- server starts before observer and attach;
- launch waits for authenticated observer readiness and private TUI config before attach;
- attach output always passes through `terminalcontrol.Filter`;
- stdin, raw mode, SIGWINCH, termination signals, exit codes, and PTY sizes propagate;
- server/observer/attach teardown as one process-group-owned generation;
- setup failure never starts attach;
- observer/server failure cancels attach and publishes unknown where possible;
- attach exit stops server and returns exact exit/signal status;
- stale-generation writer errors terminate observation instead of recreating state;
- resume/fresh behavior remains bounded and deterministic.

Add a real subprocess/PTY fixture where fake server/attach executables emit BEL and plain/tmux OSC 9; prove no notification bytes reach the outer writer.

**Step 2: Run and confirm red**

Run: `go test ./internal/opencodeadapter ./cmd/wisp-deck-tui -run 'Supervisor|OpenCodeAdapter|OpencodeAdapter' -count=1`

Expected: missing supervisor/runner behavior.

**Step 3: Implement the supervisor**

Follow the tested Codex supervisor's process-group, PTY, raw-mode, signal-router, output-pump, and exit-result patterns where semantics match. Do not share mutable global state between generations. Write errors only to the configured private log sink.

Start the server with an exact environment supplying the per-generation password, then connect the authenticated SSE observer, publish initial semantic state, and finally start `--pure attach` with the strict TUI config environment. Fail closed on any missing control.

**Step 4: Wire the Cobra runner**

Resolve the physical cwd, validate all paths/options, and invoke the supervisor. Preserve nonzero child exit status without converting it into a fresh uncontrolled shell launch.

**Step 5: Run focused and race tests**

Run:

```bash
go test ./internal/opencodeadapter ./cmd/wisp-deck-tui -count=1
go test -race ./internal/opencodeadapter ./cmd/wisp-deck-tui -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/opencodeadapter/supervisor.go internal/opencodeadapter/supervisor_test.go cmd/wisp-deck-tui/opencode_adapter.go cmd/wisp-deck-tui/opencode_adapter_test.go
git commit -m "feat(opencode): supervise pure attention runtime"
```

## Task 10: Route every Wisp-managed OpenCode path through the adapter

**Files:**

- Modify: `lib/ai-tools.sh`
- Modify: `lib/tmux-session.sh`
- Modify: `lib/account-switch.sh`
- Modify: `wrapper.sh`
- Modify: `test/bash/tmux_session_proxy_test.go`
- Modify: `test/bash/tool_switch_pool_test.go`
- Modify: `test/bash/restore_surplus_test.go`
- Modify or create: `test/bash/opencode_launch_test.go`

**Step 1: Write failing shell contract tests**

Prove that initial, fresh handoff, `--continue`, restore, account relaunch, and tool-switch OpenCode commands all invoke:

```text
wisp-deck-tui opencode-adapter --opencode ... --state-file ... --generation ...
```

Assert no managed path executes raw `opencode`, `npx ... opencode-ai`, `serve`, or `attach` outside the adapter. Test direct and both existing fixed npx resolution modes as exact argv without `eval`/`bash -c`. Preserve hostile project paths, session IDs, and handoff prompts as single arguments.

Change preflight expectations: OpenCode launch readiness depends on the installed adapter and generation runtime, not filesystem-plugin installation. A failed adapter/config preflight must preserve the currently running generation before `respawn-pane`.

**Step 2: Run and confirm red**

Run:

```bash
go test ./test/bash -run 'Open[Cc]ode|Opencode|RelaunchSwitchTool.*opencode|Restore.*opencode' -count=1
```

Expected: raw OpenCode launch/plugin-install expectations fail.

**Step 3: Replace string command resolution with exact prefix metadata**

Keep the inexpensive availability probe. Make the resolver provide a small validated launcher mode or exact argv encoding understood by `opencode-adapter`; do not interpret arbitrary shell strings. Ensure relaunch context persists that safe representation and `_tool_cmd_for` resolves it on demand for tool switches.

**Step 4: Update the single launch builder choke point**

In `build_ai_launch_cmd`, handle OpenCode before the raw legacy builder when attention generation/state are present. Emit only the adapter command with generation-fenced paths and explicit fresh/resume/handoff arguments. Reject missing attention runtime instead of falling back to raw OpenCode for Wisp-managed launches.

Update wrapper and account-switch preflights to require the installed adapter capability. Keep generation rotation transactional: prepare all adapter inputs before fencing the old generation.

**Step 5: Run focused shell and Go tests**

Run:

```bash
go test ./test/bash -run 'Open[Cc]ode|Opencode|RelaunchSwitchTool.*opencode|Restore.*opencode' -count=1
go test ./internal/opencodeadapter ./cmd/wisp-deck-tui -count=1
```

Expected: PASS.

**Step 6: Commit carefully**

```bash
git add -p lib/tmux-session.sh wrapper.sh lib/account-switch.sh lib/ai-tools.sh
git add test/bash/tmux_session_proxy_test.go test/bash/tool_switch_pool_test.go test/bash/restore_surplus_test.go test/bash/opencode_launch_test.go
git commit -m "fix(opencode): route launches through pure adapter"
```

Do not stage unrelated ledger/hover hunks.

## Task 11: Retire only positively identified legacy OpenCode plugins

**Files:**

- Modify: `lib/install.sh`
- Modify: `test/bash/install_test.go`
- Modify: `test/npx/install_integrity_test.go`
- Delete after native parity is proven: `templates/opencode-plugin.ts`
- Delete or replace obsolete tests: `test/bash/opencode_plugin_test.go`
- Modify: `wrapper.sh`
- Modify: `test/bash/wrapper_layout_test.go`

**Step 1: Write migration safety tests**

Replace installation tests with a retirement helper contract:

- remove `wisp-deck.ts` only when it is byte-identical to a shipped legacy version or matches a complete, collision-resistant Wisp marker/fingerprint set;
- remove known legacy `ghost-tab.ts` audio-plugin variants only when positively identified;
- preserve unknown/customized regular files byte-for-byte;
- preserve symlinks, directories, devices, and unknown entries;
- tolerate missing plugin directories;
- never recursively delete the user plugin directory;
- make repeat invocation idempotent and race-safe.

Test both `${XDG_CONFIG_HOME}` and default `~/.config`. Add an integrity fixture for every recognized legacy digest so package installation retains the migration knowledge after deleting the executable plugin template.

**Step 2: Run and confirm red**

Run:

```bash
go test ./test/bash -run 'OpenCodePlugin|OpencodePlugin|Wrapper.*Plugin' -count=1
go test ./test/npx -run 'InstallIntegrity' -count=1
```

Expected: old installer still writes `wisp-deck.ts`.

**Step 3: Implement conservative retirement**

Rename `install_opencode_plugin` to a migration/retirement helper or leave a compatibility wrapper that only retires known files. Use fixed SHA-256 digests plus structural checks where necessary. Never delete based on filename alone.

Remove wrapper launch-time plugin installation. `ensure_opencode` may run the idempotent retirement after confirming the CLI exists, but retirement failure must be surfaced without re-enabling plugin execution. Unknown plugins remain inert because every managed server/client uses `--pure`.

Delete the obsolete TypeScript runtime and Node behavior test only after the Go differential corpus from Task 7 passes.

**Step 4: Run focused tests**

Run the commands from Step 2.

Expected: PASS.

**Step 5: Commit**

```bash
git add lib/install.sh test/bash/install_test.go test/npx/install_integrity_test.go test/bash/opencode_plugin_test.go test/bash/wrapper_layout_test.go wrapper.sh templates/opencode-plugin.ts
git commit -m "fix(opencode): retire audio plugin escape paths"
```

## Task 12: Add source invariants for every forbidden automatic sound path

**Files:**

- Create or modify: `test/bash/sound_ownership_source_test.go`
- Modify as required by existing docs layout: `README.md`
- Modify as required: `CLAUDE.md`
- Modify as required: `docs/ARCHITECTURE.md`

**Step 1: Add failing repository-source guards**

Scan production launch/install sources, excluding tests/docs/vendor/build artifacts, and fail on:

- agent-owned `afplay`, `say`, `osascript` sound, or equivalent playback outside Wisp's approved playback modules;
- Claude launch overlays missing either strict key;
- Codex server/TUI argv missing `notify=[]` or `features.hooks=false`;
- OpenCode managed argv without `--pure`, loopback, authentication, and `OPENCODE_TUI_CONFIG`;
- any production call that installs or auto-loads the obsolete OpenCode event plugin;
- raw managed agent PTY output copied directly to outer stdout;
- raw OpenCode launch from wrapper/relaunch/session-builder paths;
- new notification method/hook/plugin enablement in managed launch code.

Use narrowly scoped semantic patterns and explicit allowlists so comments/docs do not cause noise and a legitimate Wisp preview/playback path remains allowed.

**Step 2: Run and confirm intended failures before final wiring**

Run: `go test ./test/bash -run 'SoundOwnershipSource|AgentSoundSource' -count=1`

Expected: fail for any remaining old path; never weaken the guard to hide a real path.

**Step 3: Close remaining source paths and document the boundary**

Update user docs to state that Wisp owns automatic attention sound for managed Claude/Codex/OpenCode launches, explicit agent audio commands remain user-authorized tool actions, and a running session must be relaunched after upgrades.

**Step 4: Run focused tests**

Run:

```bash
go test ./test/bash -run 'SoundOwnershipSource|AgentSoundSource' -count=1
go test ./test/bash -run 'Notification|Sound|ClaudeLaunchSettings|Codex|Open[Cc]ode' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add test/bash/sound_ownership_source_test.go README.md CLAUDE.md docs/ARCHITECTURE.md
git commit -m "test(sound): guard exclusive notification ownership"
```

## Task 13: Exhaustive verification, install, and handoff

**Files:**

- Modify only if verification exposes a task-owned bug.

**Step 1: Format and static-check changed files**

Run:

```bash
gofmt -w cmd/wisp-deck-tui internal/opencodeadapter internal/terminalcontrol internal/codexadapter
git diff --check
go vet ./cmd/wisp-deck-tui ./internal/opencodeadapter ./internal/terminalcontrol ./internal/codexadapter ./internal/attention
```

Expected: PASS. Review `git diff --stat` and `git diff` to confirm no unrelated hunks were staged or overwritten.

**Step 2: Run focused race tests**

Run:

```bash
go test -race ./internal/opencodeadapter ./internal/terminalcontrol ./internal/codexadapter ./internal/attention ./cmd/wisp-deck-tui -count=1
go test ./test/bash -run 'Notification|Sound|ClaudeLaunchSettings|Codex|Open[Cc]ode|Opencode|BuildAILaunchCmd|Relaunch|Restore' -count=1
go test ./test/npx -count=1
```

Expected: PASS.

**Step 3: Run the authoritative suite and build**

Run:

```bash
make test
make build
```

Expected: PASS. If a failure is in unrelated dirty user work, capture the exact command/output and establish non-causation; do not edit that work.

**Step 4: Perform an executable OpenCode integration**

With a fake or installed OpenCode-compatible server/attach harness, prove end to end that:

- server and attach argv include `--pure`;
- authentication is required;
- `OPENCODE_TUI_CONFIG` is the private silent file;
- representative SSE busy/idle, question, permission, and error events advance the generation state;
- filesystem plugin fixtures are not loaded;
- BEL and plain/tmux OSC 9 emitted by attach never appear at the outer PTY.

Run the equivalent real-PTY integration for Claude and Codex notification controls.

**Step 5: Install locally**

Run: `make install`

Expected: build, ad-hoc signing, copy, re-signing, and version smoke test succeed.

**Step 6: Verify installed identity**

Run:

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 bin/wisp-deck-tui
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: both tests and both signatures succeed.

**Step 7: Final repository audit**

Run:

```bash
git status --short
git log --oneline --decorate -15
```

Confirm only pre-existing unrelated user changes remain uncommitted. Mark the active goal complete only after all required evidence is present.

**Step 8: Handoff**

Report the three strict controls, tests executed, installation path/hash/signature verification, any unrelated test contamination, and the need to relaunch every running ledger pane/Wisp session so its agent process and PTY boundary pick up the new installed binary.
