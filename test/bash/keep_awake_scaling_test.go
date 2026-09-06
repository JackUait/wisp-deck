package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every live session reaps the WHOLE holder directory twice a second, so a
// per-holder subprocess makes the machine-wide cost quadratic in sessions: on a
// 17-session deck the reap alone was 24 of the watcher tick's 52 forks. The
// reap must cost the same number of processes whether one session is open or
// fifty.
func TestKeepAwakeReap_spawn_count_does_not_grow_with_holders(t *testing.T) {
	countFor := func(t *testing.T, holders int) int {
		t.Helper()
		dir := t.TempDir()
		psLog := filepath.Join(dir, "ps.log")
		// A live PID keeps every holder, so the reap walks all of them.
		binDir := mockCommand(t, dir, "ps", `echo "ps $*" >> "$KA_PS_LOG"; exit 0`)
		env := buildEnv(t, []string{binDir}, "KA_PS_LOG="+psLog)

		holderDir := filepath.Join(dir, "config", "keep-awake.d")
		if err := os.MkdirAll(holderDir, 0o700); err != nil {
			t.Fatalf("create holder directory: %v", err)
		}
		for i := 0; i < holders; i++ {
			name := filepath.Join(holderDir, fmt.Sprintf("sess-%d", i))
			if err := os.WriteFile(name, []byte(fmt.Sprintf("%d\n", 1000+i)), 0o600); err != nil {
				t.Fatalf("write holder: %v", err)
			}
		}

		runBashFunc(t, "lib/keep-awake.sh", "keep_awake_reap",
			[]string{filepath.Join(dir, "config")}, env)

		raw, err := os.ReadFile(psLog)
		if err != nil {
			if os.IsNotExist(err) {
				return 0
			}
			t.Fatalf("read ps log: %v", err)
		}
		return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
	}

	one := countFor(t, 1)
	many := countFor(t, 20)
	if many > one {
		t.Errorf("reap spawned %d processes for 20 holders but %d for 1; the cost must not grow with the number of sessions", many, one)
	}
	if many > 1 {
		t.Errorf("reap spawned %d processes for 20 holders; one batched liveness probe is enough", many)
	}
}

// Every holder file was read through `tr`, one process each. The PID is a
// single short line, which a shell builtin reads for free.
func TestKeepAwakeReap_reads_holder_pids_without_a_subprocess(t *testing.T) {
	dir := t.TempDir()
	trLog := filepath.Join(dir, "tr.log")
	binDir := mockCommand(t, dir, "tr", `echo "tr $*" >> "$KA_TR_LOG"; exec /usr/bin/tr "$@"`)
	mockCommand(t, dir, "ps", `exit 0`)
	env := buildEnv(t, []string{binDir}, "KA_TR_LOG="+trLog)

	holderDir := filepath.Join(dir, "config", "keep-awake.d")
	if err := os.MkdirAll(holderDir, 0o700); err != nil {
		t.Fatalf("create holder directory: %v", err)
	}
	for i := 0; i < 5; i++ {
		name := filepath.Join(holderDir, fmt.Sprintf("sess-%d", i))
		if err := os.WriteFile(name, []byte(fmt.Sprintf("%d\n", 2000+i)), 0o600); err != nil {
			t.Fatalf("write holder: %v", err)
		}
	}

	runBashFunc(t, "lib/keep-awake.sh", "keep_awake_reap",
		[]string{filepath.Join(dir, "config")}, env)

	if raw, err := os.ReadFile(trLog); err == nil && len(strings.TrimSpace(string(raw))) != 0 {
		t.Errorf("reap shelled out to tr to read holder PIDs:\n%s", raw)
	}
}
