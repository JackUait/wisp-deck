package codexadapter

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackuait/wisp-deck/internal/attention"
)

const (
	// MaxReducerEntries bounds each long-lived correlation collection. Overflow
	// latches unknown until a complete, bounded observer snapshot is installed.
	MaxReducerEntries = 4096
	// MaxReducerIDBytes bounds generation, thread, session, and OSC
	// observation identities.
	MaxReducerIDBytes = 256
	// MaxReducerCWDBytes bounds the launcher project and observed thread cwd.
	MaxReducerCWDBytes = 4096
)

// ThreadStatusType mirrors the stable Codex app-server ThreadStatus union.
type ThreadStatusType string

const (
	ThreadStatusNotLoaded   ThreadStatusType = "notLoaded"
	ThreadStatusIdle        ThreadStatusType = "idle"
	ThreadStatusSystemError ThreadStatusType = "systemError"
	ThreadStatusActive      ThreadStatusType = "active"
)

// ActiveFlag is one exact member of an active ThreadStatus.
type ActiveFlag string

const (
	ActiveWaitingOnApproval  ActiveFlag = "waitingOnApproval"
	ActiveWaitingOnUserInput ActiveFlag = "waitingOnUserInput"
)

// ThreadStatus is the validated serialized status supplied by Task 8's
// protocol decoder. ActiveFlags is valid only for type active.
type ThreadStatus struct {
	Type        ThreadStatusType
	ActiveFlags []ActiveFlag
}

// Thread contains only the app-server fields needed for passive correlation.
type Thread struct {
	ID             string
	SessionID      string
	ParentThreadID string // empty represents the protocol's null
	CWD            string
	Status         ThreadStatus
}

// interactionKind classifies the aggregate attention states exposed by
// ThreadStatus. Thread-scoped request delivery on the passive topology is not a
// stable or complete census, so these status epochs are its interaction truth.
type interactionKind string

const (
	interactionQuestion   interactionKind = "question"
	interactionPermission interactionKind = "permission"
	interactionError      interactionKind = "error"
)

// EventKind identifies one serialized reducer input. Network decoding and
// WebSocket ownership deliberately remain outside this pure state machine.
type EventKind uint8

const (
	EventThreadObserved EventKind = iota + 1
	EventThreadStatus
	EventThreadClosed
	EventOSC9Completion
	EventObserverLost
	EventObserverUnavailable
	EventObserverSnapshot
)

// ReducerEvent is the single event-loop input. Only fields named by Kind are
// read. Identity for EventOSC9Completion must be a supervisor-supplied
// monotonic observation identity, not the fixed OSC message text.
type ReducerEvent struct {
	Kind     EventKind
	Thread   Thread
	ThreadID string
	Status   ThreadStatus
	Identity string
	Threads  []Thread
}

// ReducerConfig fixes correlation to one Wisp launch. ResumeThreadID, when
// present, is authoritative. A fresh launch instead selects exactly one new
// top-level thread outside BaselineThreadIDs whose cwd equals ProjectCWD.
type ReducerConfig struct {
	Generation        string
	ProjectCWD        string
	ResumeThreadID    string
	BaselineThreadIDs []string
}

type statusRequestKey struct {
	threadID string
	kind     interactionKind
}

// statusEpoch remembers whether one aggregate (thread, kind) epoch already
// alerted. uncertain is set when the same status spans missing evidence: the
// passive topology cannot tell continuity from an unseen 1 -> 0 -> 1 cycle.
type statusEpoch struct {
	announced bool
	uncertain bool
}

// Reducer is mutated only by its owner's serialized event loop.
type Reducer struct {
	generation string
	projectCWD string
	resumeID   string
	baseline   map[string]struct{}

	rootID       string
	threads      map[string]Thread
	status       map[statusRequestKey]statusEpoch
	closed       map[string]struct{}
	ambiguous    bool
	reliable     bool
	observerLost bool
	oscOnly      bool

	rootArmed            bool
	completionPending    bool
	completionDuringLoss bool
	completionID         string
	state                attention.State
}

// NewReducer constructs a deterministic reducer without starting I/O.
func NewReducer(config ReducerConfig) (*Reducer, error) {
	if err := validateReducerField("generation", config.Generation, MaxReducerIDBytes, false); err != nil {
		return nil, err
	}
	if err := validateReducerField("project cwd", config.ProjectCWD, MaxReducerCWDBytes, false); err != nil {
		return nil, err
	}
	if config.ResumeThreadID != "" {
		if err := validateReducerField("resume thread id", config.ResumeThreadID, MaxReducerIDBytes, false); err != nil {
			return nil, err
		}
		if err := validateCanonicalUUID("resume thread id", config.ResumeThreadID); err != nil {
			return nil, err
		}
	}
	if len(config.BaselineThreadIDs) > MaxReducerEntries {
		return nil, fmt.Errorf("baseline contains %d thread ids, limit %d", len(config.BaselineThreadIDs), MaxReducerEntries)
	}

	baseline := make(map[string]struct{}, len(config.BaselineThreadIDs))
	for _, id := range config.BaselineThreadIDs {
		if err := validateReducerField("baseline thread id", id, MaxReducerIDBytes, false); err != nil {
			return nil, err
		}
		baseline[id] = struct{}{}
	}

	r := &Reducer{
		generation: config.Generation,
		projectCWD: config.ProjectCWD,
		resumeID:   config.ResumeThreadID,
		baseline:   baseline,
		rootID:     config.ResumeThreadID,
		threads:    make(map[string]Thread),
		status:     make(map[statusRequestKey]statusEpoch),
		closed:     make(map[string]struct{}),
		reliable:   true,
		state: attention.State{
			Generation: config.Generation,
			Phase:      attention.PhaseUnknown,
			Reason:     attention.ReasonNone,
		},
	}
	return r, nil
}

// Reduce applies one already-decoded event and returns the complete current
// normalized state. Callers serialize all calls through one event loop.
func (r *Reducer) Reduce(event ReducerEvent) attention.State {
	switch event.Kind {
	case EventThreadObserved:
		r.observeThread(event.Thread)
	case EventThreadStatus:
		r.observeStatus(event.ThreadID, event.Status)
	case EventThreadClosed:
		r.closeThread(event.ThreadID)
	case EventOSC9Completion:
		r.observeCompletion(event.Identity)
	case EventObserverLost:
		r.loseObserver()
	case EventObserverUnavailable:
		r.useOSCOnly()
	case EventObserverSnapshot:
		r.installSnapshot(event.Threads)
	default:
		r.latchUnknown()
	}
	return r.recompute()
}

// Current returns the last reduced state.
func (r *Reducer) Current() attention.State {
	return r.state
}

// RootThreadID returns the exact correlated root, or empty while fresh
// correlation is unresolved or ambiguous.
func (r *Reducer) RootThreadID() string {
	return r.rootID
}

// Ambiguous reports whether fresh root selection saw multiple candidates.
func (r *Reducer) Ambiguous() bool {
	return r.ambiguous
}

func (r *Reducer) observeThread(thread Thread) {
	if err := validateReducerField("thread id", thread.ID, MaxReducerIDBytes, false); err != nil {
		r.latchUnknown()
		return
	}
	if _, stale := r.closed[thread.ID]; stale {
		return
	}
	if err := validateThread(thread); err != nil {
		r.latchUnknown()
		return
	}
	if err := r.validateRootRecord(thread); err != nil {
		r.latchUnknown()
		return
	}
	if previous, exists := r.threads[thread.ID]; exists {
		if previous.SessionID != thread.SessionID || previous.ParentThreadID != thread.ParentThreadID {
			r.latchUnknown()
			return
		}
	}
	if _, exists := r.threads[thread.ID]; !exists && len(r.threads) >= MaxReducerEntries {
		r.latchUnknown()
		return
	}
	r.threads[thread.ID] = cloneThread(thread)
	if hasParentCycle(r.threads) || hasParentSessionContradiction(r.threads) {
		r.latchUnknown()
		return
	}

	if r.resumeID == "" && !r.ambiguous && r.isFreshRootCandidate(thread) {
		if err := validateCanonicalUUID("fresh root thread id", thread.ID); err != nil {
			r.latchUnknown()
			return
		}
		switch {
		case r.rootID == "":
			r.rootID = thread.ID
		case r.rootID != thread.ID:
			r.rootID = ""
			r.ambiguous = true
		}
	}

	if r.threadIsCorrelated(thread.ID) && thread.Status.Type == ThreadStatusActive {
		r.completionPending = false
		r.completionDuringLoss = false
		r.completionID = ""
		if thread.ID == r.rootID {
			r.rootArmed = true
		}
	}
}

func (r *Reducer) observeStatus(threadID string, status ThreadStatus) {
	if err := validateReducerField("thread id", threadID, MaxReducerIDBytes, false); err != nil {
		r.latchUnknown()
		return
	}
	if _, stale := r.closed[threadID]; stale {
		return
	}
	if err := validateThreadStatus(status); err != nil {
		r.latchUnknown()
		return
	}
	if !r.threadIsCorrelated(threadID) {
		return
	}
	thread, ok := r.threads[threadID]
	if !ok {
		return
	}
	thread.Status = cloneStatus(status)
	r.threads[threadID] = thread
	if status.Type == ThreadStatusActive {
		r.completionPending = false
		r.completionDuringLoss = false
		r.completionID = ""
		if threadID == r.rootID {
			r.rootArmed = true
		}
	}
}

func (r *Reducer) closeThread(threadID string) {
	if err := validateReducerField("thread id", threadID, MaxReducerIDBytes, false); err != nil {
		r.latchUnknown()
		return
	}
	if _, exists := r.closed[threadID]; !exists {
		if len(r.closed) >= MaxReducerEntries {
			r.latchUnknown()
			return
		}
		r.closed[threadID] = struct{}{}
	}
	delete(r.threads, threadID)
	for key := range r.status {
		if key.threadID == threadID {
			delete(r.status, key)
		}
	}
	if threadID == r.rootID {
		r.rootArmed = false
		r.completionPending = false
		r.completionDuringLoss = false
		r.completionID = ""
	}
}

func (r *Reducer) observeCompletion(identity string) {
	if err := validateReducerField("OSC observation identity", identity, MaxReducerIDBytes, false); err != nil {
		return
	}
	if r.oscOnly {
		r.completionPending = true
		r.completionDuringLoss = false
		r.completionID = identity
		return
	}
	if r.observerLost {
		r.completionPending = true
		r.completionDuringLoss = true
		r.completionID = identity
		return
	}
	if !r.rootArmed {
		return
	}
	r.rootArmed = false
	r.completionPending = true
	r.completionDuringLoss = false
	r.completionID = identity
}

func (r *Reducer) loseObserver() {
	r.reliable = false
	r.observerLost = true
	r.oscOnly = false
	r.rootArmed = false
	r.completionPending = false
	r.completionDuringLoss = false
	r.completionID = ""
	// Keep the attention status epochs privately. A reconnect snapshot needs them
	// to distinguish a definitely new 0 -> 1 transition from an ambiguous status
	// that was already 1 before observation was lost.
}

func (r *Reducer) useOSCOnly() {
	r.reliable = false
	r.observerLost = false
	r.oscOnly = true
	r.rootArmed = false
	r.completionPending = false
	r.completionDuringLoss = false
	r.completionID = ""
	r.status = make(map[statusRequestKey]statusEpoch)
}

func (r *Reducer) installSnapshot(threads []Thread) {
	wasLost := r.observerLost
	previousStatus := r.status
	outageCompletion := r.observerLost && r.completionPending && r.completionDuringLoss
	outageIdentity := r.completionID
	if len(threads) > MaxReducerEntries {
		r.latchUnknown()
		return
	}
	next := make(map[string]Thread, len(threads))
	for _, thread := range threads {
		if err := validateThread(thread); err != nil {
			r.latchUnknown()
			return
		}
		if _, duplicate := next[thread.ID]; duplicate {
			r.latchUnknown()
			return
		}
		if err := r.validateRootRecord(thread); err != nil {
			r.latchUnknown()
			return
		}
		if r.resumeID == "" && r.isFreshRootCandidate(thread) {
			if err := validateCanonicalUUID("fresh root thread id", thread.ID); err != nil {
				r.latchUnknown()
				return
			}
		}
		next[thread.ID] = cloneThread(thread)
	}
	if hasParentCycle(next) || hasParentSessionContradiction(next) {
		r.latchUnknown()
		return
	}

	nextRoot := ""
	nextAmbiguous := false
	if r.resumeID != "" {
		nextRoot = r.resumeID
	} else {
		candidates := make([]string, 0, 2)
		for _, thread := range next {
			if r.isFreshRootCandidate(thread) {
				candidates = append(candidates, thread.ID)
			}
		}
		sort.Strings(candidates)
		switch len(candidates) {
		case 0:
		case 1:
			nextRoot = candidates[0]
		default:
			nextAmbiguous = true
		}
	}

	correlated := correlatedThreadIDsFor(next, nextRoot)
	correlationTrusted := !nextAmbiguous && nextRoot != ""
	if _, rootPresent := next[nextRoot]; !rootPresent {
		correlationTrusted = false
	}
	if r.hasLostProjectAncestryIn(next, correlated) {
		correlationTrusted = false
	}

	var nextStatus map[statusRequestKey]statusEpoch
	if !correlationTrusted {
		// An uncorrelated barrier cannot prove that any prior status cleared. Carry
		// every epoch forward privately so a later clean barrier cannot re-alert it.
		nextStatus = make(map[statusRequestKey]statusEpoch, len(previousStatus))
		for key, epoch := range previousStatus {
			epoch.uncertain = true
			nextStatus[key] = epoch
		}
	} else {
		activeStatus := aggregateStatusKeysFor(next, correlated)
		if len(activeStatus) > MaxReducerEntries {
			r.latchUnknown()
			return
		}
		unknownStatusThreads := notLoadedThreadIDsFor(next, correlated)
		nextStatus = make(map[statusRequestKey]statusEpoch, len(activeStatus)+len(previousStatus))
		for key := range activeStatus {
			epoch, existed := previousStatus[key]
			if wasLost && existed {
				// The status might be the old epoch or a new epoch hidden by a 1 -> 0 -> 1
				// cycle during the outage. Suppress both guesses until a clear is seen.
				epoch.uncertain = true
			}
			nextStatus[key] = epoch
		}
		for key, epoch := range previousStatus {
			if _, unknown := unknownStatusThreads[key.threadID]; !unknown {
				continue
			}
			if _, exists := nextStatus[key]; !exists && len(nextStatus) >= MaxReducerEntries {
				r.latchUnknown()
				return
			}
			// notLoaded carries no evidence that an existing status cleared. Keep
			// the epoch private and uncertain until a known status omits that kind.
			epoch.uncertain = true
			nextStatus[key] = epoch
		}
	}

	// Commit the validated thread barrier and its derived aggregate epochs at
	// once. There is deliberately no request census on this passive connection.
	r.threads = next
	r.status = nextStatus
	r.closed = make(map[string]struct{})
	r.reliable = true
	r.observerLost = false
	r.oscOnly = false
	r.ambiguous = nextAmbiguous
	r.rootID = nextRoot
	r.rootArmed = false
	r.completionPending = false
	r.completionDuringLoss = false
	r.completionID = ""

	if root, ok := r.threads[r.rootID]; ok && root.Status.Type == ThreadStatusActive {
		r.rootArmed = true
	}
	if outageCompletion && r.snapshotIsCleanIdle() {
		r.completionPending = true
		r.completionDuringLoss = false
		r.completionID = outageIdentity
	}
}

func (r *Reducer) latchUnknown() {
	for key, epoch := range r.status {
		epoch.uncertain = true
		r.status[key] = epoch
	}
	r.reliable = false
	r.observerLost = false
	r.oscOnly = false
	r.rootArmed = false
	r.completionPending = false
	r.completionDuringLoss = false
	r.completionID = ""
}

func (r *Reducer) recompute() attention.State {
	unknown := func() attention.State {
		return attention.State{
			Generation: r.generation,
			Phase:      attention.PhaseUnknown,
			Reason:     attention.ReasonNone,
		}
	}
	if r.oscOnly {
		if r.completionPending {
			r.state = r.attentionState(attention.ReasonDone, "osc:"+r.completionID)
		} else {
			r.state = unknown()
		}
		return r.state
	}
	if r.observerLost {
		r.state = unknown()
		return r.state
	}
	if !r.reliable || r.ambiguous || r.rootID == "" {
		r.state = unknown()
		return r.state
	}
	if _, ok := r.threads[r.rootID]; !ok {
		r.state = unknown()
		return r.state
	}

	correlated := r.correlatedThreadIDs()
	if r.hasLostProjectAncestry(correlated) {
		r.state = unknown()
		return r.state
	}
	questionStatuses := make(map[string]struct{})
	permissionStatuses := make(map[string]struct{})
	errorStatuses := make(map[string]struct{})
	unknownStatusThreads := make(map[string]struct{})
	anyActive := false
	anyUnknown := false

	for id := range correlated {
		thread, ok := r.threads[id]
		if !ok {
			continue
		}
		switch thread.Status.Type {
		case ThreadStatusActive:
			anyActive = true
			for _, flag := range thread.Status.ActiveFlags {
				switch flag {
				case ActiveWaitingOnUserInput:
					questionStatuses[id] = struct{}{}
				case ActiveWaitingOnApproval:
					permissionStatuses[id] = struct{}{}
				}
			}
		case ThreadStatusSystemError:
			errorStatuses[id] = struct{}{}
		case ThreadStatusIdle:
		case ThreadStatusNotLoaded:
			anyUnknown = true
			unknownStatusThreads[id] = struct{}{}
		}
	}
	activeStatus := make(map[statusRequestKey]struct{}, len(questionStatuses)+len(permissionStatuses)+len(errorStatuses))
	for threadID := range questionStatuses {
		activeStatus[statusRequestKey{threadID: threadID, kind: interactionQuestion}] = struct{}{}
	}
	for threadID := range permissionStatuses {
		activeStatus[statusRequestKey{threadID: threadID, kind: interactionPermission}] = struct{}{}
	}
	for threadID := range errorStatuses {
		activeStatus[statusRequestKey{threadID: threadID, kind: interactionError}] = struct{}{}
	}
	if !r.syncStatusEpochs(activeStatus, unknownStatusThreads) {
		r.state = unknown()
		return r.state
	}

	if state, ok := r.statusAttentionState(
		interactionQuestion, attention.ReasonQuestion, questionStatuses,
	); ok {
		r.state = state
		return r.state
	}
	if r.hasUncertainStatusEpoch(interactionQuestion) {
		// Question dominates permission even when its epoch cannot be trusted.
		r.state = unknown()
		return r.state
	}
	if state, ok := r.statusAttentionState(
		interactionPermission, attention.ReasonPermission, permissionStatuses,
	); ok {
		r.state = state
		return r.state
	}
	if r.hasUncertainStatusEpoch(interactionPermission) {
		// A flag spanning a connection outage cannot safely be called either the
		// old epoch or a new one. Stay unknown until a status update clears it.
		r.state = unknown()
		return r.state
	}
	if state, ok := r.statusAttentionState(
		interactionError, attention.ReasonError, errorStatuses,
	); ok {
		r.state = state
		return r.state
	}

	switch {
	case r.hasUncertainStatusEpoch(interactionError):
		// A system-error status with missing evidence cannot safely be re-alerted
		// until a known status proves that its prior epoch cleared.
		r.state = unknown()
	case r.completionPending:
		r.state = r.attentionState(attention.ReasonDone, "osc:"+r.completionID)
	case anyActive:
		r.state = attention.State{Generation: r.generation, Phase: attention.PhaseWorking, Reason: attention.ReasonNone}
	case !anyUnknown:
		r.state = attention.State{Generation: r.generation, Phase: attention.PhaseReady, Reason: attention.ReasonNone}
	default:
		r.state = unknown()
	}
	return r.state
}

// syncStatusEpochs closes entries as soon as a known status clears their kind
// and creates a fresh, alertable epoch on the next observed 0 -> 1 transition.
func (r *Reducer) syncStatusEpochs(
	active map[statusRequestKey]struct{},
	unknownThreads map[string]struct{},
) bool {
	for key, epoch := range r.status {
		if _, exists := active[key]; !exists {
			if _, unknown := unknownThreads[key.threadID]; unknown {
				epoch.uncertain = true
				r.status[key] = epoch
				continue
			}
			delete(r.status, key)
		}
	}
	for key := range active {
		if epoch, exists := r.status[key]; exists {
			if epoch.uncertain && !epoch.announced {
				// Whether the status continued or restarted while evidence was missing,
				// it has never alerted. The exact active observation is safely alertable.
				epoch.uncertain = false
				r.status[key] = epoch
			}
			continue
		}
		if len(r.status) >= MaxReducerEntries {
			r.latchUnknown()
			return false
		}
		r.status[key] = statusEpoch{}
	}
	return true
}

func (r *Reducer) statusAttentionState(
	kind interactionKind,
	reason attention.Reason,
	statusThreads map[string]struct{},
) (attention.State, bool) {
	unannounced := make(map[string]struct{})
	certain := make(map[string]struct{})
	for threadID := range statusThreads {
		key := statusRequestKey{threadID: threadID, kind: kind}
		epoch := r.status[key]
		if epoch.uncertain {
			continue
		}
		certain[threadID] = struct{}{}
		if !epoch.announced {
			unannounced[threadID] = struct{}{}
			epoch.announced = true
			r.status[key] = epoch
		}
	}
	if len(unannounced) > 0 {
		identity := string(kind) + ":" + sortedSet(unannounced)
		if r.state.Phase == attention.PhaseAttention && r.state.Reason == reason && r.state.Identity == identity {
			// Another same-kind blocker may have kept attention continuously visible
			// while this exact (thread, kind) epoch cleared and re-entered. Toggle the
			// identity so the new 0 -> 1 transition still alerts exactly once.
			identity += ":epoch"
		}
		return r.attentionState(reason, identity), true
	}
	if len(certain) == 0 {
		return attention.State{}, false
	}
	identity := string(kind) + ":" + sortedSet(certain)
	if r.state.Phase == attention.PhaseAttention && r.state.Reason == reason {
		identity = r.state.Identity
	}
	return r.attentionState(reason, identity), true
}

func (r *Reducer) hasUncertainStatusEpoch(kind interactionKind) bool {
	for key, epoch := range r.status {
		if key.kind == kind && epoch.uncertain {
			return true
		}
	}
	return false
}

func (r *Reducer) hasLostProjectAncestry(correlated map[string]struct{}) bool {
	return r.hasLostProjectAncestryIn(r.threads, correlated)
}

func (r *Reducer) hasLostProjectAncestryIn(
	threads map[string]Thread,
	correlated map[string]struct{},
) bool {
	for id, thread := range threads {
		if _, ok := correlated[id]; ok {
			continue
		}
		if _, existedAtLaunch := r.baseline[id]; existedAtLaunch {
			continue
		}
		if thread.ParentThreadID != "" && thread.CWD == r.projectCWD {
			return true
		}
	}
	return false
}

func (r *Reducer) attentionState(reason attention.Reason, identity string) attention.State {
	return attention.State{
		Generation: r.generation,
		Phase:      attention.PhaseAttention,
		Reason:     reason,
		Identity:   identity,
	}
}

func (r *Reducer) isFreshRootCandidate(thread Thread) bool {
	if thread.ParentThreadID != "" || thread.CWD != r.projectCWD {
		return false
	}
	_, stale := r.baseline[thread.ID]
	return !stale
}

func (r *Reducer) validateRootRecord(thread Thread) error {
	if r.resumeID == "" || thread.ID != r.resumeID {
		return nil
	}
	if thread.ParentThreadID != "" {
		return errors.New("resume root must be top-level")
	}
	if thread.CWD != r.projectCWD {
		return fmt.Errorf("resume root cwd %q does not match project cwd %q", thread.CWD, r.projectCWD)
	}
	return validateCanonicalUUID("resume root thread id", thread.ID)
}

func (r *Reducer) snapshotIsCleanIdle() bool {
	if !r.reliable || r.ambiguous || r.rootID == "" {
		return false
	}
	if _, ok := r.threads[r.rootID]; !ok {
		return false
	}
	correlated := r.correlatedThreadIDs()
	if r.hasLostProjectAncestry(correlated) {
		return false
	}
	for id := range correlated {
		if r.threads[id].Status.Type != ThreadStatusIdle {
			return false
		}
	}
	return true
}

func (r *Reducer) threadIsCorrelated(threadID string) bool {
	_, ok := r.correlatedThreadIDs()[threadID]
	return ok
}

func (r *Reducer) correlatedThreadIDs() map[string]struct{} {
	return correlatedThreadIDsFor(r.threads, r.rootID)
}

func aggregateStatusKeysFor(
	threads map[string]Thread,
	correlated map[string]struct{},
) map[statusRequestKey]struct{} {
	active := make(map[statusRequestKey]struct{})
	for threadID := range correlated {
		status := threads[threadID].Status
		switch status.Type {
		case ThreadStatusActive:
			for _, flag := range status.ActiveFlags {
				switch flag {
				case ActiveWaitingOnUserInput:
					active[statusRequestKey{threadID: threadID, kind: interactionQuestion}] = struct{}{}
				case ActiveWaitingOnApproval:
					active[statusRequestKey{threadID: threadID, kind: interactionPermission}] = struct{}{}
				}
			}
		case ThreadStatusSystemError:
			active[statusRequestKey{threadID: threadID, kind: interactionError}] = struct{}{}
		}
	}
	return active
}

func notLoadedThreadIDsFor(
	threads map[string]Thread,
	correlated map[string]struct{},
) map[string]struct{} {
	unknown := make(map[string]struct{})
	for threadID := range correlated {
		if threads[threadID].Status.Type == ThreadStatusNotLoaded {
			unknown[threadID] = struct{}{}
		}
	}
	return unknown
}

func correlatedThreadIDsFor(threads map[string]Thread, rootID string) map[string]struct{} {
	correlated := make(map[string]struct{})
	root, rootKnown := threads[rootID]
	if rootID == "" || !rootKnown {
		return correlated
	}
	correlated[rootID] = struct{}{}

	children := make(map[string][]string, len(threads))
	queue := []string{rootID}
	for id, thread := range threads {
		if thread.ParentThreadID != "" {
			children[thread.ParentThreadID] = append(children[thread.ParentThreadID], id)
		}
		if id != rootID && thread.ParentThreadID != "" && thread.SessionID == root.SessionID {
			correlated[id] = struct{}{}
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if _, known := correlated[child]; known {
				continue
			}
			correlated[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return correlated
}

func validateThread(thread Thread) error {
	if err := validateReducerField("thread id", thread.ID, MaxReducerIDBytes, false); err != nil {
		return err
	}
	if err := validateReducerField("thread session id", thread.SessionID, MaxReducerIDBytes, false); err != nil {
		return err
	}
	if err := validateReducerField("thread parent id", thread.ParentThreadID, MaxReducerIDBytes, true); err != nil {
		return err
	}
	if thread.ParentThreadID == thread.ID {
		return errors.New("thread cannot parent itself")
	}
	if err := validateReducerField("thread cwd", thread.CWD, MaxReducerCWDBytes, false); err != nil {
		return err
	}
	return validateThreadStatus(thread.Status)
}

func validateCanonicalUUID(name, value string) error {
	if len(value) != 36 {
		return fmt.Errorf("%s is not a canonical UUID", name)
	}
	for index, b := range []byte(value) {
		switch index {
		case 8, 13, 18, 23:
			if b != '-' {
				return fmt.Errorf("%s is not a canonical UUID", name)
			}
		default:
			if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')) {
				return fmt.Errorf("%s is not a canonical UUID", name)
			}
		}
	}
	return nil
}

func validateThreadStatus(status ThreadStatus) error {
	if len(status.ActiveFlags) > MaxReducerEntries {
		return fmt.Errorf("thread status contains %d active flags, limit %d", len(status.ActiveFlags), MaxReducerEntries)
	}
	switch status.Type {
	case ThreadStatusNotLoaded, ThreadStatusIdle, ThreadStatusSystemError:
		if len(status.ActiveFlags) != 0 {
			return fmt.Errorf("thread status %q cannot contain active flags", status.Type)
		}
	case ThreadStatusActive:
		for _, flag := range status.ActiveFlags {
			switch flag {
			case ActiveWaitingOnApproval, ActiveWaitingOnUserInput:
			default:
				return fmt.Errorf("unknown active flag %q", flag)
			}
		}
	default:
		return fmt.Errorf("unknown thread status %q", status.Type)
	}
	return nil
}

func validateReducerField(name, value string, limit int, allowEmpty bool) error {
	if value == "" && !allowEmpty {
		return fmt.Errorf("%s is empty", name)
	}
	if len(value) > limit {
		return fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", name)
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("%s contains a control character", name)
	}
	return nil
}

func cloneThread(thread Thread) Thread {
	thread.Status = cloneStatus(thread.Status)
	return thread
}

func cloneStatus(status ThreadStatus) ThreadStatus {
	flags := make([]ActiveFlag, 0, 2)
	seen := make(map[ActiveFlag]struct{}, 2)
	for _, flag := range status.ActiveFlags {
		if _, duplicate := seen[flag]; duplicate {
			continue
		}
		seen[flag] = struct{}{}
		flags = append(flags, flag)
	}
	status.ActiveFlags = flags
	return status
}

func hasParentCycle(threads map[string]Thread) bool {
	const (
		visiting = 1
		visited  = 2
	)
	marks := make(map[string]uint8, len(threads))
	var visit func(string) bool
	visit = func(id string) bool {
		switch marks[id] {
		case visiting:
			return true
		case visited:
			return false
		}
		marks[id] = visiting
		if parent := threads[id].ParentThreadID; parent != "" {
			if _, known := threads[parent]; known && visit(parent) {
				return true
			}
		}
		marks[id] = visited
		return false
	}
	for id := range threads {
		if visit(id) {
			return true
		}
	}
	return false
}

func hasParentSessionContradiction(threads map[string]Thread) bool {
	for _, thread := range threads {
		if thread.ParentThreadID == "" {
			continue
		}
		parent, known := threads[thread.ParentThreadID]
		if known && parent.SessionID != thread.SessionID {
			return true
		}
	}
	return false
}

func sortedSet(values map[string]struct{}) string {
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, escapeIdentityComponent(value))
	}
	sort.Strings(ordered)
	return strings.Join(ordered, ",")
}

func escapeIdentityComponent(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, ",", "%2C")
	return strings.ReplaceAll(value, ":", "%3A")
}
