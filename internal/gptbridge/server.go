package gptbridge

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPBodyBytes = 64 << 20
	defaultHeaderBytes   = 1 << 20
)

// MessageExecutor runs one translated Claude request.
type MessageExecutor interface {
	Execute(context.Context, Translation, func([]StreamEvent) error) (AnthropicMessage, error)
}

// ServerOptions controls loopback HTTP resource limits.
type ServerOptions struct {
	MaxBodyBytes      int64
	KeepAliveInterval time.Duration
}

// AnthropicErrorResponse is Anthropic's JSON error envelope.
type AnthropicErrorResponse struct {
	Type  string         `json:"type"`
	Error AnthropicError `json:"error"`
}

// AnthropicError is one user-visible API error.
type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type bridgeHandler struct {
	executor  MessageExecutor
	keyHash   [sha256.Size]byte
	maxBody   int64
	keepAlive time.Duration
}

// NewHandler creates the authenticated Anthropic-compatible HTTP handler.
func NewHandler(executor MessageExecutor, key string, options ServerOptions) (http.Handler, error) {
	if executor == nil {
		return nil, errors.New("bridge message executor is required")
	}
	if key == "" {
		return nil, errors.New("bridge API key is required")
	}
	maxBody := options.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultHTTPBodyBytes
	}
	return &bridgeHandler{
		executor: executor, keyHash: sha256.Sum256([]byte(key)), maxBody: maxBody,
		keepAlive: options.KeepAliveInterval,
	}, nil
}

func (h *bridgeHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !h.authenticated(request) {
		writeAnthropicError(writer, http.StatusUnauthorized, "authentication_error", "invalid local bridge key")
		return
	}
	switch request.URL.Path {
	case "/health":
		if request.Method != http.MethodGet {
			writeAnthropicError(writer, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	case "/v1/messages":
		if request.Method != http.MethodPost {
			writeAnthropicError(writer, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
			return
		}
		h.handleMessages(writer, request)
	case "/v1/messages/count_tokens":
		if request.Method != http.MethodPost {
			writeAnthropicError(writer, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
			return
		}
		h.handleCountTokens(writer, request)
	default:
		writeAnthropicError(writer, http.StatusNotFound, "not_found_error", "endpoint not found")
	}
}

func (h *bridgeHandler) authenticated(request *http.Request) bool {
	provided := request.Header.Get("x-api-key")
	if provided == "" {
		const bearer = "Bearer "
		authorization := request.Header.Get("authorization")
		if strings.HasPrefix(authorization, bearer) {
			provided = strings.TrimSpace(strings.TrimPrefix(authorization, bearer))
		}
	}
	hash := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(hash[:], h.keyHash[:]) == 1
}

func (h *bridgeHandler) validateJSONRequest(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	if request.Header.Get("anthropic-version") == "" {
		writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error", "anthropic-version header is required")
		return nil, false
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("content-type"))
	if err != nil || contentType != "application/json" {
		writeAnthropicError(writer, http.StatusUnsupportedMediaType, "invalid_request_error", "content-type must be application/json")
		return nil, false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, h.maxBody)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAnthropicError(writer, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body is too large")
			return nil, false
		}
		writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error", "could not read request body")
		return nil, false
	}
	return payload, true
}

func (h *bridgeHandler) handleMessages(writer http.ResponseWriter, request *http.Request) {
	payload, ok := h.validateJSONRequest(writer, request)
	if !ok {
		return
	}
	messages, err := ParseMessagesRequest(payload)
	if err != nil {
		writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	translation, err := TranslateRequest(messages)
	if err != nil {
		writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if window, known := modelContextWindow(translation.Model); known {
		if estimate := translation.EstimatedInputTokens; estimate > int64(window) {
			writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("prompt is too long: %d tokens > %d maximum", estimate, window))
			return
		}
	}
	if !messages.Stream {
		response, err := h.executor.Execute(request.Context(), translation, nil)
		if err != nil {
			writeExecutionError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, response)
		return
	}

	stream := newStreamWriter(writer, h.keepAlive)
	defer stream.Close()
	_, err = h.executor.Execute(request.Context(), translation, stream.Emit)
	if err == nil {
		return
	}
	if stream.Started() {
		errorType, message := "api_error", err.Error()
		switch {
		case isContextOverflowMessage(message) || isRefusedOversizedMessage(err):
			errorType, message = "invalid_request_error", "prompt is too long: "+message
		case isDeterministicCodexFailure(message):
			errorType = "invalid_request_error"
		}
		_ = stream.Emit([]StreamEvent{AnthropicErrorEvent(errorType, message)})
		return
	}
	writeExecutionError(writer, request, err)
}

func (h *bridgeHandler) handleCountTokens(writer http.ResponseWriter, request *http.Request) {
	payload, ok := h.validateJSONRequest(writer, request)
	if !ok {
		return
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error", "invalid token-count request JSON")
		return
	}
	if _, ok := object["max_tokens"]; !ok {
		object["max_tokens"] = 1
	}
	normalized, _ := json.Marshal(object)
	messages, err := ParseMessagesRequest(normalized)
	if err != nil {
		writeAnthropicError(writer, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	// Codex app-server does not expose exact preflight tokenization. Count the
	// normalized semantic content, not JSON escaping or base64 transport bytes.
	tokens := estimatePromptTokens(messages)
	writeJSON(writer, http.StatusOK, map[string]int{"input_tokens": tokens})
}

func writeExecutionError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if request.Context().Err() != nil {
			return
		}
	}
	status := http.StatusBadGateway
	errorType := "api_error"
	message := err.Error()
	var invalidContinuation invalidContinuationError
	switch {
	case isContextOverflowMessage(message) || isRefusedOversizedMessage(err):
		status = http.StatusBadRequest
		errorType = "invalid_request_error"
		message = "prompt is too long: " + message
	case errors.As(err, &invalidContinuation) ||
		(strings.Contains(message, "model ") && strings.Contains(message, "not available")):
		status = http.StatusBadRequest
		errorType = "invalid_request_error"
	case isDeterministicCodexFailure(message):
		status = http.StatusBadRequest
		errorType = "invalid_request_error"
	}
	writeAnthropicError(writer, status, errorType, message)
}

func writeAnthropicError(writer http.ResponseWriter, status int, errorType, message string) {
	writeJSON(writer, status, AnthropicErrorResponse{
		Type: "error", Error: AnthropicError{Type: errorType, Message: message},
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("content-type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// BridgeHTTPServer owns the private loopback listener.
type BridgeHTTPServer struct {
	listener net.Listener
	server   *http.Server
	client   *http.Client
	done     chan error
}

// StartLoopbackServer binds an authenticated server to an ephemeral IPv4
// loopback port.
func StartLoopbackServer(
	executor MessageExecutor,
	key string,
	options ServerOptions,
) (*BridgeHTTPServer, error) {
	handler, err := NewHandler(executor, key, options)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for GPT bridge: %w", err)
	}
	httpServer := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: defaultHeaderBytes,
	}
	bridge := &BridgeHTTPServer{
		listener: listener, server: httpServer, done: make(chan error, 1),
		client: &http.Client{Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: 2 * time.Second,
			}).DialContext,
		}},
	}
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		bridge.done <- err
		close(bridge.done)
	}()
	return bridge, nil
}

// Address returns host:port.
func (s *BridgeHTTPServer) Address() string { return s.listener.Addr().String() }

// URL returns the loopback base URL.
func (s *BridgeHTTPServer) URL() string { return "http://" + s.Address() }

// Client returns a direct no-proxy HTTP client for health checks.
func (s *BridgeHTTPServer) Client() *http.Client { return s.client }

// Shutdown gracefully stops the HTTP server.
func (s *BridgeHTTPServer) Shutdown(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	if err != nil {
		_ = s.server.Close()
	}
	select {
	case serveErr := <-s.done:
		if err == nil {
			err = serveErr
		}
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	}
	s.client.CloseIdleConnections()
	return err
}

// Port returns the selected port for diagnostics.
func (s *BridgeHTTPServer) Port() int {
	_, port, _ := net.SplitHostPort(s.Address())
	value, _ := strconv.Atoi(port)
	return value
}
