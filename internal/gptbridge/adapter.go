package gptbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultAdapterStartupTimeout  = 15 * time.Second
	defaultAdapterShutdownTimeout = 2 * time.Second
	defaultAdapterLoginTimeout    = 10 * time.Minute
)

// AdapterOptions configures the hidden Claude-to-GPT process supervisor.
type AdapterOptions struct {
	CodexPath     string
	ClaudeArgv    []string
	Environment   []string
	WorkingDir    string
	ClientVersion string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	LoginTimeout    time.Duration
	OpenURL         func(string) error
}

// AdapterResult mirrors the Claude child exit.
type AdapterResult struct {
	ExitCode int
	Signaled bool
	Signal   syscall.Signal
}

// ValidateChatGPTSubscription rejects every auth mode that could silently use
// metered API billing or externally supplied tokens.
func ValidateChatGPTSubscription(account AccountReadResult) error {
	if account.Account == nil {
		return errors.New("Codex is still signed out after ChatGPT sign-in; relaunch the session to try again")
	}
	switch account.Account.Type {
	case "chatgpt":
		return nil
	case "apiKey":
		return errors.New("Codex is using API-key authentication; run `codex logout`, then relaunch the session to sign in with ChatGPT subscription access")
	default:
		return fmt.Errorf("Codex authentication type %q is unsupported; use Codex-managed ChatGPT login", account.Account.Type)
	}
}

// BuildClaudeEnvironment replaces any upstream Anthropic route/credential with
// the private bridge's gateway auth token and ensures loopback bypasses ambient
// proxies.
func BuildClaudeEnvironment(base []string, bridgeURL, bridgeKey string) []string {
	overrides := map[string]string{
		"ANTHROPIC_BASE_URL":   bridgeURL,
		"ANTHROPIC_API_KEY":    "",
		"ANTHROPIC_AUTH_TOKEN": bridgeKey,
	}
	var noProxy string
	result := make([]string, 0, len(base)+4)
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "NO_PROXY" || key == "no_proxy" {
			if noProxy == "" {
				noProxy = value
			}
			continue
		}
		if _, replace := overrides[key]; replace {
			continue
		}
		result = append(result, entry)
	}
	noProxy = appendNoProxy(noProxy, "127.0.0.1", "localhost")
	result = append(result,
		"ANTHROPIC_BASE_URL="+bridgeURL,
		"ANTHROPIC_AUTH_TOKEN="+bridgeKey,
		"NO_PROXY="+noProxy,
	)
	return result
}

func appendNoProxy(existing string, values ...string) string {
	parts := strings.Split(existing, ",")
	seen := make(map[string]bool, len(parts)+len(values))
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			result = append(result, part)
		}
	}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return strings.Join(result, ",")
}

// appServerBundle pairs one app-server process with its engine so the
// resilient executor can replace both together when the connection dies.
type appServerBundle struct {
	server   *AppServer
	engine   *Engine
	shutdown time.Duration
}

func (b *appServerBundle) Execute(
	ctx context.Context, translation Translation, emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	return b.engine.Execute(ctx, translation, emit)
}

func (b *appServerBundle) Dead() <-chan struct{} { return b.server.RPC.Done() }

func (b *appServerBundle) Close() {
	b.engine.Close()
	closeContext, cancel := context.WithTimeout(context.Background(), b.shutdown)
	defer cancel()
	_ = b.server.Close(closeContext)
}

// subscriptionModelNames flattens the visible subscription model identifiers.
func subscriptionModelNames(models []Model) []string {
	names := make([]string, 0, len(models)*2)
	seen := make(map[string]bool)
	for _, model := range models {
		for _, name := range []string{model.ID, model.Model} {
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

// buildAppServerBundle starts a fresh app-server + engine without any
// interactive login, so bridge restarts mid-session stay non-interactive.
func buildAppServerBundle(
	ctx context.Context,
	options AdapterOptions,
	privateCWD string,
	startupTimeout, shutdownTimeout time.Duration,
) (*appServerBundle, error) {
	startupContext, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	server, err := StartAppServer(startupContext, AppServerOptions{
		CodexPath: options.CodexPath, ClientVersion: options.ClientVersion,
		ShutdownTimeout: shutdownTimeout,
	})
	cancelStartup()
	if err != nil {
		return nil, err
	}
	bundle, err := finishAppServerBundle(server, privateCWD, shutdownTimeout)
	if err != nil {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Close(closeContext)
		return nil, err
	}
	return bundle, nil
}

// finishAppServerBundle validates the account and wires the engine for an
// already-started (and possibly just-logged-in) app-server. The caller keeps
// ownership of server until this succeeds.
func finishAppServerBundle(
	server *AppServer, privateCWD string, shutdownTimeout time.Duration,
) (*appServerBundle, error) {
	if err := ValidateChatGPTSubscription(server.Account); err != nil {
		return nil, err
	}
	models := subscriptionModelNames(server.Models)
	if len(models) == 0 {
		return nil, errors.New("Codex reported no ChatGPT subscription models; update Codex and verify `codex login status`")
	}
	engine, err := NewEngine(server.RPC, EngineOptions{
		PrivateCWD: privateCWD, Models: models,
	})
	if err != nil {
		return nil, err
	}
	return &appServerBundle{server: server, engine: engine, shutdown: shutdownTimeout}, nil
}

// RunAdapter owns app-server, the loopback API, and the Claude child for one
// Wisp Deck pane.
func RunAdapter(ctx context.Context, options AdapterOptions) (AdapterResult, error) {
	if options.CodexPath == "" {
		return AdapterResult{}, errors.New("Codex is required for OpenAI GPT; install it with `wisp-deck`, then relaunch—the ChatGPT sign-in will open automatically")
	}
	if len(options.ClaudeArgv) == 0 || options.ClaudeArgv[0] == "" {
		return AdapterResult{}, errors.New("Claude command is required")
	}
	startupTimeout := options.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = defaultAdapterStartupTimeout
	}
	shutdownTimeout := options.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultAdapterShutdownTimeout
	}
	privateCWD, err := os.MkdirTemp("", "wisp-deck-gpt-")
	if err != nil {
		return AdapterResult{}, fmt.Errorf("create private GPT bridge directory: %w", err)
	}
	if err := os.Chmod(privateCWD, 0700); err != nil {
		_ = os.RemoveAll(privateCWD)
		return AdapterResult{}, fmt.Errorf("secure private GPT bridge directory: %w", err)
	}
	defer os.RemoveAll(privateCWD)

	startupContext, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	appServer, err := StartAppServer(startupContext, AppServerOptions{
		CodexPath: options.CodexPath, ClientVersion: options.ClientVersion,
		ShutdownTimeout: shutdownTimeout,
	})
	cancelStartup()
	if err != nil {
		return AdapterResult{}, fmt.Errorf("start ChatGPT subscription bridge: %w", err)
	}
	closeAppServer := func() {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = appServer.Close(closeContext)
	}
	if appServer.Account.Account == nil {
		loginTimeout := options.LoginTimeout
		if loginTimeout <= 0 {
			loginTimeout = defaultAdapterLoginTimeout
		}
		stderr := options.Stderr
		if stderr == nil {
			stderr = os.Stderr
		}
		openURL := options.OpenURL
		if openURL == nil {
			openURL = OpenChatGPTAuthURL
		}
		loginContext, cancelLogin := context.WithTimeout(ctx, loginTimeout)
		err := appServer.LoginChatGPT(loginContext, func(authURL string) {
			fmt.Fprintln(stderr, "OpenAI GPT needs ChatGPT sign-in.")
			fmt.Fprintf(stderr, "Sign in in your browser: %s\n", authURL)
			if openErr := openURL(authURL); openErr != nil {
				fmt.Fprintf(stderr, "Could not open the browser automatically; use the URL above: %v\n", openErr)
			}
			fmt.Fprintln(stderr, "Waiting for ChatGPT sign-in to finish…")
		})
		cancelLogin()
		if err != nil {
			closeAppServer()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return AdapterResult{}, fmt.Errorf("ChatGPT sign-in timed out after %s; relaunch the session to try again", loginTimeout)
			}
			return AdapterResult{}, err
		}
	}
	bundle, err := finishAppServerBundle(appServer, privateCWD, shutdownTimeout)
	if err != nil {
		closeAppServer()
		return AdapterResult{}, err
	}
	executor := NewResilientExecutor(bundle, func() (EngineBundle, error) {
		return buildAppServerBundle(ctx, options, privateCWD, startupTimeout, shutdownTimeout)
	}, 0, nil)
	defer executor.Close()

	bridgeKey, err := randomBridgeID("sk-wisp-")
	if err != nil {
		return AdapterResult{}, err
	}
	httpBridge, err := StartLoopbackServer(executor, bridgeKey, ServerOptions{})
	if err != nil {
		return AdapterResult{}, err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = httpBridge.Shutdown(closeContext)
	}()
	healthContext, cancelHealth := context.WithTimeout(ctx, startupTimeout)
	err = checkBridgeHealth(healthContext, httpBridge, bridgeKey)
	cancelHealth()
	if err != nil {
		return AdapterResult{}, err
	}

	command := exec.Command(options.ClaudeArgv[0], options.ClaudeArgv[1:]...)
	command.Stdin = options.Stdin
	command.Stdout = options.Stdout
	command.Stderr = options.Stderr
	if command.Stdin == nil {
		command.Stdin = os.Stdin
	}
	if command.Stdout == nil {
		command.Stdout = os.Stdout
	}
	if command.Stderr == nil {
		command.Stderr = os.Stderr
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	command.Env = BuildClaudeEnvironment(environment, httpBridge.URL(), bridgeKey)
	if options.WorkingDir != "" {
		if !filepath.IsAbs(options.WorkingDir) {
			return AdapterResult{}, errors.New("Claude working directory must be absolute")
		}
		command.Dir = options.WorkingDir
	}
	if err := command.Start(); err != nil {
		return AdapterResult{}, fmt.Errorf("start Claude through ChatGPT bridge: %w", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- command.Wait()
		close(waitDone)
	}()

	select {
	case waitErr := <-waitDone:
		return adapterExitResult(waitErr), nil
	case <-ctx.Done():
		_ = command.Process.Signal(syscall.SIGTERM)
		timer := time.NewTimer(shutdownTimeout)
		select {
		case waitErr := <-waitDone:
			if !timer.Stop() {
				<-timer.C
			}
			result := adapterExitResult(waitErr)
			if result.ExitCode == 0 {
				result.ExitCode = 1
			}
			return result, ctx.Err()
		case <-timer.C:
			_ = command.Process.Kill()
			waitErr := <-waitDone
			result := adapterExitResult(waitErr)
			if result.ExitCode == 0 {
				result.ExitCode = 1
			}
			return result, ctx.Err()
		}
	}
}

// OpenChatGPTAuthURL opens a validated managed-login URL in the default browser.
func OpenChatGPTAuthURL(authURL string) error {
	if err := exec.Command("open", authURL).Run(); err != nil {
		return fmt.Errorf("open ChatGPT sign-in URL: %w", err)
	}
	return nil
}

func checkBridgeHealth(ctx context.Context, server *BridgeHTTPServer, key string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL()+"/health", nil)
	if err != nil {
		return err
	}
	request.Header.Set("x-api-key", key)
	response, err := server.Client().Do(request)
	if err != nil {
		return fmt.Errorf("check GPT bridge health: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GPT bridge health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func adapterExitResult(err error) AdapterResult {
	if err == nil {
		return AdapterResult{ExitCode: 0}
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return AdapterResult{ExitCode: 1}
	}
	result := AdapterResult{ExitCode: exitErr.ExitCode()}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.Signaled = true
		result.Signal = status.Signal()
		if result.ExitCode < 0 {
			result.ExitCode = 128 + int(result.Signal)
		}
	}
	if result.ExitCode < 0 {
		result.ExitCode = 1
	}
	return result
}
