package codexadapter

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/attention"
)

const (
	testResumeID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testFreshID    = "22222222-2222-4222-8222-222222222222"
	testFreshOther = "33333333-3333-4333-8333-333333333333"
)

func TestReducerCorrelatesExactResumeRootAndItsChildren(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	reducer.Reduce(threadEvent(testThread("unrelated", "session-other", "", "/repo", idleStatus())))
	assertReducerState(t, reducer.Current(), attention.PhaseUnknown, attention.ReasonNone, "")

	root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
	assertReducerState(t, reducer.Reduce(threadEvent(root)), attention.PhaseReady, attention.ReasonNone, "")
	if got := reducer.RootThreadID(); got != testResumeID {
		t.Fatalf("RootThreadID() = %q, want %q", got, testResumeID)
	}

	// Resume correlation accepts both direct ancestry and the stable session ID.
	childByParent := testThread("child-parent", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnApproval))
	assertReducerState(t, reducer.Reduce(threadEvent(childByParent)), attention.PhaseAttention, attention.ReasonPermission, "permission:child-parent")

	childBySession := testThread("child-session", "session-root", "missing-parent", "/repo", activeStatus(ActiveWaitingOnUserInput))
	assertReducerState(t, reducer.Reduce(threadEvent(childBySession)), attention.PhaseAttention, attention.ReasonQuestion, "question:child-session")
}

func TestReducerFreshCorrelationUsesBaselineCWDAndTopLevelRoot(t *testing.T) {
	t.Parallel()

	reducer, err := NewReducer(ReducerConfig{
		Generation:        "generation-fresh",
		ProjectCWD:        "/repo",
		BaselineThreadIDs: []string{"stale-root"},
	})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}

	ignored := []Thread{
		testThread("stale-root", "stale-session", "", "/repo", activeStatus()),
		testThread("wrong-cwd", "wrong-session", "", "/other", activeStatus()),
		testThread("orphan-child", "fresh-session", "orphan-parent", "/repo", activeStatus(ActiveWaitingOnUserInput)),
	}
	for _, thread := range ignored {
		reducer.Reduce(threadEvent(thread))
	}
	assertReducerState(t, reducer.Current(), attention.PhaseUnknown, attention.ReasonNone, "")

	root := testThread("fresh-root", "fresh-session", "", "/repo", activeStatus())
	assertReducerState(t, reducer.Reduce(threadEvent(root)), attention.PhaseAttention, attention.ReasonQuestion, "question:orphan-child")
	if got := reducer.RootThreadID(); got != testFreshID {
		t.Fatalf("RootThreadID() = %q, want %q", got, testFreshID)
	}

	// The child observed before its parent becomes correlated once the parent
	// record completes its ancestry chain.
	parent := testThread("orphan-parent", "fresh-session", "fresh-root", "/repo", idleStatus())
	reducer.Reduce(threadEvent(parent))
	assertReducerState(t, reducer.Current(), attention.PhaseAttention, attention.ReasonQuestion, "question:orphan-child")
}

func TestReducerAmbiguousFreshRootsFailUnknownUntilCleanReconnect(t *testing.T) {
	t.Parallel()

	reducer, err := NewReducer(ReducerConfig{Generation: "generation-ambiguous", ProjectCWD: "/repo"})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}
	one := testThread("root-a", "session-a", "", "/repo", idleStatus())
	two := testThread("root-b", "session-b", "", "/repo", activeStatus(ActiveWaitingOnUserInput))
	reducer.Reduce(threadEvent(one))
	assertReducerState(t, reducer.Reduce(threadEvent(two)), attention.PhaseUnknown, attention.ReasonNone, "")
	if !reducer.Ambiguous() {
		t.Fatal("Ambiguous() = false after two eligible fresh roots")
	}
	reducer.Reduce(threadEvent(one))
	if got := reducer.RootThreadID(); got != "" {
		t.Fatalf("RootThreadID() after ambiguous replay = %q, want empty until snapshot", got)
	}

	clean := ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{one}}
	assertReducerState(t, reducer.Reduce(clean), attention.PhaseReady, attention.ReasonNone, "")
	if reducer.Ambiguous() {
		t.Fatal("Ambiguous() remained true after a clean reconnect snapshot")
	}
}

func TestReducerPriorityAndActiveClearingOfCompletion(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	reducer.Reduce(threadEvent(root))
	reducer.Reduce(threadEvent(testThread("error-child", "session-root", "resume-root", "/repo", systemErrorStatus())))
	reducer.Reduce(threadEvent(testThread("permission-child", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnApproval))))
	reducer.Reduce(threadEvent(testThread("question-child", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput))))

	assertReducerState(t, reducer.Reduce(oscEvent("turn-1")), attention.PhaseAttention, attention.ReasonQuestion, "question:question-child")
	reducer.Reduce(statusEvent("question-child", idleStatus()))
	assertReducerState(t, reducer.Current(), attention.PhaseAttention, attention.ReasonPermission, "permission:permission-child")
	reducer.Reduce(statusEvent("permission-child", idleStatus()))
	assertReducerState(t, reducer.Current(), attention.PhaseAttention, attention.ReasonError, "error:error-child")
	reducer.Reduce(statusEvent("error-child", idleStatus()))
	assertReducerState(t, reducer.Current(), attention.PhaseAttention, attention.ReasonDone, "osc:turn-1")

	// A later active observation means Codex continued. It clears the prior OSC
	// completion and rearms the root for a genuinely later notification.
	assertReducerState(t, reducer.Reduce(statusEvent("resume-root", activeStatus())), attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent("resume-root", idleStatus())), attention.PhaseReady, attention.ReasonNone, "")
}

func TestReducerIdleAloneNeverCreatesDoneAttention(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	reducer.Reduce(threadEvent(testThread("resume-root", "session-root", "", "/repo", idleStatus())))
	assertReducerState(t, reducer.Reduce(statusEvent("resume-root", idleStatus())), attention.PhaseReady, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(oscEvent("unarmed-noise")), attention.PhaseReady, attention.ReasonNone, "")
}

func TestReducerOSCIdentityDedupeKeepsAtomicWriterSequenceStable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writer, err := attention.NewAtomicWriter(filepath.Join(dir, "state"), "generation-codex")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	reducer, err := NewReducer(ReducerConfig{
		Generation:     "generation-codex",
		ProjectCWD:     "/repo",
		ResumeThreadID: testResumeID,
	})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}
	publish := func(event ReducerEvent) {
		t.Helper()
		state := reducer.Reduce(event)
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}

	publish(threadEvent(testThread("resume-root", "session-root", "", "/repo", activeStatus())))
	publish(oscEvent("turn-1"))
	publish(oscEvent("turn-1"))
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("duplicate OSC sequence = %d, want 2", got)
	}

	// A different OSC identity without intervening root activity is still the
	// same completed turn and must not advance the writer.
	publish(oscEvent("duplicate-for-same-turn"))
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("unarmed OSC sequence = %d, want 2", got)
	}

	publish(statusEvent("resume-root", activeStatus()))
	publish(statusEvent("resume-root", activeStatus()))
	publish(oscEvent("turn-2"))
	if got := writer.Current().Sequence; got != 4 {
		t.Fatalf("second armed completion sequence = %d, want 4", got)
	}
}

func TestReducerDeterministicallySortsAttentionIdentities(t *testing.T) {
	t.Parallel()

	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	children := []Thread{
		testThread("child-z", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnApproval, ActiveWaitingOnUserInput)),
		testThread("child-a", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput)),
	}

	first := newResumeReducer(t)
	first.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{children[0], root, children[1]}})
	second := newResumeReducer(t)
	second.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{children[1], children[0], root}})

	wantIdentity := "question:child-a,child-z"
	assertReducerState(t, first.Current(), attention.PhaseAttention, attention.ReasonQuestion, wantIdentity)
	if first.Current() != second.Current() {
		t.Fatalf("snapshot order changed state: first %#v, second %#v", first.Current(), second.Current())
	}

	// Replaying a differently ordered but equivalent snapshot is semantically
	// stable for the AtomicWriter.
	dir := t.TempDir()
	writer, err := attention.NewAtomicWriter(filepath.Join(dir, "state"), "generation-resume")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	for _, state := range []attention.State{first.Current(), second.Current()} {
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}
	if got := writer.Current().Sequence; got != 1 {
		t.Fatalf("equivalent snapshot sequence = %d, want 1", got)
	}
}

func TestReducerReenteredStatusEpochAlertsWhileSameKindRemainsActive(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	writer, err := attention.NewAtomicWriter(filepath.Join(t.TempDir(), "state"), "generation-resume")
	if err != nil {
		t.Fatal(err)
	}
	publish := func(event ReducerEvent) {
		state := reducer.Reduce(event)
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			t.Fatal(err)
		}
	}

	publish(threadEvent(testThread(
		"resume-root", "session-root", "", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)))
	publish(threadEvent(testThread(
		"child", "session-root", "resume-root", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)))
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("two question epochs sequence = %d, want 2", got)
	}

	publish(statusEvent("child", activeStatus()))
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("clearing one of two questions sequence = %d, want 2", got)
	}
	publish(statusEvent("child", activeStatus(ActiveWaitingOnUserInput)))
	if got := writer.Current().Sequence; got != 3 {
		t.Fatalf("re-entered question epoch sequence = %d, want 3", got)
	}
	publish(statusEvent("child", activeStatus(ActiveWaitingOnUserInput)))
	if got := writer.Current().Sequence; got != 3 {
		t.Fatalf("repeated active status sequence = %d, want 3", got)
	}
}

func TestReducerAttentionIdentityCannotCollideOnIDDelimiters(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	first := ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{
		root,
		testThread("a,b", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput)),
		testThread("c", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput)),
	}}
	firstState := reducer.Reduce(first)

	second := ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{
		root,
		testThread("a", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput)),
		testThread("b,c", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput)),
	}}
	secondState := reducer.Reduce(second)
	if firstState.Identity == secondState.Identity {
		t.Fatalf("distinct pending sets share identity %q", firstState.Identity)
	}
}

func TestReducerObserverLossAndRecoveryFailUnknown(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	reducer.Reduce(threadEvent(root))
	reducer.Reduce(oscEvent("turn-before-loss"))
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverLost}), attention.PhaseUnknown, attention.ReasonNone, "")

	root.Status = idleStatus()
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseReady, attention.ReasonNone, "")

	// A reconnect snapshot with two eligible roots cannot recover a fresh
	// reducer to attention.
	fresh, err := NewReducer(ReducerConfig{Generation: "generation-reconnect", ProjectCWD: "/repo"})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}
	ambiguous := ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{
		testThread("root-a", "session-a", "", "/repo", idleStatus()),
		testThread("root-b", "session-b", "", "/repo", activeStatus(ActiveWaitingOnUserInput)),
	}}
	assertReducerState(t, fresh.Reduce(ambiguous), attention.PhaseUnknown, attention.ReasonNone, "")
}

func TestReducerReconnectSuppressesUncertainStatusEpochUntilItClears(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	waiting := testThread(
		"resume-root", "session-root", "", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting},
	}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)

	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverLost}),
		attention.PhaseUnknown, attention.ReasonNone, "")
	// The passive connection can only see aggregate flags. If the same flag is
	// present after an outage, it cannot distinguish one continuous prompt from
	// an unseen 1 -> 0 -> 1 cycle, so it must neither duplicate nor invent one.
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting},
	}), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus(ActiveWaitingOnUserInput))),
		attention.PhaseUnknown, attention.ReasonNone, "")

	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus())),
		attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus(ActiveWaitingOnUserInput))),
		attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
}

func TestReducerReconnectSuppressesContinuousSystemErrorUntilKnownClear(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	writer, err := attention.NewAtomicWriter(filepath.Join(t.TempDir(), "state"), "generation-resume")
	if err != nil {
		t.Fatal(err)
	}
	publish := func(event ReducerEvent) attention.State {
		t.Helper()
		state := reducer.Reduce(event)
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			t.Fatal(err)
		}
		return state
	}

	failed := testThread("resume-root", "session-root", "", "/repo", systemErrorStatus())
	assertReducerState(t, publish(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{failed},
	}), attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)
	if got := writer.Current().Sequence; got != 1 {
		t.Fatalf("initial system-error sequence = %d, want 1", got)
	}

	assertReducerState(t, publish(ReducerEvent{Kind: EventObserverLost}),
		attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, publish(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{failed},
	}), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, publish(statusEvent(testResumeID, systemErrorStatus())),
		attention.PhaseUnknown, attention.ReasonNone, "")
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("continuous system-error sequence = %d, want 2", got)
	}

	assertReducerState(t, publish(statusEvent(testResumeID, idleStatus())),
		attention.PhaseReady, attention.ReasonNone, "")
	assertReducerState(t, publish(statusEvent(testResumeID, systemErrorStatus())),
		attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)
	if got := writer.Current().Sequence; got != 4 {
		t.Fatalf("new system-error sequence = %d, want 4", got)
	}
}

func TestReducerSystemErrorEpochSurvivesMissingEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		makeUnknown func(*Reducer) attention.State
		recover     func(*Reducer, Thread) attention.State
	}{
		{
			name: "live notLoaded",
			makeUnknown: func(reducer *Reducer) attention.State {
				return reducer.Reduce(statusEvent(testResumeID, notLoadedStatus()))
			},
			recover: func(reducer *Reducer, root Thread) attention.State {
				return reducer.Reduce(statusEvent(root.ID, root.Status))
			},
		},
		{
			name: "untrusted root-missing barrier",
			makeUnknown: func(reducer *Reducer) attention.State {
				return reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot})
			},
			recover: func(reducer *Reducer, root Thread) attention.State {
				return reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reducer := newResumeReducer(t)
			failed := testThread("resume-root", "session-root", "", "/repo", systemErrorStatus())
			assertReducerState(t, reducer.Reduce(ReducerEvent{
				Kind: EventObserverSnapshot, Threads: []Thread{failed},
			}), attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)

			assertReducerState(t, test.makeUnknown(reducer),
				attention.PhaseUnknown, attention.ReasonNone, "")
			assertReducerState(t, test.recover(reducer, failed),
				attention.PhaseUnknown, attention.ReasonNone, "")

			assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, idleStatus())),
				attention.PhaseReady, attention.ReasonNone, "")
			assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, systemErrorStatus())),
				attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)
		})
	}
}

func TestReducerKnownSystemErrorClearEndsEpoch(t *testing.T) {
	t.Parallel()

	t.Run("active status", func(t *testing.T) {
		reducer := newResumeReducer(t)
		failed := testThread("resume-root", "session-root", "", "/repo", systemErrorStatus())
		reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{failed}})
		assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus())),
			attention.PhaseWorking, attention.ReasonNone, "")
		assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, systemErrorStatus())),
			attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)
	})

	t.Run("idle status", func(t *testing.T) {
		reducer := newResumeReducer(t)
		failed := testThread("resume-root", "session-root", "", "/repo", systemErrorStatus())
		reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{failed}})
		assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, idleStatus())),
			attention.PhaseReady, attention.ReasonNone, "")
		assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, systemErrorStatus())),
			attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)
	})

	t.Run("thread close", func(t *testing.T) {
		reducer := newResumeReducer(t)
		root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
		failed := testThread("error-child", "session-root", "resume-root", "/repo", systemErrorStatus())
		reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root, failed}})
		assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventThreadClosed, ThreadID: failed.ID}),
			attention.PhaseReady, attention.ReasonNone, "")
		assertReducerState(t, reducer.Reduce(ReducerEvent{
			Kind: EventObserverSnapshot, Threads: []Thread{root, failed},
		}), attention.PhaseAttention, attention.ReasonError, "error:error-child")
	})

	t.Run("trusted snapshot absence", func(t *testing.T) {
		reducer := newResumeReducer(t)
		root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
		failed := testThread("error-child", "session-root", "resume-root", "/repo", systemErrorStatus())
		reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root, failed}})
		assertReducerState(t, reducer.Reduce(ReducerEvent{
			Kind: EventObserverSnapshot, Threads: []Thread{root},
		}), attention.PhaseReady, attention.ReasonNone, "")
		assertReducerState(t, reducer.Reduce(ReducerEvent{
			Kind: EventObserverSnapshot, Threads: []Thread{root, failed},
		}), attention.PhaseAttention, attention.ReasonError, "error:error-child")
	})
}

func TestReducerNewSystemErrorAlertsDespiteOldUncertainError(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	failed := testThread("resume-root", "session-root", "", "/repo", systemErrorStatus())
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{failed},
	}), attention.PhaseAttention, attention.ReasonError, "error:"+testResumeID)
	reducer.Reduce(ReducerEvent{Kind: EventObserverLost})

	newFailure := testThread("new-error", "session-root", "resume-root", "/repo", systemErrorStatus())
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{failed, newFailure},
	}), attention.PhaseAttention, attention.ReasonError, "error:new-error")
}

func TestReducerPrioritySuppressedSystemErrorRemainsAlertableAfterUncertainty(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	waiting := testThread(
		"resume-root", "session-root", "", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)
	failed := testThread("error-child", "session-root", "resume-root", "/repo", systemErrorStatus())
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting, failed},
	}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
	reducer.Reduce(ReducerEvent{Kind: EventObserverLost})
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting, failed},
	}), attention.PhaseUnknown, attention.ReasonNone, "")

	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, idleStatus())),
		attention.PhaseAttention, attention.ReasonError, "error:error-child")
}

func TestReducerLiveNotLoadedPreservesStatusEpochUntilKnownClear(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	waiting := activeStatus(ActiveWaitingOnUserInput)
	root := testThread("resume-root", "session-root", "", "/repo", waiting)
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{root},
	}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)

	// notLoaded is missing evidence, not evidence that the waiting flag cleared.
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, notLoadedStatus())),
		attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, waiting)),
		attention.PhaseUnknown, attention.ReasonNone, "")

	// A known status without the flag proves the old epoch ended. Its next
	// 0 -> 1 transition is therefore new and alertable.
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus())),
		attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, waiting)),
		attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
}

func TestReducerUnannouncedStatusEpochRemainsAlertableAfterUncertainty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		makeUnknown func(*Reducer) attention.State
		recover     func(*Reducer, Thread) attention.State
	}{
		{
			name: "live notLoaded",
			makeUnknown: func(reducer *Reducer) attention.State {
				return reducer.Reduce(statusEvent(testResumeID, notLoadedStatus()))
			},
			recover: func(reducer *Reducer, root Thread) attention.State {
				return reducer.Reduce(statusEvent(root.ID, root.Status))
			},
		},
		{
			name: "malformed event and clean snapshot",
			makeUnknown: func(reducer *Reducer) attention.State {
				return reducer.Reduce(statusEvent(testResumeID, ThreadStatus{
					Type: ThreadStatusActive, ActiveFlags: []ActiveFlag{"future-flag"},
				}))
			},
			recover: func(reducer *Reducer, root Thread) attention.State {
				return reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reducer := newResumeReducer(t)
			root := testThread("resume-root", "session-root", "", "/repo", activeStatus(
				ActiveWaitingOnUserInput, ActiveWaitingOnApproval,
			))
			assertReducerState(t, reducer.Reduce(ReducerEvent{
				Kind: EventObserverSnapshot, Threads: []Thread{root},
			}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
			assertReducerState(t, test.makeUnknown(reducer),
				attention.PhaseUnknown, attention.ReasonNone, "")

			// Permission was priority-suppressed and never announced. Once an exact
			// known status clears question but retains permission, alerting permission
			// is safe whether its epoch spanned the uncertainty or restarted within it.
			root.Status = activeStatus(ActiveWaitingOnApproval)
			assertReducerState(t, test.recover(reducer, root),
				attention.PhaseAttention, attention.ReasonPermission, "permission:"+testResumeID)
		})
	}
}

func TestReducerUncertainQuestionBlocksNewPermissionUntilQuestionClears(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread(
		"resume-root", "session-root", "", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{root},
	}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
	reducer.Reduce(ReducerEvent{Kind: EventObserverLost})

	root.Status = activeStatus(ActiveWaitingOnUserInput, ActiveWaitingOnApproval)
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{root},
	}), attention.PhaseUnknown, attention.ReasonNone, "")

	// The permission is definitely new, but cannot bypass a higher-priority
	// question whose epoch is uncertain across the outage.
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus(ActiveWaitingOnApproval))),
		attention.PhaseAttention, attention.ReasonPermission, "permission:"+testResumeID)
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus(ActiveWaitingOnApproval))),
		attention.PhaseAttention, attention.ReasonPermission, "permission:"+testResumeID)
}

func TestReducerTrustLossMakesSurvivingStatusEpochUncertain(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	waiting := testThread(
		"resume-root", "session-root", "", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting},
	}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)

	malformed := statusEvent(testResumeID, ThreadStatus{
		Type: ThreadStatusActive, ActiveFlags: []ActiveFlag{"future-flag"},
	})
	assertReducerState(t, reducer.Reduce(malformed), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting},
	}), attention.PhaseUnknown, attention.ReasonNone, "")

	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus())),
		attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus(ActiveWaitingOnUserInput))),
		attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
}

func TestReducerUntrustedReconnectSnapshotCannotErasePriorStatusEpoch(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	waiting := testThread(
		"resume-root", "session-root", "", "/repo",
		activeStatus(ActiveWaitingOnUserInput),
	)
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting},
	}), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
	reducer.Reduce(ReducerEvent{Kind: EventObserverLost})

	// A root-missing barrier is not evidence that the old flag cleared. If it
	// erased the epoch, a later snapshot would incorrectly alert the same flag.
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot}),
		attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{waiting},
	}), attention.PhaseUnknown, attention.ReasonNone, "")

	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus())),
		attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent(testResumeID, activeStatus(ActiveWaitingOnUserInput))),
		attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
}

func TestReducerReconnectAlertsOnlyStatusEpochsKnownToBeNew(t *testing.T) {
	t.Parallel()

	t.Run("inactive before loss and active after reconnect", func(t *testing.T) {
		reducer := newResumeReducer(t)
		root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
		assertReducerState(t, reducer.Reduce(ReducerEvent{
			Kind: EventObserverSnapshot, Threads: []Thread{root},
		}), attention.PhaseWorking, attention.ReasonNone, "")
		reducer.Reduce(ReducerEvent{Kind: EventObserverLost})
		root.Status = activeStatus(ActiveWaitingOnApproval)
		assertReducerState(t, reducer.Reduce(ReducerEvent{
			Kind: EventObserverSnapshot, Threads: []Thread{root},
		}), attention.PhaseAttention, attention.ReasonPermission, "permission:"+testResumeID)
	})

	t.Run("active before loss and clear after reconnect", func(t *testing.T) {
		reducer := newResumeReducer(t)
		root := testThread(
			"resume-root", "session-root", "", "/repo",
			activeStatus(ActiveWaitingOnApproval),
		)
		reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}})
		reducer.Reduce(ReducerEvent{Kind: EventObserverLost})
		root.Status = activeStatus()
		assertReducerState(t, reducer.Reduce(ReducerEvent{
			Kind: EventObserverSnapshot, Threads: []Thread{root},
		}), attention.PhaseWorking, attention.ReasonNone, "")
	})
}

func TestReducerObserverLossRetainsOSCPrivatelyUntilCleanIdleSnapshot(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
	dir := t.TempDir()
	writer, err := attention.NewAtomicWriter(filepath.Join(dir, "state"), "generation-resume")
	if err != nil {
		t.Fatalf("NewAtomicWriter() error = %v", err)
	}
	publish := func(event ReducerEvent) attention.State {
		t.Helper()
		state := reducer.Reduce(event)
		if err := writer.Publish(state.Phase, state.Reason, state.Identity); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
		return state
	}

	assertReducerState(t, publish(threadEvent(root)), attention.PhaseReady, attention.ReasonNone, "")
	assertReducerState(t, publish(oscEvent("initial-unarmed")), attention.PhaseReady, attention.ReasonNone, "")
	assertReducerState(t, publish(ReducerEvent{Kind: EventObserverLost}), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, publish(oscEvent("outage-1")), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, publish(oscEvent("outage-1")), attention.PhaseUnknown, attention.ReasonNone, "")
	if got := writer.Current().Sequence; got != 2 {
		t.Fatalf("repeated outage OSC sequence = %d, want 2", got)
	}
	assertReducerState(t, publish(oscEvent("outage-2")), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, publish(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: []Thread{root},
	}), attention.PhaseAttention, attention.ReasonDone, "osc:outage-2")
	if got := writer.Current().Sequence; got != 3 {
		t.Fatalf("idle reconnect sequence = %d, want 3", got)
	}

	// Embedded fallback has no correlated app-server root. Explicit observer
	// unavailability tells the reducer that configured OSC is the independent truth source.
	fallback, err := NewReducer(ReducerConfig{Generation: "generation-fallback", ProjectCWD: "/repo"})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}
	fallback.Reduce(ReducerEvent{Kind: EventObserverUnavailable})
	assertReducerState(t, fallback.Reduce(oscEvent("fallback-1")), attention.PhaseAttention, attention.ReasonDone, "osc:fallback-1")
}

func TestReducerReconnectNonIdleSnapshotClearsOutageCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		threads    []Thread
		wantPhase  attention.Phase
		wantReason attention.Reason
		wantID     string
	}{
		{
			name:      "active",
			threads:   []Thread{testThread("resume-root", "session-root", "", "/repo", activeStatus())},
			wantPhase: attention.PhaseWorking,
		},
		{
			name:       "waiting question",
			threads:    []Thread{testThread("resume-root", "session-root", "", "/repo", activeStatus(ActiveWaitingOnUserInput))},
			wantPhase:  attention.PhaseAttention,
			wantReason: attention.ReasonQuestion,
			wantID:     "question:" + testResumeID,
		},
		{
			name:       "system error",
			threads:    []Thread{testThread("resume-root", "session-root", "", "/repo", systemErrorStatus())},
			wantPhase:  attention.PhaseAttention,
			wantReason: attention.ReasonError,
			wantID:     "error:" + testResumeID,
		},
		{
			name:      "not loaded",
			threads:   []Thread{testThread("resume-root", "session-root", "", "/repo", notLoadedStatus())},
			wantPhase: attention.PhaseUnknown,
		},
		{
			name:      "root missing",
			threads:   nil,
			wantPhase: attention.PhaseUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := newResumeReducer(t)
			reducer.Reduce(threadEvent(testThread("resume-root", "session-root", "", "/repo", idleStatus())))
			reducer.Reduce(ReducerEvent{Kind: EventObserverLost})
			reducer.Reduce(oscEvent("during-outage"))
			got := reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: tt.threads})
			wantReason := tt.wantReason
			if wantReason == "" {
				wantReason = attention.ReasonNone
			}
			assertReducerState(t, got, tt.wantPhase, wantReason, tt.wantID)

			cleanRoot := testThread("resume-root", "session-root", "", "/repo", idleStatus())
			if len(tt.threads) == 0 {
				got = reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{cleanRoot}})
			} else {
				got = reducer.Reduce(statusEvent("resume-root", idleStatus()))
			}
			assertReducerState(t, got, attention.PhaseReady, attention.ReasonNone, "")
		})
	}
}

func TestReducerResumeRootRequiresExactCWDAndTopLevelRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		root Thread
	}{
		{
			name: "wrong cwd",
			root: testThread("resume-root", "session-root", "", "/other", idleStatus()),
		},
		{
			name: "claims parent",
			root: testThread("resume-root", "session-root", "foreign-parent", "/repo", idleStatus()),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" live", func(t *testing.T) {
			t.Parallel()
			reducer := newResumeReducer(t)
			assertReducerState(t, reducer.Reduce(threadEvent(tt.root)), attention.PhaseUnknown, attention.ReasonNone, "")
			good := testThread("resume-root", "session-root", "", "/repo", idleStatus())
			assertReducerState(t, reducer.Reduce(threadEvent(good)), attention.PhaseUnknown, attention.ReasonNone, "")
			assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{good}}), attention.PhaseReady, attention.ReasonNone, "")
		})
		t.Run(tt.name+" snapshot", func(t *testing.T) {
			t.Parallel()
			reducer := newResumeReducer(t)
			assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{tt.root}}), attention.PhaseUnknown, attention.ReasonNone, "")
		})
	}
}

func TestReducerFreshRootRequiresCanonicalUUID(t *testing.T) {
	t.Parallel()

	reducer, err := NewReducer(ReducerConfig{Generation: "generation-fresh-uuid", ProjectCWD: "/repo"})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}
	bad := testThread("not-a-uuid", "session", "", "/repo", idleStatus())
	assertReducerState(t, reducer.Reduce(threadEvent(bad)), attention.PhaseUnknown, attention.ReasonNone, "")
	if got := reducer.RootThreadID(); got != "" {
		t.Fatalf("invalid fresh root selected as %q", got)
	}
	good := testThread("fresh-root", "session", "", "/repo", idleStatus())
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{good}}), attention.PhaseReady, attention.ReasonNone, "")
}

func TestReducerRejectsParentSessionContradiction(t *testing.T) {
	t.Parallel()

	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	badChild := testThread("child", "different-session", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput))

	live := newResumeReducer(t)
	live.Reduce(threadEvent(root))
	assertReducerState(t, live.Reduce(threadEvent(badChild)), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, live.Reduce(statusEvent("resume-root", activeStatus())), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, live.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseWorking, attention.ReasonNone, "")

	snapshot := newResumeReducer(t)
	assertReducerState(t, snapshot.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root, badChild}}), attention.PhaseUnknown, attention.ReasonNone, "")
}

func TestReducerThreadCorrelationFieldsAreImmutableUntilSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Thread)
	}{
		{name: "session", mutate: func(thread *Thread) { thread.SessionID = "changed-session" }},
		{name: "parent", mutate: func(thread *Thread) { thread.ParentThreadID = "changed-parent" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := newResumeReducer(t)
			root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
			child := testThread("child", "session-root", "resume-root", "/repo", idleStatus())
			reducer.Reduce(threadEvent(root))
			reducer.Reduce(threadEvent(child))
			tt.mutate(&child)
			assertReducerState(t, reducer.Reduce(threadEvent(child)), attention.PhaseUnknown, attention.ReasonNone, "")
			assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseWorking, attention.ReasonNone, "")
		})
	}
}

func TestReducerReconnectWithLostProjectAncestryIsUnknown(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
	orphan := testThread("orphan", "different-session", "missing-parent", "/repo", activeStatus(ActiveWaitingOnUserInput))
	snapshot := ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root, orphan}}
	assertReducerState(t, reducer.Reduce(snapshot), attention.PhaseUnknown, attention.ReasonNone, "")
}

func TestReducerMalformedAndUnmappedEventsCannotCreateAttention(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
	reducer.Reduce(threadEvent(root))

	unmapped := []ReducerEvent{statusEvent("foreign", activeStatus(ActiveWaitingOnUserInput))}
	for _, event := range unmapped {
		assertReducerState(t, reducer.Reduce(event), attention.PhaseReady, attention.ReasonNone, "")
	}

	malformed := statusEvent("resume-root", ThreadStatus{Type: ThreadStatusActive, ActiveFlags: []ActiveFlag{"future-flag"}})
	assertReducerState(t, reducer.Reduce(malformed), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent("resume-root", activeStatus(ActiveWaitingOnUserInput))), attention.PhaseUnknown, attention.ReasonNone, "")

	// Only a clean full snapshot restores trust after malformed observer data.
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseReady, attention.ReasonNone, "")
}

func TestReducerNotLoadedIsUnknownWithoutPoisoningLaterStatus(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", idleStatus())
	reducer.Reduce(threadEvent(root))
	child := testThread("child", "session-root", "resume-root", "/repo", notLoadedStatus())
	assertReducerState(t, reducer.Reduce(threadEvent(child)), attention.PhaseUnknown, attention.ReasonNone, "")

	assertReducerState(t, reducer.Reduce(statusEvent("child", activeStatus())), attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent("child", idleStatus())), attention.PhaseReady, attention.ReasonNone, "")
}

func TestReducerThreadClosedRemovesChildContributionAndRootBecomesUnknown(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	reducer.Reduce(threadEvent(testThread("resume-root", "session-root", "", "/repo", activeStatus())))
	reducer.Reduce(threadEvent(testThread("child", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput))))
	assertReducerState(t, reducer.Current(), attention.PhaseAttention, attention.ReasonQuestion, "question:child")

	closedChild := ReducerEvent{Kind: EventThreadClosed, ThreadID: "child"}
	assertReducerState(t, reducer.Reduce(closedChild), attention.PhaseWorking, attention.ReasonNone, "")
	staleChild := testThread("child", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnUserInput))
	assertReducerState(t, reducer.Reduce(threadEvent(staleChild)), attention.PhaseWorking, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(statusEvent("child", activeStatus(ActiveWaitingOnUserInput))), attention.PhaseWorking, attention.ReasonNone, "")

	closedRoot := ReducerEvent{Kind: EventThreadClosed, ThreadID: testResumeID}
	assertReducerState(t, reducer.Reduce(closedRoot), attention.PhaseUnknown, attention.ReasonNone, "")
	staleRoot := testThread("resume-root", "session-root", "", "/repo", activeStatus(ActiveWaitingOnUserInput))
	assertReducerState(t, reducer.Reduce(threadEvent(staleRoot)), attention.PhaseUnknown, attention.ReasonNone, "")
	cleanRoot := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{cleanRoot}}), attention.PhaseWorking, attention.ReasonNone, "")
}

func TestReducerReparentingCannotLeaveStaleCorrelatedAttention(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	reducer.Reduce(threadEvent(testThread("resume-root", "session-root", "", "/repo", activeStatus())))
	child := testThread("child", "session-root", "resume-root", "/repo", activeStatus(ActiveWaitingOnApproval))
	reducer.Reduce(threadEvent(child))
	assertReducerState(t, reducer.Current(), attention.PhaseAttention, attention.ReasonPermission, "permission:child")

	child.SessionID = "foreign-session"
	child.ParentThreadID = "foreign-parent"
	assertReducerState(t, reducer.Reduce(threadEvent(child)), attention.PhaseUnknown, attention.ReasonNone, "")

	child.ParentThreadID = "child"
	assertReducerState(t, reducer.Reduce(threadEvent(child)), attention.PhaseUnknown, attention.ReasonNone, "")
}

func TestReducerSameSessionParentCycleLatchesUnknown(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	reducer.Reduce(threadEvent(root))
	reducer.Reduce(threadEvent(testThread("cycle-a", "session-root", "cycle-b", "/repo", activeStatus(ActiveWaitingOnUserInput))))
	cycleB := testThread("cycle-b", "session-root", "cycle-a", "/repo", idleStatus())
	assertReducerState(t, reducer.Reduce(threadEvent(cycleB)), attention.PhaseUnknown, attention.ReasonNone, "")

	assertReducerState(t, reducer.Reduce(statusEvent("resume-root", activeStatus())), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseWorking, attention.ReasonNone, "")
}

func TestReducerDuplicateKnownActiveFlagsAreIdempotent(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus(
		ActiveWaitingOnUserInput,
		ActiveWaitingOnUserInput,
		ActiveWaitingOnApproval,
		ActiveWaitingOnApproval,
	))
	assertReducerState(t, reducer.Reduce(threadEvent(root)), attention.PhaseAttention, attention.ReasonQuestion, "question:"+testResumeID)
}

func TestReducerBoundsSnapshotAndRecoversFromCleanSnapshot(t *testing.T) {
	t.Parallel()

	if MaxReducerEntries != 4096 || MaxReducerIDBytes != 256 {
		t.Fatalf("unexpected reducer bounds: entries=%d id=%d", MaxReducerEntries, MaxReducerIDBytes)
	}
	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	reducer.Reduce(threadEvent(root))

	tooMany := make([]Thread, MaxReducerEntries+1)
	for i := range tooMany {
		tooMany[i] = testThread(fmt.Sprintf("thread-%04d", i), fmt.Sprintf("session-%04d", i), "", "/other", idleStatus())
	}
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: tooMany}), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseWorking, attention.ReasonNone, "")
}

func TestReducerBoundsActiveAndPreservedUnknownEpochUnion(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	preservedThreads := MaxReducerEntries / 3
	snapshot := make([]Thread, 0, preservedThreads+1)
	for i := 0; i < preservedThreads; i++ {
		threadID := fmt.Sprintf("unknown-%04d", i)
		parentID := testResumeID
		if i == 0 {
			threadID = testResumeID
			parentID = ""
		}
		snapshot = append(snapshot, testThread(
			threadID, "session-root", parentID, "/repo", notLoadedStatus(),
		))
		reducer.status[statusRequestKey{threadID: threadID, kind: interactionQuestion}] = statusEpoch{announced: true}
		reducer.status[statusRequestKey{threadID: threadID, kind: interactionPermission}] = statusEpoch{announced: true}
		reducer.status[statusRequestKey{threadID: threadID, kind: interactionError}] = statusEpoch{announced: true}
	}
	snapshot = append(snapshot, testThread(
		"new-active", "session-root", testResumeID, "/repo",
		activeStatus(ActiveWaitingOnUserInput, ActiveWaitingOnApproval),
	))

	assertReducerState(t, reducer.Reduce(ReducerEvent{
		Kind: EventObserverSnapshot, Threads: snapshot,
	}), attention.PhaseUnknown, attention.ReasonNone, "")
	if got := len(reducer.status); got > MaxReducerEntries {
		t.Fatalf("status epoch union contains %d entries, limit %d", got, MaxReducerEntries)
	}
}

func TestReducerBoundsClosedThreadTombstones(t *testing.T) {
	t.Parallel()

	reducer := newResumeReducer(t)
	root := testThread("resume-root", "session-root", "", "/repo", activeStatus())
	reducer.Reduce(threadEvent(root))
	for i := 0; i < MaxReducerEntries; i++ {
		reducer.Reduce(ReducerEvent{Kind: EventThreadClosed, ThreadID: fmt.Sprintf("closed-%04d", i)})
	}
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventThreadClosed, ThreadID: "closed-overflow"}), attention.PhaseUnknown, attention.ReasonNone, "")
	assertReducerState(t, reducer.Reduce(ReducerEvent{Kind: EventObserverSnapshot, Threads: []Thread{root}}), attention.PhaseWorking, attention.ReasonNone, "")
}

func TestNewReducerRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []ReducerConfig{
		{},
		{Generation: "bad\tgeneration", ProjectCWD: "/repo"},
		{Generation: "generation", ProjectCWD: ""},
		{Generation: "generation", ProjectCWD: "/bad\ncwd"},
		{Generation: "generation", ProjectCWD: strings.Repeat("x", MaxReducerCWDBytes+1)},
		{Generation: "generation", ProjectCWD: "/repo", ResumeThreadID: "bad\nthread"},
		{Generation: "generation", ProjectCWD: "/repo", ResumeThreadID: "resume-root"},
		{Generation: "generation", ProjectCWD: "/repo", ResumeThreadID: strings.ToUpper(testResumeID)},
		{Generation: "generation", ProjectCWD: "/repo", ResumeThreadID: strings.Repeat("x", MaxReducerIDBytes+1)},
		{Generation: "generation", ProjectCWD: "/repo", BaselineThreadIDs: []string{""}},
		{Generation: "generation", ProjectCWD: "/repo", BaselineThreadIDs: []string{strings.Repeat("x", MaxReducerIDBytes+1)}},
	}
	tooManyBaseline := ReducerConfig{Generation: "generation", ProjectCWD: "/repo"}
	tooManyBaseline.BaselineThreadIDs = make([]string, MaxReducerEntries+1)
	for i := range tooManyBaseline.BaselineThreadIDs {
		tooManyBaseline.BaselineThreadIDs[i] = fmt.Sprintf("baseline-%04d", i)
	}
	tests = append(tests, tooManyBaseline)
	for _, config := range tests {
		if _, err := NewReducer(config); err == nil {
			t.Fatalf("NewReducer(%#v) succeeded", config)
		}
	}
}

func newResumeReducer(t *testing.T) *Reducer {
	t.Helper()
	reducer, err := NewReducer(ReducerConfig{
		Generation:     "generation-resume",
		ProjectCWD:     "/repo",
		ResumeThreadID: testResumeID,
	})
	if err != nil {
		t.Fatalf("NewReducer() error = %v", err)
	}
	return reducer
}

func testThread(id, sessionID, parentID, cwd string, status ThreadStatus) Thread {
	switch id {
	case "resume-root":
		id = testResumeID
	case "fresh-root", "root-a":
		id = testFreshID
	case "root-b":
		id = testFreshOther
	}
	switch parentID {
	case "resume-root":
		parentID = testResumeID
	case "fresh-root", "root-a":
		parentID = testFreshID
	case "root-b":
		parentID = testFreshOther
	}
	return Thread{
		ID:             id,
		SessionID:      sessionID,
		ParentThreadID: parentID,
		CWD:            cwd,
		Status:         status,
	}
}

func idleStatus() ThreadStatus {
	return ThreadStatus{Type: ThreadStatusIdle}
}

func systemErrorStatus() ThreadStatus {
	return ThreadStatus{Type: ThreadStatusSystemError}
}

func notLoadedStatus() ThreadStatus {
	return ThreadStatus{Type: ThreadStatusNotLoaded}
}

func activeStatus(flags ...ActiveFlag) ThreadStatus {
	return ThreadStatus{Type: ThreadStatusActive, ActiveFlags: flags}
}

func threadEvent(thread Thread) ReducerEvent {
	return ReducerEvent{Kind: EventThreadObserved, Thread: thread}
}

func statusEvent(threadID string, status ThreadStatus) ReducerEvent {
	if threadID == "resume-root" {
		threadID = testResumeID
	}
	return ReducerEvent{Kind: EventThreadStatus, ThreadID: threadID, Status: status}
}

func oscEvent(identity string) ReducerEvent {
	return ReducerEvent{Kind: EventOSC9Completion, Identity: identity}
}

func assertReducerState(t *testing.T, got attention.State, phase attention.Phase, reason attention.Reason, identity string) {
	t.Helper()
	want := attention.State{
		Generation: got.Generation,
		Phase:      phase,
		Reason:     reason,
		Identity:   identity,
	}
	if got != want {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
	if !strings.HasPrefix(got.Generation, "generation-") {
		t.Fatalf("state generation = %q", got.Generation)
	}
}
