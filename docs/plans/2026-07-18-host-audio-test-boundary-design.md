# Host-Audio Test Boundary Design

## Problem

The existing preview fix keeps sound execution out of reusable TUI state and
blocks it inside a `go test` executable. That boundary does not cover every
automated child process:

- `test/bash/small_test.go` runs `make build`, which creates a normal executable
  with preview capability enabled. In that child, `testing.Testing()` is false.
- `lib/notification-setup.sh` can reach `afplay` after a test calls the
  production shell helper with an enabled temporary preference.
- `claudeBackgroundNotifier` defaults a nil injected runner to the real
  detached process runner, so a future test can indirectly reach both
  Notification Center and `/usr/bin/afplay`.

Current tests use fake runners carefully, but that convention is not a
permanent boundary. A future integration test can forget one fake and still
pass the existing ownership checks.

The repository also still tracks an unreferenced legacy `ghost-tab-tui`
Mach-O containing the old player path. Although npm does not ship it, a future
repository test could execute it outside every source-level guard.

At the same time, sound previews are intentional product behavior. Normal
`make build`, `make install`, and release binaries must keep previews.

## Decision

Use five independent, fail-closed layers.

### 1. Propagate an explicit test-mode contract

`WISP_DECK_TESTING=1` means that a process belongs to repository automation and
must not create host-facing sound or notification effects.

- `run-tests.sh` exports the marker before starting any package.
- Go test packages that launch shell/application children re-exec once from
  `TestMain` when necessary, placing the marker in the test process's
  exec-time environment. This matters because Darwin's `KERN_PROCARGS2`
  snapshot does not reflect a later `os.Setenv`. Direct
  `go test ./test/bash` and `go test ./test/npx` runs are therefore covered.
- `test/bash.buildEnv` strips live Wisp Deck session state but always restores
  this one reserved marker.
- Npx subprocess helpers restore the marker even when a test replaces a
  child's complete environment slice.
- tmux session creation stamps the marker into panes when the wrapper is
  running under repository automation.
- `make build` remains host-effect-capable for normal users, but produces a
  globally host-effect-disabled artifact whenever the test selector is present,
  regardless of value or Make command-line override. This makes every effect in
  the existing Makefile test artifact inert even if a later child drops its
  environment.

The marker is defense in depth, not the only check. In-process Go test
executables continue to use the linker-backed `testing.Testing()` signal.

### 2. Detect the propagated test contract in process ancestry

Before any host effect, a production child walks its Darwin process ancestry
with `sysctl`. The exact `WISP_DECK_TESTING=1` marker in any ancestor
environment marks the entire descendant chain as repository automation, even
when a helper replaces or unsets the child's environment. A full ancestor
executable path ending in `.test` is a defense-in-depth signal, not the
structural identity contract. An unreadable or malformed ancestry is denied
rather than treated as production.

This covers a host-effect-enabled installed or release binary spawned from a
Go integration test. Pure tests cover long test-binary names, multi-hop
shell/Node parents, renamed test binaries retaining the exact marker, cycles,
lookup failures, and bounded traversal. An enabled child subprocess test
deliberately removes `WISP_DECK_TESTING` and must still report host effects
denied because its ancestor retains the test contract.

### 3. Centralize and guard every real host-effect runner

The guard lives at the last point before process creation, so indirect callers
cannot bypass it.

- A single Go runner owns every Go host-effect use of `/usr/bin/afplay`,
  `/usr/bin/osascript`, and its `exec.CommandContext` call. It accepts only
  typed, internally validated system-sound and visual-notification
  effects—never an arbitrary executable or injectable process callback.
  Existing unrelated Git, agent, process-inspection, and screenshot subprocess
  owners remain exact-audited rather than being mislabeled as host effects.
- The runner requires a production host-effects build capability,
  `testing.Testing() == false`, no exact test marker, and no Go-test ancestor.
- Main-menu preview and Claude background notification planning remain pure and
  feed the same guarded runner.
- `play_notification_sound` returns immediately in marked test mode. In normal
  operation it delegates the feature file to a hidden
  `wisp-deck-tui notification-sound` command, which uses the canonical Go
  preference reader and shared lock before entering the sole runner. Shell
  tests never execute a fake audio player.

The preference locks, allowlists, Off behavior, and interactive preview
semantics stay unchanged.

### 4. Make the boundary observable, versioned, and repairable

The binary exposes a machine-readable capability probe that reports:

- whether global host effects and preview support were compiled into the
  binary;
- the host-effects boundary protocol version; and
- whether host effects are allowed in the current process, including a stable
  denial reason for diagnostics.

Installers validate the application version, compiled host-effect capability,
boundary protocol, command exit status, and parsed JSON types/values. A
same-version ordinary `go build`, empty/malformed probe, or older binary that
merely claims preview support can no longer be mistaken for a complete
production installation. Release and local-install verification also exercise
the probe. The Bash installer maps Intel `x86_64` to the release asset name
`amd64`, matching the release script and Node installer.

The runtime field is diagnostic and shares the exact guard used by playback;
install acceptance depends on compiled state and protocol version, so running
an installer from a test does not convert a valid production artifact into an
invalid one. Because the current `v2.23.0` assets predate this protocol, the
implementation bumps the next release to `2.23.1`; no launcher requiring the
probe may be published against an older asset.

### 5. Enforce ownership in tests and CI

The repository invariant collapses real process execution into the sole Go
host-effect owner. It requires:

- the shared build/test/environment/ancestry guard at the sole process boundary;
- no injectable generic executor in preview, notification, or test APIs;
- immediate shell denial in marked tests and delegation to the guarded Go
  notification command in production;
- test-marker propagation through the runner and test helpers;
- marker propagation into tmux session panes;
- host-effect-enabled normal production builders and globally disabled marked
  test builds;
- version, compiled-capability, and boundary-protocol checks in both installers;
- command-package tests in `run-tests.sh` and CI; and
- no new unaudited player, speech, AppleScript sound, raw bell, or terminal
  notification owner in the explicit compiled, shipped, and build/release text
  inventory. The inventory is checked against `package.json.files` so a new
  shipped root cannot silently escape the scan; tracked executable build
  artifacts are forbidden so stale binaries cannot bypass source guards.

Focused regression tests safely reproduce each former escape using pure effect
planning, safe lock callbacks, ancestry fixtures, and capability diagnostics.
No regression test invokes a player or generic process runner.

## Resulting Boundaries

```text
normal interactive build
  user changes Idle Sound
    -> global host-effect capability enabled
    -> no test binary, marker, or test ancestor
    -> sole typed runner
    -> fixed /usr/bin/afplay

go test process
  direct production function call
    -> testing.Testing()
    -> denied

test child (shell or separately built application)
  current/ancestor WISP_DECK_TESTING=1
    OR defense-in-depth .test ancestor
    -> last-moment runner guard
    -> denied

test-created make build
  inherited WISP_DECK_TESTING=1
    -> global host effects compiled disabled
    -> every effect denied even if environment is later lost
```

## Rejected Alternatives

- **Remove previews:** violates the approved product behavior.
- **Make every developer build silent:** avoids one child-binary escape but
  unnecessarily removes previews from normal `make build`.
- **Environment marker only:** too easy for a helper to strip; the linker-backed
  Go-test check, global marked-build capability, process-ancestry check, and
  source invariant remain necessary.
- **Executable fake audio players:** an absolute “fake” path could itself be
  the real player or delegate to it. Marked tests therefore execute no player.
- **PATH interception only:** both audited Go paths use absolute
  `/usr/bin/afplay`.
- **TTY detection:** integration tests legitimately use pseudo-terminals.
- **Truncated process-name heuristics:** Darwin's short process name can truncate
  long Go test names. The guard reads the full executable path and checks its
  basename instead.

## Verification

Implementation follows strict red-green TDD. Completion requires:

1. Focused tests proving each new guard initially fails and then passes.
2. A production-capability child probe proving an enabled binary remains
   runtime-denied after its own test-marker environment is deliberately
   removed, with the diagnostic reason proving its parent test process retains
   the exact exec-time marker.
3. Shell tests proving marked mode returns before any player or delegated
   binary, plus pure notification-command tests.
4. Background-notifier tests proving effect planning and preference locking
   without any injectable process runner.
5. Installer tests proving same-version preview-disabled binaries are repaired
   and Intel uses the published asset name against the new `2.23.1` version.
6. The ownership invariant, command package, race-sensitive focused tests,
   `go vet`, and the complete repository suite under a macOS sandbox that
   denies execution of the real player and Notification Center adapter.
7. `make install`, required command-path and SHA-256 equality checks, a valid
   code signature, and a compiled-preview capability probe returning true.

Running Wisp Deck panes and sessions must be relaunched after installation to
load the new binary.
