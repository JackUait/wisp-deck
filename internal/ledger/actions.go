package ledger

import (
	"context"
	"fmt"
	"strings"
)

// Mutator performs destructive working-tree actions outside the UI loop.
type Mutator interface {
	Discard(context.Context, string, []string) error
}

// GitMutator implements ledger actions with Git commands.
type GitMutator struct {
	runner Runner
}

// NewGitMutator creates a working-tree mutator backed by runner.
func NewGitMutator(runner Runner) *GitMutator {
	return &GitMutator{runner: runner}
}

// Discard restores tracked paths from the index and cleans untracked paths.
// Paths are handled individually so staged-new files remain staged.
//
// The clean needs -ffd, not -f. `git ls-files --others` does not descend into a
// nested repository, so a subagent's .claude/worktrees/<name> checkout arrives
// as ONE row whose path is "<dir>/" — and plain -f removes no directory at all,
// and refuses a nested repository even with -d, exiting 0 either way. Discarding
// those rows reported success and deleted nothing.
func (m *GitMutator) Discard(ctx context.Context, dir string, paths []string) error {
	if m == nil || m.runner == nil {
		return fmt.Errorf("discard: no Git runner configured")
	}
	removedNested := false
	for _, path := range paths {
		if path == "" {
			continue
		}
		_, trackedErr := m.runner.Run(ctx, dir, "ls-files", "--error-unmatch", "--", path)
		args := []string{"clean", "-ffdq", "--", path}
		if trackedErr == nil {
			args = []string{"restore", "--", path}
		} else if strings.HasSuffix(path, "/") {
			removedNested = true
		}
		if _, err := m.runner.Run(ctx, dir, args...); err != nil {
			return fmt.Errorf("discard %q: %w", path, err)
		}
	}
	// Deleting the checkout leaves its registration behind, and the project menu
	// polls `git worktree list` — a leftover is a menu row that launches into a
	// path that no longer exists. prune only drops registrations whose directory
	// is already gone, so it can never unregister a live worktree.
	if removedNested {
		if _, err := m.runner.Run(ctx, dir, "worktree", "prune"); err != nil {
			return fmt.Errorf("discard: unregister removed worktrees: %w", err)
		}
	}
	return nil
}
