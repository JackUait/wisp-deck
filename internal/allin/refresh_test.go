package allin

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/proxy"
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
	// readsBeforeErr > 0 lets that many reads succeed before readErr applies.
	readsBeforeErr int
	// writeFailures fails that many writes before the next one lands.
	writeFailures int
}

func (k *fakeKeychain) read(string) (oauthCredential, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reads++
	if k.readErr != nil && k.reads > k.readsBeforeErr {
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
	if k.writeFailures > 0 {
		k.writeFailures--
		return errors.New("security: write failed")
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

// invalid_grant means the refresh token itself is dead: only a new /login on
// that login brings it back, so the router must not advise a retry.
func TestFreshToken_marks_a_refused_refresh_signed_out(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: now - 1}}
	_, err := freshToken(store, func(string) (oauthCredential, error) {
		return oauthCredential{}, &proxy.RefreshError{Status: 400, Code: "invalid_grant", Body: `{"error":"invalid_grant"}`}
	}, "/cfg", at(now))
	if !errors.Is(err, ErrSignedOut) {
		t.Fatalf("err = %v, want ErrSignedOut", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error %q must carry the upstream reason", err)
	}
}

func TestFreshToken_does_not_mark_a_dropped_connection_signed_out(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: now - 1}}
	_, err := freshToken(store, func(string) (oauthCredential, error) {
		return oauthCredential{}, errors.New("dial tcp: i/o timeout")
	}, "/cfg", at(now))
	if err == nil || errors.Is(err, ErrSignedOut) {
		t.Fatalf("err = %v, want a failure that is not ErrSignedOut", err)
	}
}

func TestFreshToken_marks_a_login_with_no_refresh_token_signed_out(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{cred: oauthCredential{AccessToken: "stale", ExpiresAt: now - 1}}
	_, err := freshToken(store, nil, "/cfg", at(now))
	if !errors.Is(err, ErrSignedOut) {
		t.Fatalf("err = %v, want ErrSignedOut", err)
	}
}

// The copy read before the lock may name a refresh token another process has
// already spent. Spending it again is how a login dies, so a failed re-read
// fails the turn instead.
func TestFreshToken_never_spends_the_unlocked_copy_when_the_locked_read_fails(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{
		cred:           oauthCredential{AccessToken: "stale", RefreshToken: "rt-maybe-spent", ExpiresAt: now - 1},
		readErr:        errors.New("security: interrupted"),
		readsBeforeErr: 1,
	}
	_, err := freshToken(store, func(string) (oauthCredential, error) {
		t.Fatal("refreshed with the copy read before the lock")
		return oauthCredential{}, nil
	}, "/cfg", at(now))
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, ErrSignedOut) {
		t.Fatalf("err = %v: a failed read is not a signed-out login", err)
	}
}

func TestFreshToken_retries_a_failed_write_back(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{
		cred:          oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1},
		writeFailures: 2,
	}
	fresh := oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}
	if _, err := freshToken(store, func(string) (oauthCredential, error) { return fresh, nil }, "/cfg", at(now)); err != nil {
		t.Fatal(err)
	}
	if store.cred != fresh {
		t.Fatalf("stored %+v, want %+v", store.cred, fresh)
	}
}

// The server retired rt-old the moment it answered. If the new pair is not
// saved, refreshing again with rt-old fails forever, so this process keeps the
// pair, serves it, and saves it on a later call.
func TestFreshToken_keeps_a_rotated_login_it_could_not_save(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{
		cred:     oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1},
		writeErr: errors.New("security: write failed"),
	}
	fresh := oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}
	refreshes := 0
	refresh := func(string) (oauthCredential, error) {
		refreshes++
		return fresh, nil
	}
	if _, err := freshToken(store, refresh, "/cfg", at(now)); err != nil {
		t.Fatal(err)
	}
	token, err := freshToken(store, refresh, "/cfg", at(now+1000))
	if err != nil {
		t.Fatal(err)
	}
	if token != "fresh" || refreshes != 1 {
		t.Fatalf("token %q after %d refreshes, want the kept pair after 1", token, refreshes)
	}
	store.writeErr = nil
	if _, err := freshToken(store, refresh, "/cfg", at(now+2000)); err != nil {
		t.Fatal(err)
	}
	if store.cred != fresh {
		t.Fatalf("stored %+v, want the kept pair saved", store.cred)
	}
}

// A /login or Claude Code's own refresh may land a newer pair while one is
// kept. The kept pair must never overwrite it.
func TestFreshToken_drops_a_kept_login_once_the_keychain_holds_a_newer_one(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{
		cred:     oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1},
		writeErr: errors.New("security: write failed"),
	}
	refresh := func(string) (oauthCredential, error) {
		return oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}, nil
	}
	if _, err := freshToken(store, refresh, "/cfg", at(now)); err != nil {
		t.Fatal(err)
	}
	relogin := oauthCredential{AccessToken: "relogin", RefreshToken: "rt-relogin", ExpiresAt: now - 1}
	store.cred, store.writeErr = relogin, nil
	token, err := freshToken(store, func(rt string) (oauthCredential, error) {
		if rt != "rt-relogin" {
			t.Fatalf("refreshed with %q, want the newer login's token", rt)
		}
		return oauthCredential{AccessToken: "relogin-2", RefreshToken: "rt-relogin-2", ExpiresAt: now + 3_600_000}, nil
	}, "/cfg", at(now+1000))
	if err != nil {
		t.Fatal(err)
	}
	if token != "relogin-2" {
		t.Fatalf("token = %q, want the newer login refreshed", token)
	}
}

// Claude Code clears the stored refresh token after one is refused. An empty
// slot is not a newer login, so the kept pair is still the only live one.
func TestFreshToken_keeps_its_pair_when_claude_code_clears_the_spent_token(t *testing.T) {
	now := int64(1_000_000_000_000)
	store := &fakeKeychain{
		cred:     oauthCredential{AccessToken: "stale", RefreshToken: "rt-old", ExpiresAt: now - 1},
		writeErr: errors.New("security: write failed"),
	}
	fresh := oauthCredential{AccessToken: "fresh", RefreshToken: "rt-new", ExpiresAt: now + 3_600_000}
	if _, err := freshToken(store, func(string) (oauthCredential, error) { return fresh, nil }, "/cfg", at(now)); err != nil {
		t.Fatal(err)
	}
	store.cred, store.writeErr = oauthCredential{AccessToken: "stale", ExpiresAt: now - 1}, nil
	token, err := freshToken(store, nil, "/cfg", at(now+1000))
	if err != nil {
		t.Fatal(err)
	}
	if token != "fresh" || store.cred != fresh {
		t.Fatalf("token %q, stored %+v; want the kept pair served and saved", token, store.cred)
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
