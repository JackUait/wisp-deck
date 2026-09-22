package main

import (
	"path/filepath"
	"testing"
)

func TestCodexModelsCache_sits_in_the_codex_home(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	if got, want := codexModelsCache(), filepath.Join(home, ".codex", "models_cache.json"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	custom := t.TempDir()
	t.Setenv("CODEX_HOME", custom)
	if got, want := codexModelsCache(), filepath.Join(custom, "models_cache.json"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
