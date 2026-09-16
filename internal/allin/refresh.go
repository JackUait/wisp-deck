package allin

import (
	"errors"
	"fmt"
	"time"
)

// refreshSkew starts the refresh this far before the recorded expiry. A token
// that is minutes from expiring outlives the request that swaps it in but not
// the turn it starts, and Claude Code retries an upstream 401 about eleven
// times before it surfaces — so the window has to be wider than a turn.
const refreshSkew = 5 * time.Minute

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
	// token the server has already retired and spending it would fail — and
	// would leave whichever caller lost the race permanently unable to refresh.
	if again, err := store.read(configDir); err == nil {
		if !again.expired(now()) {
			return again.AccessToken, nil
		}
		cred = again
	}
	if cred.RefreshToken == "" {
		return "", errors.New("allin: the login stores no refresh token — sign in again")
	}

	fresh, err := refresh(cred.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("allin: refreshing the login failed: %w", err)
	}
	if fresh.AccessToken == "" {
		return "", errors.New("allin: the refresh returned no access token")
	}
	// A failed write is not a failed turn. The rotation already happened
	// upstream, so refusing here would lose the turn without saving the login.
	_ = store.write(configDir, fresh)
	return fresh.AccessToken, nil
}
