package gptbridge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// MessagesRequest is the normalized subset of Anthropic's Messages request
// used by Claude Code.
type MessagesRequest struct {
	Model         string
	MaxTokens     int
	System        []ContentBlock
	Messages      []Message
	Tools         []Tool
	ToolChoice    ToolChoice
	Stream        bool
	Temperature   *float64
	TopP          *float64
	StopSequences []string
	Thinking      *Thinking
}

// Message is one normalized Anthropic transcript message.
type Message struct {
	Role    string
	Content []ContentBlock
}

// ContentBlock is one supported Anthropic content block.
type ContentBlock struct {
	Type string
	Text string

	Image *ImageSource

	ID    string
	Name  string
	Input json.RawMessage

	ToolUseID   string
	ToolContent []ContentBlock
	IsError     bool
}

// ImageSource is an Anthropic image source.
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}

// Tool is one Claude-hosted dynamic tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolChoice is Anthropic's tool-selection policy.
type ToolChoice struct {
	Type               string `json:"type"`
	Name               string `json:"name"`
	DisableParallelUse bool   `json:"disable_parallel_tool_use"`
}

// Thinking is Anthropic's extended-thinking request.
type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type wireMessagesRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system"`
	Messages      []wireMessage   `json:"messages"`
	Tools         []Tool          `json:"tools"`
	ToolChoice    ToolChoice      `json:"tool_choice"`
	Stream        bool            `json:"stream"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	StopSequences []string        `json:"stop_sequences"`
	Thinking      *Thinking       `json:"thinking"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type wireContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`

	Source json.RawMessage `json:"source"`

	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ParseMessagesRequest validates and normalizes an Anthropic Messages request.
func ParseMessagesRequest(payload []byte) (MessagesRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var wire wireMessagesRequest
	if err := decoder.Decode(&wire); err != nil {
		return MessagesRequest{}, fmt.Errorf("invalid Messages JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return MessagesRequest{}, errors.New("Messages request contains trailing JSON")
	}
	if wire.Model == "" {
		return MessagesRequest{}, errors.New("model is required")
	}
	if wire.MaxTokens <= 0 {
		return MessagesRequest{}, errors.New("max_tokens must be positive")
	}
	if len(wire.Messages) == 0 {
		return MessagesRequest{}, errors.New("messages must not be empty")
	}

	request := MessagesRequest{
		Model: wire.Model, MaxTokens: wire.MaxTokens, Tools: wire.Tools,
		ToolChoice: wire.ToolChoice, Stream: wire.Stream,
		Temperature: wire.Temperature, TopP: wire.TopP,
		StopSequences: wire.StopSequences, Thinking: wire.Thinking,
	}
	if len(bytes.TrimSpace(wire.System)) > 0 && !bytes.Equal(bytes.TrimSpace(wire.System), []byte("null")) {
		system, err := parseContent(wire.System, "system")
		if err != nil {
			return MessagesRequest{}, fmt.Errorf("system: %w", err)
		}
		for _, block := range system {
			if block.Type != "text" {
				return MessagesRequest{}, fmt.Errorf("system: unsupported content block %q", block.Type)
			}
		}
		request.System = system
	}
	for index, message := range wire.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return MessagesRequest{}, fmt.Errorf("messages[%d]: unsupported role %q", index, message.Role)
		}
		content, err := parseContent(message.Content, message.Role)
		if err != nil {
			return MessagesRequest{}, fmt.Errorf("messages[%d]: %w", index, err)
		}
		if len(content) == 0 {
			return MessagesRequest{}, fmt.Errorf("messages[%d]: content must not be empty", index)
		}
		request.Messages = append(request.Messages, Message{Role: message.Role, Content: content})
	}
	return request, nil
}

func parseContent(raw json.RawMessage, role string) ([]ContentBlock, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("content is required")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, errors.New("content must be a string or block array")
		}
		return []ContentBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return nil, errors.New("content must be a string or block array")
	}
	result := make([]ContentBlock, 0, len(blocks))
	for index, rawBlock := range blocks {
		var wire wireContentBlock
		if err := json.Unmarshal(rawBlock, &wire); err != nil {
			return nil, fmt.Errorf("content[%d] is not an object", index)
		}
		block := ContentBlock{Type: wire.Type}
		switch wire.Type {
		case "text":
			block.Text = wire.Text
		case "image":
			var source ImageSource
			if err := json.Unmarshal(wire.Source, &source); err != nil {
				return nil, fmt.Errorf("content[%d]: image source is invalid", index)
			}
			if _, err := imageURL(source); err != nil {
				return nil, fmt.Errorf("content[%d]: %w", index, err)
			}
			block.Image = &source
		case "tool_use":
			if role != "assistant" {
				return nil, fmt.Errorf("content[%d]: tool_use is valid only for assistant messages", index)
			}
			if wire.ID == "" || wire.Name == "" {
				return nil, fmt.Errorf("content[%d]: tool_use requires id and name", index)
			}
			if len(bytes.TrimSpace(wire.Input)) == 0 {
				wire.Input = json.RawMessage(`{}`)
			}
			if !validJSONObject(wire.Input) {
				return nil, fmt.Errorf("content[%d]: tool_use input must be an object", index)
			}
			block.ID, block.Name, block.Input = wire.ID, wire.Name, wire.Input
		case "tool_result":
			if role != "user" {
				return nil, fmt.Errorf("content[%d]: tool_result is valid only for user messages", index)
			}
			if wire.ToolUseID == "" {
				return nil, fmt.Errorf("content[%d]: tool_result requires tool_use_id", index)
			}
			toolContent, err := parseToolResultContent(wire.Content)
			if err != nil {
				return nil, fmt.Errorf("content[%d]: %w", index, err)
			}
			block.ToolUseID, block.ToolContent, block.IsError = wire.ToolUseID, toolContent, wire.IsError
		case "thinking", "redacted_thinking":
			if role != "assistant" {
				return nil, fmt.Errorf("content[%d]: %s is valid only for assistant messages", index, wire.Type)
			}
			// Thinking signatures are Anthropic-specific and cannot be replayed
			// into Responses history. Preserve the block type so translation can
			// intentionally omit it.
		default:
			return nil, fmt.Errorf("unsupported content block %q", wire.Type)
		}
		result = append(result, block)
	}
	return result, nil
}

func parseToolResultContent(raw json.RawMessage) ([]ContentBlock, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []ContentBlock{{Type: "text"}}, nil
	}
	content, err := parseContent(trimmed, "tool_result")
	if err != nil {
		return nil, err
	}
	for _, block := range content {
		if block.Type != "text" && block.Type != "image" {
			return nil, fmt.Errorf("unsupported tool_result content block %q", block.Type)
		}
	}
	return content, nil
}

func validJSONObject(raw json.RawMessage) bool {
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func imageURL(source ImageSource) (string, error) {
	switch source.Type {
	case "base64":
		switch source.MediaType {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return "", fmt.Errorf("unsupported image media type %q", source.MediaType)
		}
		if source.Data == "" {
			return "", errors.New("base64 image data is empty")
		}
		if _, err := base64.StdEncoding.DecodeString(source.Data); err != nil {
			return "", errors.New("base64 image data is invalid")
		}
		return "data:" + source.MediaType + ";base64," + source.Data, nil
	case "url":
		parsed, err := url.Parse(source.URL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return "", errors.New("image URL must be absolute HTTP(S)")
		}
		return source.URL, nil
	default:
		return "", fmt.Errorf("unsupported image source %q", source.Type)
	}
}

func normalizedSystem(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
