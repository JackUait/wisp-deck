# Discard a file from the file preview

## Goal

Give users a button to discard a file's changes directly from the **file
preview** (the `wisp-deck-tui diff-view` popup opened by clicking a row in the
compact-view change ledger). "Discard" reverts the file's working-tree changes.

## Context

- **File list** = the changeset ledger rendered by `compact_view` in
  `lib/compact-view.sh`. Clicking a file row calls `open_diff_popup`.
- **File preview** = `wisp-deck-tui diff-view` (`internal/tui/diffview.go`,
  `DiffViewModel`), a full-screen tmux popup showing one file's `git diff HEAD`.
- The preview is a **separate process**: the diff arrives on stdin, the file
  path comes via `--title`. It does not currently know the repo directory and
  performs no git mutations.

## Decisions (confirmed with user)

1. **Confirmation required** — clicking Discard arms a Yes/No confirm step before
   anything is discarded (the op is irreversible).
2. **Bash runs git** — the Go preview only signals intent; `compact-view.sh`
   runs the git command after the popup closes. Keeps git mutations in the bash
   orchestration layer, matching the existing architecture.
3. **Discard = working-tree changes** — `git restore -- <file>` (restore the
   worktree from the index: git's own "discard changes in working directory").
   A staged copy, if any, is left intact.

## UX

The preview's title row (status badge · file path · `+N −M` counts) gains a
right-anchored, red `[ Discard ]` button. It is always present — including
single-sided whole-file add/delete previews, which otherwise hide the tab row.

- **Activate**: click `[ Discard ]`, or press `d` → enter ARMED state.
- **ARMED state**: the right side of the title row replaces `[ Discard ]` with
  `[ Yes ]` `[ No ]` chips; the bottom hint bar reads `Discard changes? · y confirm · n/Esc cancel`.
  - **Confirm**: click `[ Yes ]`, or press `y`/Enter → mark discard requested,
    quit the program.
  - **Cancel**: click `[ No ]`, press `n`, or press `Esc` → return to normal
    state. (While armed, `Esc` cancels the arm; it does **not** close the popup.)

Hit-boxes are anchored to the right edge of the content area and computed from
the content width alone (independent of title length), mirroring the stable
fixed-column hit-testing already used for the view-switch tabs.

## Architecture / data flow

```
compact_view (bash)
  └─ click a file row → open_diff_popup dir file
       ├─ mktemp discard-decision file
       ├─ tmux display-popup: git diff … | wisp-deck-tui diff-view --discard-file F …
       │     └─ DiffViewModel: [ Discard ] → confirm → DiscardRequested()=true → quit
       │           runDiffView: writeDiscardDecision(F, requested)  // writes "discard"
       └─ after popup: should_discard F && discard_worktree_file dir file
             └─ git -C dir restore -- file ; need_build=1 (refresh ledger)
```

### Go: `internal/tui/diffview.go` (`DiffViewModel`)

- New field `discardArmed bool` (confirm step active) and `discardRequested bool`.
- New exported method `DiscardRequested() bool`.
- `View()`:
  - normal: render `[ Discard ]` right-anchored on the title row.
  - armed: render `[ Yes ] [ No ]` right-anchored; bottom bar shows the prompt.
- `Update()` mouse + key handling for arm / confirm / cancel, with the armed
  `Esc` branch taking precedence over the existing close-on-`Esc`.
- Helpers `discardButtonSpan(cw)` and `discardConfirmSpans(cw)` return content
  column ranges for hit-testing; the title row sits at screen row `mv+1`.

### Go: `cmd/wisp-deck-tui/diff_view.go`

- New flag `--discard-file <path>`.
- After `p.Run()`, type-assert the final model and call a pure helper
  `writeDiscardDecision(path string, requested bool) error` — writes the literal
  `discard` when requested and the path is non-empty; otherwise a no-op.

### Bash: `lib/compact-view.sh`

- `should_discard <decision_file>` → exit 0 iff the file's content is `discard`.
- `discard_worktree_file <dir> <file>` → `git -C "$dir" restore -- "$file"`.
- `open_diff_popup` creates the temp decision file, passes `--discard-file`, and
  after the popup runs `should_discard … && discard_worktree_file …`, then lets
  the loop refresh (`need_build=1`).

## Testing (TDD — tests first, watch fail, then implement)

### Go (`internal/tui/diffview_test.go`)
1. `DiscardRequested()` is false on a fresh model.
2. `View()` (sized) contains `Discard` in normal state — multi-view AND
   single-sided file.
3. Clicking the discard button → armed: `View()` shows `Yes`/`No`, model is not
   quitting, `DiscardRequested()` still false.
4. Armed + key `y` → `DiscardRequested()` true and quitting.
5. Armed + click `[ Yes ]` → `DiscardRequested()` true.
6. Armed + key `n` → back to normal (not quitting, not requested, `View()` shows
   `Discard` again).
7. Armed + `Esc` → cancels the arm (not quitting), does not request discard.
8. `Esc` when NOT armed → quits (regression guard, unchanged behavior).
9. Key `d` arms the confirm.

### Go (`cmd/wisp-deck-tui/cmd_test.go`)
10. `writeDiscardDecision(path, true)` writes `discard`; `(path, false)` writes
    nothing / leaves an empty decision; empty path is a no-op.

### Bash (`test/bash/compact_view_test.go`)
11. `should_discard` returns 0 for content `discard`, non-zero for empty/other.
12. `discard_worktree_file` reverts a modified tracked file in a temp git repo
    (modify file → run → file matches HEAD again); a staged-only change is left
    intact.

## Out of scope

- Discarding untracked files (the ledger already omits them).
- Undo of a discard.
- Any change to the compact-view ledger rendering beyond the post-discard refresh
  (already triggered by `need_build=1`).
