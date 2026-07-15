package opencodeadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fakeManagedProcess struct {
	done chan error
	once sync.Once
	stop chan struct{}
}

func newFakeManagedProcess() *fakeManagedProcess {
	return &fakeManagedProcess{done: make(chan error), stop: make(chan struct{})}
}

func (p *fakeManagedProcess) Done() <-chan error { return p.done }
func (p *fakeManagedProcess) Stop(context.Context) error {
	p.once.Do(func() { close(p.stop) })
	return nil
}

func openCodeSupervisorOptions(t *testing.T) SupervisorOptions {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "generation.OpenCode")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return SupervisorOptions{
		Prefix:     []string{"/opt/opencode"},
		StateFile:  filepath.Join(dir, "state"),
		Generation: "generation.OpenCode",
		ProjectDir: "/workspace/project",
		Continue:   true,
		Prompt:     "resume from handoff",
	}
}

func TestSupervisorUsesAuthenticatedPureServerSilentAttachAndPromptHandoff(t *testing.T) {
	options := openCodeSupervisorOptions(t)
	process := newFakeManagedProcess()
	var server processSpec
	var attach ptySpec
	var observed ObserverOptions
	var prompt struct {
		baseURL, directory, password, text string
	}
	handedOff := make(chan struct{})
	supervisor := Supervisor{
		SetupTimeout: time.Second,
		PromptDelay:  time.Nanosecond,
		SelectPort:   func() (int, error) { return 43123, nil },
		RandomSecret: func() (string, error) { return "fixed-private-secret", nil },
		StartServer: func(_ context.Context, spec processSpec) (ManagedProcess, error) {
			server = spec
			return process, nil
		},
		Observe: func(ctx context.Context, current ObserverOptions) error {
			observed = current
			current.OnReady()
			<-ctx.Done()
			return ctx.Err()
		},
		RunPTY: func(_ context.Context, spec ptySpec, onStarted func()) (ExitResult, error) {
			attach = spec
			onStarted()
			<-handedOff
			return ExitResult{ExitCode: 0}, nil
		},
		PostPrompt: func(_ context.Context, baseURL, directory, password, text string) error {
			prompt.baseURL, prompt.directory, prompt.password, prompt.text = baseURL, directory, password, text
			close(handedOff)
			return nil
		},
	}

	result, err := supervisor.Run(context.Background(), options)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
	wantServer := []string{"/opt/opencode", "--pure", "serve", "--hostname", "127.0.0.1", "--port", "43123"}
	if !reflect.DeepEqual(server.Argv, wantServer) || server.CWD != options.ProjectDir {
		t.Fatalf("server spec = %#v, want argv %#v cwd %q", server, wantServer, options.ProjectDir)
	}
	if valueForEnvironment(server.Env, "OPENCODE_SERVER_PASSWORD") != "fixed-private-secret" {
		t.Fatalf("server password env = %#v", server.Env)
	}
	wantAttach := []string{"/opt/opencode", "--pure", "attach", "http://127.0.0.1:43123", "--dir", options.ProjectDir, "--password", "fixed-private-secret", "--continue"}
	if !reflect.DeepEqual(attach.Argv, wantAttach) || attach.CWD != options.ProjectDir {
		t.Fatalf("attach spec = %#v, want argv %#v cwd %q", attach, wantAttach, options.ProjectDir)
	}
	configPath := valueForEnvironment(attach.Env, "OPENCODE_TUI_CONFIG")
	if configPath != filepath.Join(filepath.Dir(options.StateFile), "opencode-tui.json") {
		t.Fatalf("OPENCODE_TUI_CONFIG = %q", configPath)
	}
	if valueForEnvironment(attach.Env, "OPENCODE_SERVER_PASSWORD") != "" {
		t.Fatalf("attach leaked server password through env: %#v", attach.Env)
	}
	if observed.BaseURL != "http://127.0.0.1:43123" || observed.Password != "fixed-private-secret" || observed.Directory != options.ProjectDir {
		t.Fatalf("observer options = %#v", observed)
	}
	if prompt.baseURL != observed.BaseURL || prompt.directory != options.ProjectDir || prompt.password != observed.Password || prompt.text != options.Prompt {
		t.Fatalf("prompt handoff = %#v", prompt)
	}
	select {
	case <-process.stop:
	default:
		t.Fatal("OpenCode server was not stopped after attach exit")
	}
}

func TestSupervisorClosesUnknownAndDoesNotAttachWhenObservationNeverBecomesReady(t *testing.T) {
	options := openCodeSupervisorOptions(t)
	process := newFakeManagedProcess()
	attached := false
	supervisor := Supervisor{
		SetupTimeout: 25 * time.Millisecond,
		SelectPort:   func() (int, error) { return 43124, nil },
		RandomSecret: func() (string, error) { return "secret", nil },
		StartServer:  func(context.Context, processSpec) (ManagedProcess, error) { return process, nil },
		Observe: func(ctx context.Context, _ ObserverOptions) error {
			<-ctx.Done()
			return ctx.Err()
		},
		RunPTY: func(context.Context, ptySpec, func()) (ExitResult, error) {
			attached = true
			return ExitResult{}, nil
		},
	}
	_, err := supervisor.Run(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "observer") {
		t.Fatalf("Run() error = %v, want observer setup failure", err)
	}
	if attached {
		t.Fatal("attach launched without authenticated observation readiness")
	}
}

func TestSupervisorValidationRejectsUnsafeLaunches(t *testing.T) {
	base := openCodeSupervisorOptions(t)
	tests := []struct {
		name string
		edit func(*SupervisorOptions)
	}{
		{name: "relative executable", edit: func(o *SupervisorOptions) { o.Prefix = []string{"opencode"} }},
		{name: "wrong state owner", edit: func(o *SupervisorOptions) {
			o.StateFile = filepath.Join(filepath.Dir(filepath.Dir(o.StateFile)), "state")
		}},
		{name: "relative project", edit: func(o *SupervisorOptions) { o.ProjectDir = "repo" }},
		{name: "continue and session", edit: func(o *SupervisorOptions) { o.Session = "session-1" }},
		{name: "nul prompt", edit: func(o *SupervisorOptions) { o.Prompt = "bad\x00prompt" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			options.Prefix = append([]string(nil), base.Prefix...)
			test.edit(&options)
			if err := validateSupervisorOptions(options); err == nil {
				t.Fatal("unsafe supervisor options accepted")
			}
		})
	}
}

func TestPostOpenCodePromptAuthenticatesAppendThenSubmit(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "opencode" || password != "private-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Query().Get("directory") != "/workspace/project with space" {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/tui/append-prompt":
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text != "handoff text" {
				http.Error(w, "bad append body", http.StatusBadRequest)
				return
			}
		case "/tui/submit-prompt":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 0 {
				http.Error(w, "bad submit body", http.StatusBadRequest)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "true")
	}))
	defer server.Close()

	if err := postOpenCodePrompt(context.Background(), server.URL, "/workspace/project with space", "private-secret", "handoff text"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/tui/append-prompt", "/tui/submit-prompt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("prompt paths = %#v, want %#v", paths, want)
	}
}

func TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications(t *testing.T) {
	var output bytes.Buffer
	started := false
	supervisor := Supervisor{Stdin: bytes.NewReader(nil), Stdout: &output}
	result, err := supervisor.runDefaultPTY(context.Background(), ptySpec{
		Argv: []string{"/bin/sh", "-c", "printf 'left\\007middle\\033]9;native\\007right'; exit 7"},
		Env:  os.Environ(), CWD: t.TempDir(), Stdin: bytes.NewReader(nil), Stdout: &output,
	}, func() { started = true })
	if err != nil {
		t.Fatalf("PTY exit error = %v, want normal status result", err)
	}
	if !started || result.ExitCode != 7 || result.Signaled {
		t.Fatalf("PTY result = %+v, started=%v", result, started)
	}
	if got := output.String(); !strings.Contains(got, "leftmiddleright") || strings.ContainsRune(got, '\a') || strings.Contains(got, "]9;") {
		t.Fatalf("filtered PTY output = %q", got)
	}
}

func TestManagedServerStopTerminatesProcessGroup(t *testing.T) {
	dir := t.TempDir()
	childFile := filepath.Join(dir, "child.pid")
	process, err := startManagedProcess(processSpec{
		Argv: []string{"/bin/sh", "-c", "sleep 300 & child=$!; printf '%s' \"$child\" > \"$CHILD_FILE\"; wait"},
		Env:  append(os.Environ(), "CHILD_FILE="+childFile), CWD: dir, Log: io.Discard,
	}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var childPID int
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(childFile)
		if readErr == nil && len(data) > 0 {
			if _, scanErr := fmt.Sscanf(string(data), "%d", &childPID); scanErr == nil {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("managed server did not start its child")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = process.Stop(stopContext)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process-group child %d survived managed server stop", childPID)
}

func TestDefaultRuntimePrimitivesArePrivateAndLoopback(t *testing.T) {
	port, err := selectLoopbackPort()
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("loopback port = %d, %v", port, err)
	}
	secret, err := randomSecret()
	if err != nil || len(secret) != 64 || strings.Trim(secret, "0123456789abcdef") != "" {
		t.Fatalf("random secret = %q, %v", secret, err)
	}
}

func valueForEnvironment(environment []string, key string) string {
	prefix := key + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
