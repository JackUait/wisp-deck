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

func TestRotate_switchesAwayFromNearQuotaAccount(t *testing.T) {
	m := NewManager(testAccounts(3), 0.98)
	now := time.Now()
	// Active account (0) is over threshold; account 1 is busiest of the rest,
	// account 2 is idlest — rotation should land on 2.
	m.UpdateQuota(0, hdr("anthropic-ratelimit-unified-5h-utilization", "0.99"))
	m.UpdateQuota(1, hdr("anthropic-ratelimit-unified-5h-utilization", "0.80"))
	m.UpdateQuota(2, hdr("anthropic-ratelimit-unified-5h-utilization", "0.10"))

	idx, switched := m.Rotate(now)
	if !switched {
		t.Fatal("expected a switch")
	}
	if idx != 2 {
		t.Errorf("rotated to %d, want 2 (least utilized)", idx)
	}
	if m.ActiveIndex() != 2 {
		t.Errorf("ActiveIndex = %d, want 2", m.ActiveIndex())
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
