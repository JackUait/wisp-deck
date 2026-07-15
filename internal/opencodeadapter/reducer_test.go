package opencodeadapter

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/attention"
)

func decodeTestEvent(t *testing.T, raw string) Event {
	t.Helper()
	event, err := DecodeEvent([]byte(raw))
	if err != nil {
		t.Fatalf("decode event: %v\n%s", err, raw)
	}
	return event
}

func newTestReducer(t *testing.T) *Reducer {
	t.Helper()
	reducer, err := NewReducer("generation.OpenCodeReducer")
	if err != nil {
		t.Fatal(err)
	}
	return reducer
}

func assertReducerState(t *testing.T, got attention.State, phase attention.Phase, reason attention.Reason) {
	t.Helper()
	if got.Phase != phase || got.Reason != reason {
		t.Fatalf("state = %+v, want phase=%s reason=%s", got, phase, reason)
	}
}

func TestReducerArmsBusyRootAndPublishesOneCompletionPerEpoch(t *testing.T) {
	r := newTestReducer(t)
	r.Apply(decodeTestEvent(t, `{"type":"session.created","properties":{"sessionID":"ses_root","info":{"id":"ses_root"}}}`))
	assertReducerState(t, r.Current(), attention.PhaseUnknown, attention.ReasonNone)
	r.Apply(decodeTestEvent(t, `{"type":"session.status","properties":{"sessionID":"ses_root","status":{"type":"busy"}}}`))
	assertReducerState(t, r.Current(), attention.PhaseWorking, attention.ReasonNone)
	first := r.Apply(decodeTestEvent(t, `{"type":"session.status","properties":{"sessionID":"ses_root","status":{"type":"idle"}}}`))
	assertReducerState(t, first, attention.PhaseAttention, attention.ReasonDone)
	if first.Identity == "" {
		t.Fatal("completion identity is empty")
	}
	duplicate := r.Apply(decodeTestEvent(t, `{"type":"session.status","properties":{"sessionID":"ses_root","status":{"type":"idle"}}}`))
	if duplicate.Identity != first.Identity {
		t.Fatalf("duplicate idle identity = %q, want %q", duplicate.Identity, first.Identity)
	}
	r.Apply(decodeTestEvent(t, `{"type":"session.status","properties":{"sessionID":"ses_root","status":{"type":"busy"}}}`))
	second := r.Apply(decodeTestEvent(t, `{"type":"session.status","properties":{"sessionID":"ses_root","status":{"type":"idle"}}}`))
	if second.Identity == first.Identity {
		t.Fatal("second busy/idle epoch reused completion identity")
	}
}

func TestReducerCorrelatesChildQuestionsAndPermissionsToRoot(t *testing.T) {
	r := newTestReducer(t)
	for _, raw := range []string{
		`{"type":"session.created","properties":{"sessionID":"root","info":{"id":"root"}}}`,
		`{"type":"session.created","properties":{"sessionID":"child","info":{"id":"child","parentID":"root"}}}`,
		`{"type":"question.asked","properties":{"id":"q1","sessionID":"child","questions":[{"header":"Choice","question":"Continue?","options":[{"label":"Yes","description":"continue"}]}]}}`,
	} {
		r.Apply(decodeTestEvent(t, raw))
	}
	question := r.Current()
	assertReducerState(t, question, attention.PhaseAttention, attention.ReasonQuestion)
	if question.Identity != "question:q1" {
		t.Fatalf("question identity = %q", question.Identity)
	}
	r.Apply(decodeTestEvent(t, `{"type":"permission.asked","properties":{"id":"p1","sessionID":"root","permission":"bash","patterns":["*"],"metadata":{},"always":[]}}`))
	assertReducerState(t, r.Current(), attention.PhaseAttention, attention.ReasonQuestion)
	r.Apply(decodeTestEvent(t, `{"type":"question.replied","properties":{"requestID":"q1","sessionID":"child","answers":[["Yes"]]}}`))
	permission := r.Current()
	assertReducerState(t, permission, attention.PhaseAttention, attention.ReasonPermission)
	if permission.Identity != "permission:p1" {
		t.Fatalf("permission identity = %q", permission.Identity)
	}
	r.Apply(decodeTestEvent(t, `{"type":"permission.replied","properties":{"requestID":"p1","sessionID":"root","reply":"once"}}`))
	assertReducerState(t, r.Current(), attention.PhaseUnknown, attention.ReasonNone)
}

func TestReducerRootErrorWinsAndDuplicateEventDoesNotReplay(t *testing.T) {
	r := newTestReducer(t)
	r.Apply(decodeTestEvent(t, `{"type":"session.created","properties":{"sessionID":"root","info":{"id":"root"}}}`))
	r.Apply(decodeTestEvent(t, `{"id":"evt_error","type":"session.error","properties":{"sessionID":"root","error":{"name":"UnknownError","data":{"message":"boom"}}}}`))
	first := r.Current()
	assertReducerState(t, first, attention.PhaseAttention, attention.ReasonError)
	if first.Identity != "error:root:evt_error" {
		t.Fatalf("error identity = %q", first.Identity)
	}
	duplicate := r.Apply(decodeTestEvent(t, `{"id":"evt_error","type":"session.error","properties":{"sessionID":"root","error":{"name":"UnknownError","data":{"message":"boom"}}}}`))
	if duplicate.Identity != first.Identity {
		t.Fatalf("duplicate error identity = %q, want %q", duplicate.Identity, first.Identity)
	}
	r.Apply(decodeTestEvent(t, `{"type":"session.status","properties":{"sessionID":"root","status":{"type":"idle"}}}`))
	afterIdle := r.Current()
	assertReducerState(t, afterIdle, attention.PhaseAttention, attention.ReasonError)
	if afterIdle.Identity != first.Identity {
		t.Fatalf("idle after error changed identity = %q, want %q", afterIdle.Identity, first.Identity)
	}
}

func TestReducerChildCompletionAndErrorDoNotBecomeRootAttention(t *testing.T) {
	r := newTestReducer(t)
	for _, raw := range []string{
		`{"type":"session.created","properties":{"sessionID":"root","info":{"id":"root"}}}`,
		`{"type":"session.created","properties":{"sessionID":"child","info":{"id":"child","parentID":"root"}}}`,
		`{"type":"session.status","properties":{"sessionID":"child","status":{"type":"busy"}}}`,
		`{"type":"session.status","properties":{"sessionID":"child","status":{"type":"idle"}}}`,
		`{"id":"child_error","type":"session.error","properties":{"sessionID":"child","error":{"name":"UnknownError","data":{"message":"boom"}}}}`,
	} {
		r.Apply(decodeTestEvent(t, raw))
	}
	if got := r.Current(); got.Phase == attention.PhaseAttention {
		t.Fatalf("child terminal state became root attention: %+v", got)
	}
}

func TestReducerDeletesSessionTreeAndRejectsCycles(t *testing.T) {
	r := newTestReducer(t)
	r.Apply(decodeTestEvent(t, `{"type":"session.created","properties":{"sessionID":"root","info":{"id":"root"}}}`))
	r.Apply(decodeTestEvent(t, `{"type":"session.created","properties":{"sessionID":"child","info":{"id":"child","parentID":"root"}}}`))
	r.Apply(decodeTestEvent(t, `{"type":"question.asked","properties":{"id":"q1","sessionID":"child","questions":[]}}`))
	r.Apply(decodeTestEvent(t, `{"type":"session.deleted","properties":{"sessionID":"root","info":{"id":"root"}}}`))
	assertReducerState(t, r.Current(), attention.PhaseUnknown, attention.ReasonNone)

	bad := newTestReducer(t)
	bad.Apply(decodeTestEvent(t, `{"type":"session.created","properties":{"sessionID":"a","info":{"id":"a","parentID":"b"}}}`))
	bad.Apply(decodeTestEvent(t, `{"type":"session.created","properties":{"sessionID":"b","info":{"id":"b","parentID":"a"}}}`))
	assertReducerState(t, bad.Current(), attention.PhaseUnknown, attention.ReasonNone)
	if !bad.Invalid() {
		t.Fatal("session cycle did not latch invalid state")
	}
}

func TestDecodeEventRejectsMalformedKnownSchemasAndBounds(t *testing.T) {
	bad := []string{
		`{"type":"session.status","properties":{"sessionID":"root","status":{"type":"finished"}}}`,
		`{"type":"session.status","properties":{"sessionID":"root","status":{"type":"retry","attempt":1,"message":"retry","next":2,"action":null}}}`,
		`{"type":"session.status","properties":{"sessionID":"root","status":{"type":"retry","attempt":1,"message":"retry","next":2,"action":{"reason":"r"}}}}`,
		`{"type":"session.created","properties":{"sessionID":"a","info":{"id":"b"}}}`,
		`{"type":"session.created","properties":{"sessionID":"a","info":{"id":"a","parentID":null}}}`,
		`{"type":"question.asked","properties":{"id":"q","sessionID":"s","questions":"wrong"}}`,
		`{"type":"question.asked","properties":{"id":"q","sessionID":"s"}}`,
		`{"type":"question.asked","properties":{"id":"q","sessionID":"s","questions":[],"tool":{"messageID":"m"}}}`,
		`{"type":"question.asked","properties":{"id":"q","sessionID":"s","questions":[{"header":"h","question":"q","options":[],"multiple":null}]}}`,
		`{"type":"question.replied","properties":{"requestID":"q","sessionID":"s"}}`,
		`{"type":"permission.asked","properties":{"id":"p","sessionID":"s","permission":"bash"}}`,
		`{"type":"permission.replied","properties":{"requestID":"p","sessionID":"s","reply":"maybe"}}`,
		`{"id":"e","type":"session.error","properties":{"sessionID":"s","error":{"name":"Invented","data":{}}}}`,
		`{"id":"e","type":"session.error","properties":{"sessionID":"s","error":{"name":"StructuredOutputError","data":{"message":"bad"}}}}`,
		`{"id":"e","type":"session.error","properties":{"sessionID":"s","error":{"name":"APIError","data":{"message":"bad","isRetryable":false,"statusCode":-1}}}}`,
		`{"id":"e","type":"session.error","properties":{"sessionID":"s","error":{"name":"UnknownError","data":{"message":"bad","ref":7}}}}`,
	}
	for _, raw := range bad {
		if _, err := DecodeEvent([]byte(raw)); err == nil {
			t.Fatalf("malformed event accepted: %s", raw)
		}
	}
	oversize := `{"type":"session.created","properties":{"sessionID":"` + strings.Repeat("x", MaxIdentifierBytes+1) + `","info":{"id":"x"}}}`
	if _, err := DecodeEvent([]byte(oversize)); err == nil {
		t.Fatal("oversize identifier accepted")
	}
	ignored, err := DecodeEvent([]byte(`{"type":"server.connected","properties":{}}`))
	if err != nil || ignored.Kind != EventIgnored {
		t.Fatalf("unrelated server event = %#v, %v", ignored, err)
	}
}
