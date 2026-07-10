package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDisabledTools_missing_file_is_empty(t *testing.T) {
	got := LoadDisabledTools(filepath.Join(t.TempDir(), "nope"))
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestLoadDisabledTools_reads_one_name_per_line(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disabled-tools")
	if err := os.WriteFile(path, []byte("codex\n\nopencode\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := LoadDisabledTools(path)
	if !got["codex"] || !got["opencode"] || got["claude"] {
		t.Errorf("got %v, want codex+opencode disabled", got)
	}
}

func TestToggleDisabledTool_adds_then_removes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "disabled-tools")

	disabled, err := ToggleDisabledTool(path, "codex")
	if err != nil || !disabled {
		t.Fatalf("first toggle = (%v, %v), want (true, nil)", disabled, err)
	}
	if !LoadDisabledTools(path)["codex"] {
		t.Error("codex should be persisted as disabled")
	}

	disabled, err = ToggleDisabledTool(path, "codex")
	if err != nil || disabled {
		t.Fatalf("second toggle = (%v, %v), want (false, nil)", disabled, err)
	}
	if LoadDisabledTools(path)["codex"] {
		t.Error("codex should be re-enabled")
	}
}

func TestToggleDisabledTool_preserves_other_entries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disabled-tools")
	if _, err := ToggleDisabledTool(path, "opencode"); err != nil {
		t.Fatal(err)
	}
	if _, err := ToggleDisabledTool(path, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := ToggleDisabledTool(path, "opencode"); err != nil {
		t.Fatal(err)
	}
	got := LoadDisabledTools(path)
	if got["opencode"] || !got["codex"] {
		t.Errorf("got %v, want only codex disabled", got)
	}
}
