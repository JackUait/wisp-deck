package models

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IsThrowawayWorktreePath reports whether a worktree lives where only agents
// put scratch checkouts: a Claude scratchpad, or a repo's .claude/worktrees.
// Other places are left alone even when detached — some tools keep a detached
// base checkout there on purpose.
func IsThrowawayWorktreePath(path string) bool {
	for _, root := range []string{"/private/tmp/claude-", "/tmp/claude-"} {
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	marker := string(filepath.Separator) + filepath.Join(".claude", "worktrees") + string(filepath.Separator)
	i := strings.Index(path, marker)
	return i >= 0 && len(path) > i+len(marker)
}

type sweepCandidate struct {
	path string
	head string
}

// SweepDetachedWorktrees removes the project's throwaway detached worktrees
// that can go without losing anything, then prunes registrations whose folder
// is already gone. It returns the removed paths.
//
// A worktree is removed only when it is detached, unlocked, in a throwaway
// root, untouched for minIdle, its HEAD is on some ref, and no process has its
// cwd inside it. The final `git worktree remove` runs without --force, so git
// itself refuses one with uncommitted or untracked files. Ignored files go
// with it.
func SweepDetachedWorktrees(projectPath string, minIdle time.Duration) []string {
	out, err := exec.Command("git", "-C", projectPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}

	// The cheap checks run first: lsof costs a quarter CPU-second, so it only
	// runs when something is left to check.
	var candidates []sweepCandidate
	for _, c := range parseSweepCandidates(string(out)) {
		if !IsThrowawayWorktreePath(c.path) || !idleFor(c.path, minIdle) {
			continue
		}
		refs, err := exec.Command("git", "-C", projectPath, "for-each-ref", "--contains", c.head, "--count=1", "--format=%(refname)", "refs/heads", "refs/remotes", "refs/tags").Output()
		if err != nil || strings.TrimSpace(string(refs)) == "" {
			continue
		}
		candidates = append(candidates, c)
	}

	var removed []string
	if len(candidates) > 0 {
		cwds, ok := processCwds()
		for _, c := range candidates {
			// Without a process table nothing proves a worktree is unused.
			if !ok || inUse(c.path, cwds) {
				continue
			}
			if RemoveWorktree(projectPath, c.path, false) == nil {
				removed = append(removed, c.path)
			}
		}
	}

	_ = exec.Command("git", "-C", projectPath, "worktree", "prune").Run()
	return removed
}

// parseSweepCandidates returns the detached, unlocked, non-main worktrees of
// `git worktree list --porcelain` output.
func parseSweepCandidates(output string) []sweepCandidate {
	var result []sweepCandidate
	blocks := strings.Split(strings.TrimRight(output, "\n"), "\n\n")
	for i, block := range blocks {
		if i == 0 {
			continue
		}
		var c sweepCandidate
		detached, locked := false, false
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				c.path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "HEAD "):
				c.head = strings.TrimPrefix(line, "HEAD ")
			case line == "detached":
				detached = true
			case line == "locked" || strings.HasPrefix(line, "locked "):
				locked = true
			}
		}
		if detached && !locked && c.path != "" && c.head != "" {
			result = append(result, c)
		}
	}
	return result
}

// idleFor reports whether nothing git tracks about the worktree changed in the
// last d. An agent's shell does not stay inside a worktree between commands,
// so a fresh clean checkout looks unused to lsof; this wait is what spares it.
func idleFor(wtPath string, d time.Duration) bool {
	paths := []string{wtPath}
	if gitFile, err := os.ReadFile(filepath.Join(wtPath, ".git")); err == nil {
		admin := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(gitFile)), "gitdir:"))
		paths = append(paths, filepath.Join(admin, "HEAD"), filepath.Join(admin, "index"), filepath.Join(admin, "logs", "HEAD"))
	} else {
		return false
	}
	cutoff := time.Now().Add(-d)
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			return false
		}
	}
	return true
}

// processCwds lists every process's working directory. ok is false when lsof
// could not run.
func processCwds() (cwds []string, ok bool) {
	out, err := exec.Command("lsof", "-n", "-P", "-w", "-d", "cwd", "-Fn").Output()
	// lsof exits 1 when some process could not be read, with the rest listed.
	if err != nil && len(out) == 0 {
		return nil, false
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "n") {
			cwds = append(cwds, line[1:])
		}
	}
	return cwds, true
}

// inUse reports whether any cwd is the worktree or inside it. lsof reports
// resolved paths (/private/tmp, not /tmp), so the worktree is resolved too.
func inUse(wtPath string, cwds []string) bool {
	resolved, err := filepath.EvalSymlinks(wtPath)
	if err != nil {
		return true
	}
	for _, cwd := range cwds {
		if cwd == resolved || strings.HasPrefix(cwd, resolved+"/") {
			return true
		}
	}
	return false
}
