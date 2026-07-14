package ledger

import (
	"context"
	"fmt"
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
func (m *GitMutator) Discard(ctx context.Context, dir string, paths []string) error {
	if m == nil || m.runner == nil {
		return fmt.Errorf("discard: no Git runner configured")
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		_, trackedErr := m.runner.Run(ctx, dir, "ls-files", "--error-unmatch", "--", path)
		args := []string{"clean", "-fq", "--", path}
		if trackedErr == nil {
			args = []string{"restore", "--", path}
		}
		if _, err := m.runner.Run(ctx, dir, args...); err != nil {
			return fmt.Errorf("discard %q: %w", path, err)
		}
	}
	return nil
}
