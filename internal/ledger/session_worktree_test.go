package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pill is the only way into the switch modal, and it was eligible only on
// another login, agent or subscription. A session with one login, one agent and
// no subscriptions still has its project's other checkouts to switch between —
// without this the worktree rows are unreachable.
func TestSessionPillEligibleWithOnlyWorktrees(t *testing.T) {
	directory := t.TempDir()
	project := filepath.Join(directory, "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git", "worktrees", "feature"), 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude", "project_dir=" + project,
	}, "\n"))

	got, err := NewSessionSource(&recordingProcessRunner{}).Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil {
		t.Fatal("a project with worktrees must show the switch pill")
	}
	if len(got.SwitchOptions) == 0 {
		t.Fatal("the pill must carry switch options or the click is inert")
	}
}

// A linked worktree is itself a checkout with the main tree to go back to: its
// .git is a FILE, not a directory of its own.
func TestSessionPillEligibleInsideALinkedWorktree(t *testing.T) {
	directory := t.TempDir()
	project := filepath.Join(directory, "repo--feature")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: /repo/.git/worktrees/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude", "project_dir=" + project,
	}, "\n"))

	got, err := NewSessionSource(&recordingProcessRunner{}).Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill == nil {
		t.Fatal("a linked worktree must show the switch pill")
	}
}

// The gate must stay closed for an ordinary single-checkout project: a pill
// there opens a modal with exactly one choice — the one already running.
func TestSessionPillStaysHiddenWithoutWorktrees(t *testing.T) {
	directory := t.TempDir()
	project := filepath.Join(directory, "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude", "project_dir=" + project,
	}, "\n"))

	got, err := NewSessionSource(&recordingProcessRunner{}).Load(context.Background(), relaunch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Pill != nil {
		t.Fatalf("single-checkout project showed a pill: %#v", got.Pill)
	}
}

// The gate runs on EVERY ledger refresh tick, so it must decide from stats
// alone — a `git worktree list` per tick would put a subprocess on the pane's
// hot path.
func TestProjectHasWorktreesSpawnsNoProcess(t *testing.T) {
	directory := t.TempDir()
	project := filepath.Join(directory, "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git", "worktrees", "feature"), 0o755); err != nil {
		t.Fatal(err)
	}
	relaunch := writeSessionFixture(t, directory, "relaunch", strings.Join([]string{
		"tool=claude", "tools=claude", "project_dir=" + project,
	}, "\n"))
	runner := &recordingProcessRunner{}

	if _, err := NewSessionSource(runner).Load(context.Background(), relaunch); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call.name+" "+strings.Join(call.args, " "), "worktree") {
			t.Fatalf("worktree detection shelled out: %s %v", call.name, call.args)
		}
	}
}
