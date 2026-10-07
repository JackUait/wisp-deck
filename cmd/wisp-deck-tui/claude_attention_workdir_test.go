package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/attention"
)

// One registry poll feeds two of the generation's outputs: the semantic
// attention state, and the sidecar the wisp session follows into a worktree.
func TestClaudeAttentionPublishesTheWorkingDirectoryWithTheAttentionState(t *testing.T) {
	t.Parallel()

	t.Run("a found session publishes both", func(t *testing.T) {
		t.Parallel()
		var observed []attention.ClaudeReducerObservation
		var directories []string
		claudeAttentionPublish(
			attention.ClaudeRegistryStatus{
				PID:            101,
				Status:         "busy",
				StatusIdentity: "100",
				Cwd:            "/tmp/project/.claude/worktrees/feature",
			},
			true,
			func(o attention.ClaudeReducerObservation) { observed = append(observed, o) },
			func(s attention.ClaudeRegistryStatus) { directories = append(directories, s.Cwd) },
		)
		if len(observed) != 1 || observed[0].Status != attention.ClaudeObservedBusy {
			t.Fatalf("observations = %#v, want one busy observation", observed)
		}
		want := []string{"/tmp/project/.claude/worktrees/feature"}
		if len(directories) != 1 || directories[0] != want[0] {
			t.Fatalf("directories = %#v, want %#v", directories, want)
		}
	})

	// Attention has an answer for "we could not read the registry" — unknown.
	// The working directory does not: the session must keep following the last
	// directory it actually observed.
	t.Run("an unfound session publishes no directory", func(t *testing.T) {
		t.Parallel()
		var observed []attention.ClaudeReducerObservation
		var directories []string
		claudeAttentionPublish(
			attention.ClaudeRegistryStatus{Cwd: "/tmp/stale"},
			false,
			func(o attention.ClaudeReducerObservation) { observed = append(observed, o) },
			func(s attention.ClaudeRegistryStatus) { directories = append(directories, s.Cwd) },
		)
		if len(observed) != 1 || observed[0].Status != attention.ClaudeObservedUnknown {
			t.Fatalf("observations = %#v, want one unknown observation", observed)
		}
		if len(directories) != 0 {
			t.Fatalf("directories = %#v, want none", directories)
		}
	})
}

// The registry record keeps the launch directory when a Bash cd moves Claude
// into another worktree; the published directory has to come from the
// transcript, which records the move.
func TestClaudeWorkdirPublisherFollowsTheTranscript(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	main := filepath.Join(root, "repo")
	worktree := filepath.Join(main, ".claude", "worktrees", "feature")
	if err := os.MkdirAll(filepath.Join(main, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "config")
	var directories []string
	publish := newClaudeWorkdirPublisher(configDir, func(dir string) { directories = append(directories, dir) })
	transcript := filepath.Join(configDir, "projects", "p", "sid.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + worktree + `"}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	publish(attention.ClaudeRegistryStatus{SessionID: "sid", Cwd: main})
	if len(directories) != 1 || directories[0] != worktree {
		t.Fatalf("directories = %#v, want [%q]", directories, worktree)
	}
}
