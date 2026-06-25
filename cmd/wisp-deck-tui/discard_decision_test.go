package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDiscardDecision records the user's choice for the bash caller: the literal
// "discard" when confirmed, otherwise nothing. An empty path is a no-op.
func TestWriteDiscardDecision_requested_writes_marker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision")
	if err := writeDiscardDecision(path, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("decision file not written: %v", err)
	}
	if string(got) != "discard" {
		t.Errorf("decision = %q, want %q", string(got), "discard")
	}
}

func TestWriteDiscardDecision_not_requested_writes_nothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision")
	if err := writeDiscardDecision(path, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		t.Errorf("no discard should leave no marker, got %q", string(data))
	}
}

func TestWriteDiscardDecision_empty_path_is_noop(t *testing.T) {
	if err := writeDiscardDecision("", true); err != nil {
		t.Errorf("empty path should be a no-op, got error: %v", err)
	}
}

func TestDiffViewCmd_HasDiscardFileFlag(t *testing.T) {
	if diffViewCmd.Flags().Lookup("discard-file") == nil {
		t.Fatal("expected --discard-file flag on diff-view")
	}
}
