package gptbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultToolBatchWindow = 20 * time.Millisecond
	// Claude executes tools for as long as it needs — bash runs 10+ minutes,
	// subagents far longer — and expiring a suspended turn kills the
	// conversation permanently. The TTL only reclaims turns Claude abandoned.
	defaultPendingTTL    = 24 * time.Hour
	engineCleanupTimeout = time.Second
)

// EngineRPC is the active subset of app-server RPC used by the turn engine.
type EngineRPC interface {
	Call(context.Context, string, any, any) error
	Respond(RequestID, any) error
	RespondError(RequestID, int64, string, any) error
	Notifications() <-chan Notification
	ServerRequests() <-chan ServerRequest
	Done() <-chan struct{}
	Err() error
}

// EngineOptions configures isolated ephemeral bridge turns.
type EngineOptions struct {
	PrivateCWD      string
	ToolBatchWindow time.Duration
	PendingTTL      time.Duration
	Models          []string
}

// Engine owns app-server event dispatch and pending Claude tool turns.
type Engine struct {
	rpc     EngineRPC
	options EngineOptions
	models  map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	turns     map[string]*engineTurn
	toolIndex map[string]*engineTurn
	closeOnce sync.Once
}

type engineTurn struct {
	mu sync.Mutex

	threadID string
	turnID   string
	tools    map[string]string

	events   chan Notification
	requests chan ServerRequest
	errors   chan error

	pending map[string]*pendingDynamicTool
	timer   *time.Timer

	cleanupOnce sync.Once
}

// invalidContinuationError marks a tool continuation the bridge could not
// satisfy or recover; the client must not retry it. Unknown/expired IDs first
// get a history-based recovery attempt in Execute before surfacing this way.
type invalidContinuationError struct{ err error }

func (e invalidContinuationError) Error() string { return e.err.Error() }
func (e invalidContinuationError) Unwrap() error { return e.err }

type unknownContinuationError struct{ id string }

func (e unknownContinuationError) Error() string {
	return fmt.Sprintf("unknown or expired tool_result %q", e.id)
}

type pendingDynamicTool struct {
	request ServerRequest
	callID  string
	name    string
}

// NewEngine creates the single dispatcher for one app-server connection.
func NewEngine(rpc EngineRPC, options EngineOptions) (*Engine, error) {
	if rpc == nil {
		return nil, errors.New("engine RPC is required")
	}
	if options.PrivateCWD == "" || !filepath.IsAbs(options.PrivateCWD) {
		return nil, errors.New("engine private cwd must be absolute")
	}
	if options.ToolBatchWindow <= 0 {
		options.ToolBatchWindow = defaultToolBatchWindow
	}
	if options.PendingTTL <= 0 {
		options.PendingTTL = defaultPendingTTL
	}
	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{
		rpc: rpc, options: options, models: make(map[string]bool, len(options.Models)),
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		turns: make(map[string]*engineTurn), toolIndex: make(map[string]*engineTurn),
	}
	for _, model := range options.Models {
		engine.models[model] = true
	}
	go engine.dispatch()
	return engine, nil
}

// Execute starts a new ephemeral turn or resumes one using Claude tool results.
func (e *Engine) Execute(
	ctx context.Context,
	translation Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	if len(e.models) > 0 && !e.models[translation.Model] {
		return AnthropicMessage{}, fmt.Errorf("model %q is not available", translation.Model)
	}
	if len(translation.ToolResults) > 0 {
		message, err := e.resume(ctx, translation, emit)
		var unknown unknownContinuationError
		if !errors.As(err, &unknown) {
			return message, err
		}
		recovery, ok := recoverContinuationFromHistory(translation)
		if !ok {
			return message, err
		}
		e.cleanupContinuationOwners(translation.ToolResults)
		return e.start(ctx, recovery, emit)
	}
	if len(translation.Input) == 0 {
		return AnthropicMessage{}, errors.New("new bridge turn requires user input")
	}
	return e.start(ctx, translation, emit)
}

// Claude can replay a continuation after replacing an in-flight request (Skill
// meta injections do this). Rebuild from validated history so the one-shot
// app-server request ID cannot poison the rest of the conversation.
func recoverContinuationFromHistory(translation Translation) (Translation, bool) {
	calls := make(map[string]bool, len(translation.ToolResults))
	for _, item := range translation.History {
		if item["type"] == "function_call" {
			if id, ok := item["call_id"].(string); ok {
				calls[id] = true
			}
		}
	}
	for _, result := range translation.ToolResults {
		if !calls[result.ToolUseID] {
			return Translation{}, false
		}
	}

	recovery := translation
	supplemental := append([]UserInput(nil), translation.Input...)
	recovery.ToolResults = nil
	recovery.History = append([]map[string]any(nil), translation.History...)
	for _, result := range translation.ToolResults {
		content := result.HistoryContentItems
		if content == nil {
			content = result.ContentItems
		}
		output, ok := translatedToolHistoryOutput(content)
		if !ok {
			return Translation{}, false
		}
		recovery.History = append(recovery.History, map[string]any{
			"type": "function_call_output", "call_id": result.ToolUseID,
			"output": output,
		})
	}
	recovery.Input = []UserInput{{
		Type: "text",
		Text: "[bridge] The tool results above are complete; continue the interrupted response. " +
			"Do not repeat tool calls that already have results above, and do not " +
			"re-ask questions whose answers already appear above.",
	}}
	recovery.Input = append(recovery.Input, supplemental...)
	return recovery, true
}

// cleanupContinuationOwners interrupts and deletes any still-live suspended
// turn a replayed continuation references, so recovery does not leak its Codex
// thread and unanswered app-server request until the TTL fires.
func (e *Engine) cleanupContinuationOwners(results []TranslatedToolResult) {
	e.mu.Lock()
	owners := make(map[*engineTurn]bool)
	for _, result := range results {
		if owner := e.toolIndex[result.ToolUseID]; owner != nil {
			owners[owner] = true
		}
	}
	e.mu.Unlock()
	for owner := range owners {
		e.cleanupTurn(owner, true)
	}
}

// translatedToolHistoryOutput mirrors historyToolOutput's shapes; like normal
// replayed history, the Success/is_error flag is intentionally dropped.
func translatedToolHistoryOutput(items []ToolOutputItem) (any, bool) {
	if len(items) == 1 && items[0].Type == "inputText" {
		return items[0].Text, true
	}
	output := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "inputText":
			output = append(output, map[string]any{
				"type": "input_text", "text": item.Text,
			})
		case "inputImage":
			output = append(output, map[string]any{
				"type": "input_image", "image_url": item.ImageURL, "detail": "auto",
			})
		default:
			return nil, false
		}
	}
	return output, true
}

func (e *Engine) start(
	ctx context.Context,
	translation Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	threadParams := map[string]any{
		"model":                      translation.Model,
		"cwd":                        e.options.PrivateCWD,
		"ephemeral":                  true,
		"approvalPolicy":             "never",
		"sandbox":                    "read-only",
		"allowProviderModelFallback": false,
		"dynamicTools":               translation.DynamicTools,
		"environments":               []any{},
		"runtimeWorkspaceRoots":      []string{},
		"baseInstructions":           baseInstructions(translation),
		"developerInstructions": combinedDeveloperInstructions(
			translation.System, translation.ToolDirective,
		),
		"config": map[string]any{
			"web_search": codexWebSearchMode(translation.WebSearch),
			"features": map[string]bool{
				"apps": false, "goals": false, "hooks": false,
				"memories": false, "multi_agent": false, "remote_plugin": false,
				"shell_snapshot": false, "shell_tool": false, "unified_exec": false,
			},
			"mcp_servers": map[string]any{},
		},
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := e.rpc.Call(ctx, "thread/start", threadParams, &started); err != nil {
		return AnthropicMessage{}, fmt.Errorf("start Codex thread: %w", err)
	}
	if started.Thread.ID == "" {
		return AnthropicMessage{}, errors.New("thread/start response is missing thread id")
	}
	state := &engineTurn{
		threadID: started.Thread.ID, tools: make(map[string]string),
		events: make(chan Notification, 256), requests: make(chan ServerRequest, 64),
		errors: make(chan error, 1), pending: make(map[string]*pendingDynamicTool),
	}
	for _, tool := range translation.DynamicTools {
		originalName := tool.OriginalName
		if originalName == "" {
			originalName = tool.Name
		}
		state.tools[tool.Name] = originalName
	}
	e.mu.Lock()
	if _, duplicate := e.turns[state.threadID]; duplicate {
		e.mu.Unlock()
		return AnthropicMessage{}, fmt.Errorf("duplicate app-server thread %q", state.threadID)
	}
	e.turns[state.threadID] = state
	e.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(translation.History) > 0 {
		if err := e.rpc.Call(ctx, "thread/inject_items", map[string]any{
			"threadId": state.threadID, "items": translation.History,
		}, &struct{}{}); err != nil {
			e.cleanupTurn(state, true)
			return AnthropicMessage{}, fmt.Errorf("inject Claude history: %w", err)
		}
	}
	turnParams := map[string]any{
		"threadId":       state.threadID,
		"input":          translation.Input,
		"model":          translation.Model,
		"approvalPolicy": "never",
		"environments":   []any{},
		"sandboxPolicy":  map[string]any{"type": "readOnly", "networkAccess": false},
	}
	if translation.Effort != "" {
		turnParams["effort"] = translation.Effort
	}
	var turnStarted struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := e.rpc.Call(ctx, "turn/start", turnParams, &turnStarted); err != nil {
		e.cleanupTurn(state, true)
		return AnthropicMessage{}, fmt.Errorf("start Codex turn: %w", err)
	}
	if turnStarted.Turn.ID == "" {
		e.cleanupTurn(state, true)
		return AnthropicMessage{}, errors.New("turn/start response is missing turn id")
	}
	state.turnID = turnStarted.Turn.ID
	return e.runTurnBoundary(ctx, state, translation, emit)
}

func (e *Engine) resume(
	ctx context.Context,
	translation Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	state, err := e.validateContinuation(translation.ToolResults)
	if err != nil {
		return AnthropicMessage{}, invalidContinuationError{err}
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	e.mu.Lock()
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	e.mu.Unlock()

	for _, result := range translation.ToolResults {
		e.mu.Lock()
		pending := state.pending[result.ToolUseID]
		delete(state.pending, result.ToolUseID)
		delete(e.toolIndex, result.ToolUseID)
		e.mu.Unlock()
		content := result.ContentItems
		if content == nil {
			content = []ToolOutputItem{}
		}
		if err := e.rpc.Respond(pending.request.ID, map[string]any{
			"success": result.Success, "contentItems": content,
		}); err != nil {
			e.cleanupTurn(state, true)
			return AnthropicMessage{}, fmt.Errorf("return Claude tool result: %w", err)
		}
	}
	return e.runTurnBoundary(ctx, state, translation, emit)
}

func (e *Engine) validateContinuation(results []TranslatedToolResult) (*engineTurn, error) {
	if len(results) == 0 {
		return nil, errors.New("tool continuation has no results")
	}
	seen := make(map[string]bool, len(results))
	var state *engineTurn
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, result := range results {
		if result.ToolUseID == "" {
			return nil, errors.New("tool_result ID is empty")
		}
		if seen[result.ToolUseID] {
			return nil, fmt.Errorf("duplicate tool_result %q", result.ToolUseID)
		}
		seen[result.ToolUseID] = true
		owner, ok := e.toolIndex[result.ToolUseID]
		if !ok {
			return nil, unknownContinuationError{id: result.ToolUseID}
		}
		if state == nil {
			state = owner
		} else if state != owner {
			return nil, errors.New("tool results belong to different turns")
		}
	}
	if len(results) != len(state.pending) {
		return nil, fmt.Errorf("tool continuation has %d results; pending batch requires %d", len(results), len(state.pending))
	}
	for id := range state.pending {
		if !seen[id] {
			return nil, fmt.Errorf("tool continuation is missing result %q", id)
		}
	}
	return state, nil
}

func (e *Engine) runTurnBoundary(
	ctx context.Context,
	state *engineTurn,
	translation Translation,
	emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	messageID, err := randomBridgeID("msg_")
	if err != nil {
		e.cleanupTurn(state, true)
		return AnthropicMessage{}, err
	}
	reducer := NewResponseReducer(ResponseOptions{
		MessageID: messageID, Model: translation.Model,
		ThreadID: state.threadID, TurnID: state.turnID,
		IncludeThinking:      translation.Effort != "",
		EstimatedInputTokens: estimateTranslationTokens(translation),
	})
	if err := emitEvents(emit, reducer.Start()); err != nil {
		e.cleanupTurn(state, true)
		return AnthropicMessage{}, err
	}

	var batchTimer *time.Timer
	var batch <-chan time.Time
	resetBatch := func() {
		if batchTimer == nil {
			batchTimer = time.NewTimer(e.options.ToolBatchWindow)
		} else {
			if !batchTimer.Stop() {
				select {
				case <-batchTimer.C:
				default:
				}
			}
			batchTimer.Reset(e.options.ToolBatchWindow)
		}
		batch = batchTimer.C
	}
	defer func() {
		if batchTimer != nil {
			batchTimer.Stop()
		}
	}()

	for {
		select {
		case notification := <-state.events:
			if err := rejectCodexOwnedItem(notification, translation.WebSearch); err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			events, err := reducer.Apply(notification)
			if err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			if err := emitEvents(emit, events); err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			if message, err := reducer.Message(); err == nil {
				e.cleanupTurn(state, false)
				return message, nil
			}
		case request := <-state.requests:
			call, err := e.acceptDynamicTool(state, request)
			if err != nil {
				_ = e.rpc.RespondError(request.ID, -32602, err.Error(), nil)
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			events, err := reducer.AddToolCall(call)
			if err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			if err := emitEvents(emit, events); err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			resetBatch()
		case <-batch:
			events, err := reducer.Finish("tool_use")
			if err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			if err := emitEvents(emit, events); err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			message, err := reducer.Message()
			if err != nil {
				e.cleanupTurn(state, true)
				return AnthropicMessage{}, err
			}
			e.schedulePendingExpiry(state)
			return message, nil
		case err := <-state.errors:
			e.cleanupTurn(state, true)
			return AnthropicMessage{}, err
		case <-ctx.Done():
			e.cleanupTurn(state, true)
			return AnthropicMessage{}, ctx.Err()
		case <-e.rpc.Done():
			e.cleanupTurn(state, false)
			return AnthropicMessage{}, e.rpc.Err()
		case <-e.ctx.Done():
			e.cleanupTurn(state, true)
			return AnthropicMessage{}, errors.New("GPT bridge engine closed")
		}
	}
}

func (e *Engine) acceptDynamicTool(state *engineTurn, request ServerRequest) (DynamicToolCall, error) {
	if request.Method != "item/tool/call" {
		return DynamicToolCall{}, fmt.Errorf("unsupported app-server request %q", request.Method)
	}
	var params struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return DynamicToolCall{}, errors.New("malformed dynamic tool request")
	}
	if params.ThreadID != state.threadID || params.TurnID != state.turnID {
		return DynamicToolCall{}, errors.New("dynamic tool request belongs to a different turn")
	}
	originalName, supplied := state.tools[params.Tool]
	if params.CallID == "" || params.Tool == "" || !supplied {
		return DynamicToolCall{}, fmt.Errorf("dynamic tool %q was not supplied by Claude", params.Tool)
	}
	if !validJSONObject(params.Arguments) {
		return DynamicToolCall{}, errors.New("dynamic tool arguments must be an object")
	}
	bridgeID, err := randomBridgeID("toolu_wisp_")
	if err != nil {
		return DynamicToolCall{}, err
	}
	e.mu.Lock()
	state.pending[bridgeID] = &pendingDynamicTool{
		request: request, callID: params.CallID, name: originalName,
	}
	e.toolIndex[bridgeID] = state
	e.mu.Unlock()
	return DynamicToolCall{ID: bridgeID, Name: originalName, Arguments: params.Arguments}, nil
}

// codexWebSearchMode maps the request's server-tool declaration onto the
// app-server's web_search config, whose variants are disabled|cached|indexed|live.
// The bridge otherwise keeps Codex's own tool surface switched off entirely.
func codexWebSearchMode(requested bool) string {
	if requested {
		return "live"
	}
	return "disabled"
}

func baseInstructions(translation Translation) string {
	lines := []string{
		"You are the language model inside Claude Code.",
		"Follow the supplied developer and user instructions.",
	}
	if !translation.WebSearch {
		return strings.Join(append(lines,
			"Only host-provided dynamic tools are available. Never use or request any Codex-owned shell, filesystem, web, MCP, app, image, collaboration, or environment tool.",
		), " ")
	}
	// Claude Code asked for Anthropic's server-hosted web_search, which this
	// bridge answers with Codex's own search. Its result schema takes the reply
	// as prose, so say what belongs in it.
	lines = append(lines,
		"Only host-provided dynamic tools and Codex's own web search are available. Never use or request any Codex-owned shell, filesystem, MCP, app, image, collaboration, or environment tool.",
		"Use web search to answer, then report the findings as plain text with the source URLs inline. Do not answer from memory alone.",
	)
	// config.web_search carries only a mode, so these bind here or nowhere.
	if domains := translation.WebSearchAllowedDomains; len(domains) > 0 {
		lines = append(lines, fmt.Sprintf(
			"Report results only from these domains: %s.", strings.Join(domains, ", ")))
	}
	if domains := translation.WebSearchBlockedDomains; len(domains) > 0 {
		lines = append(lines, fmt.Sprintf(
			"Never report results from these domains: %s.", strings.Join(domains, ", ")))
	}
	return strings.Join(lines, " ")
}

func rejectCodexOwnedItem(notification Notification, webSearch bool) error {
	if notification.Method != "item/started" && notification.Method != "item/completed" {
		return nil
	}
	var params struct {
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if err := json.Unmarshal(notification.Params, &params); err != nil || params.Item.Type == "" {
		return errors.New("malformed app-server item notification")
	}
	switch params.Item.Type {
	case "userMessage", "agentMessage", "reasoning", "dynamicToolCall":
		return nil
	case "contextCompaction":
		// Not a tool: the app-server emits this when it compacts its own
		// thread mid-turn. It confers no host capability and needs no host
		// action, and the reducer ignores it. Claude owns the transcript
		// (every Execute opens a fresh thread and injects the full history),
		// so Codex compacting its private copy is invisible to us. Aborting
		// here 502'd every sufficiently long turn.
		return nil
	case "webSearch":
		// The turn asked for Anthropic's server-hosted web_search, so Codex
		// running its own search is the requested capability, not a leak.
		if webSearch {
			return nil
		}
		return fmt.Errorf("forbidden Codex-owned tool item %q", params.Item.Type)
	default:
		return fmt.Errorf("forbidden Codex-owned tool item %q", params.Item.Type)
	}
}

func (e *Engine) schedulePendingExpiry(state *engineTurn) {
	e.mu.Lock()
	if state.timer != nil {
		state.timer.Stop()
	}
	state.timer = time.AfterFunc(e.options.PendingTTL, func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		e.cleanupTurn(state, true)
	})
	e.mu.Unlock()
}

func (e *Engine) cleanupTurn(state *engineTurn, interrupt bool) {
	state.cleanupOnce.Do(func() {
		e.mu.Lock()
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		delete(e.turns, state.threadID)
		for id := range state.pending {
			delete(e.toolIndex, id)
		}
		e.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), engineCleanupTimeout)
		defer cancel()
		if interrupt && state.turnID != "" {
			_ = e.rpc.Call(ctx, "turn/interrupt", map[string]string{
				"threadId": state.threadID, "turnId": state.turnID,
			}, &struct{}{})
		}
		_ = e.rpc.Call(ctx, "thread/delete", map[string]string{
			"threadId": state.threadID,
		}, &struct{}{})
	})
}

func (e *Engine) dispatch() {
	defer close(e.done)
	for {
		select {
		case notification := <-e.rpc.Notifications():
			threadID := notificationThreadID(notification)
			if threadID == "" {
				continue
			}
			e.mu.Lock()
			state := e.turns[threadID]
			e.mu.Unlock()
			if state == nil {
				continue
			}
			select {
			case state.events <- notification:
			default:
				sendEngineError(state, errors.New("app-server event queue overflow"))
			}
		case request := <-e.rpc.ServerRequests():
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			e.mu.Lock()
			state := e.turns[params.ThreadID]
			e.mu.Unlock()
			if state == nil {
				_ = e.rpc.RespondError(request.ID, -32602, "unknown bridge thread", nil)
				continue
			}
			select {
			case state.requests <- request:
			default:
				_ = e.rpc.RespondError(request.ID, -32603, "bridge request queue overflow", nil)
				sendEngineError(state, errors.New("app-server request queue overflow"))
			}
		case <-e.rpc.Done():
			e.broadcastError(e.rpc.Err())
			return
		case <-e.ctx.Done():
			return
		}
	}
}

func notificationThreadID(notification Notification) string {
	var params struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(notification.Params, &params) != nil {
		return ""
	}
	if params.ThreadID != "" {
		return params.ThreadID
	}
	return params.Thread.ID
}

func sendEngineError(state *engineTurn, err error) {
	select {
	case state.errors <- err:
	default:
	}
}

func (e *Engine) broadcastError(err error) {
	if err == nil {
		err = errors.New("Codex app-server closed")
	}
	e.mu.Lock()
	states := make([]*engineTurn, 0, len(e.turns))
	for _, state := range e.turns {
		states = append(states, state)
	}
	e.mu.Unlock()
	for _, state := range states {
		sendEngineError(state, err)
	}
}

func emitEvents(emit func([]StreamEvent) error, events []StreamEvent) error {
	if emit == nil || len(events) == 0 {
		return nil
	}
	return emit(events)
}

func estimateTranslationTokens(translation Translation) int64 {
	data, _ := json.Marshal(struct {
		System  string
		History []map[string]any
		Input   []UserInput
		Results []TranslatedToolResult
	}{
		System: translation.System, History: translation.History,
		Input: translation.Input, Results: translation.ToolResults,
	})
	tokens := (len(data) + 3) / 4
	if tokens < 1 {
		tokens = 1
	}
	return int64(tokens)
}

func randomBridgeID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate bridge id: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

// PendingTurns reports in-memory suspended/active turns for diagnostics/tests.
func (e *Engine) PendingTurns() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.turns)
}

// Close interrupts and removes every active turn and stops dispatch.
func (e *Engine) Close() {
	e.closeOnce.Do(func() {
		e.cancel()
		<-e.done
		e.mu.Lock()
		states := make([]*engineTurn, 0, len(e.turns))
		for _, state := range e.turns {
			states = append(states, state)
		}
		e.mu.Unlock()
		for _, state := range states {
			e.cleanupTurn(state, true)
		}
	})
}
