package models_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/models"
)

func TestParseWorktreeListPorcelain(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int // number of non-main worktrees
	}{
		{
			name:   "no worktrees (main only)",
			output: "worktree /Users/jack/wisp-deck\nHEAD abc123\nbranch refs/heads/main\n\n",
			want:   0,
		},
		{
			name: "two additional worktrees",
			output: "worktree /Users/jack/wisp-deck\nHEAD abc123\nbranch refs/heads/main\n\n" +
				"worktree /Users/jack/wt/feature-auth\nHEAD def456\nbranch refs/heads/feature/auth\n\n" +
				"worktree /Users/jack/wt/fix-cleanup\nHEAD 789abc\nbranch refs/heads/fix/cleanup\n\n",
			want: 2,
		},
		{
			name: "worktree with detached HEAD",
			output: "worktree /Users/jack/wisp-deck\nHEAD abc123\nbranch refs/heads/main\n\n" +
				"worktree /Users/jack/wt/detached\nHEAD def456\ndetached\n\n",
			want: 1,
		},
		{
			name:   "empty output",
			output: "",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees := models.ParseWorktreeListPorcelain(tt.output)
			if len(worktrees) != tt.want {
				t.Errorf("got %d worktrees, want %d", len(worktrees), tt.want)
			}
		})
	}
}

func TestParseWorktreeListPorcelain_BranchNames(t *testing.T) {
	output := "worktree /Users/jack/wisp-deck\nHEAD abc123\nbranch refs/heads/main\n\n" +
		"worktree /Users/jack/wt/feature-auth\nHEAD def456\nbranch refs/heads/feature/auth\n\n" +
		"worktree /Users/jack/wt/fix-cleanup\nHEAD 789abc\nbranch refs/heads/fix/cleanup\n\n"

	worktrees := models.ParseWorktreeListPorcelain(output)
	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}
	if worktrees[0].Branch != "feature/auth" {
		t.Errorf("worktree 0 branch: got %q, want %q", worktrees[0].Branch, "feature/auth")
	}
	if worktrees[0].Path != "/Users/jack/wt/feature-auth" {
		t.Errorf("worktree 0 path: got %q, want %q", worktrees[0].Path, "/Users/jack/wt/feature-auth")
	}
	if worktrees[1].Branch != "fix/cleanup" {
		t.Errorf("worktree 1 branch: got %q, want %q", worktrees[1].Branch, "fix/cleanup")
	}
}

func TestParseWorktreeListPorcelain_DetachedHead(t *testing.T) {
	output := "worktree /main\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /detached\nHEAD def456\ndetached\n\n"

	worktrees := models.ParseWorktreeListPorcelain(output)
	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}
	if worktrees[0].Branch != "(detached)" {
		t.Errorf("detached branch: got %q, want %q", worktrees[0].Branch, "(detached)")
	}
}

func TestLoadProjectsWithWorktrees_NonGitDir(t *testing.T) {
	// A temp dir that's not a git repo should produce 0 worktrees
	tmpDir := t.TempDir()
	projects := []models.Project{
		{Name: "no-git", Path: tmpDir},
	}

	models.PopulateWorktrees(projects)

	if len(projects[0].Worktrees) != 0 {
		t.Errorf("expected 0 worktrees for non-git dir, got %d", len(projects[0].Worktrees))
	}
}

func TestParseBranchList(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{
			name:   "local and remote branches",
			output: "  main\n  feature/auth\n  origin/main\n  origin/feature/auth\n  origin/fix/cleanup\n",
			want:   []string{"main", "feature/auth", "origin/fix/cleanup"},
		},
		{
			name:   "deduplicates local+remote same branch",
			output: "  main\n  origin/main\n",
			want:   []string{"main"},
		},
		{
			name:   "strips HEAD pointer",
			output: "  main\n  origin/HEAD\n  origin/main\n",
			want:   []string{"main"},
		},
		{
			name:   "empty output",
			output: "",
			want:   nil,
		},
		{
			name:   "remote-only branch kept with origin/ prefix",
			output: "  origin/feature/new\n",
			want:   []string{"origin/feature/new"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.ParseBranchList(tt.output)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d branches %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("branch[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFilterAvailableBranches(t *testing.T) {
	tests := []struct {
		name       string
		branches   []string
		worktrees  []models.Worktree
		mainBranch string
		want       []string
	}{
		{
			name:     "filters out branches with existing worktrees",
			branches: []string{"main", "feature/auth", "fix/cleanup", "develop"},
			worktrees: []models.Worktree{
				{Path: "/wt/auth", Branch: "feature/auth"},
			},
			mainBranch: "main",
			want:       []string{"fix/cleanup", "develop"},
		},
		{
			name:       "filters out main branch",
			branches:   []string{"main", "feature/new"},
			worktrees:  nil,
			mainBranch: "main",
			want:       []string{"feature/new"},
		},
		{
			name:       "all branches taken returns nil",
			branches:   []string{"main"},
			worktrees:  nil,
			mainBranch: "main",
			want:       nil,
		},
		{
			name:       "no worktrees no main returns all",
			branches:   []string{"feature/a", "feature/b"},
			worktrees:  nil,
			mainBranch: "",
			want:       []string{"feature/a", "feature/b"},
		},
		{
			name:       "filters out master even when main branch is different",
			branches:   []string{"develop", "master", "feature/x"},
			worktrees:  nil,
			mainBranch: "develop",
			want:       []string{"feature/x"},
		},
		{
			name:       "filters out main even when main branch is different",
			branches:   []string{"develop", "main", "feature/x"},
			worktrees:  nil,
			mainBranch: "develop",
			want:       []string{"feature/x"},
		},
		{
			name:       "filters out origin/main and origin/master",
			branches:   []string{"origin/main", "origin/master", "origin/feature/y"},
			worktrees:  nil,
			mainBranch: "",
			want:       []string{"origin/feature/y"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.FilterAvailableBranches(tt.branches, tt.worktrees, tt.mainBranch)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d branches %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("branch[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseMainBranch(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "main branch",
			output: "worktree /Users/jack/wisp-deck\nHEAD abc123\nbranch refs/heads/main\n\n",
			want:   "main",
		},
		{
			name:   "master branch",
			output: "worktree /Users/jack/project\nHEAD abc123\nbranch refs/heads/master\n\n",
			want:   "master",
		},
		{
			name: "with additional worktrees returns first",
			output: "worktree /Users/jack/wisp-deck\nHEAD abc\nbranch refs/heads/main\n\n" +
				"worktree /wt/feature\nHEAD def\nbranch refs/heads/feature\n\n",
			want: "main",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.ParseMainBranch(tt.output)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// PopulateWorktrees runs before the main menu's first paint, so N projects
// must not cost N serial `git worktree list` spawns — detection must run
// concurrently. Four mocked 300ms git calls: serial ≈ 1.2s, concurrent ≈ 0.3s.
func TestPopulateWorktrees_RunsConcurrently(t *testing.T) {
	dir := t.TempDir()
	gitMock := `#!/bin/bash
sleep 0.3
printf 'worktree %s\nHEAD abc123\nbranch refs/heads/main\n\nworktree %s-wt\nHEAD def456\nbranch refs/heads/feature\n\n' "$3" "$3"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(gitMock), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	projects := []models.Project{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
		{Name: "c", Path: "/tmp/c"},
		{Name: "d", Path: "/tmp/d"},
	}
	start := time.Now()
	models.PopulateWorktrees(projects)
	elapsed := time.Since(start)

	if elapsed > 700*time.Millisecond {
		t.Errorf("PopulateWorktrees took %v; expected concurrent detection (< 700ms for 4×300ms)", elapsed)
	}
	for _, p := range projects {
		if len(p.Worktrees) != 1 || p.Worktrees[0].Branch != "feature" {
			t.Errorf("project %s: worktrees not populated correctly: %+v", p.Name, p.Worktrees)
		}
	}
}
