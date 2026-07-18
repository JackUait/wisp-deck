package gptbridge

import (
	"context"
	"errors"
	"time"
)

// ChatGPTAuthOptions configures a short-lived Codex app-server used only for
// account inspection or managed browser authentication.
type ChatGPTAuthOptions struct {
	CodexPath       string
	ClientVersion   string
	StartupTimeout  time.Duration
	LoginTimeout    time.Duration
	ShutdownTimeout time.Duration
	OpenURL         func(string) error
}

// ChatGPTAuthEvent presents the managed HTTPS login URL and any non-fatal
// browser-opener error while the login transaction continues.
type ChatGPTAuthEvent struct {
	URL     string
	OpenErr error
}

// ReadChatGPTAccount asks Codex for its current persisted authentication state.
func ReadChatGPTAccount(
	ctx context.Context,
	options ChatGPTAuthOptions,
) (AccountReadResult, error) {
	server, closeServer, err := startAuthAppServer(ctx, options)
	if err != nil {
		return AccountReadResult{}, err
	}
	defer closeServer()
	return server.Account, nil
}

// AuthenticateChatGPT starts a fresh Codex-managed browser login even when an
// account already exists, allowing the caller to sign in again or switch it.
func AuthenticateChatGPT(
	ctx context.Context,
	options ChatGPTAuthOptions,
	present func(ChatGPTAuthEvent),
) (AccountReadResult, error) {
	server, closeServer, err := startAuthAppServer(ctx, options)
	if err != nil {
		return AccountReadResult{}, err
	}
	defer closeServer()

	loginTimeout := options.LoginTimeout
	if loginTimeout <= 0 {
		loginTimeout = defaultAdapterLoginTimeout
	}
	loginContext, cancelLogin := context.WithTimeout(ctx, loginTimeout)
	defer cancelLogin()
	openURL := options.OpenURL
	if openURL == nil {
		openURL = OpenChatGPTAuthURL
	}
	err = server.LoginChatGPT(loginContext, func(authURL string) {
		openErr := openURL(authURL)
		if present != nil {
			present(ChatGPTAuthEvent{URL: authURL, OpenErr: openErr})
		}
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return AccountReadResult{}, errors.New("ChatGPT sign-in timed out")
		}
		return AccountReadResult{}, err
	}
	if err := ValidateChatGPTSubscription(server.Account); err != nil {
		return AccountReadResult{}, err
	}
	return server.Account, nil
}

func startAuthAppServer(
	ctx context.Context,
	options ChatGPTAuthOptions,
) (*AppServer, func(), error) {
	startupTimeout := options.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = defaultAdapterStartupTimeout
	}
	shutdownTimeout := options.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultAdapterShutdownTimeout
	}
	startupContext, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	server, err := StartAppServer(startupContext, AppServerOptions{
		CodexPath:       options.CodexPath,
		ClientVersion:   options.ClientVersion,
		ShutdownTimeout: shutdownTimeout,
	})
	cancelStartup()
	if err != nil {
		return nil, nil, err
	}
	closeServer := func() {
		closeContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Close(closeContext)
	}
	return server, closeServer, nil
}
