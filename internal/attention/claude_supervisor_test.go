package attention

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestClaudeSupervisor_inherits_stdio_and_preserves_exit_code(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var built *exec.Cmd
	exitSeen := ClaudeExitResult{ExitCode: -1}
	s := ClaudeSupervisor{
		Stdin:  strings.NewReader("hello\n"),
		Stdout: &stdout,
		Stderr: &stderr,
		CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			built = exec.CommandContext(ctx, name, args...)
			return built
		},
		OnExit: func(result ClaudeExitResult) error {
			exitSeen = result
			return nil
		},
	}

	result, err := s.Run(context.Background(), []string{
		"bash", "-c", `IFS= read -r line; printf 'out:%s\n' "$line"; printf 'err:%s\n' "$line" >&2; exit 7`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 7 || result.Signaled {
		t.Fatalf("result = %+v, want ordinary exit 7", result)
	}
	if exitSeen != result {
		t.Fatalf("OnExit = %+v, want %+v", exitSeen, result)
	}
	if stdout.String() != "out:hello\n" || stderr.String() != "err:hello\n" {
		t.Fatalf("stdio not inherited: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if built == nil {
		t.Fatal("command factory was not called")
	}
	if built.SysProcAttr != nil {
		t.Fatalf("supervisor allocated process-group attributes: %+v", built.SysProcAttr)
	}
}

func TestClaudeSupervisor_polls_with_supervised_root_pid(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	var roots []int
	s := ClaudeSupervisor{
		PollInterval: 5 * time.Millisecond,
		Poll: func(_ context.Context, rootPID int) error {
			mu.Lock()
			roots = append(roots, rootPID)
			mu.Unlock()
			return nil
		},
	}
	result, err := s.Run(ctx, []string{"bash", "-c", "sleep 0.08"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(roots) == 0 {
		t.Fatal("poll callback was never called")
	}
	for _, pid := range roots {
		if pid <= 0 || pid != roots[0] {
			t.Fatalf("poll roots = %v, want one stable positive child PID", roots)
		}
	}
}

func TestClaudeDescendantsDeepFirst(t *testing.T) {
	processes := []SupervisorProcess{
		{PID: 10, PPID: 1},
		{PID: 20, PPID: 10},
		{PID: 30, PPID: 20},
		{PID: 40, PPID: 10},
		{PID: 50, PPID: 999},
		{PID: 60, PPID: 30},
	}
	got := claudeDescendantsDeepFirst(processes, 10)
	want := []int{60, 30, 20, 40, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deep-first descendants = %v, want %v", got, want)
	}
}

func TestClaudeSupervisor_forwards_signal_deepest_first(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var childPID int
	var sent []string
	var mu sync.Mutex
	var once sync.Once
	s := ClaudeSupervisor{
		PollInterval: 5 * time.Millisecond,
		GracePeriod:  50 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, rootPID int) error {
			childPID = rootPID
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			return []SupervisorProcess{
				{PID: childPID, PPID: 1, Start: "root"},
				{PID: childPID + 1, PPID: childPID, Start: "child-1"},
				{PID: childPID + 2, PPID: childPID + 1, Start: "child-2"},
				{PID: childPID + 3, PPID: 999, Start: "unrelated"},
			}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			mu.Lock()
			sent = append(sent, fmt.Sprintf("%d:%s", pid-childPID, sig))
			mu.Unlock()
			if pid == childPID {
				return syscall.Kill(pid, sig)
			}
			return nil
		},
	}

	result, err := s.Run(context.Background(), []string{"sleep", "10"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGTERM {
		t.Fatalf("result = %+v, want SIGTERM exit", result)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"2:terminated", "1:terminated", "0:terminated",
		"2:killed", "1:killed",
	}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want %v", sent, want)
	}
}

func TestClaudeSupervisor_kills_deepest_first_after_grace(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var childPID int
	var sent []string
	var once sync.Once
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, rootPID int) error {
			childPID = rootPID
			once.Do(func() { signalCh <- syscall.SIGINT })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			return []SupervisorProcess{
				{PID: childPID, PPID: 1, Start: "root"},
				{PID: childPID + 1, PPID: childPID, Start: "child"},
			}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-childPID, sig))
			if pid == childPID && sig == syscall.SIGKILL {
				return syscall.Kill(pid, sig)
			}
			return nil
		},
	}
	result, err := s.Run(context.Background(), []string{"sleep", "10"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want forced SIGKILL", result)
	}
	want := []string{"1:interrupt", "0:interrupt", "1:killed", "0:killed"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want %v", sent, want)
	}
}

func TestClaudeSupervisor_snapshot_failure_still_kills_owned_root(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var rootPID int
	var once sync.Once
	var snapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			snapshots++
			if snapshots > 1 {
				return nil, errors.New("ps unavailable during grace")
			}
			return []SupervisorProcess{{PID: rootPID, PPID: 1, Start: "owned-root"}}, nil
		},
		SignalProcess: func(int, syscall.Signal) error {
			// The injected tree signal deliberately has no effect. The lifecycle-
			// safe os.Process handle must be the final root fallback.
			return nil
		},
	}
	started := time.Now()
	result, err := s.Run(context.Background(), []string{"sleep", "1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("snapshot failure left owned root alive for %v", elapsed)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want owned root killed through process handle", result)
	}
}

func TestClaudeSupervisor_kills_captured_descendants_after_root_exits(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var rootPID int
	var sent []string
	var once sync.Once
	rootGone := false
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			if rootGone {
				return []SupervisorProcess{{PID: rootPID + 1, PPID: 1, Start: "child"}}, nil
			}
			return []SupervisorProcess{
				{PID: rootPID, PPID: 1, Start: "root"},
				{PID: rootPID + 1, PPID: rootPID, Start: "child"},
			}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-rootPID, sig))
			if pid == rootPID && sig == syscall.SIGTERM {
				err := syscall.Kill(pid, sig)
				rootGone = true
				return err
			}
			return nil
		},
	}
	result, err := s.Run(context.Background(), []string{"sleep", "10"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGTERM {
		t.Fatalf("result = %+v, want external SIGTERM", result)
	}
	want := []string{"1:terminated", "0:terminated", "1:killed"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want retained descendant cleanup %v", sent, want)
	}
}

func TestClaudeSupervisor_revalidates_process_identity_before_kill(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var rootPID int
	var sent []string
	var once sync.Once
	var snapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			snapshots++
			childStart := "child-original"
			childParent := rootPID
			if snapshots > 1 {
				childStart = "child-reused"
				childParent = 999
			}
			return []SupervisorProcess{
				{PID: rootPID, PPID: 1, Start: "root-original"},
				{PID: rootPID + 1, PPID: childParent, Start: childStart},
			}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-rootPID, sig))
			if pid == rootPID && sig == syscall.SIGKILL {
				return syscall.Kill(pid, sig)
			}
			return nil
		},
	}
	result, err := s.Run(context.Background(), []string{"sleep", "10"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want forced SIGKILL", result)
	}
	want := []string{"1:terminated", "0:terminated", "0:killed"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want reused child PID excluded: %v", sent, want)
	}
}

func TestClaudeSupervisor_does_not_follow_reused_root_tree(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var rootPID int
	var sent []string
	var once sync.Once
	rootGone := false
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			if rootGone {
				return []SupervisorProcess{
					{PID: rootPID, PPID: 999, Start: "reused-root"},
					{PID: rootPID + 1, PPID: rootPID, Start: "unrelated-child"},
				}, nil
			}
			return []SupervisorProcess{{PID: rootPID, PPID: 1, Start: "owned-root"}}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-rootPID, sig))
			if pid == rootPID && sig == syscall.SIGTERM {
				err := syscall.Kill(pid, sig)
				rootGone = true
				return err
			}
			return nil
		},
	}
	result, err := s.Run(context.Background(), []string{"sleep", "10"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGTERM {
		t.Fatalf("result = %+v, want external SIGTERM", result)
	}
	want := []string{"0:terminated"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want reused root tree untouched %v", sent, want)
	}
}

func TestClaudeSupervisor_kills_descendants_spawned_during_grace(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var rootPID int
	var sent []string
	var once sync.Once
	var snapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			snapshots++
			processes := []SupervisorProcess{{PID: rootPID, PPID: 1, Start: "root"}}
			if snapshots > 1 {
				processes = append(processes, SupervisorProcess{
					PID: rootPID + 1, PPID: rootPID, Start: "late-child",
				})
			}
			return processes, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-rootPID, sig))
			if pid == rootPID && sig == syscall.SIGKILL {
				return syscall.Kill(pid, sig)
			}
			return nil
		},
	}
	result, err := s.Run(context.Background(), []string{"sleep", "10"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want forced SIGKILL", result)
	}
	want := []string{"0:terminated", "1:killed", "0:killed"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want late descendant cleanup %v", sent, want)
	}
}

func TestClaudeSupervisor_context_cancel_cleans_descendants(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	readyFile := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  20 * time.Millisecond,
		Poll: func(context.Context, int) error {
			if _, err := os.Stat(readyFile); err == nil {
				once.Do(func() {
					cancel()
					// Give an exec.CommandContext cancellation goroutine enough
					// time to reap/reparent the root before Run can snapshot it.
					time.Sleep(50 * time.Millisecond)
				})
			}
			return nil
		},
	}
	result, err := s.Run(ctx, []string{
		"bash", "-c",
		`trap '' TERM; sleep 10 & child=$!; printf '%s\n' "$child" > "$1"; : > "$2"; wait "$child"`,
		"claude-supervisor-test", pidFile, readyFile,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled {
		t.Fatalf("result = %+v, want controlled cancellation", result)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child PID: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID = %q, error = %v", data, err)
	}
	defer func() { _ = syscall.Kill(childPID, syscall.SIGKILL) }()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		err = syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant PID %d survived context cancellation", childPID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClaudeSupervisor_marks_trapped_forwarded_signal_as_external(t *testing.T) {
	cmd := exec.Command("bash", "-c", "exit 42")
	waitErr := cmd.Run()
	if waitErr == nil {
		t.Fatal("fixture unexpectedly exited zero")
	}
	var observed ClaudeExitResult
	s := ClaudeSupervisor{
		OnExit: func(result ClaudeExitResult) error {
			observed = result
			return nil
		},
	}
	result, err := s.finishExternal(waitErr, syscall.SIGTERM)
	if err != nil {
		t.Fatalf("finishExternal: %v", err)
	}
	if result.ExitCode != 42 || !result.Signaled || result.Signal != syscall.SIGTERM {
		t.Fatalf("result = %+v, want exit 42 marked as externally signalled by SIGTERM", result)
	}
	if observed != result {
		t.Fatalf("OnExit = %+v, want externally signalled result %+v", observed, result)
	}
}

func TestClaudeSupervisor_rejects_empty_command(t *testing.T) {
	_, err := (&ClaudeSupervisor{}).Run(context.Background(), nil)
	if err == nil {
		t.Fatal("empty command accepted")
	}
}
