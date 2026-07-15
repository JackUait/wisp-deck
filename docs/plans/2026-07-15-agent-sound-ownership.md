# Agent Sound Ownership Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Wisp Deck the sole owner of every built-in notification sound path it configures for Claude, Codex, and OpenCode.

**Architecture:** Claude's launch-local overlay disables its native notification channel. Codex disables external notifier commands and keeps OSC 9 only as a private completion protocol; a bounded streaming filter consumes those frames inside Wisp Deck before PTY output reaches Ghostty. OpenCode remains an event-only state publisher, and the existing locked Wisp Deck playback gate remains the only runtime audio owner.

**Tech Stack:** Go 1.25, Bash, pseudo-terminals, ANSI/OSC state machines, Node executable-contract tests.

---

### Task 1: Disable Claude's native notification channel

**Files:**
- Modify: `test/bash/claude_launch_settings_test.go:17-100`
- Modify: `lib/settings-json.sh:53-99`

**Step 1: Write the failing tests**

Rename the two launch-overlay tests to describe disabled native notifications.
Change both assertions from `terminal_bell` to
`notifications_disabled`, while retaining the assertions for unrelated
settings, permissions, source-file immutability, atomicity, and mode 0600.

```go
if got["preferredNotifChannel"] != "notifications_disabled" {
    t.Fatalf("preferredNotifChannel = %#v, want notifications_disabled", got["preferredNotifChannel"])
}
```

**Step 2: Run the focused tests and verify RED**

```bash
go test ./test/bash -run 'TestSettingsJsonClaudeLaunchSettings(Merges|Without|Failure)' -count=1
```

Expected: FAIL because the generated overlay still contains `terminal_bell`.

**Step 3: Implement the minimal launch override**

Update the ownership comment in `write_claude_launch_settings` and replace:

```python
settings["preferredNotifChannel"] = "terminal_bell"
```

with:

```python
settings["preferredNotifChannel"] = "notifications_disabled"
```

Do not change `migrate_legacy_claude_notif_channel`; its `terminal_bell`
references clean up historical global state.

**Step 4: Run the focused tests and verify GREEN**

```bash
go test ./test/bash -run 'TestSettingsJsonClaudeLaunchSettings(Merges|Without|Failure)' -count=1
```

Expected: PASS.

**Step 5: Commit only the Claude ownership files**

```bash
git add lib/settings-json.sh test/bash/claude_launch_settings_test.go
git commit -m "fix(claude): disable native notification sound"
```

### Task 2: Build a bounded Codex OSC 9 output filter

**Files:**
- Create: `internal/codexadapter/osc9_filter.go`
- Modify: `internal/codexadapter/osc9_test.go`

**Step 1: Write failing streaming-filter tests**

Add tests for a wished-for zero-value API:

```go
forward, events := filter.Feed(chunk)
tail := filter.Flush()
```

Exercise plain OSC 9 with BEL and ST terminators and tmux-wrapped OSC 9 with
both inner terminators. For each fixture, test every possible two-way split and
one-byte feeds. Surround each frame with `before` and `after`; require output
`beforeafter` and exactly one event containing the payload.

Also prove that ANSI colors, OSC 0 titles, OSC 8 hyperlinks, non-tmux DCS, and
ordinary bytes remain byte-identical; incomplete unconfirmed prefixes flush at
EOF; incomplete confirmed notifications drop at EOF; a 64 KiB payload works;
and an oversized payload emits no event, remains bounded, drops through its
terminator, then recognizes a following valid frame.

**Step 2: Run the filter tests and verify RED**

```bash
go test ./internal/codexadapter -run 'TestOSC9Filter' -count=1
```

Expected: build FAIL because `OSC9Filter` does not exist.

**Step 3: Implement the minimal filter**

Create a zero-value streaming state machine in `osc9_filter.go`:

```go
type OSC9Filter struct {
    parser    OSC9Parser
    mode      osc9FilterMode
    candidate []byte
    confirmed bool
}

func (f *OSC9Filter) Feed(data []byte) ([]byte, []OSC9Event) {
    events := f.parser.Feed(data)
    output := make([]byte, 0, len(data))
    for _, b := range data {
        f.filterByte(b, &output)
    }
    return output, events
}

func (f *OSC9Filter) Flush() []byte
func (f *OSC9Filter) RetainedBytes() int
```

Use finite states for ground, ambiguous ESC, plain `ESC ] 9 ;`, confirmed plain
OSC 9, `ESC P tmux;`, confirmed wrapped OSC 9, and escape/terminator states.
Before confirmation, retain only the fixed prefix. After confirmation, discard
payload bytes; the embedded `OSC9Parser` owns the bounded event payload. On a
prefix mismatch, flush the candidate exactly and reprocess a mismatching ESC as
a new candidate. `Flush` emits only unconfirmed bytes and drops a confirmed,
incomplete notification.

**Step 4: Run parser/filter tests and verify GREEN**

```bash
go test ./internal/codexadapter -run 'TestOSC9(Parser|Filter)' -count=1
```

Expected: PASS.

**Step 5: Commit the standalone filter**

```bash
git add internal/codexadapter/osc9_filter.go internal/codexadapter/osc9_test.go
git commit -m "feat(codex): filter private OSC 9 notifications"
```

### Task 3: Terminate Codex notifications inside the PTY relay

**Files:**
- Modify: `internal/codexadapter/supervisor_test.go:1294-1333`
- Modify: `internal/codexadapter/supervisor.go:1088-1115`
- Modify: `test/bash/idle_sound_ownership_test.go`

**Step 1: Write failing integration and source-invariant tests**

Rename `TestCodexPTYForwardsBytesInputAndFragmentedOSC` to
`TestCodexPTYConsumesFragmentedOSCAndForwardsOrdinaryBytes`. Preserve its
fragmented tmux frame and event assertion, but require:

```go
wantOutput := "ordinary:" + input
```

Extend the idle-sound ownership guard to require Claude's
`notifications_disabled`, a `var filter OSC9Filter` declaration, and
`filtered, events := filter.Feed(chunk)`. Reject the old raw forwarding form
`writeFull(s.output(), chunk)`.

Extend the exact Codex argv contract to require `notify=[]` before the TUI
notification overrides for every fresh/resume and embedded/remote launch.

**Step 2: Run focused tests and verify RED**

```bash
go test ./internal/codexadapter -run 'TestCodexPTYConsumesFragmentedOSC' -count=1
go test ./test/bash -run 'TestIdleSoundRuntimeSitesUseSharedLiveGate' -count=1
```

Expected: FAIL because the relay still forwards the original OSC frame.

**Step 3: Wire the filter into the output loop**

Replace the separate parser and raw write with:

```go
var filter OSC9Filter
filtered, events := filter.Feed(chunk)
for _, event := range events {
    if onOSC != nil {
        onOSC(event)
    }
}
if len(filtered) > 0 {
    if err := writeFull(s.output(), filtered); err != nil {
        outputCh <- err
        return
    }
}
```

On EOF, EIO, or another read error, call `filter.Flush()` and write a non-empty
tail before preserving the existing error handling. Never write the original
chunk after filtering.

**Step 4: Run focused tests and verify GREEN**

```bash
go test ./internal/codexadapter -run 'TestOSC9|TestCodexPTYConsumesFragmentedOSC|TestCodexPTYDoesNotWait' -count=1
go test ./test/bash -run 'TestIdleSoundRuntimeSitesUseSharedLiveGate|TestOpencodePluginExecutableSpec' -count=1
```

Expected: PASS.

**Step 5: Commit the relay wiring and invariant**

```bash
git add internal/codexadapter/supervisor.go internal/codexadapter/supervisor_test.go test/bash/idle_sound_ownership_test.go
git commit -m "fix(codex): consume notifications before terminal"
```

### Task 4: Update the durable sound contract

**Files:**
- Modify: `docs/plans/2026-07-14-idle-sound-prevention.md`

**Step 1: Add the ownership requirements**

Add contract clauses stating that adapters publish attention state only;
Claude's native notification channel is disabled; Codex OSC 9 terminates inside
its private PTY relay; OpenCode has no notification effect; and no
agent-generated BEL, OSC notification, or direct audio path configured by Wisp
Deck reaches the outer terminal. Update runtime paths and prevention tests with
the corresponding evidence.

**Step 2: Verify the documentation diff**

```bash
git diff --check -- docs/plans/2026-07-14-idle-sound-prevention.md
git diff -- docs/plans/2026-07-14-idle-sound-prevention.md
```

Expected: no whitespace errors and a contract matching the implementation.

**Step 3: Commit the contract update**

```bash
git add docs/plans/2026-07-14-idle-sound-prevention.md
git commit -m "docs: require wisp-owned notification audio"
```

### Task 5: Verify and install

**Files:**
- Verify only; preserve unrelated ledger work.

**Step 1: Format and check changed source**

```bash
gofmt -w internal/codexadapter/osc9_filter.go internal/codexadapter/osc9_test.go internal/codexadapter/supervisor.go internal/codexadapter/supervisor_test.go test/bash/idle_sound_ownership_test.go test/bash/claude_launch_settings_test.go
git diff --check
```

Expected: exit 0.

**Step 2: Run focused ownership suites**

```bash
go test ./internal/codexadapter -count=1
go test ./test/bash -run 'Sound|Notification|ClaudeLaunchSettings|OpencodePlugin' -count=1
```

Expected: PASS.

**Step 3: Run full verification**

Inspect the Makefile and `run-tests.sh`, then run the authoritative suite and
build. At minimum:

```bash
go test ./... -count=1
make build
```

Expected: exit 0 with no failures.

**Step 4: Audit ownership requirement by requirement**

```bash
rg -n 'terminal_bell|notifications_disabled|notification_method|OSC9Filter|afplay|osascript' lib internal cmd templates test
```

Confirm Claude is disabled, Codex frames cannot reach the terminal, OpenCode is
event-only, every playback site belongs to Wisp Deck, and every non-preview
runtime playback uses the intended live preference boundary.

**Step 5: Install and verify the artifact**

```bash
make install
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: install, path, checksum, and signature checks all succeed. A running
ledger pane or session must be relaunched to load the installed binary and new
launch overlays.
