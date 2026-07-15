package opencodeadapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"

	"github.com/jackuait/wisp-deck/internal/attention"
	"github.com/jackuait/wisp-deck/internal/terminalcontrol"
)

const (
	// The cached npx launcher takes 6-13 seconds on a warm machine before the
	// OpenCode HTTP listener exists; keep this above that measured boundary.
	defaultSetupTimeout = 20 * time.Second
	defaultRetryDelay   = 25 * time.Millisecond
	defaultProcessGrace = 300 * time.Millisecond
	defaultPromptDelay  = 150 * time.Millisecond
	maxPromptBytes      = 64 * 1024
)

type SupervisorOptions struct {
	Prefix     []string
	StateFile  string
	Generation string
	ProjectDir string
	Continue   bool
	Session    string
	Prompt     string
	ErrorLog   string
}

type ExitResult struct {
	ExitCode int
	Signaled bool
	Signal   syscall.Signal
	Elapsed  time.Duration
}

type ManagedProcess interface {
	Done() <-chan error
	Stop(context.Context) error
}

type processSpec struct {
	Argv []string
	Env  []string
	CWD  string
	Log  io.Writer
}

type ptySpec struct {
	Argv   []string
	Env    []string
	CWD    string
	Stdin  io.Reader
	Stdout io.Writer
}

type Supervisor struct {
	Stdin  io.Reader
	Stdout io.Writer

	SetupTimeout time.Duration
	RetryDelay   time.Duration
	ProcessGrace time.Duration
	PromptDelay  time.Duration

	SelectPort   func() (int, error)
	RandomSecret func() (string, error)
	StartServer  func(context.Context, processSpec) (ManagedProcess, error)
	Observe      func(context.Context, ObserverOptions) error
	RunPTY       func(context.Context, ptySpec, func()) (ExitResult, error)
	PostPrompt   func(context.Context, string, string, string, string) error
}

func (s *Supervisor) Run(ctx context.Context, options SupervisorOptions) (ExitResult, error) {
	if err := validateSupervisorOptions(options); err != nil {
		return ExitResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExitResult{}, err
	}
	writer, err := attention.NewAtomicWriter(options.StateFile, options.Generation)
	if err != nil {
		return ExitResult{}, fmt.Errorf("open OpenCode attention state: %w", err)
	}
	if err := writer.Publish(attention.PhaseUnknown, attention.ReasonNone, ""); err != nil {
		return ExitResult{}, fmt.Errorf("initialize OpenCode attention state: %w", err)
	}
	configPath, err := WriteSilentTUIConfig(filepath.Dir(options.StateFile))
	if err != nil {
		return ExitResult{}, err
	}

	selectPort := s.SelectPort
	if selectPort == nil {
		selectPort = selectLoopbackPort
	}
	port, err := selectPort()
	if err != nil {
		return ExitResult{}, fmt.Errorf("select OpenCode loopback port: %w", err)
	}
	secretSource := s.RandomSecret
	if secretSource == nil {
		secretSource = randomSecret
	}
	password, err := secretSource()
	if err != nil || password == "" || strings.ContainsAny(password, "\x00\r\n") {
		if err == nil {
			err = errors.New("secret source returned an invalid value")
		}
		return ExitResult{}, fmt.Errorf("create OpenCode server secret: %w", err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	serverArgv, err := BuildServerArgv(options.Prefix, port)
	if err != nil {
		return ExitResult{}, err
	}
	attachArgv, err := BuildAttachArgv(options.Prefix, AttachOptions{
		URL: baseURL, ProjectDir: options.ProjectDir, Password: password,
		Continue: options.Continue, Session: options.Session,
	})
	if err != nil {
		return ExitResult{}, err
	}

	log, err := openOpenCodeErrorLog(options.ErrorLog)
	if err != nil {
		return ExitResult{}, err
	}
	defer log.Close()
	startServer := s.StartServer
	if startServer == nil {
		startServer = func(_ context.Context, spec processSpec) (ManagedProcess, error) {
			return startManagedProcess(spec, effectiveGrace(s.ProcessGrace))
		}
	}
	server, err := startServer(ctx, processSpec{
		Argv: serverArgv,
		Env:  replaceEnvironment(os.Environ(), map[string]string{"OPENCODE_SERVER_PASSWORD": password}, "OPENCODE_TUI_CONFIG"),
		CWD:  options.ProjectDir,
		Log:  log,
	})
	if err != nil || server == nil {
		if err == nil {
			err = errors.New("server starter returned no process")
		}
		return ExitResult{}, fmt.Errorf("start OpenCode server: %w", err)
	}
	defer func() {
		stopContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), effectiveGrace(s.ProcessGrace)+time.Second)
		defer cancel()
		_ = server.Stop(stopContext)
	}()

	monitorContext, cancelMonitor := context.WithCancel(ctx)
	defer cancelMonitor()
	observe := s.Observe
	if observe == nil {
		observe = ObserveEvents
	}
	observerDone, err := s.waitForObserver(monitorContext, server, observe, ObserverOptions{
		BaseURL: baseURL, Password: password, Directory: options.ProjectDir,
		Publisher: writer,
	}, options.Generation)
	if err != nil {
		return ExitResult{}, err
	}
	go func() {
		if observerErr := <-observerDone; observerErr != nil && monitorContext.Err() == nil {
			_ = writer.Publish(attention.PhaseUnknown, attention.ReasonNone, "")
		}
	}()

	runPTY := s.RunPTY
	if runPTY == nil {
		runPTY = s.runDefaultPTY
	}
	launchContext, cancelLaunch := context.WithCancel(ctx)
	defer cancelLaunch()
	started := make(chan struct{})
	var startedOnce sync.Once
	outcome := make(chan struct {
		result ExitResult
		err    error
	}, 1)
	go func() {
		result, runErr := runPTY(launchContext, ptySpec{
			Argv: attachArgv,
			Env:  replaceEnvironment(os.Environ(), map[string]string{"OPENCODE_TUI_CONFIG": configPath}, "OPENCODE_SERVER_PASSWORD"),
			CWD:  options.ProjectDir, Stdin: s.input(), Stdout: s.output(),
		}, func() { startedOnce.Do(func() { close(started) }) })
		outcome <- struct {
			result ExitResult
			err    error
		}{result: result, err: runErr}
	}()

	select {
	case <-started:
	case exited := <-outcome:
		if exited.err == nil {
			exited.err = errors.New("OpenCode attach exited before PTY readiness")
		}
		return exited.result, exited.err
	case serverErr := <-server.Done():
		cancelLaunch()
		return waitOutcome(outcome, fmt.Errorf("OpenCode server exited before attach readiness: %w", normalizeExitError(serverErr)))
	case <-ctx.Done():
		cancelLaunch()
		return waitOutcome(outcome, ctx.Err())
	}

	if options.Prompt != "" {
		delay := s.PromptDelay
		if delay <= 0 {
			delay = defaultPromptDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case exited := <-outcome:
			if !timer.Stop() {
				<-timer.C
			}
			if exited.err == nil {
				exited.err = errors.New("OpenCode attach exited before prompt handoff")
			}
			return exited.result, exited.err
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			cancelLaunch()
			return waitOutcome(outcome, ctx.Err())
		}
		postPrompt := s.PostPrompt
		if postPrompt == nil {
			postPrompt = postOpenCodePrompt
		}
		if err := postPrompt(launchContext, baseURL, options.ProjectDir, password, options.Prompt); err != nil {
			cancelLaunch()
			return waitOutcome(outcome, fmt.Errorf("handoff OpenCode prompt: %w", err))
		}
	}

	select {
	case exited := <-outcome:
		return exited.result, exited.err
	case serverErr := <-server.Done():
		cancelLaunch()
		return waitOutcome(outcome, fmt.Errorf("OpenCode server exited during attach: %w", normalizeExitError(serverErr)))
	case <-ctx.Done():
		cancelLaunch()
		return waitOutcome(outcome, ctx.Err())
	}
}

func (s *Supervisor) waitForObserver(
	ctx context.Context,
	server ManagedProcess,
	observe func(context.Context, ObserverOptions) error,
	base ObserverOptions,
	generation string,
) (<-chan error, error) {
	timeout := s.SetupTimeout
	if timeout <= 0 {
		timeout = defaultSetupTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	retry := s.RetryDelay
	if retry <= 0 {
		retry = defaultRetryDelay
	}
	var lastErr error
	for {
		reducer, err := NewReducer(generation)
		if err != nil {
			return nil, err
		}
		attemptContext, cancelAttempt := context.WithCancel(ctx)
		ready := make(chan struct{})
		var readyOnce sync.Once
		attempt := make(chan error, 1)
		current := base
		current.Reducer = reducer
		current.OnReady = func() { readyOnce.Do(func() { close(ready) }) }
		go func() { attempt <- observe(attemptContext, current) }()

		select {
		case <-ready:
			select {
			case lastErr = <-attempt:
				cancelAttempt()
			case <-time.After(time.Millisecond):
				return attempt, nil
			}
		case lastErr = <-attempt:
			cancelAttempt()
		case serverErr := <-server.Done():
			cancelAttempt()
			return nil, fmt.Errorf("OpenCode server exited during observer setup: %w", normalizeExitError(serverErr))
		case <-deadline.C:
			cancelAttempt()
			if lastErr != nil {
				return nil, fmt.Errorf("initialize OpenCode observer: %w", lastErr)
			}
			return nil, errors.New("initialize OpenCode observer: setup deadline exceeded")
		case <-ctx.Done():
			cancelAttempt()
			return nil, fmt.Errorf("initialize OpenCode observer: %w", ctx.Err())
		}

		timer := time.NewTimer(retry)
		select {
		case <-timer.C:
		case serverErr := <-server.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("OpenCode server exited during observer setup: %w", normalizeExitError(serverErr))
		case <-deadline.C:
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("initialize OpenCode observer: %w", lastErr)
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("initialize OpenCode observer: %w", ctx.Err())
		}
	}
}

func validateSupervisorOptions(options SupervisorOptions) error {
	if err := validatePrefix(options.Prefix); err != nil {
		return err
	}
	if options.StateFile == "" || !filepath.IsAbs(options.StateFile) || filepath.Clean(options.StateFile) != options.StateFile {
		return errors.New("OpenCode attention state file must be a clean absolute path")
	}
	if !generationName.MatchString(options.Generation) || filepath.Base(filepath.Dir(options.StateFile)) != options.Generation || filepath.Base(options.StateFile) != "state" {
		return errors.New("OpenCode attention state file does not belong to generation")
	}
	if options.ProjectDir == "" || !filepath.IsAbs(options.ProjectDir) || filepath.Clean(options.ProjectDir) != options.ProjectDir || strings.ContainsAny(options.ProjectDir, "\x00\r\n") {
		return errors.New("OpenCode project directory must be a clean absolute path")
	}
	if options.Continue && options.Session != "" {
		return errors.New("OpenCode continue and exact session are mutually exclusive")
	}
	if options.Session != "" && (!identifier(options.Session) || len(options.Session) > MaxIdentifierBytes) {
		return errors.New("invalid OpenCode session identifier")
	}
	if !utf8.ValidString(options.Prompt) || len(options.Prompt) > maxPromptBytes || strings.ContainsRune(options.Prompt, '\x00') {
		return errors.New("invalid OpenCode prompt")
	}
	return nil
}

func (s *Supervisor) input() io.Reader {
	if s.Stdin != nil {
		return s.Stdin
	}
	return os.Stdin
}

func (s *Supervisor) output() io.Writer {
	if s.Stdout != nil {
		return s.Stdout
	}
	return os.Stdout
}

func effectiveGrace(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultProcessGrace
	}
	return value
}

func selectLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func replaceEnvironment(environment []string, values map[string]string, remove ...string) []string {
	blocked := make(map[string]bool, len(values)+len(remove))
	for key := range values {
		blocked[key] = true
	}
	for _, key := range remove {
		blocked[key] = true
	}
	result := make([]string, 0, len(environment)+len(values))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && blocked[key] {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func openOpenCodeErrorLog(path string) (io.WriteCloser, error) {
	if path == "" {
		path = os.Getenv("WISP_DECK_ERROR_LOG")
	}
	if path == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open OpenCode error log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("protect OpenCode error log: %w", err)
	}
	return file, nil
}

func normalizeExitError(err error) error {
	if err == nil {
		return errors.New("process exited")
	}
	return err
}

func waitOutcome(outcome <-chan struct {
	result ExitResult
	err    error
}, preferred error) (ExitResult, error) {
	exited := <-outcome
	if exited.err == nil {
		exited.err = preferred
	}
	return exited.result, exited.err
}

type execManagedProcess struct {
	cmd   *exec.Cmd
	done  chan error
	grace time.Duration
	once  sync.Once
}

func startManagedProcess(spec processSpec, grace time.Duration) (ManagedProcess, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("OpenCode server command is empty")
	}
	command := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	command.Env, command.Dir = spec.Env, spec.CWD
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdin = nil
	command.Stdout, command.Stderr = spec.Log, spec.Log
	if err := command.Start(); err != nil {
		return nil, err
	}
	process := &execManagedProcess{cmd: command, done: make(chan error, 1), grace: grace}
	go func() {
		process.done <- command.Wait()
		close(process.done)
	}()
	return process, nil
}

func (p *execManagedProcess) Done() <-chan error { return p.done }

func (p *execManagedProcess) Stop(ctx context.Context) error {
	var result error
	p.once.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(p.grace)
		defer timer.Stop()
		select {
		case result = <-p.done:
		case <-timer.C:
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			result = <-p.done
		case <-ctx.Done():
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			result = ctx.Err()
		}
	})
	return result
}

func (s *Supervisor) runDefaultPTY(ctx context.Context, spec ptySpec, onStarted func()) (ExitResult, error) {
	startedAt := time.Now()
	if len(spec.Argv) == 0 {
		return ExitResult{}, errors.New("OpenCode attach command is empty")
	}
	command := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	command.Env, command.Dir = spec.Env, spec.CWD
	terminal, err := pty.Start(command)
	if err != nil {
		return ExitResult{}, fmt.Errorf("start OpenCode attach PTY: %w", err)
	}
	defer terminal.Close()
	if onStarted != nil {
		onStarted()
	}

	var restore func()
	if inputFile, ok := spec.Stdin.(*os.File); ok && term.IsTerminal(inputFile.Fd()) {
		state, rawErr := term.MakeRaw(inputFile.Fd())
		if rawErr == nil {
			restore = func() { _ = term.Restore(inputFile.Fd(), state) }
			defer restore()
		}
	}
	if inputFile, ok := spec.Stdin.(*os.File); ok {
		if outputFile, ok := spec.Stdout.(*os.File); ok {
			_ = pty.InheritSize(outputFile, terminal)
		}
		go func() { _, _ = io.Copy(terminal, inputFile) }()
	} else if spec.Stdin != nil {
		go func() { _, _ = io.Copy(terminal, spec.Stdin) }()
	}

	outputDone := make(chan error, 1)
	go func() { outputDone <- pumpFilteredOutput(terminal, spec.Stdout) }()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
	defer signal.Stop(signals)

	var waitErr error
	var terminating syscall.Signal
	for waitErr == nil {
		select {
		case waitErr = <-waitDone:
			if waitErr == nil {
				waitErr = errProcessComplete
			}
		case received := <-signals:
			sig, ok := received.(syscall.Signal)
			if !ok {
				continue
			}
			if sig == syscall.SIGWINCH {
				if outputFile, ok := spec.Stdout.(*os.File); ok {
					_ = pty.InheritSize(outputFile, terminal)
				}
				continue
			}
			terminating = sig
			_ = syscall.Kill(-command.Process.Pid, sig)
		case <-ctx.Done():
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(effectiveGrace(s.ProcessGrace))
			select {
			case waitErr = <-waitDone:
				if waitErr == nil {
					waitErr = errProcessComplete
				}
			case <-timer.C:
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				waitErr = <-waitDone
				if waitErr == nil {
					waitErr = errProcessComplete
				}
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
	_ = terminal.Close()
	outputErr := <-outputDone
	if errors.Is(waitErr, errProcessComplete) {
		waitErr = nil
	}
	result := exitResult(command.ProcessState, time.Since(startedAt))
	if terminating != 0 {
		result.Signaled, result.Signal, result.ExitCode = true, terminating, 128+int(terminating)
	}
	if ctx.Err() != nil && waitErr == nil {
		waitErr = ctx.Err()
	}
	if waitErr == nil && outputErr != nil {
		waitErr = outputErr
	}
	return result, waitErr
}

var errProcessComplete = errors.New("process complete")

func exitResult(state *os.ProcessState, elapsed time.Duration) ExitResult {
	result := ExitResult{Elapsed: elapsed}
	if state == nil {
		return result
	}
	result.ExitCode = state.ExitCode()
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.Signaled, result.Signal = true, status.Signal()
		result.ExitCode = 128 + int(result.Signal)
	}
	return result
}

func pumpFilteredOutput(input io.Reader, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	var filter terminalcontrol.Filter
	buffer := make([]byte, 32*1024)
	for {
		count, readErr := input.Read(buffer)
		if count > 0 {
			filtered, _ := filter.Feed(buffer[:count])
			if err := writeAll(output, filtered); err != nil {
				return err
			}
		}
		if readErr != nil {
			if remaining := filter.Flush(); len(remaining) > 0 {
				if err := writeAll(output, remaining); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, syscall.EIO) {
				return nil
			}
			return readErr
		}
	}
}

func writeAll(output io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := output.Write(data)
		if err != nil {
			return err
		}
		if count <= 0 || count > len(data) {
			return io.ErrShortWrite
		}
		data = data[count:]
	}
	return nil
}

func postOpenCodePrompt(ctx context.Context, baseURL, directory, password, prompt string) error {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ResponseHeaderTimeout: 3 * time.Second, DisableCompression: true,
		MaxResponseHeaderBytes: 32 * 1024,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("OpenCode prompt redirect rejected")
	}}
	appendBody, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: prompt})
	if err != nil {
		return err
	}
	for _, request := range []struct {
		path string
		body []byte
	}{{path: "/tui/append-prompt", body: appendBody}, {path: "/tui/submit-prompt", body: []byte("{}")}} {
		endpoint := baseURL + request.path + "?directory=" + url.QueryEscape(directory)
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(request.body))
		if err != nil {
			return err
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		httpRequest.Header.Set("Accept", "application/json")
		httpRequest.SetBasicAuth("opencode", password)
		response, err := client.Do(httpRequest)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode != http.StatusOK || len(body) > 4096 || string(bytes.TrimSpace(body)) != "true" {
			return fmt.Errorf("OpenCode prompt endpoint %s returned status %d body %q", request.path, response.StatusCode, body)
		}
	}
	return nil
}
