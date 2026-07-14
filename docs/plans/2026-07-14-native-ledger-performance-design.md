# Native Ledger Performance Design

## Problem

The changes ledger in `lib/compact-view.sh` still performs work proportional to
the complete file list on interactive paths. The July 14 fix removed an O(N²)
checkbox pass and per-file subshells, but a hover repaint still copies and scans
the whole rendered body in `apply_checkboxes`, `highlight_body_line`, and
`viewport_slice`. Measurements in zsh show the combined path growing from about
18 ms at 100 rows to 157 ms at 1,000 rows and 649 ms at 3,000 rows.

The shell loop also rebuilds the Git snapshot synchronously every two seconds.
Opening a file performs click-to-path lookup, backdrop capture, popup setup, and
diff loading in sequence. These costs make the interface less responsive as the
changeset or opened file grows.

The target is a high-end file list whose hover, scrolling, selection, and file
opening stay responsive with at least 10,000 changed files. Interaction cost
must depend on the visible viewport, not the total number of files.

## Chosen Architecture

Implement the ledger as a native `wisp-deck-tui ledger` command. Keep
`lib/compact-view.sh` as a compatibility and integration launcher while moving
the interactive state machine, indexed row model, Git snapshot loading, and
viewport rendering into Go.

This is preferred over further shell optimization because the shell model stores
the list as newline-delimited strings. Even a carefully reordered shell pipeline
continues to copy strings, scan to an offset, and coordinate state through
subshells. A hybrid Go snapshot helper would improve refresh cost but leave two
state machines and an IPC boundary on every update. A single native model gives
bounded interaction work and one source of truth.

## Performance Contract

- Hover and click lookup are O(1).
- A visible redraw is O(viewport height), never O(total rows).
- Scrolling changes an integer viewport offset and renders only visible rows.
- Mouse reports that resolve to the existing hover target cause no state change
  and no redraw.
- Git refresh, untracked-file inspection, and diff preparation never run on the
  input/update loop.
- A slow or failed refresh leaves the previous snapshot interactive.
- Stale refresh results are discarded by generation number or cancellation.
- The hard benchmark fixture contains 10,000 rows; 100,000-row in-memory
  benchmarks guard asymptotic behavior.

## Components

### Ledger model

Add a focused internal package for the ledger model. A snapshot contains an
indexed slice of display rows, path lookup, group metadata, totals, branch state,
and a monotonically increasing generation. File rows have a stable identity made
from their status group and current path. Headers and spacer rows have explicit
row kinds rather than empty sentinel strings.

The interactive model owns only small mutable state: dimensions, viewport
offset, hovered row identity, selected-path set, discard confirmation state,
current snapshot, refresh status, and popup/account-switch intent. Selection,
hover, and the top visible file are reconciled by identity when a new snapshot
arrives.

The view function computes `[start:end]` once and renders that slice. It adds
checkbox and hover styling while rendering each visible row; there is no
intermediate complete-body string. Header and bottom-bar rows are rendered from
snapshot metadata and model state.

### Asynchronous Git source

A snapshot loader runs outside Bubble Tea's update loop. Staged, unstaged,
untracked, branch, and upstream queries may run concurrently where Git locking
semantics allow it. Machine-readable NUL-delimited output is used so spaces,
tabs, newlines, renames, and unusual path bytes cannot corrupt row identity.

Untracked line and image-size inspection uses bounded concurrency. Refreshes are
coalesced: if another tick arrives while a load is active, cancel or supersede
the older generation instead of accumulating work. The event loop continues to
serve the previous immutable snapshot while loading.

### Input and rendering

The command uses Bubble Tea's alternate-screen and mouse support already present
in the Go TUI. Mouse coordinates map directly to the viewport slice. Same-row
motion is ignored, and the renderer coalesces rapid state changes into terminal
frames. Wheel, arrow, `j`/`k`, page, `g`/`G`, selection, and discard operations
preserve their existing behavior.

Rendering always writes complete terminal rows and reserves the measured bottom
bar height, preserving the current no-blink and wrapped-bar invariants. Only
explicitly changed styles are emitted; no cosmetic animation is added to the
high-frequency file-list path.

### File opening

Clicking a file resolves its row and path directly from the visible slice. Popup
startup must not wait for a full-file diff to be read. The diff-view command will
enter its UI immediately and load/parse stdin asynchronously, showing its normal
chrome and a quiet loading state until content is ready.

Backdrop capture is removed from the click-critical path. A bounded, refreshable
backdrop cache is prepared while the ledger is idle and reused for the next
popup. If no valid backdrop is ready, the popup opens without delaying for one.

After a short stable hover dwell, the ledger may start one cancellable diff
prefetch for the hovered file. Prefetch entries are keyed by path plus a Git and
worktree fingerprint, stored in a small LRU, invalidated by a new snapshot, and
deleted on exit. Clicking without a valid prefetched entry still opens the popup
immediately and uses asynchronous loading; prefetch is an optimization, never a
correctness dependency.

Image previews retain their existing path, status, kitty-graphics, and Preview
integration. Binary/image metadata is computed during snapshot preparation, not
hover or click lookup.

### Compatibility boundary

`compact_view` remains the wrapper-facing entry point. When the installed Go
binary supports `ledger`, it executes the native command with the project path
and current session context. The shell implementation remains temporarily
available as a fallback during migration and can be removed only after feature
parity and release compatibility are proven.

The native command preserves staged, modified, and new groups; line and image
size deltas; pinned totals; scroll position; hover checkbox; multi-select and
discard confirmation; branch/upstream status; plan text; account/agent pill;
diff popup; image preview; keyboard controls; mouse hit regions; resize behavior;
and terminal cleanup.

## Data Flow

1. The launcher starts `wisp-deck-tui ledger` with project and session context.
2. The model displays an initial loading frame and schedules snapshot generation
   1 without blocking input.
3. The loader gathers Git data and returns an immutable snapshot message.
4. The model accepts only the latest generation, reconciles stable identities,
   clamps the viewport, and renders visible rows.
5. Mouse and keyboard events mutate O(1) state. View construction visits only
   pinned rows, visible file rows, and the bottom bar.
6. Refresh ticks schedule or supersede background loads while the current
   snapshot remains interactive.
7. A file activation launches the existing whole-window popup immediately. Its
   content arrives from a valid prefetch or an asynchronous diff read.

## Error Handling

- Git refresh errors retain the last good snapshot and display a compact status
  message; a later tick retries automatically.
- An initial load failure shows an actionable empty state without exiting the UI.
- Stale snapshot, prefetch, or popup preparation results are ignored.
- Missing/deleted paths are revalidated before open or discard and trigger a
  refresh instead of acting on a different row.
- Discard failures leave selection intact and surface the failing path.
- Temporary cache files are scoped to the process and removed on normal exit and
  cancellation; stale files are safe to ignore and can be pruned at startup.
- Terminal mode and mouse reporting are restored on all exit and signal paths.

## Testing and Evidence

### Model tests

- Mouse-to-row mapping at the top, middle, and bottom of a 10,000-row snapshot.
- Same-row motion produces no model change or render request.
- Scroll, page, top, and bottom operations clamp in O(1) model work.
- Snapshot replacement preserves selection, hover, and the visible anchor by
  stable identity and drops paths that disappeared.
- Wrapped headers and bottom bars preserve exact hit mapping.

### Source tests

- NUL-safe parsing for spaces, tabs, newlines, renames, deletions, and binary
  paths.
- Refresh generation and cancellation prevent stale results from replacing a
  newer snapshot.
- Slow loaders do not prevent input messages from being processed.
- Untracked inspection concurrency is bounded.

### Popup tests

- File activation is direct lookup and launches before diff input completes.
- Async diff loading transitions from loading to content without blocking input.
- Backdrop cache miss never delays popup creation.
- Prefetch cancellation, fingerprint invalidation, bounded capacity, and cleanup.

### Performance tests

- Benchmarks for hover, scrolling, visible rendering, and snapshot reconciliation
  at 1,000, 10,000, and 100,000 rows with allocation reporting.
- Structural tests assert that interactive functions receive a viewport slice or
  row ID rather than the complete snapshot.
- An end-to-end PTY latency test drives a real 10,000-row model and verifies that
  hover and scroll frames arrive within a conservative single-frame budget under
  CI load.
- Existing compact-view, mouse, discard, image-preview, popup, resize, wrapping,
  and blink regression suites remain green.

## Rollout

Build the native model behind the existing launcher boundary. Establish pure
model performance first, then Git snapshots, rendering/input parity, popup
startup, and finally wrapper activation. Keep the shell fallback until the full
PTY parity suite runs against the native command. No new branch or worktree is
used; all work stays on the repository's existing `main` branch.
