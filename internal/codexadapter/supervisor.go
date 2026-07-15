package codexadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"

	"github.com/jackuait/wisp-deck/internal/attention"
)

const (
	defaultCodexSetupTimeout  = 3 * time.Second
	defaultCodexReconnectWait = 100 * time.Millisecond
	defaultCodexServerGrace   = 300 * time.Millisecond
	defaultCodexSocketPoll    = 10 * time.Millisecond
	defaultCodexPTYGrace      = 300 * time.Millisecond
	codexNotifyDisabled       = `notify=[]`
	codexHooksDisabled        = `features.hooks=false`
)

var codexNotificationConfigs = []string{
	codexNotifyDisabled,
	codexHooksDisabled,
	`tui.notifications=["agent-turn-complete"]`,
	`tui.notification_method="osc9"`,
	`tui.notification_condition="always"`,
}

var codexAttentionGeneration = regexp.MustCompile(`^generation\.[A-Za-z0-9]+$`)

type CodexSupervisorOptions struct {
	CodexPath      string
	StateFile      string
	Generation     string
	ProjectCWD     string
	ClientVersion  string
	ResumeSession  string
	FallbackWindow time.Duration
	Prompt         string
	ErrorLog       string
}

type CodexExitResult struct {
	ExitCode int
	Signaled bool
	Signal   syscall.Signal
	Elapsed  time.Duration
}

type ObserverConnection interface {
	Snapshot() []Thread
	Next(context.Context) (ReducerEvent, error)
	Close()
}

type AppServerProcess interface {
	Stop(context.Context) error
	Done() <-chan error
}

type CodexSupervisor struct {
	Stdin  io.Reader
	Stdout io.Writer

	TempBase       string
	SetupTimeout   time.Duration
	ReconnectDelay time.Duration
	ServerGrace    time.Duration
	PTYGrace       time.Duration

	StartServer  func(context.Context, []string, io.Writer) (AppServerProcess, error)
	OpenObserver func(context.Context, ObserverConfig) (ObserverConnection, error)
	RunPTY       func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error)
	EnterRaw     func() (func(), error)

	Signals      <-chan os.Signal
	TerminalSize func() (*pty.Winsize, error)
}

type codexSignalRouter struct {
	out  chan os.Signal
	stop chan struct{}
	done chan struct{}
	once sync.Once

	mu          sync.Mutex
	termination syscall.Signal
}

func newCodexSignalRouter(source <-chan os.Signal, cancel context.CancelFunc) *codexSignalRouter {
	router := &codexSignalRouter{
		out: make(chan os.Signal, 8), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go func() {
		defer close(router.done)
		defer close(router.out)
		for {
			select {
			case received, ok := <-source:
				if !ok {
					return
				}
				sig, ok := received.(syscall.Signal)
				if !ok || (sig != syscall.SIGWINCH && !forwardedCodexSignal(sig)) {
					continue
				}
				select {
				case router.out <- received:
				default:
				}
				if forwardedCodexSignal(sig) {
					router.mu.Lock()
					if router.termination == 0 {
						router.termination = sig
					}
					router.mu.Unlock()
					cancel()
					return
				}
			case <-router.stop:
				return
			}
		}
	}()
	return router
}

func (r *codexSignalRouter) Stop() {
	r.once.Do(func() { close(r.stop) })
	<-r.done
}

func (r *codexSignalRouter) Termination() syscall.Signal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.termination
}

func buildCodexServerArgv(codexPath, socketPath string) []string {
	return []string{
		codexPath, "app-server", "-c", codexNotifyDisabled,
		"-c", codexHooksDisabled,
		"--listen", "unix://" + socketPath,
	}
}

func buildCodexTUIArgv(codexPath, socketPath string, remote bool, resumeSession, prompt string) []string {
	argv := []string{codexPath}
	if resumeSession != "" {
		argv = append(argv, "resume")
	}
	if remote {
		argv = append(argv, "--remote", "unix://"+socketPath)
	}
	for _, config := range codexNotificationConfigs {
		argv = append(argv, "-c", config)
	}
	if resumeSession != "" || prompt != "" {
		argv = append(argv, "--")
		if resumeSession != "" {
			argv = append(argv, resumeSession)
		}
		if prompt != "" {
			argv = append(argv, prompt)
		}
	}
	return argv
}

func (s *CodexSupervisor) Run(ctx context.Context, options CodexSupervisorOptions) (CodexExitResult, error) {
	if err := validateCodexSupervisorOptions(options); err != nil {
		return CodexExitResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CodexExitResult{}, err
	}
	runStartedAt := time.Now()
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	signalSource := s.Signals
	var ownedSignals chan os.Signal
	if signalSource == nil {
		ownedSignals = make(chan os.Signal, 8)
		signal.Notify(ownedSignals, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		defer signal.Stop(ownedSignals)
		signalSource = ownedSignals
	}
	signalRouter := newCodexSignalRouter(signalSource, cancelRun)
	defer signalRouter.Stop()
	ctx = runContext
	signalOutcome := func() (CodexExitResult, bool) {
		sig := signalRouter.Termination()
		if sig == 0 {
			return CodexExitResult{}, false
		}
		return CodexExitResult{
			ExitCode: 128 + int(sig), Signaled: true, Signal: sig, Elapsed: time.Since(runStartedAt),
		}, true
	}

	writer, writerErr := attention.NewAtomicWriter(options.StateFile, options.Generation)
	monitoring := writerErr == nil
	publish := func(state attention.State) {
		if !monitoring {
			return
		}
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			monitoring = false
		}
	}

	runtimeBase := s.TempBase
	if runtimeBase == "" {
		runtimeBase = "/tmp"
	}
	runtimeDir, socketPath, privateSetupErr := makeCodexRuntimeDir(runtimeBase)
	privateServerReady := privateSetupErr == nil
	if privateServerReady {
		defer os.RemoveAll(runtimeDir)
	}

	var serverLog *os.File
	if privateServerReady {
		logPath := options.ErrorLog
		if logPath == "" {
			logPath = os.Getenv("WISP_DECK_ERROR_LOG")
		}
		serverLog, privateSetupErr = openCodexErrorLog(logPath)
		if privateSetupErr != nil {
			privateServerReady = false
		} else {
			defer serverLog.Close()
		}
	}

	startServer := s.StartServer
	usingDefaultServer := startServer == nil
	if startServer == nil {
		grace := s.ServerGrace
		if grace <= 0 {
			grace = defaultCodexServerGrace
		}
		startServer = func(startContext context.Context, argv []string, log io.Writer) (AppServerProcess, error) {
			return startDefaultAppServer(startContext, argv, log, grace)
		}
	}
	openObserver := s.OpenObserver
	if openObserver == nil {
		openObserver = func(openContext context.Context, config ObserverConfig) (ObserverConnection, error) {
			observer, err := NewObserver(config)
			if err != nil {
				return nil, err
			}
			return observer.Open(openContext)
		}
	}

	var server AppServerProcess
	remote := false
	var serverErr error
	if privateServerReady {
		server, serverErr = startServer(ctx, buildCodexServerArgv(options.CodexPath, socketPath), serverLog)
		if serverErr == nil && server != nil {
			remote = true
		}
	}
	var stopServerOnce sync.Once
	stopServer := func() {
		stopServerOnce.Do(func() {
			if server == nil {
				return
			}
			grace := s.ServerGrace
			if grace <= 0 {
				grace = defaultCodexServerGrace
			}
			stopContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace+time.Second)
			defer cancel()
			_ = server.Stop(stopContext)
		})
	}
	defer stopServer()

	setupTimeout := s.SetupTimeout
	if setupTimeout <= 0 {
		setupTimeout = defaultCodexSetupTimeout
	}
	observerConfig := ObserverConfig{SocketPath: socketPath, ClientVersion: options.ClientVersion}
	var initial ObserverConnection
	if remote {
		setupContext, cancel := context.WithTimeout(ctx, setupTimeout)
		if usingDefaultServer {
			serverErr = waitForCodexSocketOrServer(setupContext, socketPath, defaultCodexSocketPoll, server.Done())
		}
		if serverErr == nil {
			initial, serverErr = openObserverUntil(setupContext, openObserver, observerConfig, defaultCodexSocketPoll, server.Done())
		}
		cancel()
		if serverErr != nil || initial == nil {
			remote = false
			stopServer()
		}
	}
	if result, signaled := signalOutcome(); signaled {
		return result, nil
	}

	newReducer := func(resume string, snapshot []Thread, reliable bool) (*Reducer, error) {
		baseline := []string(nil)
		if resume == "" && reliable {
			baseline = snapshotThreadIDs(snapshot)
		}
		reducer, err := NewReducer(ReducerConfig{
			Generation: options.Generation, ProjectCWD: options.ProjectCWD,
			ResumeThreadID: resume, BaselineThreadIDs: baseline,
		})
		if err != nil {
			return nil, err
		}
		if reliable {
			publish(reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: snapshot}))
		} else {
			publish(reducer.Reduce(ReducerEvent{Kind: EventObserverUnavailable}))
		}
		return reducer, nil
	}

	var reducer *Reducer
	var err error
	if remote {
		reducer, err = newReducer(options.ResumeSession, initial.Snapshot(), true)
	} else {
		reducer, err = newReducer(options.ResumeSession, nil, false)
	}
	if err != nil {
		if initial != nil {
			initial.Close()
		}
		return CodexExitResult{}, err
	}

	messages := make(chan supervisorMessage, 64)
	managerEpoch := uint64(0)
	var cancelManager context.CancelFunc
	var managerDone <-chan struct{}
	startManager := func(session ObserverConnection) {
		managerEpoch++
		managerContext, cancel := context.WithCancel(ctx)
		cancelManager = cancel
		done := make(chan struct{})
		managerDone = done
		go s.runObserverManager(managerContext, managerEpoch, session, openObserver, observerConfig, messages, done)
	}
	stopManager := func() {
		if cancelManager == nil {
			return
		}
		cancelManager()
		if managerDone != nil {
			<-managerDone
		}
		cancelManager = nil
		managerDone = nil
		// Messages already buffered by the stopped manager retain its old
		// epoch. Fence them before either starting a replacement manager or
		// entering an embedded attempt with no observer at all.
		managerEpoch++
	}
	defer stopManager()
	if remote {
		startManager(initial)
	}

	restoreRaw, err := s.enterRaw()
	if err != nil {
		return CodexExitResult{}, err
	}
	defer restoreRaw()
	if result, signaled := signalOutcome(); signaled {
		return result, nil
	}

	runPTY := s.RunPTY
	var inputRouter *codexInputRouter
	if runPTY == nil {
		// One reader owns outer stdin for the complete fallback chain. Attempts
		// only attach/detach their current PTY, so a read blocked during a quick
		// failure cannot later deliver bytes to the obsolete PTY or race a
		// second stdin goroutine.
		inputRouter = newCodexInputRouter(s.input())
		defer inputRouter.Stop()
		runPTY = func(runContext context.Context, argv []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
			return s.runPTYAttemptWithRouter(
				runContext, argv, onOSC, nil, inputRouter, signalRouter.out, signalRouter.Termination,
			)
		}
	}
	resume := options.ResumeSession
	oscCounter := uint64(0)

	for {
		if result, signaled := signalOutcome(); signaled {
			return result, nil
		}
		argv := buildCodexTUIArgv(options.CodexPath, socketPath, remote, resume, options.Prompt)
		result, runErr := s.runAttempt(ctx, argv, runPTY, messages, managerEpoch, reducer, publish, &oscCounter)
		if signalResult, signaled := signalOutcome(); signaled {
			if result.Signaled && result.Signal == signalResult.Signal {
				return result, runErr
			}
			return signalResult, nil
		}
		quickFailure := !result.Signaled && result.ExitCode != 0 && result.Elapsed < options.FallbackWindow
		if runErr != nil && !result.Signaled && result.Elapsed < options.FallbackWindow {
			quickFailure = true
		}
		if !quickFailure {
			return result, runErr
		}

		switch {
		case remote && resume != "":
			// The failed resume may have changed the server's loaded set. Stop
			// the current reader, take a new coherent barrier, and construct a
			// new fresh reducer whose immutable baseline includes that set.
			stopManager()
			barrierContext, cancel := context.WithTimeout(ctx, setupTimeout)
			barrier, barrierErr := openObserverUntil(barrierContext, openObserver, observerConfig, defaultCodexSocketPoll, server.Done())
			cancel()
			if signalResult, signaled := signalOutcome(); signaled {
				if barrier != nil {
					barrier.Close()
				}
				return signalResult, nil
			}
			resume = ""
			if barrierErr == nil && barrier != nil {
				reducer, err = newReducer("", barrier.Snapshot(), true)
				if err != nil {
					barrier.Close()
					return result, err
				}
				startManager(barrier)
				continue
			}
			remote = false
			stopServer()
			reducer, err = newReducer("", nil, false)
			if err != nil {
				return result, err
			}

		case remote:
			stopManager()
			publish(reducer.Reduce(ReducerEvent{Kind: EventObserverLost}))
			remote = false
			stopServer()
			resume = ""
			reducer, err = newReducer("", nil, false)
			if err != nil {
				return result, err
			}

		case resume != "":
			resume = ""
			reducer, err = newReducer("", nil, false)
			if err != nil {
				return result, err
			}

		default:
			return result, runErr
		}
	}
}

func validateCodexSupervisorOptions(options CodexSupervisorOptions) error {
	if !filepath.IsAbs(options.CodexPath) {
		return errors.New("Codex executable path must be absolute")
	}
	if err := validateObserverString("Codex executable path", options.CodexPath, MaxReducerCWDBytes, false); err != nil {
		return err
	}
	if options.StateFile == "" || !filepath.IsAbs(options.StateFile) {
		return errors.New("attention state file must be absolute")
	}
	if !codexAttentionGeneration.MatchString(options.Generation) {
		return errors.New("invalid attention generation")
	}
	if filepath.Base(filepath.Dir(filepath.Clean(options.StateFile))) != options.Generation || filepath.Base(options.StateFile) != "state" {
		return errors.New("attention state file does not belong to generation")
	}
	if !filepath.IsAbs(options.ProjectCWD) {
		return errors.New("project cwd must be absolute")
	}
	if err := validateObserverString("project cwd", options.ProjectCWD, MaxReducerCWDBytes, false); err != nil {
		return err
	}
	if err := validateObserverString("client version", options.ClientVersion, MaxReducerIDBytes, false); err != nil {
		return err
	}
	if options.ResumeSession != "" {
		if err := validateCanonicalUUID("resume session", options.ResumeSession); err != nil {
			return err
		}
	}
	if options.FallbackWindow <= 0 {
		return errors.New("fallback window must be positive")
	}
	return nil
}

type supervisorMessageKind uint8

const (
	supervisorObserverEvent supervisorMessageKind = iota + 1
	supervisorObserverLost
	supervisorObserverSnapshot
	supervisorOSC9
)

type supervisorMessage struct {
	epoch    uint64
	kind     supervisorMessageKind
	event    ReducerEvent
	snapshot []Thread
	ack      chan struct{}
}

func (s *CodexSupervisor) runObserverManager(
	ctx context.Context,
	epoch uint64,
	session ObserverConnection,
	open func(context.Context, ObserverConfig) (ObserverConnection, error),
	config ObserverConfig,
	out chan<- supervisorMessage,
	done chan<- struct{},
) {
	defer close(done)
	delay := s.ReconnectDelay
	if delay <= 0 {
		delay = defaultCodexReconnectWait
	}
	current := session
	for {
		for {
			event, err := current.Next(ctx)
			if err != nil {
				current.Close()
				if ctx.Err() != nil {
					return
				}
				select {
				case out <- supervisorMessage{epoch: epoch, kind: supervisorObserverLost}:
				case <-ctx.Done():
					return
				}
				break
			}
			select {
			case out <- supervisorMessage{epoch: epoch, kind: supervisorObserverEvent, event: event}:
			case <-ctx.Done():
				current.Close()
				return
			}
		}

		for {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			}
			next, err := open(ctx, config)
			if err != nil || next == nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			current = next
			select {
			case out <- supervisorMessage{epoch: epoch, kind: supervisorObserverSnapshot, snapshot: next.Snapshot()}:
			case <-ctx.Done():
				next.Close()
				return
			}
			break
		}
	}
}

func (s *CodexSupervisor) runAttempt(
	ctx context.Context,
	argv []string,
	runPTY func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error),
	messages chan supervisorMessage,
	epoch uint64,
	reducer *Reducer,
	publish func(attention.State),
	oscCounter *uint64,
) (CodexExitResult, error) {
	resultCh := make(chan struct {
		result CodexExitResult
		err    error
	}, 1)
	go func() {
		result, err := runPTY(ctx, argv, func(event OSC9Event) {
			// The exact launch overrides admit only agent-turn-complete, while Codex
			// puts its dynamic display text (usually a response preview) in OSC9.
			// The parser already bounds the payload, so provenance—not its text—is
			// the completion discriminator.
			_ = event
			observation := supervisorMessage{epoch: epoch, kind: supervisorOSC9, ack: make(chan struct{})}
			select {
			case messages <- observation:
				select {
				case <-observation.ack:
				case <-ctx.Done():
				}
			case <-ctx.Done():
			}
		})
		resultCh <- struct {
			result CodexExitResult
			err    error
		}{result: result, err: err}
	}()

	for {
		select {
		case message := <-messages:
			if message.epoch != epoch {
				if message.ack != nil {
					close(message.ack)
				}
				continue
			}
			switch message.kind {
			case supervisorObserverLost:
				publish(reducer.Reduce(ReducerEvent{Kind: EventObserverLost}))
			case supervisorObserverSnapshot:
				publish(reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: message.snapshot}))
			case supervisorObserverEvent:
				publish(reducer.Reduce(message.event))
			case supervisorOSC9:
				(*oscCounter)++
				identity := fmt.Sprintf("osc:%d", *oscCounter)
				publish(reducer.Reduce(ReducerEvent{Kind: EventOSC9Completion, Identity: identity}))
				close(message.ack)
			default:
				publish(reducer.Reduce(ReducerEvent{}))
			}

		case outcome := <-resultCh:
			return outcome.result, outcome.err

		case <-ctx.Done():
			outcome := <-resultCh
			return outcome.result, outcome.err
		}
	}
}

func snapshotThreadIDs(snapshot []Thread) []string {
	ids := make([]string, len(snapshot))
	for i := range snapshot {
		ids[i] = snapshot[i].ID
	}
	return ids
}

func openObserverUntil(
	ctx context.Context,
	open func(context.Context, ObserverConfig) (ObserverConnection, error),
	config ObserverConfig,
	delay time.Duration,
	serverDone <-chan error,
) (ObserverConnection, error) {
	var lastErr error
	for {
		select {
		case serverErr := <-serverDone:
			return nil, observerServerExitError(serverErr)
		default:
		}
		connection, err := open(ctx, config)
		if err == nil && connection != nil {
			select {
			case serverErr := <-serverDone:
				connection.Close()
				return nil, observerServerExitError(serverErr)
			default:
				return connection, nil
			}
		}
		if connection != nil {
			connection.Close()
		}
		if err != nil {
			lastErr = err
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case serverErr := <-serverDone:
			if !timer.Stop() {
				<-timer.C
			}
			return nil, observerServerExitError(serverErr)
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if lastErr != nil {
				return nil, fmt.Errorf("initialize Codex observer: %w", lastErr)
			}
			return nil, ctx.Err()
		}
	}
}

func observerServerExitError(serverErr error) error {
	if serverErr == nil {
		serverErr = errors.New("app-server exited during observer setup")
	}
	return fmt.Errorf("initialize Codex observer: %w", serverErr)
}

func makeCodexRuntimeDir(base string) (string, string, error) {
	if base == "" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "wdc.")
	if err != nil {
		return "", "", fmt.Errorf("create Codex runtime directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("chmod Codex runtime directory: %w", err)
	}
	return dir, filepath.Join(dir, "a.sock"), nil
}

func waitForCodexSocket(ctx context.Context, socketPath string, poll time.Duration) error {
	return waitForCodexSocketOrServer(ctx, socketPath, poll, nil)
}

func waitForCodexSocketOrServer(
	ctx context.Context,
	socketPath string,
	poll time.Duration,
	serverDone <-chan error,
) error {
	if poll <= 0 {
		poll = defaultCodexSocketPoll
	}
	for {
		info, err := os.Lstat(socketPath)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
				return errors.New("app-server path is not a Unix socket")
			}
			if err := os.Chmod(socketPath, 0o600); err != nil {
				return fmt.Errorf("chmod app-server socket: %w", err)
			}
			return nil
		case !os.IsNotExist(err):
			return fmt.Errorf("stat app-server socket: %w", err)
		}
		timer := time.NewTimer(poll)
		select {
		case <-timer.C:
		case serverErr := <-serverDone:
			if !timer.Stop() {
				<-timer.C
			}
			if serverErr == nil {
				serverErr = errors.New("app-server exited before creating its socket")
			}
			return fmt.Errorf("wait for app-server socket: %w", serverErr)
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("wait for app-server socket: %w", ctx.Err())
		}
	}
}

func openCodexErrorLog(path string) (*os.File, error) {
	if path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			if chmodErr := file.Chmod(0o600); chmodErr == nil {
				return file, nil
			}
			_ = file.Close()
		}
	}
	file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open Codex app-server log sink: %w", err)
	}
	return file, nil
}

type execAppServer struct {
	cmd   *exec.Cmd
	done  chan error
	grace time.Duration
	once  sync.Once
}

func startDefaultAppServer(ctx context.Context, argv []string, log io.Writer, grace time.Duration) (AppServerProcess, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("app-server command is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = devNull
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	_ = devNull.Close()
	process := &execAppServer{cmd: cmd, done: make(chan error, 1), grace: grace}
	go func() {
		process.done <- cmd.Wait()
		close(process.done)
	}()
	return process, nil
}

func (p *execAppServer) Done() <-chan error { return p.done }

func (p *execAppServer) Stop(ctx context.Context) error {
	var stopErr error
	p.once.Do(func() {
		pid := 0
		if p.cmd.Process != nil {
			pid = p.cmd.Process.Pid
			_ = syscall.Kill(-pid, syscall.SIGTERM)
		}
		done := (<-chan error)(p.done)
		leaderDone := false
		select {
		case <-done:
			leaderDone = true
			done = nil
		default:
		}
		if leaderDone && !processGroupExists(pid) {
			return
		}
		grace := p.grace
		if grace <= 0 {
			grace = defaultCodexServerGrace
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		for {
			select {
			case <-done:
				leaderDone = true
				done = nil
				if !processGroupExists(pid) {
					return
				}
			case <-timer.C:
				if pid != 0 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				if !leaderDone {
					select {
					case <-done:
					case <-ctx.Done():
						stopErr = ctx.Err()
					}
				}
				return
			case <-ctx.Done():
				stopErr = ctx.Err()
				if pid != 0 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				return
			}
		}
	})
	return stopErr
}

func processGroupExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (s *CodexSupervisor) enterRaw() (func(), error) {
	if s.EnterRaw != nil {
		return s.EnterRaw()
	}
	file, ok := s.input().(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(file.Fd())
	if err != nil {
		return nil, fmt.Errorf("enter terminal raw mode: %w", err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = term.Restore(file.Fd(), state) }) }, nil
}

func (s *CodexSupervisor) input() io.Reader {
	if s.Stdin != nil {
		return s.Stdin
	}
	return os.Stdin
}

func (s *CodexSupervisor) output() io.Writer {
	if s.Stdout != nil {
		return s.Stdout
	}
	return os.Stdout
}

// codexInputRouter is the sole reader of outer stdin for an entire fallback
// chain. A completed attempt detaches its PTY; a read that was already blocked
// then waits for the next attachment and is delivered there without loss.
type codexInputRouter struct {
	reader io.Reader

	mu      sync.Mutex
	changed *sync.Cond
	target  *os.File
	stopped bool
}

func newCodexInputRouter(reader io.Reader) *codexInputRouter {
	router := &codexInputRouter{reader: reader}
	router.changed = sync.NewCond(&router.mu)
	go router.run()
	return router
}

func (r *codexInputRouter) Attach(target *os.File) {
	r.mu.Lock()
	if !r.stopped {
		r.target = target
		r.changed.Broadcast()
	}
	r.mu.Unlock()
}

func (r *codexInputRouter) Detach(target *os.File) {
	r.mu.Lock()
	if r.target == target {
		r.target = nil
	}
	r.mu.Unlock()
}

func (r *codexInputRouter) Stop() {
	r.mu.Lock()
	r.stopped = true
	r.target = nil
	r.changed.Broadcast()
	r.mu.Unlock()
}

func (r *codexInputRouter) run() {
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := r.reader.Read(buffer)
		if n > 0 && !r.route(append([]byte(nil), buffer[:n]...)) {
			return
		}
		if readErr != nil {
			return
		}
	}
}

func (r *codexInputRouter) route(data []byte) bool {
	for len(data) > 0 {
		r.mu.Lock()
		for r.target == nil && !r.stopped {
			r.changed.Wait()
		}
		if r.stopped {
			r.mu.Unlock()
			return false
		}
		target := r.target
		r.mu.Unlock()

		n, err := target.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil || n == 0 {
			r.mu.Lock()
			if r.target == target {
				r.target = nil
			}
			r.mu.Unlock()
		}
	}
	return true
}

func (s *CodexSupervisor) runPTYAttempt(
	ctx context.Context,
	argv []string,
	onOSC func(OSC9Event),
	extraEnv []string,
) (CodexExitResult, error) {
	router := newCodexInputRouter(s.input())
	defer router.Stop()
	return s.runPTYAttemptWithRouter(ctx, argv, onOSC, extraEnv, router, s.Signals, nil)
}

func (s *CodexSupervisor) runPTYAttemptWithRouter(
	ctx context.Context,
	argv []string,
	onOSC func(OSC9Event),
	extraEnv []string,
	inputRouter *codexInputRouter,
	signalSource <-chan os.Signal,
	terminationSignal func() syscall.Signal,
) (CodexExitResult, error) {
	startedAt := time.Now()
	if len(argv) == 0 || argv[0] == "" {
		return CodexExitResult{}, errors.New("Codex TUI command is empty")
	}
	if err := ctx.Err(); err != nil {
		return CodexExitResult{}, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), extraEnv...)

	initialSize, _ := s.terminalSize()
	var ptmx *os.File
	var err error
	if initialSize != nil {
		ptmx, err = pty.StartWithSize(cmd, initialSize)
	} else {
		ptmx, err = pty.Start(cmd)
	}
	if err != nil {
		return CodexExitResult{ExitCode: 1, Elapsed: time.Since(startedAt)}, fmt.Errorf("start Codex TUI: %w", err)
	}
	defer ptmx.Close()
	inputRouter.Attach(ptmx)
	var detachInputOnce sync.Once
	detachInput := func() { detachInputOnce.Do(func() { inputRouter.Detach(ptmx) }) }
	defer detachInput()

	waitCh := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		detachInput()
		waitCh <- waitErr
	}()
	outputCh := make(chan error, 1)
	go func() {
		var filter OSC9Filter
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := ptmx.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				filtered, events := filter.Feed(chunk)
				for _, event := range events {
					if onOSC != nil {
						onOSC(event)
					}
				}
				if len(filtered) > 0 {
					if err := writeFull(s.output(), filtered); err != nil {
						outputCh <- err
						return
					}
				}
			}
			if readErr != nil {
				if tail := filter.Flush(); len(tail) > 0 {
					if err := writeFull(s.output(), tail); err != nil {
						outputCh <- err
						return
					}
				}
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, syscall.EIO) {
					outputCh <- nil
				} else {
					outputCh <- readErr
				}
				return
			}
		}
	}()
	signals := signalSource
	var ownedSignals chan os.Signal
	if signals == nil {
		ownedSignals = make(chan os.Signal, 8)
		signal.Notify(ownedSignals, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		defer signal.Stop(ownedSignals)
		signals = ownedSignals
	}

	var waitErr error
	waited := false
	outputDone := false
	for !waited || !outputDone {
		select {
		case waitErr = <-waitCh:
			waited = true
			waitCh = nil
			detachInput()
		case outputErr := <-outputCh:
			outputDone = true
			outputCh = nil
			if outputErr != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				if !waited {
					waitErr = <-waitCh
					waited = true
					detachInput()
				}
				return codexExitFromWait(waitErr, time.Since(startedAt)), fmt.Errorf("forward Codex PTY output: %w", outputErr)
			}
		case received, ok := <-signals:
			if !ok {
				signals = nil
				continue
			}
			sig, ok := received.(syscall.Signal)
			if !ok {
				continue
			}
			if sig == syscall.SIGWINCH {
				if size, sizeErr := s.terminalSize(); sizeErr == nil && size != nil {
					_ = pty.Setsize(ptmx, size)
				}
				continue
			}
			if forwardedCodexSignal(sig) {
				detachInput()
				return s.terminatePTY(cmd, ptmx, waitCh, outputCh, sig, startedAt)
			}
		case <-ctx.Done():
			sig := syscall.SIGTERM
			if terminationSignal != nil {
				if terminatedBy := terminationSignal(); terminatedBy != 0 {
					sig = terminatedBy
				}
			}
			detachInput()
			return s.terminatePTY(cmd, ptmx, waitCh, outputCh, sig, startedAt)
		}
	}
	return codexExitFromWait(waitErr, time.Since(startedAt)), nil
}

func (s *CodexSupervisor) terminalSize() (*pty.Winsize, error) {
	if s.TerminalSize != nil {
		return s.TerminalSize()
	}
	file, ok := s.input().(*os.File)
	if !ok {
		return nil, nil
	}
	return pty.GetsizeFull(file)
}

func forwardedCodexSignal(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT:
		return true
	default:
		return false
	}
}

func (s *CodexSupervisor) terminatePTY(
	cmd *exec.Cmd,
	ptmx *os.File,
	waitCh <-chan error,
	outputCh <-chan error,
	sig syscall.Signal,
	startedAt time.Time,
) (CodexExitResult, error) {
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
		_ = syscall.Kill(-pid, sig)
	}
	grace := s.PTYGrace
	if grace <= 0 {
		grace = defaultCodexPTYGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	leaderDone := waitCh == nil
	if !leaderDone || processGroupExists(pid) {
		for {
			select {
			case <-waitCh:
				leaderDone = true
				waitCh = nil
				if !processGroupExists(pid) {
					goto groupStopped
				}
			case <-timer.C:
				if pid != 0 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				if !leaderDone {
					<-waitCh
				}
				goto groupStopped
			}
		}
	}

groupStopped:
	_ = ptmx.Close()
	if outputCh != nil {
		select {
		case <-outputCh:
		case <-time.After(grace):
		}
	}
	return CodexExitResult{
		ExitCode: 128 + int(sig), Signaled: true, Signal: sig, Elapsed: time.Since(startedAt),
	}, nil
}

func codexExitFromWait(waitErr error, elapsed time.Duration) CodexExitResult {
	result := CodexExitResult{Elapsed: elapsed}
	if waitErr == nil {
		return result
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		result.ExitCode = exitError.ExitCode()
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.Signaled = true
			result.Signal = status.Signal()
			result.ExitCode = 128 + int(result.Signal)
		}
		return result
	}
	result.ExitCode = 1
	return result
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
