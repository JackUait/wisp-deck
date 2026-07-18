# Durable Codex Crash Restore Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Persist each Wisp pane's exact Codex thread UUID across macOS crashes and guarantee that restore never silently opens an empty Codex conversation.

**Architecture:** The Codex adapter durably writes its reducer-correlated root UUID to a per-Wisp-session sidecar. The live snapshot selects identity by active tool and carries the sidecar key through the restore queue. Exact restore uses the UUID; missing, invalid, or failed exact restore opens Codex's resume selector instead of plain Codex.

**Tech Stack:** Go, Bash 3.2, Codex app-server observer, tmux, Go integration tests, shellcheck.

---

### Task 1: Persist the adapter's correlated Codex identity

**Files:**
- Create: `internal/codexadapter/identity.go`
- Modify: `internal/codexadapter/supervisor.go`
- Modify: `internal/codexadapter/supervisor_test.go`

**Step 1: Write failing supervisor tests**

Add tests that use real temporary identity files:

```go
func TestCodexSupervisorPersistsResumeIdentityBeforeTUI(t *testing.T)
func TestCodexSupervisorPersistsFreshCorrelatedRoot(t *testing.T)
func TestCodexSupervisorStopsWhenFreshIdentityCannotPersist(t *testing.T)
func TestCodexIdentityWriteIsAtomicPrivateAndCanonical(t *testing.T)
```

The resume test supplies `IdentityFile` and asserts it contains
`ResumeSession` before `RunPTY` begins. The fresh test injects a
`ThreadObserved` event and waits for the file. The failure test points the
identity path at an unwritable/non-directory parent and asserts `Run` returns
an identity-persistence error and cancels the PTY. The writer test asserts
mode `0600`, exact UUID plus newline, no orphan temporary file, and rejection
of malformed UUIDs.

**Step 2: Run tests and verify RED**

```bash
go test ./internal/codexadapter -run 'TestCodex(SupervisorPersists|SupervisorStopsWhenFreshIdentity|IdentityWrite)' -count=1 -v
```

Expected: compile failure because `IdentityFile` and the identity writer do
not exist.

**Step 3: Implement durable identity writing**

Create `identity.go` with:

```go
func writeCodexIdentity(path, id string) error
func clearCodexIdentity(path string) error
```

Validate `id` with `validateCanonicalUUID`. Require an absolute path. Create
the parent with `0700`; create a same-directory temporary file with `0600`;
write `id+"\n"`; `Sync`, close, rename, and sync the parent directory. Cleanup
the temporary file on every failure.

Add `IdentityFile string` to `CodexSupervisorOptions`. In `Run`, publish a
resume UUID before the TUI starts. Track later top-level roots from the private
TUI independently of the attention reducer so Codex `/new` atomically replaces
the sidecar. Recover one missed transition from a reconnect snapshot. Refactor
`runAttempt` to use a cancellable attempt context so an identity-write failure
terminates and reaps the active PTY before returning the error, and fail closed
after a bounded reconnect window when observer loss occurs before the first
identity is known.

If `IdentityFile` is non-empty, failure to initialize the observer is fatal:
an embedded/OSC-only session cannot discover an exact root and must not create
an unrestoreable conversation.

**Step 4: Run tests and verify GREEN**

```bash
go test ./internal/codexadapter -run 'TestCodex' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/codexadapter/identity.go internal/codexadapter/supervisor.go internal/codexadapter/supervisor_test.go
git commit -m "fix(codex): persist exact session identity"
```

### Task 2: Wire the identity file and recovery selector through the CLI

**Files:**
- Modify: `cmd/wisp-deck-tui/codex_adapter.go`
- Modify: `cmd/wisp-deck-tui/codex_adapter_cmd_test.go`
- Modify: `internal/codexadapter/supervisor.go`
- Modify: `internal/codexadapter/supervisor_test.go`

**Step 1: Write failing command and argv tests**

Extend valid adapter invocations with:

```text
--session-file /private/wisp/session-identities/dev-app-1.codex
```

Add assertions that the path reaches `CodexSupervisorOptions.IdentityFile`.
Reject missing/relative session files. Add a `--resume-picker` flag and reject
using it together with `--resume-session`.

Update `buildCodexTUIArgv` tests:

```go
exact  -> codex resume ... -- <uuid>
picker -> codex resume ...             // no UUID, no prompt separator
fresh  -> codex ...
```

Add a supervisor test proving a quick exact-resume failure clears the stale
identity and launches the remote resume picker, not a fresh command.

**Step 2: Run tests and verify RED**

```bash
go test ./cmd/wisp-deck-tui ./internal/codexadapter -run 'Codex.*(SessionFile|ResumePicker|ResumeQuickFailure)' -count=1 -v
```

Expected: failures for unknown flags/options and fresh fallback.

**Step 3: Implement CLI and fallback state**

Add `SessionFile string` and `ResumePicker bool` to command options, require
an absolute session file, enforce mutual exclusion, and pass both fields to
the supervisor. Add `ResumePicker bool` to supervisor options and to
`buildCodexTUIArgv`.

Track picker intent separately from the exact UUID. On quick exact-resume
failure:

1. clear the stale identity sidecar;
2. take the existing coherent observer barrier;
3. rebuild a fresh reducer from that barrier;
4. run `codex resume` through the same remote app-server.

When identity persistence is required, observer or remote-TUI failure returns
a visible error instead of falling back to an unobservable embedded fresh
session.

**Step 4: Run tests and verify GREEN**

```bash
go test ./cmd/wisp-deck-tui ./internal/codexadapter -run 'Codex' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/wisp-deck-tui/codex_adapter.go cmd/wisp-deck-tui/codex_adapter_cmd_test.go internal/codexadapter/supervisor.go internal/codexadapter/supervisor_test.go
git commit -m "fix(codex): fail closed into resume picker"
```

### Task 3: Make snapshots tool-aware and sidecar-backed

**Files:**
- Modify: `lib/session-restore.sh`
- Modify: `test/bash/session_restore_test.go`

**Step 1: Write failing snapshot tests**

Add tests for:

```go
func TestWriteSessionSnapshotCodexUsesCodexIDNotClaudeID(t *testing.T)
func TestWriteSessionSnapshotCodexReadsDurableIdentity(t *testing.T)
func TestWriteSessionSnapshotCodexRejectsMalformedIdentity(t *testing.T)
func TestMaybeRestoreCodexResolvesIdentityKeyIntoQueue(t *testing.T)
func TestMaybeRestoreCodexKeepsMissingLegacyIDForPicker(t *testing.T)
```

Use canonical UUIDs. The first test provides both Claude and Codex IDs and
requires the Codex UUID in field six. The sidecar test provides only
`WISP_DECK_CODEX_SESSION_FILE` and requires both its UUID and basename key.
The queue test starts from a snapshot with an empty embedded UUID plus key,
then requires the sidecar UUID in the queue.

**Step 2: Run tests and verify RED**

```bash
go test ./test/bash -run 'Test(WriteSessionSnapshotCodex|MaybeRestoreCodex)' -count=1 -v
```

Expected: snapshot uses the Claude field or emits an empty Codex field.

**Step 3: Implement tool-aware snapshot and queue resolution**

Add Bash helpers:

```bash
codex_session_id_valid
codex_identity_key
codex_identity_read
```

Accept only canonical lowercase UUIDs. Accept identity basenames only when
they contain no slash, pipe, CR, or LF and end in `.codex`, then read strictly
under `$config_dir/session-identities/`. The adapter CLI likewise accepts only
a clean `.codex` child of a non-symlinked `session-identities` directory.

Change snapshot field six selection to a `case "$tool"`:

- Claude reads `WISP_DECK_CLAUDE_SESSION`.
- Codex reads the valid sidecar, then a valid `WISP_DECK_CODEX_SESSION`
  compatibility stamp.
- Other tools emit empty.

Append the sidecar basename as field nine. Extend snapshot and queue parsers
backward-compatibly; resolve a missing Codex UUID from the key during queue
build and carry the key as queue field seven. Deduplicate on `tool|sid`, not
bare UUID.

Update duplicate-open checks to inspect the session variable belonging to the
entry's tool and use a live Codex sidecar when needed.

**Step 4: Run tests and verify GREEN**

```bash
go test ./test/bash -run 'Test(SessionRestore|WriteSessionSnapshot|MaybeRestore|RestoreEntry)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add lib/session-restore.sh test/bash/session_restore_test.go
git commit -m "fix(restore): snapshot active tool identity"
```

### Task 4: Wire per-session sidecars through the wrapper and launch builder

**Files:**
- Modify: `wrapper.sh`
- Modify: `lib/tmux-session.sh`
- Modify: `lib/account-switch.sh`
- Modify: `test/bash/codex_launch_test.go`
- Modify: `test/bash/wrapper_restore_test.go`
- Modify: `test/bash/tool_switch_pool_test.go`

**Step 1: Write failing launch and wrapper tests**

Change Codex launch expectations:

- normal adapter command includes `--session-file`;
- exact restore includes both `--session-file` and `--resume-session`;
- restored missing ID includes `--resume-picker`;
- raw exact failure chains to `codex resume`, never plain `codex`;
- raw missing ID is `codex resume`.

Add wrapper assertions that:

- `session-identities/<SESSION_NAME>.codex` is created as the configured path;
- tmux receives `WISP_DECK_CODEX_SESSION_FILE`;
- a restored Codex UUID is stamped in `WISP_DECK_CODEX_SESSION`, not
  `WISP_DECK_CLAUDE_SESSION`;
- the queue's identity-key field is accepted.

Add a switch test proving leaving Codex prefers its durable identity over cwd
rollout guessing.

**Step 2: Run tests and verify RED**

```bash
go test ./test/bash -run 'Test(BuildAiLaunchCmd_codex|Wrapper.*Codex|ToolSwitch.*Codex)' -count=1 -v
```

Expected: missing session-file/picker arguments and wrong tmux stamp.

**Step 3: Implement wrapper and launch plumbing**

After `SESSION_NAME` is created:

```bash
WISP_DECK_CODEX_SESSION_DIR="$SHARE_DIR/session-identities"
WISP_DECK_CODEX_SESSION_FILE="$WISP_DECK_CODEX_SESSION_DIR/${SESSION_NAME}.codex"
mkdir -p "$WISP_DECK_CODEX_SESSION_DIR"
chmod 700 "$WISP_DECK_CODEX_SESSION_DIR"
```

Do not remove this file in `cleanup()`. On launch, opportunistically prune
`.codex` sidecars older than 30 days only when they are unreferenced by live
tmux sessions, `last-session`, `last-session.prev`, and `restore-queue`. A tmux
inspection failure defers pruning.

Add `--session-file` to the adapter command. In restore mode, add
`--resume-picker` when Codex has no exact UUID. Change raw Codex restore so
missing ID runs `codex resume`, and exact quick failure falls back to that
selector.

Stamp `WISP_DECK_CODEX_SESSION` and
`WISP_DECK_CODEX_SESSION_FILE` in `tmux new-session`; stamp
`WISP_DECK_CLAUDE_SESSION` only for Claude.

In account switching, read the valid durable sidecar before using
`codex_current_session`; keep the rollout scan only as backward-compatible
fallback.

**Step 4: Run tests and verify GREEN**

```bash
go test ./test/bash -run 'Test(BuildAiLaunchCmd_codex|Wrapper.*Codex|ToolSwitch.*Codex)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add wrapper.sh lib/tmux-session.sh lib/account-switch.sh test/bash/codex_launch_test.go test/bash/wrapper_restore_test.go test/bash/tool_switch_pool_test.go
git commit -m "fix(wrapper): restore Codex by durable UUID"
```

### Task 5: Prove the crash roundtrip and close regressions

**Files:**
- Create: `test/bash/codex_crash_restore_test.go`
- Modify: `CLAUDE.md`
- Modify: `README.md`

**Step 1: Write the end-to-end regression**

Build two mocked live tmux sessions for the same project:

```text
dev-app-1 -> UUID A sidecar
dev-app-2 -> UUID B sidecar
```

Give each a stale, different Claude UUID to prove it is ignored. Run the real
snapshot function, queue builder, two queue pops, and launch builder. Require:

- two distinct Codex UUIDs survive in original order;
- both commands contain their exact `--resume-session`;
- neither command is fresh or selector-based;
- deleting one sidecar before queue build makes only that entry selector-based;
- an eight-field legacy Codex snapshot is selector-based.

**Step 2: Run the test and verify RED if any integration is missing**

```bash
go test ./test/bash -run 'TestCodexCrashRestore' -count=1 -v
```

Expected before final integration: FAIL on at least one roundtrip assertion.

**Step 3: Complete only the integration gaps exposed by the test**

Keep production changes limited to the snapshot, queue, wrapper, and launch
boundaries named above. Document the invariant in `CLAUDE.md`: every Codex
pane that can accumulate a conversation must have a durable identity path,
and restored Codex must never fall back to plain launch. Update README's
reboot-restore description to state that legacy unidentified Codex tabs open
the resume selector.

**Step 4: Run focused and full verification**

```bash
go test ./internal/codexadapter ./cmd/wisp-deck-tui -run 'Codex' -count=1
go test ./test/bash -run 'Codex|Restore|SessionSnapshot|ToolSwitch' -count=1
./run-tests.sh
shellcheck wrapper.sh lib/session-restore.sh lib/tmux-session.sh lib/account-switch.sh
git diff --check
```

Expected: all commands exit zero with no failures.

**Step 5: Commit**

```bash
git add test/bash/codex_crash_restore_test.go CLAUDE.md README.md
git commit -m "test(restore): guard Codex crash roundtrip"
```

### Task 6: Install and audit the shipped state

**Files:**
- Build/install artifact: `bin/wisp-deck-tui`
- Installed artifact: `~/.local/bin/wisp-deck-tui`

**Step 1: Install**

```bash
make install
```

Expected: build, signing, and installation succeed.

**Step 2: Verify required installation invariants**

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --deep --strict --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: all commands exit zero and code signing reports a valid designated
requirement.

**Step 3: Audit requirements against evidence**

Re-read the approved design and map each invariant to:

- a focused unit/integration test;
- the full-suite result;
- the installed binary checks;
- a clean `git status --short`.

Do not claim completion if exact UUID persistence, same-project distinction,
legacy selector behavior, exact-failure selector behavior, or installed
artifact verification lacks direct evidence.
