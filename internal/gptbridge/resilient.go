package gptbridge

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultBridgeRestartBackoff = 3 * time.Second

// EngineBundle is one live app-server + engine pair owned by the executor.
type EngineBundle interface {
	Execute(context.Context, Translation, func([]StreamEvent) error) (AnthropicMessage, error)
	// Dead closes when the underlying app-server connection has failed for any
	// reason and the bundle can never serve another turn.
	Dead() <-chan struct{}
	Close()
}

// ResilientExecutor serves Claude requests from a live bundle and replaces it
// when it dies. Without this, a single protocol incompatibility or app-server
// crash poisons every remaining turn of the session with the same sticky
// error; with it, the failure costs one turn and the retry lands on a fresh
// app-server.
type ResilientExecutor struct {
	build   func() (EngineBundle, error)
	backoff time.Duration
	now     func() time.Time

	mu          sync.Mutex
	current     EngineBundle
	lastAttempt time.Time
	hasAttempt  bool
}

// NewResilientExecutor wraps the initial bundle with rebuild-on-death.
func NewResilientExecutor(
	initial EngineBundle,
	build func() (EngineBundle, error),
	backoff time.Duration,
	now func() time.Time,
) *ResilientExecutor {
	if backoff <= 0 {
		backoff = defaultBridgeRestartBackoff
	}
	if now == nil {
		now = time.Now
	}
	return &ResilientExecutor{build: build, backoff: backoff, now: now, current: initial}
}

// Execute runs one turn on the live bundle, rebuilding it first if it died.
func (r *ResilientExecutor) Execute(
	ctx context.Context,
	translation Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	bundle, err := r.acquire()
	if err != nil {
		return AnthropicMessage{}, err
	}
	return bundle.Execute(ctx, translation, emit)
}

func (r *ResilientExecutor) acquire() (EngineBundle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		select {
		case <-r.current.Dead():
			go r.current.Close()
			r.current = nil
		default:
			return r.current, nil
		}
	}
	if r.hasAttempt && r.now().Sub(r.lastAttempt) < r.backoff {
		return nil, fmt.Errorf(
			"ChatGPT bridge restart throttled for %s after a failure; retry shortly", r.backoff,
		)
	}
	r.lastAttempt = r.now()
	r.hasAttempt = true
	bundle, err := r.build()
	if err != nil {
		return nil, fmt.Errorf("restart ChatGPT subscription bridge: %w", err)
	}
	r.current = bundle
	return bundle, nil
}

// Close shuts down the live bundle.
func (r *ResilientExecutor) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		r.current.Close()
		r.current = nil
	}
}
