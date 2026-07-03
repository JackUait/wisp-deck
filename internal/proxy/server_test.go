package proxy

import (
	"context"
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

func TestServer_waitsAndRetriesSameAccountWhenNoneOtherAvailable(t *testing.T) {
	// When there is no other account to fail over to, a transient 429 waits out
	// retry-after and retries the SAME (sole) account rather than giving up.
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

	// A mutable clock the sleep hook advances, so the sole account's throttle
	// window elapses during the (no-op) wait and it becomes retryable again.
	clk := time.Unix(1_700_000_000, 0)
	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}}, 0.98)
	srv := NewServer(mgr, "proxy-key", upstream.URL,
		WithNow(func() time.Time { return clk }),
		WithSleep(func(d time.Duration) { clk = clk.Add(d) }))

	rec := doRequest(t, srv, "proxy-key", `{}`)
	if rec.Code != 200 || rec.Body.String() != "ok-after-wait" {
		t.Fatalf("got (%d, %q), want (200, ok-after-wait)", rec.Code, rec.Body.String())
	}
	if mgr.ActiveIndex() != 0 {
		t.Errorf("active = %d, want 0 (retried the sole account)", mgr.ActiveIndex())
	}
}

func TestServer_failsOverImmediatelyWithoutWaitingOnRateLimit(t *testing.T) {
	// Regression: hitting an account's limit must switch to another available
	// account WITHOUT first waiting out retry-after on the exhausted one. The
	// old code slept retry-after and retried the SAME account for poolSize
	// attempts before failing over — up to poolSize*300s of hanging on a dead
	// account — which read as "it didn't switch".
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tok-A" {
			w.Header().Set("retry-after", "300")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
		io.WriteString(w, "ok-from-B")
	}))
	defer upstream.Close()

	var slept []time.Duration
	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	srv := NewServer(mgr, "proxy-key", upstream.URL, WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	rec := doRequest(t, srv, "proxy-key", `{}`)
	if rec.Code != 200 || rec.Body.String() != "ok-from-B" {
		t.Fatalf("got (%d, %q), want (200, ok-from-B) after immediate failover", rec.Code, rec.Body.String())
	}
	if mgr.ActiveIndex() != 1 {
		t.Errorf("active = %d, want 1 (switched to B)", mgr.ActiveIndex())
	}
	if len(slept) != 0 {
		t.Errorf("must not wait on the exhausted account when another is free; slept %v", slept)
	}
}

func TestServer_sidelinesRateLimitedAccountEvenIfClientAbandonsMidWait(t *testing.T) {
	// Regression for the reported "didn't switch" bug: a 429 must sideline the
	// account immediately, before any wait. The old code only marked the account
	// throttled after poolSize retries, and checked for client disconnect only
	// AFTER the retry-after sleep — so a client that gave up during the wait left
	// the account fully "available". Every later request then re-selected the
	// same exhausted account and never switched.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both accounts are out of quota this request; the point is the account
		// must be sidelined even when the client goes away mid-request.
		w.Header().Set("retry-after", "300")
		w.WriteHeader(429)
	}))
	defer upstream.Close()

	base := time.Unix(1_700_000_000, 0)
	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)

	ctx, cancel := context.WithCancel(context.Background())
	// Simulate the client giving up during the very first retry-after wait.
	srv := NewServer(mgr, "proxy-key", upstream.URL,
		WithSleep(func(time.Duration) { cancel() }),
		WithNow(func() time.Time { return base }))

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("x-api-key", "proxy-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// The current account must be sidelined for the retry-after window even
	// though the client abandoned the request — otherwise the next request just
	// hits the same exhausted account again.
	if mgr.isAvailable(0, base) {
		t.Error("rate-limited account should be sidelined immediately, but it is still available after the client abandoned the request")
	}
	if !mgr.isAvailable(0, base.Add(301*time.Second)) {
		t.Error("account should recover once retry-after elapses")
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

func TestServer_servesFromNearQuotaAccountWhenAllNearQuota(t *testing.T) {
	// Regression: when every pooled account sits at/above the switch threshold
	// but upstream still accepts requests, the proxy must forward from the
	// least-utilized account rather than fabricating an "all accounts exhausted"
	// error — otherwise the session is interrupted at 98% when it would have kept
	// working without the proxy.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "still-serving")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	mgr.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99"))
	mgr.UpdateQuota(1, hdr("anthropic-ratelimit-unified-7d-utilization", "0.985"))
	srv := newTestServer(t, mgr, upstream.URL)

	rec := doRequest(t, srv, "proxy-key", `{}`)
	if rec.Code != 200 || rec.Body.String() != "still-serving" {
		t.Fatalf("got (%d, %q), want (200, still-serving) — near-quota is not exhausted", rec.Code, rec.Body.String())
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
