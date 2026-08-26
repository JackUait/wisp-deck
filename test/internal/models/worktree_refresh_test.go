package models_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// The menu re-detects worktrees on a background tick while View reads the
// project list, so detection must return fresh data keyed by path instead of
// writing into the projects slice the way PopulateWorktrees does.
func TestDetectWorktreesFor_keysResultsByPath(t *testing.T) {
	dir := t.TempDir()
	gitMock := `#!/bin/bash
printf 'worktree %s\nHEAD abc123\nbranch refs/heads/main\n\nworktree %s-wt\nHEAD def456\nbranch refs/heads/feature\n\n' "$2" "$2"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(gitMock), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got := models.DetectWorktreesFor([]string{"/tmp/a", "/tmp/b"})

	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	for _, path := range []string{"/tmp/a", "/tmp/b"} {
		wts := got[path]
		if len(wts) != 1 || wts[0].Branch != "feature" || wts[0].Path != path+"-wt" {
			t.Errorf("%s: got %+v, want one worktree on branch feature", path, wts)
		}
	}
}

// A background refresh must never write through the caller's slice — the UI
// goroutine is reading it at the same time.
func TestDetectWorktreesFor_leavesTheCallersProjectsUntouched(t *testing.T) {
	dir := t.TempDir()
	gitMock := `#!/bin/bash
printf 'worktree %s\nHEAD abc123\nbranch refs/heads/main\n\nworktree %s-wt\nHEAD def456\nbranch refs/heads/feature\n\n' "$2" "$2"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(gitMock), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	projects := []models.Project{{Name: "a", Path: "/tmp/a"}}

	models.DetectWorktreesFor([]string{"/tmp/a"})

	if projects[0].Worktrees != nil {
		t.Errorf("DetectWorktreesFor wrote into the projects slice: %+v", projects[0].Worktrees)
	}
}

// A project directory that git cannot read still needs an entry, otherwise the
// menu cannot tell "no worktrees any more" from "not reported this round".
func TestDetectWorktreesFor_reportsANonGitPathAsEmpty(t *testing.T) {
	tmp := t.TempDir()

	got := models.DetectWorktreesFor([]string{tmp})

	if _, ok := got[tmp]; !ok {
		t.Fatalf("non-git path missing from the result: %+v", got)
	}
	if len(got[tmp]) != 0 {
		t.Errorf("got %d worktrees for a non-git dir, want 0", len(got[tmp]))
	}
}
