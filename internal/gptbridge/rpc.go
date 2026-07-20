// Package gptbridge translates Claude Code's Anthropic Messages traffic into
// authenticated Codex app-server turns.
package gptbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultRPCMaxMessageBytes = 16 << 20
	defaultStderrTailBytes    = 64 << 10
	defaultShutdownTimeout    = 2 * time.Second
	loginCancelTimeout        = time.Second
	processTerminateGrace     = 250 * time.Millisecond
)

var ErrRPCClosed = errors.New("codex app-server RPC client closed")

// RPCOptions controls protocol resource limits.
type RPCOptions struct {
	MaxMessageBytes int
}

// RPCError is an error response returned by Codex app-server.
type RPCError struct {
	Code    int64
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

// RequestID retains the exact string-or-integer JSON ID supplied by app-server.
type RequestID struct {
	raw json.RawMessage
	key string
}

// String returns the human-readable ID value.
func (id RequestID) String() string {
	var value string
	if len(id.raw) > 0 && id.raw[0] == '"' && json.Unmarshal(id.raw, &value) == nil {
		return value
	}
	return string(id.raw)
}

// Notification is a server message that does not expect a response.
type Notification struct {
	Method string
	Params json.RawMessage
}

// ServerRequest is a server-initiated request, including dynamic tool calls.
type ServerRequest struct {
	ID     RequestID
	Method string
	Params json.RawMessage
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

// RPCClient is a concurrent line-delimited app-server protocol client.
type RPCClient struct {
	reader io.ReadCloser
	writer io.WriteCloser
	max    int

	nextID atomic.Int64
	write  sync.Mutex

	mu      sync.Mutex
	pending map[string]chan rpcResponse
	err     error

	notifications chan Notification
	requests      chan ServerRequest
	done          chan struct{}
	closeOnce     sync.Once
}

// NewRPCClient starts a client over app-server stdout/stdin.
func NewRPCClient(reader io.ReadCloser, writer io.WriteCloser, options RPCOptions) *RPCClient {
	maxBytes := options.MaxMessageBytes
	if maxBytes <= 0 {
		maxBytes = defaultRPCMaxMessageBytes
	}
	client := &RPCClient{
		reader:        reader,
		writer:        writer,
		max:           maxBytes,
		pending:       make(map[string]chan rpcResponse),
		notifications: make(chan Notification, 256),
		requests:      make(chan ServerRequest, 256),
		done:          make(chan struct{}),
	}
	go client.readLoop()
	return client
}

// Notifications returns app-server notifications.
func (c *RPCClient) Notifications() <-chan Notification { return c.notifications }

// ServerRequests returns app-server requests that the bridge must answer.
func (c *RPCClient) ServerRequests() <-chan ServerRequest { return c.requests }

// Done closes when the protocol connection fails or is closed.
func (c *RPCClient) Done() <-chan struct{} { return c.done }

// Err returns the terminal connection error after Done closes.
func (c *RPCClient) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call sends a request and decodes its result into target.
func (c *RPCClient) Call(ctx context.Context, method string, params, target any) error {
	if method == "" {
		return errors.New("codex app-server RPC method is empty")
	}
	id := c.nextID.Add(1)
	rawID := json.RawMessage(strconv.FormatInt(id, 10))
	key := "n:" + string(rawID)
	response := make(chan rpcResponse, 1)

	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.pending[key] = response
	c.mu.Unlock()

	message := map[string]any{"id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := c.writeJSON(message); err != nil {
		c.removePending(key)
		c.fail(err)
		return err
	}

	select {
	case got := <-response:
		if got.err != nil {
			return got.err
		}
		if target == nil {
			return nil
		}
		if err := json.Unmarshal(got.result, target); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.abandonPending(key)
		return ctx.Err()
	case <-c.done:
		err := c.Err()
		if err == nil {
			return ErrRPCClosed
		}
		return err
	}
}

// Notify sends a client notification.
func (c *RPCClient) Notify(method string, params any) error {
	if method == "" {
		return errors.New("codex app-server notification method is empty")
	}
	message := map[string]any{"method": method}
	if params != nil {
		message["params"] = params
	}
	return c.writeJSON(message)
}

// Respond answers a server-initiated request.
func (c *RPCClient) Respond(id RequestID, result any) error {
	if id.key == "" {
		return errors.New("codex app-server response ID is empty")
	}
	return c.writeJSON(map[string]any{
		"id":     id.raw,
		"result": result,
	})
}

// RespondError answers a server request with a structured protocol error.
func (c *RPCClient) RespondError(id RequestID, code int64, message string, data any) error {
	if id.key == "" {
		return errors.New("codex app-server response ID is empty")
	}
	rpcErr := map[string]any{"code": code, "message": message}
	if data != nil {
		rpcErr["data"] = data
	}
	return c.writeJSON(map[string]any{
		"id":    id.raw,
		"error": rpcErr,
	})
}

// Close terminates the protocol connection and unblocks pending calls.
func (c *RPCClient) Close() {
	c.fail(ErrRPCClosed)
}

func (c *RPCClient) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

// abandonPending marks a cancelled call so its late response is discarded
// instead of failing the whole connection as an unknown response id. The nil
// tombstone stays until the response arrives; app-server answers every
// request, so abandoned entries do not accumulate.
func (c *RPCClient) abandonPending(key string) {
	c.mu.Lock()
	if _, ok := c.pending[key]; ok {
		c.pending[key] = nil
	}
	c.mu.Unlock()
}

func (c *RPCClient) writeJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode app-server message: %w", err)
	}
	if len(payload) > c.max {
		return fmt.Errorf("app-server message exceeds %d bytes", c.max)
	}
	payload = append(payload, '\n')

	c.write.Lock()
	defer c.write.Unlock()
	select {
	case <-c.done:
		if err := c.Err(); err != nil {
			return err
		}
		return ErrRPCClosed
	default:
	}
	if _, err := c.writer.Write(payload); err != nil {
		return fmt.Errorf("write app-server message: %w", err)
	}
	return nil
}

func (c *RPCClient) readLoop() {
	scanner := bufio.NewScanner(c.reader)
	initial := 64 << 10
	if c.max < initial {
		initial = c.max
	}
	scanner.Buffer(make([]byte, initial), c.max)
	for scanner.Scan() {
		if err := c.dispatch(scanner.Bytes()); err != nil {
			c.fail(err)
			return
		}
	}
	select {
	case <-c.done:
		return
	default:
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			c.fail(fmt.Errorf("app-server message exceeds %d bytes", c.max))
			return
		}
		c.fail(fmt.Errorf("read app-server message: %w", err))
		return
	}
	c.fail(errors.New("codex app-server stdout closed unexpectedly"))
}

func (c *RPCClient) dispatch(payload []byte) error {
	envelope, err := decodeEnvelope(payload)
	if err != nil {
		return err
	}
	switch envelope.kind {
	case envelopeResponse:
		c.mu.Lock()
		pending, ok := c.pending[envelope.id.key]
		if ok {
			delete(c.pending, envelope.id.key)
		}
		c.mu.Unlock()
		if !ok {
			return fmt.Errorf("unknown response id %s", envelope.id.String())
		}
		if pending == nil {
			return nil // late response to an abandoned call
		}
		pending <- rpcResponse{result: envelope.result, err: envelope.rpcErr}
	case envelopeNotification:
		select {
		case c.notifications <- Notification{Method: envelope.method, Params: envelope.params}:
		case <-c.done:
		default:
			return errors.New("app-server notification queue is full")
		}
	case envelopeRequest:
		select {
		case c.requests <- ServerRequest{ID: envelope.id, Method: envelope.method, Params: envelope.params}:
		case <-c.done:
		default:
			return errors.New("app-server request queue is full")
		}
	}
	return nil
}

func (c *RPCClient) fail(err error) {
	if err == nil {
		err = ErrRPCClosed
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
		_ = c.reader.Close()
		_ = c.writer.Close()
	})
}

type envelopeKind int

const (
	envelopeResponse envelopeKind = iota
	envelopeNotification
	envelopeRequest
)

type decodedEnvelope struct {
	kind   envelopeKind
	id     RequestID
	method string
	params json.RawMessage
	result json.RawMessage
	rpcErr error
}

func decodeEnvelope(payload []byte) (decodedEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return decodedEnvelope{}, fmt.Errorf("decode app-server message: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return decodedEnvelope{}, errors.New("app-server message contains trailing JSON")
	}
	if fields == nil {
		return decodedEnvelope{}, errors.New("app-server message must be an object")
	}
	// Unknown envelope fields (including jsonrpc framing) are ignored: a Codex
	// update that adds fields must not kill the connection.
	rawID, hasID := fields["id"]
	rawMethod, hasMethod := fields["method"]
	result, hasResult := fields["result"]
	rawError, hasError := fields["error"]
	if hasResult && hasError {
		return decodedEnvelope{}, errors.New("app-server response contains both result and error")
	}

	var method string
	if hasMethod {
		if err := json.Unmarshal(rawMethod, &method); err != nil || method == "" {
			return decodedEnvelope{}, errors.New("app-server method is not a non-empty string")
		}
	}

	var id RequestID
	var err error
	if hasID {
		id, err = decodeRequestID(rawID)
		if err != nil {
			return decodedEnvelope{}, err
		}
	}

	switch {
	case hasID && !hasMethod && (hasResult || hasError):
		envelope := decodedEnvelope{kind: envelopeResponse, id: id, result: result}
		if hasError {
			var value struct {
				Code    int64           `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rawError, &value); err != nil || value.Message == "" {
				return decodedEnvelope{}, errors.New("malformed app-server error response")
			}
			envelope.rpcErr = &RPCError{Code: value.Code, Message: value.Message, Data: value.Data}
		}
		return envelope, nil
	case hasID && hasMethod && !hasResult && !hasError:
		return decodedEnvelope{
			kind: envelopeRequest, id: id, method: method, params: fields["params"],
		}, nil
	case !hasID && hasMethod && !hasResult && !hasError:
		return decodedEnvelope{
			kind: envelopeNotification, method: method, params: fields["params"],
		}, nil
	default:
		return decodedEnvelope{}, errors.New("malformed app-server message envelope")
	}
}

func decodeRequestID(raw json.RawMessage) (RequestID, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return RequestID{}, errors.New("app-server request id is missing")
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil || value == "" {
			return RequestID{}, errors.New("app-server request id is not a non-empty string")
		}
		return RequestID{raw: append(json.RawMessage(nil), trimmed...), key: "s:" + value}, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return RequestID{}, errors.New("app-server request id is not a string or integer")
	}
	value, err := number.Int64()
	if err != nil || strconv.FormatInt(value, 10) != string(trimmed) {
		return RequestID{}, errors.New("app-server request id is not a string or integer")
	}
	return RequestID{raw: append(json.RawMessage(nil), trimmed...), key: "n:" + string(trimmed)}, nil
}

// Account describes the authentication identity reported by account/read.
type Account struct {
	Type     string  `json:"type"`
	Email    *string `json:"email,omitempty"`
	PlanType string  `json:"planType,omitempty"`
}

// AccountReadResult is the app-server account/read response.
type AccountReadResult struct {
	Account            *Account `json:"account"`
	RequiresOpenAIAuth bool     `json:"requiresOpenaiAuth"`
}

type accountLoginStartResult struct {
	Type    string `json:"type"`
	LoginID string `json:"loginId"`
	AuthURL string `json:"authUrl"`
}

type accountLoginCompletedNotification struct {
	LoginID string  `json:"loginId"`
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

// Model is the model metadata needed by the bridge.
type Model struct {
	ID                     string                  `json:"id"`
	Model                  string                  `json:"model"`
	DisplayName            string                  `json:"displayName"`
	Description            string                  `json:"description"`
	Hidden                 bool                    `json:"hidden"`
	IsDefault              bool                    `json:"isDefault"`
	DefaultReasoningEffort string                  `json:"defaultReasoningEffort"`
	SupportedReasoning     []ReasoningEffortOption `json:"supportedReasoningEfforts"`
}

// ReasoningEffortOption is one model reasoning choice.
type ReasoningEffortOption struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

// AppServerOptions configures a Codex app-server child.
type AppServerOptions struct {
	CodexPath       string
	ClientVersion   string
	MaxMessageBytes int
	StderrTailBytes int
	ShutdownTimeout time.Duration
}

// AppServer is an initialized app-server process and its discovered state.
type AppServer struct {
	RPC     *RPCClient
	Account AccountReadResult
	Models  []Model

	command  *exec.Cmd
	waitDone chan error
	stderr   *tailBuffer
	close    sync.Once
	shutdown time.Duration
}

// StartAppServer starts Codex on stdio, opts into the experimental dynamic-tool
// API, and discovers authentication and visible models.
func StartAppServer(ctx context.Context, options AppServerOptions) (*AppServer, error) {
	if options.CodexPath == "" {
		return nil, errors.New("Codex executable path is required")
	}
	version := options.ClientVersion
	if version == "" {
		version = "dev"
	}
	stderrBytes := options.StderrTailBytes
	if stderrBytes <= 0 {
		stderrBytes = defaultStderrTailBytes
	}
	command := exec.Command(options.CodexPath, "app-server")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdout: %w", err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdin: %w", err)
	}
	tail := newTailBuffer(stderrBytes)
	command.Stderr = tail
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}

	client := NewRPCClient(stdout, stdin, RPCOptions{MaxMessageBytes: options.MaxMessageBytes})
	server := &AppServer{
		RPC: client, command: command, waitDone: make(chan error, 1), stderr: tail,
		shutdown: options.ShutdownTimeout,
	}
	if server.shutdown <= 0 {
		server.shutdown = defaultShutdownTimeout
	}
	go func() {
		err := command.Wait()
		server.waitDone <- err
		close(server.waitDone)
		if err != nil {
			client.fail(fmt.Errorf("Codex app-server exited: %w%s", err, stderrSuffix(tail.String())))
		} else {
			client.fail(errors.New("Codex app-server exited"))
		}
	}()

	var initializeResult struct {
		UserAgent string `json:"userAgent"`
	}
	if err := client.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "wisp-deck",
			"title":   "Wisp Deck",
			"version": version,
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}, &initializeResult); err != nil {
		_ = server.Close(context.Background())
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if initializeResult.UserAgent == "" {
		_ = server.Close(context.Background())
		return nil, errors.New("initialize Codex app-server: response is missing userAgent")
	}
	if err := client.Notify("initialized", nil); err != nil {
		_ = server.Close(context.Background())
		return nil, fmt.Errorf("notify Codex initialized: %w", err)
	}
	if err := client.Call(ctx, "account/read", map[string]bool{"refreshToken": false}, &server.Account); err != nil {
		_ = server.Close(context.Background())
		return nil, fmt.Errorf("read Codex account: %w", err)
	}
	var models struct {
		Data []Model `json:"data"`
	}
	if err := client.Call(ctx, "model/list", map[string]any{
		"includeHidden": false,
		"limit":         100,
	}, &models); err != nil {
		_ = server.Close(context.Background())
		return nil, fmt.Errorf("list Codex models: %w", err)
	}
	server.Models = models.Data
	return server, nil
}

// LoginChatGPT starts Codex's managed browser login, waits for the matching
// completion notification, then refreshes the account and visible model list.
func (s *AppServer) LoginChatGPT(ctx context.Context, presentAuthURL func(string)) error {
	if s == nil || s.RPC == nil {
		return errors.New("Codex app-server is unavailable")
	}
	var started accountLoginStartResult
	if err := s.RPC.Call(ctx, "account/login/start", map[string]any{
		"type":                      "chatgpt",
		"useHostedLoginSuccessPage": true,
		"appBrand":                  "chatgpt",
	}, &started); err != nil {
		return fmt.Errorf("start ChatGPT login: %w", err)
	}
	if started.Type != "chatgpt" || started.LoginID == "" {
		return errors.New("start ChatGPT login: response is missing a managed login ID")
	}
	authURL, err := url.Parse(started.AuthURL)
	if err != nil || authURL.Scheme != "https" || authURL.Host == "" {
		return errors.New("start ChatGPT login: response has an invalid authentication URL")
	}
	if presentAuthURL != nil {
		presentAuthURL(started.AuthURL)
	}

	for {
		select {
		case notification := <-s.RPC.Notifications():
			if notification.Method != "account/login/completed" {
				continue
			}
			var completed accountLoginCompletedNotification
			if err := json.Unmarshal(notification.Params, &completed); err != nil {
				return fmt.Errorf("decode ChatGPT login completion: %w", err)
			}
			if completed.LoginID != started.LoginID {
				continue
			}
			if !completed.Success {
				message := "login failed"
				if completed.Error != nil && strings.TrimSpace(*completed.Error) != "" {
					message = strings.TrimSpace(*completed.Error)
				}
				return fmt.Errorf("ChatGPT login failed: %s", message)
			}
		case <-ctx.Done():
			cancelContext, cancel := context.WithTimeout(context.Background(), loginCancelTimeout)
			var result struct{}
			_ = s.RPC.Call(cancelContext, "account/login/cancel", map[string]string{
				"loginId": started.LoginID,
			}, &result)
			cancel()
			return ctx.Err()
		case <-s.RPC.Done():
			if err := s.RPC.Err(); err != nil {
				return err
			}
			return ErrRPCClosed
		}
		break
	}

	if err := s.RPC.Call(ctx, "account/read", map[string]bool{"refreshToken": false}, &s.Account); err != nil {
		return fmt.Errorf("read Codex account after ChatGPT login: %w", err)
	}
	var models struct {
		Data []Model `json:"data"`
	}
	if err := s.RPC.Call(ctx, "model/list", map[string]any{
		"includeHidden": false,
		"limit":         100,
	}, &models); err != nil {
		return fmt.Errorf("list Codex models after ChatGPT login: %w", err)
	}
	s.Models = models.Data
	return nil
}

// Close closes stdio, waits for app-server, and kills it if the context expires.
func (s *AppServer) Close(ctx context.Context) error {
	var result error
	s.close.Do(func() {
		s.RPC.Close()
		waitContext := ctx
		cancel := func() {}
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			waitContext, cancel = context.WithTimeout(ctx, s.shutdown)
		}
		defer cancel()
		select {
		case err := <-s.waitDone:
			if err != nil {
				result = err
			}
		case <-waitContext.Done():
			_ = signalProcessGroup(s.command.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(processTerminateGrace)
			select {
			case <-s.waitDone:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				_ = signalProcessGroup(s.command.Process.Pid, syscall.SIGKILL)
				<-s.waitDone
			}
			result = waitContext.Err()
		}
	})
	return result
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	group, err := syscall.Getpgid(pid)
	if err != nil {
		return err
	}
	return syscall.Kill(-group, signal)
}

// StderrTail returns bounded app-server diagnostics.
func (s *AppServer) StderrTail() string { return s.stderr.String() }

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(payload) >= b.limit {
		b.data = append(b.data[:0], payload[len(payload)-b.limit:]...)
		return len(payload), nil
	}
	overflow := len(b.data) + len(payload) - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, payload...)
	return len(payload), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}
