package gptbridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	// defaultChatGPTBridgeStartupTimeout bounds a lazy start. Unlike
	// RunAdapter's three minutes — paid once at pane launch, before Claude
	// exists — this one is paid INSIDE a turn, where Claude Code's byte-stall
	// watchdog aborts a stream at 180s. Measured on this machine under load
	// average 25: 2.15s through the npm shim and 2.25s for a 220MB native
	// binary copied to a fresh inode, against a 7.4s worst case recorded in
	// project memory. 60s is ~8x the worst measurement and still leaves the
	// turn a clean 400 well inside the abort budget.
	defaultChatGPTBridgeStartupTimeout = 60 * time.Second
	// defaultChatGPTBridgeShutdownTimeout matches the adapter's own.
	defaultChatGPTBridgeShutdownTimeout = 2 * time.Second
)

// ChatGPTBridgeOptions configures the lazily started ChatGPT bridge.
type ChatGPTBridgeOptions struct {
	// CodexPath is the absolute Codex executable. Empty means Codex is not
	// installed on this machine, which is reported rather than attempted.
	CodexPath       string
	ClientVersion   string
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	// ColdStarts is shared by every engine this bridge builds.
	ColdStarts *ColdStartFuse
}

// ChatGPTBridge owns one Codex app-server, its engine, and the loopback
// Anthropic-compatible server in front of them, for the lifetime of one Claude
// launch. It exists so a caller that only routes requests — internal/allin —
// needs no Codex knowledge at all: it asks for an endpoint and a key, forwards
// there, and closes the bridge when the launch ends.
//
// Nothing starts until Endpoint is called, because a session that never picks a
// GPT row must never pay for an app-server; every later call reuses the one
// that started, because a second app-server per turn is 220MB of process and a
// fresh throwaway thread each time.
type ChatGPTBridge struct {
	options ChatGPTBridgeOptions
	// buildBundle is the seam tests replace. Production builds a real
	// app-server + engine pair; it is also what ResilientExecutor calls to
	// replace one that died mid-session.
	buildBundle func(ctx context.Context, privateCWD string) (EngineBundle, error)

	// mu is held across the whole start, so a second concurrent turn waits for
	// the first one's app-server instead of racing it into existence twice.
	mu         sync.Mutex
	closed     bool
	privateCWD string
	executor   *ResilientExecutor
	server     *BridgeHTTPServer
	key        string
}

// NewChatGPTBridge returns a bridge that has started nothing yet.
func NewChatGPTBridge(options ChatGPTBridgeOptions) *ChatGPTBridge {
	bridge := &ChatGPTBridge{options: options}
	bridge.buildBundle = bridge.buildAppServer
	return bridge
}

// Endpoint returns the loopback base URL and the local API key a request must
// carry, starting the bridge on the first call.
//
// It takes no context on purpose: the only caller is allin.Resolver.Resolve,
// which has none to give, and a start that outlived its request's context would
// have to be abandoned half-built. The bound is options.StartupTimeout instead,
// so a wedged Codex costs one turn a deterministic error rather than hanging
// until Claude Code's own abort.
func (b *ChatGPTBridge) Endpoint() (string, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", "", errors.New("the ChatGPT bridge is shutting down")
	}
	if b.server != nil {
		return b.server.URL(), b.key, nil
	}
	if b.options.CodexPath == "" {
		return "", "", errors.New(
			"Codex is required for the OpenAI / ChatGPT subscription; install it with `wisp-deck`, " +
				"then relaunch this session")
	}
	if err := b.start(); err != nil {
		// A failed start is never cached: the usual cause (a signed-out Codex,
		// a Codex being upgraded) is fixed between turns, and a cached failure
		// would make the row dead for the life of the pane.
		b.teardownLocked()
		return "", "", err
	}
	return b.server.URL(), b.key, nil
}

// start builds everything, with the caller holding b.mu.
func (b *ChatGPTBridge) start() error {
	startupTimeout := b.options.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = defaultChatGPTBridgeStartupTimeout
	}
	privateCWD, err := os.MkdirTemp("", "wisp-deck-gpt-")
	if err != nil {
		return fmt.Errorf("create private GPT bridge directory: %w", err)
	}
	// 0700 before anything is written into it: the engine's threads run with
	// this as their cwd, and MkdirTemp's own mode already is 0700 on every
	// platform this ships to — this makes the requirement explicit rather than
	// inherited, exactly as RunAdapter does.
	if err := os.Chmod(privateCWD, 0o700); err != nil {
		_ = os.RemoveAll(privateCWD)
		return fmt.Errorf("secure private GPT bridge directory: %w", err)
	}
	b.privateCWD = privateCWD

	startContext, cancel := context.WithTimeout(context.Background(), startupTimeout)
	bundle, err := b.buildBundle(startContext, privateCWD)
	cancel()
	if err != nil {
		return err
	}
	b.executor = NewResilientExecutor(bundle, func() (EngineBundle, error) {
		rebuildContext, cancelRebuild := context.WithTimeout(context.Background(), startupTimeout)
		defer cancelRebuild()
		return b.buildBundle(rebuildContext, privateCWD)
	}, 0, nil)

	key, err := randomBridgeID("sk-wisp-")
	if err != nil {
		return err
	}
	server, err := StartLoopbackServer(b.executor, key, ServerOptions{})
	if err != nil {
		return err
	}
	b.key, b.server = key, server
	return nil
}

// buildAppServer is the production bundle builder: a fresh app-server, its
// account checked, and an engine whose model allowlist is whatever that
// app-server reports.
//
// It never calls LoginChatGPT. A dedicated GPT pane can run the browser login
// because it owns the terminal before Claude starts; here the only writable
// stream is the pane Claude Code is painting on, so a signed-out Codex is
// reported as a turn error naming the fix instead.
func (b *ChatGPTBridge) buildAppServer(ctx context.Context, privateCWD string) (EngineBundle, error) {
	shutdownTimeout := b.options.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultChatGPTBridgeShutdownTimeout
	}
	server, err := StartAppServer(ctx, AppServerOptions{
		CodexPath: b.options.CodexPath, ClientVersion: b.options.ClientVersion,
		ShutdownTimeout: shutdownTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("start ChatGPT subscription bridge: %w", err)
	}
	closeServer := func() {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Close(closeContext)
	}
	if server.Account.Account == nil {
		closeServer()
		return nil, errors.New(
			"Codex is signed out, so the OpenAI / ChatGPT subscription cannot serve this turn; " +
				"run `codex login` in a terminal, then retry")
	}
	bundle, err := finishAppServerBundle(server, privateCWD, shutdownTimeout, b.options.ColdStarts)
	if err != nil {
		closeServer()
		return nil, err
	}
	return bundle, nil
}

// Close stops the loopback server, the engine, and the app-server, and removes
// the private cwd. Safe before any turn and safe twice, because the launch
// wrapper calls it on every exit route whether or not a GPT row was ever
// picked.
func (b *ChatGPTBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.teardownLocked()
}

func (b *ChatGPTBridge) teardownLocked() {
	shutdownTimeout := b.options.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultChatGPTBridgeShutdownTimeout
	}
	if b.server != nil {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		_ = b.server.Shutdown(closeContext)
		cancel()
		b.server = nil
	}
	if b.executor != nil {
		b.executor.Close()
		b.executor = nil
	}
	if b.privateCWD != "" {
		_ = os.RemoveAll(b.privateCWD)
		b.privateCWD = ""
	}
	b.key = ""
}
