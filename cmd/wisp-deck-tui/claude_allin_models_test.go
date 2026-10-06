package main

import (
	"os"
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

func TestCodexModelLister_asks_the_bridge_codex(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	codex := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf '%s ' \"$@\" > " + argsFile + "\n" +
		`printf '%s' '{"models":[{"slug":"gpt-6.1-sol","visibility":"list","context_window":272000},{"slug":"gpt-reserve","visibility":"hide"}]}'` + "\n"
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	list := codexModelLister(codex)
	if list == nil {
		t.Fatal("lister = nil for an absolute codex path")
	}
	models, err := list()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-6.1-sol" || models[0].Context != 272000 {
		t.Fatalf("models = %+v", models)
	}
	if args, _ := os.ReadFile(argsFile); string(args) != "debug models " {
		t.Fatalf("argv = %q, want \"debug models \"", args)
	}
	if codexModelLister("") != nil {
		t.Fatal("no codex path must leave the lister unset, so the cache file is read")
	}
}
