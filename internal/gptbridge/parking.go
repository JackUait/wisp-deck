package gptbridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	defaultParkLimit = 16
	defaultParkIdle  = 30 * time.Minute
)

// inputHistoryItem is the history item translate builds from this same user
// message on the next request, so its digest can be compared with that one.
func inputHistoryItem(input []UserInput) map[string]any {
	content := make([]map[string]any, 0, len(input))
	for _, item := range input {
		switch item.Type {
		case "text":
			content = append(content, map[string]any{"type": "input_text", "text": item.Text})
		case "image":
			content = append(content, map[string]any{"type": "input_image", "image_url": item.URL, "detail": "auto"})
		}
	}
	return map[string]any{"type": "message", "role": "user", "content": content}
}

// threadFingerprint covers everything fixed at thread/start: a thread started
// with other instructions, tools or web-search settings cannot serve the
// request. Web search sets config.web_search and the base instructions.
func threadFingerprint(t Translation) string {
	fixed, _ := json.Marshal([]any{
		t.Model, t.System, t.ToolDirective, t.DynamicTools,
		t.WebSearch, t.WebSearchAllowedDomains, t.WebSearchBlockedDomains,
	})
	sum := sha256.Sum256(fixed)
	return string(sum[:])
}

// park keeps a finished thread for the next message: Codex scopes the prompt
// cache to the thread, so a new one replays the whole history uncached.
// Caller holds state.mu.
func (e *Engine) park(state *engineTurn) {
	e.mu.Lock()
	delete(e.turns, state.threadID)
	e.parked = append(e.parked, state)
	// evictParked removes it from the pool: it deletes only what it finds there.
	var evict *engineTurn
	if len(e.parked) > e.options.ParkLimit {
		evict = e.parked[0]
	}
	state.parkTimer = time.AfterFunc(e.options.ParkIdle, func() { e.evictParked(state) })
	e.mu.Unlock()
	if evict != nil {
		e.evictParked(evict)
	}
}

// takeParked removes and returns the parked thread this request extends.
func (e *Engine) takeParked(t Translation) *engineTurn {
	if len(t.ToolResults) > 0 {
		return nil
	}
	fingerprint := threadFingerprint(t)
	digests := historyDigests(t.History)
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := len(e.parked) - 1; index >= 0; index-- {
		state := e.parked[index]
		if state.fingerprint != fingerprint || !extendsParked(state, t.History, digests) {
			continue
		}
		e.parked = append(e.parked[:index], e.parked[index+1:]...)
		if state.parkTimer != nil {
			state.parkTimer.Stop()
			state.parkTimer = nil
		}
		return state
	}
	return nil
}

func extendsParked(state *engineTurn, history []map[string]any, digests [][sha256.Size]byte) bool {
	if len(digests) < len(state.history) {
		return false
	}
	for index, digest := range state.history {
		if digests[index] != digest {
			return false
		}
	}
	rest := len(state.history)
	if state.expectInput {
		if rest >= len(digests) || digests[rest] != state.lastInput {
			return false
		}
		rest++
	}
	supplement := state.hasSupplement
	for index, item := range history[rest:] {
		switch item["type"] {
		case "function_call", "function_call_output":
		case "message":
			if item["role"] == "assistant" {
				continue
			}
			if !supplement || digests[rest+index] != state.supplement {
				return false
			}
			supplement = false
		default:
			return false
		}
	}
	return true
}

// evictParked deletes a parked thread. It does nothing for a thread that is
// no longer in the pool: an idle timer that fires just as takeParked hands the
// thread out must not delete a thread that is now running a turn.
func (e *Engine) evictParked(state *engineTurn) {
	e.mu.Lock()
	found := false
	for index, parked := range e.parked {
		if parked == state {
			e.parked = append(e.parked[:index], e.parked[index+1:]...)
			found = true
			break
		}
	}
	if found && state.parkTimer != nil {
		state.parkTimer.Stop()
		state.parkTimer = nil
	}
	e.mu.Unlock()
	if found {
		e.cleanupTurn(state, false)
	}
}

// continueParked runs the next turn on a parked thread: only the new input
// is sent, and the prefix is already in this thread's cache.
func (e *Engine) continueParked(ctx context.Context, state *engineTurn, translation Translation, emit func([]StreamEvent) error) (AnthropicMessage, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	// Fresh channels: anything the app-server sent while the thread was
	// parked belongs to no turn of ours.
	state.events = make(chan Notification, 256)
	state.requests = make(chan ServerRequest, 64)
	state.errors = make(chan error, 1)
	state.pending = make(map[string]*pendingDynamicTool)
	state.history = historyDigests(translation.History)
	state.lastInput = historyDigests([]map[string]any{inputHistoryItem(translation.Input)})[0]
	state.expectInput = true
	state.hasSupplement = false
	e.mu.Lock()
	e.turns[state.threadID] = state
	e.mu.Unlock()
	turnParams := map[string]any{
		"threadId": state.threadID, "input": translation.Input, "model": translation.Model,
		"approvalPolicy": "never", "environments": []any{},
		"sandboxPolicy": map[string]any{"type": "readOnly", "networkAccess": false},
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
