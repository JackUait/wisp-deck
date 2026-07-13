package attention

import (
	"fmt"
	"strconv"
)

// ClaudeObservedStatus is the small status vocabulary accepted from Claude's
// interactive-session registry. Unrecognized values reduce conservatively to
// unknown.
type ClaudeObservedStatus string

const (
	ClaudeObservedIdle    ClaudeObservedStatus = "idle"
	ClaudeObservedBusy    ClaudeObservedStatus = "busy"
	ClaudeObservedWaiting ClaudeObservedStatus = "waiting"
	ClaudeObservedUnknown ClaudeObservedStatus = "unknown"
)

// ClaudeWaitingReason is the structured reason attached to a waiting registry
// status. It deliberately has no catch-all attention value: ambiguity must not
// be promoted to a notification.
type ClaudeWaitingReason string

const (
	ClaudeWaitingQuestion   ClaudeWaitingReason = "question"
	ClaudeWaitingPermission ClaudeWaitingReason = "permission"
)

// ClaudeReducerObservation is one validated registry observation. The registry
// update time is adapter-private identity, allowing a second request of the same
// kind to advance AtomicWriter without exposing the timestamp on disk.
type ClaudeReducerObservation struct {
	Status          ClaudeObservedStatus
	WaitingReason   ClaudeWaitingReason
	StatusUpdatedAt string
}

// ClaudeReducerExit describes how the supervised foreground launch finished.
// Signaled distinguishes user/controller shutdown from an unexpected exit even
// when both produce a nonzero shell code.
type ClaudeReducerExit struct {
	Code     int
	Signaled bool
}

// ClaudeReducer converts Claude registry observations into normalized semantic
// state. It is intentionally single-threaded: the supervisor owns one reducer
// and serializes poll and exit events through its control loop.
type ClaudeReducer struct {
	generation         string
	state              State
	armed              bool
	turn               uint64
	questionIdentity   string
	permissionIdentity string
	errorIdentity      string
}

// NewClaudeReducer creates a reducer fenced to one launch generation.
func NewClaudeReducer(generation string) (*ClaudeReducer, error) {
	if err := validateGeneration(generation); err != nil {
		return nil, fmt.Errorf("create Claude reducer: %w", err)
	}
	return &ClaudeReducer{
		generation: generation,
		state: State{
			Generation: generation,
			Phase:      PhaseUnknown,
			Reason:     ReasonNone,
		},
	}, nil
}

// Reduce applies one registry observation and returns the complete current
// normalized state. Repeating an observation returns the same identity, so an
// AtomicWriter will not advance its sequence.
func (r *ClaudeReducer) Reduce(observation ClaudeReducerObservation) State {
	switch observation.Status {
	case ClaudeObservedIdle:
		if pending, ok := r.highestAttention(); ok {
			return pending
		}
		if r.armed {
			r.armed = false
			return r.setState(PhaseAttention, ReasonDone, "done:"+strconv.FormatUint(r.turn, 10))
		}
		if r.state.Phase == PhaseAttention {
			return r.state
		}
		return r.setState(PhaseReady, ReasonNone, "")

	case ClaudeObservedBusy:
		r.clearPendingAttention()
		if !r.armed {
			r.turn++
			r.armed = true
		}
		return r.setState(PhaseWorking, ReasonNone, "")

	case ClaudeObservedWaiting:
		var reason Reason
		switch observation.WaitingReason {
		case ClaudeWaitingQuestion:
			reason = ReasonQuestion
		case ClaudeWaitingPermission:
			reason = ReasonPermission
		default:
			return r.reduceUnknown()
		}
		r.armed = false
		identity := "waiting:" + observation.StatusUpdatedAt
		switch reason {
		case ReasonQuestion:
			r.questionIdentity = identity
		case ReasonPermission:
			r.permissionIdentity = identity
		}
		pending, _ := r.highestAttention()
		return pending

	case ClaudeObservedUnknown:
		return r.reduceUnknown()

	default:
		return r.reduceUnknown()
	}
}

// ReduceExit applies the supervised process result. Normal and externally
// signalled exits preserve the last observation; only an unexpected nonzero
// result creates error attention.
func (r *ClaudeReducer) ReduceExit(exit ClaudeReducerExit) State {
	if exit.Signaled || exit.Code == 0 {
		return r.state
	}
	r.armed = false
	r.errorIdentity = "error:" + strconv.Itoa(exit.Code)
	pending, _ := r.highestAttention()
	return pending
}

func (r *ClaudeReducer) reduceUnknown() State {
	if pending, ok := r.highestAttention(); ok {
		return pending
	}
	return r.setState(PhaseUnknown, ReasonNone, "")
}

func (r *ClaudeReducer) highestAttention() (State, bool) {
	switch {
	case r.questionIdentity != "":
		return r.setState(PhaseAttention, ReasonQuestion, r.questionIdentity), true
	case r.permissionIdentity != "":
		return r.setState(PhaseAttention, ReasonPermission, r.permissionIdentity), true
	case r.errorIdentity != "":
		return r.setState(PhaseAttention, ReasonError, r.errorIdentity), true
	case r.state.Phase == PhaseAttention:
		return r.state, true
	default:
		return State{}, false
	}
}

func (r *ClaudeReducer) clearPendingAttention() {
	r.questionIdentity = ""
	r.permissionIdentity = ""
	r.errorIdentity = ""
}

func (r *ClaudeReducer) setState(phase Phase, reason Reason, identity string) State {
	r.state = State{
		Generation: r.generation,
		Phase:      phase,
		Reason:     reason,
		Identity:   identity,
	}
	return r.state
}
