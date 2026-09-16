//go:build darwin

package allin

import (
	"os"
	"testing"
	"time"
)

// TestLiveKeychainRefresh drives the real Keychain and the real Anthropic token
// endpoint for one login: it reads the stored entry, refreshes it if expired,
// and re-reads to prove the rotation was written back.
//
// It COSTS the login's refresh token a rotation, so it is env-gated. Run it
// after a change to the refresh path, naming a login whose access token has
// actually expired — a live one is served untouched and proves nothing:
//
//	WISP_DECK_LIVE_KEYCHAIN_E2E=1 \
//	WISP_DECK_LIVE_KEYCHAIN_DIR=~/.config/wisp-deck/claude-accounts/personal \
//	go test ./internal/allin/ -run TestLiveKeychainRefresh -v
func TestLiveKeychainRefresh(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_KEYCHAIN_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_KEYCHAIN_E2E=1 to drive the real Keychain")
	}
	configDir := os.Getenv("WISP_DECK_LIVE_KEYCHAIN_DIR")

	before, err := keychainCLI{}.read(configDir)
	if err != nil {
		t.Fatalf("reading the entry: %v", err)
	}
	t.Logf("before: expiresAt=%s expired=%v",
		time.UnixMilli(before.ExpiresAt).Format(time.RFC3339), before.expired(time.Now()))

	token, err := keychainToken(configDir)
	if err != nil {
		t.Fatalf("keychainToken: %v", err)
	}
	if token == "" {
		t.Fatal("keychainToken returned an empty token")
	}

	after, err := keychainCLI{}.read(configDir)
	if err != nil {
		t.Fatalf("re-reading the entry: %v", err)
	}
	t.Logf("after:  expiresAt=%s", time.UnixMilli(after.ExpiresAt).Format(time.RFC3339))

	if after.expired(time.Now()) {
		t.Fatalf("the stored entry is still expired after a refresh: expiresAt=%d", after.ExpiresAt)
	}
	if after.AccessToken != token {
		t.Fatal("the token handed to the turn is not the one written back")
	}
	if before.expired(time.Now()) && after.RefreshToken == before.RefreshToken {
		t.Log("note: the endpoint returned the same refresh token (no rotation)")
	}
}
