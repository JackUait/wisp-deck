package allin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixedResolver struct{ credential Credential }

func (f fixedResolver) Resolve(Target) (Credential, error) { return f.credential, nil }

func observeOnce(t *testing.T, upstream, model string) http.Header {
	t.Helper()
	var seen http.Header
	called := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	resolver := fixedResolver{Credential{BaseURL: backend.URL, Header: "Authorization", Value: "Bearer swapped"}}
	handler := NewObservingHandler(resolver, upstream, func(h http.Header) { called = true; seen = h })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"`+model+`"}`))
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("X-Api-Key", "session-key")
	req.Header.Set("Anthropic-Beta", "something")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("observe was not called")
	}
	return seen
}

func TestHandler_hands_the_session_auth_to_the_observer_before_the_swap(t *testing.T) {
	seen := observeOnce(t, "https://api.anthropic.com", "wisp/acct.personal/claude-opus-5-5[1m]")
	if seen.Get("Authorization") != "Bearer session" || seen.Get("X-Api-Key") != "session-key" {
		t.Fatalf("seen = %v", seen)
	}
	if seen.Get("Anthropic-Beta") != "" {
		t.Fatal("only credential headers may be handed on")
	}
}

func TestHandler_passes_no_session_auth_for_a_non_anthropic_upstream(t *testing.T) {
	if seen := observeOnce(t, "https://api.z.ai/api/anthropic", "wisp/acct.personal/claude-opus-5[1m]"); len(seen) != 0 {
		t.Fatalf("a non-Anthropic session credential leaked: %v", seen)
	}
}
