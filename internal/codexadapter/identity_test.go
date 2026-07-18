package codexadapter

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const supervisorFreshID = "22222222-2222-4222-8222-222222222222"

func TestCodexIdentityWriteIsAtomicPrivateAndCanonical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-identities", "wisp-session.codex")

	if err := writeCodexIdentity(path, supervisorResumeID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), supervisorResumeID+"\n"; got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("identity permissions = %#o, want 0600", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("identity directory contains partial files: %#v", entries)
	}

	if err := writeCodexIdentity(path, supervisorFreshID); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), supervisorFreshID+"\n"; got != want {
		t.Fatalf("replaced identity = %q, want %q", got, want)
	}
	if err := writeCodexIdentity(path, strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")); err == nil {
		t.Fatal("non-canonical identity was accepted")
	}
}

func TestCodexSupervisorPersistsResumeIdentityBeforeTUI(t *testing.T) {
	options := supervisorOptions(t, supervisorResumeID, "")
	options.IdentityFile = filepath.Join(t.TempDir(), "session-identities", "resume.codex")
	observer := newFakeObserverSession(
		testSupervisorThread(supervisorResumeID, "session", "", ThreadStatusIdle),
	)
	server := &fakeServerProcess{}

	supervisor := CodexSupervisor{
		TempBase: t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return server, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			return observer, nil
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			data, err := os.ReadFile(options.IdentityFile)
			if err != nil {
				t.Fatalf("identity was not durable before TUI launch: %v", err)
			}
			if got, want := string(data), supervisorResumeID+"\n"; got != want {
				t.Fatalf("identity before TUI = %q, want %q", got, want)
			}
			return CodexExitResult{ExitCode: 0, Elapsed: time.Second}, nil
		},
	}

	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSupervisorPersistsFreshCorrelatedRoot(t *testing.T) {
	options := supervisorOptions(t, "", "")
	options.IdentityFile = filepath.Join(t.TempDir(), "session-identities", "fresh.codex")
	observer := newFakeObserverSession()
	observer.next <- observerNext{event: ReducerEvent{
		Kind:   EventThreadObserved,
		Thread: testSupervisorThread(supervisorFreshID, "fresh-session", "", ThreadStatusActive),
	}}
	server := &fakeServerProcess{}

	supervisor := CodexSupervisor{
		TempBase: t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return server, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			return observer, nil
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(context.Context, []string, func(OSC9Event)) (CodexExitResult, error) {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(options.IdentityFile)
				if err == nil && string(data) == supervisorFreshID+"\n" {
					return CodexExitResult{ExitCode: 0, Elapsed: time.Second}, nil
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("fresh correlated root was not persisted")
			return CodexExitResult{}, nil
		},
	}

	if _, err := supervisor.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSupervisorStopsWhenFreshIdentityCannotPersist(t *testing.T) {
	options := supervisorOptions(t, "", "")
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	options.IdentityFile = filepath.Join(blockedParent, "fresh.codex")
	observer := newFakeObserverSession()
	observer.next <- observerNext{event: ReducerEvent{
		Kind:   EventThreadObserved,
		Thread: testSupervisorThread(supervisorFreshID, "fresh-session", "", ThreadStatusActive),
	}}
	server := &fakeServerProcess{}
	var canceled atomic.Bool

	supervisor := CodexSupervisor{
		TempBase: t.TempDir(),
		StartServer: func(context.Context, []string, io.Writer) (AppServerProcess, error) {
			return server, nil
		},
		OpenObserver: func(context.Context, ObserverConfig) (ObserverConnection, error) {
			return observer, nil
		},
		EnterRaw: func() (func(), error) { return func() {}, nil },
		RunPTY: func(ctx context.Context, _ []string, _ func(OSC9Event)) (CodexExitResult, error) {
			<-ctx.Done()
			canceled.Store(true)
			return CodexExitResult{ExitCode: 1, Elapsed: time.Second}, ctx.Err()
		},
	}

	_, err := supervisor.Run(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "persist Codex session identity") {
		t.Fatalf("Run() error = %v, want persistence failure", err)
	}
	if !canceled.Load() {
		t.Fatal("PTY attempt was not canceled after identity persistence failed")
	}
}
