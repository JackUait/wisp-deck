package proxy

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// hopByHopHeaders are connection-scoped headers never forwarded upstream.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// Server is the account-rotation reverse proxy: it authenticates the local
// client, injects the active account's OAuth token, forwards to the Anthropic
// upstream, learns quota from the response, and fails over to another account
// on 429.
type Server struct {
	mgr           *Manager
	proxyKey      string
	upstream      string // base URL, e.g. https://api.anthropic.com
	tokenEndpoint string
	accountsDir   string // for writing refreshed credentials back
	client        *http.Client
	now           func() time.Time
	maxAttempts   int
}

// Option configures a Server.
type Option func(*Server)

// WithTokenEndpoint overrides the OAuth refresh endpoint (used in tests).
func WithTokenEndpoint(url string) Option { return func(s *Server) { s.tokenEndpoint = url } }

// WithAccountsDir sets the directory holding per-account credential dirs so
// refreshed tokens can be written back.
func WithAccountsDir(dir string) Option { return func(s *Server) { s.accountsDir = dir } }

// WithNow overrides the clock (used in tests).
func WithNow(now func() time.Time) Option { return func(s *Server) { s.now = now } }

// NewServer builds a Server over the account manager.
func NewServer(mgr *Manager, proxyKey, upstream string, opts ...Option) *Server {
	s := &Server{
		mgr:           mgr,
		proxyKey:      proxyKey,
		upstream:      strings.TrimRight(upstream, "/"),
		tokenEndpoint: DefaultTokenEndpoint,
		client:        &http.Client{Timeout: 0}, // streaming; no overall timeout
		now:           time.Now,
		maxAttempts:   0, // 0 → default to pool size at request time
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Authenticate the local client. Claude Code sends the proxy key as x-api-key
	// (and/or Authorization: Bearer <key>); either is accepted.
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Buffer the body so it can be replayed across failover attempts.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadGateway)
		return
	}
	r.Body.Close()

	attempts := s.maxAttempts
	if attempts <= 0 {
		attempts = s.mgr.Len()
	}
	tried := map[int]bool{}

	for attempt := 0; attempt < attempts; attempt++ {
		now := s.now()
		idx, _ := s.mgr.Rotate(now)
		if tried[idx] {
			// Rotate returned an account we already tried this request (no fresh
			// account available) — stop and surface the last upstream 429 below.
			break
		}
		tried[idx] = true

		s.ensureFreshToken(idx, now)
		acct := s.mgr.AccountAt(idx)

		resp, err := s.forward(r, body, acct.AccessToken)
		if err != nil {
			log.Printf("[wisp-deck-proxy] upstream error on %q: %v", acct.Label, err)
			s.mgr.MarkThrottled(idx, now.Add(5*time.Second))
			continue
		}
		s.mgr.UpdateQuota(idx, resp.Header)

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(resp.Header.Get("retry-after"))
			s.mgr.MarkThrottled(idx, now.Add(retryAfter))
			log.Printf("[wisp-deck-proxy] 429 on %q — throttling %s and failing over", acct.Label, retryAfter)
			// If another account is available, drain this response and retry;
			// otherwise pass the 429 through to the client.
			if s.mgr.HasAvailable(now) && len(tried) < attempts {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				continue
			}
		}

		s.relay(w, resp)
		return
	}

	// Every account is exhausted — synthesize a 429 for the client.
	w.Header().Set("retry-after", "60")
	http.Error(w, "all accounts rate-limited", http.StatusTooManyRequests)
}

func (s *Server) authorized(r *http.Request) bool {
	if s.proxyKey == "" {
		return true
	}
	if r.Header.Get("x-api-key") == s.proxyKey {
		return true
	}
	if auth := r.Header.Get("Authorization"); auth == "Bearer "+s.proxyKey {
		return true
	}
	return false
}

// ensureFreshToken refreshes an account's OAuth token if it is near expiry,
// persisting the new token to the manager and the account's credentials file.
func (s *Server) ensureFreshToken(idx int, now time.Time) {
	if !s.mgr.NeedsRefresh(idx, now) {
		return
	}
	acct := s.mgr.AccountAt(idx)
	tok, err := RefreshToken(s.tokenEndpoint, acct.RefreshToken)
	if err != nil {
		log.Printf("[wisp-deck-proxy] token refresh failed for %q: %v", acct.Label, err)
		return
	}
	s.mgr.SetTokens(idx, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt)
	if s.accountsDir != "" && acct.Dir != "" {
		path := filepath.Join(s.accountsDir, acct.Dir, ".credentials.json")
		if err := WriteCredentials(path, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt); err != nil {
			log.Printf("[wisp-deck-proxy] could not persist refreshed token for %q: %v", acct.Label, err)
		}
	}
}

// forward sends the buffered request upstream with the given bearer token.
func (s *Server) forward(r *http.Request, body []byte, token string) (*http.Response, error) {
	url := s.upstream + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vv := range r.Header {
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] || lk == "x-api-key" || lk == "authorization" || lk == "host" {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return s.client.Do(req)
}

// relay copies an upstream response back to the client, streaming the body.
func (s *Server) relay(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 5 * time.Second
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 5 * time.Second
}
