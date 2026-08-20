package attention

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func generationDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "generation.abc123")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create generation: %v", err)
	}
	return dir
}

func TestWorkingDirectoryWriterPublishesBesideTheAttentionState(t *testing.T) {
	t.Parallel()

	t.Run("writes the directory to the generation's cwd sidecar", func(t *testing.T) {
		t.Parallel()
		dir := generationDir(t)
		writer := NewWorkingDirectoryWriter(filepath.Join(dir, "state"))
		if err := writer.Publish("/tmp/project/.claude/worktrees/feature"); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "cwd"))
		if err != nil {
			t.Fatalf("read sidecar: %v", err)
		}
		if string(data) != "/tmp/project/.claude/worktrees/feature\n" {
			t.Fatalf("sidecar = %q", data)
		}
	})

	// The watcher follows the agent by reading this file, and absence is what a
	// transient registry miss looks like. Blanking it would snap the session
	// back to a checkout the agent never left.
	t.Run("an unknown directory never overwrites the last known one", func(t *testing.T) {
		t.Parallel()
		dir := generationDir(t)
		writer := NewWorkingDirectoryWriter(filepath.Join(dir, "state"))
		if err := writer.Publish("/tmp/project"); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
		if err := writer.Publish(""); err != nil {
			t.Fatalf("Publish(\"\") error = %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "cwd"))
		if err != nil {
			t.Fatalf("read sidecar: %v", err)
		}
		if string(data) != "/tmp/project\n" {
			t.Fatalf("sidecar = %q, want the last known directory", data)
		}
	})

	// Attention polls several times a second for the whole life of a session;
	// only a move is worth a rename.
	t.Run("an unchanged directory rewrites nothing", func(t *testing.T) {
		t.Parallel()
		dir := generationDir(t)
		sidecar := filepath.Join(dir, "cwd")
		writer := NewWorkingDirectoryWriter(filepath.Join(dir, "state"))
		if err := writer.Publish("/tmp/project"); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
		if err := os.Remove(sidecar); err != nil {
			t.Fatalf("remove sidecar: %v", err)
		}
		if err := writer.Publish("/tmp/project"); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
		if _, err := os.Stat(sidecar); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat sidecar err = %v, want the repeat publish to have written nothing", err)
		}
	})

	// A rotated generation owns a different directory; recreating this one would
	// resurrect a dead session's answer beside no state at all.
	t.Run("a removed generation is stale, never recreated", func(t *testing.T) {
		t.Parallel()
		dir := generationDir(t)
		writer := NewWorkingDirectoryWriter(filepath.Join(dir, "state"))
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("remove generation: %v", err)
		}
		if err := writer.Publish("/tmp/project"); !errors.Is(err, ErrStaleGeneration) {
			t.Fatalf("Publish() error = %v, want ErrStaleGeneration", err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("generation was recreated: %v", err)
		}
	})

	t.Run("a writer without a state file publishes nothing", func(t *testing.T) {
		t.Parallel()
		if err := NewWorkingDirectoryWriter("").Publish("/tmp/project"); err == nil {
			t.Fatal("Publish() error = nil, want a refusal")
		}
	})
}
