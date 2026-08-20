package codexadapter

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// coldAppServerSocketDelay is longer than the 3s budget the supervisor shipped
// with and far shorter than the one it needs, so this test separates the two.
const coldAppServerSocketDelay = 4 * time.Second

// A cold `codex app-server` binds its socket long after a warm one does: the
// vendored binary is a few hundred MB of signed Mach-O, and a first exec pays
// page-in plus signature validation before it reaches main().
func TestCodexSupervisorObservesAnAppServerThatBindsItsSocketCold(t *testing.T) {
	dir := t.TempDir()
	listenFile := filepath.Join(dir, "listen")
	t.Setenv("WISP_TEST_LISTEN_FILE", listenFile)

	fakeCodex := filepath.Join(dir, "codex")
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    unix://*)\n" +
		"      printf '%s' \"${arg#unix://}\" > \"$WISP_TEST_LISTEN_FILE.tmp\"\n" +
		"      mv \"$WISP_TEST_LISTEN_FILE.tmp\" \"$WISP_TEST_LISTEN_FILE\"\n" +
		"      ;;\n" +
		"  esac\n" +
		"done\n" +
		"sleep 120\n"
	if err := os.WriteFile(fakeCodex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	options := supervisorOptions(t, "", "")
	options.CodexPath = fakeCodex
	options.IdentityFile = filepath.Join(t.TempDir(), "session-identities", "cold.codex")

	stop := make(chan struct{})
	defer close(stop)
	bound := make(chan error, 1)
	go func() { bound <- bindCodexSocketLate(listenFile, coldAppServerSocketDelay, stop) }()

	observer := newFakeObserverSession()
	var launch []string
	supervisor := CodexSupervisor{
		TempBase: shortCodexTempBase(t),
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			return observer, nil
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(_ context.Context, argv []string, _ func(OSC9Event)) (CodexExitResult, error) {
			launch = append([]string(nil), argv...)
			observer.next <- observerNext{event: ReducerEvent{
				Kind:   EventThreadObserved,
				Thread: testSupervisorThread(supervisorFreshID, "cold-session", "", ThreadStatusIdle),
			}}
			if err := waitForCodexIdentity(options.IdentityFile, supervisorFreshID); err != nil {
				return CodexExitResult{Elapsed: options.FallbackWindow}, err
			}
			return CodexExitResult{Elapsed: options.FallbackWindow}, nil
		},
	}

	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatalf("Run() error = %v, want a cold app-server to still be observed", err)
	}
	if err := <-bound; err != nil {
		t.Fatal(err)
	}
	if !containsArg(launch, "--remote") {
		t.Fatalf("cold-start launch was not observed: %#v", launch)
	}
}

// shortCodexTempBase keeps the runtime socket inside the 104-byte sun_path
// limit, which a t.TempDir() named after this test would blow on its own.
// Production never needs it: TempBase is unset there, so the base is /tmp.
func shortCodexTempBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "wdc-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return base
}

// bindCodexSocketLate stands in for an app-server that reaches its listener only
// after delay, by binding the socket path the supervisor told the process to use.
func bindCodexSocketLate(listenFile string, delay time.Duration, stop <-chan struct{}) error {
	deadline := time.Now().Add(30 * time.Second)
	var socketPath string
	for socketPath == "" {
		if data, err := os.ReadFile(listenFile); err == nil && len(data) > 0 {
			socketPath = string(data)
			break
		}
		if time.Now().After(deadline) {
			return errors.New("app-server was never told a socket path")
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-stop:
			return nil
		}
	}
	select {
	case <-time.After(delay):
	case <-stop:
		return nil
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	go func() {
		<-stop
		listener.Close()
	}()
	return nil
}

// The cold-start budget covers one exec of the Codex binary. The waits that
// follow talk to an app-server that is already running, and their reconnect
// loop cannot see that server die, so it spins until this window closes.
// Sizing them for a cold start would leave a user typing into a session that
// has already lost the identity it needs to be restorable.
func TestCodexSupervisorVerdictOnAnObserverLostBeforeIdentityStaysPrompt(t *testing.T) {
	options := supervisorOptions(t, "", "")
	options.IdentityFile = filepath.Join(t.TempDir(), "session-identities", "lost.codex")
	observer := newFakeObserverSession()
	observer.next <- observerNext{err: errors.New("app-server died before any thread was observed")}
	var opens atomic.Int32

	supervisor := CodexSupervisor{
		TempBase: shortCodexTempBase(t),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return &fakeServerProcess{}, nil
		},
		OpenObserver: func(_ context.Context, _ ObserverConfig) (ObserverConnection, error) {
			if opens.Add(1) > 1 {
				return nil, errors.New("observer remains unavailable")
			}
			return observer, nil
		},
		ReconnectDelay: time.Millisecond,
		EnterRaw:       func() (func(), error) { return func() {}, nil },
		RunPTY: func(ctx context.Context, _ []string, _ func(OSC9Event)) (CodexExitResult, error) {
			<-ctx.Done()
			return CodexExitResult{ExitCode: 1, Elapsed: options.FallbackWindow}, ctx.Err()
		},
	}

	started := time.Now()
	_, err := supervisor.Run(context.Background(), options)
	if err == nil || !errors.Is(err, errCodexIdentityObserver) {
		t.Fatalf("Run() error = %v, want the pre-identity observer verdict", err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("pre-identity observer verdict took %s, want a prompt one", elapsed)
	}
}
