package statusline

import (
	"encoding/json"
	"math"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// RewriteContextWindow corrects the context_window object in Claude Code's
// statusline JSON for third-party subscription models. Claude Code hardcodes
// context_window_size to its own models' windows (200K, or 1M for extended
// context) and pre-computes used/remaining_percentage against that guess, so a
// pane running e.g. glm-5.2 (real window 1M) or gpt-5.6 (272K) shows a context
// figure that misstates reality. When the active model is in the subscription
// catalog, the real window replaces the guess and the percentages are
// recomputed against it; anything else — native Claude models, missing
// context_window, malformed JSON — passes through verbatim.
func RewriteContextWindow(raw []byte) (out []byte, changed bool) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return raw, false
	}
	model, _ := data["model"].(map[string]any)
	modelID, _ := model["id"].(string)
	realWindow, _, ok := claudeconfig.ModelLimit(modelID)
	if !ok || realWindow <= 0 {
		return raw, false
	}
	window, _ := data["context_window"].(map[string]any)
	if window == nil {
		return raw, false
	}

	oldSize, _ := window["context_window_size"].(float64)
	window["context_window_size"] = float64(realWindow)

	// Used tokens, best source first: the real per-kind counts (input + cache
	// creation + cache read — output excluded, matching Claude Code's own
	// used_percentage formula), then a plain numeric current_usage, then the
	// reported percentage un-scaled by the reported window.
	usedTokens := -1.0
	switch usage := window["current_usage"].(type) {
	case map[string]any:
		input, _ := usage["input_tokens"].(float64)
		creation, _ := usage["cache_creation_input_tokens"].(float64)
		read, _ := usage["cache_read_input_tokens"].(float64)
		usedTokens = input + creation + read
	case float64:
		usedTokens = usage
	default:
		if pct, ok := window["used_percentage"].(float64); ok && oldSize > 0 {
			usedTokens = pct / 100 * oldSize
		}
	}
	if usedTokens >= 0 {
		used := math.Round(usedTokens/float64(realWindow)*1000) / 10
		used = math.Max(0, math.Min(100, used))
		window["used_percentage"] = used
		window["remaining_percentage"] = 100 - used
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return raw, false
	}
	return encoded, true
}
