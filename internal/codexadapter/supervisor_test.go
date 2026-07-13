package codexadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/jackuait/wisp-deck/internal/attention"
)

const (
	supervisorGeneration = "generation.Task8"
	supervisorResumeID   = "11111111-1111-4111-8111-111111111111"
)

func TestCodexArgvIsExactAndAppliesAllNotificationOverrides(t *testing.T) {
	socket := "/tmp/wdc.test/a.sock"
	uri := "unix://" + socket
	configs := []string{
		`tui.notifications=["agent-turn-complete"]`,
		`tui.notification_method="osc9"`,
		`tui.notification_condition="always"`,
	}
	withConfigs := func(prefix []string) []string {
		result := append([]string(nil), prefix...)
		for _, value := range configs {
			result = append(result, "-c", value)
		}
		return result
	}

	if got, want := buildCodexServerArgv("/opt/codex", socket), []string{
		"/opt/codex", "app-server", "--listen", uri,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("server argv = %#v, want %#v", got, want)
	}

	tests := []struct {
		name   string
		remote bool
		resume string
		prompt string
		want   []string
	}{
		{
			name: "remote fresh hostile prompt", remote: true, prompt: "--hostile prompt with spaces",
			want: append(withConfigs([]string{"/opt/codex", "--remote", uri}), "--", "--hostile prompt with spaces"),
		},
		{
			name:   "remote resume",
			remote: true, resume: supervisorResumeID, prompt: "continue exactly",
			want: append(withConfigs([]string{"/opt/codex", "resume", "--remote", uri}), "--", supervisorResumeID, "continue exactly"),
		},
		{
			name:   "embedded fresh",
			prompt: "seed",
			want:   append(withConfigs([]string{"/opt/codex"}), "--", "seed"),
		},
		{
			name:   "embedded resume hostile prompt",
			resume: supervisorResumeID, prompt: "-not-a-flag",
			want: append(withConfigs([]string{"/opt/codex", "resume"}), "--", supervisorResumeID, "-not-a-flag"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildCodexTUIArgv("/opt/codex", socket, test.remote, test.resume, test.prompt)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("argv = %#v, want %#v", got, test.want)
			}
		})
	}
}

type fakeObserverSession struct {
	snapshot       []Thread
	nilSnapshotRaw bool
	next           chan observerNext
	closed         chan struct{}
	once           sync.Once
}

type observerNext struct {
	event ReducerEvent
	err   error
}

type burstSnapshotObserver struct {
	snapshot []Thread
	limit    int
	count    int
	produced chan struct{}
	closed   chan struct{}
	once     sync.Once
}

func newBurstSnapshotObserver(limit int, thread Thread) *burstSnapshotObserver {
	return &burstSnapshotObserver{
		snapshot: []Thread{cloneObservedThread(thread)},
		limit:    limit,
		produced: make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (s *burstSnapshotObserver) Snapshot() []Thread { return cloneThreads(s.snapshot) }

func (s *burstSnapshotObserver) Next(ctx context.Context) (ReducerEvent, error) {
	if s.count < s.limit {
		s.count++
		thread := cloneObservedThread(s.snapshot[0])
		if s.count%2 == 0 {
			thread.Status = ThreadStatus{Type: ThreadStatusIdle}
		}
		if s.count == s.limit {
			close(s.produced)
		}
		return ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{thread}}, nil
	}
	select {
	case <-ctx.Done():
		return ReducerEvent{}, ctx.Err()
	case <-s.closed:
		return ReducerEvent{}, errors.New("observer closed")
	}
}

func (s *burstSnapshotObserver) Close() { s.once.Do(func() { close(s.closed) }) }

func newFakeObserverSession(snapshot ...Thread) *fakeObserverSession {
	return &fakeObserverSession{
		snapshot: cloneThreads(snapshot), next: make(chan observerNext, 16), closed: make(chan struct{}),
	}
}

func (s *fakeObserverSession) Snapshot() []Thread {
	if s.nilSnapshotRaw {
		return nil
	}
	return cloneThreads(s.snapshot)
}

func (s *fakeObserverSession) Next(ctx context.Context) (ReducerEvent, error) {
	select {
	case item := <-s.next:
		return item.event, item.err
	case <-s.closed:
		return ReducerEvent{}, errors.New("observer closed")
	case <-ctx.Done():
		return ReducerEvent{}, ctx.Err()
	}
}

func (s *fakeObserverSession) Close() { s.once.Do(func() { close(s.closed) }) }

type fakeServerProcess struct {
	mu      sync.Mutex
	stopped bool
	done    chan error
}

func (p *fakeServerProcess) Stop(context.Context) error {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	return nil
}

func (p *fakeServerProcess) Done() <-chan error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done == nil {
		p.done = make(chan error)
	}
	return p.done
}

func (p *fakeServerProcess) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

func supervisorOptions(t *testing.T, resume, prompt string) CodexSupervisorOptions {
	t.Helper()
	dir := filepath.Join(t.TempDir(), supervisorGeneration)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return CodexSupervisorOptions{
		CodexPath:      "/opt/codex",
		StateFile:      filepath.Join(dir, "state"),
		Generation:     supervisorGeneration,
		ProjectCWD:     "/repo",
		ClientVersion:  "test-version",
		ResumeSession:  resume,
		FallbackWindow: 10 * time.Second,
		Prompt:         prompt,
	}
}

func TestCodexSupervisorRemoteResumeQuickFailureTakesNewSnapshotThenRemoteFresh(t *testing.T) {
	firstObserver := newFakeObserverSession()
	secondObserver := newFakeObserverSession(testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive))
	observers := []ObserverConnection{firstObserver, secondObserver}
	var openCalls atomic.Int32
	server := &fakeServerProcess{}
	var launches [][]string
	rawBegins, rawRestores := 0, 0
	options := supervisorOptions(t, supervisorResumeID, "--handoff")

	supervisor := CodexSupervisor{
		TempBase: t.TempDir(),
		StartServer: func(_ context.Context, argv []string, _ io.Writer) (AppServerProcess, error) {
			if len(argv) != 4 || argv[1] != "app-server" {
				t.Fatalf("server argv = %#v", argv)
			}
			return server, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			call := int(openCalls.Add(1))
			if call > len(observers) {
				return nil, errors.New("unexpected extra observer open")
			}
			observer := observers[call-1]
			return observer, nil
		},
		EnterRaw: func() (func(), error) {
			rawBegins++
			return func() { rawRestores++ }, nil
		},
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			launches = append(launches, append([]string(nil), argv...))
			if len(launches) == 1 {
				return CodexExitResult{ExitCode: 9, Elapsed: time.Second}, nil
			}
			if server.isStopped() {
				t.Fatal("server stopped before remote fresh attempt")
			}
			if state := readSupervisorState(t, options.StateFile); state.Phase != attention.PhaseUnknown {
				t.Fatalf("fresh barrier state = %+v, want old active resume thread frozen in baseline", state)
			}
			newRoot := testSupervisorThread("22222222-2222-4222-8222-222222222222", "fresh-session", "", ThreadStatusActive)
			secondObserver.next <- observerNext{event: ReducerEvent{Kind: EventThreadObserved, Thread: newRoot}}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if state := readSupervisorState(t, options.StateFile); state.Phase == attention.PhaseWorking {
					return CodexExitResult{ExitCode: 0, Elapsed: 11 * time.Second}, nil
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("new top-level thread after fresh barrier was not correlated")
			return CodexExitResult{ExitCode: 0, Elapsed: 11 * time.Second}, nil
		},
	}

	result, err := supervisor.Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || len(launches) != 2 {
		t.Fatalf("result=%+v launches=%#v", result, launches)
	}
	if got := openCalls.Load(); got != 2 {
		t.Fatalf("observer opens = %d, want initial plus fresh barrier", got)
	}
	if !server.isStopped() {
		t.Fatal("server was not cleaned up after final remote attempt")
	}
	if !containsArgSequence(launches[0], "resume", "--remote") || containsArg(launches[1], "resume") || !containsArg(launches[1], "--remote") {
		t.Fatalf("fallback launches = %#v", launches)
	}
	if rawBegins != 1 || rawRestores != 1 {
		t.Fatalf("raw lifecycle = begin %d restore %d, want once/once", rawBegins, rawRestores)
	}
}

func TestCodexSupervisorDefaultServerEarlyExitStartsEmbeddedPromptly(t *testing.T) {
	dir := t.TempDir()
	fakeCodex := filepath.Join(dir, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexit 17\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	options := supervisorOptions(t, "", "")
	options.CodexPath = fakeCodex
	started := time.Now()
	var launch []string
	supervisor := CodexSupervisor{
		TempBase:     t.TempDir(),
		SetupTimeout: 3 * time.Second,
		EnterRaw:     func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			launch = append([]string(nil), argv...)
			return CodexExitResult{}, nil
		},
	}
	result, err := supervisor.Run(context.Background(), options)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("embedded launch waited %v for a setup timeout after app-server exit", elapsed)
	}
	if containsArg(launch, "--remote") {
		t.Fatalf("early-exit fallback launch was remote: %#v", launch)
	}
}

func TestCodexSupervisorServerExitDuringObserverSetupSkipsSetupTimeout(t *testing.T) {
	server := &fakeServerProcess{done: make(chan error, 1)}
	serverExit := errors.New("app-server exited during observer setup")
	var once sync.Once
	options := supervisorOptions(t, "", "")
	started := time.Now()
	supervisor := CodexSupervisor{
		TempBase:     t.TempDir(),
		SetupTimeout: 3 * time.Second,
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return server, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			once.Do(func() { server.done <- serverExit })
			return nil, errors.New("observer unavailable")
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			if containsArg(argv, "--remote") {
				return CodexExitResult{}, errors.New("dead app-server launch remained remote")
			}
			return CodexExitResult{}, nil
		},
	}
	result, err := supervisor.Run(context.Background(), options)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("embedded launch waited %v after app-server exited during observer setup", elapsed)
	}
}

type supervisorOutcome struct {
	result CodexExitResult
	err    error
}

func runSupervisorAsync(ctx context.Context, supervisor CodexSupervisor, options CodexSupervisorOptions) <-chan supervisorOutcome {
	out := make(chan supervisorOutcome, 1)
	go func() {
		result, err := supervisor.Run(ctx, options)
		out <- supervisorOutcome{result: result, err: err}
	}()
	return out
}

func requirePromptSignalOutcome(
	t *testing.T,
	out <-chan supervisorOutcome,
	cancel context.CancelFunc,
	want syscall.Signal,
) supervisorOutcome {
	t.Helper()
	select {
	case outcome := <-out:
		if outcome.err != nil || !outcome.result.Signaled || outcome.result.Signal != want || outcome.result.ExitCode != 128+int(want) {
			t.Fatalf("signal outcome = (%+v, %v), want exact %v", outcome.result, outcome.err, want)
		}
		return outcome
	case <-time.After(750 * time.Millisecond):
		cancel()
		select {
		case <-out:
		case <-time.After(3 * time.Second):
		}
		t.Fatalf("supervisor did not own %v outside the active PTY", want)
		return supervisorOutcome{}
	}
}

func TestCodexSupervisorOwnsSignalDuringInitialObserverSetup(t *testing.T) {
	signals := make(chan os.Signal, 1)
	observerStarted := make(chan struct{})
	server := &fakeServerProcess{}
	var ptyCalls atomic.Int32
	var startOnce sync.Once
	supervisor := CodexSupervisor{
		Signals: signals, TempBase: t.TempDir(), SetupTimeout: 10 * time.Second,
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return server, nil },
		OpenObserver: func(ctx context.Context, _ ObserverConfig) (ObserverConnection, error) {
			startOnce.Do(func() { close(observerStarted) })
			<-ctx.Done()
			return nil, ctx.Err()
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(ctx context.Context, _ []string, _ func(OSC9Event)) (CodexExitResult, error) {
			ptyCalls.Add(1)
			return CodexExitResult{}, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := runSupervisorAsync(ctx, supervisor, supervisorOptions(t, "", ""))
	select {
	case <-observerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("observer setup did not start")
	}
	signals <- syscall.SIGINT
	requirePromptSignalOutcome(t, out, cancel, syscall.SIGINT)
	if !server.isStopped() || ptyCalls.Load() != 0 {
		t.Fatalf("server stopped=%v PTY calls=%d, want cleanup without TUI", server.isStopped(), ptyCalls.Load())
	}
}

func TestCodexSupervisorOwnsSignalDuringResumeBarrier(t *testing.T) {
	signals := make(chan os.Signal, 1)
	barrierStarted := make(chan struct{})
	server := &fakeServerProcess{}
	initial := newFakeObserverSession()
	var opens atomic.Int32
	var launches atomic.Int32
	supervisor := CodexSupervisor{
		Signals: signals, TempBase: t.TempDir(), SetupTimeout: 10 * time.Second,
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return server, nil },
		OpenObserver: func(ctx context.Context, _ ObserverConfig) (ObserverConnection, error) {
			if opens.Add(1) == 1 {
				return initial, nil
			}
			close(barrierStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			launches.Add(1)
			return CodexExitResult{ExitCode: 9, Elapsed: time.Millisecond}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := runSupervisorAsync(ctx, supervisor, supervisorOptions(t, supervisorResumeID, ""))
	select {
	case <-barrierStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("resume barrier did not start")
	}
	signals <- syscall.SIGTERM
	requirePromptSignalOutcome(t, out, cancel, syscall.SIGTERM)
	if !server.isStopped() || launches.Load() != 1 {
		t.Fatalf("server stopped=%v launches=%d, want cleanup after only failed resume", server.isStopped(), launches.Load())
	}
}

func TestCodexSupervisorOwnsSignalDuringPTYStartWindow(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ptyStarted := make(chan struct{})
	supervisor := CodexSupervisor{
		Signals: signals, TempBase: t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return nil, errors.New("embedded fallback")
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(ctx context.Context, _ []string, _ func(OSC9Event)) (CodexExitResult, error) {
			close(ptyStarted)
			<-ctx.Done()
			return CodexExitResult{}, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := runSupervisorAsync(ctx, supervisor, supervisorOptions(t, "", ""))
	select {
	case <-ptyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("PTY start window did not open")
	}
	signals <- syscall.SIGQUIT
	requirePromptSignalOutcome(t, out, cancel, syscall.SIGQUIT)
}

func TestCodexSupervisorRemoteFreshQuickFailureStopsServerThenEmbeddedFresh(t *testing.T) {
	observer := newFakeObserverSession()
	server := &fakeServerProcess{}
	var launches [][]string
	supervisor := CodexSupervisor{
		TempBase:     t.TempDir(),
		StartServer:  func(context.Context, []string, io.Writer) (AppServerProcess, error) { return server, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) { return observer, nil },
		EnterRaw:     func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			launches = append(launches, append([]string(nil), argv...))
			if len(launches) == 1 {
				return CodexExitResult{ExitCode: 2, Elapsed: 9*time.Second + 999*time.Millisecond}, nil
			}
			if !server.isStopped() {
				t.Fatal("embedded fallback started before private server stopped")
			}
			return CodexExitResult{}, nil
		},
	}
	result, err := supervisor.Run(context.Background(), supervisorOptions(t, "", ""))
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
	if len(launches) != 2 || !containsArg(launches[0], "--remote") || containsArg(launches[1], "--remote") {
		t.Fatalf("launches = %#v", launches)
	}
}

func TestCodexSupervisorFencesBufferedObserverMessagesBeforeEmbeddedFallback(t *testing.T) {
	root := testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive)
	observer := newBurstSnapshotObserver(256, root)
	server := &fakeServerProcess{}
	options := supervisorOptions(t, supervisorResumeID, "")
	launches := 0
	var openCalls atomic.Int32
	supervisor := CodexSupervisor{
		TempBase:     t.TempDir(),
		SetupTimeout: 25 * time.Millisecond,
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return server, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return observer, nil
			}
			return nil, errors.New("fresh barrier unavailable")
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, _ []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
			launches++
			if launches == 1 {
				select {
				case <-observer.produced:
				case <-time.After(3 * time.Second):
					return CodexExitResult{}, errors.New("observer burst did not fill the handoff queue")
				}
				return CodexExitResult{ExitCode: 9, Elapsed: time.Millisecond}, nil
			}
			time.Sleep(100 * time.Millisecond)
			data, err := os.ReadFile(options.StateFile)
			if err != nil {
				return CodexExitResult{}, err
			}
			state, err := attention.ParseState(data)
			if err != nil {
				return CodexExitResult{}, err
			}
			if state.Phase != attention.PhaseUnknown {
				return CodexExitResult{}, errors.New("stale observer message escaped into embedded reducer")
			}
			onOSC(OSC9Event{Message: "embedded completion"})
			state = readSupervisorState(t, options.StateFile)
			if state.Phase != attention.PhaseAttention || state.Reason != attention.ReasonDone {
				return CodexExitResult{}, errors.New("embedded OSC did not become authoritative")
			}
			return CodexExitResult{}, nil
		},
	}
	result, err := supervisor.Run(context.Background(), options)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%+v, %v), buffered observer messages crossed the embedded fence", result, err)
	}
	if launches != 2 || !server.isStopped() {
		t.Fatalf("launches=%d server stopped=%v, want two attempts and stopped server", launches, server.isStopped())
	}
}

func TestCodexSupervisorRuntimeDirFailureRunsEmbeddedWithOSCTracking(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	marker := []byte("leave me intact")
	if err := os.WriteFile(base, marker, 0o600); err != nil {
		t.Fatal(err)
	}

	options := supervisorOptions(t, "", "seed")
	wantResult := CodexExitResult{Elapsed: 17 * time.Millisecond}
	serverCalls, observerCalls, ptyCalls := 0, 0, 0
	rawBegins, rawRestores := 0, 0
	var launch []string
	supervisor := CodexSupervisor{
		TempBase: base,
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			serverCalls++
			return &fakeServerProcess{}, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			observerCalls++
			return newFakeObserverSession(), nil
		},
		EnterRaw: func() (func(), error) {
			rawBegins++
			return func() { rawRestores++ }, nil
		},
		RunPTY: func(_ context.Context, argv []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
			ptyCalls++
			launch = append([]string(nil), argv...)
			onOSC(OSC9Event{Message: "embedded completion"})
			return wantResult, nil
		},
	}

	got, err := supervisor.Run(context.Background(), options)
	if err != nil || got != wantResult {
		t.Fatalf("Run() = (%+v,%v), want (%+v,nil)", got, err, wantResult)
	}
	if serverCalls != 0 || observerCalls != 0 || ptyCalls != 1 {
		t.Fatalf("calls: server=%d observer=%d PTY=%d, want 0/0/1", serverCalls, observerCalls, ptyCalls)
	}
	if containsArg(launch, "--remote") {
		t.Fatalf("runtime-dir failure launched remote Codex: %#v", launch)
	}
	if rawBegins != 1 || rawRestores != 1 {
		t.Fatalf("raw lifecycle = begin %d restore %d, want 1/1", rawBegins, rawRestores)
	}
	if state := readSupervisorState(t, options.StateFile); state.Phase != attention.PhaseAttention || state.Reason != attention.ReasonDone {
		t.Fatalf("embedded OSC state = %+v, want attention/done", state)
	}
	if data, readErr := os.ReadFile(base); readErr != nil || !reflect.DeepEqual(data, marker) {
		t.Fatalf("unusable TempBase changed during cleanup: data=%q err=%v", data, readErr)
	}
}

func TestCodexSupervisorSetupFailureUsesEmbeddedResumeThenQuickFresh(t *testing.T) {
	var launches [][]string
	supervisor := CodexSupervisor{
		TempBase: t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return nil, errors.New("server unavailable")
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			launches = append(launches, append([]string(nil), argv...))
			if len(launches) == 1 {
				return CodexExitResult{ExitCode: 7, Elapsed: time.Second}, nil
			}
			return CodexExitResult{}, nil
		},
	}
	result, err := supervisor.Run(context.Background(), supervisorOptions(t, supervisorResumeID, "seed"))
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
	if len(launches) != 2 || !containsArg(launches[0], "resume") || containsArg(launches[1], "resume") {
		t.Fatalf("embedded resume fallback = %#v", launches)
	}
	for _, argv := range launches {
		if containsArg(argv, "--remote") {
			t.Fatalf("setup-failure launch unexpectedly remote: %#v", argv)
		}
	}
}

func TestCodexSupervisorNeverFallbacksForBoundaryLateZeroOrSignal(t *testing.T) {
	tests := []struct {
		name   string
		result CodexExitResult
	}{
		{"strict boundary", CodexExitResult{ExitCode: 1, Elapsed: 10 * time.Second}},
		{"late", CodexExitResult{ExitCode: 1, Elapsed: 11 * time.Second}},
		{"zero", CodexExitResult{ExitCode: 0, Elapsed: time.Second}},
		{"signal", CodexExitResult{ExitCode: 128 + int(syscall.SIGINT), Signaled: true, Signal: syscall.SIGINT, Elapsed: time.Second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := newFakeObserverSession()
			launches := 0
			supervisor := CodexSupervisor{
				TempBase:     t.TempDir(),
				StartServer:  func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
				OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) { return observer, nil },
				EnterRaw:     func() (func(), error) { return func() {}, nil },
				RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
					launches++
					return test.result, nil
				},
			}
			got, err := supervisor.Run(context.Background(), supervisorOptions(t, "", ""))
			if err != nil || got != test.result {
				t.Fatalf("Run() = (%+v, %v), want %+v", got, err, test.result)
			}
			if launches != 1 {
				t.Fatalf("launch count = %d, want 1", launches)
			}
		})
	}
}

func TestCodexSupervisorObserverReadyBeforeTUIAndAcceptsConfiguredOSCCompletionPayload(t *testing.T) {
	observerReady := false
	observer := newFakeObserverSession(testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive))
	options := supervisorOptions(t, supervisorResumeID, "")
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			observerReady = true
			return observer, nil
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, _ []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
			if !observerReady {
				t.Fatal("TUI started before observer coherent snapshot")
			}
			// The configured event is agent-turn-complete, while Codex uses the
			// OSC9 payload for dynamic user-facing response text.
			onOSC(OSC9Event{Message: "dynamic response preview"})
			return CodexExitResult{}, nil
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(options.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	state, err := attention.ParseState(data)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != attention.PhaseAttention || state.Reason != attention.ReasonDone {
		t.Fatalf("state = %+v, want attention/done", state)
	}
}

func TestCodexSupervisorObserverLossPublishesUnknownReconnectsAndPreservesOutageOSC(t *testing.T) {
	first := newFakeObserverSession(testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusIdle))
	second := newFakeObserverSession(testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusIdle))
	first.next <- observerNext{err: errors.New("socket lost")}
	allowReconnect := make(chan struct{})
	reconnectStarted := make(chan struct{})
	var openCalls atomic.Int32
	options := supervisorOptions(t, supervisorResumeID, "")
	launches := 0
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return first, nil
			}
			close(reconnectStarted)
			<-allowReconnect
			return second, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, _ []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
			launches++
			select {
			case <-reconnectStarted:
			case <-time.After(2 * time.Second):
				return CodexExitResult{}, errors.New("observer loss did not trigger reconnect")
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state, err := loadSupervisorState(options.StateFile)
				if err != nil {
					return CodexExitResult{}, err
				}
				if state.Phase == attention.PhaseUnknown {
					break
				}
				time.Sleep(time.Millisecond)
			}
			state, err := loadSupervisorState(options.StateFile)
			if err != nil {
				return CodexExitResult{}, err
			}
			if state.Phase != attention.PhaseUnknown {
				return CodexExitResult{}, errors.New("observer loss did not publish unknown before reconnect")
			}
			onOSC(OSC9Event{Message: "outage completion"})
			state, err = loadSupervisorState(options.StateFile)
			if err != nil {
				return CodexExitResult{}, err
			}
			if state.Phase != attention.PhaseUnknown {
				return CodexExitResult{}, errors.New("outage OSC escaped observer uncertainty")
			}
			close(allowReconnect)
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state, err := loadSupervisorState(options.StateFile)
				if err != nil {
					return CodexExitResult{}, err
				}
				if openCalls.Load() >= 2 && state.Phase == attention.PhaseAttention {
					if state.Reason != attention.ReasonDone {
						return CodexExitResult{}, fmt.Errorf("idle reconnect state = %+v, want done", state)
					}
					return CodexExitResult{}, nil
				}
				time.Sleep(time.Millisecond)
			}
			return CodexExitResult{}, errors.New("clean idle reconnect did not preserve outage completion")
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got := openCalls.Load(); launches != 1 || got != 2 {
		t.Fatalf("launches=%d observer opens=%d, want 1 and 2", launches, got)
	}
}

func TestCodexSupervisorReconnectSuppressesStatusFlagThatSpansOutage(t *testing.T) {
	waiting := testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive)
	waiting.Status.ActiveFlags = []ActiveFlag{ActiveWaitingOnUserInput}
	first := newFakeObserverSession(waiting)
	second := newFakeObserverSession(waiting)
	first.next <- observerNext{err: errors.New("socket lost")}
	// This known status is queued behind the reconnect snapshot. Reaching ready
	// therefore proves the snapshot was applied; observing the second Open call
	// alone is too early because it happens before the manager publishes the barrier.
	second.next <- observerNext{event: statusEvent(supervisorResumeID, idleStatus())}
	var openCalls atomic.Int32
	options := supervisorOptions(t, supervisorResumeID, "")
	terminal := CodexExitResult{Elapsed: options.FallbackWindow}
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state := readSupervisorState(t, options.StateFile)
				if openCalls.Load() >= 2 && state.Phase == attention.PhaseReady {
					// Initial question attention, observer-loss unknown, and this final ready
					// are the only transitions. A duplicate reconnect alert makes this 4.
					if state.Sequence != 3 {
						return terminal, fmt.Errorf("post-reconnect ready sequence = %d, want 3", state.Sequence)
					}
					return terminal, nil
				}
				time.Sleep(time.Millisecond)
			}
			return terminal, errors.New("reconnect snapshot and final idle status were not processed")
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSupervisorReconnectNotLoadedDoesNotRestartWaitingEpoch(t *testing.T) {
	waiting := testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive)
	waiting.Status.ActiveFlags = []ActiveFlag{ActiveWaitingOnUserInput}
	unknown := waiting
	unknown.Status = ThreadStatus{Type: ThreadStatusNotLoaded}

	first := newFakeObserverSession(waiting)
	second := newFakeObserverSession(unknown)
	first.next <- observerNext{err: errors.New("socket lost")}
	second.next <- observerNext{event: statusEvent(supervisorResumeID, waiting.Status)}
	second.next <- observerNext{event: statusEvent(supervisorResumeID, idleStatus())}

	var openCalls atomic.Int32
	options := supervisorOptions(t, supervisorResumeID, "")
	terminal := CodexExitResult{Elapsed: options.FallbackWindow}
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state := readSupervisorState(t, options.StateFile)
				if openCalls.Load() >= 2 && state.Phase == attention.PhaseReady {
					// Initial waiting, observer loss, and the final known idle are the
					// only semantic transitions. Re-alerting the same waiting flag after
					// the notLoaded snapshot would add a fourth sequence step.
					if state.Sequence != 3 {
						return terminal, fmt.Errorf("post-notLoaded sequence = %d, want 3", state.Sequence)
					}
					return terminal, nil
				}
				time.Sleep(time.Millisecond)
			}
			return terminal, errors.New("reconnect did not process the final known idle status")
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSupervisorNonIdleReconnectSupersedesOutageOSC(t *testing.T) {
	first := newFakeObserverSession(testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusIdle))
	second := newFakeObserverSession(testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive))
	first.next <- observerNext{err: errors.New("socket lost")}
	allowReconnect := make(chan struct{})
	reconnectStarted := make(chan struct{})
	var openCalls atomic.Int32
	options := supervisorOptions(t, supervisorResumeID, "")
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return first, nil
			}
			close(reconnectStarted)
			<-allowReconnect
			return second, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, _ []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
			select {
			case <-reconnectStarted:
			case <-time.After(2 * time.Second):
				return CodexExitResult{}, errors.New("observer loss did not trigger reconnect")
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if state := readSupervisorState(t, options.StateFile); state.Phase == attention.PhaseUnknown {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if state := readSupervisorState(t, options.StateFile); state.Phase != attention.PhaseUnknown {
				return CodexExitResult{}, errors.New("observer loss did not publish unknown before reconnect")
			}
			onOSC(OSC9Event{Message: "outage completion"})
			if state := readSupervisorState(t, options.StateFile); state.Phase != attention.PhaseUnknown {
				return CodexExitResult{}, errors.New("outage OSC escaped observer uncertainty")
			}
			close(allowReconnect)
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if state := readSupervisorState(t, options.StateFile); state.Phase == attention.PhaseWorking {
					return CodexExitResult{}, nil
				}
				time.Sleep(time.Millisecond)
			}
			return CodexExitResult{}, errors.New("active reconnect snapshot did not supersede outage completion")
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got := openCalls.Load(); got != 2 {
		t.Fatalf("observer opens = %d, want 2", got)
	}
}

func TestCodexSupervisorEmptyReconnectSnapshotIsExplicitAndCanRecover(t *testing.T) {
	root := testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusIdle)
	first := newFakeObserverSession(root)
	second := newFakeObserverSession()
	second.nilSnapshotRaw = true // A valid empty snapshot must not mean "no snapshot message".
	second.next <- observerNext{event: ReducerEvent{
		Kind:   EventThreadObserved,
		Thread: testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusActive),
	}}
	first.next <- observerNext{err: errors.New("socket lost")}
	var openCalls atomic.Int32
	options := supervisorOptions(t, supervisorResumeID, "")
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state, err := loadSupervisorState(options.StateFile)
				if err != nil {
					return CodexExitResult{}, err
				}
				if openCalls.Load() >= 2 && state.Phase == attention.PhaseWorking {
					return CodexExitResult{}, nil
				}
				time.Sleep(time.Millisecond)
			}
			return CodexExitResult{}, errors.New("empty reconnect snapshot was not installed before the later root event")
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

func TestCodexRunAttemptPreservesAdmittedLossAsUnknownAfterLaterOSC(t *testing.T) {
	for iteration := 0; iteration < 64; iteration++ {
		reducer, err := NewReducer(ReducerConfig{
			Generation: "generation.Order", ProjectCWD: "/repo", ResumeThreadID: supervisorResumeID,
		})
		if err != nil {
			t.Fatal(err)
		}
		root := testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusIdle)
		last := reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}})
		messages := make(chan supervisorMessage, 2)
		messages <- supervisorMessage{epoch: 1, kind: supervisorObserverEvent, event: ReducerEvent{
			Kind: EventThreadStatus, ThreadID: supervisorResumeID, Status: ThreadStatus{Type: ThreadStatusIdle},
		}}
		messages <- supervisorMessage{epoch: 1, kind: supervisorObserverLost}
		firstPublishing := make(chan struct{})
		enteringOSC := make(chan struct{})
		releasePublish := make(chan struct{})
		go func() {
			<-firstPublishing
			<-enteringOSC
			close(releasePublish)
		}()
		var publishOnce sync.Once
		counter := uint64(0)
		supervisor := CodexSupervisor{}
		_, err = supervisor.runAttempt(
			context.Background(), nil,
			func(_ context.Context, _ []string, onOSC func(OSC9Event)) (CodexExitResult, error) {
				close(enteringOSC)
				onOSC(OSC9Event{Message: "completion after admitted loss"})
				return CodexExitResult{}, nil
			},
			messages, 1, reducer,
			func(state attention.State) {
				last = state
				publishOnce.Do(func() {
					close(firstPublishing)
					<-releasePublish
				})
			},
			&counter,
		)
		if err != nil {
			t.Fatal(err)
		}
		if last.Phase != attention.PhaseUnknown || last.Reason != attention.ReasonNone {
			t.Fatalf("iteration %d final state = %+v, admitted loss must dominate OSC", iteration, last)
		}
	}
}

func TestCodexSupervisorFreshBaselineIsFrozenAcrossReconnect(t *testing.T) {
	old := testSupervisorThread("22222222-2222-4222-8222-222222222222", "old-session", "", ThreadStatusIdle)
	newRoot := testSupervisorThread("33333333-3333-4333-8333-333333333333", "new-session", "", ThreadStatusActive)
	first := newFakeObserverSession(old)
	second := newFakeObserverSession(old, newRoot)
	first.next <- observerNext{err: errors.New("disconnect")}
	var openCalls atomic.Int32
	options := supervisorOptions(t, "", "")
	supervisor := CodexSupervisor{
		TempBase:    t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			if openCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state := readSupervisorState(t, options.StateFile)
				if openCalls.Load() >= 2 && state.Phase == attention.PhaseWorking {
					return CodexExitResult{}, nil
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("reconnect recomputed the fresh baseline instead of selecting the new root")
			return CodexExitResult{}, nil
		},
	}
	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSupervisorCleansServerForPTYErrorAndSignal(t *testing.T) {
	tests := []struct {
		name   string
		result CodexExitResult
		err    error
	}{
		{name: "start error", result: CodexExitResult{Elapsed: 11 * time.Second}, err: errors.New("pty start failed")},
		{name: "signal", result: CodexExitResult{ExitCode: 143, Signaled: true, Signal: syscall.SIGTERM, Elapsed: time.Second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &fakeServerProcess{}
			observer := newFakeObserverSession()
			supervisor := CodexSupervisor{
				TempBase:     t.TempDir(),
				StartServer:  func(context.Context, []string, io.Writer) (AppServerProcess, error) { return server, nil },
				OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) { return observer, nil },
				EnterRaw:     func() (func(), error) { return func() {}, nil },
				RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
					return test.result, test.err
				},
			}
			got, err := supervisor.Run(context.Background(), supervisorOptions(t, "", ""))
			if !server.isStopped() {
				t.Fatal("server process was not stopped")
			}
			if test.err != nil && err == nil {
				t.Fatal("PTY error was lost")
			}
			if test.err == nil && (err != nil || got != test.result) {
				t.Fatalf("Run() = (%+v,%v), want %+v", got, err, test.result)
			}
		})
	}
}

func TestCodexRuntimeDirSocketAndServerLogArePrivate(t *testing.T) {
	dir, socket, err := makeCodexRuntimeDir("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if filepath.Dir(socket) != dir || filepath.Base(socket) != "a.sock" || !strings.HasPrefix(filepath.Base(dir), "wdc.") {
		t.Fatalf("runtime layout dir=%q socket=%q", dir, socket)
	}
	if len(socket) >= 104 {
		t.Fatalf("Darwin Unix socket path is too long: %d bytes", len(socket))
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime dir mode = %v, err=%v", info.Mode().Perm(), err)
	}

	regular := filepath.Join(dir, "regular.sock")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := waitForCodexSocket(ctx, regular, time.Millisecond); err == nil {
		t.Fatal("regular file accepted as app-server socket")
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := waitForCodexSocket(context.Background(), socket, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	socketInfo, _ := os.Stat(socket)
	if socketInfo.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 600", socketInfo.Mode().Perm())
	}

	logPath := filepath.Join(t.TempDir(), "wisp-errors.log")
	log, err := openCodexErrorLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	process, err := startDefaultAppServer(context.Background(), []string{
		"/bin/sh", "-c", "printf server-out; printf server-err >&2; exit 7",
	}, log, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("fake app-server did not exit")
	}
	_ = process.Stop(context.Background())
	data, err := os.ReadFile(logPath)
	if err != nil || string(data) != "server-outserver-err" {
		t.Fatalf("error log = %q, err=%v", data, err)
	}
	logInfo, _ := os.Stat(logPath)
	if logInfo.Mode().Perm() != 0o600 {
		t.Fatalf("error log mode = %o, want 600", logInfo.Mode().Perm())
	}
}

func TestExecAppServerStopKillsStubbornDescendantAfterLeaderExit(t *testing.T) {
	pidFile := os.Getenv("WISP_DECK_SERVER_DESC_PID")
	if os.Getenv("WISP_DECK_SERVER_DESC") == "1" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		_ = os.WriteFile(pidFile, []byte(fmt.Sprint(os.Getpid())), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	}
	if os.Getenv("WISP_DECK_SERVER_LEADER") == "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestExecAppServerStopKillsStubbornDescendantAfterLeaderExit$")
		child.Env = append(os.Environ(), "WISP_DECK_SERVER_DESC=1")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if _, err := waitPIDFile(pidFile, 2*time.Second); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}

	pidFile = filepath.Join(t.TempDir(), "descendant.pid")
	t.Setenv("WISP_DECK_SERVER_LEADER", "1")
	t.Setenv("WISP_DECK_SERVER_DESC_PID", pidFile)
	process, err := startDefaultAppServer(context.Background(), []string{
		os.Args[0], "-test.run=^TestExecAppServerStopKillsStubbornDescendantAfterLeaderExit$",
	}, io.Discard, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	execProcess := process.(*execAppServer)
	defer func() { _ = syscall.Kill(-execProcess.cmd.Process.Pid, syscall.SIGKILL) }()
	descendantPID, err := waitPIDFile(pidFile, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("app-server leader did not exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !waitProcessGone(descendantPID, time.Second) {
		t.Fatalf("TERM-ignoring app-server descendant %d survived Stop", descendantPID)
	}
}

func waitPIDFile(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var pid int
			if _, scanErr := fmt.Sscan(string(data), &pid); scanErr == nil && pid > 0 {
				return pid, nil
			}
		}
		time.Sleep(time.Millisecond)
	}
	return 0, errors.New("descendant PID file was not written")
}

func waitProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestCodexPTYForwardsBytesInputAndFragmentedOSC(t *testing.T) {
	if os.Getenv("WISP_DECK_CODEX_PTY_CHILD") == "1" {
		state, _ := term.MakeRaw(os.Stdin.Fd())
		defer func() {
			if state != nil {
				_ = term.Restore(os.Stdin.Fd(), state)
			}
		}()
		_, _ = os.Stdout.Write([]byte("ordinary:"))
		_, _ = os.Stdout.Write([]byte("\x1bPtmux;\x1b"))
		_, _ = os.Stdout.Write([]byte("\x1b]9;dynamic"))
		_, _ = os.Stdout.Write([]byte(" preview\x07\x1b\\"))
		input := make([]byte, len("stdin-bytes\n"))
		_, _ = io.ReadFull(os.Stdin, input)
		_, _ = os.Stdout.Write(input)
		os.Exit(0)
	}

	input := "stdin-bytes\n"
	ready := make(chan struct{})
	output := &readyWriter{ready: ready}
	var events []OSC9Event
	runner := CodexSupervisor{Stdin: &gatedStringReader{ready: ready, reader: strings.NewReader(input)}, Stdout: output}
	result, err := runner.runPTYAttempt(context.Background(), []string{os.Args[0], "-test.run=^TestCodexPTYForwardsBytesInputAndFragmentedOSC$"}, func(event OSC9Event) {
		events = append(events, event)
	}, []string{"WISP_DECK_CODEX_PTY_CHILD=1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	wantOutput := "ordinary:\x1bPtmux;\x1b\x1b]9;dynamic preview\x07\x1b\\" + input
	if output.String() != wantOutput {
		t.Fatalf("output = %q, want byte-identical %q", output.String(), wantOutput)
	}
	if len(events) != 1 || events[0].Message != "dynamic preview" {
		t.Fatalf("OSC events = %#v", events)
	}
}

func TestCodexDefaultEnterRawRestoresOuterSlavePTYTermios(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	before, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	supervisor := CodexSupervisor{Stdin: slave}
	restore, err := supervisor.enterRaw()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(raw, before) {
		t.Fatal("default enterRaw did not change slave PTY termios")
	}
	restore()
	restore() // restoration is intentionally idempotent across cleanup paths.
	after, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("slave PTY termios was not restored: before=%#v after=%#v", before, after)
	}
}

type gatedStringReader struct {
	ready  <-chan struct{}
	reader *strings.Reader
	once   sync.Once
}

func (r *gatedStringReader) Read(p []byte) (int, error) {
	r.once.Do(func() { <-r.ready })
	return r.reader.Read(p)
}

type readyWriter struct {
	mu    sync.Mutex
	buf   strings.Builder
	ready chan struct{}
	once  sync.Once
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	seen := strings.Contains(w.buf.String(), "ordinary:")
	w.mu.Unlock()
	if seen {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *readyWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type blockingReader struct{ unblock <-chan struct{} }

func (r blockingReader) Read([]byte) (int, error) {
	<-r.unblock
	return 0, io.EOF
}

type oneByteWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (w *oneByteWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	_ = w.buf.WriteByte(p[0])
	return 1, nil
}

func (w *oneByteWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestCodexPTYDoesNotWaitForBlockedStdinAndUsesFullWrites(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)
	output := &oneByteWriter{}
	runner := CodexSupervisor{Stdin: blockingReader{unblock: unblock}, Stdout: output}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := runner.runPTYAttempt(ctx, []string{"/bin/sh", "-c", "printf complete-output"}, func(OSC9Event) {}, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("runPTYAttempt = (%+v,%v)", result, err)
	}
	if output.String() != "complete-output" {
		t.Fatalf("short-writing stdout got %q", output.String())
	}
}

func TestCodexPTYForwardsTerminationToProcessGroupAndPropagatesResize(t *testing.T) {
	if os.Getenv("WISP_DECK_CODEX_SIGNAL_CHILD") == "1" {
		for {
			time.Sleep(time.Second)
		}
	}

	signals := make(chan os.Signal, 4)
	var sizeCalls int
	runner := CodexSupervisor{
		Signals: signals,
		Stdin:   strings.NewReader(""),
		Stdout:  io.Discard,
		TerminalSize: func() (*pty.Winsize, error) {
			sizeCalls++
			return &pty.Winsize{Rows: 31, Cols: 97}, nil
		},
	}
	resultCh := make(chan CodexExitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := runner.runPTYAttempt(context.Background(), []string{
			os.Args[0], "-test.run=^TestCodexPTYForwardsTerminationToProcessGroupAndPropagatesResize$",
		}, func(OSC9Event) {}, []string{"WISP_DECK_CODEX_SIGNAL_CHILD=1"})
		resultCh <- result
		errCh <- err
	}()
	time.Sleep(40 * time.Millisecond)
	signals <- syscall.SIGWINCH
	signals <- syscall.SIGTERM
	select {
	case result := <-resultCh:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if !result.Signaled || result.Signal != syscall.SIGTERM || result.ExitCode != 128+int(syscall.SIGTERM) {
			t.Fatalf("signal result = %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY child process group did not terminate")
	}
	if sizeCalls < 2 {
		t.Fatalf("terminal size calls = %d, want initial plus SIGWINCH", sizeCalls)
	}
}

func TestCodexPTYTerminationKillsStubbornDescendantAfterLeaderWait(t *testing.T) {
	pidFile := os.Getenv("WISP_DECK_PTY_DESC_PID")
	if os.Getenv("WISP_DECK_PTY_DESC") == "1" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		_ = os.WriteFile(pidFile, []byte(fmt.Sprint(os.Getpid())), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	}
	if os.Getenv("WISP_DECK_PTY_LEADER") == "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestCodexPTYTerminationKillsStubbornDescendantAfterLeaderWait$")
		child.Env = append(os.Environ(), "WISP_DECK_PTY_DESC=1")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if _, err := waitPIDFile(pidFile, 2*time.Second); err != nil {
			os.Exit(3)
		}
		caught := make(chan os.Signal, 1)
		signal.Notify(caught, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
		<-caught
		os.Exit(0)
	}

	pidFile = filepath.Join(t.TempDir(), "descendant.pid")
	t.Setenv("WISP_DECK_PTY_LEADER", "1")
	t.Setenv("WISP_DECK_PTY_DESC_PID", pidFile)
	signals := make(chan os.Signal, 1)
	runner := CodexSupervisor{
		Signals: signals, Stdin: strings.NewReader(""), Stdout: io.Discard, PTYGrace: 50 * time.Millisecond,
	}
	resultCh := make(chan CodexExitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := runner.runPTYAttempt(context.Background(), []string{
			os.Args[0], "-test.run=^TestCodexPTYTerminationKillsStubbornDescendantAfterLeaderWait$",
		}, func(OSC9Event) {}, nil)
		resultCh <- result
		errCh <- err
	}()
	descendantPID, err := waitPIDFile(pidFile, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) }()
	signals <- syscall.SIGTERM
	select {
	case result := <-resultCh:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if !result.Signaled || result.Signal != syscall.SIGTERM {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY leader did not terminate")
	}
	if !waitProcessGone(descendantPID, time.Second) {
		t.Fatalf("TERM-ignoring PTY descendant %d survived leader Wait", descendantPID)
	}
}

type firstReadRelease struct {
	data      []byte
	release   <-chan struct{}
	firstRead chan struct{}
	once      sync.Once
}

func (r *firstReadRelease) Read(p []byte) (int, error) {
	first := false
	r.once.Do(func() {
		first = true
		close(r.firstRead)
	})
	if !first {
		select {}
	}
	<-r.release
	n := copy(p, r.data)
	return n, io.EOF
}

func TestCodexSupervisorFallbackHasExactlyOneStdinReaderAndRoutesToCurrentPTY(t *testing.T) {
	dir := t.TempDir()
	countFile := filepath.Join(dir, "count")
	readyFile := filepath.Join(dir, "second-ready")
	fakeCodex := filepath.Join(dir, "codex")
	script := `#!/bin/bash
n=0
[ -f "$COUNT_FILE" ] && n=$(cat "$COUNT_FILE")
n=$((n + 1)); printf '%s' "$n" > "$COUNT_FILE"
if [ "$n" -eq 1 ]; then exit 9; fi
stty raw -echo
: > "$READY_FILE"
IFS= read -r -n 12 value
printf 'current:%s' "$value"
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNT_FILE", countFile)
	t.Setenv("READY_FILE", readyFile)

	release := make(chan struct{})
	reader := &firstReadRelease{
		data: []byte("stdin-bytes\n"), release: release, firstRead: make(chan struct{}),
	}
	go func() {
		<-reader.firstRead
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyFile); err == nil {
				close(release)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	observer := newFakeObserverSession()
	var output strings.Builder
	supervisor := CodexSupervisor{
		Stdin: reader, Stdout: &output, TempBase: t.TempDir(),
		StartServer:  func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) { return observer, nil },
		EnterRaw:     func() (func(), error) { return func() {}, nil },
	}
	options := supervisorOptions(t, "", "")
	options.CodexPath = fakeCodex
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := supervisor.Run(ctx, options)
	if err != nil || result.ExitCode != 0 || result.Signaled {
		t.Fatalf("Run() = (%+v,%v), output=%q", result, err, output.String())
	}
	if !strings.Contains(output.String(), "current:stdin-bytes") {
		t.Fatalf("released stdin did not reach only the current PTY: %q", output.String())
	}
}

func TestCodexSupervisorDetachesInputAtChildWaitBeforeOutputDrain(t *testing.T) {
	dir := t.TempDir()
	countFile := filepath.Join(dir, "count")
	descendantReadyFile := filepath.Join(dir, "descendant-ready")
	leaderGoneFile := filepath.Join(dir, "leader-gone")
	readyFile := filepath.Join(dir, "second-ready")
	fakeCodex := filepath.Join(dir, "codex")
	script := `#!/bin/bash
n=0
[ -f "$COUNT_FILE" ] && n=$(cat "$COUNT_FILE")
n=$((n + 1)); printf '%s' "$n" > "$COUNT_FILE"
if [ "$n" -eq 1 ]; then
  leader=$$
  (
    trap '' HUP TERM
    : > "$DESCENDANT_READY_FILE"
    while kill -0 "$leader" 2>/dev/null; do sleep 0.005; done
    : > "$LEADER_GONE_FILE"
    sleep 0.45
  ) &
  while [ ! -f "$DESCENDANT_READY_FILE" ]; do sleep 0.001; done
  exit 9
fi
stty raw -echo
: > "$READY_FILE"
IFS= read -r -n 12 value
printf 'current:%s' "$value"
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNT_FILE", countFile)
	t.Setenv("DESCENDANT_READY_FILE", descendantReadyFile)
	t.Setenv("LEADER_GONE_FILE", leaderGoneFile)
	t.Setenv("READY_FILE", readyFile)

	release := make(chan struct{})
	reader := &firstReadRelease{
		data: []byte("stdin-bytes\n"), release: release, firstRead: make(chan struct{}),
	}
	go func() {
		<-reader.firstRead
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(leaderGoneFile); err == nil {
				close(release)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	observer := newFakeObserverSession()
	var output strings.Builder
	supervisor := CodexSupervisor{
		Stdin: reader, Stdout: &output, TempBase: t.TempDir(),
		StartServer:  func(context.Context, []string, io.Writer) (AppServerProcess, error) { return &fakeServerProcess{}, nil },
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) { return observer, nil },
		EnterRaw:     func() (func(), error) { return func() {}, nil },
	}
	options := supervisorOptions(t, "", "")
	options.CodexPath = fakeCodex
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	result, err := supervisor.Run(ctx, options)
	if err != nil || result.ExitCode != 0 || result.Signaled {
		count, _ := os.ReadFile(countFile)
		_, leaderGoneErr := os.Stat(leaderGoneFile)
		_, secondReadyErr := os.Stat(readyFile)
		t.Fatalf(
			"Run() = (%+v,%v), output=%q, count=%q, leaderGone=%t, secondReady=%t",
			result, err, output.String(), count, leaderGoneErr == nil, secondReadyErr == nil,
		)
	}
	if !strings.Contains(output.String(), "current:stdin-bytes") {
		t.Fatalf("post-Wait stdin was consumed by the dead attempt: %q", output.String())
	}
}

func TestCodexPTYSignalAfterChildWaitWhileOutputDrainsCannotDeadlock(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	ptmx, err := os.CreateTemp(t.TempDir(), "closed-pty")
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	runner := CodexSupervisor{PTYGrace: 20 * time.Millisecond}
	resultCh := make(chan CodexExitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		// nil waitCh is the run loop's explicit "Wait already consumed"
		// state. A signal can still arrive while PTY output is draining.
		result, err := runner.terminatePTY(cmd, ptmx, nil, nil, syscall.SIGTERM, time.Now())
		resultCh <- result
		errCh <- err
	}()
	select {
	case result := <-resultCh:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if !result.Signaled || result.Signal != syscall.SIGTERM {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("signal deadlocked after child Wait completed")
	}
}

func TestWaitForCodexSocketReturnsImmediatelyWhenServerExits(t *testing.T) {
	done := make(chan error, 1)
	want := errors.New("server exited early")
	done <- want
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	err := waitForCodexSocketOrServer(ctx, filepath.Join(t.TempDir(), "missing.sock"), time.Second, done)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want server exit", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("early server exit observed after %v", elapsed)
	}
}

func TestCodexSupervisorValidatesIdentityBeforeEmbeddedFallback(t *testing.T) {
	base := supervisorOptions(t, "", "")
	tests := []struct {
		name   string
		mutate func(*CodexSupervisorOptions)
	}{
		{"generation", func(o *CodexSupervisorOptions) {
			o.Generation = "bad-generation"
			o.StateFile = filepath.Join(filepath.Dir(filepath.Dir(o.StateFile)), o.Generation, "state")
			_ = os.Mkdir(filepath.Dir(o.StateFile), 0o700)
		}},
		{"resume UUID", func(o *CodexSupervisorOptions) { o.ResumeSession = "NOT-A-UUID" }},
		{"client version", func(o *CodexSupervisorOptions) { o.ClientVersion = "bad\nversion" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.mutate(&options)
			called := false
			supervisor := CodexSupervisor{
				TempBase: t.TempDir(),
				StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
					return nil, errors.New("embedded")
				},
				RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
					called = true
					return CodexExitResult{}, nil
				},
			}
			if _, err := supervisor.Run(context.Background(), options); err == nil {
				t.Fatal("invalid identity accepted")
			}
			if called {
				t.Fatal("embedded TUI started before validation")
			}
		})
	}
}

func readSupervisorState(t *testing.T, path string) attention.State {
	t.Helper()
	state, err := loadSupervisorState(path)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func loadSupervisorState(path string) (attention.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return attention.State{}, err
	}
	state, err := attention.ParseState(data)
	if err != nil {
		return attention.State{}, err
	}
	return state, nil
}

func testSupervisorThread(id, session, parent string, status ThreadStatusType) Thread {
	return Thread{ID: id, SessionID: session, ParentThreadID: parent, CWD: "/repo", Status: ThreadStatus{Type: status}}
}

func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}
	return false
}

func containsArgSequence(argv []string, first, second string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == first && argv[i+1] == second {
			return true
		}
	}
	return false
}
