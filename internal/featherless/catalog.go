// Package featherless reads Featherless's remote model catalog: ~22,000
// HuggingFace models behind one API key, of which the ~15,500 reporting
// tool_use are the ones a Claude Code pane can actually use.
package featherless

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ClaudeCodeFloorTokens is what a Claude Code turn costs before the
// conversation has started. Measured 2026-09-02 by capturing the first request
// a live headless pane sends and counting it on a Qwen tokenizer: 26 tool
// schemas are 65,526 bytes of JSON, the system prompt 7,037, and the agent and
// skill rosters Claude Code appends to messages[] another 7,390 — ~20,000
// tokens with an empty project, no plugins and no MCP servers. This machine's
// full setup measured 23,165.
const ClaudeCodeFloorTokens = 20000

// MinContext is the narrowest window a pane can actually work in. A profile
// reserves a quarter of the window for the reply (see claudeconfig's output
// reserve), so the room left for the conversation is window - window/4 - floor,
// and that has to be at least as large as the floor itself. That puts the bar
// at 53,334 tokens; 65536 is the next power of two, and the catalog has nothing
// between 32768 and 131072 anyway.
//
// A 32768-token model — 88% of Featherless's tool-calling catalog — leaves
// 4,576 tokens for the whole conversation. One file read spends that, and
// because the endpoint reports no usage at all (see the proxy's usage repair)
// nothing compacts and nothing warns: the pane simply stops being able to work.
const MinContext = 65536

// Model is one Featherless model, reduced to what the picker renders and what
// picking one writes into a profile.
type Model struct {
	ID         string  `json:"id"`
	Class      string  `json:"class"`
	Context    int     `json:"context"`
	InPerM     float64 `json:"in_per_m"`
	OutPerM    float64 `json:"out_per_m"`
	ImageInput bool    `json:"image_input"`
	OnPlan     bool    `json:"on_plan"`
	Created    int64   `json:"created"`
}

// wireModel is the catalog's own shape. It is separate from Model because the
// cache stores Model, so a Featherless field rename cannot silently reshape
// what a cache written by an older build decodes into.
type wireModel struct {
	ID       string `json:"id"`
	Class    string `json:"model_class"`
	Context  int    `json:"context_length"`
	Created  int64  `json:"created"`
	Features struct {
		ToolUse    bool `json:"tool_use"`
		ImageInput bool `json:"image_input"`
	} `json:"features"`
	Pricing struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"pricing"`
	// A pointer because the field is absent on an unauthenticated listing, and
	// absent must read as available rather than as "not on your plan" — which
	// would empty the picker before the user has entered a key.
	OnPlan *bool `json:"available_on_current_plan"`
}

// Parse decodes a /v1/models body, keeping only models a Claude Code pane can
// use: tool calling is what lets it read and edit files, a declared context
// length is what keeps the session off the flat 200000 default that strands a
// small model permanently, and a window of at least MinContext is what leaves
// room to do anything once Claude Code's own floor is loaded.
func Parse(data []byte) ([]Model, error) {
	var body struct {
		Data []wireModel `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("featherless: parse catalog: %w", err)
	}
	if len(body.Data) == 0 {
		return nil, fmt.Errorf("featherless: catalog has no models")
	}
	models := make([]Model, 0, len(body.Data))
	for _, w := range body.Data {
		if !w.Features.ToolUse || w.Context < MinContext || w.ID == "" {
			continue
		}
		onPlan := true
		if w.OnPlan != nil {
			onPlan = *w.OnPlan
		}
		models = append(models, Model{
			ID:         w.ID,
			Class:      w.Class,
			Context:    w.Context,
			InPerM:     w.Pricing.Input,
			OutPerM:    w.Pricing.Output,
			ImageInput: w.Features.ImageInput,
			OnPlan:     onPlan,
			Created:    w.Created,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("featherless: catalog has no tool-calling models with a window of at least %d tokens", MinContext)
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Context != models[j].Context {
			return models[i].Context > models[j].Context
		}
		if models[i].Created != models[j].Created {
			return models[i].Created > models[j].Created
		}
		return models[i].ID < models[j].ID
	})
	return models, nil
}
