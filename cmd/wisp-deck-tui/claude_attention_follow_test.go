package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The whole run path, with a stand-in Claude that does what a real one does
// after a Bash cd into a worktree: its registry record keeps the launch
// directory and only its transcript names the worktree. The published
// directory must be the worktree, or the tab's terminal stays behind.
func TestRunClaudeAttentionPublishesTheTranscriptsCheckout(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "repo")
	worktree := filepath.Join(main, ".claude", "worktrees", "feature")
	for _, dir := range []string{filepath.Join(main, ".git"), filepath.Join(worktree, "src")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "config")
	generation := "generation.Follow1"
	stateFile := filepath.Join(root, "attention", generation, "state")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatal(err)
	}

	// procStart must match the kernel's start time for this pid, in the
	// format and timezone Claude Code writes it.
	script := `set -e
mkdir -p "$CFG/sessions" "$CFG/projects/p"
start=$(LC_ALL=C TZ=UTC ps -o lstart= -p $$ | sed 's/^ *//; s/ *$//')
printf '{"type":"user","isSidechain":false,"cwd":"%s"}\n' "$WT/src" > "$CFG/projects/p/sid-follow.jsonl"
printf '{"pid":%d,"sessionId":"sid-follow","cwd":"%s","procStart":"%s","kind":"interactive","status":"busy","updatedAt":1}\n' \
  $$ "$MAIN" "$start" > "$CFG/sessions/$$.json"
i=0
while [ ! -s "$STATE_DIR/cwd" ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done
`
	t.Setenv("CFG", configDir)
	t.Setenv("WT", worktree)
	t.Setenv("MAIN", main)
	t.Setenv("STATE_DIR", filepath.Dir(stateFile))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := runClaudeAttention(ctx, claudeAttentionOptions{
		StateFile:  stateFile,
		Generation: generation,
		ConfigDir:  configDir,
	}, []string{"bash", "-c", script}); err != nil {
		t.Fatalf("runClaudeAttention: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(stateFile), "cwd"))
	if err != nil {
		state, _ := os.ReadFile(stateFile)
		records, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*.json"))
		var dump []string
		for _, record := range records {
			body, _ := os.ReadFile(record)
			dump = append(dump, string(body))
		}
		t.Fatalf("no working directory published: %v\nstate: %s\nrecords: %s", err, state, dump)
	}
	if got := strings.TrimSpace(string(data)); got != worktree {
		t.Fatalf("published %q, want the worktree %q", got, worktree)
	}
}
