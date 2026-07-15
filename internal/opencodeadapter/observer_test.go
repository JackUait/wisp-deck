package opencodeadapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/attention"
)

type recordingPublisher struct {
	mu     sync.Mutex
	states []attention.State
	err    error
}

func (p *recordingPublisher) Publish(phase attention.Phase, reason attention.Reason, identity string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.states = append(p.states, attention.State{Phase: phase, Reason: reason, Identity: identity})
	return p.err
}

func (p *recordingPublisher) snapshot() []attention.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]attention.State(nil), p.states...)
}

func serveEmptyOpenCodeSnapshot(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/session", "/question", "/permission":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[]")
		return true
	case "/session/status":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}")
		return true
	default:
		return false
	}
}

func TestObserveEventsAuthenticatesParsesSSEAndPublishesInOrder(t *testing.T) {
	const password = "per-generation-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if serveEmptyOpenCodeSnapshot(w, r) {
			return
		}
		if r.URL.Path != "/event" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		flusher := w.(http.Flusher)
		for _, data := range []string{
			`{"type":"server.connected","properties":{}}`,
			`{"type":"session.created","properties":{"sessionID":"root","info":{"id":"root"}}}`,
			`{"type":"session.status","properties":{"sessionID":"root","status":{"type":"busy"}}}`,
			`{"type":"session.status","properties":{"sessionID":"root","status":{"type":"idle"}}}`,
		} {
			fmt.Fprintf(w, ": comment\r\ndata: %s\r\n\r\n", data)
			flusher.Flush()
		}
	}))
	defer server.Close()

	reducer := newTestReducer(t)
	publisher := &recordingPublisher{}
	ready := make(chan struct{}, 1)
	err := ObserveEvents(context.Background(), ObserverOptions{
		BaseURL: server.URL, Password: password, Reducer: reducer, Publisher: publisher,
		OnReady: func() { ready <- struct{}{} },
	})
	if err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("observer error = %v, want stream ended", err)
	}
	select {
	case <-ready:
	default:
		t.Fatal("observer never reported authenticated readiness")
	}
	states := publisher.snapshot()
	if len(states) != 4 {
		t.Fatalf("published states = %d, want 4: %#v", len(states), states)
	}
	if states[2].Phase != attention.PhaseWorking || states[3].Phase != attention.PhaseAttention || states[3].Reason != attention.ReasonDone {
		t.Fatalf("published state sequence = %#v", states)
	}
}

func TestObserveEventsHydratesAuthenticatedSnapshotBeforeReady(t *testing.T) {
	const password = "snapshot-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("directory") != "/workspace/project" && r.URL.Path != "/event" {
			http.Error(w, "missing directory", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/session":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"root"},{"id":"child","parentID":"root"}]`)
		case "/session/status":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"root":{"type":"busy"},"child":{"type":"idle"}}`)
		case "/question":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"question-1","sessionID":"root","questions":[{"header":"Choose","question":"Proceed?","options":[]}]}]`)
		case "/permission":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"permission-1","sessionID":"root","permission":"bash","patterns":[],"metadata":{},"always":[]}]`)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	publisher := &recordingPublisher{}
	ready := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ObserveEvents(ctx, ObserverOptions{
			BaseURL: server.URL, Password: password, Directory: "/workspace/project",
			Reducer: newTestReducer(t), Publisher: publisher,
			OnReady: func() { ready <- struct{}{} },
		})
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("observer did not hydrate and become ready")
	}
	states := publisher.snapshot()
	if len(states) != 1 || states[0].Phase != attention.PhaseAttention ||
		states[0].Reason != attention.ReasonQuestion || states[0].Identity != "question:question-1" {
		t.Fatalf("hydrated state = %#v, want pending question identity", states)
	}
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestObserveEventsJoinsMultilineDataAndRejectsRedirects(t *testing.T) {
	t.Run("multiline data is one JSON event", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if serveEmptyOpenCodeSnapshot(w, r) {
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\",\n")
			fmt.Fprint(w, "data: \"properties\":{}}\n\n")
		}))
		defer server.Close()
		publisher := &recordingPublisher{}
		err := ObserveEvents(context.Background(), ObserverOptions{
			BaseURL: server.URL, Password: "secret", Reducer: newTestReducer(t), Publisher: publisher,
		})
		if err == nil || len(publisher.snapshot()) != 1 {
			t.Fatalf("multiline observer = %v, states %#v", err, publisher.snapshot())
		}
	})

	t.Run("redirect is rejected", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer target.Close()
		redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
		defer redirect.Close()
		err := ObserveEvents(context.Background(), ObserverOptions{
			BaseURL: redirect.URL, Password: "secret", Reducer: newTestReducer(t), Publisher: &recordingPublisher{},
		})
		if err == nil || !strings.Contains(err.Error(), "redirect") {
			t.Fatalf("redirect error = %v", err)
		}
	})
}

func TestObserveEventsRejectsWrongContentTypeOversizeAndMalformedJSON(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{name: "content type", contentType: "application/json", body: `{}`, want: "content type"},
		{name: "oversize", contentType: "text/event-stream", body: "data: " + strings.Repeat("x", MaxSSEEventBytes+1) + "\n\n", want: "exceeds"},
		{name: "malformed", contentType: "text/event-stream", body: "data: {bad}\n\n", want: "decode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveEmptyOpenCodeSnapshot(w, r) {
					return
				}
				w.Header().Set("Content-Type", test.contentType)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			reducer := newTestReducer(t)
			err := ObserveEvents(context.Background(), ObserverOptions{
				BaseURL: server.URL, Password: "secret", Reducer: reducer, Publisher: &recordingPublisher{},
			})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if test.name == "malformed" && !reducer.Invalid() {
				t.Fatal("malformed known stream did not latch reducer invalid")
			}
		})
	}
}

func TestObserveEventsCancellationUnblocksSilentStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveEmptyOpenCodeSnapshot(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ObserveEvents(ctx, ObserverOptions{
			BaseURL: server.URL, Password: "secret", Reducer: newTestReducer(t), Publisher: &recordingPublisher{},
		})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("observer did not stop after cancellation")
	}
}
