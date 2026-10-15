package allin

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackuait/wisp-deck/internal/proxy"
)

// refreshSkew starts the refresh this far before the recorded expiry. A token
// that is minutes from expiring outlives the request that swaps it in but not
// the turn it starts, and Claude Code retries an upstream 401 about eleven
// times before it surfaces — so the window has to be wider than a turn.
const refreshSkew = 5 * time.Minute

// ErrSignedOut marks a login whose refresh token is gone or was refused. A
// retry cannot fix it; only a /login run under that login's own config dir can.
var ErrSignedOut = errors.New("allin: the login is signed out")

// oauthCredential is the part of one login's Keychain blob a refresh reads and
// rewrites. Every other key in that blob (subscriptionType, scopes) belongs to
// Claude Code and is preserved untouched by the store's write.
type oauthCredential struct {
	AccessToken  string
	RefreshToken string
	// ExpiresAt is ms since the epoch. Zero means the entry records no expiry,
	// which is served as-is rather than refreshed — see freshToken.
	ExpiresAt int64
}

func (c oauthCredential) expired(now time.Time) bool {
	if c.ExpiresAt == 0 {
		return false
	}
	return now.Add(refreshSkew).UnixMilli() >= c.ExpiresAt
}

// keychainStore is the Keychain seam. lock serializes the refresh across every
// process that shares one login, and its scope is the config dir, so two
// different logins never wait on each other.
type keychainStore interface {
	read(configDir string) (oauthCredential, error)
	write(configDir string, cred oauthCredential) error
	lock(configDir string) (release func(), err error)
}

// freshToken answers the access token a turn should carry, refreshing it first
// when the stored one has expired.
//
// Nothing else refreshes these entries. Claude Code refreshes the login of the
// config dir it is RUNNING under; All-In borrows another login's credential
// without ever starting a process under that dir, so an account nobody has
// opened a pane on simply ages out and every turn routed to it 401s.
func freshToken(
	store keychainStore,
	refresh func(refreshToken string) (oauthCredential, error),
	configDir string,
	now func() time.Time,
) (string, error) {
	cred, err := store.read(configDir)
	if err != nil {
		return "", err
	}
	if !cred.expired(now()) {
		return cred.AccessToken, nil
	}

	release, err := store.lock(configDir)
	if err != nil {
		return "", fmt.Errorf("allin: locking the login for refresh failed: %w", err)
	}
	defer release()

	// Re-read under the lock. A refresh ROTATES the refresh token, so if
	// another pane refreshed while this one waited, the copy read above names a
	// token the server has already retired. Spending it again is how a login
	// dies, so a failed re-read fails the turn rather than fall back to it.
	cred, err = store.read(configDir)
	if err != nil {
		return "", fmt.Errorf("allin: re-reading the login under the refresh lock failed: %w", err)
	}
	stored := cred.RefreshToken
	if kept, ok := unsaved.take(store, configDir, cred); ok {
		cred = kept.oauthCredential
		if !cred.expired(now()) {
			saveLogin(store, configDir, cred, stored)
			return cred.AccessToken, nil
		}
	}
	if !cred.expired(now()) {
		return cred.AccessToken, nil
	}
	if cred.RefreshToken == "" {
		return "", fmt.Errorf("%w: it stores no refresh token", ErrSignedOut)
	}

	fresh, err := refresh(cred.RefreshToken)
	if err != nil {
		var refused *proxy.RefreshError
		if errors.As(err, &refused) && refused.Code == "invalid_grant" {
			return "", fmt.Errorf("%w: the refresh token was refused: %w", ErrSignedOut, err)
		}
		return "", fmt.Errorf("allin: refreshing the login failed: %w", err)
	}
	if fresh.AccessToken == "" {
		return "", errors.New("allin: the refresh returned no access token")
	}
	// A failed write is not a failed turn. The rotation already happened
	// upstream, so refusing here would lose the turn without saving the login.
	saveLogin(store, configDir, fresh, stored)
	return fresh.AccessToken, nil
}

// saveAttempts bounds the write-back retries inside one turn. A pair that
// still fails is kept in this process and saved by a later call.
const saveAttempts = 3

// saveLogin writes a rotated pair back. stored is the refresh token the
// Keychain holds now: a kept pair only stands while that is still so.
func saveLogin(store keychainStore, configDir string, cred oauthCredential, stored string) {
	for range saveAttempts {
		if store.write(configDir, cred) == nil {
			unsaved.forget(store, configDir)
			return
		}
	}
	unsaved.keep(store, configDir, cred, stored)
}

// unsaved holds pairs this process rotated but could not write back. Without
// it the Keychain keeps a refresh token the server has retired, and every
// later refresh of that login fails for good.
var unsaved = &unsavedLogins{byKey: map[unsavedKey]keptLogin{}}

type unsavedKey struct {
	store     keychainStore
	configDir string
}

type keptLogin struct {
	oauthCredential
	spent string
}

type unsavedLogins struct {
	mu    sync.Mutex
	byKey map[unsavedKey]keptLogin
}

func (u *unsavedLogins) keep(store keychainStore, configDir string, cred oauthCredential, spent string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.byKey[unsavedKey{store, configDir}] = keptLogin{oauthCredential: cred, spent: spent}
}

func (u *unsavedLogins) forget(store keychainStore, configDir string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.byKey, unsavedKey{store, configDir})
}

// take answers the kept pair while stored still names the token it replaced,
// or names none: Claude Code clears a refused token, and that is not a newer
// login. Anything else in the Keychain is newer (a /login, or Claude Code's own
// refresh), so the kept pair is dropped and never written over it.
func (u *unsavedLogins) take(store keychainStore, configDir string, stored oauthCredential) (keptLogin, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	key := unsavedKey{store, configDir}
	kept, ok := u.byKey[key]
	if !ok {
		return keptLogin{}, false
	}
	if stored.RefreshToken != kept.spent && stored.RefreshToken != "" {
		delete(u.byKey, key)
		return keptLogin{}, false
	}
	return kept, true
}
