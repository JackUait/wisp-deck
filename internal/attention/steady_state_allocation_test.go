package attention

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

// syntheticProcessTable returns a launch tree of treeSize processes rooted at
// rootPID, followed by unrelated processes until the table holds total rows.
// It is the shape the poll actually meets: a handful of supervised processes
// inside a machine-wide table.
func syntheticProcessTable(rootPID, treeSize, total int) []SupervisorProcess {
	rows := make([]SupervisorProcess, 0, total)
	rows = append(rows, SupervisorProcess{PID: rootPID, PPID: 1, StartSec: 1_704_067_200})
	for i := 1; i < treeSize; i++ {
		rows = append(rows, SupervisorProcess{
			PID:      rootPID + i,
			PPID:     rootPID + i - 1,
			StartSec: int64(1_704_067_200 + i),
		})
	}
	for pid := rootPID + treeSize; len(rows) < total; pid++ {
		rows = append(rows, SupervisorProcess{
			PID:      pid,
			PPID:     1,
			StartSec: 1_704_067_200,
		})
	}
	return rows
}

func pollAllocationsForTableSize(t *testing.T, total int) float64 {
	t.Helper()
	const rootPID = 900000
	mapper := ClaudeRegistryMapper{
		ConfigDir:     t.TempDir(),
		LaunchRootPID: rootPID,
		ReadFile:      func(string) ([]byte, error) { return nil, errors.New("no record") },
		Processes:     syntheticProcessTable(rootPID, 8, total),
	}
	ctx := context.Background()
	if _, _, err := mapper.Poll(ctx); err != nil {
		t.Fatalf("poll a %d-row table: %v", total, err)
	}
	return testing.AllocsPerRun(20, func() {
		if _, _, err := mapper.Poll(ctx); err != nil {
			t.Fatalf("poll a %d-row table: %v", total, err)
		}
	})
}

// A poll supervises ONE launch tree of a handful of processes, but it is handed
// the whole machine's process table. Its cost must therefore be set by the tree
// it supervises, not by how busy the machine is: this poll runs 4 times a second
// in every open session, so per-row work is multiplied by the table size, the
// tick rate AND the number of sessions at once.
//
// The budget is deliberately generous. Indexing the table by PID is one map, so
// it costs a handful of growth allocations across thousands of rows; anything
// that allocates PER row — a formatted start time, a cycle-detection set built
// for each candidate — blows straight through it.
func TestClaudeRegistryPoll_allocatesForTheTreeNotForTheMachine(t *testing.T) {
	const (
		small = 200
		large = 4000
	)

	smallAllocs := pollAllocationsForTableSize(t, small)
	largeAllocs := pollAllocationsForTableSize(t, large)

	extraRows := float64(large - small)
	perRow := (largeAllocs - smallAllocs) / extraRows
	const budgetPerRow = 0.05

	if perRow > budgetPerRow {
		t.Errorf(
			"poll allocates %.3f times per extra process in the table, budget %.3f\n"+
				"  %d rows: %.0f allocations\n"+
				"  %d rows: %.0f allocations\n"+
				"a machine-wide table is ~3500 rows, polled 4 times a second in every session",
			perRow, budgetPerRow, small, smallAllocs, large, largeAllocs,
		)
	}
}

// The supervisor reads the whole machine's process table 4 times a second in
// every open session, and it wants three numbers from each row. Rendering a
// per-row start TIME made the read allocate once per process on the machine:
// ~3700 allocations a call, so a deck of 16 sessions produced a quarter of a
// million allocations a second to notice that nothing had changed.
//
// The start time is only ever compared for equality against the one Claude Code
// records, so it travels as a number. Nothing here may allocate per row again.
func TestSystemProcessTable_allocatesForTheTableNotForEachProcess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the kernel process table is read through a darwin sysctl")
	}

	table, err := systemProcessTable()
	if err != nil {
		t.Fatalf("read process table: %v", err)
	}
	if len(table) < 50 {
		t.Skipf("process table has only %d rows, too few to size per-row cost", len(table))
	}

	allocations := testing.AllocsPerRun(10, func() {
		if _, err := systemProcessTable(); err != nil {
			t.Fatalf("read process table: %v", err)
		}
	})

	// The slice itself, the kernel buffer and a few growth steps are the whole
	// budget; anything proportional to the row count is the defect.
	perRow := allocations / float64(len(table))
	const budgetPerRow = 0.05

	if perRow > budgetPerRow {
		t.Errorf(
			"reading the process table allocates %.3f times per process, budget %.3f\n"+
				"  %d rows produced %.0f allocations\n"+
				"this read runs 4 times a second in every open session",
			perRow, budgetPerRow, len(table), allocations,
		)
	}
}

// The table read used to build a set of every PID on the machine purely to
// drop a repeat the kernel does not produce: a PID is the kernel's own unique
// handle for a live process, and one read returns each live process once.
// Hashing ~3700 PIDs a call cost real time 64 times a second across a deck, so
// the set is gone and this is the assumption it left behind. Every consumer
// indexes the table by PID anyway; if this ever fails, they pick a winner
// rather than break, but the read should stop claiming a guarantee it dropped.
func TestSystemProcessTable_neverReportsAPIDTwice(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the kernel process table is read through a darwin sysctl")
	}

	for read := 0; read < 20; read++ {
		table, err := systemProcessTable()
		if err != nil {
			t.Fatalf("read process table: %v", err)
		}
		seen := make(map[int]struct{}, len(table))
		for _, row := range table {
			if _, repeat := seen[row.PID]; repeat {
				t.Fatalf("read %d reported pid %d twice in %d rows", read, row.PID, len(table))
			}
			seen[row.PID] = struct{}{}
		}
	}
}
