package allin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testClaudeLock never sleeps for real: each wait advances the fake clock.
func testClaudeLock(clock *time.Time) claudeLock {
	return claudeLock{
		stale:  60 * time.Second,
		touch:  time.Hour,
		budget: 10 * time.Second,
		now:    func() time.Time { return *clock },
		sleep:  func(d time.Duration) { *clock = clock.Add(d) },
	}
}

func makeHeldLock(t *testing.T, dir string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, claudeRefreshLockName)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeLock_takes_and_releases_the_directory_claude_code_locks(t *testing.T) {
	dir := t.TempDir()
	clock := time.Now()
	release, err := testClaudeLock(&clock).acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".oauth_refresh.lock"))
	if err != nil || !info.IsDir() {
		t.Fatalf("lock dir missing while held: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(dir, ".oauth_refresh.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock dir left after release: %v", err)
	}
}

// A live Claude Code holder is mid-refresh. Refreshing beside it would spend
// the same refresh token twice.
func TestClaudeLock_waits_for_a_live_holder(t *testing.T) {
	dir := t.TempDir()
	clock := time.Now()
	path := makeHeldLock(t, dir, clock)
	lock := testClaudeLock(&clock)
	waits := 0
	lock.sleep = func(d time.Duration) {
		waits++
		clock = clock.Add(d)
		if waits == 2 {
			_ = os.Remove(path)
		}
	}
	release, err := lock.acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if waits != 2 {
		t.Fatalf("waited %d times, want 2 (until the holder let go)", waits)
	}
}

func TestClaudeLock_gives_up_on_a_holder_that_outlasts_the_budget(t *testing.T) {
	dir := t.TempDir()
	clock := time.Now()
	lock := testClaudeLock(&clock)
	path := makeHeldLock(t, dir, clock)
	// A live holder keeps its lock fresh.
	lock.sleep = func(d time.Duration) {
		clock = clock.Add(d)
		_ = os.Chtimes(path, clock, clock)
	}
	if _, err := lock.acquire(dir); !errors.Is(err, errClaudeRefreshing) {
		t.Fatalf("err = %v, want errClaudeRefreshing", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed a live holder's lock: %v", err)
	}
}

// Claude Code treats a lock untouched for 60s as abandoned and takes it over;
// the router must too, or a crashed holder wedges the login.
func TestClaudeLock_takes_over_a_stale_lock(t *testing.T) {
	dir := t.TempDir()
	clock := time.Now()
	makeHeldLock(t, dir, clock.Add(-2*time.Minute))
	lock := testClaudeLock(&clock)
	lock.sleep = func(time.Duration) { t.Fatal("waited on an abandoned lock") }
	release, err := lock.acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// Claude Code's holder rewrites the mtime every 5s; a holder that stops looks
// abandoned after 60s and gets taken over mid-refresh.
func TestClaudeLock_keeps_its_lock_fresh_while_held(t *testing.T) {
	dir := t.TempDir()
	lock := claudeLock{stale: time.Minute, touch: 10 * time.Millisecond, budget: time.Second, now: time.Now, sleep: time.Sleep}
	release, err := lock.acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	path := filepath.Join(dir, claudeRefreshLockName)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(old.Add(time.Minute)) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the held lock was never touched")
}

// replaceLock stands in for a Claude Code process that took the lock over
// after this one stalled past the stale window: same path, a new dir.
func replaceLock(t *testing.T, dir string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, claudeRefreshLockName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return makeHeldLock(t, dir, mtime)
}

func TestClaudeLock_leaves_a_lock_it_no_longer_owns(t *testing.T) {
	dir := t.TempDir()
	clock := time.Now()
	release, err := testClaudeLock(&clock).acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := replaceLock(t, dir, clock)
	release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed the lock another process holds: %v", err)
	}
}

// Claude Code's lockfile treats an mtime it did not write as a compromised
// lock and aborts its refresh mid-flight, which can lose the rotated token.
func TestClaudeLock_never_touches_a_lock_it_no_longer_owns(t *testing.T) {
	dir := t.TempDir()
	lock := claudeLock{stale: time.Minute, touch: 5 * time.Millisecond, budget: time.Second, now: time.Now, sleep: time.Sleep}
	release, err := lock.acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	path := replaceLock(t, dir, old)
	time.Sleep(100 * time.Millisecond)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Fatalf("touched another process's lock: mtime %v, want %v", info.ModTime(), old)
	}
}

func TestClaudeLockDir_is_the_config_dir_or_the_default_claude_dir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := claudeLockDir(""); got != filepath.Join(home, ".claude") {
		t.Fatalf("default login locks %q", got)
	}
	if got := claudeLockDir("/accounts/personal"); got != "/accounts/personal" {
		t.Fatalf("personal login locks %q", got)
	}
}
