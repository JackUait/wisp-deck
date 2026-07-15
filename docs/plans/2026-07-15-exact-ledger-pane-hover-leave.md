# Exact Ledger Pane Hover Leave Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Keep ledger hover visible while the pointer is stationary and clear it only when mouse movement enters another tmux pane.

**Architecture:** Remove the native idle timer and install a Wisp-session-specific tmux key table. Its `Any` fallback receives tmux's otherwise-unbindable no-button motion event, sends a synthetic out-of-bounds SGR report to the ledger when the target pane changes, then forwards the original event normally.

**Tech Stack:** Go, Bubble Tea, Bash 3.2, tmux 3.6a key tables, isolated tmux PTY integration tests.

---

Project instructions prohibit worktrees, so execute these tasks directly on the existing `main` branch. Do not stage or modify concurrent main-menu/worktree changes.

### Task 1: Lock stationary-hover behavior

**Files:**
- Modify: `test/bash/native_ledger_pty_test.go`
- Modify: `internal/tui/ledger_test.go`

**Step 1: Replace the idle-expiry PTY assertion**

After hovering `file_00000.go`, wait longer than the former 250 ms timeout and assert that no redraw is emitted:

```go
time.Sleep(400 * time.Millisecond)
if delta := session.capture.after(offset); delta != "" {
    t.Fatalf("stationary hover emitted a redraw: %q", delta)
}
```

Keep the existing same-row motion no-redraw assertion and re-hover only when a later test step needs it.

**Step 2: Add a model-level persistence test**

Create a model with the production hover configuration, establish hover, deliver any old timeout command, and assert hover remains. The test should express the desired API by requiring timeout-free `LedgerOptions`.

**Step 3: Run tests to verify RED**

Run:

```bash
go test ./test/bash -run TestNativeLedgerPTYInteractionParity10k -count=1
```

Expected: FAIL because the current idle timer redraws and clears hover.

### Task 2: Remove native idle expiry

**Files:**
- Modify: `cmd/wisp-deck-tui/ledger.go`
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`

**Step 1: Remove command wiring**

Delete `ledgerHoverTimeout` and the `HoverTimeout` field passed to `tui.LedgerOptions`.

**Step 2: Remove model timer state**

Delete:

- `LedgerOptions.HoverTimeout`
- `LedgerModel.hoverTimeout`, `hoverDeadline`, and `hoverTimerActive`
- `ledgerHoverTimeoutMsg`
- `scheduleLedgerHoverTimeout`
- `expireLedgerHover`

Return `nil` from ordinary hover updates again. Keep the exact horizontal bound:

```go
if msg.X < 0 || msg.X >= m.width {
    m.state.HoverScreenRow(0)
    return
}
```

Do not introduce an edge sentinel; `X == width-1` is valid.

**Step 3: Run tests to verify GREEN**

```bash
go test ./internal/tui -run '^TestLedgerMouse' -count=1
go test ./test/bash -run TestNativeLedgerPTYInteractionParity10k -count=1
```

Expected: PASS, with stationary hover surviving the wait.

**Step 4: Commit the timeout removal**

```bash
git add cmd/wisp-deck-tui/ledger.go internal/tui/ledger.go internal/tui/ledger_test.go test/bash/native_ledger_pty_test.go
git commit -m "fix(ledger): preserve stationary hover"
```

### Task 3: Specify exact tmux routing

**Files:**
- Create: `test/bash/ledger_hover_routing_test.go`
- Test: `lib/ledger-hover.sh`

**Step 1: Add a real tmux capture helper**

Use the test binary as a child pane process when a capture-path environment variable is set. Put its stdin in raw mode, enable SGR any-motion reporting, and copy received bytes to the capture file.

**Step 2: Add the isolated two-pane test**

The test must:

1. Start an isolated tmux server with global mouse enabled.
2. Start the raw capture helper in both panes.
3. Source `lib/ledger-hover.sh` and call `ledger_hover_install` with the isolated tmux shim, session name, and ledger pane ID.
4. Attach a 100x20 PTY client.
5. Send motion inside the ledger and verify the ledger receives it.
6. Wait 400 ms and verify no synthetic leave arrived while stationary.
7. Send motion directly into the right pane.
8. Verify the ledger receives `ESC[<35;9999;9999M` and the right pane receives the original motion.
9. Select the ledger, send an ordinary key, and verify it is forwarded unchanged.
10. Verify a copied root binding still exists in the session table.

**Step 3: Run the test to verify RED**

```bash
go test ./test/bash -run TestLedgerHoverRouting -count=1 -v
```

Expected: FAIL because `lib/ledger-hover.sh` and `ledger_hover_install` do not exist.

### Task 4: Implement scoped tmux routing

**Files:**
- Create: `lib/ledger-hover.sh`
- Modify: `test/bash/ledger_hover_routing_test.go`

**Step 1: Add deterministic table naming**

```bash
ledger_hover_table_name() {
  local safe="${1//[^a-zA-Z0-9_]/_}"
  printf 'wisp-ledger-%s' "$safe"
}
```

**Step 2: Clone the active default table**

`ledger_hover_install <tmux-cmd> <session> <ledger-pane>` reads the effective session `key-table` (falling back to `root`), clones its bindings into the unique table through a temporary tmux source file, and leaves the session unchanged on any failure.

**Step 3: Install the `Any` fallback**

Bind an internal tmux command sequence equivalent to:

```tmux
if -F '#{&&:#{mouse_pane},#{!=:#{mouse_pane},LEDGER}}' \
  'send-keys -t LEDGER -H 1b 5b 3c 33 35 3b 39 39 39 39 3b 39 39 39 39 4d' '' ; \
if -F '#{mouse_pane}' 'send-keys -M' 'send-keys'
```

The first command injects `ESC[<35;9999;9999M`. `send-keys -M` preserves the original mouse metadata and target pane. Argumentless `send-keys` re-injects the current ordinary key event.

Only after cloning and binding succeed should the installer set the session's `key-table` option.

**Step 4: Add idempotent cleanup**

`ledger_hover_uninstall <tmux-cmd> <session>` restores the saved base table when the session still exists, deletes the cloned table, and succeeds when called repeatedly.

**Step 5: Run the real tmux test to verify GREEN**

```bash
go test ./test/bash -run TestLedgerHoverRouting -count=1 -v
shellcheck lib/ledger-hover.sh
```

Expected: PASS with no ShellCheck diagnostics.

**Step 6: Commit the router**

```bash
git add lib/ledger-hover.sh test/bash/ledger_hover_routing_test.go
git commit -m "fix(tmux): route ledger pane leave"
```

### Task 5: Wire routing into Wisp sessions

**Files:**
- Modify: `wrapper.sh`
- Modify: `lib/tmux-session.sh`
- Modify: `test/bash/wrapper_layout_test.go`
- Modify: `test/bash/attention_runtime_test.go`

**Step 1: Add failing wrapper assertions**

Assert the recorded `new-session` chain calls `ledger_hover_install` through `run-shell`, passes the current `#{pane_id}` before the horizontal split, and preserves geometric pane selection. Assert cleanup calls `ledger_hover_uninstall` before killing the session.

**Step 2: Run tests to verify RED**

```bash
go test ./test/bash -run 'TestWrapper.*ledger.*hover|TestAttention.*cleanup' -count=1
```

Expected: FAIL because the wrapper is not wired.

**Step 3: Load and install the helper**

Add `ledger-hover` to `_gt_libs`. Build a shell-quoted `run-shell` command that sources `lib/ledger-hover.sh` and calls:

```bash
ledger_hover_install "$TMUX_CMD" "$SESSION_NAME" "#{pane_id}"
```

Place it before `split-window -h`, while the command target is still the ledger pane. Setup is best-effort and must not abort session creation.

**Step 4: Clean up the table**

Call `ledger_hover_uninstall "$tmux_cmd" "$session_name"` from `cleanup_tmux_session` before `kill-session`.

**Step 5: Run tests to verify GREEN**

```bash
go test ./test/bash -run 'TestWrapper|TestLedgerHoverRouting|TestAttention' -count=1
shellcheck wrapper.sh lib/tmux-session.sh lib/ledger-hover.sh
```

Expected: PASS with no ShellCheck output.

**Step 6: Commit the integration**

```bash
git add wrapper.sh lib/tmux-session.sh test/bash/wrapper_layout_test.go test/bash/attention_runtime_test.go
git commit -m "fix(tmux): install ledger leave routing"
```

### Task 6: Verify, install, and hand off

**Step 1: Run focused verification**

```bash
go test ./internal/ledger ./internal/tui ./cmd/wisp-deck-tui -count=1
go test ./test/bash -run 'NativeLedger|LedgerHoverRouting|Wrapper' -count=1
shellcheck wrapper.sh lib/tmux-session.sh lib/ledger-hover.sh
git diff --check
```

Expected: all commands pass. If unrelated concurrent tests fail, isolate and report them without changing their files.

**Step 2: Request code review**

Review the scoped diff for input loss, recursion, global key-table leakage, quoting, cleanup, and mouse-flood performance. Resolve all Critical and Important findings.

**Step 3: Update the local installation**

```bash
make install
```

**Step 4: Verify installation integrity**

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
cmp -s bin/wisp-deck-tui "$HOME/.local/bin/wisp-deck-tui"
shasum -a 256 bin/wisp-deck-tui "$HOME/.local/bin/wisp-deck-tui"
codesign --verify --deep --strict --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: paths and hashes match; code signature is valid.

**Step 5: Handoff**

Report the exact tmux root cause, session-scoped routing fix, verification results, installation hash, and that existing Wisp sessions must be relaunched to install the new key table and binary.
