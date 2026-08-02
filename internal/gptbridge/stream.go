package gptbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Usage is Anthropic-compatible token usage.
type Usage struct {
	InputTokens              int64            `json:"input_tokens"`
	OutputTokens             int64            `json:"output_tokens"`
	CacheCreationInputTokens int64            `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64            `json:"cache_read_input_tokens,omitempty"`
	ServerToolUse            *ServerToolUsage `json:"server_tool_use,omitempty"`
}

// ServerToolUsage is Anthropic-compatible server-tool request usage.
type ServerToolUsage struct {
	WebSearchRequests int64 `json:"web_search_requests"`
}

// ResponseContentBlock is a Messages response block.
type ResponseContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
}

// AnthropicMessage is a non-streaming Messages response.
type AnthropicMessage struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Model        string                 `json:"model"`
	Content      []ResponseContentBlock `json:"content"`
	StopReason   string                 `json:"stop_reason"`
	StopSequence *string                `json:"stop_sequence"`
	Usage        Usage                  `json:"usage"`
}

// StreamEvent is one Anthropic SSE event.
type StreamEvent struct {
	Event string
	Data  any
}

// DynamicToolCall is a bridge-generated Anthropic tool call.
type DynamicToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ResponseOptions scopes one reducer to a single app-server turn.
type ResponseOptions struct {
	MessageID            string
	Model                string
	ThreadID             string
	TurnID               string
	IncludeThinking      bool
	EstimatedInputTokens int64
}

// ResponseReducer converts app-server deltas into one Anthropic message and
// its equivalent stream.
type ResponseReducer struct {
	options ResponseOptions

	started  bool
	finished bool
	current  int
	itemID   string
	blocks   []ResponseContentBlock
	usage    Usage
	usageSet bool
	stop     string

	webSearchToolUses map[string]string
	webSearchDone     map[string]bool
	webSearchRequests int64

	imageGenerations map[string]bool
}

// NewResponseReducer constructs a turn-scoped response reducer.
func NewResponseReducer(options ResponseOptions) *ResponseReducer {
	return &ResponseReducer{
		options: options, current: -1,
		webSearchToolUses: make(map[string]string),
		webSearchDone:     make(map[string]bool),
		imageGenerations:  make(map[string]bool),
	}
}

// Start emits the stream's message_start event once.
func (r *ResponseReducer) Start() []StreamEvent {
	if r.started {
		return nil
	}
	r.started = true
	input := r.options.EstimatedInputTokens
	if input < 0 {
		input = 0
	}
	return []StreamEvent{{
		Event: "message_start",
		Data: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": r.options.MessageID, "type": "message", "role": "assistant",
				"model": r.options.Model, "content": []any{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": Usage{InputTokens: input},
			},
		},
	}}
}

// Apply reduces one app-server notification.
func (r *ResponseReducer) Apply(notification Notification) ([]StreamEvent, error) {
	if r.finished {
		return nil, errors.New("response is already finished")
	}
	switch notification.Method {
	case "item/started", "item/completed":
		return r.applyItem(notification)
	case "item/agentMessage/delta":
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil || params.ItemID == "" {
			return nil, errors.New("malformed agent message delta")
		}
		if err := r.acceptScope(params.ThreadID, params.TurnID); err != nil {
			return nil, err
		}
		events := r.Start()
		if r.current < 0 || r.itemID != params.ItemID || r.blocks[r.current].Type != "text" {
			events = append(events, r.closeCurrent()...)
			r.blocks = append(r.blocks, ResponseContentBlock{Type: "text"})
			r.current = len(r.blocks) - 1
			r.itemID = params.ItemID
			events = append(events, contentStart(r.current, map[string]any{"type": "text", "text": ""}))
		}
		r.blocks[r.current].Text += params.Delta
		if params.Delta != "" {
			events = append(events, StreamEvent{
				Event: "content_block_delta",
				Data: map[string]any{
					"type": "content_block_delta", "index": r.current,
					"delta": map[string]any{"type": "text_delta", "text": params.Delta},
				},
			})
		}
		return events, nil
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		if !r.options.IncludeThinking {
			return nil, nil
		}
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil || params.ItemID == "" {
			return nil, errors.New("malformed reasoning delta")
		}
		if err := r.acceptScope(params.ThreadID, params.TurnID); err != nil {
			return nil, err
		}
		events := r.Start()
		if r.current < 0 || r.itemID != params.ItemID || r.blocks[r.current].Type != "thinking" {
			events = append(events, r.closeCurrent()...)
			r.blocks = append(r.blocks, ResponseContentBlock{
				Type: "thinking", Signature: "wisp-deck-gpt:" + r.options.MessageID,
			})
			r.current = len(r.blocks) - 1
			r.itemID = params.ItemID
			events = append(events, contentStart(r.current, map[string]any{
				"type": "thinking", "thinking": "", "signature": "",
			}))
		}
		r.blocks[r.current].Thinking += params.Delta
		if params.Delta != "" {
			events = append(events, StreamEvent{
				Event: "content_block_delta",
				Data: map[string]any{
					"type": "content_block_delta", "index": r.current,
					"delta": map[string]any{"type": "thinking_delta", "thinking": params.Delta},
				},
			})
		}
		return events, nil
	case "thread/tokenUsage/updated":
		var params struct {
			ThreadID   string `json:"threadId"`
			TurnID     string `json:"turnId"`
			TokenUsage struct {
				Last struct {
					InputTokens           int64 `json:"inputTokens"`
					CachedInputTokens     int64 `json:"cachedInputTokens"`
					OutputTokens          int64 `json:"outputTokens"`
					ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
				} `json:"last"`
			} `json:"tokenUsage"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return nil, errors.New("malformed token usage notification")
		}
		if err := r.acceptScope(params.ThreadID, params.TurnID); err != nil {
			return nil, err
		}
		input := params.TokenUsage.Last.InputTokens - params.TokenUsage.Last.CachedInputTokens
		if input < 0 {
			input = 0
		}
		r.usage = Usage{
			InputTokens: input, OutputTokens: params.TokenUsage.Last.OutputTokens,
			CacheReadInputTokens: params.TokenUsage.Last.CachedInputTokens,
		}
		r.usageSet = true
		return nil, nil
	case "turn/completed":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil || params.Turn.ID == "" {
			return nil, errors.New("malformed turn/completed notification")
		}
		if err := r.acceptScope(params.ThreadID, params.Turn.ID); err != nil {
			return nil, err
		}
		switch params.Turn.Status {
		case "completed":
			return r.Finish("end_turn")
		case "failed":
			if params.Turn.Error != nil && params.Turn.Error.Message != "" {
				return nil, errors.New(params.Turn.Error.Message)
			}
			return nil, errors.New("Codex turn failed")
		case "interrupted":
			return nil, errors.New("Codex turn was interrupted")
		default:
			return nil, fmt.Errorf("unsupported completed turn status %q", params.Turn.Status)
		}
	case "error":
		var params struct {
			ThreadID  string `json:"threadId"`
			TurnID    string `json:"turnId"`
			WillRetry bool   `json:"willRetry"`
			Error     struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return nil, errors.New("malformed Codex error notification")
		}
		if err := r.acceptScope(params.ThreadID, params.TurnID); err != nil {
			return nil, err
		}
		if params.WillRetry {
			return nil, nil
		}
		if params.Error.Message == "" {
			params.Error.Message = "Codex turn failed"
		}
		return nil, errors.New(params.Error.Message)
	default:
		return nil, nil
	}
}

// applyItem routes the app-server item types that carry a user-visible result.
// Everything else is Codex's own bookkeeping and reduces to no output.
func (r *ResponseReducer) applyItem(notification Notification) ([]StreamEvent, error) {
	var probe struct {
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if err := json.Unmarshal(notification.Params, &probe); err != nil {
		return nil, errors.New("malformed app-server item notification")
	}
	if probe.Item.Type == "imageGeneration" {
		return r.applyImageGenerationItem(notification)
	}
	return r.applyWebSearchItem(notification)
}

// applyImageGenerationItem reports an image Codex generated on its own.
//
// Codex's image generation cannot be disabled from the client, so the model
// uses it whenever a turn calls for a picture. The Messages API has no
// assistant-role image block, so the picture itself cannot reach Claude Code —
// but dropping the item in silence is the worse failure: the model's own
// "Generated the image." prose would stand with nothing behind it, and the user
// would never learn why nothing appeared. The base64 payload stays out of the
// transcript; it is megabytes of context that neither Claude nor the user can
// act on through this channel.
func (r *ResponseReducer) applyImageGenerationItem(notification Notification) ([]StreamEvent, error) {
	if notification.Method != "item/completed" {
		return nil, nil
	}
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			ID            string `json:"id"`
			RevisedPrompt string `json:"revisedPrompt"`
		} `json:"item"`
	}
	if err := json.Unmarshal(notification.Params, &params); err != nil {
		return nil, errors.New("malformed image generation item notification")
	}
	if err := r.acceptScope(params.ThreadID, params.TurnID); err != nil {
		return nil, err
	}
	if params.Item.ID != "" {
		if r.imageGenerations[params.Item.ID] {
			return nil, nil
		}
		r.imageGenerations[params.Item.ID] = true
	}

	notice := "[Codex generated an image, but this bridge cannot return images " +
		"to Claude Code, so it was discarded."
	if prompt := strings.TrimSpace(params.Item.RevisedPrompt); prompt != "" {
		notice += " Prompt used: " + prompt
	}
	notice += "]"

	events := r.Start()
	events = append(events, r.closeCurrent()...)
	index := len(r.blocks)
	r.blocks = append(r.blocks, ResponseContentBlock{Type: "text", Text: notice})
	events = append(events,
		contentStart(index, map[string]any{"type": "text", "text": ""}),
		StreamEvent{
			Event: "content_block_delta",
			Data: map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "text_delta", "text": notice},
			},
		},
		contentStop(index),
	)
	return events, nil
}

func (r *ResponseReducer) applyWebSearchItem(notification Notification) ([]StreamEvent, error) {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Query string `json:"query"`
		} `json:"item"`
	}
	if err := json.Unmarshal(notification.Params, &params); err != nil {
		return nil, errors.New("malformed web search item notification")
	}
	if params.Item.Type != "webSearch" {
		return nil, nil
	}
	if params.Item.ID == "" {
		return nil, errors.New("malformed web search item notification")
	}
	if err := r.acceptScope(params.ThreadID, params.TurnID); err != nil {
		return nil, err
	}
	if notification.Method == "item/started" {
		return nil, nil
	}
	query := params.Item.Query
	if query == "" {
		query = "web search"
	}

	events, toolUseID, err := r.startWebSearch(params.Item.ID, query)
	if err != nil {
		return nil, err
	}
	if notification.Method != "item/completed" || r.webSearchDone[params.Item.ID] {
		return events, nil
	}
	r.webSearchDone[params.Item.ID] = true
	events = append(events, r.closeCurrent()...)
	index := len(r.blocks)
	r.blocks = append(r.blocks, ResponseContentBlock{
		Type: "web_search_tool_result", ToolUseID: toolUseID, Content: []any{},
	})
	events = append(events,
		contentStart(index, map[string]any{
			"type": "web_search_tool_result", "tool_use_id": toolUseID,
			"content": []any{},
		}),
		contentStop(index),
	)
	return events, nil
}

func (r *ResponseReducer) startWebSearch(itemID, query string) ([]StreamEvent, string, error) {
	if toolUseID := r.webSearchToolUses[itemID]; toolUseID != "" {
		return nil, toolUseID, nil
	}
	input, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, "", err
	}
	toolUseID := "srvtoolu_wisp_" + itemID
	r.webSearchToolUses[itemID] = toolUseID
	r.webSearchRequests++

	events := r.Start()
	events = append(events, r.closeCurrent()...)
	index := len(r.blocks)
	r.blocks = append(r.blocks, ResponseContentBlock{
		Type: "server_tool_use", ID: toolUseID, Name: "web_search", Input: input,
	})
	events = append(events,
		contentStart(index, map[string]any{
			"type": "server_tool_use", "id": toolUseID,
			"name": "web_search", "input": map[string]any{},
		}),
		StreamEvent{
			Event: "content_block_delta",
			Data: map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{
					"type": "input_json_delta", "partial_json": string(input),
				},
			},
		},
		contentStop(index),
	)
	return events, toolUseID, nil
}

// AddToolCall emits and records one complete dynamic tool-use block.
func (r *ResponseReducer) AddToolCall(call DynamicToolCall) ([]StreamEvent, error) {
	if r.finished {
		return nil, errors.New("response is already finished")
	}
	if call.ID == "" || call.Name == "" {
		return nil, errors.New("dynamic tool call requires ID and name")
	}
	if !validJSONObject(call.Arguments) {
		return nil, errors.New("dynamic tool arguments must be an object")
	}
	arguments, err := compactJSON(call.Arguments)
	if err != nil {
		return nil, err
	}
	events := r.Start()
	events = append(events, r.closeCurrent()...)
	index := len(r.blocks)
	r.blocks = append(r.blocks, ResponseContentBlock{
		Type: "tool_use", ID: call.ID, Name: call.Name,
		Input: append(json.RawMessage(nil), call.Arguments...),
	})
	events = append(events,
		contentStart(index, map[string]any{
			"type": "tool_use", "id": call.ID, "name": call.Name,
			"input": map[string]any{},
		}),
		StreamEvent{
			Event: "content_block_delta",
			Data: map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{
					"type": "input_json_delta", "partial_json": arguments,
				},
			},
		},
		contentStop(index),
	)
	return events, nil
}

// Finish terminates the response with an Anthropic stop reason.
func (r *ResponseReducer) Finish(stopReason string) ([]StreamEvent, error) {
	if r.finished {
		return nil, errors.New("response is already finished")
	}
	switch stopReason {
	case "end_turn", "tool_use", "max_tokens", "stop_sequence":
	default:
		return nil, fmt.Errorf("unsupported Anthropic stop reason %q", stopReason)
	}
	if !r.usageSet {
		if r.options.EstimatedInputTokens <= 0 {
			return nil, errors.New("successful response is missing token usage or an input estimate")
		}
		r.usage = Usage{
			InputTokens:  r.options.EstimatedInputTokens,
			OutputTokens: r.estimatedOutputTokens(),
		}
	}
	if r.webSearchRequests > 0 {
		r.usage.ServerToolUse = &ServerToolUsage{WebSearchRequests: r.webSearchRequests}
	}
	events := r.Start()
	events = append(events, r.closeCurrent()...)
	r.stop = stopReason
	r.finished = true
	events = append(events,
		StreamEvent{
			Event: "message_delta",
			Data: map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": stopReason, "stop_sequence": nil,
				},
				"usage": Usage{
					OutputTokens: r.usage.OutputTokens, ServerToolUse: r.usage.ServerToolUse,
				},
			},
		},
		StreamEvent{Event: "message_stop", Data: map[string]any{"type": "message_stop"}},
	)
	return events, nil
}

// Message returns the non-streaming response after Finish.
func (r *ResponseReducer) Message() (AnthropicMessage, error) {
	if !r.finished {
		return AnthropicMessage{}, errors.New("response is not finished")
	}
	content := append([]ResponseContentBlock(nil), r.blocks...)
	return AnthropicMessage{
		ID: r.options.MessageID, Type: "message", Role: "assistant",
		Model: r.options.Model, Content: content, StopReason: r.stop, Usage: r.usage,
	}, nil
}

func (r *ResponseReducer) acceptScope(threadID, turnID string) error {
	if threadID == "" || turnID == "" {
		return errors.New("app-server event is missing thread or turn ID")
	}
	if r.options.ThreadID == "" {
		r.options.ThreadID = threadID
	} else if r.options.ThreadID != threadID {
		return fmt.Errorf("event belongs to different thread %q", threadID)
	}
	if r.options.TurnID == "" {
		r.options.TurnID = turnID
	} else if r.options.TurnID != turnID {
		return fmt.Errorf("event belongs to different turn %q", turnID)
	}
	return nil
}

func (r *ResponseReducer) closeCurrent() []StreamEvent {
	if r.current < 0 {
		return nil
	}
	index := r.current
	var events []StreamEvent
	if r.blocks[index].Type == "thinking" {
		events = append(events, StreamEvent{
			Event: "content_block_delta",
			Data: map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{
					"type": "signature_delta", "signature": r.blocks[index].Signature,
				},
			},
		})
	}
	events = append(events, contentStop(index))
	r.current = -1
	r.itemID = ""
	return events
}

func (r *ResponseReducer) estimatedOutputTokens() int64 {
	var runes int
	for _, block := range r.blocks {
		switch block.Type {
		case "text":
			runes += utf8.RuneCountInString(block.Text)
		case "thinking":
			runes += utf8.RuneCountInString(block.Thinking)
		case "tool_use", "server_tool_use":
			runes += len(block.Input)
		}
	}
	if runes == 0 {
		return 0
	}
	return int64((runes + 3) / 4)
}

func contentStart(index int, block any) StreamEvent {
	return StreamEvent{
		Event: "content_block_start",
		Data: map[string]any{
			"type": "content_block_start", "index": index, "content_block": block,
		},
	}
}

func contentStop(index int) StreamEvent {
	return StreamEvent{
		Event: "content_block_stop",
		Data:  map[string]any{"type": "content_block_stop", "index": index},
	}
}

// WriteSSE encodes Anthropic events in wire order.
func WriteSSE(writer io.Writer, events []StreamEvent) error {
	for _, event := range events {
		if event.Event == "" {
			return errors.New("SSE event name is empty")
		}
		payload, err := json.Marshal(event.Data)
		if err != nil {
			return fmt.Errorf("encode SSE %s: %w", event.Event, err)
		}
		var frame bytes.Buffer
		fmt.Fprintf(&frame, "event: %s\n", event.Event)
		frame.WriteString("data: ")
		frame.Write(payload)
		frame.WriteString("\n\n")
		if _, err := writer.Write(frame.Bytes()); err != nil {
			return fmt.Errorf("write SSE %s: %w", event.Event, err)
		}
		if flusher, ok := writer.(interface{ Flush() }); ok {
			flusher.Flush()
		}
	}
	return nil
}

// AnthropicErrorEvent creates an SSE error event after headers have begun.
func AnthropicErrorEvent(errorType, message string) StreamEvent {
	return StreamEvent{
		Event: "error",
		Data: map[string]any{
			"type":  "error",
			"error": map[string]any{"type": errorType, "message": message},
		},
	}
}
