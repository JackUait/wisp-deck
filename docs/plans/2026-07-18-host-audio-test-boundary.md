# Host-Audio Test Boundary Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Keep intentional sound previews in normal builds while making every audited repository-owned host-effect path fail closed during tests and their descendants.

**Architecture:** Use a global disabled-by-default host-effects build
capability, Go's in-process test identity, an exact versioned argv0
repository-test sentinel, exact exec-time markers where XNU exposes ancestor
environments, and a fail-closed Darwin ancestry lookup. Treat a `.test`
ancestor name only as defense in depth. Collapse all real player/Notification
Center process creation into one typed Go runner; shell notification playback
delegates to that runner and marked shell tests execute no player. Version the
boundary in a machine-readable capability command so installers and release
tooling reject incomplete binaries.

**Tech Stack:** Go 1.25, Cobra, `golang.org/x/sys/unix`, Bash, Node.js, macOS `sysctl`, `/usr/bin/afplay`, and existing Go integration tests.

**Repository constraint:** Work directly on the existing `main` branch. Never create a branch, worktree, or detached checkout.

## Review correction: XNU-redaction-safe test identity

This correction is authoritative anywhere the original task steps below refer
to an environment-marker-only ancestry contract.

XNU redacts `KERN_PROCARGS2` environment entries for restricted executables
such as `/bin/bash` and `/bin/zsh`; their full argv remains visible. Repository
test identity therefore consists of the marker `WISP_DECK_TESTING=1` and the
exact versioned argv0 sentinel
`__WISP_DECK_REPOSITORY_TEST_V1__.test`. Every executable repository `go test`
entrypoint routes through executable `scripts/go-test.sh`, which exports the
marker and runs `exec -a '<sentinel>' go test "$@"`. Make, `run-tests.sh`, both
workflows, and release preflight contain no executable raw `go test` command.

The Bash and Npx `TestMain` functions re-exec unless both the exact marker and
exact argv0 sentinel are present. They copy `os.Args`, replace only argv0,
normalize the environment, and call `syscall.Exec` once. Production ancestry
parsing retains executable, argv, and environment separately. It checks exact
argv0 sentinel first, then exact environment marker, then the full `.test`
executable fallback. Sentinel and marker are conclusive; `.test` requires a
successful walk to PID 1. A marker-only restricted ancestor is out of contract
because XNU has information-hidden the only marker signal.

**Before Task 1:** Commit this reviewed plan and its design so execution starts
from a clean documented state:

```bash
git add -f docs/plans/2026-07-18-host-audio-test-boundary-design.md \
  docs/plans/2026-07-18-host-audio-test-boundary.md
git commit -m "docs(sound): plan host-effect boundary"
```

---

### Task 1: Make test-mode propagation unavoidable in current harnesses

**Files:**
- Create: `scripts/go-test.sh`
- Create: `test/bash/main_test.go`
- Create: `test/npx/main_test.go`
- Create: `test/bash/test_mode_contract_test.go`
- Modify: `test/bash/helpers_test.go:166-225`
- Modify: `lib/notification-setup.sh:5-25`
- Modify: `test/bash/notification_update_test.go:170-220`
- Modify: `test/bash/tab_title_watcher_test.go:587-667`
- Modify: `test/npx/helpers_test.go:1-45`
- Modify: `test/npx/install_e2e_test.go:140-150`
- Modify: `run-tests.sh:1-6`
- Modify: `Makefile:20-36`
- Modify: `.github/workflows/tests.yml:15-55`
- Modify: `.github/workflows/install.yml:30-85`

**Step 1: Write failing propagation tests**

Add this Bash helper test:

```go
func TestBuildEnvForcesRepositoryTestMode(t *testing.T) {
	env := buildEnv(t, nil, "WISP_DECK_TESTING=0")
	got := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "WISP_DECK_TESTING=") {
			got = strings.TrimPrefix(entry, "WISP_DECK_TESTING=")
		}
	}
	if got != "1" {
		t.Fatalf("WISP_DECK_TESTING = %q, want 1", got)
	}
}
```

Add a pure Npx helper test for:

```go
func repositoryTestEnvironment(env []string) []string
```

It must copy the input, remove every existing `WISP_DECK_TESTING=*` entry, and
append exactly one `WISP_DECK_TESTING=1`.

In `test_mode_contract_test.go`, inspect source and require exact propagation in
`run-tests.sh`, Makefile test targets, both workflows, both TestMain files,
`buildEnv`, `runLauncher`, and `installSandbox.run`. The TestMain contract must
require a one-time `syscall.Exec` with normalized copied arguments and marker
environment, not `os.Setenv`: `KERN_PROCARGS2` exposes exec-time state, does
not see later in-process mutations, and may redact restricted environments.
Add pure argument-normalizer tests requiring exact sentinel argv0, preserved
remaining arguments, input copying, and safe empty input.

Before installing the package-wide marker, replace the legacy shell tests that
execute a PATH-shadowed `afplay`. First add a safe source-contract test requiring
the exact marked-mode return to be the first executable statement; this is the
RED test and must not call `play_notification_sound`. After the source guard is
implemented, add a runtime marked-mode test that places only a `wisp-deck-tui`
recorder on PATH, calls `play_notification_sound`, waits for background jobs,
and requires the recorder to remain absent. At no point may the test create or
invoke any player. Keep preference parsing/validation coverage pure.

**Step 2: Verify RED**

```bash
go test ./test/bash -run 'TestBuildEnvForcesRepositoryTestMode|TestRepositoryTestEntrypointsPropagateMode|TestNotificationTestModeGuardSource' -count=1
go test ./test/npx -run '^TestRepositoryTestEnvironment$' -count=1
```

Expected: failures because helpers and entrypoints currently strip or omit the
marker, and because the shell helper does not yet deny marked mode.

**Step 3: Implement propagation**

In `buildEnv`, apply all normal overrides first, then remove any test-marker
entry and append exactly:

```go
env = append(env, "WISP_DECK_TESTING=1")
```

At the very start of `play_notification_sound`, add:

```bash
[[ "${WISP_DECK_TESTING:-}" == "1" ]] && return 0
```

This early boundary is required in the same change as `test/bash/TestMain`, so
turning on package-wide test mode never leaves the existing shell suite in an
unsafe or broken intermediate state. Task 4 removes the remaining production
shell player ownership and delegates normal playback to Go.

Implement `repositoryTestEnvironment` in `test/npx/helpers_test.go`, call it in
`runLauncher`, and use it when assigning `cmd.Env` in `installSandbox.run`.
Implement the same pure normalizer in the Bash test package and use it at the
end of `buildEnv`.

Create both TestMain files with the package-specific package declaration and
this one-time re-exec shape:

```go
func TestMain(m *testing.M) {
	if os.Getenv("WISP_DECK_TESTING") != "1" ||
		len(os.Args) == 0 ||
		os.Args[0] != repositoryTestArgv0 {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		environment := repositoryTestEnvironment(os.Environ())
		arguments := repositoryTestArguments(os.Args)
		if err := syscall.Exec(executable, arguments, environment); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}
```

The environment normalizer removes every prior marker entry and appends exactly
one `WISP_DECK_TESTING=1`. The argument normalizer copies the slice, preserves
arguments 1 onward, and sets argv0 to
`__WISP_DECK_REPOSITORY_TEST_V1__.test`. Re-exec preserves all Go test flags
and standard streams. Add an exact allowlist for this non-audio `syscall.Exec`
shape to the final source invariant.

Create executable `scripts/go-test.sh` with strict Bash mode, export the marker,
and execute `exec -a '__WISP_DECK_REPOSITORY_TEST_V1__.test' go test "$@"`.
Route `run-tests.sh`, Make test commands, every direct Go-test step in both
workflows, and release preflight through it while preserving arguments,
statuses, and workflow pipes. The driver is the only executable raw `go test`
owner. Keep job-level:

```yaml
env:
  WISP_DECK_TESTING: "1"
```

in test/install workflows.

**Step 4: Verify GREEN**

```bash
go test ./test/bash -run 'TestBuildEnvForcesRepositoryTestMode|TestRepositoryTestEntrypointsPropagateMode|TestNotificationTestMode' -count=1
go test ./test/npx -run 'TestRepositoryTestEnvironment|TestLauncher_copies_bash_files' -count=1
go test ./test/bash ./test/npx -timeout=20m -count=1
```

Expected: PASS, including the complete affected packages after package-wide
exec-time test mode is enabled.

**Step 5: Commit**

```bash
git add test/bash/main_test.go test/npx/main_test.go \
  test/bash/test_mode_contract_test.go test/bash/helpers_test.go \
  lib/notification-setup.sh test/bash/notification_update_test.go \
  test/bash/tab_title_watcher_test.go \
  test/npx/helpers_test.go test/npx/install_e2e_test.go \
  run-tests.sh Makefile .github/workflows/tests.yml \
  .github/workflows/install.yml
git commit -m "test(sound): propagate silent test mode"
```

---

### Task 2: Add a global build, process, and ancestry boundary

**Files:**
- Create: `cmd/wisp-deck-tui/host_effects_policy.go`
- Create: `cmd/wisp-deck-tui/host_effects_policy_test.go`
- Create: `cmd/wisp-deck-tui/host_effects_ancestry_darwin.go`
- Create: `cmd/wisp-deck-tui/host_effects_ancestry_other.go`
- Create: `cmd/wisp-deck-tui/host_effects_ancestry_test.go`
- Create: `cmd/wisp-deck-tui/capabilities.go`
- Create: `cmd/wisp-deck-tui/capabilities_test.go`
- Modify: `cmd/wisp-deck-tui/main_menu.go:94-165`
- Modify: `cmd/wisp-deck-tui/main_menu_sound_test.go:63-140`
- Modify: `Makefile:1-15`
- Modify: `scripts/release.sh:136-145`
- Modify: `test/bash/small_test.go:11-37`
- Modify: `test/bash/idle_sound_ownership_test.go:13-310`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write failing pure policy and ancestry tests**

Specify:

```go
func hostEffectsAllowed(
	capability string,
	testBinary bool,
	testEnvironment string,
	testAncestor bool,
	ancestryKnown bool,
) bool
```

Only this row may return true:

```text
capability=enabled, testBinary=false, marker!="1",
testAncestor=false, ancestryKnown=true
```

Every unknown capability, Go test binary, exact test marker, test ancestor, or
ancestry lookup failure returns false.

Back the boolean helper with:

```go
type hostEffectsDecision struct {
	Allowed      bool
	DenialReason string
}
```

`currentHostEffectsDecision` is the single runtime policy; playback uses its
`Allowed` field and capability diagnostics expose only its stable reason.

Define:

```go
type hostProcessInfo struct {
	ParentPID  int
	Executable string
	Arguments []string
	Environment []string
}

type hostProcessLookup func(int) (hostProcessInfo, error)
```

Pure ancestry tests must cover:

- the exact `__WISP_DECK_REPOSITORY_TEST_V1__.test` sentinel only at argv0;
- rejection of the sentinel in argv1 and prefix/suffix variants;
- an exact `WISP_DECK_TESTING=1` in any ancestor environment;
- a full executable basename ending `.test`, including a long name that would
  be truncated by Darwin's `P_comm`, as a defense-in-depth signal;
- normal shell/Node ancestors;
- a renamed test executable that is still detected by its marker;
- malformed/cyclic ancestry and lookup errors returning unknown/denied;
- PID 1 as a validated successful root without reading its protected
  argument/environment record;
- a hard traversal bound.

Do not infer test identity from a short process name.

Add raw `KERN_PROCARGS2` byte fixtures, independent of the already-parsed
ancestry fixtures. They must prove:

- the native-endian argc and NUL boundaries separate executable, argv, and
  environment;
- argv is returned independently and an exact argv0 sentinel is detectable;
- `WISP_DECK_TESTING=1` appearing only in argv is not treated as environment;
- the exact environment entry is distinguished from prefixes such as
  `WISP_DECK_TESTING=10`;
- a long full executable path is recovered without `P_comm` truncation; and
- truncated argc data, missing NUL boundaries, impossible argc, and malformed
  records return errors that fail closed.

Add Darwin-only live tests that:

- start a harmless helper subprocess with the marker in its initial environment
  and prove `KERN_PROCARGS2` returns both its exact full executable and exact
  environment entry; and
- start restricted `/bin/bash` with the exact sentinel argv0, keep it alive,
  and prove its argv0 remains visible and denies even if its environment is
  redacted; and
- look up PID 1 through the production lookup and prove it terminates with
  parent PID 0 without calling `KERN_PROCARGS2` for PID 1.

Use `//go:build darwin` on the Darwin file and
`//go:build !darwin` on `host_effects_ancestry_other.go`, with the build
constraint followed by a blank line and then the package clause.

**Step 2: Write the failing enabled-child integration test**

In `test/bash/small_test.go`, build a temporary child with:

```bash
-X main.HostEffectsCapability=enabled
```

Run `capabilities` beneath a restricted Bash helper and child whose
environments exclude every `WISP_DECK_TESTING` entry. Set only the helper's
exact argv0 sentinel and use a background child plus `wait` so Bash cannot
tail-exec it. Require:

```json
{
  "host_effects_compiled": true,
  "sound_preview_compiled": true,
  "host_effects_boundary": 1,
  "host_effects_allowed": false,
  "host_effects_denial_reason": "test_ancestor_sentinel"
}
```

The distinct stable denial reason proves the child sees the non-redacted exact
sentinel rather than its own environment, a farther marker, or the `.test`
filename fallback. No audio command is invoked.

Also extend the existing Makefile build test: marked `make build` must report
both compiled fields false even when invoked as:

```bash
make WISP_DECK_TESTING=0 HOST_EFFECTS_CAPABILITY=enabled build
```

The command-line overrides must not defeat test mode. Any defined
`WISP_DECK_TESTING` Make selector—`1`, `0`, or empty—must choose the globally
disabled build; normal production builds leave the selector undefined.

**Step 3: Write failing ownership mutations**

Before production edits, update the ownership test to reject:

- default `HostEffectsCapability = "enabled"`;
- a marked Make build that honors a command-line enabled override;
- a preview process policy that omits any of global capability, Go test,
  current marker, ancestry sentinel/marker/path, or fail-closed lookup;
- a capability command whose boundary version is absent or not `1`.

Keep current preview allowlist/deferred-command/injection assertions.

**Step 4: Verify RED**

```bash
go test ./cmd/wisp-deck-tui -run 'TestHostEffectsAllowed|TestHostProcessAncestry|TestCapabilities' -count=1
go test ./test/bash -run 'TestMakefile_build_creates_binary|TestEnabledChild|TestIdleSound' -count=1
```

Expected: build/test failures because the global capability, ancestry reader,
and capability command do not exist.

**Step 5: Implement the policy**

Use:

```go
var HostEffectsCapability = "disabled"
const HostEffectsBoundaryVersion = 1
const wispDeckTestingEnvironment = "WISP_DECK_TESTING"
const wispDeckRepositoryTestSentinel = "__WISP_DECK_REPOSITORY_TEST_V1__.test"
```

`currentHostEffectsAllowed` must short-circuit false for disabled capability,
`testing.Testing()`, or the current marker. Otherwise it walks from
`os.Getpid()` through Darwin process metadata, checking exact argv0 sentinel
before environment marker and `.test` fallback. Sentinel and marker are
conclusive; a lookup or parse error without them is fail-closed. Reaching the
well-known PID 1 boundary is successful traversal;
after `SysctlKinfoProc` validates PID 1 and parent PID 0, do not call
`KERN_PROCARGS2` for PID 1 because macOS returns `EINVAL`.

On Darwin, use `unix.SysctlKinfoProc("kern.proc.pid", pid)` for PPID and
`unix.SysctlRaw("kern.procargs2", pid)` for the full executable/argv/environment
record. Put parsing in a pure helper with fixtures. On non-Darwin, return an
unsupported error so host effects remain disabled.

Make `golang.org/x/sys` a direct dependency and run `go mod tidy`.

**Step 6: Implement capability diagnostics**

The hidden `capabilities` command emits exact JSON:

```go
type binaryCapabilities struct {
	HostEffectsCompiled    bool   `json:"host_effects_compiled"`
	SoundPreviewCompiled   bool   `json:"sound_preview_compiled"`
	HostEffectsBoundary    int    `json:"host_effects_boundary"`
	HostEffectsAllowed     bool   `json:"host_effects_allowed"`
	HostEffectsDenialReason string `json:"host_effects_denial_reason,omitempty"`
}
```

Define stable denial values for compiled-disabled, Go test binary, current test
marker, ancestor test sentinel, ancestor test marker, `.test` ancestor
fallback, and unknown ancestry. Sentinel wins over marker and `.test`; marker
wins over `.test`. This ordering makes the enabled-child integration prove the
non-redacted structural signal.

Add `--require-production`; it exits nonzero unless compiled host effects and
sound previews are true and the boundary is exactly `1`. Runtime test denial
must not make a valid production artifact fail installation verification.
The command always emits its JSON before applying the required-production exit
status.

Use a command factory so tests do not share mutable Cobra flag state.

**Step 7: Make marked builds globally disabled**

Use non-overridable Make assignments:

```make
ifeq ($(origin WISP_DECK_TESTING),undefined)
override HOST_EFFECTS_CAPABILITY := enabled
else
override HOST_EFFECTS_CAPABILITY := disabled
endif
```

Stamp `main.HostEffectsCapability`. Release builds always stamp it enabled.
Ordinary `go build` remains disabled.

Temporarily adapt main-menu injection/playback policy to call the new global
policy; Task 3 moves actual execution into the sole typed runner.

**Step 8: Verify GREEN**

```bash
go test ./cmd/wisp-deck-tui -run 'TestHostEffectsAllowed|TestHostProcessAncestry|TestCapabilities|TestMainMenuSound' -count=1
go test ./test/bash -run 'TestMakefile_build_creates_binary|TestEnabledChild|TestIdleSound' -count=1
go test ./cmd/wisp-deck-tui ./test/bash -run 'TestHost|TestIdleSound|TestMainMenuSound|TestMakefile' -count=1
```

Expected: PASS, including an enabled child beneath a marker-free restricted
helper identified only by its exact sentinel argv0.

**Step 9: Commit**

```bash
git add cmd/wisp-deck-tui/host_effects_policy.go \
  cmd/wisp-deck-tui/host_effects_policy_test.go \
  cmd/wisp-deck-tui/host_effects_ancestry_darwin.go \
  cmd/wisp-deck-tui/host_effects_ancestry_other.go \
  cmd/wisp-deck-tui/host_effects_ancestry_test.go \
  cmd/wisp-deck-tui/capabilities.go \
  cmd/wisp-deck-tui/capabilities_test.go \
  cmd/wisp-deck-tui/main_menu.go \
  cmd/wisp-deck-tui/main_menu_sound_test.go \
  Makefile scripts/release.sh test/bash/small_test.go \
  test/bash/idle_sound_ownership_test.go go.mod go.sum
git commit -m "fix(sound): add global test boundary"
```

---

### Task 3: Collapse Go host effects into one typed runner

**Files:**
- Create: `cmd/wisp-deck-tui/host_effects.go`
- Create: `cmd/wisp-deck-tui/host_effects_test.go`
- Create: `cmd/wisp-deck-tui/notification_sound.go`
- Create: `cmd/wisp-deck-tui/notification_sound_test.go`
- Modify: `cmd/wisp-deck-tui/main_menu.go:94-145`
- Modify: `cmd/wisp-deck-tui/main_menu_sound_test.go:49-98`
- Modify: `cmd/wisp-deck-tui/claude_background.go:388-396,839-936`
- Modify: `cmd/wisp-deck-tui/claude_background_test.go:864-1094`
- Modify: `test/bash/idle_sound_ownership_test.go:13-1120`

**Step 1: Write failing typed-effect tests**

Define an unexported effect enum/struct supporting only:

- an allowlisted macOS system sound; and
- the fixed generic Claude background visual notification.

Pure planner tests require exact `/usr/bin/afplay`,
`/System/Library/Sounds/<allowlisted>.aiff`, exact `/usr/bin/osascript`, and the
existing privacy-preserving title/body environment. Invalid sounds must not
produce an effect.

Do not call the production runner with a valid host effect from any `_test.go`.
A guard-regression test that does so could launch the real player while still
passing on a zero exit status. Prove last-moment policy use through pure policy
tests plus the mutation-tested source/AST invariant; runner tests may call only
invalid effects that cannot plan a process.

**Step 2: Write failing background and notification-command tests**

Replace `claudeBackgroundNotifier.Run` tests with pure effect-planning tests.
Specify:

```go
func withConfiguredNotificationSound(
	features string,
	play func(string) error,
) error
```

The callback receives only a validated sound name, never an executable/args.
Use it to preserve the lock-linearization test with channels.

Add a hidden `notification-sound --features-file <path>` command. Its command
factory may accept only a validated-sound callback—not an executable, argument
slice, generic executor, or typed-effect runner. Tests cover
missing/invalid/Off/On/unsafe names through that harmless callback. No test
invokes the production command adapter or production host-effect runner with an
enabled sound.

**Step 3: Write failing sole-owner mutations**

At this task's intermediate boundary, the ownership test must reject in
production Go roots:

- any player, speech, sound-producing AppleScript, OSC notification, or other
  resolved host-effect process creation through `exec.Command*`,
  `os.StartProcess`, or `syscall.Exec` outside `host_effects.go`;
- any generic executable/args or executor callback parameter accepted by the
  runner;
- loss or reordering of the runtime policy before host-effect process
  construction, exact process-group cancellation, discarded stdio, bounded
  `WaitDelay`, or synchronous `Run`;
- replacement of `Run` with `Start`, a detached/unwaited process, or any path
  that can construct a command before the policy allows it;
- `claudeBackgroundNotifier.Run`;
- preview-specific `runMainMenuSoundWith`;
- notification playback outside the shared preference lock.

Allow the intentional non-sound session-restore AppleScript, existing
non-effect Git/agent/process-inspection/screenshot subprocesses, and existing
OSC0 title terminators only through exact file/function/argument-shape checks.
The existing production shell `afplay` owner remains temporarily guarded by
Task 1's marked-mode return; Task 4 removes it and expands the invariant across
shell production roots. Do not require the Task 4 end state in this task's
GREEN test.

**Step 4: Verify RED**

```bash
go test ./cmd/wisp-deck-tui -run 'TestHostEffect|TestClaudeBackground|TestNotificationSound' -count=1
go test ./test/bash -run 'TestIdleSound|TestMainMenuSoundPreviewOwnershipGuardRejectsBypasses' -count=1
```

Expected: failures because the separate Go preview/background host-effect
owners and generic injected runners still exist.

**Step 5: Implement the sole runner**

Only `host_effects.go` may call `exec.CommandContext` for a host effect. It
first calls `currentHostEffectsAllowed`, validates the typed effect, configures
the existing process-group cancellation/wait behavior, discards stdio, and
waits with `cmd.Run()`. Existing unrelated process calls stay exact-audited.
Because no valid effect is ever executed from tests, AST/mutation tests must
verify those ordering and lifecycle properties directly.

Move main-menu playback to the typed system-sound effect. Remove
`mainMenuSoundRunner` and `runMainMenuSoundWith`.

Remove `Run` from `claudeBackgroundNotifier`. Plan the visual notification and
configured sound as typed effects, and call the sole runner. Preserve separate
timeouts and lock duration.

Implement `notification-sound` with the canonical `soundpref` reader and
exclusive lock held through typed sound playback.

**Step 6: Verify GREEN**

```bash
go test ./cmd/wisp-deck-tui -run 'TestHostEffect|TestMainMenuSound|TestClaudeBackground|TestNotificationSound' -count=1
go test ./test/bash -run 'TestIdleSound|TestMainMenuSoundPreviewOwnershipGuardRejectsBypasses' -count=1
go test -race ./cmd/wisp-deck-tui -run 'TestHostEffect|TestClaudeBackgroundNotifierHolds' -count=1
```

Expected: PASS, with no generic process runner exposed to tests.

**Step 7: Commit**

```bash
git add cmd/wisp-deck-tui/host_effects.go \
  cmd/wisp-deck-tui/host_effects_test.go \
  cmd/wisp-deck-tui/notification_sound.go \
  cmd/wisp-deck-tui/notification_sound_test.go \
  cmd/wisp-deck-tui/main_menu.go \
  cmd/wisp-deck-tui/main_menu_sound_test.go \
  cmd/wisp-deck-tui/claude_background.go \
  cmd/wisp-deck-tui/claude_background_test.go \
  test/bash/idle_sound_ownership_test.go
git commit -m "refactor(sound): centralize host effects"
```

---

### Task 4: Remove shell playback and preserve test mode through tmux

**Files:**
- Modify: `lib/notification-setup.sh:5-25`
- Modify: `wrapper.sh:640-680`
- Modify: `test/bash/notification_update_test.go:170-220`
- Modify: `test/bash/tab_title_watcher_test.go:587-667`
- Modify: `test/bash/wrapper_restore_test.go:20-360`
- Modify: `test/bash/test_mode_contract_test.go`
- Modify: `test/bash/idle_sound_ownership_test.go:13-180`

**Step 1: Write failing shell denial/delegation tests**

In marked test mode, place a `wisp-deck-tui` mock on PATH that writes a marker,
call `play_notification_sound` with sound On, wait for shell background jobs,
and require the marker to remain absent. This test cannot invoke any player.

Add source/argument tests requiring the normal branch to invoke:

```text
wisp-deck-tui notification-sound --features-file <tool-features.json>
```

and forbidding `afplay`, `/System/Library/Sounds`, or a test-player executable
variable anywhere in `notification-setup.sh`.

The existing enabled/disabled/invalid shell tests should now validate
preference helpers and safe delegation shape; playback/locking semantics live
in `notification_sound_test.go`.

**Step 2: Write failing tmux propagation tests**

Run the real wrapper with its existing tmux recorder under the forced test
environment. Require the `new-session` command to contain exactly:

```text
-e WISP_DECK_TESTING=1
```

Add a source invariant proving it is stamped into the session environment and
is inherited by spare-session commands rather than unset.

**Step 3: Verify RED**

```bash
go test ./test/bash -run 'TestNotification_.*testMode|TestNotificationDelegation|TestWrapper.*Testing|TestRepositoryTestEntrypoints' -count=1
```

Expected: delegation/ownership and wrapper assertions fail; the marked-mode
runtime assertion already stays green because Task 1 installed the early
return.

**Step 4: Implement shell and tmux boundaries**

Keep the marked-mode return introduced in Task 1 as the first executable
statement. The normal branch backgrounds only the hidden Go command with the
exact features path. It never reads the preference before delegating and never
names or executes a player.

Build a small tmux argument array only when
`[[ "${WISP_DECK_TESTING:-}" == "1" ]]`, containing exactly:

```text
-e WISP_DECK_TESTING=1
```

Expand it into `new-session`. Marked panes receive the exact marker; normal
sessions do not define the variable at all. This preserves preview-capable
normal `make build` behavior while making any defined Make selector
fail-closed.

Expand the Task 3 Go ownership invariant across shell production roots: after
this change, `notification-setup.sh` contains no player/path and the repository
has no shell audio owner. Add a mutation proving a reintroduced shell `afplay`
or system-sound path fails the guard.

Resolve the pre-existing ShellCheck SC2016 reports around the two intentional
single-quoted inner Bash programs in `notification-setup.sh` with narrowly
scoped, documented suppressions or an equivalent safe refactor. Do not add a
file-wide suppression.

**Step 5: Verify GREEN and shell syntax**

```bash
go test ./test/bash -run 'TestNotification|TestTabTitleWatcher_play_notification_sound|TestWrapper.*Testing|TestRepositoryTestEntrypoints' -count=1
bash -n lib/notification-setup.sh wrapper.sh
shellcheck lib/notification-setup.sh wrapper.sh
```

Expected: PASS. Marked shell tests invoke neither player nor delegated binary.

**Step 6: Commit**

```bash
git add lib/notification-setup.sh wrapper.sh \
  test/bash/notification_update_test.go \
  test/bash/tab_title_watcher_test.go \
  test/bash/wrapper_restore_test.go \
  test/bash/test_mode_contract_test.go \
  test/bash/idle_sound_ownership_test.go
git commit -m "fix(sound): route shell through guarded owner"
```

---

### Task 5: Version and verify install/release artifacts

**Files:**
- Modify: `VERSION`
- Modify: `package.json`
- Modify: `lib/install.sh:60-165`
- Modify: `bin/npx-wisp-deck.js:35-175`
- Modify: `scripts/release.sh:55-195`
- Modify: `test/bash/install_test.go:257-370`
- Modify: `test/bash/install_reliability_test.go:100-180`
- Modify: `test/bash/tui_warm_test.go:97-150`
- Modify: `test/bash/release_test.go:300-520`
- Modify: `test/npx/launcher_reliability_test.go:65-165`
- Modify: `test/npx/install_e2e_test.go:128-280`

**Step 1: Write failing exact-verification tests**

For both installers add cases for:

- exact expected version plus `capabilities --require-production` exit success
  and valid JSON fields: keep the existing binary without curl;
- superstring version (`12.23.1` for expected `2.23.1`): reject;
- exact version but missing/failing production capability: replace;
- downloaded exact version but failing capability: reject atomically;
- downloaded exact version plus capability success: install;
- exit-zero empty/malformed JSON, false compiled fields, a non-integer or wrong
  boundary, and wrong JSON types all fail verification.

Fake binaries implement:

```bash
case "$1" in
  --version) echo "wisp-deck-tui version <exact>" ;;
  capabilities)
    printf '%s\n' \
      '{"host_effects_compiled":true,"sound_preview_compiled":true,"host_effects_boundary":1,"host_effects_allowed":false}'
    [ "$2" = "--require-production" ] && exit <0-or-1>
    ;;
esac
```

Node parses exact version text and JSON. Bash compares an exact normalized
version, never a substring, and parses JSON with the already-installed `jq`.
Both require command exit success and exact typed values:

```text
host_effects_compiled == true
sound_preview_compiled == true
host_effects_boundary == 1
```

The runtime allowed/denial fields are diagnostic and do not affect artifact
acceptance under a test ancestor.

Add Intel Bash URL coverage requiring `darwin-amd64`.

**Step 2: Write failing release-order tests**

Require release verification to:

- inspect linker metadata for both arm64 and amd64 artifacts;
- execute `capabilities --require-production` on the host-architecture asset
  with `x86_64 -> amd64` mapping;
- fail before codesign, tag, push, GitHub release, or npm publish;
- reject either missing enabled linker metadata or a failing capability probe.

Use sourced helper tests with fake files/tools, not only substring ordering.

**Step 3: Verify RED**

```bash
go test ./test/bash -run 'TestEnsureWispDeckTui|TestRelease.*Capability|TestRelease.*Artifact|TestEnsureWispDeckTui.*x86_64' -count=1
go test ./test/npx -run 'TestLauncher_.*(version|capability|verifying|up_to_date)' -count=1
```

Expected: failures because current installers check only version substrings and
release tooling does not verify artifacts.

**Step 4: Bump the unreleased version**

Set both files to `2.23.1`. `v2.23.0` predates the boundary and cannot satisfy
the new probe; the next npm launcher must only ship after matching `v2.23.1`
GitHub assets exist. Do not tag, push, publish, or create a release in this
task.

**Step 5: Implement installer verification**

Both installers require:

1. exact version equality; and
2. successful `capabilities --require-production`; and
3. parsed JSON with both compiled booleans exactly true and boundary exactly
   integer `1`.

Use `amd64` for the TUI asset when `detect_arch` returns `x86_64`; leave jq/tmux
architecture behavior unchanged.

Restrict `WISP_DECK_SKIP_TUI_DOWNLOAD=1` to exact repository test mode. Outside
test mode it must not bypass production artifact verification.

**Step 6: Implement pre-publication release verification**

Run all artifact checks immediately after both builds and before signing. Reuse
one enabled linker-flag variable. Any failure exits before tag/push/publish.

**Step 7: Verify GREEN**

```bash
go test ./test/bash -run 'TestEnsureWispDeckTui|TestInstallBinary|TestRelease' -count=1
go test ./test/npx -run 'TestLauncher|TestInstall' -count=1
bash -n lib/install.sh scripts/release.sh
shellcheck lib/install.sh scripts/release.sh
```

Expected: PASS.

**Step 8: Commit**

```bash
git add VERSION package.json lib/install.sh bin/npx-wisp-deck.js \
  scripts/release.sh test/bash/install_test.go \
  test/bash/install_reliability_test.go test/bash/tui_warm_test.go \
  test/bash/release_test.go test/npx/launcher_reliability_test.go \
  test/npx/install_e2e_test.go
git commit -m "fix(install): require host-effect boundary"
```

---

### Task 6: Final ownership audit, verification, and local install

**Files:**
- Modify: `test/bash/idle_sound_ownership_test.go`
- Modify: `run-tests.sh`
- Modify: `.github/workflows/tests.yml`
- Delete: `ghost-tab-tui`
- Verify: repository-wide
- Install: `bin/wisp-deck-tui`, `~/.local/bin/wisp-deck-tui`

**Step 1: Finish the repository invariant with failing mutations**

Before final source changes, add mutations proving the guard rejects:

- a new player/speech/AppleScript-sound/OSC notification owner in any production
  root, including `scripts`;
- a direct or indirect audio launch in any `_test.go`;
- a direct or indirect audio launch in tracked
  `*.{spec,test}.{js,jsx,ts,tsx,mjs,cjs}` or shell test source;
- a tracked Mach-O/executable build artifact that can retain an older,
  unguarded host-effect implementation;
- repository application-launch test code that explicitly unsets/overrides the
  marker or replaces its complete child environment without an enforced
  propagation helper;
- a second `exec.Command*` host-effect owner, while preserving exact-audited
  non-effect process owners;
- a generic executor injected into notifier or preview code;
- loss of global compile denial, current marker, ancestor sentinel/marker/path,
  or fail-closed ancestry;
- loss of shell early denial, Go delegation, tmux propagation, installer
  verification, release ordering, or command-package CI coverage.

Whitelist existing OSC0 BEL title terminators and Codex/terminal filters by
exact file/function shape; do not use a raw substring rule that rejects them.
Allow unrelated Git, tmux, shell-fixture, and process-lifecycle commands to
manage their own environments; the test must distinguish those from audited
Wisp Deck application/installer launch paths.
Exact-audit the TestMain `syscall.Exec` as the one non-audio re-exec that
establishes the kernel-visible marker and exact argv0 sentinel contract.
Exact-allow only the named enabled-child ancestry regression to strip markers
from a restricted helper and child while setting the helper's exact sentinel
argv0. Its distinct `test_ancestor_sentinel` reason must win over farther
marker/`.test` signals. Mutation-test that the exception cannot apply to
another test/function or change any reserved test contract.
Exact-allow the named Make override regression to pass only the literal
`WISP_DECK_TESTING=0` Make variable argument. It may not alter `cmd.Env`, use
`env -u`, launch an application child, or authorize any other test/function.
Mutation-test those boundaries.
Scope “sole definition” assertions to the production Go capability constant,
not test/workflow marker literals.

Define the audited inventory explicitly:

- compiled/runtime source: all tracked text under `cmd/` and `internal/`,
  including workflow helpers such as `cmd/ci-report` that do not link the TUI
  runtime guard;
- shipped text roots from `package.json.files`: the three `bin/` scripts,
  `lib/`, `templates/`, `defaults/`, `ghostty/`, `terminals/`, and
  `wrapper.sh`, with `VERSION` classified as exact metadata-only input;
- shipped/build metadata: `package.json` itself, including every npm lifecycle
  script even though npm ships this manifest outside its `files` array;
- build/release surfaces: `Makefile`, `scripts/`, `run-tests.sh`, and the two
  relevant workflows.

Require every executable repository `go test` entrypoint in those surfaces to
route through `scripts/go-test.sh`; only that strict, executable driver may
contain the raw command, and its exact versioned sentinel is mutation-tested.

Use tracked text files only and exact-ignore binary/generated artifacts such as
the ignored local `bin/wisp-deck-tui`, ignored root `wisp-deck-tui`, and
`.DS_Store`. Delete the tracked legacy `ghost-tab-tui` Mach-O: it is unreferenced
and unshipped but still contains the old player path. Fail if any future tracked
Mach-O executable appears instead of auditable source. Include tracked Go,
TypeScript/JavaScript spec/test, and shell test sources in the no-host-effect
launch audit, with mutations covering both spec/test naming and JavaScript/
TypeScript families. Parse
`package.json.files` and fail if a newly shipped file/root is not assigned to
the audit inventory. Mutation-test that adding a new shipped root or a
host-effect npm lifecycle script cannot escape the scanner.

**Step 2: Verify RED, implement, and verify GREEN**

```bash
go test ./test/bash -run 'TestIdleSound|TestHostEffectOwnership|TestTestSourceAudioLaunch|TestRepositoryTestEntrypoints' -count=1
```

For RED, the new test must fail because the current guard incorrectly accepts
at least one synthetic bad source mutation. Complete the guard so every
synthetic mutation is rejected; the mutation test itself then passes GREEN.

Ensure both `run-tests.sh` and the `go-tests` job in
`.github/workflows/tests.yml` include `./cmd/wisp-deck-tui/...`; keep the
source-contract assertion mutation-tested so CI cannot silently drop it later.

**Step 3: Commit the invariant and complete final reviews**

```bash
gofmt -w cmd/wisp-deck-tui/*.go test/bash/*.go test/npx/*.go
git diff --check
git add test/bash/idle_sound_ownership_test.go run-tests.sh \
  .github/workflows/tests.yml ghost-tab-tui
git commit -m "test(sound): seal host-effect boundary"
```

Dispatch full spec and code-quality reviews. Fix every finding, commit review
fixes with the affected files, and re-review until both pass. The verification
and installation below must run only after that final source state is fixed.

**Step 4: Run focused verification**

```bash
go test ./cmd/wisp-deck-tui -count=5
go test -race ./cmd/wisp-deck-tui -run 'TestHostEffect|TestHostProcess|TestClaudeBackground|TestNotificationSound' -count=1
go test ./test/bash -run 'TestIdleSound|TestHostEffect|TestNotification|TestMakefile|TestEnsureWispDeckTui|TestRelease' -count=1
go test ./test/npx -run 'TestLauncher|TestInstall' -count=1
go vet ./...
shellcheck run-tests.sh lib/notification-setup.sh lib/install.sh \
  scripts/release.sh wrapper.sh
```

Expected: all PASS.

**Step 5: Run the complete suite under an OS host-effect deny policy**

Use macOS's inherited process sandbox as an outer safety net. This creates no
fake player and denies both real audited host-effect executables even if a
regression reaches process creation:

```bash
host_effect_sandbox='(version 1)
  (allow default)
  (deny process-exec (literal "/usr/bin/afplay"))
  (deny process-exec (literal "/usr/bin/osascript"))'
WISP_DECK_TESTING=1 /usr/bin/sandbox-exec -p "$host_effect_sandbox" \
  ./run-tests.sh -p=1 -timeout=20m -count=1
```

Expected: every package passes while the OS refuses either audited host-effect
executable. Static/mutation ownership covers non-process bell/OSC mechanisms.

**Step 6: Audit explicit requirements**

Confirm from fresh source/output:

- normal Make/release builds compile global host effects and previews enabled;
- ordinary Go and marked Make builds compile them disabled;
- marked Make cannot be overridden from the command line;
- an enabled installed-style child and restricted helper with markers removed
  remain denied through the helper's exact sentinel argv0;
- tmux panes retain exact test mode;
- one typed Go runner solely owns real player/Notification Center execution;
- shell production delegates and marked shell tests return before delegation;
- no test API accepts a generic executor or player;
- preview Enter/Left/Right still work, while Off/persistence failure remain
  silent;
- preference locks remain held through configured playback;
- installers require exact version and boundary protocol;
- release verifies both artifacts before any external mutation;
- current roots and CI are covered by mutation-tested invariants;
- no real player, Notification Center effect, bell, or OSC notification was
  invoked during verification.

**Step 7: Install and verify the required local binary**

```bash
make install
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --deep --strict "$HOME/.local/bin/wisp-deck-tui"
"$HOME/.local/bin/wisp-deck-tui" capabilities --require-production
"$HOME/.local/bin/wisp-deck-tui" capabilities
```

Expected: exact command path, matching SHA-256, valid signature, successful
production requirement, compiled fields true, boundary `1`, and runtime allowed
in the normal shell.

**Step 8: Clean handoff**

Confirm `git status` is clean. Report that running ledger panes and Wisp Deck
sessions must be relaunched to load the new binary.
