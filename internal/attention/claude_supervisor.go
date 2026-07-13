package attention

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
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
	PID   int
	PPID  int
	Start string
}

// ClaudeSupervisor transparently owns one complete Claude fallback chain. It
// adds no PTY and no process group: the existing screenshot filter remains the
// sole PTY boundary.
type ClaudeSupervisor struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	PollInterval time.Duration
	GracePeriod  time.Duration
	Poll         func(context.Context, int) error
	OnExit       func(ClaudeExitResult) error

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
		return ClaudeExitResult{}, fmt.Errorf("start Claude launch chain: %w", err)
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

	rootPID := cmd.Process.Pid
	if s.Poll != nil {
		_ = s.Poll(ctx, rootPID)
	}
	for {
		select {
		case waitErr := <-waitCh:
			return s.finish(waitErr)
		case <-ticker.C:
			if s.Poll != nil {
				_ = s.Poll(ctx, rootPID)
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
			return s.shutdown(ctx, cmd.Process, sig, waitCh)
		case <-ctx.Done():
			return s.shutdown(ctx, cmd.Process, syscall.SIGTERM, waitCh)
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
	sig syscall.Signal,
	waitCh <-chan error,
) (ClaudeExitResult, error) {
	if rootProcess == nil {
		return ClaudeExitResult{}, errors.New("Claude supervisor root process is unavailable")
	}
	rootPID := rootProcess.Pid
	grace := s.GracePeriod
	if grace <= 0 {
		grace = defaultClaudeGracePeriod
	}
	// Cleanup must outlive cancellation of the caller context, but remain
	// bounded if ps or a platform process operation wedges.
	cleanupContext, cancelCleanup := context.WithTimeout(
		context.WithoutCancel(parentContext),
		grace+defaultClaudeCleanupSlack,
	)
	defer cancelCleanup()

	targets := s.signalTree(cleanupContext, rootPID, sig)
	timer := time.NewTimer(grace)
	rootExited := false
	var waitErr error
	select {
	case waitErr = <-waitCh:
		rootExited = true
		<-timer.C
	case <-timer.C:
	}
	if !rootExited {
		select {
		case waitErr = <-waitCh:
			rootExited = true
		default:
		}
	}

	// Keep the original snapshot. Once the shell root exits, surviving
	// descendants are reparented and cannot be rediscovered by ancestry.
	liveTargets := s.revalidateShutdownTargets(cleanupContext, targets, rootPID, rootExited)
	s.signalProcesses(liveTargets, syscall.SIGKILL)
	if !rootExited {
		// The child handle is lifecycle-aware and remains safe when a process
		// table snapshot fails. It is the final root fallback after verified
		// descendant kills.
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

func (s *ClaudeSupervisor) signalTree(ctx context.Context, rootPID int, sig syscall.Signal) []SupervisorProcess {
	snapshot := s.Snapshot
	if snapshot == nil {
		snapshot = claudeSupervisorSnapshot
	}
	processes, err := snapshot(ctx)
	if err != nil {
		processes = nil
	}
	ordered := claudeShutdownTargets(processes, rootPID)
	s.signalProcesses(ordered, sig)
	return ordered
}

func (s *ClaudeSupervisor) revalidateShutdownTargets(
	ctx context.Context,
	targets []SupervisorProcess,
	rootPID int,
	rootExited bool,
) []SupervisorProcess {
	snapshot := s.Snapshot
	if snapshot == nil {
		snapshot = claudeSupervisorSnapshot
	}
	processes, err := snapshot(ctx)
	if err != nil {
		processes = nil
	}
	current := make(map[int]SupervisorProcess, len(processes))
	for _, process := range processes {
		if process.PID > 0 {
			current[process.PID] = process
		}
	}
	live := make([]SupervisorProcess, 0, len(targets))
	type processIdentity struct {
		pid   int
		start string
	}
	seen := make(map[processIdentity]struct{}, len(targets))
	appendLive := func(target SupervisorProcess) {
		key := processIdentity{pid: target.PID, start: target.Start}
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		live = append(live, target)
	}
	var originalRoot SupervisorProcess
	for _, target := range targets {
		if target.PID == rootPID {
			originalRoot = target
			break
		}
	}

	// First take the refreshed launch tree, but only while its root is still
	// the exact owned process. Otherwise a recycled root PID could make an
	// unrelated tree look like newly spawned descendants.
	refreshedRoot, refreshedRootFound := current[rootPID]
	acceptRefreshedTree := !rootExited && refreshedRootFound
	if acceptRefreshedTree && originalRoot.Start != "" {
		acceptRefreshedTree = refreshedRoot.Start == originalRoot.Start &&
			refreshedRoot.PPID == originalRoot.PPID
	}
	if acceptRefreshedTree {
		for _, target := range claudeShutdownTargets(processes, rootPID) {
			if target.Start != "" {
				appendLive(target)
			}
		}
	}

	// Then retain original descendants that survived but were reparented after
	// the shell root exited. Only an exact PID + process-start identity matches.
	for _, target := range targets {
		if target.PID == rootPID && rootExited {
			continue
		}
		observed, ok := current[target.PID]
		identityMatches := target.Start != "" && ok && observed.Start == target.Start
		if target.PID == rootPID {
			identityMatches = identityMatches && observed.PPID == target.PPID
		}
		if identityMatches {
			appendLive(target)
			continue
		}
		// An identity-free root is the only safe fallback: if Wait has not
		// returned, it is still the owned child. Descendants are never killed
		// from an unverified stale PID.
		if target.PID == rootPID && !rootExited && target.Start == "" {
			appendLive(target)
		}
	}
	return live
}

func (s *ClaudeSupervisor) signalProcesses(ordered []SupervisorProcess, sig syscall.Signal) {
	signalProcess := s.SignalProcess
	if signalProcess == nil {
		signalProcess = func(pid int, signal syscall.Signal) error {
			return syscall.Kill(pid, signal)
		}
	}
	for _, process := range ordered {
		_ = signalProcess(process.PID, sig)
	}
}

func (s *ClaudeSupervisor) finish(waitErr error) (ClaudeExitResult, error) {
	result, err := claudeExitResult(waitErr)
	if err != nil {
		return ClaudeExitResult{}, err
	}
	return s.publishExit(result)
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

func claudeSupervisorSnapshot(ctx context.Context) ([]SupervisorProcess, error) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,lstart=")
	cmd.Env = applyEnvironmentOverrides(os.Environ(), []string{"LC_ALL=C", "TZ=UTC"})
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("snapshot processes: %w", err)
	}
	var processes []SupervisorProcess
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	seen := make(map[int]struct{})
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		match := psSnapshotLine.FindStringSubmatch(scanner.Text())
		if match == nil {
			return nil, fmt.Errorf("invalid ps row %d", lineNumber)
		}
		pid, err := parsePositiveInt(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid PID on ps row %d: %w", lineNumber, err)
		}
		ppid, err := parseNonnegativeInt(match[2])
		if err != nil {
			return nil, fmt.Errorf("invalid parent PID on ps row %d: %w", lineNumber, err)
		}
		start := strings.TrimSpace(match[3])
		if _, err := time.Parse("Mon Jan _2 15:04:05 2006", start); err != nil {
			return nil, fmt.Errorf("invalid start time on ps row %d: %w", lineNumber, err)
		}
		if _, duplicate := seen[pid]; duplicate {
			return nil, fmt.Errorf("duplicate PID %d in ps snapshot", pid)
		}
		seen[pid] = struct{}{}
		processes = append(processes, SupervisorProcess{PID: pid, PPID: ppid, Start: start})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse process snapshot: %w", err)
	}
	return processes, nil
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
