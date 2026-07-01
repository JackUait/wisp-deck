package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, mgr *Manager, upstream string) *Server {
	t.Helper()
	// No-op sleep so retry-after waits don't slow the tests.
	return NewServer(mgr, "proxy-key", upstream, WithSleep(func(time.Duration) {}))
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

func TestServer_loopbackDoesNotBypassAuth(t *testing.T) {
	// A loopback RemoteAddr must NOT bypass the key check — loopback is not a
	// trust boundary on a multi-user host.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be reached without a valid key")
	}))
	defer upstream.Close()
	mgr := NewManager([]Account{{AccessToken: "tok-A"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:5000" // loopback, but NO key
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (loopback must still require the key)", rec.Code)
	}
}

func TestServer_connectRequiresProxyAuth(t *testing.T) {
	// CONNECT must be authenticated (via Proxy-Authorization) before any tunnel
	// or hijack — otherwise the proxy is an open forward proxy.
	mgr := NewManager([]Account{{AccessToken: "tok-A"}, {AccessToken: "tok-B"}}, 0.98)
	srv := NewServer(mgr, "proxy-key", "https://api.anthropic.com", WithSleep(func(time.Duration) {}))
	if _, err := srv.EnableMITM(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodConnect, "//api.anthropic.com:443", nil)
	req.Host = "api.anthropic.com:443"
	req.RemoteAddr = "127.0.0.1:5000" // loopback must not help
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want 407 for unauthenticated CONNECT", rec.Code)
	}
}

func TestServer_retriesSameAccountOnTransient429(t *testing.T) {
	// teamclaude pattern: a transient 429 waits retry-after and retries the SAME
	// account rather than immediately switching.
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("retry-after", "1")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
		io.WriteString(w, "ok-after-wait")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)

	rec := doRequest(t, srv, "proxy-key", `{}`)
	if rec.Code != 200 || rec.Body.String() != "ok-after-wait" {
		t.Fatalf("got (%d, %q), want (200, ok-after-wait)", rec.Code, rec.Body.String())
	}
	if mgr.ActiveIndex() != 0 {
		t.Errorf("active = %d, want 0 (stayed on A after transient 429)", mgr.ActiveIndex())
	}
}

func TestServer_stripsAcceptEncodingAndDoesNotFollowRedirects(t *testing.T) {
	var sawAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Location", "https://example.com/elsewhere")
		w.WriteHeader(302)
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{AccessToken: "tok-A"}, {AccessToken: "tok-B"}}, 0.98)
	srv := newTestServer(t, mgr, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("x-api-key", "proxy-key")
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// The client's explicit "gzip, br" must not be forwarded; the transport may
	// substitute its own single "gzip" for transparent decompression (as undici
	// does in teamclaude), but never the client's value.
	if sawAcceptEncoding == "gzip, br" {
		t.Errorf("client accept-encoding should be stripped, got forwarded %q", sawAcceptEncoding)
	}
	if rec.Code != 302 {
		t.Errorf("redirect should pass through unfollowed, got %d", rec.Code)
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
	// teamclaude returns a structured rate_limit_error body when all exhausted.
	if !strings.Contains(rec.Body.String(), "rate_limit_error") {
		t.Errorf("body = %q, want a rate_limit_error payload", rec.Body.String())
	}
	if rec.Header().Get("retry-after") == "" {
		t.Error("exhausted response should carry a retry-after header")
	}
}
