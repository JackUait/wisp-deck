# Clean Session First Frame Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make a new Wisp Deck session reveal only after its complete three-pane tmux layout is ready.

**Architecture:** Reuse `loading.sh`'s tested terminal-size detector, create the tmux session detached at that size, and append attachment to the existing tmux command queue after pane construction and focus selection. Enable `exit-unattached` only after attachment so detached construction cannot destroy the session. Preserve the exact pane build order required by layout restoration.

**Tech Stack:** Bash 3.2, tmux command queues, Go integration tests

**Repository constraint:** Execute directly on the existing `main` checkout. Do not create a branch or worktree.

---

### Task 1: Lock down the launch command contract

**Files:**
- Modify: `test/bash/wrapper_layout_test.go`

**Step 1: Make wrapper recording deterministic**

Add a `tput` mock to `recordWrapperNewSession`:

```go
"tput": `#!/bin/bash
case "$1" in
  cols) echo 173 ;;
  lines) echo 47 ;;
  colors) echo 256 ;;
esac
`,
```

**Step 2: Write the failing launch-order test**

Add `TestWrapperBuildsDetachedSessionBeforeAttach`. Assert that the recorded
tmux command contains `new-session -d`, `-x 173`, and `-y 47`. Find the indexes
of the horizontal split, vertical split, final `select-pane -R`,
`attach-session`, and `set-option exit-unattached on`, then require this order:

```text
horizontal split < vertical split < final select right < attach < exit-unattached
```

Also assert there is no `set-option exit-unattached on` before attachment.

**Step 3: Run the focused test to verify it fails**

Run:

```bash
go test ./test/bash -run 'TestWrapperBuildsDetachedSessionBeforeAttach' -count=1 -v
```

Expected: FAIL because `new-session` is attached, has no explicit geometry,
and enables `exit-unattached` before pane construction.

**Step 4: Commit the failing test**

```bash
git add test/bash/wrapper_layout_test.go
git commit -m "test(wrapper): require clean first frame"
```

### Task 2: Construct the workspace off-screen

**Files:**
- Modify: `wrapper.sh:581-616`
- Test: `test/bash/wrapper_layout_test.go`

**Step 1: Read the current terminal geometry**

Immediately before the restore watcher and tmux launch, ensure the existing
detector is available for direct-path launches, then read its `rows cols`
result:

```bash
if ! declare -f _detect_term_size >/dev/null 2>&1; then
  # shellcheck disable=SC1091  # Runtime library path
  source "$_WRAPPER_DIR/lib/loading.sh"
fi
read -r _tmux_rows _tmux_cols <<< "$(_detect_term_size)"
```

`_detect_term_size` already validates all methods and falls back to `24 80`.

**Step 2: Detach initial creation at the detected size**

Change the start of the tmux queue to include:

```bash
"$TMUX_CMD" new-session -d -x "$_tmux_cols" -y "$_tmux_rows" \
  -s "$SESSION_NAME" ...
```

Keep all environment flags, the initial ledger command, pane options,
bindings, hover setup, split percentages, and pane construction order intact.

**Step 3: Attach only after the AI pane is selected**

Remove `set-option exit-unattached on` from the early option block. End the
queue with:

```bash
  select-pane -R \; \
  attach-session -t "$SESSION_NAME" \; \
  set-option exit-unattached on 2>&3
```

The command queue attaches only after all panes exist. `exit-unattached` is
safe at that point and retains cleanup on client detach.

**Step 4: Run the focused wrapper tests**

Run:

```bash
go test ./test/bash -run 'TestWrapper_(BuildsDetachedSessionBeforeAttach|terminal_pane_is_45_percent|selects_ai_pane_geometrically|spare_pane_runs_tabbed_tmux|marks_ai_pane|active_pane_border)' -count=1 -v
```

Expected: PASS.

**Step 5: Commit the implementation**

```bash
git add wrapper.sh
git commit -m "fix(wrapper): reveal completed session layout"
```

### Task 3: Preserve restore and tmux lifecycle behavior

**Files:**
- Modify: `test/bash/layout_settle_test.go:180-255`
- Modify: `test/bash/layout_roundtrip_test.go`

**Step 1: Update stale restore-test terminology**

Change comments that say `new-session` itself blocks. The full launch command
now blocks at its final `attach-session`; the layout watcher must still run
while that attached command is alive.

**Step 2: Add a real-tmux lifecycle regression test**

Using an isolated tmux socket and a PTY, execute a queue that mirrors the
production lifecycle: detached session at an explicit size, both pane splits,
final right-pane selection, attachment, then `exit-unattached`. Poll tmux until
a client is attached and assert:

- exactly three panes already exist;
- the active pane is the right-hand AI pane;
- `exit-unattached` is enabled.

Send tmux's detach key sequence through the PTY, wait for the client command to
exit, and assert the isolated server/session exits too. Skip only when tmux is
unavailable.

**Step 3: Run restore and lifecycle tests**

Run:

```bash
go test ./test/bash -run 'Test(LayoutRoundtrip|WrapperRestore|DetachedSession)' -count=1 -v
```

Expected: PASS.

**Step 4: Commit the regression coverage**

```bash
git add test/bash/layout_settle_test.go test/bash/layout_roundtrip_test.go
git commit -m "test(tmux): cover detached session lifecycle"
```

### Task 4: Verify and install

**Files:**
- Verify: `wrapper.sh`
- Verify: `test/bash/*.go`
- Verify: `bin/wisp-deck-tui`
- Verify: `~/.local/bin/wisp-deck-tui`

**Step 1: Run shell validation**

```bash
bash -n wrapper.sh
shellcheck wrapper.sh
```

Expected: both exit 0.

**Step 2: Run the full test suite**

```bash
./run-tests.sh
```

Expected: PASS.

**Step 3: Update the local installation**

```bash
make install
```

Expected: successful local installation.

**Step 4: Verify the installed binary**

```bash
test "$(command -v wisp-deck-tui)" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$HOME/.local/bin/wisp-deck-tui" | awk '{print $1}')"
codesign --verify --verbose=2 "$HOME/.local/bin/wisp-deck-tui"
```

Expected: path and hash assertions succeed; code signature is valid.

**Step 5: Inspect final state**

```bash
git status --short
git log -5 --oneline
```

Expected: no unintended changes. Existing Wisp Deck sessions must be relaunched
to use the updated wrapper and binary.
