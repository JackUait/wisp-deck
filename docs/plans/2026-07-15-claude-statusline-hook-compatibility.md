# Claude Status-Line Compatibility Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:test-driven-development to execute this plan task-by-task.

**Goal:** Restore Claude's configured status and subagent status lines while keeping its native notification channel disabled in every Wisp-managed launch.

**Architecture:** Keep the existing atomic generation-local settings overlay and remove only Wisp's forced `disableAllHooks` assignment. Preserve any explicit source value so Wisp neither enables nor disables hooks against the user's configuration.

**Tech Stack:** Bash, Python 3 JSON generation, Go integration tests.

**Design:** `docs/plans/2026-07-15-claude-statusline-hook-compatibility-design.md`

---

## Guardrails

- Work directly on the existing `main` branch; do not create branches or worktrees.
- Preserve unrelated working-tree changes and stage only task-owned hunks.
- Modify tests before production code and observe the expected failing result.
- Keep `preferredNotifChannel: notifications_disabled`, atomic publication, mode
  0600, source immutability, and relaunch behavior unchanged.
- Run `make install` and the repository-mandated path, hash, and signature checks
  before handoff.

### Task 1: Define the repaired overlay contract

**Files:**

- Modify: `test/bash/claude_launch_settings_test.go:27-109`

**Step 1: Change the active-config expectation**

The existing source fixture contains `"disableAllHooks": false`. Require the
generated overlay to preserve `false` instead of forcing `true`:

```go
if got["disableAllHooks"] != false {
	t.Fatalf("disableAllHooks = %#v, want preserved false", got["disableAllHooks"])
}
```

Retain the hook and plugin preservation checks, but remove wording that assumes
the hooks are globally disabled.

**Step 2: Add an explicit-disable preservation test**

Generate an overlay from a source containing `"disableAllHooks": true` and
assert that it remains true while `preferredNotifChannel` becomes
`notifications_disabled`.

**Step 3: Tighten the source-free contract**

Require a source-free overlay to contain exactly one setting:

```go
if len(got) != 1 || got["preferredNotifChannel"] != "notifications_disabled" {
	t.Fatalf("launch settings = %#v, want only native notification override", got)
}
```

**Step 4: Run the focused tests and verify RED**

Run:

```bash
go test ./test/bash -run 'TestSettingsJsonClaudeLaunchSettings' -count=1
```

Expected: FAIL because production still forces `disableAllHooks: true` and adds
that second key to a source-free overlay.

### Task 2: Remove the conflicting launch override

**Files:**

- Modify: `lib/settings-json.sh:55-98`

**Step 1: Implement the minimal repair**

Delete only:

```python
settings["disableAllHooks"] = True
```

Update the function comment to state that lifecycle and status-line settings
are preserved. Do not change the native notification override or the atomic
write path.

**Step 2: Run the focused tests and verify GREEN**

Run:

```bash
go test ./test/bash -run 'TestSettingsJsonClaudeLaunchSettings' -count=1
```

Expected: PASS.

**Step 3: Run the full settings tests and shell validation**

Run:

```bash
go test ./test/bash -run 'SettingsJson|ClaudeLaunchSettings' -count=1
shellcheck lib/settings-json.sh
git diff --check
```

Expected: PASS with no shell or whitespace diagnostics.

**Step 4: Commit the repair**

```bash
git add lib/settings-json.sh test/bash/claude_launch_settings_test.go
git commit -m "fix(claude): restore status-line execution"
```

### Task 3: Verify and install

**Files:** None expected.

**Step 1: Run the authoritative repository suite**

Run:

```bash
make test
```

Expected: both Go and Bash test phases pass.

**Step 2: Install locally**

Run:

```bash
make install
```

Expected: the binary builds, is ad-hoc signed, and installs successfully.

**Step 3: Verify the installation contract**

Verify that `command -v wisp-deck-tui` is exactly
`~/.local/bin/wisp-deck-tui`, its SHA-256 matches `bin/wisp-deck-tui`, and
`codesign --verify --verbose=4` succeeds.

**Step 4: Verify the final diff and history**

Run:

```bash
git status --short
git log -3 --oneline
```

Expected: no uncommitted changes. Existing Claude sessions launched with the
broken overlay must be relaunched to load the repaired setting.
