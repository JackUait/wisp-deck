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
		Poll: func(_ context.Context, rootPID int, _ []SupervisorProcess) error {
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

func TestClaudeSupervisor_readyForwardedSignalWinsReadyTrappedWait(t *testing.T) {
	cmd := exec.Command("bash", "-c", `trap 'exit 42' TERM; kill -TERM $$`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start trapped-exit fixture: %v", err)
	}
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("fixture wait error = %v, want ordinary exit 42", waitErr)
	}

	waitCh := make(chan error, 1)
	waitCh <- waitErr
	signalCh := make(chan os.Signal, 1)
	signalCh <- syscall.SIGTERM
	var observed ClaudeExitResult
	var signalsSent int
	s := ClaudeSupervisor{
		GracePeriod: time.Millisecond,
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			return nil, nil
		},
		SignalProcess: func(int, syscall.Signal) error {
			signalsSent++
			return nil
		},
		OnExit: func(result ClaudeExitResult) error {
			observed = result
			return nil
		},
	}

	result, err := s.superviseStarted(context.Background(), cmd.Process, waitCh, signalCh, nil)
	if err != nil {
		t.Fatalf("superviseStarted: %v", err)
	}
	want := ClaudeExitResult{ExitCode: 42, Signaled: true, Signal: syscall.SIGTERM}
	if result != want {
		t.Fatalf("result = %+v, want ready forwarded signal to mark trapped exit %+v", result, want)
	}
	if observed != want {
		t.Fatalf("OnExit = %+v, want %+v", observed, want)
	}
	if signalsSent != 0 {
		t.Fatalf("SignalProcess calls = %d, want none after Wait reaped the child", signalsSent)
	}
}

func TestClaudeSupervisor_reconcilesForwardedSignalDeliveredJustAfterWait(t *testing.T) {
	cmd := exec.Command("bash", "-c", `trap 'exit 42' TERM; kill -TERM $$`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start trapped-exit fixture: %v", err)
	}
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("fixture wait error = %v, want ordinary exit 42", waitErr)
	}
	waitCh := make(chan error, 1)
	waitCh <- waitErr
	signalCh := make(chan os.Signal, 1)
	// The production race is signal.Notify delivery trailing child Wait by a few
	// scheduler turns. Five milliseconds is deliberately inside the supervisor's
	// bounded reconciliation window, not an estimate of process runtime.
	timer := time.AfterFunc(5*time.Millisecond, func() {
		signalCh <- syscall.SIGTERM
	})
	defer timer.Stop()
	var signalsSent int
	s := ClaudeSupervisor{
		SignalReconcile: 2 * time.Second,
		SignalProcess: func(int, syscall.Signal) error {
			signalsSent++
			return nil
		},
	}

	type superviseResult struct {
		result ClaudeExitResult
		err    error
	}
	done := make(chan superviseResult, 1)
	go func() {
		result, err := s.superviseStarted(context.Background(), cmd.Process, waitCh, signalCh, nil)
		done <- superviseResult{result: result, err: err}
	}()
	var completed superviseResult
	select {
	case completed = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("superviseStarted did not complete within the bounded reconciliation deadline")
	}
	result, err := completed.result, completed.err
	if err != nil {
		t.Fatalf("superviseStarted: %v", err)
	}
	want := ClaudeExitResult{ExitCode: 42, Signaled: true, Signal: syscall.SIGTERM}
	if result != want {
		t.Fatalf("result = %+v, want delayed forwarded signal to mark trapped exit %+v", result, want)
	}
	if signalsSent != 0 {
		t.Fatalf("SignalProcess calls = %d, want none after Wait reaped the child", signalsSent)
	}
}

func TestClaudeSupervisor_RunDefaultReconcileStressMarksDelayedTrappedExitExternal(t *testing.T) {
	const (
		attempts          = 12
		notificationDelay = 75 * time.Millisecond
	)
	dir := t.TempDir()
	for attempt := 0; attempt < attempts; attempt++ {
		ready := filepath.Join(dir, fmt.Sprintf("ready-%d", attempt))
		signalCh := make(chan os.Signal, 1)
		notificationDone := make(chan struct{})
		triggered := false
		var pollErr error
		var observed []ClaudeExitResult
		s := ClaudeSupervisor{
			PollInterval: time.Millisecond,
			Signals:      signalCh,
			Snapshot: func(context.Context) ([]SupervisorProcess, error) {
				return nil, nil
			},
			Poll: func(ctx context.Context, rootPID int, _ []SupervisorProcess) error {
				if triggered {
					return pollErr
				}
				if _, err := os.Stat(ready); err != nil {
					return nil
				}
				triggered = true
				if err := syscall.Kill(rootPID, syscall.SIGTERM); err != nil {
					pollErr = fmt.Errorf("signal trapped-exit fixture: %w", err)
					close(notificationDone)
					return pollErr
				}
				// Wait until the command's internal Wait goroutine has reaped the
				// root, then reproduce signal.Notify delivery lag. This pins the
				// bounded post-Wait arbitration path rather than scheduler luck.
				reapDeadline := time.NewTimer(time.Second)
				defer reapDeadline.Stop()
				for {
					err := syscall.Kill(rootPID, 0)
					if errors.Is(err, syscall.ESRCH) {
						break
					}
					select {
					case <-ctx.Done():
						pollErr = ctx.Err()
						close(notificationDone)
						return pollErr
					case <-reapDeadline.C:
						pollErr = errors.New("trapped-exit fixture was not reaped")
						close(notificationDone)
						return pollErr
					case <-time.After(time.Millisecond):
					}
				}
				time.AfterFunc(notificationDelay, func() {
					signalCh <- syscall.SIGTERM
					close(notificationDone)
				})
				return nil
			},
			OnExit: func(result ClaudeExitResult) error {
				observed = append(observed, result)
				return nil
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := s.Run(ctx, []string{
			"bash", "-c", `trap 'exit 42' TERM; : > "$1"; while :; do :; done`,
			"claude-supervisor-trapped-exit", ready,
		})
		cancel()
		select {
		case <-notificationDone:
		case <-time.After(time.Second):
			t.Fatalf("attempt %d delayed notification did not complete", attempt)
		}
		if pollErr != nil {
			t.Fatalf("attempt %d poll: %v", attempt, pollErr)
		}
		if err != nil {
			t.Fatalf("attempt %d Run: %v", attempt, err)
		}
		want := ClaudeExitResult{ExitCode: 42, Signaled: true, Signal: syscall.SIGTERM}
		if result != want {
			t.Fatalf("attempt %d result = %+v, want delayed terminal signal %+v", attempt, result, want)
		}
		if !reflect.DeepEqual(observed, []ClaudeExitResult{want}) {
			t.Fatalf("attempt %d OnExit = %+v, want one external result %+v", attempt, observed, want)
		}
	}
}

func TestClaudeSupervisor_waitReapedRootBetweenArbitrationAndForwardingIsNeverSignalledByPID(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	cmd := exec.Command("bash", "-c", `while [ ! -e "$1" ]; do sleep 0.005; done; exit 42`, "claude-supervisor-race", release)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gated fixture: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	waitCh := make(chan error, 1)
	waitDelivered := make(chan struct{})
	go func() {
		waitCh <- cmd.Wait()
		close(waitDelivered)
	}()
	signalCh := make(chan os.Signal, 1)
	signalCh <- syscall.SIGTERM

	rootPID := cmd.Process.Pid
	var numericSignals []string
	s := ClaudeSupervisor{
		GracePeriod: time.Millisecond,
		Snapshot: func(ctx context.Context) ([]SupervisorProcess, error) {
			// Force Wait to reap the owned child after the supervisor has chosen
			// the signal branch but before a process snapshot is returned. The
			// same numeric PID now represents only an untrusted recycled identity.
			if err := os.WriteFile(release, nil, 0o600); err != nil {
				return nil, err
			}
			select {
			case <-waitDelivered:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []SupervisorProcess{{PID: rootPID, PPID: 999, Start: "recycled-root"}}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			numericSignals = append(numericSignals, fmt.Sprintf("%d:%s", pid, sig))
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := s.superviseStarted(ctx, cmd.Process, waitCh, signalCh, nil)
	if err != nil {
		t.Fatalf("superviseStarted: %v", err)
	}
	want := ClaudeExitResult{ExitCode: 42, Signaled: true, Signal: syscall.SIGTERM}
	if result != want {
		t.Fatalf("result = %+v, want trapped external exit %+v", result, want)
	}
	if len(numericSignals) != 0 {
		t.Fatalf("numeric signals = %v, want recycled root PID %d untouched", numericSignals, rootPID)
	}
}

func TestClaudeSupervisor_waitFirstExternalSignalCleansRetainedVerifiedDescendant(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "child.pid")
	ready := filepath.Join(dir, "ready")
	cmd := exec.Command(
		"bash", "-c",
		`trap 'exit 42' TERM; sleep 10 & printf '%s\n' "$!" > "$1"; : > "$2"; while :; do sleep 1; done`,
		"claude-supervisor-descendant", childPIDFile, ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start descendant fixture: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant fixture did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	childPIDData, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("read child PID: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDData)))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID = %q, error = %v", childPIDData, err)
	}
	defer func() { _ = syscall.Kill(childPID, syscall.SIGKILL) }()

	waitCh := make(chan error, 1)
	waitDelivered := make(chan struct{})
	go func() {
		waitCh <- cmd.Wait()
		close(waitDelivered)
	}()
	signalCh := make(chan os.Signal, 1)
	rootPID := cmd.Process.Pid
	var pollOnce sync.Once
	var numericSignals []string
	s := ClaudeSupervisor{
		GracePeriod: time.Millisecond,
		Poll: func(ctx context.Context, _ int, _ []SupervisorProcess) error {
			pollOnce.Do(func() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-waitDelivered:
					signalCh <- syscall.SIGTERM
				case <-ctx.Done():
				}
			})
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			select {
			case <-waitDelivered:
				return []SupervisorProcess{{PID: childPID, PPID: 1, Start: "known-child"}}, nil
			default:
				return []SupervisorProcess{
					{PID: rootPID, PPID: 1, Start: "owned-root"},
					{PID: childPID, PPID: rootPID, Start: "known-child"},
				}, nil
			}
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			numericSignals = append(numericSignals, fmt.Sprintf("%d:%s", pid, sig))
			if pid == childPID && sig == syscall.SIGKILL {
				return syscall.Kill(pid, sig)
			}
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := s.superviseStarted(ctx, cmd.Process, waitCh, signalCh, nil)
	if err != nil {
		t.Fatalf("superviseStarted: %v", err)
	}
	wantResult := ClaudeExitResult{ExitCode: 42, Signaled: true, Signal: syscall.SIGTERM}
	if result != wantResult {
		t.Fatalf("result = %+v, want %+v", result, wantResult)
	}
	wantSignals := []string{
		fmt.Sprintf("%d:terminated", childPID),
		fmt.Sprintf("%d:killed", childPID),
	}
	if !reflect.DeepEqual(numericSignals, wantSignals) {
		t.Fatalf("numeric signals = %v, want retained descendant cleanup %v", numericSignals, wantSignals)
	}
	for _, sent := range numericSignals {
		if strings.HasPrefix(sent, strconv.Itoa(rootPID)+":") {
			t.Fatalf("numeric root signal observed: %s", sent)
		}
	}
}

func TestClaudeSupervisor_readyWaitIgnoresUnsupportedSignalAndClosedChannel(t *testing.T) {
	for _, tt := range []struct {
		name    string
		signals chan os.Signal
	}{
		{
			name: "unsupported",
			signals: func() chan os.Signal {
				ch := make(chan os.Signal, 1)
				ch <- syscall.SIGUSR1
				return ch
			}(),
		},
		{
			name: "closed",
			signals: func() chan os.Signal {
				ch := make(chan os.Signal)
				close(ch)
				return ch
			}(),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("true")
			if err := cmd.Run(); err != nil {
				t.Fatalf("run fixture: %v", err)
			}
			waitCh := make(chan error, 1)
			waitCh <- nil
			s := ClaudeSupervisor{}
			result, err := s.superviseStarted(
				context.Background(), cmd.Process, waitCh, tt.signals, nil,
			)
			if err != nil {
				t.Fatalf("superviseStarted: %v", err)
			}
			if result != (ClaudeExitResult{ExitCode: 0}) {
				t.Fatalf("result = %+v, want ordinary zero exit", result)
			}
		})
	}
}

func TestClaudeSupervisor_startFailurePublishesTerminalError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-claude")
	var observed []ClaudeExitResult
	s := ClaudeSupervisor{
		OnExit: func(result ClaudeExitResult) error {
			observed = append(observed, result)
			return nil
		},
	}

	result, err := s.Run(context.Background(), []string{missing})
	if err == nil {
		t.Fatal("Run succeeded with a missing launch command")
	}
	want := ClaudeExitResult{ExitCode: 1}
	if result != want {
		t.Fatalf("result = %+v, want terminal setup error %+v", result, want)
	}
	if !reflect.DeepEqual(observed, []ClaudeExitResult{want}) {
		t.Fatalf("OnExit results = %+v, want one terminal setup error %+v", observed, want)
	}
}

func TestClaudeSupervisor_nonExitWaitFailurePublishesTerminalError(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run fixture: %v", err)
	}
	waitFailure := errors.New("opaque wait failure")
	waitCh := make(chan error, 1)
	waitCh <- waitFailure
	var observed []ClaudeExitResult
	s := ClaudeSupervisor{
		OnExit: func(result ClaudeExitResult) error {
			observed = append(observed, result)
			return nil
		},
	}

	result, err := s.superviseStarted(context.Background(), cmd.Process, waitCh, nil, nil)
	if !errors.Is(err, waitFailure) {
		t.Fatalf("superviseStarted error = %v, want wrapped %v", err, waitFailure)
	}
	want := ClaudeExitResult{ExitCode: 1}
	if result != want {
		t.Fatalf("result = %+v, want terminal wait error %+v", result, want)
	}
	if !reflect.DeepEqual(observed, []ClaudeExitResult{want}) {
		t.Fatalf("OnExit results = %+v, want one terminal wait error %+v", observed, want)
	}
}

func TestClaudeSupervisor_cancellationBeforeStartDoesNotPublishTerminalError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	commandBuilt := false
	exitCalled := false
	s := ClaudeSupervisor{
		CommandContext: func(context.Context, string, ...string) *exec.Cmd {
			commandBuilt = true
			return exec.Command("true")
		},
		OnExit: func(ClaudeExitResult) error {
			exitCalled = true
			return nil
		},
	}

	result, err := s.Run(ctx, []string{"true"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
	if result != (ClaudeExitResult{}) {
		t.Fatalf("result = %+v, want no terminal result", result)
	}
	if commandBuilt || exitCalled {
		t.Fatalf("canceled launch built=%v OnExit=%v, want both false", commandBuilt, exitCalled)
	}
}

func TestClaudeSupervisor_signalledWaitDoesNotBecomeTerminalError(t *testing.T) {
	cmd := exec.Command("bash", "-c", "kill -TERM $$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start signal fixture: %v", err)
	}
	waitCh := make(chan error, 1)
	waitCh <- cmd.Wait()
	var observed ClaudeExitResult
	s := ClaudeSupervisor{
		OnExit: func(result ClaudeExitResult) error {
			observed = result
			return nil
		},
	}

	result, err := s.superviseStarted(context.Background(), cmd.Process, waitCh, nil, nil)
	if err != nil {
		t.Fatalf("superviseStarted: %v", err)
	}
	want := ClaudeExitResult{
		ExitCode: 128 + int(syscall.SIGTERM),
		Signaled: true,
		Signal:   syscall.SIGTERM,
	}
	if result != want || observed != want {
		t.Fatalf("result=%+v OnExit=%+v, want silent signal result %+v", result, observed, want)
	}
}

func TestClaudeSupervisorSnapshotDoesNotResolvePSFromPATH(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "path-ps-ran")
	fakePS := filepath.Join(dir, "ps")
	script := "#!/bin/sh\n: > \"$CLAUDE_PS_MARKER\"\nprintf ' 100 1 Mon Jul 13 09:00:00 2026\\n'\n"
	if err := os.WriteFile(fakePS, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake ps: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CLAUDE_PS_MARKER", marker)

	if _, err := claudeSupervisorSnapshot(context.Background()); err != nil {
		t.Fatalf("claudeSupervisorSnapshot: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity snapshot executed PATH-controlled ps: %v", err)
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
		Poll: func(_ context.Context, rootPID int, _ []SupervisorProcess) error {
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
		"2:terminated", "1:terminated",
		"2:killed", "1:killed",
	}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want %v", sent, want)
	}
}

func TestClaudeSupervisor_kills_deepest_first_after_grace(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	ready := filepath.Join(t.TempDir(), "ready")
	var childPID int
	var sent []string
	var once sync.Once
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, rootPID int, _ []SupervisorProcess) error {
			if _, err := os.Stat(ready); err != nil {
				return nil
			}
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
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Run(ctx, []string{
		"bash", "-c", `trap '' INT; : > "$1"; while :; do :; done`,
		"claude-supervisor-ignore-int", ready,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want forced SIGKILL", result)
	}
	want := []string{"1:interrupt", "1:killed"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want %v", sent, want)
	}
}

func TestClaudeSupervisor_snapshot_failure_still_kills_owned_root(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	ready := filepath.Join(t.TempDir(), "ready")
	var once sync.Once
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, _ int, _ []SupervisorProcess) error {
			if _, err := os.Stat(ready); err != nil {
				return nil
			}
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			return nil, errors.New("ps unavailable during shutdown")
		},
		SignalProcess: func(int, syscall.Signal) error {
			return errors.New("numeric signalling must never be used for the root")
		},
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Run(ctx, []string{
		"bash", "-c", `trap '' TERM; : > "$1"; while :; do :; done`,
		"claude-supervisor-ignore-term", ready,
	})
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
	var liveSnapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int, _ []SupervisorProcess) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			if rootPID <= 0 {
				return nil, nil
			}
			liveSnapshots++
			if liveSnapshots > 2 {
				return []SupervisorProcess{{PID: rootPID + 1, PPID: 1, Start: "child"}}, nil
			}
			return []SupervisorProcess{
				{PID: rootPID, PPID: 1, Start: "root"},
				{PID: rootPID + 1, PPID: rootPID, Start: "child"},
			}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-rootPID, sig))
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
	want := []string{"1:terminated", "1:killed"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want retained descendant cleanup %v", sent, want)
	}
}

func TestClaudeSupervisor_revalidates_process_identity_before_kill(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	ready := filepath.Join(t.TempDir(), "ready")
	var rootPID int
	var sent []string
	var once sync.Once
	var snapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int, _ []SupervisorProcess) error {
			if _, err := os.Stat(ready); err != nil {
				return nil
			}
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			if rootPID <= 0 {
				return nil, nil
			}
			snapshots++
			childStart := "child-original"
			childParent := rootPID
			if snapshots > 2 {
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
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Run(ctx, []string{
		"bash", "-c", `trap '' TERM; : > "$1"; while :; do :; done`,
		"claude-supervisor-ignore-term", ready,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want forced SIGKILL", result)
	}
	want := []string{"1:terminated"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want reused child PID excluded: %v", sent, want)
	}
}

func TestClaudeSupervisor_does_not_follow_reused_root_tree(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	var rootPID int
	var sent []string
	var once sync.Once
	var snapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int, _ []SupervisorProcess) error {
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			if rootPID <= 0 {
				return nil, nil
			}
			snapshots++
			if snapshots > 2 {
				return []SupervisorProcess{
					{PID: rootPID, PPID: 999, Start: "reused-root"},
					{PID: rootPID + 1, PPID: rootPID, Start: "unrelated-child"},
				}, nil
			}
			return []SupervisorProcess{
				{PID: rootPID, PPID: 1, Start: "owned-root"},
				{PID: rootPID + 1, PPID: rootPID, Start: "owned-child"},
			}, nil
		},
		SignalProcess: func(pid int, sig syscall.Signal) error {
			sent = append(sent, fmt.Sprintf("%d:%s", pid-rootPID, sig))
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
	want := []string{"1:terminated"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals = %v, want reused root tree untouched %v", sent, want)
	}
}

func TestClaudeSupervisor_kills_descendants_spawned_during_grace(t *testing.T) {
	signalCh := make(chan os.Signal, 1)
	ready := filepath.Join(t.TempDir(), "ready")
	var rootPID int
	var sent []string
	var once sync.Once
	var snapshots int
	s := ClaudeSupervisor{
		PollInterval: time.Millisecond,
		GracePeriod:  5 * time.Millisecond,
		Signals:      signalCh,
		Poll: func(_ context.Context, pid int, _ []SupervisorProcess) error {
			if _, err := os.Stat(ready); err != nil {
				return nil
			}
			rootPID = pid
			once.Do(func() { signalCh <- syscall.SIGTERM })
			return nil
		},
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			if rootPID <= 0 {
				return nil, nil
			}
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
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.Run(ctx, []string{
		"bash", "-c", `trap '' TERM; : > "$1"; while :; do :; done`,
		"claude-supervisor-ignore-term", ready,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Signaled || result.Signal != syscall.SIGKILL {
		t.Fatalf("result = %+v, want forced SIGKILL", result)
	}
	want := []string{"1:killed"}
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
		Poll: func(context.Context, int, []SupervisorProcess) error {
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
