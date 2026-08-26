package models

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Worktree represents a git worktree entry.
type Worktree struct {
	Path   string
	Branch string
}

// ParseWorktreeListPorcelain parses the output of `git worktree list --porcelain`
// and returns only non-main worktrees.
func ParseWorktreeListPorcelain(output string) []Worktree {
	if output == "" {
		return nil
	}

	var all []Worktree
	blocks := strings.Split(strings.TrimRight(output, "\n"), "\n\n")

	for _, block := range blocks {
		if block == "" {
			continue
		}
		var wt Worktree
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "worktree ") {
				wt.Path = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch ") {
				ref := strings.TrimPrefix(line, "branch ")
				wt.Branch = strings.TrimPrefix(ref, "refs/heads/")
			} else if line == "detached" {
				wt.Branch = "(detached)"
			}
		}
		all = append(all, wt)
	}

	// First entry is always the main worktree
	if len(all) <= 1 {
		return nil
	}

	// First is main worktree, return the rest
	return all[1:]
}

// PopulateWorktrees runs DetectWorktrees for each project and attaches
// the results to the Worktrees field. Detection runs concurrently: it happens
// before the main menu's first paint, so N projects must cost one git
// round-trip of wall-clock, not N.
func PopulateWorktrees(projects []Project) {
	var wg sync.WaitGroup
	for i := range projects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			projects[i].Worktrees = DetectWorktrees(projects[i].Path)
		}(i)
	}
	wg.Wait()
}

// DetectWorktreesFor detects the worktrees of every path concurrently and
// returns them keyed by path. Every path gets an entry, so a caller can tell
// "no worktrees any more" from "not measured this round".
//
// Unlike PopulateWorktrees it writes nothing back into a Project: the menu
// refreshes on a background tick while the UI goroutine reads the project
// list, and mutating that slice from the detection goroutine is a data race.
func DetectWorktreesFor(paths []string) map[string][]Worktree {
	results := make(map[string][]Worktree, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			found := DetectWorktrees(path)
			mu.Lock()
			results[path] = found
			mu.Unlock()
		}(path)
	}
	wg.Wait()
	return results
}

// DetectWorktrees runs `git worktree list --porcelain` for the given path
// and returns non-main worktrees. Returns nil on any error.
func DetectWorktrees(projectPath string) []Worktree {
	cmd := exec.Command("git", "-C", projectPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return ParseWorktreeListPorcelain(string(out))
}

// ParseBranchList parses the output of `git branch -a --format='%(refname:short)'`
// and returns a deduplicated list. When a branch exists both locally and as
// origin/<name>, only the local name is kept. HEAD refs are removed.
func ParseBranchList(output string) []string {
	if output == "" {
		return nil
	}

	localSet := make(map[string]bool)
	var remoteOnly []string

	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" {
			continue
		}
		if branch == "origin/HEAD" || strings.HasPrefix(branch, "origin/HEAD ") {
			continue
		}
		if strings.HasPrefix(branch, "origin/") {
			localName := strings.TrimPrefix(branch, "origin/")
			if !localSet[localName] {
				remoteOnly = append(remoteOnly, branch)
			}
		} else {
			localSet[branch] = true
		}
	}

	var result []string
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		branch := strings.TrimSpace(line)
		if localSet[branch] {
			result = append(result, branch)
			delete(localSet, branch)
		}
	}
	for _, remote := range remoteOnly {
		localName := strings.TrimPrefix(remote, "origin/")
		found := false
		for _, r := range result {
			if r == localName {
				found = true
				break
			}
		}
		if !found {
			result = append(result, remote)
		}
	}

	return result
}

// ListBranches runs `git branch -a --format='%(refname:short)'` and returns
// a deduplicated branch list. Returns nil on error.
func ListBranches(projectPath string) []string {
	cmd := exec.Command("git", "-C", projectPath, "branch", "-a", "--format=%(refname:short)")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return ParseBranchList(string(out))
}

// FilterAvailableBranches removes branches that already have worktrees and
// the main branch. Returns nil if no branches remain.
func FilterAvailableBranches(branches []string, worktrees []Worktree, mainBranch string) []string {
	taken := make(map[string]bool)
	if mainBranch != "" {
		taken[mainBranch] = true
	}
	for _, wt := range worktrees {
		taken[wt.Branch] = true
	}
	// Always exclude default branch names
	taken["main"] = true
	taken["master"] = true
	taken["origin/main"] = true
	taken["origin/master"] = true

	var result []string
	for _, b := range branches {
		if !taken[b] {
			result = append(result, b)
		}
	}
	return result
}

// DeleteBranch deletes a git branch. Local branches are force-deleted with
// `git branch -D`. Remote branches (origin/ prefix) are deleted with
// `git push origin --delete`.
func DeleteBranch(projectPath, branch string) error {
	if strings.HasPrefix(branch, "origin/") {
		name := strings.TrimPrefix(branch, "origin/")
		cmd := exec.Command("git", "-C", projectPath, "push", "origin", "--delete", name)
		return cmd.Run()
	}
	cmd := exec.Command("git", "-C", projectPath, "branch", "-D", branch)
	return cmd.Run()
}

// ParseMainBranch extracts the branch name of the main worktree (first entry)
// from `git worktree list --porcelain` output. Returns "" if not found.
func ParseMainBranch(output string) string {
	if output == "" {
		return ""
	}
	blocks := strings.Split(strings.TrimRight(output, "\n"), "\n\n")
	if len(blocks) == 0 {
		return ""
	}
	for _, line := range strings.Split(blocks[0], "\n") {
		if strings.HasPrefix(line, "branch ") {
			ref := strings.TrimPrefix(line, "branch ")
			return strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	return ""
}

// AddWorktree runs `git -C projectPath worktree add -b branch wtPath`,
// creating a new branch off HEAD together with its worktree. Failures return
// an error carrying git's stderr.
func AddWorktree(projectPath, wtPath, branch string) error {
	cmd := exec.Command("git", "-C", projectPath, "worktree", "add", "-b", branch, wtPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeDirtyError is returned by RemoveWorktree when the worktree has
// uncommitted changes and force=false.
type WorktreeDirtyError struct {
	Stderr string
}

func (e *WorktreeDirtyError) Error() string {
	return "worktree has uncommitted changes: " + e.Stderr
}

// IsWorktreeDirtyError reports whether err is a WorktreeDirtyError.
func IsWorktreeDirtyError(err error) bool {
	var e *WorktreeDirtyError
	return errors.As(err, &e)
}

// WorktreeLockedError is returned by RemoveWorktree when the worktree is
// locked (e.g. by a stale agent process) and force=false.
type WorktreeLockedError struct {
	Stderr string
}

func (e *WorktreeLockedError) Error() string {
	return "worktree is locked: " + e.Stderr
}

// IsWorktreeLockedError reports whether err is a WorktreeLockedError.
func IsWorktreeLockedError(err error) bool {
	var e *WorktreeLockedError
	return errors.As(err, &e)
}

// RemoveWorktree runs `git -C projectPath worktree remove [--force [--force]] wtPath`.
// When force is true, `--force` is passed twice so that locked worktrees are
// also removed (git requires -f -f to override a lock).
// If force is false and the worktree has uncommitted changes, a *WorktreeDirtyError
// is returned. If it is locked, a *WorktreeLockedError is returned. Other
// failures return a plain error with stderr.
func RemoveWorktree(projectPath, wtPath string, force bool) error {
	args := []string{"-C", projectPath, "worktree", "remove"}
	if force {
		// Two --force flags override both "dirty" and "locked" refusal.
		args = append(args, "--force", "--force")
	}
	args = append(args, wtPath)
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	stderr := strings.TrimSpace(string(out))
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "locked") {
		return &WorktreeLockedError{Stderr: stderr}
	}
	if strings.Contains(lower, "modified") || strings.Contains(lower, "untracked") ||
		strings.Contains(lower, "dirty") || strings.Contains(lower, "changes") {
		return &WorktreeDirtyError{Stderr: stderr}
	}
	return fmt.Errorf("git worktree remove: %s", stderr)
}
