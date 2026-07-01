package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, mgr *Manager, upstream string) *Server {
	t.Helper()
	return NewServer(mgr, "proxy-key", upstream)
}

func doRequest(t *testing.T, srv *Server, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	if key != "" {
		req.Header.Set("x-api-key", key)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestServer_injectsActiveTokenAndStripsClientKey(t *testing.T) {
	var gotAuth, gotXAPIKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotXAPIKey = r.Header.Get("x-api-key")
		w.Header().Set("anthropic-ratelimit-unified-5h-utilization", "0.3")
		w.WriteHeader(200)
		io.WriteString(w, "hello-body")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)

	rec := doRequest(t, srv, "proxy-key", `{"x":1}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hello-body" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if gotAuth != "Bearer tok-A" {
		t.Errorf("upstream Authorization = %q, want Bearer tok-A", gotAuth)
	}
	if gotXAPIKey != "" {
		t.Errorf("client x-api-key should be stripped, got %q", gotXAPIKey)
	}
	if u := mgr.Utilization(0); u < 0.29 || u > 0.31 {
		t.Errorf("quota not learned from response headers: %v", u)
	}
}

func TestServer_rejectsBadProxyKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be reached on bad key")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{AccessToken: "tok-A"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)
	rec := doRequest(t, srv, "wrong", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestServer_switchesAccountOn429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Account A is rate-limited; account B succeeds.
		if r.Header.Get("Authorization") == "Bearer tok-A" {
			w.Header().Set("retry-after", "1")
			w.WriteHeader(429)
			io.WriteString(w, "rate limited")
			return
		}
		w.WriteHeader(200)
		io.WriteString(w, "ok-from-B")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)

	rec := doRequest(t, srv, "proxy-key", `{}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 after failover", rec.Code)
	}
	if rec.Body.String() != "ok-from-B" {
		t.Errorf("body = %q, want ok-from-B", rec.Body.String())
	}
	if mgr.ActiveIndex() != 1 {
		t.Errorf("active = %d, want 1 (switched to B)", mgr.ActiveIndex())
	}
}

func TestServer_passesThrough429WhenAllExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "5")
		w.WriteHeader(429)
		io.WriteString(w, "all limited")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)

	rec := doRequest(t, srv, "proxy-key", `{}`)
	if rec.Code != 429 {
		t.Errorf("status = %d, want 429 passthrough", rec.Code)
	}
}
