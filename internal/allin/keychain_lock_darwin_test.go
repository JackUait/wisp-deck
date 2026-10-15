//go:build darwin

package allin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Refreshes and Reconcile's moves both lock through keychainCLI, so both must
// exclude a Claude Code process refreshing the same login.
func TestKeychainLock_holds_claude_codes_refresh_lock(t *testing.T) {
	dir := t.TempDir()
	release, err := keychainCLI{}.lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, claudeRefreshLockName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Claude Code's lock is not held: %v", err)
	}
	release()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Claude Code's lock outlived the release: %v", err)
	}
}
