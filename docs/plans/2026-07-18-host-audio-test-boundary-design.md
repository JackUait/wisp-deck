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

Darwin adds a less obvious failure mode: XNU redacts the environment reported
by `KERN_PROCARGS2` for restricted executables such as `/bin/bash` and
`/bin/zsh`. A marker-only test parent is therefore indistinguishable from a
normal production shell. Its full argument vector remains visible.

The repository also still tracks an unreferenced legacy `ghost-tab-tui`
Mach-O containing the old player path. Although npm does not ship it, a future
repository test could execute it outside every source-level guard.

At the same time, sound previews are intentional product behavior. Normal
`make build`, `make install`, and release binaries must keep previews.

## Decision

Use five independent, fail-closed layers.

### 1. Propagate an explicit test-mode contract

Repository automation uses a versioned, two-part exec-time contract:
`WISP_DECK_TESTING=1` plus the exact argv0 sentinel
`__WISP_DECK_REPOSITORY_TEST_V1__.test`. The `.test` suffix preserves
conventional test-process argv0 behavior and compatibility; the separate
defense-in-depth fallback checks the full `Executable` basename. Exact
sentinel equality prevents prefixes, suffixes, or later arguments from
impersonating the contract.

- Every repository-owned executable `go test` entrypoint routes through
  `scripts/go-test.sh`. The driver exports the marker and uses `exec -a` to
  place the sentinel on the long-lived Go tool process. Make targets,
  `run-tests.sh`, both test/install workflows, and release preflight all use
  this driver; it is the only executable raw `go test` owner.
- Go test packages that launch shell/application children re-exec once from
  `TestMain` unless both the marker and exact argv0 sentinel are already
  present. The re-exec copies all arguments, replaces only argv0, and
  normalizes the exec-time environment. This matters because
  `KERN_PROCARGS2` does not reflect a later `os.Setenv`, and XNU may hide the
  environment of a restricted ancestor. Direct test-package runs are still
  covered by their `TestMain`.
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

The marker and sentinel complement each other. In-process Go test executables
also continue to use the linker-backed `testing.Testing()` signal.

### 2. Detect the propagated test contract in process ancestry

Before any host effect, a production child walks its Darwin process ancestry
with `sysctl`. It first checks exact argv0 for the versioned repository
sentinel, which remains visible on restricted `/bin/bash` and `/bin/zsh`
ancestors. It then checks the exact `WISP_DECK_TESTING=1` environment entry for
unrestricted ancestors. Either exact signal is conclusive and survives an
unreadable farther ancestor. A full ancestor executable path ending in `.test`
is a defense-in-depth fallback accepted only after traversal reaches the
validated PID 1 root. Otherwise unreadable or malformed ancestry is denied.

This covers a host-effect-enabled installed or release binary spawned from a
Go integration test. Pure tests cover exact sentinel placement, rejected
sentinel variants, argument/environment separation, long test-binary names,
multi-hop shell/Node parents, cycles, lookup failures, and bounded traversal.
An enabled child test removes every marker from a restricted shell and child,
sets only the shell's exact argv0 sentinel, and must report
`test_ancestor_sentinel`.

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
  `testing.Testing() == false`, no exact current marker, and no sentinel,
  marker, or `.test` test ancestor.
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
- marker/sentinel propagation through the driver, TestMain, runner, and test
  helpers;
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
  exact ancestor __WISP_DECK_REPOSITORY_TEST_V1__.test argv0
    OR current/ancestor WISP_DECK_TESTING=1
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
- **Environment marker only:** XNU information-hides restricted-shell
  environments, so a marker-only restricted ancestor is outside the
  repository contract and cannot be distinguished from production. The exact
  argv0 sentinel is the non-redacted structural signal; linker-backed Go-test
  identity, global marked-build capability, ancestry checks, and source
  invariants remain necessary.
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
   runtime-denied when a restricted parent and child contain no marker, with
   `test_ancestor_sentinel` proving the exact non-redacted argv0 contract.
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
