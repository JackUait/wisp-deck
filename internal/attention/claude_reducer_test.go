package attention

import (
	"path/filepath"
	"testing"
)

func TestClaudeReducerInitialIdleIsReady(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseReady, ReasonNone, "")

	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseReady, ReasonNone, "")
}

func TestClaudeReducerBusyArmsOneDoneAttention(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
	assertClaudeState(t, got, PhaseWorking, ReasonNone, "")

	// Repeated registry polls are the same turn, not another completion identity.
	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
	assertClaudeState(t, got, PhaseWorking, ReasonNone, "")

	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")

	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")
}

func TestClaudeReducerDoneIdentityIsUniquePerArmedTurn(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
	reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})

	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
	assertClaudeState(t, got, PhaseWorking, ReasonNone, "")
	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:2")
}

func TestClaudeReducerWaitingUsesStructuredReasonAndUpdateIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		waitingReason ClaudeWaitingReason
		wantReason    Reason
	}{
		{name: "question", waitingReason: ClaudeWaitingQuestion, wantReason: ReasonQuestion},
		{name: "permission", waitingReason: ClaudeWaitingPermission, wantReason: ReasonPermission},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := newTestClaudeReducer(t)
			observation := ClaudeReducerObservation{
				Status:          ClaudeObservedWaiting,
				WaitingReason:   tt.waitingReason,
				StatusUpdatedAt: "2026-07-13T10:11:12.123Z",
			}

			got := reducer.Reduce(observation)
			assertClaudeState(t, got, PhaseAttention, tt.wantReason, "waiting:2026-07-13T10:11:12.123Z")

			got = reducer.Reduce(observation)
			assertClaudeState(t, got, PhaseAttention, tt.wantReason, "waiting:2026-07-13T10:11:12.123Z")
		})
	}
}

func TestClaudeReducerWaitingThenIdleRetainsUnresolvedAttention(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
	waiting := ClaudeReducerObservation{
		Status:          ClaudeObservedWaiting,
		WaitingReason:   ClaudeWaitingPermission,
		StatusUpdatedAt: "update-7",
	}
	want := reducer.Reduce(waiting)
	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	if got != want {
		t.Fatalf("waiting -> idle = %#v, want unresolved %#v", got, want)
	}
}

func TestClaudeReducerRetainsIndependentWaitingPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first ClaudeReducerObservation
		last  ClaudeReducerObservation
	}{
		{
			name: "question remains above a later permission",
			first: ClaudeReducerObservation{
				Status:          ClaudeObservedWaiting,
				WaitingReason:   ClaudeWaitingQuestion,
				StatusUpdatedAt: "question-1",
			},
			last: ClaudeReducerObservation{
				Status:          ClaudeObservedWaiting,
				WaitingReason:   ClaudeWaitingPermission,
				StatusUpdatedAt: "permission-1",
			},
		},
		{
			name: "question supersedes an earlier permission",
			first: ClaudeReducerObservation{
				Status:          ClaudeObservedWaiting,
				WaitingReason:   ClaudeWaitingPermission,
				StatusUpdatedAt: "permission-2",
			},
			last: ClaudeReducerObservation{
				Status:          ClaudeObservedWaiting,
				WaitingReason:   ClaudeWaitingQuestion,
				StatusUpdatedAt: "question-2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := newTestClaudeReducer(t)
			reducer.Reduce(tt.first)
			got := reducer.Reduce(tt.last)
			assertClaudeState(t, got, PhaseAttention, ReasonQuestion, "waiting:"+questionIdentity(tt.first, tt.last))

			// Idle cannot resolve either request. A later foreground busy state is
			// the first semantic proof that both blockers cleared.
			got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
			assertClaudeState(t, got, PhaseAttention, ReasonQuestion, "waiting:"+questionIdentity(tt.first, tt.last))
			got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
			assertClaudeState(t, got, PhaseWorking, ReasonNone, "")
		})
	}
}

func TestClaudeReducerLowerPriorityExitCannotHideWaitingRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		waitingReason ClaudeWaitingReason
		wantReason    Reason
	}{
		{name: "question", waitingReason: ClaudeWaitingQuestion, wantReason: ReasonQuestion},
		{name: "permission", waitingReason: ClaudeWaitingPermission, wantReason: ReasonPermission},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := newTestClaudeReducer(t)
			want := reducer.Reduce(ClaudeReducerObservation{
				Status:          ClaudeObservedWaiting,
				WaitingReason:   tt.waitingReason,
				StatusUpdatedAt: tt.name + "-exit",
			})
			got := reducer.ReduceExit(ClaudeReducerExit{Code: 17})
			if got != want {
				t.Fatalf("%s then nonzero exit = %#v, want retained %#v", tt.name, got, want)
			}
		})
	}
}

func TestClaudeReducerAutomaticContinuationAndSubagentProgressStayWorking(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	for _, semanticStep := range []string{
		"foreground started",
		"automatic continuation queued",
		"subagent completed while foreground continues",
		"continued foreground work",
	} {
		got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
		if got.Phase != PhaseWorking || got.Reason != ReasonNone {
			t.Fatalf("%s = %#v, want working without attention", semanticStep, got)
		}
	}
	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")
}

func TestClaudeReducerBusyAfterAttentionWorksAndRearms(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	reducer.Reduce(ClaudeReducerObservation{
		Status:          ClaudeObservedWaiting,
		WaitingReason:   ClaudeWaitingQuestion,
		StatusUpdatedAt: "question-1",
	})

	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
	assertClaudeState(t, got, PhaseWorking, ReasonNone, "")
	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
	assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")
}

func TestClaudeReducerUnknownPreservesOnlyAttentionAndArming(t *testing.T) {
	t.Parallel()

	t.Run("otherwise publishes unknown", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedUnknown})
		assertClaudeState(t, got, PhaseUnknown, ReasonNone, "")
	})

	t.Run("armed turn survives temporary unknown", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
		got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedUnknown})
		assertClaudeState(t, got, PhaseUnknown, ReasonNone, "")
		got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
		assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")
	})

	t.Run("attention survives temporary unknown", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		want := reducer.Reduce(ClaudeReducerObservation{
			Status:          ClaudeObservedWaiting,
			WaitingReason:   ClaudeWaitingQuestion,
			StatusUpdatedAt: "question-2",
		})
		got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedUnknown})
		if got != want {
			t.Fatalf("attention -> unknown = %#v, want retained %#v", got, want)
		}
	})

	t.Run("unstructured waiting is unknown without losing arming", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
		got := reducer.Reduce(ClaudeReducerObservation{
			Status:          ClaudeObservedWaiting,
			WaitingReason:   ClaudeWaitingReason("other"),
			StatusUpdatedAt: "ambiguous",
		})
		assertClaudeState(t, got, PhaseUnknown, ReasonNone, "")
		got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
		assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")
	})
}

// A cleared chat (/new, /clear) replaces the conversation the attention was
// raised about. Claude reports the replacement by changing the registry
// record's sessionId while its status stays idle — nothing else moves — so
// without this the bell (or the eyes it decayed into) is sticky forever: only a
// busy observation clears attention, and the user just threw the turn away.
func TestClaudeReducerClearedConversationRetiresAttention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		raise func(*ClaudeReducer)
	}{
		{
			name: "finished turn",
			raise: func(reducer *ClaudeReducer) {
				reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy, SessionID: "conversation-1"})
				reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle, SessionID: "conversation-1"})
			},
		},
		{
			name: "unanswered question",
			raise: func(reducer *ClaudeReducer) {
				reducer.Reduce(ClaudeReducerObservation{
					Status:          ClaudeObservedWaiting,
					WaitingReason:   ClaudeWaitingQuestion,
					StatusUpdatedAt: "question-cleared",
					SessionID:       "conversation-1",
				})
			},
		},
		{
			name: "unanswered permission",
			raise: func(reducer *ClaudeReducer) {
				reducer.Reduce(ClaudeReducerObservation{
					Status:          ClaudeObservedWaiting,
					WaitingReason:   ClaudeWaitingPermission,
					StatusUpdatedAt: "permission-cleared",
					SessionID:       "conversation-1",
				})
			},
		},
		{
			name: "failed launch",
			raise: func(reducer *ClaudeReducer) {
				reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy, SessionID: "conversation-1"})
				reducer.ReduceExit(ClaudeReducerExit{Code: 17})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := newTestClaudeReducer(t)
			tt.raise(reducer)
			if got := reducer.Reduce(ClaudeReducerObservation{
				Status:    ClaudeObservedIdle,
				SessionID: "conversation-1",
			}); got.Phase != PhaseAttention {
				t.Fatalf("state before the clear = %#v, want attention", got)
			}

			got := reducer.Reduce(ClaudeReducerObservation{
				Status:    ClaudeObservedIdle,
				SessionID: "conversation-2",
			})
			assertClaudeState(t, got, PhaseReady, ReasonNone, "")

			// The retired request must not come back on the next poll of the same
			// fresh conversation.
			got = reducer.Reduce(ClaudeReducerObservation{
				Status:    ClaudeObservedIdle,
				SessionID: "conversation-2",
			})
			assertClaudeState(t, got, PhaseReady, ReasonNone, "")
		})
	}
}

func TestClaudeReducerKeepsAttentionWithoutAConversationChange(t *testing.T) {
	t.Parallel()

	t.Run("the same conversation keeps its request", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy, SessionID: "conversation-1"})
		want := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle, SessionID: "conversation-1"})
		got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle, SessionID: "conversation-1"})
		if got != want {
			t.Fatalf("same conversation = %#v, want retained %#v", got, want)
		}
	})

	// A registry read that failed, or a Claude too old to report a conversation,
	// says nothing about the conversation. Reading its absence as a change would
	// silence a real request on the next transient miss.
	t.Run("an unreported conversation is not a change", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy, SessionID: "conversation-1"})
		want := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle, SessionID: "conversation-1"})

		got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedUnknown})
		if got != want {
			t.Fatalf("unknown observation = %#v, want retained %#v", got, want)
		}
		got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
		if got != want {
			t.Fatalf("idle without a conversation = %#v, want retained %#v", got, want)
		}
		got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle, SessionID: "conversation-1"})
		if got != want {
			t.Fatalf("conversation reported again = %#v, want retained %#v", got, want)
		}
	})
}

// A conversation replaced mid-turn (a fork or a compaction, not a user clear)
// still owes the user the completion bell for the work that is still running.
func TestClaudeReducerClearedConversationKeepsTheRunningTurnArmed(t *testing.T) {
	t.Parallel()

	reducer := newTestClaudeReducer(t)
	reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy, SessionID: "conversation-1"})
	got := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy, SessionID: "conversation-2"})
	assertClaudeState(t, got, PhaseWorking, ReasonNone, "")

	got = reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle, SessionID: "conversation-2"})
	assertClaudeState(t, got, PhaseAttention, ReasonDone, "done:1")
}

func TestClaudeReducerExitHandling(t *testing.T) {
	t.Parallel()

	t.Run("unexpected nonzero exit alerts once", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})

		exit := ClaudeReducerExit{Code: 17}
		got := reducer.ReduceExit(exit)
		assertClaudeState(t, got, PhaseAttention, ReasonError, "error:17")
		got = reducer.ReduceExit(exit)
		assertClaudeState(t, got, PhaseAttention, ReasonError, "error:17")
	})

	t.Run("signal caused exit creates no attention", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		want := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedBusy})
		got := reducer.ReduceExit(ClaudeReducerExit{Code: 130, Signaled: true})
		if got != want {
			t.Fatalf("signaled exit = %#v, want unchanged %#v", got, want)
		}
	})

	t.Run("zero exit creates no attention", func(t *testing.T) {
		t.Parallel()
		reducer := newTestClaudeReducer(t)
		want := reducer.Reduce(ClaudeReducerObservation{Status: ClaudeObservedIdle})
		got := reducer.ReduceExit(ClaudeReducerExit{})
		if got != want {
			t.Fatalf("zero exit = %#v, want unchanged %#v", got, want)
		}
	})
}

func TestClaudeReducerStableObservationsDoNotAdvanceAtomicWriter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writer, err := NewAtomicWriter(filepath.Join(dir, "state"), "generation-reducer")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	reducer, err := NewClaudeReducer("generation-reducer")
	if err != nil {
		t.Fatalf("NewClaudeReducer() error = %v", err)
	}
	publish := func(state State) {
		t.Helper()
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}

	idle := ClaudeReducerObservation{Status: ClaudeObservedIdle}
	publish(reducer.Reduce(idle))
	publish(reducer.Reduce(idle))
	if got := writer.Current().Sequence; got != 1 {
		t.Fatalf("repeated idle sequence = %d, want 1", got)
	}

	busy := ClaudeReducerObservation{Status: ClaudeObservedBusy}
	publish(reducer.Reduce(busy))
	publish(reducer.Reduce(busy))
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("repeated busy sequence = %d, want 2", got)
	}

	waiting := ClaudeReducerObservation{
		Status:          ClaudeObservedWaiting,
		WaitingReason:   ClaudeWaitingQuestion,
		StatusUpdatedAt: "question-stable",
	}
	publish(reducer.Reduce(waiting))
	publish(reducer.Reduce(waiting))
	if got := writer.Current().Sequence; got != 3 {
		t.Fatalf("repeated waiting sequence = %d, want 3", got)
	}
}

func TestNewClaudeReducerRejectsInvalidGeneration(t *testing.T) {
	t.Parallel()

	if _, err := NewClaudeReducer(""); err == nil {
		t.Fatal("NewClaudeReducer() accepted an empty generation")
	}
}

func newTestClaudeReducer(t *testing.T) *ClaudeReducer {
	t.Helper()
	reducer, err := NewClaudeReducer("generation-claude")
	if err != nil {
		t.Fatalf("NewClaudeReducer() error = %v", err)
	}
	return reducer
}

func assertClaudeState(t *testing.T, got State, phase Phase, reason Reason, identity string) {
	t.Helper()
	want := State{
		Generation: "generation-claude",
		Phase:      phase,
		Reason:     reason,
		Identity:   identity,
	}
	if got != want {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
}

func questionIdentity(observations ...ClaudeReducerObservation) string {
	for _, observation := range observations {
		if observation.WaitingReason == ClaudeWaitingQuestion {
			return observation.StatusUpdatedAt
		}
	}
	return ""
}
