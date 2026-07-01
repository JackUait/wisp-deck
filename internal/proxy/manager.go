package proxy

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// refreshWindow is how long before expiry a token is considered stale and
// refreshed proactively (mirrors teamclaude's 5-minute threshold).
const refreshWindow = 5 * time.Minute

// quota holds an account's observed unified rate-limit utilization, learned
// passively from anthropic-ratelimit-unified-* response headers.
type quota struct {
	u5h, u7d float64   // utilization 0-1; -1 when unknown
	u5hReset time.Time // zero when unknown
	u7dReset time.Time
	status   string // allowed | allowed_warning | rejected
}

type acctState struct {
	Account
	q              quota
	throttledUntil time.Time
	errored        bool
}

// Manager holds the account pool and chooses the active account, rotating on
// quota exhaustion or throttling. Safe for concurrent use.
type Manager struct {
	mu        sync.Mutex
	accounts  []*acctState
	current   int
	threshold float64
}

// NewManager builds a Manager over the pool with the given switch threshold
// (0-1 utilization at/above which an account is considered near quota).
func NewManager(accounts []Account, threshold float64) *Manager {
	states := make([]*acctState, len(accounts))
	for i, a := range accounts {
		states[i] = &acctState{Account: a, q: quota{u5h: -1, u7d: -1}}
	}
	return &Manager{accounts: states, threshold: threshold}
}

// Len reports the number of accounts in the pool.
func (m *Manager) Len() int { return len(m.accounts) }

// ActiveIndex returns the index of the currently selected account.
func (m *Manager) ActiveIndex() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Active returns a copy of the currently selected account.
func (m *Manager) Active() Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accounts[m.current].Account
}

// AccountAt returns a copy of the account at idx.
func (m *Manager) AccountAt(idx int) Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accounts[idx].Account
}

// UpdateQuota records unified rate-limit utilization from response headers.
func (m *Manager) UpdateQuota(idx int, h http.Header) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx < 0 || idx >= len(m.accounts) {
		return
	}
	q := &m.accounts[idx].q
	if v, ok := parseFloatHeader(h, "anthropic-ratelimit-unified-5h-utilization"); ok {
		q.u5h = v
	}
	if v, ok := parseFloatHeader(h, "anthropic-ratelimit-unified-7d-utilization"); ok {
		q.u7d = v
	}
	if v, ok := parseUnixHeader(h, "anthropic-ratelimit-unified-5h-reset"); ok {
		q.u5hReset = v
	}
	if v, ok := parseUnixHeader(h, "anthropic-ratelimit-unified-7d-reset"); ok {
		q.u7dReset = v
	}
	if s := h.Get("anthropic-ratelimit-unified-status"); s != "" {
		q.status = s
	}
}

// Utilization returns the highest known utilization (0-1) across the account's
// quota dimensions, or 0 when nothing is known yet.
func (m *Manager) Utilization(idx int) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.util(idx)
}

func (m *Manager) util(idx int) float64 {
	q := m.accounts[idx].q
	max := 0.0
	if q.u5h > max {
		max = q.u5h
	}
	if q.u7d > max {
		max = q.u7d
	}
	return max
}

// nearQuota reports whether the account is at/above the switch threshold, or
// upstream has explicitly rejected it.
func (m *Manager) nearQuota(idx int) bool {
	if m.accounts[idx].q.status == "rejected" {
		return true
	}
	return m.util(idx) >= m.threshold
}

// clearExpired drops quota/throttle windows whose reset time has passed so a
// recovered account becomes available again.
func (m *Manager) clearExpired(idx int, now time.Time) {
	a := m.accounts[idx]
	if !a.throttledUntil.IsZero() && now.After(a.throttledUntil) {
		a.throttledUntil = time.Time{}
	}
	if a.q.u5h >= 0 && !a.q.u5hReset.IsZero() && now.After(a.q.u5hReset) {
		a.q.u5h = -1
		a.q.u5hReset = time.Time{}
		a.q.status = ""
	}
	if a.q.u7d >= 0 && !a.q.u7dReset.IsZero() && now.After(a.q.u7dReset) {
		a.q.u7d = -1
		a.q.u7dReset = time.Time{}
	}
}

func (m *Manager) isAvailable(idx int, now time.Time) bool {
	m.clearExpired(idx, now)
	a := m.accounts[idx]
	if a.errored {
		return false
	}
	if !a.throttledUntil.IsZero() && now.Before(a.throttledUntil) {
		return false
	}
	return !m.nearQuota(idx)
}

// HasAvailable reports whether any account can currently serve a request.
func (m *Manager) HasAvailable(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.accounts {
		if m.isAvailable(i, now) {
			return true
		}
	}
	return false
}

// pickBest returns the index of the best available account, skipping any in
// exclude, or -1 if none are available. Selection order mirrors teamclaude's
// _pickBestAvailable: an account with no known weekly reset sorts first (so its
// quota gets discovered), then the account whose weekly window resets soonest —
// that quota is closest to refreshing, so spending it first preserves accounts
// whose windows reset later. Ties break by index.
func (m *Manager) pickBest(exclude map[int]bool, now time.Time) int {
	best := -1
	var bestReset int64
	for i := range m.accounts {
		if exclude[i] || !m.isAvailable(i, now) {
			continue
		}
		// Unknown weekly reset sorts first (math.MinInt64 sentinel).
		reset := int64(math.MinInt64)
		if r := m.accounts[i].q.u7dReset; !r.IsZero() {
			reset = r.Unix()
		}
		if best < 0 || reset < bestReset {
			bestReset = reset
			best = i
		}
	}
	return best
}

// GetActiveAccount selects the account to serve a request, excluding any indices
// already tried this request. Mirrors teamclaude's getActiveAccount(tried): the
// current account is kept while it is available and not excluded; otherwise the
// best available account is chosen. Returns (index, true) or (-1, false) when
// every account is exhausted.
func (m *Manager) GetActiveAccount(exclude map[int]bool, now time.Time) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !exclude[m.current] && m.isAvailable(m.current, now) {
		return m.current, true
	}
	best := m.pickBest(exclude, now)
	if best < 0 {
		return -1, false
	}
	m.current = best
	return best, true
}

// Rotate selects the best account. If the current account is still available it
// stays put; otherwise it switches to the best available account (see pickBest).
// Returns the selected index and whether it changed.
func (m *Manager) Rotate(now time.Time) (int, bool) {
	prev := m.ActiveIndex()
	idx, ok := m.GetActiveAccount(nil, now)
	if !ok {
		return prev, false
	}
	return idx, idx != prev
}

// SelectBest picks the best account up front (e.g. at startup), preferring the
// least-utilized available one but never failing — it leaves current in place
// when nothing is available.
func (m *Manager) SelectBest(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if best := m.pickBest(nil, now); best >= 0 {
		m.current = best
	}
	return m.current
}

// MarkThrottled records that an account is rate-limited until the given time.
func (m *Manager) MarkThrottled(idx int, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= 0 && idx < len(m.accounts) {
		m.accounts[idx].throttledUntil = until
	}
}

// MarkErrored takes an account out of rotation (e.g. a dead refresh token).
func (m *Manager) MarkErrored(idx int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= 0 && idx < len(m.accounts) {
		m.accounts[idx].errored = true
	}
}

// SetTokens updates an account's OAuth tokens after a refresh.
func (m *Manager) SetTokens(idx int, access, refresh string, expiresAt int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx < 0 || idx >= len(m.accounts) {
		return
	}
	a := m.accounts[idx]
	a.AccessToken = access
	if refresh != "" {
		a.RefreshToken = refresh
	}
	a.ExpiresAt = expiresAt
	a.errored = false
}

// NeedsRefresh reports whether the account's access token expires within the
// refresh window (and it has a refresh token to use).
func (m *Manager) NeedsRefresh(idx int, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accounts[idx]
	if a.RefreshToken == "" || a.ExpiresAt == 0 {
		return false
	}
	return now.Add(refreshWindow).UnixMilli() >= a.ExpiresAt
}

func parseFloatHeader(h http.Header, key string) (float64, bool) {
	s := h.Get(key)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseUnixHeader parses a header holding unix seconds into a time.Time.
func parseUnixHeader(h http.Header, key string) (time.Time, bool) {
	s := h.Get(key)
	if s == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}
