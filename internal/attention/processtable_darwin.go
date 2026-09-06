//go:build darwin

package attention

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// lstartLayout is the format `ps -o lstart=` prints under LC_ALL=C and TZ=UTC.
// `_2` is the space-padded day, so a single-digit day carries two spaces. Claude
// Code writes procStart in this exact shape and the registry compares it
// byte-for-byte, so the padding is load-bearing.
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// systemProcessTable reads pid, parent and start time for every process
// straight from the kernel.
//
// This used to fork `ps -axo pid=,ppid=,lstart=`. The supervisor asks for the
// table twice per 250ms tick in every open session, and one fork cost 80 CPU-ms
// against 5 for this call, so a deck of 17 sessions kept ~17 `ps` processes
// resident at all times and the poll self-throttled to about 1s. The kernel
// read is measured byte-identical to the command it replaces.
func systemProcessTable() ([]SupervisorProcess, error) {
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read kernel process table: %w", err)
	}
	processes := make([]SupervisorProcess, 0, len(entries))
	seen := make(map[int]struct{}, len(entries))
	for index := range entries {
		entry := &entries[index]
		pid := int(entry.Proc.P_pid)
		// The kernel task is PID 0 and `ps -ax` never lists it. Every consumer
		// here rejects a non-positive PID, so passing it on fails the poll.
		if pid <= 0 {
			continue
		}
		if _, duplicate := seen[pid]; duplicate {
			continue
		}
		seen[pid] = struct{}{}
		started := time.Unix(
			entry.Proc.P_starttime.Sec,
			int64(entry.Proc.P_starttime.Usec)*int64(time.Microsecond),
		).UTC()
		processes = append(processes, SupervisorProcess{
			PID:   pid,
			PPID:  int(entry.Eproc.Ppid),
			Start: started.Format(lstartLayout),
		})
	}
	return processes, nil
}
