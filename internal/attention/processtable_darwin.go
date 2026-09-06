//go:build darwin

package attention

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// systemProcessTable reads pid, parent and start time for every process
// straight from the kernel.
//
// This used to fork `ps -axo pid=,ppid=,lstart=`. The supervisor asks for the
// table every 250ms in every open session, and one fork cost 80 CPU-ms against
// 5 for this call, so a deck of 17 sessions kept ~17 `ps` processes resident at
// all times and the poll self-throttled to about 1s. The kernel read is
// measured byte-identical to the command it replaces.
//
// Nothing here may cost anything PER PROCESS. Two things did. Rendering each
// row's lstart string allocated once per process on the machine — ~3700 a call,
// a quarter of a million a second across a deck — for a value nothing prints:
// it is only ever compared against the procStart Claude Code records, which is
// parsed to the same seconds once per record. Hashing every PID into a set to
// drop a repeat cost as much again, and the kernel does not produce one:
// TestSystemProcessTable_neverReportsAPIDTwice is that assumption, and every
// consumer indexes this table by PID regardless.
func systemProcessTable() ([]SupervisorProcess, error) {
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read kernel process table: %w", err)
	}
	processes := make([]SupervisorProcess, 0, len(entries))
	for index := range entries {
		entry := &entries[index]
		pid := int(entry.Proc.P_pid)
		// The kernel task is PID 0 and `ps -ax` never lists it. Every consumer
		// here rejects a non-positive PID, so passing it on fails the poll.
		if pid <= 0 {
			continue
		}
		processes = append(processes, SupervisorProcess{
			PID:  pid,
			PPID: int(entry.Eproc.Ppid),
			// lstart has one-second resolution, so the microseconds the kernel
			// also reports are exactly what the old string dropped.
			StartSec: entry.Proc.P_starttime.Sec,
		})
	}
	return processes, nil
}
