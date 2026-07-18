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

At the same time, sound previews are intentional product behavior. Normal
`make build`, `make install`, and release binaries must keep previews.

## Decision

Use four independent, fail-closed layers.

### 1. Propagate an explicit test-mode contract

`WISP_DECK_TESTING=1` means that a process belongs to repository automation and
must not create host-facing sound or notification effects.

- `run-tests.sh` exports the marker before starting any package.
- Go test packages that launch shell/application children set the marker from
  `TestMain`, so direct `go test ./test/bash` and `go test ./test/npx` runs are
  also covered.
- `test/bash.buildEnv` strips live Wisp Deck session state but always restores
  this one reserved marker.
- `make build` remains preview-capable for normal users, but produces a
  preview-disabled artifact when invoked under the test marker. This makes the
  existing Makefile tests safe even if a child later drops its environment.

The marker is defense in depth, not the only check. In-process Go test
executables continue to use the linker-backed `testing.Testing()` signal.

### 2. Guard every real host-effect runner

The guard lives at the last point before process creation, so indirect callers
cannot bypass it.

- The main-menu preview runner requires production preview capability,
  `testing.Testing() == false`, and no test marker.
- `runClaudeBackgroundDetached` refuses all real detached host effects in a Go
  test executable or marked test descendant. Tests may still inject a fake
  `Run` function into the notifier and inspect the planned commands.
- `play_notification_sound` returns without playback in marked test mode unless
  an explicit test-only player path is supplied. Production ignores that test
  injection and uses fixed `/usr/bin/afplay`, removing PATH shadowing.

The preference locks, allowlists, Off behavior, and interactive preview
semantics stay unchanged.

### 3. Make preview capability observable and repairable

The binary exposes a machine-readable capability probe that reports:

- whether preview support was compiled into the binary; and
- whether host effects are allowed in the current process.

Installers validate both version and compiled preview capability. A
same-version ordinary `go build` can no longer be mistaken for a complete
production installation. Release and local-install verification also exercise
the probe. The Bash installer maps Intel `x86_64` to the release asset name
`amd64`, matching the release script and Node installer.

The runtime field is diagnostic and shares the exact guard used by playback;
install acceptance depends only on the compiled field, so running an installer
from a test does not convert a valid production artifact into an invalid one.

### 4. Enforce ownership in tests and CI

The repository invariant expands from the preview adapter to all three current
host-audio owners. It requires:

- the shared Go test/environment guard at both real Go process boundaries;
- the shell test marker, explicit fake-player route, and absolute production
  player;
- test-marker propagation through the runner and test helpers;
- preview-enabled normal production builders and preview-disabled marked test
  builds;
- capability checks in both installers;
- command-package tests in `run-tests.sh` and CI; and
- no new unaudited player, speech, AppleScript sound, raw bell, or terminal
  notification owner in production source roots.

Focused regression tests safely reproduce each former escape using injected
fakes or capability diagnostics. No regression test invokes real host audio.

## Resulting Boundaries

```text
normal interactive build
  user changes Idle Sound
    -> preview capability enabled
    -> host effects allowed
    -> fixed /usr/bin/afplay

go test process
  direct production function call
    -> testing.Testing()
    -> denied

test child (shell or separately built application)
  inherited WISP_DECK_TESTING=1
    -> last-moment runner guard
    -> denied

test-created make build
  inherited WISP_DECK_TESTING=1
    -> preview capability compiled disabled
    -> denied even if environment is later lost
```

## Rejected Alternatives

- **Remove previews:** violates the approved product behavior.
- **Make every developer build silent:** avoids one child-binary escape but
  unnecessarily removes previews from normal `make build`.
- **Environment marker only:** too easy for a helper to strip; the linker-backed
  Go-test check, marked-build behavior, and source invariant remain necessary.
- **PATH interception only:** both audited Go paths use absolute
  `/usr/bin/afplay`.
- **TTY detection:** integration tests legitimately use pseudo-terminals.
- **Process-name heuristics:** test binary and ancestor names are not a stable
  API and are unnecessary with explicit propagation.

## Verification

Implementation follows strict red-green TDD. Completion requires:

1. Focused tests proving each new guard initially fails and then passes.
2. A production-capability child probe proving an enabled binary becomes
   runtime-denied under `WISP_DECK_TESTING=1`.
3. Shell tests proving marked mode is silent without a fake and still testable
   with an explicit fake.
4. Background-notifier tests proving a nil/default real runner is inert inside
   tests while injected fakes still receive both planned calls.
5. Installer tests proving same-version preview-disabled binaries are repaired
   and Intel uses the published asset name.
6. The ownership invariant, command package, race-sensitive focused tests,
   `go vet`, and the complete repository suite under a fake relative `afplay`.
7. `make install`, required command-path and SHA-256 equality checks, a valid
   code signature, and a compiled-preview capability probe returning true.

Running Wisp Deck panes and sessions must be relaunched after installation to
load the new binary.
