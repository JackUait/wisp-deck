package proxy

import (
	"net/http"
	"testing"
	"time"
)

func testAccounts(n int) []Account {
	a := make([]Account, n)
	for i := range a {
		a[i] = Account{Label: string(rune('A' + i)), Dir: string(rune('a' + i)), AccessToken: "tok" + itoa(int64(i))}
	}
	return a
}

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func TestUpdateQuota_parsesUnifiedHeaders(t *testing.T) {
	m := NewManager(testAccounts(2), 0.98)
	reset := time.Now().Add(2 * time.Hour).Unix()
	m.UpdateQuota(0, hdr(
		"anthropic-ratelimit-unified-5h-utilization", "0.5",
		"anthropic-ratelimit-unified-7d-utilization", "0.42",
		"anthropic-ratelimit-unified-7d-reset", itoa(reset),
		"anthropic-ratelimit-unified-status", "allowed",
	))
	if got := m.Utilization(0); got < 0.49 || got > 0.51 {
		t.Errorf("Utilization = %v, want ~0.5 (max of 5h/7d)", got)
	}
}

func TestRotate_prefersSoonestWeeklyReset(t *testing.T) {
	// Mirrors teamclaude's _pickBestAvailable: among available accounts, spend
	// the one whose weekly window resets soonest first (it's closest to
	// refreshing), preserving accounts whose windows reset later.
	m := NewManager(testAccounts(3), 0.98)
	now := time.Now()
	soon := now.Add(1 * time.Hour).Unix()
	later := now.Add(10 * time.Hour).Unix()
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99")) // active exhausted
	m.UpdateQuota(1, hdr("anthropic-ratelimit-unified-7d-utilization", "0.50",
		"anthropic-ratelimit-unified-7d-reset", itoa(later)))
	m.UpdateQuota(2, hdr("anthropic-ratelimit-unified-7d-utilization", "0.50",
		"anthropic-ratelimit-unified-7d-reset", itoa(soon)))

	idx, switched := m.Rotate(now)
	if !switched || idx != 2 {
		t.Errorf("got (idx=%d switched=%v), want (2, true) — soonest weekly reset", idx, switched)
	}
}

func TestRotate_prefersUnknownWeeklyToDiscoverIt(t *testing.T) {
	// An account whose weekly quota is not yet known is picked before ones with
	// a known (later) reset, so its quota gets discovered.
	m := NewManager(testAccounts(3), 0.98)
	now := time.Now()
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99")) // active exhausted
	m.UpdateQuota(1, hdr("anthropic-ratelimit-unified-7d-utilization", "0.50",
		"anthropic-ratelimit-unified-7d-reset", itoa(now.Add(1*time.Hour).Unix())))
	// account 2: no weekly reset known → should be preferred to discover it.
	idx, switched := m.Rotate(now)
	if !switched || idx != 2 {
		t.Errorf("got (idx=%d switched=%v), want (2, true) — unknown weekly first", idx, switched)
	}
}

func TestRotate_staysWhenActiveHealthy(t *testing.T) {
	m := NewManager(testAccounts(2), 0.98)
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.10"))
	idx, switched := m.Rotate(time.Now())
	if switched || idx != 0 {
		t.Errorf("got (idx=%d switched=%v), want (0, false)", idx, switched)
	}
}

func TestRotate_skipsThrottledAccounts(t *testing.T) {
	m := NewManager(testAccounts(2), 0.98)
	now := time.Now()
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99")) // active exhausted
	m.MarkThrottled(1, now.Add(time.Hour))                                      // only alternative is throttled
	_, switched := m.Rotate(now)
	if switched {
		t.Errorf("should not switch to a throttled account")
	}
	if m.HasAvailable(now) {
		t.Errorf("no account should be available")
	}
}

func TestRotate_throttleExpires(t *testing.T) {
	m := NewManager(testAccounts(2), 0.98)
	now := time.Now()
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99"))
	m.MarkThrottled(1, now.Add(time.Hour))
	// After the throttle window passes, account 1 is available again.
	later := now.Add(2 * time.Hour)
	idx, switched := m.Rotate(later)
	if !switched || idx != 1 {
		t.Errorf("got (idx=%d switched=%v), want (1, true) after throttle expiry", idx, switched)
	}
}

func TestGetActiveAccount_keepsHealthyCurrent(t *testing.T) {
	m := NewManager(testAccounts(2), 0.98)
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.10"))
	idx, ok := m.GetActiveAccount(nil, time.Now())
	if !ok || idx != 0 {
		t.Errorf("got (idx=%d ok=%v), want (0, true)", idx, ok)
	}
}

func TestGetActiveAccount_excludesTried(t *testing.T) {
	m := NewManager(testAccounts(3), 0.98)
	now := time.Now()
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99")) // current exhausted
	// Exclude account 2 (already tried this request) → must pick 1.
	idx, ok := m.GetActiveAccount(map[int]bool{2: true}, now)
	if !ok || idx != 1 {
		t.Errorf("got (idx=%d ok=%v), want (1, true)", idx, ok)
	}
}

func TestGetActiveAccount_noneAvailable(t *testing.T) {
	m := NewManager(testAccounts(2), 0.98)
	now := time.Now()
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99"))
	m.MarkThrottled(1, now.Add(time.Hour))
	if _, ok := m.GetActiveAccount(nil, now); ok {
		t.Error("expected ok=false when every account is exhausted")
	}
}

func TestNeedsRefresh_flagsSoonToExpireToken(t *testing.T) {
	m := NewManager([]Account{{AccessToken: "t", RefreshToken: "r", ExpiresAt: time.Now().Add(1 * time.Minute).UnixMilli()}}, 0.98)
	if !m.NeedsRefresh(0, time.Now()) {
		t.Error("token expiring in 1m should need refresh")
	}
	m2 := NewManager([]Account{{AccessToken: "t", RefreshToken: "r", ExpiresAt: time.Now().Add(2 * time.Hour).UnixMilli()}}, 0.98)
	if m2.NeedsRefresh(0, time.Now()) {
		t.Error("token valid for 2h should not need refresh")
	}
}
