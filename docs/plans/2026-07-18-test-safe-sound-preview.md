# Test-Safe Sound Preview Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Keep intentional interactive sound previews while making automated and programmatic menu use structurally unable to launch host audio.

**Architecture:** Make sound cycling and persistence pure model transitions. Add a nil-by-default, instance-scoped preview capability that interactive Settings handlers may turn into a Bubble Tea command, and inject a fixed `/usr/bin/afplay` adapter only after the command layer acquires its TTY. The adapter accepts no process runner, ordinary `go build` output leaves its linker capability disabled, production build entrypoints explicitly enable it, and its process boundary is inert in every `go test` binary. Strengthen syntax-aware source ownership tests so audio execution cannot migrate back into model or test code.

**Tech Stack:** Go 1.25, Bubble Tea, macOS `afplay`, existing Go and bash test suites.

**Repository constraint:** Execute directly on the existing `main` branch. Do not create a branch or worktree.

---

### Task 1: Reproduce and eliminate process execution from sound cycling

**Files:**
- Modify: `test/internal/tui/mainmenu_test.go:3280-3700`
- Modify: `internal/tui/mainmenu.go:976-1046`

**Step 1: Write the failing host-process regression test**

Add an external-package test that:

- places an executable `afplay` spy at the front of `PATH`;
- creates a normal model with `tui.NewMainMenu`;
- calls `CycleSoundName` and `CycleSoundNameReverse`; and
- observes for a bounded interval that the spy marker is never created.

The spy is required because the existing bug launches asynchronously. Use
`t.TempDir`, `os.WriteFile`, `t.Setenv`, and a short polling deadline. Report a
clear failure if the marker appears.

**Step 2: Run the test to verify RED**

Run:

```bash
go test ./test/internal/tui -run '^TestMainMenu_SoundCyclingNeverLaunchesHostPlayer$' -count=1
```

Expected: FAIL because the current cycle method starts the PATH-resolved
`afplay` spy.

**Step 3: Make the model transition pure**

Remove both `m.previewSound()` calls and delete the direct `previewSound`
implementation from `internal/tui/mainmenu.go`. Remove the now-unused
`os/exec` import.

Do not change cycling, rollback, or persistence semantics.

**Step 4: Run the regression test to verify GREEN**

Run:

```bash
go test ./test/internal/tui -run '^TestMainMenu_SoundCyclingNeverLaunchesHostPlayer$' -count=1
```

Expected: PASS with no spy marker.

**Step 5: Commit the pure transition**

```bash
git add internal/tui/mainmenu.go test/internal/tui/mainmenu_test.go
git commit -m "fix(tui): make sound cycling silent"
```

---

### Task 2: Add an explicit interactive preview capability

**Files:**
- Modify: `internal/tui/mainmenu.go:220-245`
- Modify: `internal/tui/mainmenu.go:950-1040`
- Modify: `internal/tui/mainmenu.go:2520-2590`
- Modify: `internal/tui/mainmenu.go:2800-2880`
- Test: `internal/tui/mainmenu_render_test.go:370-480`

**Step 1: Write failing capability-boundary tests**

Add focused tests that express the desired API and behavior:

```go
func TestSoundPreview_onlyInteractiveSettingsRequestsInjectedCapability(t *testing.T) {
	var previews []string
	m := newTestMenu()
	m.SetSoundName("")
	m.SetSoundPreview(func(name string) tea.Cmd {
		previews = append(previews, name)
		return func() tea.Msg { return nil }
	})

	m.CycleSoundName()
	if len(previews) != 0 {
		t.Fatalf("programmatic cycle requested previews: %v", previews)
	}

	m.settingsSelected = rowIdleSound
	_, cmd := m.settingsEnter()
	if cmd == nil {
		t.Fatal("interactive Idle Sound activation returned no preview command")
	}
	if !reflect.DeepEqual(previews, []string{"Blow"}) {
		t.Fatalf("previews = %v, want [Blow]", previews)
	}
}
```

Add table coverage for Enter, Right, and Left. Add separate assertions that
selecting `Off` and a failed persistence operation return no preview command.

**Step 2: Run the tests to verify RED**

Run:

```bash
go test ./internal/tui -run 'TestSoundPreview_' -count=1
```

Expected: build FAIL because `SetSoundPreview` does not exist and the settings
value handlers do not return preview commands.

**Step 3: Implement the fail-closed capability**

Add this instance field:

```go
soundPreview func(string) tea.Cmd
```

Add an exported setter:

```go
func (m *MainMenuModel) SetSoundPreview(preview func(string) tea.Cmd) {
	m.soundPreview = preview
}
```

Add a helper that returns nil when the capability is absent or the selected
sound is Off:

```go
func (m *MainMenuModel) soundPreviewCmd() tea.Cmd {
	if m.soundPreview == nil || m.soundName == "" {
		return nil
	}
	return m.soundPreview(m.soundName)
}
```

Only the interactive row handlers call this helper after a successful cycle:

- `settingsEnter` returns it for `rowIdleSound`;
- `settingsValueRight` and `settingsValueLeft` return `tea.Cmd`;
- `focusRight` and `focusLeft` return those commands.

All non-sound branches return nil. Direct cycle methods remain side-effect
free.

**Step 4: Run focused and package tests to verify GREEN**

Run:

```bash
go test ./internal/tui -run 'TestSoundPreview_|TestCycleSoundName|TestSoundNameForResult' -count=1
go test ./internal/tui ./test/internal/tui -count=1
```

Expected: PASS.

**Step 5: Commit the capability boundary**

```bash
git add internal/tui/mainmenu.go internal/tui/mainmenu_render_test.go
git commit -m "feat(tui): inject sound preview capability"
```

---

### Task 3: Wire the audited interactive macOS adapter

**Files:**
- Modify: `cmd/wisp-deck-tui/main_menu.go:1-120`
- Create: `cmd/wisp-deck-tui/main_menu_sound_test.go`

**Step 1: Write failing adapter tests**

Specify a pure, allowlisted command builder and a fixed preview function:

```go
func TestMainMenuSoundCommand_usesAuditedPlayerAndAllowlist(t *testing.T) {
    executable, args, ok := mainMenuSoundCommand("Glass")
    // Assert /usr/bin/afplay, the fixed system path, and ok.
}

func TestRunMainMenuSound_testBinaryCannotLaunchProcesses(t *testing.T) {
    calls := 0
    _ = runMainMenuSoundWith("Glass", func(string, ...string) error {
        calls++
        return nil
    })
    // Assert calls == 0.
}
```

**Step 2: Run the adapter test to verify RED**

Run:

```bash
go test ./cmd/wisp-deck-tui -run '^TestMainMenuSoundPreview_' -count=1
```

Expected: build FAIL because the adapter factory does not exist.

**Step 3: Implement and inject the adapter**

Implement:

- a pure allowlist-to-command function;
- a fixed `func(string) tea.Cmd` preview adapter with no runner parameter;
- a process boundary guarded by `testing.Testing()` and a disabled-by-default
  linker capability;
- a real runner that waits for the allowlisted absolute command.

In `runMainMenu`, call `model.SetSoundPreview(...)` only after
`util.TUITeaOptions()` succeeds. Keep `buildMainMenuModel` silent so command
unit tests and non-interactive construction have no audio capability. Enable
the linker capability only in the Makefile and release build entrypoints.

**Step 4: Run adapter and command tests to verify GREEN**

Run:

```bash
go test ./cmd/wisp-deck-tui -run 'TestMainMenuSoundPreview_|TestBuildMainMenuModel_' -count=1
```

Expected: PASS with only the injected fake runner called.

**Step 5: Commit the interactive adapter**

```bash
git add cmd/wisp-deck-tui/main_menu.go cmd/wisp-deck-tui/main_menu_sound_test.go
git commit -m "fix(tui): wire interactive sound previews"
```

---

### Task 4: Make audio ownership a permanent repository invariant

**Files:**
- Modify: `test/bash/idle_sound_ownership_test.go:10-125`

**Step 1: Update the ownership test for the new boundary**

Require with Go AST inspection rather than textual snippets:

- no audio marker in `internal/tui`;
- exactly one audited Settings preview site in
  `cmd/wisp-deck-tui/main_menu.go`;
- the command adapter uses absolute `/usr/bin/afplay`;
- preview injection occurs after TTY acquisition;
- the process runner is inert in `go test` binaries;
- comments cannot satisfy the ownership checks;
- `buildMainMenuModel` cannot directly or indirectly reach preview injection;
- the existing shell and Claude background sites retain their live preference
  locks; and
- direct process-launch forms using audio markers are absent from `_test.go`
  files, while existing PATH mocks remain allowed.

**Step 2: Run the ownership test**

Run:

```bash
go test ./test/bash -run '^TestIdleSoundRuntimeSitesUseSharedLiveGate$' -count=1
```

Expected: PASS.

**Step 3: Re-run the original symptom with a PATH spy**

Run both affected packages with a temporary `afplay` spy at the front of PATH:

```bash
PATH="<spy-dir>:$PATH" go test ./test/internal/tui ./internal/tui -count=1
```

Expected: both packages PASS and the spy log contains zero calls.

**Step 4: Commit the permanent guard**

```bash
git add test/bash/idle_sound_ownership_test.go
git commit -m "test(sound): enforce preview ownership"
```

---

### Task 5: Verify, install, and audit completion

**Files:**
- Verify: repository-wide
- Build/install: `bin/wisp-deck-tui`, `~/.local/bin/wisp-deck-tui`

**Step 1: Format and run focused checks**

```bash
gofmt -w internal/tui/mainmenu.go \
  internal/tui/mainmenu_render_test.go \
  test/internal/tui/mainmenu_test.go \
  cmd/wisp-deck-tui/main_menu.go \
  cmd/wisp-deck-tui/main_menu_sound_test.go \
  test/bash/idle_sound_ownership_test.go
git diff --check
go test ./internal/tui ./test/internal/tui ./cmd/wisp-deck-tui -count=1
go test ./test/bash -run 'TestIdleSoundRuntimeSitesUseSharedLiveGate|TestTabTitleWatcher_play_notification_sound|TestNotification' -count=1
go vet ./...
```

Expected: PASS.

**Step 2: Run repository tests without host audio**

Run the full test runner with the same temporary PATH spy:

```bash
PATH="<spy-dir>:$PATH" ./run-tests.sh -p=1 -timeout=20m -count=1
```

Expected: all suites PASS and the spy log contains zero calls. If unrelated
environment-sensitive PTY tests fail, record their exact output and separately
prove every sound-related package and invariant passed.

**Step 3: Audit the explicit requirements**

Confirm from current source and test output:

- direct sound cycling cannot execute a process;
- tests construct models with no preview capability;
- Enter/Left/Right still preview in interactive Settings;
- Off and persistence failure never preview;
- the real player is allowlisted and command-layer only;
- no new audio execution sites exist; and
- the original 12-plus-3 burst cannot recur.

**Step 4: Install and verify**

```bash
make install
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
shasum -a 256 bin/wisp-deck-tui "$HOME/.local/bin/wisp-deck-tui"
codesign --verify --deep --strict "$HOME/.local/bin/wisp-deck-tui"
```

Expected: build succeeds, the command resolves to the required path, both
SHA-256 values match, and the installed signature is valid.

Running ledger panes and agent sessions must be relaunched to pick up the fixed
binary.
