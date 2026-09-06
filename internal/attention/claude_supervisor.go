package attention

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

const (
	defaultClaudePollInterval = 250 * time.Millisecond
	defaultClaudeGracePeriod  = 300 * time.Millisecond
	defaultClaudeCleanupSlack = 2 * time.Second
)

// ClaudeExitResult is the supervised launch chain's exact terminal outcome.
// Signal-caused exits are distinguished so reducers never invent an error
// notification while Wisp Deck is intentionally tearing a pane down.
type ClaudeExitResult struct {
	ExitCode int
	Signaled bool
	Signal   syscall.Signal
}

// SupervisorProcess is the process-tree information needed for deterministic
// deepest-first shutdown. Start is the locale-stable UTC lstart identity used
// to reject a PID that was recycled during the grace period.
type SupervisorProcess struct {
	PID  int
	PPID int
	// StartSec is the process start time in whole Unix seconds, the resolution
	// `ps -o lstart=` prints and Claude Code records. It is an identity token,
	// never displayed: zero means the start time is unknown.
	StartSec int64
}

// claudeDescendantTracker retains only PID + start-time identities observed in
// the owned root's ancestry while the lifecycle-aware root handle was live.
// Reparented survivors remain addressable, but every numeric signal still
// requires an exact identity match in a fresh snapshot.
type claudeDescendantTracker struct {
	rootPID int
	ordered []SupervisorProcess
}

// ClaudeSupervisor transparently owns one complete Claude fallback chain. It
// adds no PTY and no process group: the existing screenshot filter remains the
// sole PTY boundary.
type ClaudeSupervisor struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	PollInterval    time.Duration
	GracePeriod     time.Duration
	SignalReconcile time.Duration
	// Poll receives the process table the tick already read, so the registry
	// does not materialise it a second time milliseconds later.
	Poll   func(context.Context, int, []SupervisorProcess) error
	OnExit func(ClaudeExitResult) error

	CommandContext func(context.Context, string, ...string) *exec.Cmd
	Snapshot       func(context.Context) ([]SupervisorProcess, error)
	SignalProcess  func(int, syscall.Signal) error
	Signals        <-chan os.Signal
}

// Run starts command with inherited stdio, polls against the stable child root
// PID, and returns its exact exit result.
func (s *ClaudeSupervisor) Run(ctx context.Context, command []string) (ClaudeExitResult, error) {
	if len(command) == 0 || command[0] == "" {
		return ClaudeExitResult{}, errors.New("claude supervisor command is empty")
	}
	commandContext := s.CommandContext
	if commandContext == nil {
		// Run owns context cancellation so it can snapshot and terminate the
		// complete launch tree before the shell root is reaped and descendants
		// are reparented. exec.CommandContext would race that lifecycle.
		commandContext = func(_ context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command(name, args...)
		}
	}
	if err := ctx.Err(); err != nil {
		return ClaudeExitResult{}, fmt.Errorf("start Claude launch chain: %w", err)
	}
	cmd := commandContext(ctx, command[0], command[1:]...)
	if cmd == nil {
		return ClaudeExitResult{}, errors.New("claude supervisor command factory returned nil")
	}
	cmd.Stdin = s.Stdin
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = s.Stdout
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = s.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		startErr := fmt.Errorf("start Claude launch chain: %w", err)
		if ctx.Err() != nil {
			return ClaudeExitResult{}, startErr
		}
		return s.finishTerminalError(startErr)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	pollInterval := s.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultClaudePollInterval
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	signals := s.Signals
	var ownedSignals chan os.Signal
	if signals == nil {
		ownedSignals = make(chan os.Signal, 4)
		signal.Notify(ownedSignals, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		defer signal.Stop(ownedSignals)
		signals = ownedSignals
	}

	return s.superviseStarted(ctx, cmd.Process, waitCh, signals, ticker.C)
}

// superviseStarted serializes terminal events for one already-started launch.
func (s *ClaudeSupervisor) superviseStarted(
	ctx context.Context,
	rootProcess *os.Process,
	waitCh <-chan error,
	signals <-chan os.Signal,
	ticks <-chan time.Time,
) (ClaudeExitResult, error) {
	if rootProcess == nil {
		return ClaudeExitResult{}, errors.New("Claude supervisor root process is unavailable")
	}
	rootPID := rootProcess.Pid
	descendants := &claudeDescendantTracker{rootPID: rootPID}
	// Poll callbacks may synchronously observe cancellation and wait for the root
	// to exit. Capture descendants first while the owned process handle can still
	// prove that the snapshot belongs to this launch.
	shared := s.refreshTrackedDescendants(ctx, rootProcess, descendants)
	if s.Poll != nil {
		_ = s.Poll(ctx, rootPID, shared)
	}

	var waitErr error
	waitReady := false
	shutdown := func(sig syscall.Signal) (ClaudeExitResult, error) {
		return s.shutdown(ctx, rootProcess, descendants, sig, waitCh)
	}
	forward := func(sig syscall.Signal) (ClaudeExitResult, error) {
		// Wait owns the lifecycle-safe child handle. If it has already reaped the
		// root, classify the latched result without signalling a potentially
		// recycled numeric PID.
		select {
		case waitErr = <-waitCh:
			waitReady = true
			s.cleanupTrackedDescendants(ctx, descendants, sig)
			return s.finishExternal(waitErr, sig)
		default:
			return shutdown(sig)
		}
	}
	for {
		// Latch Wait before inspecting signals. Once the child is reaped, shutdown
		// must never address its numeric PID again.
		select {
		case waitErr = <-waitCh:
			waitReady = true
		default:
		}
		if waitReady {
			return s.finishAfterWait(ctx, waitErr, signals, descendants)
		}

		// Consume at most one ready signal per turn. Unsupported signals cannot
		// spin forever, and a closed channel is disabled.
		if signals != nil {
			select {
			case received, ok := <-signals:
				if !ok {
					signals = nil
					continue
				}
				sig, valid := received.(syscall.Signal)
				if valid && claudeForwardedSignal(sig) {
					return forward(sig)
				}
				continue
			default:
			}
		}
		if ctx.Err() != nil {
			return forward(syscall.SIGTERM)
		}
		select {
		case waitErr = <-waitCh:
			waitReady = true
		case <-ticks:
			shared := s.refreshTrackedDescendants(ctx, rootProcess, descendants)
			if s.Poll != nil {
				_ = s.Poll(ctx, rootPID, shared)
			}
		case received, ok := <-signals:
			if !ok {
				signals = nil
				continue
			}
			sig, ok := received.(syscall.Signal)
			if !ok || !claudeForwardedSignal(sig) {
				continue
			}
			return forward(sig)
		case <-ctx.Done():
			return forward(syscall.SIGTERM)
		}
	}
}

func (s *ClaudeSupervisor) finishAfterWait(
	ctx context.Context,
	waitErr error,
	signals <-chan os.Signal,
	descendants *claudeDescendantTracker,
) (ClaudeExitResult, error) {
	if signals == nil {
		if ctx.Err() != nil {
			s.cleanupTrackedDescendants(ctx, descendants, syscall.SIGTERM)
			return s.finishExternal(waitErr, syscall.SIGTERM)
		}
		return s.finish(waitErr)
	}
	// signal.Notify cannot prove that no terminal signal is still queued behind
	// Wait. Bound that ambiguity by the same grace period used for cooperative
	// shutdown; callers may explicitly override the arbitration window.
	reconcile := s.SignalReconcile
	if reconcile <= 0 {
		reconcile = s.gracePeriod()
	}
	deadline := time.Now().Add(reconcile)
	timer := time.NewTimer(reconcile)
	defer timer.Stop()
	for {
		select {
		case received, ok := <-signals:
			if !ok {
				if ctx.Err() != nil {
					s.cleanupTrackedDescendants(ctx, descendants, syscall.SIGTERM)
					return s.finishExternal(waitErr, syscall.SIGTERM)
				}
				return s.finish(waitErr)
			}
			sig, valid := received.(syscall.Signal)
			if valid && claudeForwardedSignal(sig) {
				s.cleanupTrackedDescendants(ctx, descendants, sig)
				return s.finishExternal(waitErr, sig)
			}
			if !time.Now().Before(deadline) {
				return s.finish(waitErr)
			}
		case <-ctx.Done():
			s.cleanupTrackedDescendants(ctx, descendants, syscall.SIGTERM)
			return s.finishExternal(waitErr, syscall.SIGTERM)
		case <-timer.C:
			return s.finish(waitErr)
		}
	}
}

func claudeForwardedSignal(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT:
		return true
	default:
		return false
	}
}

func (s *ClaudeSupervisor) shutdown(
	parentContext context.Context,
	rootProcess *os.Process,
	descendants *claudeDescendantTracker,
	sig syscall.Signal,
	waitCh <-chan error,
) (ClaudeExitResult, error) {
	if rootProcess == nil {
		return ClaudeExitResult{}, errors.New("Claude supervisor root process is unavailable")
	}
	grace := s.gracePeriod()
	// Cleanup must outlive cancellation of the caller context, but remain
	// bounded if ps or a platform process operation wedges.
	cleanupContext, cancelCleanup := context.WithTimeout(
		context.WithoutCancel(parentContext),
		grace+defaultClaudeCleanupSlack,
	)
	defer cancelCleanup()

	// Refresh only while the lifecycle-aware child handle still proves that the
	// root in this snapshot is the process we started. A Wait that wins inside
	// this refresh is handled before any root signal is attempted.
	s.refreshTrackedDescendants(cleanupContext, rootProcess, descendants)
	rootExited := false
	var waitErr error
	select {
	case waitErr = <-waitCh:
		rootExited = true
	default:
	}
	if rootExited {
		s.cleanupTrackedDescendantsWithContext(cleanupContext, descendants, sig)
		return s.finishExternal(waitErr, sig)
	}

	// Descendants use numeric PIDs only after a fresh exact identity check. The
	// root is deliberately excluded and is signalled solely through os.Process.
	s.signalVerifiedDescendants(cleanupContext, descendants, sig)
	select {
	case waitErr = <-waitCh:
		rootExited = true
	default:
		_ = rootProcess.Signal(sig)
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()
	rootWait := waitCh
	if rootExited {
		rootWait = nil
	}
graceLoop:
	for {
		select {
		case waitErr = <-rootWait:
			rootExited = true
			rootWait = nil
		case <-timer.C:
			break graceLoop
		case <-cleanupContext.Done():
			break graceLoop
		}
	}

	if !rootExited {
		select {
		case waitErr = <-waitCh:
			rootExited = true
		default:
			s.refreshTrackedDescendants(cleanupContext, rootProcess, descendants)
		}
	}
	s.signalVerifiedDescendants(cleanupContext, descendants, syscall.SIGKILL)
	if !rootExited {
		// The lifecycle-aware handle is the only permitted root kill path.
		_ = rootProcess.Kill()
		select {
		case waitErr = <-waitCh:
		case <-cleanupContext.Done():
			result := ClaudeExitResult{
				ExitCode: 128 + int(syscall.SIGKILL),
				Signaled: true,
				Signal:   syscall.SIGKILL,
			}
			_, _ = s.publishExit(result)
			return result, fmt.Errorf("wait for Claude launch cleanup: %w", cleanupContext.Err())
		}
	}
	return s.finishExternal(waitErr, sig)
}

func (s *ClaudeSupervisor) gracePeriod() time.Duration {
	if s.GracePeriod > 0 {
		return s.GracePeriod
	}
	return defaultClaudeGracePeriod
}

func (s *ClaudeSupervisor) snapshotProcesses(ctx context.Context) ([]SupervisorProcess, error) {
	snapshot := s.Snapshot
	if snapshot == nil {
		snapshot = claudeSupervisorSnapshot
	}
	return snapshot(ctx)
}

// refreshTrackedDescendants re-reads the process table and returns it, so the
// caller can hand the same reading to the registry poll instead of paying for
// a second one in the same tick.
func (s *ClaudeSupervisor) refreshTrackedDescendants(
	ctx context.Context,
	rootProcess *os.Process,
	tracked *claudeDescendantTracker,
) (shared []SupervisorProcess) {
	if rootProcess == nil || tracked == nil || tracked.rootPID <= 0 || rootProcess.Pid != tracked.rootPID {
		return nil
	}
	processes, err := s.snapshotProcesses(ctx)
	if err != nil {
		return
	}
	shared = processes
	var root SupervisorProcess
	rootFound := false
	for _, process := range processes {
		if process.PID == tracked.rootPID && process.StartSec != 0 {
			root = process
			rootFound = true
			break
		}
	}
	if !rootFound {
		return
	}
	// This no-op signal uses the owned process handle (pidfd where available).
	// If Wait already reaped the root, do not trust a snapshot row that may be a
	// recycled numeric PID.
	if err := rootProcess.Signal(syscall.Signal(0)); err != nil {
		return
	}

	targets := claudeShutdownTargets(processes, tracked.rootPID)
	captured := make([]SupervisorProcess, 0, len(targets))
	capturedPIDs := make(map[int]struct{}, len(targets))
	for _, target := range targets {
		if target.PID <= 0 || target.PID == root.PID || target.StartSec == 0 {
			continue
		}
		if _, duplicate := capturedPIDs[target.PID]; duplicate {
			continue
		}
		capturedPIDs[target.PID] = struct{}{}
		captured = append(captured, target)
	}

	// Put the newest deep-first tree first, retaining older exact identities for
	// descendants that have already reparented and disappeared from ancestry.
	merged := make([]SupervisorProcess, 0, len(captured)+len(tracked.ordered))
	merged = append(merged, captured...)
	seenPIDs := capturedPIDs
	for _, target := range tracked.ordered {
		if target.PID <= 0 || target.PID == tracked.rootPID || target.StartSec == 0 {
			continue
		}
		if _, duplicate := seenPIDs[target.PID]; duplicate {
			continue
		}
		seenPIDs[target.PID] = struct{}{}
		merged = append(merged, target)
	}
	tracked.ordered = merged
	return shared
}

func (s *ClaudeSupervisor) verifiedTrackedDescendants(
	ctx context.Context,
	tracked *claudeDescendantTracker,
) []SupervisorProcess {
	if tracked == nil || len(tracked.ordered) == 0 {
		return nil
	}
	processes, err := s.snapshotProcesses(ctx)
	if err != nil {
		return nil
	}
	current := make(map[int]SupervisorProcess, len(processes))
	for _, process := range processes {
		if process.PID > 0 {
			current[process.PID] = process
		}
	}
	verified := make([]SupervisorProcess, 0, len(tracked.ordered))
	seen := make(map[int]struct{}, len(tracked.ordered))
	for _, target := range tracked.ordered {
		if target.PID <= 0 || target.PID == tracked.rootPID || target.StartSec == 0 {
			continue
		}
		if _, duplicate := seen[target.PID]; duplicate {
			continue
		}
		observed, ok := current[target.PID]
		if !ok || observed.StartSec != target.StartSec {
			continue
		}
		seen[target.PID] = struct{}{}
		verified = append(verified, target)
	}
	return verified
}

func (s *ClaudeSupervisor) signalVerifiedDescendants(
	ctx context.Context,
	tracked *claudeDescendantTracker,
	sig syscall.Signal,
) []SupervisorProcess {
	verified := s.verifiedTrackedDescendants(ctx, tracked)
	signalProcess := s.SignalProcess
	if signalProcess == nil {
		signalProcess = func(pid int, signal syscall.Signal) error {
			return syscall.Kill(pid, signal)
		}
	}
	for _, process := range verified {
		if tracked == nil || process.PID == tracked.rootPID {
			continue
		}
		_ = signalProcess(process.PID, sig)
	}
	return verified
}

func (s *ClaudeSupervisor) cleanupTrackedDescendants(
	parentContext context.Context,
	tracked *claudeDescendantTracker,
	sig syscall.Signal,
) {
	grace := s.gracePeriod()
	cleanupContext, cancelCleanup := context.WithTimeout(
		context.WithoutCancel(parentContext),
		grace+defaultClaudeCleanupSlack,
	)
	defer cancelCleanup()
	s.cleanupTrackedDescendantsWithContext(cleanupContext, tracked, sig)
}

func (s *ClaudeSupervisor) cleanupTrackedDescendantsWithContext(
	ctx context.Context,
	tracked *claudeDescendantTracker,
	sig syscall.Signal,
) {
	if len(s.signalVerifiedDescendants(ctx, tracked, sig)) == 0 {
		return
	}
	timer := time.NewTimer(s.gracePeriod())
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	s.signalVerifiedDescendants(ctx, tracked, syscall.SIGKILL)
}

func (s *ClaudeSupervisor) finish(waitErr error) (ClaudeExitResult, error) {
	result, err := claudeExitResult(waitErr)
	if err != nil {
		return s.finishTerminalError(err)
	}
	return s.publishExit(result)
}

func (s *ClaudeSupervisor) finishTerminalError(terminalErr error) (ClaudeExitResult, error) {
	result := ClaudeExitResult{ExitCode: 1}
	_, publishErr := s.publishExit(result)
	if publishErr != nil {
		terminalErr = errors.Join(terminalErr, publishErr)
	}
	return result, terminalErr
}

func (s *ClaudeSupervisor) finishExternal(waitErr error, forwarded syscall.Signal) (ClaudeExitResult, error) {
	result, err := claudeExitResult(waitErr)
	if err != nil {
		return ClaudeExitResult{}, err
	}
	if !result.Signaled {
		result.Signaled = true
		result.Signal = forwarded
	}
	return s.publishExit(result)
}

func (s *ClaudeSupervisor) publishExit(result ClaudeExitResult) (ClaudeExitResult, error) {
	if s.OnExit != nil {
		if err := s.OnExit(result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func claudeExitResult(waitErr error) (ClaudeExitResult, error) {
	if waitErr == nil {
		return ClaudeExitResult{ExitCode: 0}, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		return ClaudeExitResult{}, fmt.Errorf("wait for Claude launch chain: %w", waitErr)
	}
	result := ClaudeExitResult{ExitCode: exitErr.ExitCode()}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.Signaled = true
		result.Signal = status.Signal()
		result.ExitCode = 128 + int(result.Signal)
	}
	return result, nil
}

func claudeSupervisorSnapshot(_ context.Context) ([]SupervisorProcess, error) {
	return systemProcessTable()
}

func claudeShutdownTargets(processes []SupervisorProcess, rootPID int) []SupervisorProcess {
	orderedPIDs := claudeDescendantsDeepFirst(processes, rootPID)
	byPID := make(map[int]SupervisorProcess, len(processes))
	for _, process := range processes {
		if _, exists := byPID[process.PID]; !exists {
			byPID[process.PID] = process
		}
	}
	targets := make([]SupervisorProcess, 0, len(orderedPIDs))
	for _, pid := range orderedPIDs {
		process, ok := byPID[pid]
		if !ok {
			process = SupervisorProcess{PID: pid}
		}
		targets = append(targets, process)
	}
	return targets
}

// claudeDescendantsDeepFirst returns live descendants before their parents and
// always places the supervised root last. Equal-depth processes retain snapshot
// order for deterministic signalling.
func claudeDescendantsDeepFirst(processes []SupervisorProcess, rootPID int) []int {
	type ranked struct {
		pid   int
		depth int
		order int
	}
	parents := make(map[int]int, len(processes))
	orders := make(map[int]int, len(processes))
	for i, process := range processes {
		if process.PID <= 0 {
			continue
		}
		if _, exists := parents[process.PID]; exists {
			continue
		}
		parents[process.PID] = process.PPID
		orders[process.PID] = i
	}
	var found []ranked
	for pid := range parents {
		if pid == rootPID {
			continue
		}
		depth := 0
		current := pid
		seen := map[int]bool{}
		for current != rootPID && current > 0 && !seen[current] {
			seen[current] = true
			parent, ok := parents[current]
			if !ok {
				current = -1
				break
			}
			depth++
			current = parent
		}
		if current == rootPID {
			found = append(found, ranked{pid: pid, depth: depth, order: orders[pid]})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].depth != found[j].depth {
			return found[i].depth > found[j].depth
		}
		return found[i].order < found[j].order
	})
	ordered := make([]int, 0, len(found)+1)
	for _, process := range found {
		ordered = append(ordered, process.pid)
	}
	if rootPID > 0 {
		ordered = append(ordered, rootPID)
	}
	return ordered
}
