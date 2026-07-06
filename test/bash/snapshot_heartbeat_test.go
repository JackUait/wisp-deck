package bash_test

// The snapshot heartbeat runs for the whole life of a session inside the
// wrapper's launch-time bash, so any function it captured at source time is
// frozen until the session closes. That staleness class caused a real bug:
// after the snapshot format gained the account field (89b9c1c), every
// already-running session's heartbeat kept writing the OLD format, so a
// reboot would still have flipped restored sessions onto the global pointer's
// login. The heartbeat must therefore re-source lib/session-restore.sh on
// EVERY tick (in a throwaway bash), so lib fixes reach live sessions within
// one interval instead of never.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeHeartbeatStubLib installs a fake lib/session-restore.sh under dir whose
// write_session_snapshot writes `marker` to the snapshot file.
func writeHeartbeatStubLib(t *testing.T, dir, marker string) {
	t.Helper()
	libDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir stub lib: %v", err)
	}
	stub := "write_session_snapshot() { printf '" + marker + "\\n' > \"$2\"; }\n"
	if err := os.WriteFile(filepath.Join(libDir, "session-restore.sh"), []byte(stub), 0o644); err != nil {
		t.Fatalf("write stub lib: %v", err)
	}
}

// pollFileContent waits until the file's trimmed content equals want.
func pollFileContent(t *testing.T, path, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestRunSnapshotHeartbeat_resources_lib_each_tick(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	writeHeartbeatStubLib(t, dir, "v1")

	// Drive the REAL run_snapshot_heartbeat, pointed at the stub wrapper dir.
	// tmux_cmd is unused by the stub; a fast interval keeps the test quick.
	cmd := exec.Command("bash", "-c",
		`source "$1" && run_snapshot_heartbeat "$2" true "$3" 0.05`,
		"heartbeat-test", filepath.Join(root, "lib/session-restore.sh"), dir, snap)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start heartbeat: %v", err)
	}
	t.Cleanup(func() {
		// Negative pid = whole process group (the loop plus its tick/sleep children).
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})

	if !pollFileContent(t, snap, "v1", 3*time.Second) {
		t.Fatalf("heartbeat never wrote the v1 snapshot (run_snapshot_heartbeat missing or broken)")
	}

	// Upgrade the lib mid-run: the very next tick must pick it up, because
	// each tick re-sources the lib in a fresh bash.
	writeHeartbeatStubLib(t, dir, "v2")
	if !pollFileContent(t, snap, "v2", 3*time.Second) {
		t.Errorf("heartbeat kept writing the stale v1 write_session_snapshot after the lib changed — ticks must re-source lib/session-restore.sh")
	}
}

func TestRunSnapshotHeartbeat_survives_broken_lib_tick(t *testing.T) {
	root := projectRoot(t)
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	writeHeartbeatStubLib(t, dir, "v1")

	cmd := exec.Command("bash", "-c",
		`source "$1" && run_snapshot_heartbeat "$2" true "$3" 0.05`,
		"heartbeat-test", filepath.Join(root, "lib/session-restore.sh"), dir, snap)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start heartbeat: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})

	if !pollFileContent(t, snap, "v1", 3*time.Second) {
		t.Fatalf("heartbeat never wrote the v1 snapshot")
	}

	// A mid-edit save can leave the lib momentarily unparseable. The loop must
	// survive the failed tick and recover once the lib is valid again.
	broken := filepath.Join(dir, "lib", "session-restore.sh")
	if err := os.WriteFile(broken, []byte("this is ( not bash\n"), 0o644); err != nil {
		t.Fatalf("write broken lib: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let a few broken ticks elapse
	writeHeartbeatStubLib(t, dir, "v3")
	if !pollFileContent(t, snap, "v3", 3*time.Second) {
		t.Errorf("heartbeat died on a broken-lib tick instead of skipping it and recovering")
	}
}

// TestWrapper_heartbeat_uses_run_snapshot_heartbeat pins the wiring: the
// wrapper must delegate to run_snapshot_heartbeat (per-tick re-source) and
// must NOT call write_session_snapshot from its own launch-time bash, where
// the function would be frozen for the session's lifetime.
func TestWrapper_heartbeat_uses_run_snapshot_heartbeat(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "run_snapshot_heartbeat ") {
		t.Errorf("wrapper.sh does not call run_snapshot_heartbeat — the heartbeat would freeze write_session_snapshot at launch-time")
	}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "write_session_snapshot ") {
			t.Errorf("wrapper.sh calls write_session_snapshot directly (%q) — it would run the launch-time copy forever; go through run_snapshot_heartbeat", trimmed)
		}
	}
}
