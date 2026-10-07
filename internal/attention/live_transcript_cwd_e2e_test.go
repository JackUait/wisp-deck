package attention

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The tab follows its agent into a worktree by reading the cwd Claude Code
// writes on every transcript entry; the registry record never sees a Bash cd.
// Run after a claude upgrade: if Claude stops recording a cd there, the tab's
// terminal silently stays behind again. Costs one short turn.
func TestLiveClaudeTranscriptRecordsACdIntoAWorktree(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_CLAUDE_CWD_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_CLAUDE_CWD_E2E=1 to drive a real claude")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude not on PATH: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "repo")
	worktree := filepath.Join(main, ".claude", "worktrees", "live")
	git := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q")
	git(main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", "--detach", worktree)
	if err := os.MkdirAll(filepath.Join(worktree, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	sessionID := fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "-p",
		"--session-id", sessionID,
		"--model", "haiku",
		"--allowedTools=Bash",
		"Use the Bash tool once to run exactly: cd "+filepath.Join(worktree, "src")+" && pwd. Then reply with one word: done.")
	cmd.Dir = main
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("claude -p: %v\n%s", err, out)
	}

	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		configDir = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	tracker := &TranscriptWorkdir{ConfigDir: configDir}
	got := tracker.Resolve(ClaudeRegistryStatus{SessionID: sessionID, Cwd: main})
	if got != worktree {
		t.Fatalf("transcript resolves to %q, want the worktree %q: Claude no longer records a Bash cd in the transcript's cwd", got, worktree)
	}
}
