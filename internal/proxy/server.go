package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	sleep         func(time.Duration)
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

// WithSleep overrides the retry-after wait function (used in tests).
func WithSleep(sleep func(time.Duration)) Option { return func(s *Server) { s.sleep = sleep } }

// NewServer builds a Server over the account manager.
func NewServer(mgr *Manager, proxyKey, upstream string, opts ...Option) *Server {
	s := &Server{
		mgr:           mgr,
		proxyKey:      proxyKey,
		upstream:      strings.TrimRight(upstream, "/"),
		tokenEndpoint: DefaultTokenEndpoint,
		// Streaming; no overall timeout. Don't follow redirects — mirror
		// teamclaude's `redirect: 'manual'` so upstream 3xx passes through.
		client: &http.Client{
			Timeout:       0,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   time.Now,
		sleep: time.Sleep,
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

	// Bound total attempts by the pool size (teamclaude's maxRetries), and track
	// accounts already tried/failed this request so failover skips them.
	maxRetries := s.mgr.Len()
	tried := map[int]bool{}

	for retryCount := 0; ; retryCount++ {
		now := s.now()
		idx, ok := s.mgr.GetActiveAccount(tried, now)
		if !ok {
			s.writeExhausted(w)
			return
		}

		// Refresh a near-expiry token; a hard failure (dead refresh token) takes
		// the account out of rotation for this request and we fail over.
		if !s.ensureFreshToken(idx, now) {
			tried[idx] = true
			if retryCount < maxRetries {
				continue
			}
			s.writeExhausted(w)
			return
		}
		acct := s.mgr.AccountAt(idx)

		resp, err := s.forward(r, body, acct.AccessToken)
		if err != nil {
			// A transport error is not proof the credentials are bad (that comes
			// back as a 401 *response*). Skip this account for THIS request only
			// and fail over, matching teamclaude's non-transient error path.
			log.Printf("[wisp-deck-proxy] upstream error on %q: %v", acct.Label, err)
			tried[idx] = true
			if retryCount < maxRetries {
				continue
			}
			s.writeError(w, http.StatusBadGateway, "proxy_error", "Upstream error: "+err.Error())
			return
		}
		s.mgr.UpdateQuota(idx, resp.Header)

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := clampRetryAfter(resp.Header.Get("retry-after"))
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if retryCount >= maxRetries {
				// Persistent 429: throttle this account and re-dispatch so the next
				// iteration picks another account (or reports all exhausted).
				log.Printf("[wisp-deck-proxy] persistent 429 on %q — throttling %s and re-dispatching", acct.Label, retryAfter)
				s.mgr.MarkThrottled(idx, now.Add(retryAfter))
				continue
			}
			// Transient 429: wait the retry-after and retry the SAME account.
			log.Printf("[wisp-deck-proxy] 429 on %q — waiting %s before retry", acct.Label, retryAfter)
			s.sleep(retryAfter)
			if r.Context().Err() != nil { // client disconnected during the wait
				return
			}
			continue
		}

		s.relay(w, resp)
		return
	}
}

// writeExhausted reports that every account is rate-limited, mirroring
// teamclaude's structured rate_limit_error response.
func (s *Server) writeExhausted(w http.ResponseWriter) {
	w.Header().Set("retry-after", "60")
	s.writeError(w, http.StatusTooManyRequests, "rate_limit_error",
		fmt.Sprintf("All %d accounts exhausted. Retry in 60s.", s.mgr.Len()))
}

// writeError writes an Anthropic-style error envelope.
func (s *Server) writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
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
// Returns false only when the token is expired/near-expiry AND the refresh
// failed (a dead refresh token) — the caller then sidelines the account and
// fails over, mirroring teamclaude's ensureTokenFresh + status==='error' check.
func (s *Server) ensureFreshToken(idx int, now time.Time) bool {
	if !s.mgr.NeedsRefresh(idx, now) {
		return true
	}
	acct := s.mgr.AccountAt(idx)
	tok, err := RefreshToken(s.tokenEndpoint, acct.RefreshToken)
	if err != nil {
		log.Printf("[wisp-deck-proxy] token refresh failed for %q: %v", acct.Label, err)
		s.mgr.MarkErrored(idx)
		return false
	}
	s.mgr.SetTokens(idx, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt)
	if s.accountsDir != "" && acct.Dir != "" {
		path := filepath.Join(s.accountsDir, acct.Dir, ".credentials.json")
		if err := WriteCredentials(path, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt); err != nil {
			log.Printf("[wisp-deck-proxy] could not persist refreshed token for %q: %v", acct.Label, err)
		}
	}
	return true
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
		// Strip accept-encoding so the upstream returns a body our transport can
		// transparently decompress (mirrors teamclaude) — otherwise a forwarded
		// content-encoding would mismatch the decoded bytes we relay.
		if lk == "accept-encoding" {
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
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] {
			continue
		}
		// Our transport may have auto-decompressed the body, so the upstream's
		// content-encoding/content-length no longer describe what we relay.
		if lk == "content-encoding" || lk == "content-length" {
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

// clampRetryAfter parses a Retry-After header (seconds) and bounds it to
// [1, 300]s, defaulting to 60s when missing/invalid — matching teamclaude, so a
// negative or absurd value can't bypass the retry cap.
func clampRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		secs = 60
	}
	if secs < 1 {
		secs = 1
	}
	if secs > 300 {
		secs = 300
	}
	return time.Duration(secs) * time.Second
}
