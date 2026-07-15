package opencodeadapter

import (
	"errors"
	"fmt"
	"math"

	"github.com/jackuait/wisp-deck/internal/attention"
)

const MaxModelEntries = 4096

type sessionInfo struct {
	parent    string
	hasParent bool
}

type pendingRequest struct {
	id        string
	sessionID string
	order     uint64
	announced bool
}

type terminalState struct {
	kind         string
	identity     string
	serial       uint64
	suppressIdle bool
}

type Reducer struct {
	generation  string
	invalid     bool
	sessions    map[string]sessionInfo
	statuses    map[string]string
	armed       map[string]bool
	terminal    map[string]terminalState
	pendingErr  map[string]terminalState
	seenErrors  map[string]bool
	questions   map[string]*pendingRequest
	permissions map[string]*pendingRequest
	order       uint64
	serial      uint64
	state       attention.State
}

func NewReducer(generation string) (*Reducer, error) {
	if !identifier(generation) {
		return nil, errors.New("invalid OpenCode attention generation")
	}
	return &Reducer{
		generation: generation,
		sessions:   make(map[string]sessionInfo), statuses: make(map[string]string),
		armed: make(map[string]bool), terminal: make(map[string]terminalState),
		pendingErr: make(map[string]terminalState), seenErrors: make(map[string]bool),
		questions: make(map[string]*pendingRequest), permissions: make(map[string]*pendingRequest),
		state: attention.State{Generation: generation, Phase: attention.PhaseUnknown, Reason: attention.ReasonNone},
	}, nil
}

func (r *Reducer) Apply(event Event) attention.State {
	if !r.invalid {
		r.applyMutation(event)
	}
	r.state = r.chooseState(true)
	return r.state
}

func (r *Reducer) ApplySnapshot(events []Event) attention.State {
	if r.invalid || len(r.sessions) != 0 || len(r.statuses) != 0 ||
		len(r.questions) != 0 || len(r.permissions) != 0 {
		return r.Invalidate()
	}
	for _, event := range events {
		if r.invalid {
			break
		}
		r.applyMutation(event)
	}
	r.state = r.chooseState(true)
	return r.state
}

func (r *Reducer) applyMutation(event Event) {
	switch event.Kind {
	case EventIgnored:
	case EventSessionUpsert:
		r.upsertSession(event)
	case EventSessionDeleted:
		r.deleteSessionTree(event.SessionID)
	case EventSessionStatus:
		r.updateStatus(event)
	case EventSessionError:
		r.updateError(event)
	case EventQuestionAsked:
		r.addPending(r.questions, event)
	case EventQuestionCleared:
		r.clearPending(r.questions, event)
	case EventPermissionAsked:
		r.addPending(r.permissions, event)
	case EventPermissionCleared:
		r.clearPending(r.permissions, event)
	default:
		r.invalid = true
	}
}

func (r *Reducer) Current() attention.State { return r.state }
func (r *Reducer) Invalid() bool            { return r.invalid }

func (r *Reducer) Invalidate() attention.State {
	r.invalid = true
	r.state = r.chooseState(false)
	return r.state
}

func (r *Reducer) upsertSession(event Event) {
	if previous, ok := r.sessions[event.SessionID]; ok && previous != (sessionInfo{parent: event.ParentID, hasParent: event.HasParent}) {
		r.invalid = true
		return
	}
	if _, ok := r.sessions[event.SessionID]; !ok && len(r.sessions) >= MaxModelEntries {
		r.invalid = true
		return
	}
	r.sessions[event.SessionID] = sessionInfo{parent: event.ParentID, hasParent: event.HasParent}
	for id := range r.sessions {
		_, _, cycle := r.rootFor(id)
		if cycle {
			r.invalid = true
			return
		}
	}
	r.reconcile()
}

func (r *Reducer) updateStatus(event Event) {
	if _, exists := r.statuses[event.SessionID]; !exists && len(r.statuses) >= MaxModelEntries {
		r.invalid = true
		return
	}
	r.statuses[event.SessionID] = event.Status
	root, resolved, _ := r.rootFor(event.SessionID)
	if !resolved || root != event.SessionID {
		return
	}
	if event.Status == "busy" || event.Status == "retry" {
		r.armed[root] = true
		delete(r.terminal, root)
		return
	}
	terminal, hasTerminal := r.terminal[root]
	if hasTerminal && terminal.kind == "error" && terminal.suppressIdle {
		terminal.suppressIdle = false
		r.terminal[root] = terminal
		delete(r.armed, root)
		return
	}
	if r.armed[root] && !r.hasPendingForRoot(root) {
		delete(r.armed, root)
		if r.serial == math.MaxUint64 {
			r.invalid = true
			return
		}
		r.serial++
		r.terminal[root] = terminalState{kind: "done", serial: r.serial,
			identity: fmt.Sprintf("done:%s:%d", root, r.serial)}
	}
}

func (r *Reducer) updateError(event Event) {
	if r.seenErrors[event.ID] {
		return
	}
	if len(r.seenErrors) >= MaxModelEntries || r.serial == math.MaxUint64 {
		r.invalid = true
		return
	}
	r.seenErrors[event.ID] = true
	r.serial++
	terminal := terminalState{kind: "error", serial: r.serial, suppressIdle: true,
		identity: "error:" + event.SessionID + ":" + event.ID}
	root, resolved, _ := r.rootFor(event.SessionID)
	if !resolved {
		if _, exists := r.pendingErr[event.SessionID]; !exists && len(r.pendingErr) >= MaxModelEntries {
			r.invalid = true
			return
		}
		r.pendingErr[event.SessionID] = terminal
		return
	}
	if root != event.SessionID {
		return
	}
	delete(r.armed, root)
	r.terminal[root] = terminal
}

func (r *Reducer) addPending(target map[string]*pendingRequest, event Event) {
	if previous, exists := target[event.RequestID]; exists {
		if previous.sessionID != event.SessionID {
			r.invalid = true
		}
		return
	}
	if len(target) >= MaxModelEntries || r.order == math.MaxUint64 {
		r.invalid = true
		return
	}
	r.order++
	target[event.RequestID] = &pendingRequest{id: event.RequestID, sessionID: event.SessionID, order: r.order}
}

func (r *Reducer) clearPending(target map[string]*pendingRequest, event Event) {
	previous, exists := target[event.RequestID]
	if exists && previous.sessionID != event.SessionID {
		r.invalid = true
		return
	}
	delete(target, event.RequestID)
}

func (r *Reducer) deleteSessionTree(sessionID string) {
	removed := map[string]bool{sessionID: true}
	changed := true
	for changed {
		changed = false
		for id, info := range r.sessions {
			if info.hasParent && removed[info.parent] && !removed[id] {
				removed[id], changed = true, true
			}
		}
	}
	for id := range removed {
		delete(r.sessions, id)
		delete(r.statuses, id)
		delete(r.armed, id)
		delete(r.terminal, id)
		delete(r.pendingErr, id)
	}
	for _, pending := range []map[string]*pendingRequest{r.questions, r.permissions} {
		for id, item := range pending {
			if removed[item.sessionID] {
				delete(pending, id)
			}
		}
	}
}

func (r *Reducer) reconcile() {
	for root := range r.armed {
		resolvedRoot, resolved, _ := r.rootFor(root)
		if !resolved || resolvedRoot != root {
			delete(r.armed, root)
		}
	}
	for root := range r.terminal {
		resolvedRoot, resolved, _ := r.rootFor(root)
		if !resolved || resolvedRoot != root {
			delete(r.terminal, root)
		}
	}
	for sessionID, status := range r.statuses {
		root, resolved, _ := r.rootFor(sessionID)
		if resolved && root == sessionID && (status == "busy" || status == "retry") {
			if _, terminal := r.terminal[root]; !terminal {
				r.armed[root] = true
			}
		}
	}
	for sessionID, pending := range r.pendingErr {
		root, resolved, _ := r.rootFor(sessionID)
		if !resolved {
			continue
		}
		delete(r.pendingErr, sessionID)
		if root == sessionID {
			delete(r.armed, root)
			r.terminal[root] = pending
		}
	}
}

func (r *Reducer) chooseState(mark bool) attention.State {
	state := attention.State{Generation: r.generation, Phase: attention.PhaseUnknown, Reason: attention.ReasonNone}
	if r.invalid || r.unresolved() {
		return state
	}
	if selected, exists := newestPending(r.questions); exists {
		state.Phase, state.Reason = attention.PhaseAttention, attention.ReasonQuestion
		if !selected.announced {
			state.Identity = "question:" + selected.id
			if mark {
				selected.announced = true
			}
		}
		return state
	}
	if selected, exists := newestPending(r.permissions); exists {
		state.Phase, state.Reason = attention.PhaseAttention, attention.ReasonPermission
		if !selected.announced {
			state.Identity = "permission:" + selected.id
			if mark {
				selected.announced = true
			}
		}
		return state
	}
	var newestError, newestDone terminalState
	for _, terminal := range r.terminal {
		if terminal.kind == "error" && terminal.serial > newestError.serial {
			newestError = terminal
		}
		if terminal.kind == "done" && terminal.serial > newestDone.serial {
			newestDone = terminal
		}
	}
	if newestError.serial != 0 {
		state.Phase, state.Reason, state.Identity = attention.PhaseAttention, attention.ReasonError, newestError.identity
		return state
	}
	if newestDone.serial != 0 {
		state.Phase, state.Reason, state.Identity = attention.PhaseAttention, attention.ReasonDone, newestDone.identity
		return state
	}
	roots := r.roots()
	for _, root := range roots {
		if status := r.statuses[root]; status == "busy" || status == "retry" {
			state.Phase = attention.PhaseWorking
			return state
		}
	}
	if len(roots) > 0 {
		allIdle := true
		for _, root := range roots {
			allIdle = allIdle && r.statuses[root] == "idle"
		}
		if allIdle {
			state.Phase = attention.PhaseReady
		}
	}
	return state
}

func (r *Reducer) unresolved() bool {
	for sessionID := range r.statuses {
		if _, resolved, _ := r.rootFor(sessionID); !resolved {
			return true
		}
	}
	for sessionID := range r.pendingErr {
		if _, resolved, _ := r.rootFor(sessionID); !resolved {
			return true
		}
	}
	for _, pending := range []map[string]*pendingRequest{r.questions, r.permissions} {
		for _, item := range pending {
			if _, resolved, _ := r.rootFor(item.sessionID); !resolved {
				return true
			}
		}
	}
	return false
}

func (r *Reducer) roots() []string {
	roots := make([]string, 0)
	for id, info := range r.sessions {
		if !info.hasParent {
			roots = append(roots, id)
		}
	}
	return roots
}

func (r *Reducer) rootFor(sessionID string) (string, bool, bool) {
	cursor := sessionID
	seen := make(map[string]bool)
	for {
		if seen[cursor] {
			return "", false, true
		}
		seen[cursor] = true
		info, exists := r.sessions[cursor]
		if !exists {
			return "", false, false
		}
		if !info.hasParent {
			return cursor, true, false
		}
		cursor = info.parent
	}
}

func (r *Reducer) hasPendingForRoot(root string) bool {
	for _, pending := range []map[string]*pendingRequest{r.questions, r.permissions} {
		for _, item := range pending {
			pendingRoot, resolved, _ := r.rootFor(item.sessionID)
			if !resolved || pendingRoot == root {
				return true
			}
		}
	}
	return false
}

func newestPending(pending map[string]*pendingRequest) (*pendingRequest, bool) {
	var selected *pendingRequest
	for _, item := range pending {
		if !item.announced && (selected == nil || item.order > selected.order) {
			selected = item
		}
	}
	if selected != nil {
		return selected, true
	}
	for _, item := range pending {
		return item, true
	}
	return nil, false
}
