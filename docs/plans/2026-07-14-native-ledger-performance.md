# Native Ledger Performance Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace total-list-bound shell interaction paths with a native, viewport-bounded ledger that stays responsive with 10,000 or more changed files.

**Architecture:** Add a pure indexed ledger core under `internal/ledger`, adapt it to Bubble Tea in `internal/tui`, and expose it as `wisp-deck-tui ledger`. Git snapshots and popup preparation run asynchronously while the immutable last-good snapshot remains interactive; `compact_view` becomes a feature-detecting launcher with the current shell implementation as a temporary fallback.

**Tech Stack:** Go 1.25, Bubble Tea, Lip Gloss, Cobra, Git CLI with NUL-delimited output, existing Go/bash/PTTY test harnesses.

**Execution constraint:** Work directly on the existing `main` branch. Do not create a branch, worktree, detached checkout, or subagent workflow.

---

### Task 1: Indexed immutable snapshot

**Files:**
- Create: `internal/ledger/snapshot.go`
- Create: `internal/ledger/snapshot_test.go`

**Step 1: Write the failing snapshot tests**

Create tests that establish explicit row kinds, stable file identity, direct path
lookup, and viewport slicing without copying the complete list:

```go
package ledger

import "testing"

func testSnapshot(n int) Snapshot {
	rows := []Row{{Kind: RowGroup, Label: "modified"}}
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("src/file-%05d.go", i)
		rows = append(rows, Row{
			Kind: RowFile,
			ID: RowID{Group: GroupModified, Path: path},
			Path: path,
			Added: 12,
			Deleted: 3,
		})
	}
	return NewSnapshot(1, rows, Metadata{})
}

func TestSnapshotIndexesFilesByStableID(t *testing.T) {
	s := testSnapshot(10_000)
	id := RowID{Group: GroupModified, Path: "src/file-09999.go"}
	idx, ok := s.Index(id)
	if !ok || idx != 10_000 {
		t.Fatalf("Index(%v) = %d, %v; want 10000, true", id, idx, ok)
	}
}

func TestSnapshotVisibleRowsSharesBackingStorage(t *testing.T) {
	s := testSnapshot(10_000)
	got := s.VisibleRows(9_990, 20)
	if len(got) != 11 {
		t.Fatalf("visible rows = %d, want 11", len(got))
	}
	if &got[0] != &s.Rows[9_990] {
		t.Fatal("VisibleRows copied rows instead of slicing the snapshot")
	}
}
```

Add imports for `fmt` and any helper assertions used.

**Step 2: Run the tests and verify RED**

Run: `go test ./internal/ledger -run '^TestSnapshot' -count=1`

Expected: FAIL because `Snapshot`, `Row`, `RowID`, row kinds, and group kinds do
not exist.

**Step 3: Implement the minimal immutable snapshot**

Define:

```go
type Group uint8
const (
	GroupNone Group = iota
	GroupStaged
	GroupModified
	GroupNew
)

type RowKind uint8
const (
	RowGroup RowKind = iota
	RowFile
	RowSpacer
)

type RowID struct {
	Group Group
	Path  string
}

type Row struct {
	Kind RowKind
	ID RowID
	Path string
	Label string
	Added int
	Deleted int
	Binary bool
	OldBytes int64
	NewBytes int64
}

type Metadata struct {
	Branch string
	Ahead int
	Behind int
	Plan string
	TotalFiles int
	Added int
	Deleted int
}

type Snapshot struct {
	Generation uint64
	Rows []Row
	Metadata Metadata
	index map[RowID]int
}
```

`NewSnapshot` builds the index once. `Index` is a map lookup. `VisibleRows`
clamps its bounds and returns `s.Rows[start:end]` directly.

**Step 4: Verify GREEN**

Run: `go test ./internal/ledger -run '^TestSnapshot' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ledger/snapshot.go internal/ledger/snapshot_test.go
git commit -m "feat(ledger): add indexed snapshots"
```

### Task 2: Constant-time interaction state

**Files:**
- Create: `internal/ledger/state.go`
- Create: `internal/ledger/state_test.go`
- Create: `internal/ledger/state_bench_test.go`

**Step 1: Write failing state tests**

Cover direct hover mapping, same-row no-op, O(1) scroll changes, selection by
path, and stable-identity reconciliation:

```go
func TestStateHoverMapsViewportRowDirectly(t *testing.T) {
	s := testSnapshot(10_000)
	st := NewState(s)
	st.Resize(80, 24, 2, 1)
	st.ScrollTo(9_000)
	changed := st.HoverScreenRow(12)
	want := RowID{Group: GroupModified, Path: "src/file-09009.go"}
	if !changed || st.Hovered != want {
		t.Fatalf("hover = %v, changed=%v; want %v, true", st.Hovered, changed, want)
	}
	if st.HoverScreenRow(12) {
		t.Fatal("same-row hover must be a no-op")
	}
}

func TestStateReconcilePreservesVisibleAnchor(t *testing.T) {
	old := testSnapshot(10_000)
	st := NewState(old)
	st.ScrollTo(8_000)
	anchor := old.Rows[8_000].ID
	next := snapshotWithInsertedFile(old, 20)
	st.ReplaceSnapshot(next)
	if st.Snapshot.Rows[st.Scroll].ID != anchor {
		t.Fatal("refresh moved the visible anchor")
	}
}
```

Add tests for missing hover/selection removal, page/top/bottom clamping, and
non-file rows never becoming hovered.

**Step 2: Verify RED**

Run: `go test ./internal/ledger -run '^TestState' -count=1`

Expected: FAIL because `State` and its methods do not exist.

**Step 3: Implement the state model**

`State` owns `Snapshot`, dimensions, header/footer heights, `Scroll`, `Hovered`,
`Selected map[string]struct{}`, discard state, and last error. Mouse mapping is:

```go
viewportRow := screenRow - headerHeight
index := scroll + viewportRow
row := snapshot.Rows[index]
```

Use `Snapshot.Index` only when reconciling identities after snapshot replacement;
do not scan `Rows` from interaction methods.

**Step 4: Add scale benchmarks**

Benchmark the same hover/scroll loop against 1,000, 10,000, and 100,000 rows:

```go
func BenchmarkStateHover100K(b *testing.B) {
	st := NewState(testSnapshot(100_000))
	st.Resize(100, 40, 2, 1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		st.HoverScreenRow(2 + i%37)
	}
}
```

Target: zero allocations per hover after initialization and no dependency on
the snapshot length.

**Step 5: Verify GREEN and benchmark**

Run: `go test ./internal/ledger -run '^TestState' -count=1`

Run: `go test ./internal/ledger -run '^$' -bench 'BenchmarkStateHover' -benchmem -count=3`

Expected: tests PASS; hover benchmark reports 0 allocs/op.

**Step 6: Commit**

```bash
git add internal/ledger/state.go internal/ledger/state_test.go internal/ledger/state_bench_test.go
git commit -m "perf(ledger): bound interaction work"
```

### Task 3: NUL-safe Git record parsing

**Files:**
- Create: `internal/ledger/gitparse.go`
- Create: `internal/ledger/gitparse_test.go`

**Step 1: Capture Git's authoritative formats in failing tests**

Use a temporary repository to generate `git diff --numstat -z` for ordinary,
renamed, deleted, and binary files. Also unit-test `git ls-files -z` paths with
spaces, tabs, and newlines. Assert parsed records contain the current path and,
for renames, the old path separately.

```go
func TestParseNumstatZRenameUsesCurrentPath(t *testing.T) {
	raw := []byte("4\t2\t\x00old name.go\x00new name.go\x00")
	got, err := parseNumstatZ(raw, GroupStaged)
	if err != nil { t.Fatal(err) }
	if got[0].OldPath != "old name.go" || got[0].Path != "new name.go" {
		t.Fatalf("rename = %#v", got[0])
	}
}
```

**Step 2: Verify RED**

Run: `go test ./internal/ledger -run '^(TestParse|TestGitNumstatFormat)' -count=1`

Expected: FAIL because parser functions and `Change` do not exist.

**Step 3: Implement strict parsers**

Add `Change` with group, current path, old path, added/deleted, and binary
fields. Parse byte slices by NUL boundaries and tabs only in the numstat header;
never use `bufio.Scanner`'s default token limit or line splitting for paths.
Return descriptive errors for truncated records instead of silently shifting
subsequent paths.

**Step 4: Verify GREEN**

Run: `go test ./internal/ledger -run '^(TestParse|TestGitNumstatFormat)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ledger/gitparse.go internal/ledger/gitparse_test.go
git commit -m "feat(ledger): parse git records safely"
```

### Task 4: Cancellable asynchronous snapshot loader

**Files:**
- Create: `internal/ledger/source.go`
- Create: `internal/ledger/source_test.go`
- Create: `internal/ledger/source_integration_test.go`

**Step 1: Write failing loader tests**

Define a command-runner seam and test:

- staged, unstaged, untracked, branch, and upstream commands are assembled into
  one snapshot;
- a cancelled context terminates child commands;
- bounded untracked inspection never exceeds its worker limit;
- image sizes and text line counts match the shell ledger semantics;
- a later generation can complete without waiting for an obsolete load.

```go
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

func TestSourceCancellationStopsLoad(t *testing.T) {
	r := newBlockingRunner()
	s := NewSource(r, WithWorkers(4))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Load(ctx, repo, 7); done <- err }()
	r.WaitStarted(t)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Load error = %v, want context canceled", err)
	}
}
```

**Step 2: Verify RED**

Run: `go test ./internal/ledger -run '^TestSource' -count=1`

Expected: FAIL because `Source`, `Runner`, and options do not exist.

**Step 3: Implement the loader**

Use `exec.CommandContext` in the production runner. Launch independent Git
queries concurrently, collect them through a result channel, and return early on
context cancellation. Use `git diff --cached --numstat -z`, `git diff
--numstat -z`, `git ls-files --others --exclude-standard -z`, `git symbolic-ref
--short HEAD`, and one upstream-count command.

Inspect untracked files with a fixed worker pool. Batch image blob metadata
through `git cat-file --batch-check` instead of spawning once per image. Convert
changes to explicit group/header/file/spacer rows, totals, and metadata before
calling `NewSnapshot`.

**Step 4: Verify unit and integration tests**

Run: `go test ./internal/ledger -run '^(TestSource|TestLoadRealRepository)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ledger/source.go internal/ledger/source_test.go internal/ledger/source_integration_test.go
git commit -m "feat(ledger): load snapshots asynchronously"
```

### Task 5: Viewport-only Bubble Tea renderer

**Files:**
- Create: `internal/tui/ledger.go`
- Create: `internal/tui/ledger_test.go`
- Create: `internal/tui/ledger_bench_test.go`
- Modify: `internal/tui/theme.go`

**Step 1: Write failing renderer tests**

Construct models with 10,000 and 100,000 rows, set identical viewport geometry,
and assert rendering visits only visible rows. Provide an injected row-render
counter in tests instead of relying only on wall-clock time.

```go
func TestLedgerViewRendersOnlyViewportRows(t *testing.T) {
	m := NewLedgerModel(fakeSource{}, ledgerTestSnapshot(100_000), LedgerOptions{})
	m.width, m.height = 100, 30
	m.state.ScrollTo(90_000)
	seen := 0
	m.renderRow = func(r ledger.Row, width int, state ledger.RowVisualState) string {
		seen++
		return r.Path
	}
	_ = m.View()
	if seen > m.state.ViewportHeight() {
		t.Fatalf("rendered %d rows for viewport %d", seen, m.state.ViewportHeight())
	}
}
```

Add golden-style assertions for group headers, normal counts, binary/image size
deltas, truncation, selected/hover checkboxes, totals header, branch bar, scroll
status, empty/loading/error states, and wrapped bottom bars.

**Step 2: Verify RED**

Run: `go test ./internal/tui -run '^TestLedger' -count=1`

Expected: FAIL because `LedgerModel` does not exist.

**Step 3: Implement the renderer**

Add `LedgerModel` as a Bubble Tea model backed by `ledger.State`. `View` renders
the fixed header, exactly `Snapshot.VisibleRows(scroll, viewportHeight)`, filler
rows, and the bottom bar. Apply checkbox/hover style while each visible row is
formatted; never build or decorate an all-rows string.

Use `runewidth` for display width and the active `AIToolTheme` for chrome. Preserve
the existing ledger's semantic colors and full-row hover bar. Do not animate or
use any transition-like effect in the high-frequency path.

**Step 4: Add renderer benchmarks**

Benchmark 1,000, 10,000, and 100,000 rows with the same 40-row viewport and
`b.ReportAllocs()`. Add `TestLedgerViewScaleInvariant` using the render counter as
the deterministic asymptotic guard.

**Step 5: Verify GREEN**

Run: `go test ./internal/tui -run '^TestLedger' -count=1`

Run: `go test ./internal/tui -run '^$' -bench '^BenchmarkLedgerView' -benchmem -count=3`

Expected: tests PASS; benchmark time and allocations remain approximately flat
as total rows increase.

**Step 6: Commit**

```bash
git add internal/tui/ledger.go internal/tui/ledger_test.go internal/tui/ledger_bench_test.go internal/tui/theme.go
git commit -m "perf(ledger): virtualize native rendering"
```

### Task 6: Input, refresh, and stale-generation handling

**Files:**
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`

**Step 1: Write failing update-loop tests**

Cover:

- mouse motion maps to a direct visible row;
- repeated motion on the same row returns an unchanged model and no command;
- wheel, arrows, `j`/`k`, page, `g`/`G` preserve current behavior;
- resize recomputes viewport geometry without loading Git;
- refresh tick starts a background command while the old snapshot renders;
- generation 9 arriving after generation 10 is ignored;
- a refresh error retains generation 10 and schedules the next retry.

Use explicit message types such as `ledgerSnapshotMsg`, `ledgerLoadErrMsg`, and
`ledgerRefreshTickMsg`.

**Step 2: Verify RED**

Run: `go test ./internal/tui -run '^TestLedger(Update|Refresh|Mouse|Scroll)' -count=1`

Expected: FAIL on missing update behavior.

**Step 3: Implement update and commands**

`Init` schedules the initial load and refresh timer. Starting a new load cancels
the old load context. Snapshot/error messages carry generations. Update methods
call only O(1) state operations. Return the original model with no command when a
mouse report does not change the hovered ID.

**Step 4: Verify GREEN**

Run: `go test ./internal/tui -run '^TestLedger(Update|Refresh|Mouse|Scroll)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui/ledger.go internal/tui/ledger_test.go
git commit -m "feat(ledger): keep refresh off input loop"
```

### Task 7: Selection and discard parity

**Files:**
- Create: `internal/ledger/actions.go`
- Create: `internal/ledger/actions_test.go`
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`

**Step 1: Write failing action tests**

Test checkbox clicks, `x`, `d`, `y`, `n`, Esc cancellation, selected-file pruning,
tracked restore, staged-new behavior, untracked clean, confirmation hit spans,
failure retention, and no action for a stale/missing path.

The production mutation seam is:

```go
type Mutator interface {
	Discard(context.Context, string, []string) error
}
```

The real mutator checks each path with `git ls-files --error-unmatch --` and uses
`git restore --` for tracked files or `git clean -fq --` for untracked files,
matching `discard_worktree_file`.

**Step 2: Verify RED**

Run: `go test ./internal/ledger ./internal/tui -run '^(TestDiscard|TestLedgerSelection)' -count=1`

Expected: FAIL because native actions are missing.

**Step 3: Implement actions asynchronously**

Selection mutates the path set synchronously. Destructive Git work runs in a
Bubble Tea command and reports completion/error. Disable duplicate confirmation
while a discard is active; retain selection and show the failing path on error.

**Step 4: Verify GREEN**

Run: `go test ./internal/ledger ./internal/tui -run '^(TestDiscard|TestLedgerSelection)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ledger/actions.go internal/ledger/actions_test.go internal/tui/ledger.go internal/tui/ledger_test.go
git commit -m "feat(ledger): preserve discard workflow"
```

### Task 8: Register and run the native command

**Files:**
- Create: `cmd/wisp-deck-tui/ledger.go`
- Create: `cmd/wisp-deck-tui/ledger_cmd_test.go`
- Modify: `cmd/wisp-deck-tui/cmd_test.go`

**Step 1: Write failing command tests**

Require:

- `rootCmd.Find([]string{"ledger"})` succeeds;
- exactly one project directory argument is accepted;
- flags expose refresh interval, relaunch context, library directory, and a
  test-only deterministic snapshot input only when built under the test seam;
- `runLedger` applies theme and uses `tea.WithAltScreen()` plus
  `tea.WithMouseAllMotion()`;
- non-Git directories return a clear error.

**Step 2: Verify RED**

Run: `go test ./cmd/wisp-deck-tui -run '^TestLedgerCommand' -count=1`

Expected: FAIL because the command is not registered.

**Step 3: Implement the Cobra command**

Follow `diff_view.go` and `main_menu.go` conventions. Build the real source,
mutator, popup runner, and account-switch runner; open the TTY through
`util.TUITeaOptions`; and run the model with alternate screen and all-motion
mouse options. Default refresh interval remains two seconds.

**Step 4: Verify GREEN and build**

Run: `go test ./cmd/wisp-deck-tui -run '^TestLedgerCommand' -count=1`

Run: `go build ./cmd/wisp-deck-tui`

Expected: PASS and exit 0.

**Step 5: Commit**

```bash
git add cmd/wisp-deck-tui/ledger.go cmd/wisp-deck-tui/ledger_cmd_test.go cmd/wisp-deck-tui/cmd_test.go
git commit -m "feat(ledger): expose native command"
```

### Task 9: Immediate diff-view startup

**Files:**
- Modify: `internal/tui/diffview.go`
- Modify: `internal/tui/diffview_test.go`
- Modify: `cmd/wisp-deck-tui/diff_view.go`
- Modify: `cmd/wisp-deck-tui/cmd_test.go`

**Step 1: Write failing asynchronous-load tests**

Add `NewLoadingDiffView(title)` plus a `LoadDiff(io.Reader)` command. Assert:

- the initial model renders title/chrome and a quiet loading row before the
  reader produces data;
- keyboard and outside-click close remain responsive during loading;
- a loaded message installs the same content/status/highlighting as
  `NewDiffView`;
- read errors render a recoverable error state;
- image mode retains the current eager binary decode path until a separate image
  streaming design is justified.

Use a blocking reader to prove `Init` returns before data is available.

**Step 2: Verify RED**

Run: `go test ./internal/tui ./cmd/wisp-deck-tui -run '^(TestDiffViewAsync|TestRunDiffViewStartsBeforeEOF)' -count=1`

Expected: FAIL because diff loading is synchronous.

**Step 3: Implement asynchronous text loading**

For text diffs, construct the model before reading stdin and return a Bubble Tea
command that performs `io.ReadAll` in a goroutine managed by Tea. On
`diffLoadedMsg`, reuse one shared content-initialization helper so eager unit
tests and async runtime cannot diverge. Keep image mode unchanged because image
decoding and kitty setup require full bytes.

**Step 4: Verify GREEN**

Run: `go test ./internal/tui ./cmd/wisp-deck-tui -run '^(TestDiffViewAsync|TestRunDiffViewStartsBeforeEOF)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui/diffview.go internal/tui/diffview_test.go cmd/wisp-deck-tui/diff_view.go cmd/wisp-deck-tui/cmd_test.go
git commit -m "perf(diff): show popup before input loads"
```

### Task 10: Non-blocking popup and backdrop preparation

**Files:**
- Create: `internal/ledger/popup.go`
- Create: `internal/ledger/popup_test.go`
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`

**Step 1: Write failing popup tests**

Test direct row activation, shell-safe argument construction without a shell
string for metadata, cached backdrop use, cache-miss immediate launch, popup
completion refresh, image flags, decision-file discard, and cancellation/cleanup.

Define interfaces:

```go
type Popup interface {
	Open(context.Context, OpenRequest) (OpenResult, error)
}

type BackdropCache interface {
	Latest() (string, bool)
	Refresh(context.Context) error
	Close() error
}
```

Use a fake popup whose `Open` records the time it was called while the diff
producer remains blocked. Assert `Open` is invoked without waiting on backdrop
refresh or diff completion.

**Step 2: Verify RED**

Run: `go test ./internal/ledger ./internal/tui -run '^(TestPopup|TestLedgerOpen)' -count=1`

Expected: FAIL because native popup handling is missing.

**Step 3: Implement popup and bounded cache**

Prepare backdrop snapshots on an idle/refresh command, store only the latest
temporary file, and atomically replace it. A cache miss omits `--backdrop-file`.
Launch `tmux display-popup` in a Bubble Tea command so the update loop is never
blocked. Preserve tracked/untracked diff commands, title, theme, image status,
graphics TTY, discard decision, and cleanup behavior from `open_diff_popup`.

Do not add hover prefetch yet. First prove immediate async popup startup; add
prefetch only if the end-to-end gate identifies remaining diff-generation
latency after Task 9.

**Step 4: Verify GREEN**

Run: `go test ./internal/ledger ./internal/tui -run '^(TestPopup|TestLedgerOpen)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ledger/popup.go internal/ledger/popup_test.go internal/tui/ledger.go internal/tui/ledger_test.go
git commit -m "perf(ledger): open files off input loop"
```

### Task 11: Account pill and session-context parity

**Files:**
- Create: `internal/ledger/session.go`
- Create: `internal/ledger/session_test.go`
- Modify: `internal/tui/ledger.go`
- Modify: `internal/tui/ledger_test.go`
- Modify: `cmd/wisp-deck-tui/ledger.go`

**Step 1: Write failing parity tests**

Use relaunch-context fixtures from existing account-switch tests. Assert the
native bottom bar:

- hides the pill when switching is ineligible;
- shows the current Claude account or active non-Claude agent;
- preserves configured label/color and hover hit span;
- launches the existing `wisp-deck-tui claude-account-switch` flow on click;
- reloads the context after the popup and schedules a snapshot redraw.

**Step 2: Verify RED**

Run: `go test ./internal/ledger ./internal/tui ./cmd/wisp-deck-tui -run '^(TestLedgerAccount|TestSessionContext)' -count=1`

Expected: FAIL because native session context is missing.

**Step 3: Implement session context adapter**

Parse the existing `key=value` relaunch context without changing its format.
Reuse the Go account-switch command rather than porting account mutations into
the ledger. Keep the popup invocation asynchronous and refresh context on return.

**Step 4: Verify GREEN**

Run: `go test ./internal/ledger ./internal/tui ./cmd/wisp-deck-tui -run '^(TestLedgerAccount|TestSessionContext)' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ledger/session.go internal/ledger/session_test.go internal/tui/ledger.go internal/tui/ledger_test.go cmd/wisp-deck-tui/ledger.go
git commit -m "feat(ledger): preserve session controls"
```

### Task 12: Activate through the compatibility launcher

**Files:**
- Modify: `lib/compact-view.sh`
- Modify: `test/bash/compact_view_test.go`
- Modify: `test/bash/compact_view_perf_test.go`
- Modify: `wrapper.sh`

**Step 1: Write failing launcher tests**

Add tests proving:

- `compact_view` execs `wisp-deck-tui ledger` when the binary advertises the
  command;
- project path, tool, plan, relaunch file, lib directory, and refresh interval
  reach the native command safely;
- an older binary without `ledger` uses the current shell implementation;
- noninteractive shell test fixtures can force the fallback;
- no native launch path starts a second shell refresh loop.

**Step 2: Verify RED**

Run: `go test ./test/bash -run '^TestCompactView_(uses_native|falls_back|forwards)' -count=1`

Expected: FAIL because `compact_view` always runs the shell loop.

**Step 3: Refactor launcher and fallback**

Rename the current body to `compact_view_shell`. Add a small `compact_view`
front door that validates the repository, feature-detects `wisp-deck-tui ledger
--help`, and `exec`s the native command for interactive use. Respect a
`WISP_DECK_LEDGER_SHELL_FALLBACK=1` test/recovery override. Keep noninteractive
legacy behavior until the bash fixtures are explicitly migrated.

`wrapper.sh` continues to call `compact_view`; no duplicate command line is
introduced there.

**Step 4: Verify GREEN and shell syntax**

Run: `go test ./test/bash -run '^TestCompactView_(uses_native|falls_back|forwards)' -count=1`

Run: `bash -n lib/compact-view.sh wrapper.sh && zsh -n lib/compact-view.sh`

Expected: PASS and exit 0.

**Step 5: Commit**

```bash
git add lib/compact-view.sh test/bash/compact_view_test.go test/bash/compact_view_perf_test.go wrapper.sh
git commit -m "feat(ledger): activate native renderer"
```

### Task 13: Native PTY parity and 10k latency gate

**Files:**
- Create: `test/bash/native_ledger_pty_test.go`
- Create: `test/bash/native_ledger_perf_test.go`
- Modify: `test/bash/compact_view_pty_test.go`
- Modify: `test/bash/compact_view_perf_test.go`

**Step 1: Write the end-to-end PTY tests**

Build the real binary once per test package. Drive `wisp-deck-tui ledger` in a
PTY with deterministic 10,000-row snapshot input through the command's injected
test seam. Verify:

- first frame appears;
- hover frame appears and tracks a motion flood without crawling;
- same-row motion produces no extra frame;
- wheel and keyboard scroll never blink the highlight;
- a row near the logical bottom opens the correct path without a linear delay;
- checkbox selection/discard, resize, wrapped bar, account pill, Ctrl-C cleanup,
  and mouse-leaves-pane behavior match existing tests.

Record latency from input write to the frame containing the expected marker.
Use a conservative CI ceiling (for example 100 ms) while requiring 1k and 10k
fixtures to stay within the same order of magnitude. The deterministic viewport
render-counter tests remain the authoritative asymptotic proof; PTY timing is the
user-perceived guard.

**Step 2: Verify RED**

Run: `go test ./test/bash -run '^TestNativeLedger' -count=1`

Expected: FAIL until the native test seam and all parity behavior are connected.

**Step 3: Complete only behavior exposed by the PTY evidence**

Fix native parity defects one at a time with a failing focused test before each
production change. Do not weaken latency thresholds to hide total-list work. If
file opening still misses the gate after async popup startup, add the bounded,
fingerprinted, cancellable hover-prefetch LRU described in the design, with unit
tests for dwell, cancellation, invalidation, capacity, and cleanup.

**Step 4: Verify native PTY and performance suites**

Run: `go test ./test/bash -run '^TestNativeLedger' -count=1 -v`

Run: `go test ./internal/ledger ./internal/tui -run '^$' -bench '(Ledger|State)' -benchmem -count=5`

Expected: PTY tests PASS; 10k interaction meets the gate; in-memory interaction
and viewport render costs remain effectively flat through 100k rows.

**Step 5: Commit**

```bash
git add test/bash/native_ledger_pty_test.go test/bash/native_ledger_perf_test.go test/bash/compact_view_pty_test.go test/bash/compact_view_perf_test.go internal/ledger internal/tui/ledger.go
git commit -m "test(ledger): enforce high-scale latency"
```

### Task 14: Full verification and completion audit

**Files:**
- Modify as required by failures only; every fix needs a focused regression test.

**Step 1: Run formatting and static checks**

Run: `gofmt -w internal/ledger/*.go internal/tui/ledger*.go cmd/wisp-deck-tui/ledger*.go test/bash/native_ledger*.go`

Run: `git diff --check`

Run: `go vet ./...`

Run: `bash -n lib/compact-view.sh wrapper.sh && zsh -n lib/compact-view.sh`

Expected: all exit 0.

**Step 2: Run focused performance evidence fresh**

Run: `go test ./internal/ledger ./internal/tui -run '^$' -bench '(Ledger|State)' -benchmem -count=5`

Run: `go test ./test/bash -run '^TestNativeLedger' -count=1 -v`

Expected: benchmarks show viewport-bounded interaction through 100k rows and all
10k PTY gates pass.

**Step 3: Run the full repository suite**

Run: `go test ./... -count=1`

Run: `./run-tests.sh -count=1`

Run: `make build`

Expected: every command exits 0 with no failures.

**Step 4: Audit every approved requirement against evidence**

Re-read `docs/plans/2026-07-14-native-ledger-performance-design.md` and create a
requirement checklist covering hover, scrolling, opening, async refresh, stable
state, errors, staged/modified/new groups, image preview, discard, branch/account
bar, mouse/keyboard, resize, cleanup, 10k PTY behavior, and 100k in-memory
scaling. For each item, cite a specific test, benchmark, or runtime inspection.
Any item without direct evidence remains incomplete.

**Step 5: Commit final verification fixes if any**

Use one focused Conventional Commit per independently justified fix. Do not make
an empty verification commit.
