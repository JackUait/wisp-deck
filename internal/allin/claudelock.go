package allin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// claudeRefreshLockName is the lock Claude Code takes inside a config dir while
// it refreshes that dir's login. Decoded from 2.1.300: proper-lockfile with
// lockfilePath <dir>/.oauth_refresh.lock, stale 60000, update 5000. The lock is
// a directory made with mkdir; a holder rewrites its mtime, and one untouched
// past stale is removed with a plain rmdir and taken over.
//
// The router's own flock does not stop Claude Code, so without this a pane
// and the router can both spend one refresh token, and the loser's login dies.
const claudeRefreshLockName = ".oauth_refresh.lock"

var errClaudeRefreshing = errors.New("allin: Claude Code is refreshing this login right now")

type claudeLock struct {
	stale  time.Duration
	touch  time.Duration
	budget time.Duration
	now    func() time.Time
	sleep  func(time.Duration)
}

// defaultClaudeLock waits past Claude Code's 30s refresh timeout, so a holder
// that is mid-refresh finishes first. No response header is written yet, so
// the wait trips no stall watchdog.
var defaultClaudeLock = claudeLock{
	stale:  60 * time.Second,
	touch:  5 * time.Second,
	budget: 35 * time.Second,
	now:    time.Now,
	sleep:  time.Sleep,
}

// claudeLockDir is the dir Claude Code locks for a login: its config dir, or
// ~/.claude for the default login.
func claudeLockDir(configDir string) string {
	if configDir != "" {
		return configDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func (c claudeLock) acquire(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, claudeRefreshLockName)
	deadline := c.now().Add(c.budget)
	for {
		err := os.Mkdir(path, 0o755)
		if err == nil {
			held, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			return c.hold(path, held), nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if c.now().Sub(info.ModTime()) > c.stale {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			continue
		}
		if !c.now().Before(deadline) {
			return nil, errClaudeRefreshing
		}
		c.sleep(time.Second)
	}
}

// hold keeps the lock's mtime fresh until released, as Claude Code's own
// holder does, so a slow refresh is never mistaken for an abandoned one.
//
// It touches and removes only the dir it made. If this process stalls past
// stale, Claude Code takes the path over with a new dir, and its lockfile reads
// an mtime it did not write as a compromised lock and aborts its refresh.
func (c claudeLock) hold(path string, held fs.FileInfo) func() {
	ours := func() bool {
		info, err := os.Stat(path)
		return err == nil && os.SameFile(held, info)
	}
	stop := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		ticker := time.NewTicker(c.touch)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if ours() {
					now := time.Now()
					_ = os.Chtimes(path, now, now)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			done.Wait()
			if ours() {
				_ = os.Remove(path)
			}
		})
	}
}
