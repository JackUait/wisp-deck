# Test-Safe Sound Preview Design

## Problem

`MainMenuModel.CycleSoundName` and `CycleSoundNameReverse` currently persist the
new selection and immediately start the host `afplay` process. Unit tests call
those public state-transition methods, so ordinary `go test` runs play real
macOS system sounds. The per-agent idle-sound preference cannot stop this path
because a settings preview is intentionally separate from idle notification
playback.

The observed failure is deterministic: `test/internal/tui` starts twelve
previews and `internal/tui` starts three. macOS audio logs showed matching
twelve- and three-process speaker bursts during an agent's repository test run.

## Decision

Keep intentional previews in the interactive Settings screen, but remove all
audio execution from reusable menu state transitions.

`MainMenuModel` will hold an instance-scoped, nil-by-default preview
capability. Cycling a sound will only update and persist state. The interactive
Settings handlers will request a preview after a successful cycle and return it
as a Bubble Tea command. With no injected capability, the request is a no-op.

The `main-menu` command will inject the real macOS player only after interactive
TTY setup succeeds. `buildMainMenuModel` and every ordinary `NewMainMenu` call
remain silent. The real adapter will use the absolute `/usr/bin/afplay` path and
accept only names selected from the existing `SystemSounds` allowlist.

This creates a fail-closed capability boundary:

```
tests / reusable model
    sound selection -> persistence -> nil preview capability -> silence

interactive main-menu
    sound selection -> persistence -> injected Tea command -> /usr/bin/afplay
```

## Interaction Semantics

- Enter, Right, or Left on the Idle Sound row previews the newly selected sound.
- Selecting `Off` never creates a preview command.
- A failed persistence operation rolls the selection back and never previews.
- Calling `CycleSoundName` or `CycleSoundNameReverse` programmatically never
  plays audio.
- Existing idle-notification playback remains unchanged and continues to honor
  the per-agent live preference gate.

## Regression Boundary

Tests will prove both sides of the boundary:

1. A PATH-level `afplay` spy receives zero calls when both TUI packages run
   their sound-cycling tests.
2. An injected preview callback receives the selected allowlisted sound only
   from interactive Settings activation.
3. `Off` and failed persistence produce no preview.
4. The production model builder has no preview capability.
5. A repository ownership guard rejects audio process markers in
   `internal/tui`, test files, or any new unaudited source. The only Settings
   preview executable site is the explicit command-layer adapter.

The guard prevents future refactors from quietly restoring process execution to
state-transition methods or tests.

## Rejected Alternatives

- **TTY detection:** tests can run under pseudo-terminals, while legitimate
  output can be piped. It is not a reliable automation boundary.
- **Test-process or environment detection:** each new test package would need
  to preserve convention, and subprocess integration tests could bypass it.
- **PATH-only shims:** absolute executable paths and focused test invocations
  can bypass a harness.
- **Removing previews:** this would guarantee silence but discard intentional
  user-facing behavior. The approved design keeps previews behind an explicit
  production-only capability instead.

## Verification

Run focused red/green tests with an `afplay` spy, both complete TUI packages,
the audio ownership guard, the repository test runner, `go vet`, and
`git diff --check`. Then run `make install` and verify the installed command
path, SHA-256 equality, and code signature. Existing ledger panes and agent
sessions must be relaunched to load the fixed binary.
