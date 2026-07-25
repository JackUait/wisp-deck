package gptbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// UserInput is an app-server turn/start input item.
type UserInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

// DynamicTool is an app-server host-provided function.
type DynamicTool struct {
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	OriginalName string          `json:"-"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	DeferLoading bool            `json:"deferLoading,omitempty"`
}

// ToolOutputItem is a dynamic-tool response content item.
type ToolOutputItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
}

// TranslatedToolResult is one Claude-executed tool result.
type TranslatedToolResult struct {
	ToolUseID           string
	Success             bool
	ContentItems        []ToolOutputItem
	HistoryContentItems []ToolOutputItem
}

// Translation contains the pure request translation consumed by the turn
// engine.
type Translation struct {
	Model         string
	MaxTokens     int
	System        string
	History       []map[string]any
	Input         []UserInput
	DynamicTools  []DynamicTool
	ToolDirective string
	ToolResults   []TranslatedToolResult
	Effort        string
	Stream        bool
}

// TranslateRequest converts a normalized Anthropic request into app-server
// history, turn input, dynamic tools, and any pending tool continuation.
func TranslateRequest(request MessagesRequest) (Translation, error) {
	if err := validateSampling(request); err != nil {
		return Translation{}, err
	}
	tools, directive, err := translateTools(request.Tools, request.ToolChoice)
	if err != nil {
		return Translation{}, err
	}
	translation := Translation{
		Model: request.Model, MaxTokens: request.MaxTokens,
		System: normalizedSystem(request.System), DynamicTools: tools,
		ToolDirective: directive, Effort: reasoningEffort(request.Thinking),
		Stream: request.Stream,
	}
	if err := translateMessages(request.Messages, &translation); err != nil {
		return Translation{}, err
	}
	return translation, nil
}

func validateSampling(request MessagesRequest) error {
	if request.Temperature != nil && *request.Temperature != 1 {
		return errors.New("temperature is not supported by the ChatGPT subscription bridge")
	}
	if request.TopP != nil && *request.TopP != 1 {
		return errors.New("top_p is not supported by the ChatGPT subscription bridge")
	}
	if len(request.StopSequences) != 0 {
		return errors.New("stop_sequences are not supported by the ChatGPT subscription bridge")
	}
	return nil
}

func translateTools(tools []Tool, choice ToolChoice) ([]DynamicTool, string, error) {
	usedNames := make(map[string]bool, len(tools))
	for index, tool := range tools {
		if tool.Name == "" {
			return nil, "", fmt.Errorf("tools[%d]: name is required", index)
		}
		if usedNames[tool.Name] {
			return nil, "", fmt.Errorf("tools[%d]: duplicate tool name %q", index, tool.Name)
		}
		if !validJSONObject(tool.InputSchema) {
			return nil, "", fmt.Errorf("tools[%d]: input_schema must be an object", index)
		}
		usedNames[tool.Name] = true
	}

	translated := make([]DynamicTool, 0, len(tools))
	byName := make(map[string]DynamicTool, len(tools))
	for _, tool := range tools {
		name := tool.Name
		if strings.HasPrefix(name, "mcp__") {
			name = availableDynamicToolName("wisp_"+name, usedNames)
			usedNames[name] = true
		}
		dynamic := DynamicTool{
			Type: "function", Name: name, OriginalName: tool.Name, Description: tool.Description,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
		}
		byName[tool.Name] = dynamic
		translated = append(translated, dynamic)
	}

	switch choice.Type {
	case "", "auto":
		if choice.DisableParallelUse && len(translated) > 0 {
			return translated, "Call at most one tool in this response.", nil
		}
		return translated, "", nil
	case "none":
		return nil, "", nil
	case "any":
		if len(translated) == 0 {
			return nil, "", errors.New("tool_choice any requires at least one tool")
		}
		directive := "You must call at least one available tool in this response."
		if choice.DisableParallelUse {
			directive += " Call exactly one tool."
		}
		return translated, directive, nil
	case "tool":
		tool, ok := byName[choice.Name]
		if !ok {
			return nil, "", fmt.Errorf("tool_choice names unknown tool %q", choice.Name)
		}
		return []DynamicTool{tool}, fmt.Sprintf("You must call %q in this response.", tool.Name), nil
	default:
		return nil, "", fmt.Errorf("unsupported tool_choice type %q", choice.Type)
	}
}

func availableDynamicToolName(base string, used map[string]bool) string {
	const maxBytes = 64
	for suffix := 1; ; suffix++ {
		disambiguator := ""
		if suffix > 1 {
			disambiguator = fmt.Sprintf("_%d", suffix)
		}
		prefix := base
		if limit := maxBytes - len(disambiguator); len(prefix) > limit {
			prefix = prefix[:limit]
		}
		candidate := prefix + disambiguator
		if !used[candidate] {
			return candidate
		}
	}
}

func reasoningEffort(thinking *Thinking) string {
	if thinking == nil || thinking.Type == "" || thinking.Type == "disabled" || thinking.Type == "adaptive" {
		return ""
	}
	if thinking.Type != "enabled" || thinking.BudgetTokens <= 0 {
		return ""
	}
	switch {
	case thinking.BudgetTokens <= 2048:
		return "low"
	case thinking.BudgetTokens <= 8192:
		return "medium"
	case thinking.BudgetTokens <= 16384:
		return "high"
	case thinking.BudgetTokens <= 32768:
		return "xhigh"
	case thinking.BudgetTokens <= 65536:
		return "max"
	default:
		return "ultra"
	}
}

func translateMessages(messages []Message, translation *Translation) error {
	if len(messages) == 0 {
		return errors.New("messages must not be empty")
	}
	if messages[len(messages)-1].Role != "user" {
		return errors.New("last message must have role user")
	}

	open := make(map[string]string)
	seen := make(map[string]bool)
	last := len(messages) - 1
	for index, message := range messages {
		final := index == last
		hasResult, _ := classifyBlocks(message.Content)
		if final {
			if hasResult {
				results, supplemental, err := normalizedFinalToolResults(message.Content)
				if err != nil {
					return err
				}
				for _, block := range results {
					name, ok := open[block.Live.ToolUseID]
					if !ok {
						return fmt.Errorf("tool_result %q has no pending tool_use", block.Live.ToolUseID)
					}
					_ = name
					delete(open, block.Live.ToolUseID)
					live, err := translateToolResult(block.Live)
					if err != nil {
						return err
					}
					history, err := translateToolResult(block.History)
					if err != nil {
						return err
					}
					live.HistoryContentItems = history.ContentItems
					translation.ToolResults = append(translation.ToolResults, live)
				}
				if len(supplemental) > 0 {
					input, err := translateUserInput(supplemental)
					if err != nil {
						return err
					}
					translation.Input = input
				}
			} else {
				if len(open) != 0 {
					return errors.New("assistant tool_use is missing a following tool_result")
				}
				input, err := translateUserInput(message.Content)
				if err != nil {
					return err
				}
				translation.Input = input
			}
			continue
		}

		items, err := translateHistoryMessage(message, open, seen)
		if err != nil {
			return fmt.Errorf("messages[%d]: %w", index, err)
		}
		translation.History = append(translation.History, items...)
	}
	if len(open) != 0 {
		return errors.New("assistant tool_use is missing a following tool_result")
	}
	return nil
}

func classifyBlocks(blocks []ContentBlock) (hasResult, hasOrdinary bool) {
	for _, block := range blocks {
		if block.Type == "tool_result" {
			hasResult = true
		} else {
			hasOrdinary = true
		}
	}
	return
}

type normalizedToolResult struct {
	Live    ContentBlock
	History ContentBlock
}

// normalizedFinalToolResults preserves both meanings of a mixed final user
// message. Ordinary blocks remain attached to the adjacent live tool response,
// while clean tool output and supplemental model input stay available if the
// continuation must be reconstructed on a fresh thread.
func normalizedFinalToolResults(blocks []ContentBlock) ([]normalizedToolResult, []ContentBlock, error) {
	results := make([]normalizedToolResult, 0, len(blocks))
	var leading []ContentBlock
	var supplemental []ContentBlock
	for _, block := range blocks {
		if block.Type == "tool_result" {
			history := block
			history.ToolContent = append([]ContentBlock(nil), block.ToolContent...)
			live := block
			live.ToolContent = append(
				append([]ContentBlock(nil), leading...), block.ToolContent...,
			)
			leading = nil
			results = append(results, normalizedToolResult{Live: live, History: history})
			continue
		}
		supplemental = append(supplemental, block)
		if len(results) == 0 {
			leading = append(leading, block)
			continue
		}
		last := &results[len(results)-1]
		last.Live.ToolContent = append(last.Live.ToolContent, block)
	}
	if len(leading) != 0 {
		// Unreachable in practice: the caller only enters this path when the
		// message contains at least one tool_result.
		return nil, nil, errors.New("final user content has no tool result to attach to")
	}
	return results, supplemental, nil
}

func translateHistoryMessage(message Message, open map[string]string, seen map[string]bool) ([]map[string]any, error) {
	var items []map[string]any
	var content []map[string]any
	flushMessage := func() {
		if len(content) == 0 {
			return
		}
		items = append(items, map[string]any{
			"type": "message", "role": message.Role, "content": content,
		})
		content = nil
	}

	for _, block := range message.Content {
		switch block.Type {
		case "text":
			contentType := "input_text"
			if message.Role == "assistant" {
				contentType = "output_text"
			}
			content = append(content, map[string]any{"type": contentType, "text": block.Text})
		case "image":
			if message.Role != "user" {
				return nil, errors.New("assistant image history is not supported")
			}
			image, err := imageURL(*block.Image)
			if err != nil {
				return nil, err
			}
			content = append(content, map[string]any{
				"type": "input_image", "image_url": image, "detail": "auto",
			})
		case "tool_use":
			if message.Role != "assistant" {
				return nil, errors.New("tool_use must belong to assistant")
			}
			flushMessage()
			if seen[block.ID] {
				return nil, fmt.Errorf("duplicate tool_use id %q", block.ID)
			}
			seen[block.ID] = true
			open[block.ID] = block.Name
			arguments, err := compactJSON(block.Input)
			if err != nil {
				return nil, fmt.Errorf("tool_use %q: %w", block.ID, err)
			}
			items = append(items, map[string]any{
				"type": "function_call", "call_id": block.ID,
				"name": block.Name, "arguments": arguments,
			})
		case "tool_result":
			if message.Role != "user" {
				return nil, errors.New("tool_result must belong to user")
			}
			flushMessage()
			if _, ok := open[block.ToolUseID]; !ok {
				return nil, fmt.Errorf("tool_result %q has no pending tool_use", block.ToolUseID)
			}
			delete(open, block.ToolUseID)
			output, err := historyToolOutput(block.ToolContent)
			if err != nil {
				return nil, err
			}
			items = append(items, map[string]any{
				"type": "function_call_output", "call_id": block.ToolUseID, "output": output,
			})
		case "thinking", "redacted_thinking":
			// Anthropic thinking signatures cannot be replayed to OpenAI.
		default:
			return nil, fmt.Errorf("unsupported content block %q", block.Type)
		}
	}
	flushMessage()
	return items, nil
}

func translateUserInput(blocks []ContentBlock) ([]UserInput, error) {
	input := make([]UserInput, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			input = append(input, UserInput{Type: "text", Text: block.Text})
		case "image":
			image, err := imageURL(*block.Image)
			if err != nil {
				return nil, err
			}
			input = append(input, UserInput{Type: "image", URL: image})
		default:
			return nil, fmt.Errorf("unsupported final user content block %q", block.Type)
		}
	}
	if len(input) == 0 {
		return nil, errors.New("final user message has no model input")
	}
	return input, nil
}

func translateToolResult(block ContentBlock) (TranslatedToolResult, error) {
	result := TranslatedToolResult{ToolUseID: block.ToolUseID, Success: !block.IsError}
	for _, content := range block.ToolContent {
		switch content.Type {
		case "text":
			result.ContentItems = append(result.ContentItems, ToolOutputItem{
				Type: "inputText", Text: content.Text,
			})
		case "image":
			image, err := imageURL(*content.Image)
			if err != nil {
				return TranslatedToolResult{}, err
			}
			result.ContentItems = append(result.ContentItems, ToolOutputItem{
				Type: "inputImage", ImageURL: image,
			})
		default:
			return TranslatedToolResult{}, fmt.Errorf("unsupported tool_result content %q", content.Type)
		}
	}
	return result, nil
}

func historyToolOutput(content []ContentBlock) (any, error) {
	if len(content) == 1 && content[0].Type == "text" {
		return content[0].Text, nil
	}
	items := make([]map[string]any, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "text":
			items = append(items, map[string]any{"type": "input_text", "text": block.Text})
		case "image":
			image, err := imageURL(*block.Image)
			if err != nil {
				return nil, err
			}
			items = append(items, map[string]any{
				"type": "input_image", "image_url": image, "detail": "auto",
			})
		default:
			return nil, fmt.Errorf("unsupported tool output block %q", block.Type)
		}
	}
	return items, nil
}

func compactJSON(raw json.RawMessage) (string, error) {
	var output bytes.Buffer
	if err := json.Compact(&output, raw); err != nil {
		return "", err
	}
	return output.String(), nil
}

func combinedDeveloperInstructions(system, directive string) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(system) != "" {
		parts = append(parts, system)
	}
	if strings.TrimSpace(directive) != "" {
		parts = append(parts, directive)
	}
	return strings.Join(parts, "\n\n")
}
