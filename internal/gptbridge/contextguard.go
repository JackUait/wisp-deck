package gptbridge

import (
	"strings"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// Claude Code owns the transcript but does not know Codex models' real context
// windows. The GPT adapter disables Claude's assumed-window enforcement for
// unrecognized model IDs so inherited sessions reach this model-aware guard.
// Codex's internal contextCompaction can otherwise mask growth until even
// ingesting the injected history fails, past the point where a compaction
// request could still fit. The only overflow signal Claude Code answers with
// compaction instead of a blind retry is a 400 invalid_request_error whose
// message says "prompt is too long". This guard therefore (a) predicts overflow
// against the catalog's real window before a turn ever reaches Codex, and (b)
// classifies Codex-reported overflow so the server maps it to that same error
// shape rather than a retryable 502.

const (
	promptGuardBytesPerToken = 4
	promptGuardImageTokens   = 1600
)

// isContextOverflowMessage reports whether an upstream error message means the
// model's context window was exhausted.
func isContextOverflowMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "context window") ||
		strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "out of room")
}

// modelContextWindow returns the model's real context window from the
// subscription catalog.
func modelContextWindow(model string) (int, bool) {
	window, _, ok := claudeconfig.ModelLimit(model)
	if !ok || window <= 0 {
		return 0, false
	}
	return window, true
}

// estimatePromptTokens approximates the Codex-side prompt cost of a request.
// Text-like content is counted at 4 bytes per token; images cost a flat
// per-image fee because their base64 payload is orders of magnitude larger
// than their token cost and would otherwise dominate the estimate.
func estimatePromptTokens(request MessagesRequest) int {
	textBytes := 0
	images := 0
	var walk func(blocks []ContentBlock)
	walk = func(blocks []ContentBlock) {
		for _, block := range blocks {
			textBytes += len(block.Text) + len(block.Name) + len(block.Input)
			if block.Image != nil {
				images++
				textBytes += len(block.Image.URL)
			}
			walk(block.ToolContent)
		}
	}
	walk(request.System)
	for _, message := range request.Messages {
		walk(message.Content)
	}
	for _, tool := range request.Tools {
		textBytes += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
	}
	tokens := textBytes/promptGuardBytesPerToken + images*promptGuardImageTokens
	if tokens < 1 {
		return 1
	}
	return tokens
}
