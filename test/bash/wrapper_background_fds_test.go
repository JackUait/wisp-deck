package bash_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A job the wrapper backgrounds must not keep the caller's output open after
// the wrapper exits. The wrapper dups its stderr to fd 3, so a job that drops
// only fds 1 and 2 still holds it: every caller that reads the output to EOF
// then waits for that job, which made each wrapper test wait out the heal
// watcher's full 15s poll.
func TestWrapper_background_jobs_release_the_callers_output(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// This tmux never answers display-message, so the heal watcher keeps
	// polling for its whole budget instead of exiting on its first tick.
	mocks := map[string]string{
		"tmux":          recordingTmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	seedRestoreQueue(t, home, projDir, "claude")

	cmd := exec.Command("bash", filepath.Join(projectRoot(t), "wrapper.sh"))
	cmd.Env = buildEnv(t, nil, "HOME="+home, "GT_REC="+filepath.Join(home, "rec"))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// Far above the wrapper's own teardown, far below the watcher's 15s.
	cmd.WaitDelay = 5 * time.Second

	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("a background job still held the wrapper's output after it exited")
	}
	if err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out.String())
	}
}
