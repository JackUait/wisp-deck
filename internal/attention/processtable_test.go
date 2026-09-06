package attention

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// The attention loop asks for the process table 8 times a second in EVERY open
// session. Forking `ps` to answer that costs 80 CPU-ms a call and made 17
// sessions keep ~17 `ps` processes resident at all times, so the table is read
// from the kernel instead. This pins the replacement to the command it
// replaced: same PIDs, same parents, and the same start instant that Claude
// Code records as procStart — `ps -o lstart=` parsed to whole seconds, which is
// all the lstart format ever carried.
func TestSystemProcessTable_matches_the_ps_command_it_replaced(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the kernel process table is read through a darwin sysctl")
	}

	before, err := systemProcessTable()
	if err != nil {
		t.Fatalf("read process table: %v", err)
	}
	command := exec.Command(claudePSExecutable, "-axo", "pid=,ppid=,lstart=")
	command.Env = applyEnvironmentOverrides(os.Environ(), []string{"LC_ALL=C", "TZ=UTC"})
	out, err := command.Output()
	if err != nil {
		t.Fatalf("run ps: %v", err)
	}
	after, err := systemProcessTable()
	if err != nil {
		t.Fatalf("re-read process table: %v", err)
	}

	fromPS, err := parseProcessSnapshot(out)
	if err != nil {
		t.Fatalf("parse ps output: %v", err)
	}

	// A process that started or exited while ps was running would differ for
	// reasons that are not a defect, so only PIDs identical in both bracketing
	// reads are compared.
	stable := map[int]SupervisorProcess{}
	index := func(rows []SupervisorProcess) map[int]SupervisorProcess {
		m := make(map[int]SupervisorProcess, len(rows))
		for _, row := range rows {
			m[row.PID] = row
		}
		return m
	}
	beforeByPID, afterByPID := index(before), index(after)
	for pid, row := range beforeByPID {
		if other, ok := afterByPID[pid]; ok && other == row {
			stable[pid] = row
		}
	}
	if len(stable) < 50 {
		t.Fatalf("only %d stable processes; the comparison would prove nothing", len(stable))
	}

	compared, mismatched := 0, 0
	for pid, row := range stable {
		psRow, ok := fromPS[pid]
		if !ok {
			continue
		}
		compared++
		if psRow.parent != row.PPID || psRow.startSec != row.StartSec {
			mismatched++
			if mismatched <= 5 {
				t.Errorf("pid %d: ps reported parent %d start %d, kernel reported parent %d start %d",
					pid, psRow.parent, psRow.startSec, row.PPID, row.StartSec)
			}
		}
	}
	if compared < 50 {
		t.Fatalf("only %d processes could be compared", compared)
	}
	if mismatched != 0 {
		t.Errorf("%d of %d processes disagreed with ps", mismatched, compared)
	}
}

// parseProcessSnapshot rejects PID 0, and the kernel table reports the kernel
// task as PID 0 where `ps -ax` does not. Leaking it would fail every poll.
func TestSystemProcessTable_omits_the_kernel_task(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the kernel process table is read through a darwin sysctl")
	}
	rows, err := systemProcessTable()
	if err != nil {
		t.Fatalf("read process table: %v", err)
	}
	for _, row := range rows {
		if row.PID <= 0 {
			t.Fatalf("process table contains PID %d, which no consumer accepts", row.PID)
		}
	}
}

// The supervisor's own snapshot must answer from the same kernel read.
func TestClaudeSupervisorSnapshot_finds_this_test_process(t *testing.T) {
	rows, err := claudeSupervisorSnapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot processes: %v", err)
	}
	for _, row := range rows {
		if row.PID == os.Getpid() {
			if row.PPID != os.Getppid() {
				t.Fatalf("snapshot reported parent %d, want %d", row.PPID, os.Getppid())
			}
			return
		}
	}
	t.Fatal("snapshot did not contain the running test process")
}

// childCPU is the CPU time this process's children have consumed. A fork always
// costs some, and the `ps` this replaced cost ~80ms of it per call, so an
// unchanged reading is proof the poll answered without spawning anything.
func childCPU(t *testing.T) time.Duration {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage); err != nil {
		t.Fatalf("read child CPU: %v", err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// The registry poll runs 4 times a second in every open session. Forking `ps`
// to answer it is what kept ~17 `ps` processes resident on a 17-session deck.
func TestClaudeRegistryPoll_forks_no_process(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the kernel process table is read through a darwin sysctl")
	}
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "sessions"), 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	mapper := ClaudeRegistryMapper{ConfigDir: configDir, LaunchRootPID: os.Getpid()}

	// Warm anything one-off (module init, lazy opens) so it is not charged below.
	if _, _, err := mapper.Poll(context.Background()); err != nil {
		t.Fatalf("warm poll: %v", err)
	}

	before := childCPU(t)
	for i := 0; i < 5; i++ {
		if _, _, err := mapper.Poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if spent := childCPU(t) - before; spent != 0 {
		t.Fatalf("5 polls spent %v of CPU in child processes; the poll must not fork", spent)
	}
}
