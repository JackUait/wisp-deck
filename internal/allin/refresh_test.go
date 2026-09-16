package allin

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKeychain stands in for the real Keychain. Its lock is a real mutex, so
// the concurrency test exercises the same double-check the cross-process file
// lock exists for.
type fakeKeychain struct {
	mu       sync.Mutex
	gate     sync.Mutex
	cred     oauthCredential
	reads    int
	writes   int
	locks    int
	readErr  error
	writeErr error
	lockErr  error
}

func (k *fakeKeychain) read(string) (oauthCredential, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reads++
	if k.readErr != nil {
		return oauthCredential{}, k.readErr
	}
	return k.cred, nil
}

func (k *fakeKeychain) write(_ string, cred oauthCredential) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.writes++
	if k.writeErr != nil {
		return k.writeErr
	}
	k.cred = cred
	return nil
}

func (k *fakeKeychain) lock(string) (func(), error) {
	if k.lockErr != nil {
		return nil, k.lockErr
	}
	k.gate.Lock()
	k.mu.Lock()
	k.locks++
	k.mu.Unlock()
	return func() { k.gate.Unlock() }, nil
}

func at(unixMillis int64) func() time.Time {
	return func() time.Time { return time.UnixMilli(unixMillis) }
}

func TestFreshToken_serves_a_live_token_without_refreshing(t *testing.T) {
	store := &fakeKeychain{cred: oauthCredential{
		AccessToken:  "live",
		RefreshToken: "rt",
		ExpiresAt:    time.UnixMilli(1_000_000_000_000).Add(time.Hour).UnixMilli(),
	}}
	refreshed := false
	token, err := freshToken(store, func(string) (oauthCredential, error) {
		refreshed = true
		return oauthCredential{}, nil
	}, "/cfg", at(1_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if token != "live" {
		t.Fatalf("token = %q, want live", token)
	}
	if refreshed {
		t.Fatal("refreshed a token that had not expired")
	}
	if store.writes != 0 {
		t.Fatalf("writes = %d, want 0", store.writes)
	}
}

func TestFreshToken_refreshes_an_expired_token(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{
		AccessToken:  "stale",
		RefreshToken: "rt-old",
		ExpiresAt:    now - int64(48*time.Hour/time.Millisecond),
	}}
	token, err := freshToken(store, func(refreshToken string) (oauthCredential, error) {
		if refreshToken != "rt-old" {
			t.Fatalf("refreshed with %q", refreshToken)
		}
		return oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}, nil
	}, "/cfg", at(now))
	if err != nil {
		t.Fatal(err)
	}
	if token != "fresh" {
		t.Fatalf("token = %q, want fresh", token)
	}
}

// A token that is still valid but about to expire is refreshed now, so a turn
// that starts just under the wire does not 401 mid-stream.
func TestFreshToken_refreshes_ahead_of_the_expiry(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{
		AccessToken:  "nearly",
		RefreshToken: "rt",
		ExpiresAt:    now + int64(refreshSkew/time.Millisecond) - 1,
	}}
	token, err := freshToken(store, func(string) (oauthCredential, error) {
		return oauthCredential{AccessToken: "fresh", RefreshToken: "rt", ExpiresAt: now + 3_600_000}, nil
	}, "/cfg", at(now))
	if err != nil {
		t.Fatal(err)
	}
	if token != "fresh" {
		t.Fatalf("token = %q, want fresh", token)
	}
}

func TestFreshToken_writes_the_refreshed_credential_back(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1}}
	fresh := oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}
	if _, err := freshToken(store, func(string) (oauthCredential, error) { return fresh, nil }, "/cfg", at(now)); err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 {
		t.Fatalf("writes = %d, want 1", store.writes)
	}
	if store.cred != fresh {
		t.Fatalf("stored %+v, want %+v", store.cred, fresh)
	}
}

// The refresh rotates the refresh token, so two panes refreshing the same
// login at once would spend a token the server has already retired. The
// second caller must see the first one's write and use it.
func TestFreshToken_refreshes_once_under_concurrent_callers(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1}}
	var refreshes int64
	var mu sync.Mutex
	refresh := func(string) (oauthCredential, error) {
		mu.Lock()
		refreshes++
		mu.Unlock()
		return oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}, nil
	}
	var wait sync.WaitGroup
	tokens := make([]string, 8)
	for index := range tokens {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			token, err := freshToken(store, refresh, "/cfg", at(now))
			if err != nil {
				t.Error(err)
				return
			}
			tokens[index] = token
		}(index)
	}
	wait.Wait()
	if refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes)
	}
	for index, token := range tokens {
		if token != "fresh" {
			t.Fatalf("caller %d got %q, want fresh", index, token)
		}
	}
}

func TestFreshToken_reports_a_login_with_no_refresh_token(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", ExpiresAt: now - 1}}
	_, err := freshToken(store, func(string) (oauthCredential, error) {
		t.Fatal("refreshed without a refresh token")
		return oauthCredential{}, nil
	}, "/cfg", at(now))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.HasPrefix(err.Error(), "allin: ") {
		t.Fatalf("error %q must carry the package prefix", err)
	}
}

func TestFreshToken_reports_a_failed_refresh(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: now - 1}}
	_, err := freshToken(store, func(string) (oauthCredential, error) {
		return oauthCredential{}, errors.New("token refresh failed (400): invalid_grant")
	}, "/cfg", at(now))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error %q must carry the upstream reason", err)
	}
}

// The rotation has already happened upstream by the time the write fails, so
// refusing the turn would lose the turn without saving the login.
func TestFreshToken_serves_the_turn_when_the_write_back_fails(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{
		cred:     oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1},
		writeErr: errors.New("keychain is locked"),
	}
	token, err := freshToken(store, func(string) (oauthCredential, error) {
		return oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}, nil
	}, "/cfg", at(now))
	if err != nil {
		t.Fatal(err)
	}
	if token != "fresh" {
		t.Fatalf("token = %q, want fresh", token)
	}
}

// An entry that records no expiry is the shape Claude Code wrote before it
// stored one. Refreshing it on every request would rotate the refresh token
// on every request, so it is served as-is.
func TestFreshToken_serves_an_entry_that_records_no_expiry(t *testing.T) {
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "undated", RefreshToken: "rt"}}
	token, err := freshToken(store, func(string) (oauthCredential, error) {
		t.Fatal("refreshed an entry with no recorded expiry")
		return oauthCredential{}, nil
	}, "/cfg", at(1_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if token != "undated" {
		t.Fatalf("token = %q", token)
	}
}

func TestFreshToken_reports_an_unreadable_entry(t *testing.T) {
	store := &fakeKeychain{readErr: errors.New("no such item")}
	if _, err := freshToken(store, nil, "/cfg", at(1)); err == nil {
		t.Fatal("want an error")
	}
}

// Resolve turns every credential failure into ErrStaleAccount, which proxy.go
// reports as 400. A refresh failure must land there too: Claude Code retries a
// 401 about eleven times, and an unrefreshable login fails the same way every
// time.
func TestResolve_reports_an_unrefreshable_account_as_stale(t *testing.T) {
	env := rosterEnv(t)
	resolver := NewResolver(env)
	resolver.Token = func(string) (string, error) {
		return "", errors.New("allin: refreshing the login failed: invalid_grant")
	}
	_, err := resolver.Resolve(Target{Kind: KindAccount, Source: "personal", Model: "claude-opus-5"})
	if !errors.Is(err, ErrStaleAccount) {
		t.Fatalf("err = %v, want ErrStaleAccount", err)
	}
}
