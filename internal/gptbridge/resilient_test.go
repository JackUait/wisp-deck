package gptbridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBundle struct {
	name   string
	dead   chan struct{}
	mu     sync.Mutex
	closed bool
	calls  int
}

func newFakeBundle(name string) *fakeBundle {
	return &fakeBundle{name: name, dead: make(chan struct{})}
}

func (b *fakeBundle) Execute(
	_ context.Context, _ Translation, _ func([]StreamEvent) error,
) (AnthropicMessage, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	select {
	case <-b.dead:
		return AnthropicMessage{}, errors.New("bundle " + b.name + " is dead")
	default:
	}
	return AnthropicMessage{ID: b.name}, nil
}

func (b *fakeBundle) Dead() <-chan struct{} { return b.dead }

func (b *fakeBundle) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

func (b *fakeBundle) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func (b *fakeBundle) kill() { close(b.dead) }

func TestResilientExecutorReusesLiveBundle(t *testing.T) {
	builds := 0
	first := newFakeBundle("first")
	executor := NewResilientExecutor(first, func() (EngineBundle, error) {
		builds++
		return newFakeBundle("rebuilt"), nil
	}, time.Second, time.Now)
	defer executor.Close()

	for range 3 {
		response, err := executor.Execute(context.Background(), Translation{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.ID != "first" {
			t.Fatalf("response from %q, want the initial bundle", response.ID)
		}
	}
	if builds != 0 {
		t.Fatalf("builder ran %d times for a healthy bundle", builds)
	}
}

func TestResilientExecutorRebuildsAfterBundleDeath(t *testing.T) {
	first := newFakeBundle("first")
	second := newFakeBundle("second")
	builds := 0
	now := time.Now()
	executor := NewResilientExecutor(first, func() (EngineBundle, error) {
		builds++
		return second, nil
	}, time.Second, func() time.Time { return now })
	defer executor.Close()

	if _, err := executor.Execute(context.Background(), Translation{}, nil); err != nil {
		t.Fatal(err)
	}
	first.kill()

	// The next turn after app-server death must run on a fresh bridge instead
	// of failing forever with the dead client's sticky error.
	now = now.Add(2 * time.Second)
	response, err := executor.Execute(context.Background(), Translation{}, nil)
	if err != nil {
		t.Fatalf("turn after bundle death failed: %v (bridge poisoned)", err)
	}
	if response.ID != "second" {
		t.Fatalf("response from %q, want the rebuilt bundle", response.ID)
	}
	if builds != 1 {
		t.Fatalf("builder ran %d times, want 1", builds)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !first.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("dead bundle was not closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestResilientExecutorThrottlesCrashLooping(t *testing.T) {
	bundle := newFakeBundle("initial")
	bundle.kill()
	builds := 0
	now := time.Now()
	executor := NewResilientExecutor(bundle, func() (EngineBundle, error) {
		builds++
		dead := newFakeBundle("crashy")
		dead.kill()
		return dead, nil
	}, 3*time.Second, func() time.Time { return now })
	defer executor.Close()

	// First request after death rebuilds (and the replacement dies instantly).
	if _, err := executor.Execute(context.Background(), Translation{}, nil); err == nil {
		t.Fatal("execute on an instantly-dead rebuild should fail")
	}
	if builds != 1 {
		t.Fatalf("builder ran %d times, want 1", builds)
	}

	// Requests inside the backoff window must not spawn more app-servers.
	if _, err := executor.Execute(context.Background(), Translation{}, nil); err == nil ||
		!strings.Contains(err.Error(), "restart") {
		t.Fatalf("throttled request error = %v, want a restart-throttle error", err)
	}
	if builds != 1 {
		t.Fatalf("builder ran %d times inside the backoff window, want 1", builds)
	}

	// After the window, rebuilding resumes.
	now = now.Add(4 * time.Second)
	_, _ = executor.Execute(context.Background(), Translation{}, nil)
	if builds != 2 {
		t.Fatalf("builder ran %d times after the backoff window, want 2", builds)
	}
}

func TestResilientExecutorSurfacesBuildFailureAndRecovers(t *testing.T) {
	bundle := newFakeBundle("initial")
	bundle.kill()
	healthy := newFakeBundle("healthy")
	fail := true
	now := time.Now()
	executor := NewResilientExecutor(bundle, func() (EngineBundle, error) {
		if fail {
			return nil, errors.New("codex exploded")
		}
		return healthy, nil
	}, time.Second, func() time.Time { return now })
	defer executor.Close()

	if _, err := executor.Execute(context.Background(), Translation{}, nil); err == nil ||
		!strings.Contains(err.Error(), "codex exploded") {
		t.Fatalf("build failure error = %v", err)
	}

	fail = false
	now = now.Add(2 * time.Second)
	response, err := executor.Execute(context.Background(), Translation{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "healthy" {
		t.Fatalf("response from %q, want the recovered bundle", response.ID)
	}
}

func TestResilientExecutorCloseClosesCurrentBundle(t *testing.T) {
	bundle := newFakeBundle("only")
	executor := NewResilientExecutor(bundle, func() (EngineBundle, error) {
		t.Fatal("builder must not run on close")
		return nil, nil
	}, time.Second, time.Now)
	executor.Close()
	if !bundle.isClosed() {
		t.Fatal("Close did not close the live bundle")
	}
}
