# Semantic Agent Attention Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace heuristic attention detection with generation-fenced semantic adapters for Claude Code, Codex, and OpenCode.

**Architecture:** A private per-Wisp-session runtime owns one immutable state file per launch generation and an atomic descriptor naming the current tool/file. Claude and Codex use compiled helpers inside `wisp-deck-tui`; OpenCode publishes from its event plugin. The existing shell watcher becomes the only per-tab consumer and alerts once per semantic attention sequence.

**Tech Stack:** Go 1.25, Cobra, `creack/pty`, `coder/websocket` v1.8.15, Bash 3.2-compatible shell, tmux, TypeScript-valid JavaScript, Node/Bun, Go tests, shellcheck.

---

## Baseline note

The pre-edit full suite was run twice. The first invocation mistakenly had a
PTY; the second correctly used pipes. Both were overwhelmed by pre-existing,
orphaned one-day-old CPU stress loops from an earlier repository load-test and
the Bash package hit its 10-minute timeout. Failures were broad timing/PTY
symptoms and occurred before this branch changed runtime code. Do not classify
those as feature regressions. Every task below still requires an isolated
focused red/green run. Final completion still requires a fresh full suite after
the external load is removed or authorization is given to terminate it.

### Task 1: Normalized state protocol

**Files:**

- Create: `internal/attention/state.go`
- Create: `internal/attention/state_test.go`

**Step 1: Write failing parser tests**

Cover the exact record, invalid field count/version/generation/sequence,
unsupported phase/reason pairs, tabs/newlines in generation, and a maximum
record size:

```go
func TestParseState(t *testing.T) {
    got, err := ParseState([]byte("1\tg-1\t7\tattention\tquestion\n"))
    if err != nil {
        t.Fatal(err)
    }
    want := State{Generation: "g-1", Sequence: 7,
        Phase: PhaseAttention, Reason: ReasonQuestion}
    if got != want {
        t.Fatalf("got %#v, want %#v", got, want)
    }
}
```

Use standard-library assertions rather than adding a test framework.

**Step 2: Run the tests and observe the missing-package failure**

Run:

```bash
go test ./internal/attention -run 'TestParseState' -count=1
```

Expected: FAIL because the package/API does not exist.

**Step 3: Implement the value types and parser**

Provide:

```go
type Phase string
type Reason string

type State struct {
    Generation string
    Sequence   uint64
    Phase      Phase
    Reason     Reason
    Identity   string // in-memory dedupe only; never serialized
}

func ParseState([]byte) (State, error)
func (State) MarshalText() ([]byte, error)
```

The on-disk form is exactly
`1\t<generation>\t<sequence>\t<phase>\t<reason>\n`. Use `-` when phase is
not attention. Reject malformed records; never coerce them.

**Step 4: Write and observe failing atomic-writer tests**

Test mode 0600, same-directory rename, sequence resumption for a matching
generation, semantic/identity dedupe, no parent creation, and concurrent readers
never observing a partial record.

Run:

```bash
go test ./internal/attention -run 'TestAtomicWriter' -count=1
```

Expected: FAIL because `AtomicWriter` is missing.

**Step 5: Implement the writer**

Provide:

```go
type AtomicWriter struct { /* private path/generation/last state */ }
func NewAtomicWriter(path, generation string) (*AtomicWriter, error)
func (w *AtomicWriter) Publish(phase Phase, reason Reason, identity string) error
func (w *AtomicWriter) Current() State
```

Use `os.CreateTemp(parent, ".attention-*")`, chmod 0600, write/close, then
`os.Rename`. Never call `MkdirAll`. Treat an absent parent as a terminal stale
generation error that callers may silently stop on.

**Step 6: Run focused tests and commit**

```bash
go test ./internal/attention -count=1
git add internal/attention/state.go internal/attention/state_test.go
git commit -m "feat(attention): add state protocol"
```

### Task 2: Generation-fenced shell runtime

**Files:**

- Create: `lib/attention.sh`
- Create: `test/bash/attention_runtime_test.go`
- Modify: `wrapper.sh`
- Modify: `lib/account-switch.sh`
- Modify: `test/bash/account_switch_test.go`
- Modify: `test/bash/tool_switch_pool_test.go`

**Step 1: Write failing runtime tests**

Specify Bash 3.2-compatible APIs:

```text
attention_session_create <tmp-base>
attention_begin_generation <root> <tool>
attention_read_descriptor <descriptor>
attention_cleanup <root>
```

`attention_begin_generation` sets/prints the generation, state path, and
descriptor path; it initializes state to `unknown/-`; descriptor and state are
0600; root/generation directories are 0700. Test two sessions never collide,
rotation changes the path, old-parent removal fences a simulated late writer,
and descriptor readers see either complete old or complete new data.

**Step 2: Run and observe RED**

```bash
go test ./test/bash -run 'TestAttentionRuntime' -count=1
```

Expected: FAIL because `lib/attention.sh` is absent.

**Step 3: Implement the shell runtime**

Use `mktemp -d`, `umask 077`, sibling temp files, and `mv`. Descriptor format is
`1\t<generation>\t<tool>\t<state-file>\n`. Reject tools outside
`claude|codex|opencode`; reject fields containing tabs/newlines. The adapter
parent directory must be created only here.

**Step 4: Write failing lifecycle-order tests**

Assert that wrapper initial generation exists before `build_ai_launch_cmd`, and
that account/tool switches perform this order:

```text
begin generation
set WISP_DECK_TOOL / GENERATION / STATE_FILE / DESCRIPTOR
build command
respawn-pane
```

Assert the relaunch context carries the attention root/descriptor and both
respawn paths rotate even when the tool name is unchanged.

**Step 5: Integrate lifecycle minimally**

Source `attention.sh` before `account-switch.sh`. Replace
`WISP_DECK_MARKER_FILE` tmux environment with:

```text
WISP_DECK_ATTENTION_ROOT
WISP_DECK_ATTENTION_DESCRIPTOR
WISP_DECK_ATTENTION_GENERATION
WISP_DECK_ATTENTION_FILE
```

Create the initial generation before launch-command construction. Add attention
fields to relaunch-context parsing/writing. Rotate and stamp tmux environment
before account and tool respawns. Cleanup removes the session root after
terminating the tmux session and helper PIDs.

**Step 6: Run focused tests and commit**

```bash
go test ./test/bash -run 'TestAttentionRuntime|TestAccountSwitch|TestToolSwitch' -count=1
shellcheck wrapper.sh lib/attention.sh lib/account-switch.sh
git add lib/attention.sh wrapper.sh lib/account-switch.sh test/bash/attention_runtime_test.go test/bash/account_switch_test.go test/bash/tool_switch_pool_test.go
git commit -m "feat(attention): fence launch generations"
```

### Task 3: Semantic common consumer

**Files:**

- Modify: `lib/tab-title-watcher.sh`
- Modify: `test/bash/tab_title_watcher_test.go`
- Modify: `test/bash/keep_awake_test.go`
- Modify: `test/bash/live_settings_test.go`

**Step 1: Replace heuristic tests with failing protocol tests**

Delete tests that require markers, `-ask`, cooldown files, Claude spinner text,
generic prompt regexes, or OpenCode direct title/sound behavior. Add tests for:

- strict descriptor/state parsing;
- unknown/malformed/missing/stale state never alerting;
- one alert per `(generation, sequence)`;
- a second question identity alerting at the next sequence;
- generation rotation resetting dedupe without alerting initial ready;
- dynamic tool changes affecting sound/title/theme;
- `working|unknown` mapping to active keep-awake;
- `ready|attention` mapping to idle keep-awake;
- unique `@gt_ai=1` stable pane-ID discovery after the watcher starts.

**Step 2: Run and observe expected failures**

```bash
go test ./test/bash -run 'TestTabTitleWatcher|TestKeepAwake' -count=1
```

**Step 3: Implement the consumer**

Change the API to:

```text
start_tab_title_watcher <session> <project> <title-mode> <tmux> <descriptor> <config-dir>
stop_tab_title_watcher
```

Each tick re-read descriptor and state. Resolve the pane using
`list-panes -F '#{pane_id}\t#{@gt_ai}'` and require exactly one `1`. Keep
`last_generation`, `last_sequence`, `last_phase`, and current tool. Alert only
on a new attention sequence. No `capture-pane` call may classify attention.

**Step 4: Run focused tests, shellcheck, and commit**

```bash
go test ./test/bash -run 'TestTabTitleWatcher|TestKeepAwake|TestLiveSettings' -count=1
shellcheck lib/tab-title-watcher.sh wrapper.sh
git add lib/tab-title-watcher.sh wrapper.sh test/bash/tab_title_watcher_test.go test/bash/keep_awake_test.go test/bash/live_settings_test.go
git commit -m "feat(attention): consume semantic state"
```

### Task 4: Claude foreground adapter

**Files:**

- Create: `internal/attention/claude_registry.go`
- Create: `internal/attention/claude_registry_test.go`
- Create: `internal/attention/claude_reducer.go`
- Create: `internal/attention/claude_reducer_test.go`
- Create: `internal/attention/claude_supervisor.go`
- Create: `internal/attention/claude_supervisor_test.go`
- Create: `cmd/wisp-deck-tui/claude_attention.go`
- Create: `cmd/wisp-deck-tui/claude_attention_test.go`
- Create: `test/bash/claude_attention_launch_test.go`
- Modify: `lib/tmux-session.sh`

**Step 1: Write failing registry-mapper tests**

Inject a process snapshot and filesystem. Cover direct child, shell/filter/Claude
ancestry, UTC start validation, PID reuse, background-kind rejection, corrupt
JSON, shallowest winner, equal-depth ambiguity, process disappearance, and
unknown statuses.

**Step 2: Implement strict registry mapping**

Run one `LC_ALL=C TZ=UTC ps -axo pid=,ppid=,lstart=` snapshot per poll. Search
only `<exact-config-dir>/sessions/<descendant-pid>.json`. Accept exactly one
valid shallowest interactive record whose `.pid`, filename PID, `.procStart`,
and live ancestry match.

**Step 3: Write failing reducer table tests**

Cases:

```text
initial idle -> ready
busy -> working/armed
armed busy->idle -> attention/done
waiting -> attention/question or permission
waiting->idle -> retain existing attention
busy after attention -> working and re-arm
temporary unknown -> retain attention/armed state, otherwise unknown
unexpected nonzero exit -> error
signalled exit -> no new attention
```

**Step 4: Implement the pure reducer**

Reducer output includes phase/reason plus an identity such as
`done:<turn-counter>`, `waiting:<statusUpdatedAt>`, or `error:<exit>`. Repeated
observations must not advance writer sequence.

**Step 5: Write failing supervisor/Cobra tests**

Test inherited stdio, one wrapper around the complete fallback chain, exact
config root, polling injection, deep-first descendant signalling, signal-caused
exit suppression, exit-code preservation, required flags, and malformed
generation rejection.

The CLI is:

```text
wisp-deck-tui claude-attention \
  --state-file FILE --generation GEN --config-dir DIR \
  -- bash -c '<existing complete chain>'
```

**Step 6: Implement supervisor and outer shell wrapper**

Start the child with parent stdin/stdout/stderr and no new process group. Poll
while it lives. On TERM/HUP/INT/QUIT, snapshot descendants, signal deepest first,
wait briefly, kill survivors, and preserve the child exit result. Do not allocate
another PTY around the existing screenshot filter.

Refactor `build_ai_launch_cmd` into raw construction plus one outer Claude
wrapper when attention variables are present. Preserve the exact resume fallback
chain and all quoting.

**Step 7: Run focused tests and commit**

```bash
go test ./internal/attention ./cmd/wisp-deck-tui -run 'Claude|State' -count=1
go test ./test/bash -run 'ClaudeAttention|TmuxSession|CodexLaunch' -count=1
shellcheck lib/tmux-session.sh
git add internal/attention cmd/wisp-deck-tui/claude_attention.go cmd/wisp-deck-tui/claude_attention_test.go lib/tmux-session.sh test/bash/claude_attention_launch_test.go
git commit -m "feat(claude): track semantic attention"
```

### Task 5: Claude hook migration and account-global jobs

**Files:**

- Create: `internal/attention/claude_background.go`
- Create: `internal/attention/claude_background_test.go`
- Create: `cmd/wisp-deck-tui/claude_background.go`
- Create: `cmd/wisp-deck-tui/claude_background_test.go`
- Modify: `lib/settings-json.sh`
- Modify: `lib/notification-setup.sh`
- Modify: `wrapper.sh`
- Modify: related settings/notification tests

**Step 1: Write failing migration/settings tests**

Assert runtime no longer calls `add_waiting_indicator_hooks`, global notification
channel leasing, or marker cleanup. Preserve the removal helper so upgrade can
delete all historical Wisp/Ghost Tab marker hooks without touching user hooks.

Generate a launch-local settings JSON in the current generation. Merge an active
Wisp settings file when present and force only
`preferredNotifChannel=terminal_bell`; never mutate the user's global setting.

**Step 2: Implement migration and session-local settings**

Use an atomic generated file and pass it to Claude's existing `--settings`
position. On launch, best-effort remove historical Wisp hooks once. Delete the
marker/notification lease lifecycle from wrapper cleanup.

**Step 3: Write failing background reducer/broker tests**

Parse injected `claude agents --json --all` output. Key by exact config root and
job ID. Initial terminal jobs are baseline only; initial/current blocked jobs
need attention. New transitions to blocked, completed, failed, or stopped each
emit once; repeated polling and broker handoff do not duplicate. Ignore raw
session `.status` for background jobs.

**Step 4: Implement account-global monitoring**

Run one broker candidate per exact config root for every live Wisp wrapper that
has used that root. Elect one with an atomic private lock containing PID and
process-start identity; candidates retry so leadership transfers when a session
closes. Poll the official CLI at a low frequency and persist dedupe state under
the Wisp config directory. Emit one macOS notification labelled “Claude
background” and use the Claude sound preference; never retitle an arbitrary
interactive pane. Broker failure is silent and retries.

**Step 5: Run tests, shellcheck, and commit**

```bash
go test ./internal/attention ./cmd/wisp-deck-tui -run 'ClaudeBackground' -count=1
go test ./test/bash -run 'SettingsJson|NotifChannel|ClaudeAttention' -count=1
shellcheck wrapper.sh lib/settings-json.sh lib/notification-setup.sh
git add internal/attention cmd/wisp-deck-tui/claude_background.go cmd/wisp-deck-tui/claude_background_test.go lib/settings-json.sh lib/notification-setup.sh wrapper.sh test/bash
git commit -m "feat(claude): monitor background jobs"
```

### Task 6: OpenCode semantic plugin and installer

**Files:**

- Replace: `templates/opencode-plugin.ts`
- Replace: `test/bash/opencode_plugin_test.go`
- Create: `test/js/opencode_plugin_test.mjs`
- Modify: `lib/install.sh`
- Modify: `bin/wisp-deck`
- Modify: `test/bash/install_test.go`

**Step 1: Write executable failing plugin tests**

Keep the `.ts` template valid plain JavaScript. The Go test copies it to a temp
`.mjs` and runs the Node assertion script. Test exact atomic TSV output, missing
parent behavior, semantic sequence/identity dedupe, and plugin inertness when
either Wisp env variable is absent.

**Step 2: Write reducer tests before implementation**

Cover immediate question/permission, request-ID dedupe, reply/reject clearing,
child requests, root busy/retry arming, armed root idle done, initial idle,
child idle, root error plus idle suppression, unknown parent, and deletion.

**Step 3: Write hydration tests**

Use a real local HTTP server. Verify session/status hydration, authenticated
`/question` and `/permission` GETs with directory query, structural validation,
all-or-nothing snapshots, and epoch protection against a delayed snapshot
overwriting a live event.

Run after each test addition:

```bash
go test ./test/bash -run 'TestOpencodePlugin' -count=1
```

Expected: RED against the existing debounce/spinner plugin.

**Step 4: Implement the plugin**

Maintain parent mapping, armed roots, terminal latches, pending request maps,
hydration epoch, and reliability. Precedence is question, permission, error,
done, working, unknown, ready. Handle raw v2 events structurally; do not import
the stale legacy event union. Remove all sound, OSC, spinner, timer,
`tool.execute.after`, and deprecated `session.idle` code.

**Step 5: Write failing installer tests and implement sync**

Test XDG destination, stale replacement, identical mtime preservation, mode
0600, physical distribution-root resolution through symlinked `lib`, and every
successful `ensure_opencode` branch. Add `install_opencode_plugin`; copy to a
sibling temp and rename. Run it during setup regardless of selected tool and on
every `ensure_opencode` success.

**Step 6: Verify and commit**

```bash
go test ./test/bash -run 'TestOpencodePlugin|TestEnsureOpencode|TestInstallOpencode' -count=1
shellcheck lib/install.sh bin/wisp-deck
git add templates/opencode-plugin.ts test/js/opencode_plugin_test.mjs test/bash/opencode_plugin_test.go test/bash/install_test.go lib/install.sh bin/wisp-deck
git commit -m "feat(opencode): publish semantic attention"
```

### Task 7: Codex parser, correlation, and reducer

**Files:**

- Create: `internal/codexadapter/osc9.go`
- Create: `internal/codexadapter/osc9_test.go`
- Create: `internal/codexadapter/reducer.go`
- Create: `internal/codexadapter/reducer_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write failing streaming OSC tests**

Recognize plain OSC9 terminated by BEL/ST and tmux DCS-wrapped/doubled-ESC OSC9.
Feed every fixture split at every byte boundary. Cover noise, multiple frames,
OSC0/OSC8 rejection, oversize recovery, and a 64-KiB retained-data cap. Parser
returns events but never edits the forwarded byte slice.

**Step 2: Implement the bounded parser and verify GREEN**

```bash
go test ./internal/codexadapter -run 'TestOSC9' -count=1
```

**Step 3: Write failing reducer/correlation tests**

Cover expected resume root, fresh `thread/started`, child `sessionId` mapping,
stale unrelated roots, ambiguous reconnect, active waiting flags, system error,
idle not done, OSC done, waiting/error precedence over OSC, and active clearing
prior completion.

**Step 4: Implement reducer and add WebSocket module**

```bash
go get github.com/coder/websocket@v1.8.15
go test ./internal/codexadapter -count=1
```

Keep all mutation serialized through one event loop. Observer loss or ambiguous
root selection yields unknown, never completion.

**Step 5: Commit**

```bash
git add internal/codexadapter/osc9.go internal/codexadapter/osc9_test.go internal/codexadapter/reducer.go internal/codexadapter/reducer_test.go go.mod go.sum
git commit -m "feat(codex): reduce semantic events"
```

### Task 8: Codex passive observer and PTY supervisor

**Files:**

- Create: `internal/codexadapter/protocol.go`
- Create: `internal/codexadapter/observer.go`
- Create: `internal/codexadapter/observer_test.go`
- Create: `internal/codexadapter/supervisor.go`
- Create: `internal/codexadapter/supervisor_test.go`
- Create: `cmd/wisp-deck-tui/codex_adapter.go`
- Create: `cmd/wisp-deck-tui/codex_adapter_cmd_test.go`
- Modify: `lib/tmux-session.sh`
- Modify: `test/bash/codex_launch_test.go`

**Step 1: Write failing fake-UDS observer tests**

Serve WebSocket over a temporary Unix socket. Assert exact
`initialize -> initialized`, loaded-list pagination, read-only `thread/read`,
status/started/closed handling, buffering around initialization, reconnect
snapshot ordering, a 1-MiB read limit, and cancellation.

Record every client method and fail the test if it sends `thread/start`,
`thread/resume`, `thread/fork`, `thread/unsubscribe`, any `turn/*`, or any
response to approval/input/auth requests.

**Step 2: Implement the observer**

Use `coder/websocket` with an `http.Transport.DialContext` that always dials the
private socket, no proxy, no compression. The protocol omits `jsonrpc`. Seed a
known resume UUID; a passive connection must not infer another connection's
resume response.

**Step 3: Write failing supervisor/PTY matrix tests**

The CLI is:

```text
wisp-deck-tui codex-adapter \
  --codex /absolute/codex --state-file FILE --generation GEN \
  [--resume-session UUID] [--fallback-window 10s] [-- PROMPT]
```

Test exact argv for server, remote fresh/resume, and embedded fallback; observer
readiness before TUI; quick resume fallback; quick remote setup/fresh fallback;
late/zero exit no relaunch; app-server output only in the Wisp error log; signal
cleanup; and state serialization.

**Step 4: Write PTY byte-preservation test**

A fake child emits ordinary bytes plus fragmented wrapped OSC. Assert stdout is
byte-identical, the parser reports one completion, stdin passes unchanged,
SIGWINCH inherits size, raw terminal state restores, and PTY EIO after exit is
EOF.

**Step 5: Implement supervisor and shell launch**

Create a short mode-0700 temp directory and `a.sock`. Start app-server without
terminal stdin, initialize the observer, then run remote Codex in a child PTY
with only `agent-turn-complete` OSC9 enabled. On observer loss publish unknown
and reconnect without restarting TUI. Embedded fallback retains OSC completion
and never prompt-scrapes.

Make the Codex arm in `build_ai_launch_cmd` use argv-safe adapter flags whenever
attention state exists, including restore UUID and handoff prompt.

**Step 6: Verify and commit**

```bash
go test ./internal/codexadapter ./cmd/wisp-deck-tui -run 'Codex|OSC|Observer' -count=1
go test ./test/bash -run 'TestCodexLaunch' -count=1
shellcheck lib/tmux-session.sh
go build ./cmd/wisp-deck-tui
./wisp-deck-tui codex-adapter --help
git add internal/codexadapter cmd/wisp-deck-tui/codex_adapter.go cmd/wisp-deck-tui/codex_adapter_cmd_test.go lib/tmux-session.sh test/bash/codex_launch_test.go go.mod go.sum
git commit -m "feat(codex): observe attention protocol"
```

Remove the local build artifact after the smoke test.

### Task 9: Cross-adapter integration and stale-writer tests

**Files:**

- Modify: `wrapper.sh`
- Modify: `lib/account-switch.sh`
- Modify: `lib/session-restore.sh` only if tests expose stale attention fields
- Modify: `test/bash/account_switch_e2e_test.go`
- Modify: `test/bash/tool_switch_pool_test.go`
- Create: `test/bash/attention_integration_test.go`
- Modify: packaging tests if any new path is referenced

**Step 1: Write failing end-to-end lifecycle tests**

Using fake adapters and real tmux where practical, prove:

- watcher starts before tmux yet waits for exactly one tagged pane ID;
- initial Claude/OpenCode/Codex commands receive the current generation;
- account and tool respawn rotate before process start;
- a late old writer cannot recreate or affect current state;
- Claude -> Codex -> OpenCode changes sound/title/theme/keep-awake without
  restarting the watcher;
- two same-project sessions have disjoint roots and cleanup isolation;
- restore creates a fresh root and never imports prior attention;
- closing a session terminates owned helpers and removes its root;
- malformed/missing adapter state remains unknown with no notification.

**Step 2: Implement only exposed integration gaps**

Keep policy centralized in the watcher. Do not add screen capture, prompt regex,
generic idle timers, per-plugin sound/title, or shared fixed state paths as
fallbacks.

**Step 3: Run focused integration and static guards**

```bash
go test ./test/bash -run 'Attention|AccountSwitch|ToolSwitch|Restore|Opencode|Codex' -count=1
rg -n 'capture-pane.*(prompt|waiting)|wisp-deck-waiting|session\.idle|tool\.execute\.after|startSpinner|afplay' lib wrapper.sh templates test/bash
shellcheck wrapper.sh lib/attention.sh lib/tab-title-watcher.sh lib/account-switch.sh lib/tmux-session.sh lib/install.sh lib/settings-json.sh lib/notification-setup.sh bin/wisp-deck
```

Expected `rg` matches only deliberate migration/removal tests or unrelated UI
capture, never attention classification.

**Step 4: Commit**

```bash
git add wrapper.sh lib test/bash test/npx bin templates cmd internal go.mod go.sum
git commit -m "test(attention): cover adapter lifecycle"
```

### Task 10: Review, verification, and delivery

**Files:** all changed files and the two design/plan documents.

**Step 1: Run spec-compliance review**

Audit every design requirement against code and tests. Missing or indirect
evidence is incomplete. In particular verify questions/permissions, filtered
completion, terminal errors, initial idle, subagent completion suppression,
unknown fail-silent behavior, generation fencing, background Claude jobs, and
all respawn/restore paths.

**Step 2: Run code-quality review and fix findings through RED/GREEN tests**

Review security of private files/sockets, command quoting, signal cleanup,
WebSocket method allowlist, parser bounds, races, Bash 3.2 compatibility, and
plugin authentication. Every behavior fix starts with a failing regression test.

**Step 3: Run fresh verification**

```bash
gofmt -w internal/attention internal/codexadapter cmd/wisp-deck-tui
go vet ./...
shellcheck wrapper.sh lib/*.sh lib/terminals/*.sh bin/wisp-deck
go test ./internal/attention ./internal/codexadapter ./cmd/wisp-deck-tui -count=1
go test ./test/bash -run 'Attention|Claude|Codex|Opencode|AccountSwitch|ToolSwitch|Restore' -count=1
./run-tests.sh
go build -o /tmp/wisp-deck-tui-attention ./cmd/wisp-deck-tui
/tmp/wisp-deck-tui-attention claude-attention --help
/tmp/wisp-deck-tui-attention codex-adapter --help
rm -f /tmp/wisp-deck-tui-attention
git diff --check
git status --short --branch
```

If the known orphan stress processes still make the full suite fail, preserve
the output and request authorization to terminate only those exact stale
process trees; do not weaken timeouts or claim completion from focused tests.

**Step 4: Push and prove remote state**

```bash
git pull --rebase
git push -u origin feat/semantic-agent-attention
git status --short --branch
```

The branch is complete only when all requirements have direct evidence, the
full suite and shellcheck pass, the push succeeds, and status reports the branch
up to date with its remote.
