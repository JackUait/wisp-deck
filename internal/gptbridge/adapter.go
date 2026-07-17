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
		return errors.New("Codex is signed out; run `codex login` and choose ChatGPT")
	}
	switch account.Account.Type {
	case "chatgpt":
		return nil
	case "apiKey":
		return errors.New("Codex is using API-key authentication; run `codex logout`, then `codex login` with ChatGPT to use subscription access")
	default:
		return fmt.Errorf("Codex authentication type %q is unsupported; use Codex-managed ChatGPT login", account.Account.Type)
	}
}

// BuildClaudeEnvironment replaces any upstream Anthropic route/credential with
// the private bridge and ensures loopback bypasses ambient proxies.
func BuildClaudeEnvironment(base []string, bridgeURL, bridgeKey string) []string {
	overrides := map[string]string{
		"ANTHROPIC_BASE_URL":   bridgeURL,
		"ANTHROPIC_API_KEY":    bridgeKey,
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
		"ANTHROPIC_API_KEY="+bridgeKey,
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

// RunAdapter owns app-server, the loopback API, and the Claude child for one
// Wisp Deck pane.
func RunAdapter(ctx context.Context, options AdapterOptions) (AdapterResult, error) {
	if options.CodexPath == "" {
		return AdapterResult{}, errors.New("Codex is required for OpenAI GPT; install it with `wisp-deck`, then run `codex login`")
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
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = appServer.Close(closeContext)
	}()
	if err := ValidateChatGPTSubscription(appServer.Account); err != nil {
		return AdapterResult{}, err
	}
	models := make([]string, 0, len(appServer.Models)*2)
	seenModel := make(map[string]bool)
	for _, model := range appServer.Models {
		for _, name := range []string{model.ID, model.Model} {
			if name != "" && !seenModel[name] {
				seenModel[name] = true
				models = append(models, name)
			}
		}
	}
	if len(models) == 0 {
		return AdapterResult{}, errors.New("Codex reported no ChatGPT subscription models; update Codex and verify `codex login status`")
	}

	engine, err := NewEngine(appServer.RPC, EngineOptions{
		PrivateCWD: privateCWD, Models: models,
	})
	if err != nil {
		return AdapterResult{}, err
	}
	defer engine.Close()

	bridgeKey, err := randomBridgeID("sk-wisp-")
	if err != nil {
		return AdapterResult{}, err
	}
	httpBridge, err := StartLoopbackServer(engine, bridgeKey, ServerOptions{})
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
