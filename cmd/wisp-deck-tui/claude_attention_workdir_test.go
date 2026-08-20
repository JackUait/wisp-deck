package main

import (
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
			func(dir string) { directories = append(directories, dir) },
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
			func(dir string) { directories = append(directories, dir) },
		)
		if len(observed) != 1 || observed[0].Status != attention.ClaudeObservedUnknown {
			t.Fatalf("observations = %#v, want one unknown observation", observed)
		}
		if len(directories) != 0 {
			t.Fatalf("directories = %#v, want none", directories)
		}
	})
}
