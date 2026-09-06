//go:build !darwin

package attention

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// systemProcessTable falls back to `ps` where the darwin kernel table is not
// available. Wisp Deck ships only for macOS; this keeps the package building
// and testable elsewhere.
func systemProcessTable() ([]SupervisorProcess, error) {
	command := exec.CommandContext(context.Background(), claudePSExecutable, "-axo", "pid=,ppid=,lstart=")
	command.Env = applyEnvironmentOverrides(os.Environ(), []string{"LC_ALL=C", "TZ=UTC"})
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("snapshot processes: %w", err)
	}
	parsed, err := parseProcessSnapshot(out)
	if err != nil {
		return nil, err
	}
	processes := make([]SupervisorProcess, 0, len(parsed))
	for pid, row := range parsed {
		processes = append(processes, SupervisorProcess{PID: pid, PPID: row.parent, StartSec: row.startSec})
	}
	return processes, nil
}
